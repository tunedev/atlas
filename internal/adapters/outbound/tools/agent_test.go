package tools_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

type scriptedAgent struct {
	gotTask    ports.AgentTask
	gotSession string
	deadline   bool
}

func (s *scriptedAgent) Do(ctx context.Context, task ports.AgentTask, sessionID string, onEvent func(ports.AgentEvent)) (ports.AgentResult, string, error) {
	s.gotTask, s.gotSession = task, sessionID
	_, s.deadline = ctx.Deadline()
	onEvent(ports.AgentEvent{Kind: ports.AgentEventMessage, Text: "fragment"})
	onEvent(ports.AgentEvent{Kind: ports.AgentEventToolCall, Text: "Read notes.txt [completed]"})
	onEvent(ports.AgentEvent{Kind: ports.AgentEventPlan, Text: "[pending] summarise"})
	sid := sessionID
	if sid == "" {
		sid = "sess-new"
	}
	return ports.AgentResult{Text: "done", StopReason: "end_turn", Unverified: []string{"Write out.txt"}}, sid, nil
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
