package acpagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

func chunk(text string) map[string]any {
	return map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}
}

func TestDoStreamsATurnInOrder(t *testing.T) {
	cfg := testConfig()
	cfg.MCP = &MCPServer{Name: "atlas", URL: "http://127.0.0.1:9/mcp", Headers: map[string]string{"Authorization": "Bearer k"}}
	c, fa := startTestClient(t, cfg, map[string]any{"mcpCapabilities": map[string]any{"http": true}}, allowAll{})
	dir := t.TempDir()

	go func() {
		m := fa.expect("session/new")
		var p newSessionParams
		_ = json.Unmarshal(m.Params, &p)
		if p.CWD != dir || len(p.MCPServers) != 1 || p.MCPServers[0].Headers[0].Value != "Bearer k" {
			t.Errorf("session/new params %+v", p)
		}
		fa.reply(m, map[string]any{"sessionId": "s1"})
		pr := fa.expect("session/prompt")
		fa.update("s1", chunk("Hel"))
		fa.update("someone-else", chunk("XX"))
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Read notes.txt", "kind": "read", "status": "pending"})
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed"})
		fa.update("s1", map[string]any{"sessionUpdate": "plan", "entries": []any{
			map[string]any{"content": "read the notes", "status": "completed", "priority": "high"},
			map[string]any{"content": "summarise", "status": "pending", "priority": "high"},
		}})
		fa.update("s1", map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "hmm"}})
		fa.update("s1", chunk("lo"))
		fa.reply(pr, map[string]any{"stopReason": "end_turn"})
	}()

	var events []ports.AgentEvent
	res, sid, err := c.Do(context.Background(), ports.AgentTask{Prompt: "summarise my notes", WorkDir: dir}, "",
		func(e ports.AgentEvent) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello" || res.StopReason != "end_turn" || sid != "s1" {
		t.Errorf("result %+v, session %q", res, sid)
	}
	want := []ports.AgentEvent{
		{Kind: ports.AgentEventMessage, Text: "Hel"},
		{Kind: ports.AgentEventToolCall, Text: "Read notes.txt [pending]"},
		{Kind: ports.AgentEventToolCall, Text: "Read notes.txt [completed]"},
		{Kind: ports.AgentEventPlan, Text: "[completed] read the notes\n[pending] summarise"},
		{Kind: ports.AgentEventMessage, Text: "lo"},
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("events\n got %+v\nwant %+v", events, want)
	}
}

func TestDoRejectsARelativeWorkDir(t *testing.T) {
	c, _ := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	_, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: "notes"}, "", func(ports.AgentEvent) {})
	if err == nil || !strings.Contains(err.Error(), "notes") {
		t.Errorf("err = %v; want one naming the directory", err)
	}
}

func TestDoTimeoutCancelsTheTurnAndKeepsTheAgent(t *testing.T) {
	c, fa := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	cancelled := make(chan string, 1)
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		pr := fa.expect("session/prompt")
		var p cancelParams
		_ = json.Unmarshal(fa.expect("session/cancel").Params, &p)
		cancelled <- p.SessionID
		fa.update("s1", chunk("late"))
		fa.reply(pr, map[string]any{"stopReason": "cancelled"})

		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s2"})
		fa.reply(fa.expect("session/prompt"), map[string]any{"stopReason": "end_turn"})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, sid, err := c.Do(ctx, ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if !errors.Is(err, context.DeadlineExceeded) || sid != "s1" {
		t.Fatalf("Do = %q, %v; want s1 and deadline exceeded", sid, err)
	}
	if got := <-cancelled; got != "s1" {
		t.Errorf("session/cancel for %q; want s1", got)
	}

	_, sid, err = c.Do(context.Background(), ports.AgentTask{Prompt: "again", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if err != nil || sid != "s2" {
		t.Errorf("second Do = %q, %v; the agent should still be usable", sid, err)
	}
}

func TestDoFailsWhenTheAgentDiesMidTurn(t *testing.T) {
	c, fa := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		fa.expect("session/prompt")
		fa.hangUp()
	}()
	_, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if !errors.Is(err, errExited) {
		t.Errorf("err = %v; want errExited", err)
	}
}

func TestARealSubprocessDyingMidTurnFailsTheTurn(t *testing.T) {
	c, err := New(context.Background(), stubConfig("die", &bytes.Buffer{}), allowAll{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	_, _, err = c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if !errors.Is(err, errExited) {
		t.Errorf("err = %v; want errExited", err)
	}
}
