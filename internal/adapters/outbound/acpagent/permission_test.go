package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/termprompt"
	"github.com/tunedev/atlas/internal/core/ports"
)

// recordingPermission answers with decision (after block, when set) and
// records what it was asked and whether its context was cancelled.
type recordingPermission struct {
	mu        sync.Mutex
	decision  ports.PermissionDecision
	err       error
	block     chan struct{}
	asked     []ports.PermissionRequest
	cancelled bool
}

func (p *recordingPermission) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	p.mu.Lock()
	p.asked = append(p.asked, req)
	p.mu.Unlock()
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			p.mu.Lock()
			p.cancelled = true
			p.mu.Unlock()
			return ports.PermissionDeny, ctx.Err()
		}
	}
	return p.decision, p.err
}

var fourOptions = []any{
	map[string]any{"optionId": "yes-once", "name": "Allow", "kind": "allow_once"},
	map[string]any{"optionId": "yes-always", "name": "Always", "kind": "allow_always"},
	map[string]any{"optionId": "no-once", "name": "Reject", "kind": "reject_once"},
	map[string]any{"optionId": "no-always", "name": "Never", "kind": "reject_always"},
}

// askDuringTurn runs a turn in which the agent reports tool call t1, then
// asks permission for it by id alone, and returns the client's reply.
func askDuringTurn(t *testing.T, perm ports.Permission, options []any) permissionResult {
	t.Helper()
	c, fa := startTestClient(t, testConfig(), map[string]any{}, perm)
	replies := make(chan message, 1)
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		pr := fa.expect("session/prompt")
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1",
			"title": "Write notes.txt", "kind": "edit", "rawInput": map[string]any{"path": "notes.txt", "text": strings.Repeat("x", 500)}})
		replies <- fa.ask(100, "session/request_permission", map[string]any{
			"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "t1"}, "options": options})
		fa.reply(pr, map[string]any{"stopReason": "end_turn"})
	}()
	if _, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {}); err != nil {
		t.Fatal(err)
	}
	var res permissionResult
	if err := json.Unmarshal((<-replies).Result, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPermissionDecisionsSelectTheMatchingOption(t *testing.T) {
	cases := []struct {
		perm *recordingPermission
		want permissionOutcome
	}{
		{&recordingPermission{decision: ports.PermissionAllow}, permissionOutcome{Outcome: "selected", OptionID: "yes-once"}},
		{&recordingPermission{decision: ports.PermissionDeny}, permissionOutcome{Outcome: "selected", OptionID: "no-once"}},
		{&recordingPermission{decision: ports.PermissionAsk}, permissionOutcome{Outcome: "selected", OptionID: "no-once"}},
		{&recordingPermission{decision: ports.PermissionAllow, err: errors.New("broken")}, permissionOutcome{Outcome: "selected", OptionID: "no-once"}},
	}
	for _, c := range cases {
		if got := askDuringTurn(t, c.perm, fourOptions).Outcome; got != c.want {
			t.Errorf("decision %q err %v: outcome %+v; want %+v", c.perm.decision, c.perm.err, got, c.want)
		}
	}
}

func TestPermissionFallsBackToAlwaysOrCancelled(t *testing.T) {
	alwaysOnly := []any{fourOptions[1], fourOptions[3]}
	if got := askDuringTurn(t, &recordingPermission{decision: ports.PermissionAllow}, alwaysOnly).Outcome; got.OptionID != "yes-always" {
		t.Errorf("allow with only always options: %+v", got)
	}
	allowOnly := []any{fourOptions[0]}
	if got := askDuringTurn(t, &recordingPermission{decision: ports.PermissionDeny}, allowOnly).Outcome; got.Outcome != "cancelled" {
		t.Errorf("deny with no reject option: %+v; want cancelled", got)
	}
}

func TestPermissionShowsTheReportedToolCallBounded(t *testing.T) {
	perm := &recordingPermission{decision: ports.PermissionAllow}
	askDuringTurn(t, perm, fourOptions)
	if len(perm.asked) != 1 {
		t.Fatalf("asked %d times", len(perm.asked))
	}
	got := perm.asked[0]
	if got.ToolName != "Write notes.txt" || got.Kind != "edit" {
		t.Errorf("request %+v; want the title and kind from the earlier tool_call", got)
	}
	if !strings.HasPrefix(got.Summary, `Write notes.txt {"path":"notes.txt"`) || len(got.Summary) > 200+len("...") {
		t.Errorf("summary %q (%d bytes); want the title and arguments within 200 bytes", got.Summary, len(got.Summary))
	}
}

func TestPendingPermissionIsCancelledWhenTheTurnEnds(t *testing.T) {
	perm := &recordingPermission{decision: ports.PermissionAllow, block: make(chan struct{})}
	c, fa := startTestClient(t, testConfig(), map[string]any{}, perm)
	replies := make(chan message, 1)
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		fa.expect("session/prompt")
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Run tests", "kind": "execute"})
		fa.write(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "session/request_permission",
			"params": map[string]any{"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "t1"}, "options": fourOptions}})
		fa.expect("session/cancel")
		replies <- fa.next()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, _ = c.Do(ctx, ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})

	var res permissionResult
	_ = json.Unmarshal((<-replies).Result, &res)
	if res.Outcome.Outcome != "cancelled" {
		t.Errorf("outcome %+v; want cancelled", res.Outcome)
	}
	perm.mu.Lock()
	defer perm.mu.Unlock()
	if !perm.cancelled {
		t.Error("the permission engine's context was never cancelled")
	}
}

func TestOtherAgentRequestsAreRefused(t *testing.T) {
	_, fa := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	reply := fa.ask(7, "fs/read_text_file", map[string]any{"sessionId": "s1", "path": "/etc/hosts"})
	if reply.Error == nil || reply.Error.Code != -32601 {
		t.Errorf("reply %+v; want -32601", reply)
	}
}

// waitUntilAsked blocks until perm.Decide has been entered at least once, or
// fails the test after a couple of seconds.
func waitUntilAsked(t *testing.T, perm *recordingPermission) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		perm.mu.Lock()
		n := len(perm.asked)
		perm.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("permission engine was never asked")
}

// TestCloseReleasesAPendingPermission pins that Close cancels the context of
// a permission engine the agent is still waiting on, and returns without
// waiting for its answer.
func TestCloseReleasesAPendingPermission(t *testing.T) {
	perm := &recordingPermission{decision: ports.PermissionAllow, block: make(chan struct{})}
	c, fa := startTestClient(t, testConfig(), map[string]any{}, perm)

	agentGone := make(chan struct{})
	go func() {
		defer close(agentGone)
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		fa.expect("session/prompt")
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Run tests", "kind": "execute"})
		fa.write(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "session/request_permission",
			"params": map[string]any{"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "t1"}, "options": fourOptions}})
		// The request is left unanswered. A real agent exits, closing its own
		// stdout, once it notices its stdin (the client's write side) close;
		// fa.in.Scan reproduces that wait without fa.next's t.Errorf on EOF.
		fa.in.Scan()
		fa.hangUp()
	}()
	go c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})

	// Close must not be called before the engine is actually blocked in
	// Decide, or the request could already be answered by the time it runs.
	waitUntilAsked(t, perm)

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := c.Close(closeCtx); err != nil {
		t.Errorf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Close took %s; a pending permission stalled shutdown", elapsed)
	}
	<-agentGone

	perm.mu.Lock()
	defer perm.mu.Unlock()
	if !perm.cancelled {
		t.Error("the permission engine's context was never cancelled by Close")
	}
}

// signalWriter signals each write, dropping the signal when one is pending.
type signalWriter chan struct{}

func (w signalWriter) Write(p []byte) (int, error) {
	select {
	case w <- struct{}{}:
	default:
	}
	return len(p), nil
}

// askedPrompt counts the requests that reached the prompt it wraps.
type askedPrompt struct {
	p     *termprompt.Prompt
	mu    sync.Mutex
	asked int
}

func (a *askedPrompt) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	a.mu.Lock()
	a.asked++
	a.mu.Unlock()
	return a.p.Decide(ctx, req)
}

