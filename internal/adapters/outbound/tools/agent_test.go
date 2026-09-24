package tools_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

type scriptedAgent struct {
	gotTask    ports.AgentTask
	gotSession string
	deadline   bool
	events     []ports.AgentEvent
	err        error
}

func (s *scriptedAgent) Do(ctx context.Context, task ports.AgentTask, sessionID string, onEvent func(ports.AgentEvent)) (ports.AgentResult, string, error) {
	s.gotTask, s.gotSession = task, sessionID
	_, s.deadline = ctx.Deadline()
	events := s.events
	if events == nil {
		events = []ports.AgentEvent{
			{Kind: ports.AgentEventMessage, Text: "fragment"},
			{Kind: ports.AgentEventToolCall, Text: "Read notes.txt [completed]"},
			{Kind: ports.AgentEventPlan, Text: "[pending] summarise"},
		}
	}
	for _, ev := range events {
		onEvent(ev)
	}
	sid := sessionID
	if sid == "" {
		sid = "sess-new"
	}
	if s.err != nil {
		return ports.AgentResult{}, sid, s.err
	}
	return ports.AgentResult{Text: "done", StopReason: "end_turn", Unverified: []string{"Write out.txt"}}, sid, nil
}

// ctxDocs refuses a Put whose context has ended, as a store that honours
// its ctx would.
type ctxDocs struct{ ports.Docs }

func (d ctxDocs) Put(ctx context.Context, path string, body []byte, message string) (ports.Revision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return d.Docs.Put(ctx, path, body, message)
}

// outOfTimeAgent starts session sess-late and runs until its turn's
// deadline.
type outOfTimeAgent struct{}

func (outOfTimeAgent) Do(ctx context.Context, _ ports.AgentTask, _ string, _ func(ports.AgentEvent)) (ports.AgentResult, string, error) {
	<-ctx.Done()
	return ports.AgentResult{}, "sess-late", ctx.Err()
}

func newAgentTool(t *testing.T, a ports.Agent, command string) (*tools.Agent, ports.Docs, *bytes.Buffer) {
	t.Helper()
	docs, err := gitdocs.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var progress bytes.Buffer
	return tools.NewAgent(a, docs, &progress, tools.AgentSettings{
		Command: command, Args: []string{"--acp"}, ProtocolVersion: 1, WorkDir: "/work", Timeout: time.Minute,
	}), docs, &progress
}

