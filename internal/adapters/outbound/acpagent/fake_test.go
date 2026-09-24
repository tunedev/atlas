package acpagent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// fakeAgent is the agent side of an ACP conversation over in-memory pipes.
// A test scripts it from its own goroutine; failures use t.Errorf because
// t.Fatal may not be called off the test goroutine.
type fakeAgent struct {
	t   *testing.T
	in  *bufio.Scanner
	out io.WriteCloser
}

func pipes(t *testing.T) (io.Reader, io.WriteCloser, *fakeAgent) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	return clientIn, clientOut, &fakeAgent{t: t, in: bufio.NewScanner(agentIn), out: agentOut}
}

func (f *fakeAgent) next() message {
	if !f.in.Scan() {
		f.t.Errorf("fake agent: client stopped writing: %v", f.in.Err())
		return message{}
	}
	var m message
	if err := json.Unmarshal(f.in.Bytes(), &m); err != nil {
		f.t.Errorf("fake agent: bad line %q", f.in.Text())
	}
	return m
}

func (f *fakeAgent) expect(method string) message {
	m := f.next()
	if m.Method != method {
		f.t.Errorf("fake agent: got %q, want %q", m.Method, method)
	}
	return m
}

func (f *fakeAgent) write(v any) {
	b, _ := json.Marshal(v)
	_, _ = f.out.Write(append(b, '\n'))
}

func (f *fakeAgent) reply(m message, result any) {
	f.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
}

func (f *fakeAgent) update(sessionID string, u map[string]any) {
	f.write(map[string]any{"jsonrpc": "2.0", "method": "session/update",
		"params": map[string]any{"sessionId": sessionID, "update": u}})
}

// ask sends the client a request and returns the client's reply.
func (f *fakeAgent) ask(id int, method string, params any) message {
	f.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return f.next()
}

func (f *fakeAgent) handshake(caps map[string]any) {
	m := f.expect("initialize")
	f.reply(m, map[string]any{"protocolVersion": 1, "agentCapabilities": caps})
}

func (f *fakeAgent) hangUp() { _ = f.out.Close() }

func testConfig() Config {
	return Config{Command: "fake", Stderr: io.Discard, MaxMessageBytes: 1 << 20, SummaryBytes: 200}
}

// startTestClient returns an initialized client talking to a fake agent.
func startTestClient(t *testing.T, cfg Config, caps map[string]any, perm ports.Permission) (*Client, *fakeAgent) {
	t.Helper()
	r, w, fa := pipes(t)
	c := newClient(r, w, cfg, perm)
	go fa.handshake(caps)
	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	t.Cleanup(func() {
		fa.hangUp()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Close(ctx)
	})
	return c, fa
}

// allowAll allows every request.
type allowAll struct{}

func (allowAll) Decide(context.Context, ports.PermissionRequest) (ports.PermissionDecision, error) {
	return ports.PermissionAllow, nil
}
