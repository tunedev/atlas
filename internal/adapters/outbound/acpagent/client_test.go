package acpagent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stubConfig(mode string, stderr *bytes.Buffer) Config {
	cfg := testConfig()
	cfg.Command = os.Args[0]
	cfg.Env = append(os.Environ(), "ACPAGENT_STUB="+mode)
	cfg.Stderr = stderr
	return cfg
}

func TestInitializeNegotiatesVersionAndCapabilities(t *testing.T) {
	c, _ := startTestClient(t, testConfig(), map[string]any{"loadSession": true}, allowAll{})
	if c.ProtocolVersion() != 1 || !c.caps.LoadSession {
		t.Errorf("version %d, caps %+v; want 1 with loadSession", c.ProtocolVersion(), c.caps)
	}
}

func TestInitializeRejectsAnUnsupportedVersion(t *testing.T) {
	r, w, fa := pipes(t)
	c := newClient(r, w, testConfig(), allowAll{})
	go func() {
		m := fa.expect("initialize")
		fa.reply(m, map[string]any{"protocolVersion": 2, "agentCapabilities": map[string]any{}})
	}()
	err := c.initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Errorf("err = %v; want one naming version 2", err)
	}
}

func TestInitializeRequiresHTTPMCPWhenToolsAreOffered(t *testing.T) {
	cfg := testConfig()
	cfg.MCP = &MCPServer{Name: "atlas", URL: "http://127.0.0.1:1/mcp"}
	r, w, fa := pipes(t)
	c := newClient(r, w, cfg, allowAll{})
	go fa.handshake(map[string]any{"mcpCapabilities": map[string]any{"http": false}})
	if err := c.initialize(context.Background()); err == nil || !strings.Contains(err.Error(), "MCP") {
		t.Errorf("err = %v; want one naming MCP", err)
	}
}

func TestNewLaunchesAndClosesCleanly(t *testing.T) {
	var stderr bytes.Buffer
	c, err := New(context.Background(), stubConfig("clean", &stderr), allowAll{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	want := filepath.Base(os.Args[0]) + ": stub ready\n"
	if stderr.String() != want {
		t.Errorf("stderr %q; want %q", stderr.String(), want)
	}
}

func TestCloseKillsAnAgentThatIgnoresEOF(t *testing.T) {
	c, err := New(context.Background(), stubConfig("stubborn", &bytes.Buffer{}), allowAll{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = c.Close(ctx)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close err = %v; want it to report the deadline", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("Close took %s; the kill did not happen", time.Since(start))
	}
}

func TestNewFailsLoudlyForAMissingCommand(t *testing.T) {
	cfg := testConfig()
	cfg.Command = filepath.Join(t.TempDir(), "no-such-agent")
	_, err := New(context.Background(), cfg, allowAll{})
	if err == nil || !strings.Contains(err.Error(), "no-such-agent") {
		t.Errorf("err = %v; want one naming the command", err)
	}
}