func TestAgentDoStartsASessionAndRecordsIt(t *testing.T) {
	agent := &scriptedAgent{}
	tool, _, progress := newAgentTool(t, agent, "agent-bin")
	out, err := tool.Invoke(context.Background(), map[string]string{"prompt": "summarise", "subject_id": "notes-1"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["text"] != "done" || res["session_id"] != "sess-new" || res["path"] != "agent-sessions/notes-1.json" {
		t.Errorf("result %v", res)
	}
	if !reflect.DeepEqual(res["unverified"], []string{"Write out.txt"}) {
		t.Errorf("unverified %v", res["unverified"])
	}
	if agent.gotSession != "" || agent.gotTask.WorkDir != "/work" || !agent.deadline {
		t.Errorf("agent got session %q, workdir %q, deadline %v", agent.gotSession, agent.gotTask.WorkDir, agent.deadline)
	}
	want := "agent tool_call: Read notes.txt [completed]\nagent plan: [pending] summarise\n"
	if progress.String() != want {
		t.Errorf("progress %q; want %q", progress.String(), want)
	}
}

func TestAgentDoResumesTheRecordedSession(t *testing.T) {
	agent := &scriptedAgent{}
	tool, _, _ := newAgentTool(t, agent, "agent-bin")
	if _, err := tool.Invoke(context.Background(), map[string]string{"prompt": "a", "subject_id": "notes-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Invoke(context.Background(), map[string]string{"prompt": "b", "subject_id": "notes-1", "resume": "true", "workdir": "/elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if agent.gotSession != "sess-new" || agent.gotTask.WorkDir != "/elsewhere" {
		t.Errorf("resumed with session %q in %q", agent.gotSession, agent.gotTask.WorkDir)
	}
}

func TestAgentDoRefusesAnotherAgentsSession(t *testing.T) {
	first, docs, _ := newAgentTool(t, &scriptedAgent{}, "agent-bin")
	if _, err := first.Invoke(context.Background(), map[string]string{"prompt": "a", "subject_id": "notes-1"}); err != nil {
		t.Fatal(err)
	}
	other := tools.NewAgent(&scriptedAgent{}, docs, &bytes.Buffer{}, tools.AgentSettings{Command: "other-bin", ProtocolVersion: 1, WorkDir: "/work", Timeout: time.Minute})
	_, err := other.Invoke(context.Background(), map[string]string{"prompt": "b", "subject_id": "notes-1", "resume": "true"})
	if err == nil || !strings.Contains(err.Error(), "agent-bin") {
		t.Errorf("err = %v; want one naming the agent that owns the session", err)
	}
}

func TestAgentDoRequiresPromptAndSubject(t *testing.T) {
	tool, _, _ := newAgentTool(t, &scriptedAgent{}, "agent-bin")
	for _, with := range []map[string]string{{"subject_id": "s"}, {"prompt": "p"}} {
		if _, err := tool.Invoke(context.Background(), with); err == nil {
			t.Errorf("Invoke(%v) succeeded", with)
		}
	}
}

func TestAgentDoEscapesControlTextInProgress(t *testing.T) {
	agent := &scriptedAgent{events: []ports.AgentEvent{{Kind: ports.AgentEventToolCall, Text: "Read\x1b[2K\rharmless\nagent plan: fake"}}}
	tool, _, progress := newAgentTool(t, agent, "agent-bin")
	if _, err := tool.Invoke(context.Background(), map[string]string{"prompt": "summarise", "subject_id": "notes-1"}); err != nil {
		t.Fatal(err)
	}
	want := `agent tool_call: Read\x1b[2K\rharmless\nagent plan: fake` + "\n"
	if progress.String() != want {
		t.Errorf("progress %q; want %q", progress.String(), want)
	}
}

func TestAgentDoRecordsTheSessionOfAFailedTurn(t *testing.T) {
	agent := &scriptedAgent{err: errors.New("agent crashed mid-turn")}
	tool, _, _ := newAgentTool(t, agent, "agent-bin")
	_, err := tool.Invoke(context.Background(), map[string]string{"prompt": "a", "subject_id": "notes-1"})
	if err == nil || !strings.Contains(err.Error(), "agent crashed mid-turn") || !strings.HasPrefix(err.Error(), "agent.do: ") {
		t.Fatalf("err = %v; want the agent's error, prefixed", err)
	}

	agent.err = nil
	if _, err := tool.Invoke(context.Background(), map[string]string{"prompt": "b", "subject_id": "notes-1", "resume": "true"}); err != nil {
		t.Fatal(err)
	}
	if agent.gotSession != "sess-new" {
		t.Errorf("resumed with session %q; want the failed turn's sess-new", agent.gotSession)
	}
}

func TestAgentDoRecordsTheSessionOfATurnThatRanOutOfTime(t *testing.T) {
	docs, err := gitdocs.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.NewAgent(outOfTimeAgent{}, ctxDocs{docs}, &bytes.Buffer{}, tools.AgentSettings{
		Command: "agent-bin", ProtocolVersion: 1, WorkDir: "/work", Timeout: 10 * time.Millisecond,
	})
	if _, err := tool.Invoke(context.Background(), map[string]string{"prompt": "a", "subject_id": "notes-1"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want the turn's deadline", err)
	}
	s, err := app.LoadAgentSession(context.Background(), docs, "notes-1")
	if err != nil || s.SessionID != "sess-late" {
		t.Errorf("recorded session %q, %v; want sess-late", s.SessionID, err)
	}
}

func TestAgentDoReportsBothWhenAFailedTurnCannotBeRecorded(t *testing.T) {
	docs, err := gitdocs.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.NewAgent(&scriptedAgent{err: errors.New("agent crashed mid-turn")}, ctxDocs{docs}, &bytes.Buffer{}, tools.AgentSettings{
		Command: "agent-bin", ProtocolVersion: 1, WorkDir: "/work", Timeout: time.Minute,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tool.Invoke(ctx, map[string]string{"prompt": "a", "subject_id": "notes-1"})
	if err == nil || !strings.Contains(err.Error(), "agent crashed mid-turn") || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want both the turn's error and the recording's", err)
	}
}