// TestCloseReleasesAPermissionQueuedBehindAnotherPrompt pins that Close is
// not held up by the terminal prompt when another request already occupies
// it and the human never answers: the queued permission is released by the
// turn's context.
func TestCloseReleasesAPermissionQueuedBehindAnotherPrompt(t *testing.T) {
	in, _ := io.Pipe() // never answers
	shown := make(signalWriter, 1)
	prompt := termprompt.New(in, shown)
	perm := &askedPrompt{p: prompt}
	c, fa := startTestClient(t, testConfig(), map[string]any{}, perm)

	holderCtx, releaseHolder := context.WithCancel(context.Background())
	t.Cleanup(releaseHolder)
	// Owned by the test; stops when releaseHolder runs.
	go func() { _, _ = prompt.Decide(holderCtx, ports.PermissionRequest{ToolName: "notes.read", Kind: "read"}) }()
	<-shown

	agentGone := make(chan struct{})
	go func() {
		defer close(agentGone)
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		fa.expect("session/prompt")
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Run tests", "kind": "execute"})
		fa.write(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "session/request_permission",
			"params": map[string]any{"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "t1"}, "options": fourOptions}})
		fa.in.Scan()
		fa.hangUp()
	}()
	go c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})

	deadline := time.Now().Add(2 * time.Second)
	for {
		perm.mu.Lock()
		n := perm.asked
		perm.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prompt was never asked")
		}
		time.Sleep(time.Millisecond)
	}

	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		closed <- c.Close(ctx)
	}()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited on a permission queued behind another prompt")
	}
	<-agentGone
}
