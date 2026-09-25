package acpagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Config says which agent to run and how to bound it. The agent is any
// command that speaks ACP on its stdio. MCP, when set, is the one server
// through which the agent reaches Atlas's tools. WaitDelay bounds how long
// cmd.Wait, called after a kill, waits for the agent's stdio copy
// goroutines to finish: a descendant that escaped the agent's process
// group and kept a pipe open cannot hold Wait past this delay.
type Config struct {
	Command         string
	Args            []string
	Env             []string
	Stderr          io.Writer
	MaxMessageBytes int
	SummaryBytes    int
	MCP             *MCPServer
	WaitDelay       time.Duration
}

// MCPServer is an HTTP MCP server the agent is told to connect to.
type MCPServer struct {
	Name    string
	URL     string
	Headers map[string]string
}

// Client is one running agent subprocess. It implements ports.Agent.
type Client struct {
	conn   *conn
	stdin  io.Closer
	stdout io.Closer
	cmd    *exec.Cmd
	perm   ports.Permission
	cfg    Config

	version int
	caps    agentCapabilities

	closing   chan struct{}
	closeOnce sync.Once
	closeErr  error

	turnMu sync.Mutex // one turn at a time
	mu     sync.Mutex // guards turn
	turn   *turn
}

// New launches the agent and runs initialize, bounded by ctx. An agent that
// speaks another protocol version, or cannot reach an HTTP MCP server when
// one is configured, fails here, never partway through a turn.
func New(ctx context.Context, cfg Config, perm ports.Permission) (*Client, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = cfg.Env
	cmd.Stderr = &prefixWriter{w: cfg.Stderr, prefix: filepath.Base(cfg.Command) + ": "}
	cmd.WaitDelay = cfg.WaitDelay
	isolate(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acpagent: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acpagent: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acpagent: start %s: %w", cfg.Command, err)
	}

	c := newClient(stdout, stdin, cfg, perm)
	c.cmd = cmd
	c.stdout = stdout
	if err := c.initialize(ctx); err != nil {
		c.kill()
		c.conn.wait()
		_ = cmd.Wait()
		return nil, err
	}
	return c, nil
}

func newClient(r io.Reader, w io.WriteCloser, cfg Config, perm ports.Permission) *Client {
	c := &Client{stdin: w, perm: perm, cfg: cfg, closing: make(chan struct{})}
	c.conn = newConn(r, w, cfg.MaxMessageBytes, c)
	return c
}

func (c *Client) initialize(ctx context.Context) error {
	var res initializeResult
	err := c.conn.call(ctx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      implementation{Name: clientName, Version: clientVersion},
	}, &res)
	if err != nil {
		return fmt.Errorf("acpagent: initialize: %w", err)
	}
	if res.ProtocolVersion != protocolVersion {
		return fmt.Errorf("acpagent: agent speaks protocol version %d; atlas speaks %d", res.ProtocolVersion, protocolVersion)
	}
	if c.cfg.MCP != nil && !res.AgentCapabilities.MCPCapabilities.HTTP {
		return errors.New("acpagent: atlas tools are configured, but the agent cannot reach an HTTP MCP server")
	}
	c.version = res.ProtocolVersion
	c.caps = res.AgentCapabilities
	return nil
}

// ProtocolVersion is the ACP version agreed at initialize.
func (c *Client) ProtocolVersion() int { return c.version }

// Close closes the agent's stdin, which ACP agents treat as shutdown, and
// waits for it to exit. If ctx ends first, the agent's whole process group
// is killed and its stdout closed directly, which ends the read loop even
// when a descendant inherited stdout and kept it open; cfg.WaitDelay then
// bounds reap's cmd.Wait against a descendant that escaped the process
// group and kept stderr open. Close is idempotent: only the first call's
// ctx is used, and every call returns that call's result.
func (c *Client) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		close(c.closing)
		c.closeErr = c.shutdown(ctx)
	})
	return c.closeErr
}

func (c *Client) shutdown(ctx context.Context) error {
	_ = c.stdin.Close()

	stopped := make(chan error, 1)
	// Owned by shutdown; stops once the read loop, request handlers and
	// reap have all finished. On ctx expiry, kill ends the process group
	// and closes stdout, which stops the read loop and request handlers;
	// reap's cmd.Wait is then bounded by cfg.WaitDelay, not by kill, since
	// a descendant that escaped the process group can still hold stderr
	// open past the kill.
	go func() {
		c.conn.wait()
		stopped <- c.reap()
	}()
	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
		c.kill()
		return fmt.Errorf("acpagent: agent did not exit after stdin closed, so it was killed: %w", errors.Join(ctx.Err(), <-stopped))
	}
}

func (c *Client) reap() error {
	if c.cmd == nil {
		return nil
	}
	if err := c.cmd.Wait(); err != nil {
		return fmt.Errorf("acpagent: agent exit: %w", err)
	}
	return nil
}

// kill ends the agent's whole process group and closes its stdout directly,
// before cmd.Wait, which is what unblocks the read loop if a descendant
// inherited stdout and the group kill did not reach it. It does not bound
// cmd.Wait itself: that is cfg.WaitDelay's job, against a descendant that
// escaped the process group and kept stderr open.
func (c *Client) kill() {
	if c.cmd != nil {
		killTree(c.cmd)
	}
	if c.stdout != nil {
		_ = c.stdout.Close()
	}
}

// mcpServers is the MCP list sent with every session/new and session/load.
func (c *Client) mcpServers() []mcpServer {
	if c.cfg.MCP == nil {
		return []mcpServer{}
	}
	names := make([]string, 0, len(c.cfg.MCP.Headers))
	for n := range c.cfg.MCP.Headers {
		names = append(names, n)
	}
	sort.Strings(names)
	headers := make([]httpHeader, len(names))
	for i, n := range names {
		headers[i] = httpHeader{Name: n, Value: c.cfg.MCP.Headers[n]}
	}
	return []mcpServer{{Type: "http", Name: c.cfg.MCP.Name, URL: c.cfg.MCP.URL, Headers: headers}}
}

// prefixWriter writes each line of the agent's stderr with prefix in front.
type prefixWriter struct {
	w      io.Writer
	prefix string
	mid    bool
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	out := make([]byte, 0, len(b)+len(p.prefix))
	for _, ch := range b {
		if !p.mid {
			out = append(out, p.prefix...)
			p.mid = true
		}
		out = append(out, ch)
		if ch == '\n' {
			p.mid = false
		}
	}
	if _, err := p.w.Write(out); err != nil {
		return 0, err
	}
	return len(b), nil
}
