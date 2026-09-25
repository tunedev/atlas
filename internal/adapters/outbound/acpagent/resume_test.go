package acpagent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/ports"
)

func TestDoResumesAndDoesNotReplayOldEvents(t *testing.T) {
	c, fa := startTestClient(t, testConfig(), map[string]any{"loadSession": true}, allowAll{})
	dir := t.TempDir()
	go func() {
		m := fa.expect("session/load")
		var p loadSessionParams
		_ = json.Unmarshal(m.Params, &p)
		if p.SessionID != "s1" || p.CWD != dir {
			t.Errorf("session/load params %+v", p)
		}
		fa.update("s1", map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "first ask"}})
		fa.update("s1", chunk("old answer"))
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "a", "title": "Read notes.txt", "status": "completed"})
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "b", "title": "Write summary.txt", "status": "in_progress"})
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c", "title": "Delete draft.txt"})
		fa.reply(m, nil)

		pr := fa.expect("session/prompt")
		fa.update("s1", chunk("new answer"))
		fa.reply(pr, map[string]any{"stopReason": "end_turn"})
	}()

	var events []ports.AgentEvent
	res, sid, err := c.Do(context.Background(), ports.AgentTask{Prompt: "continue", WorkDir: dir}, "s1",
		func(e ports.AgentEvent) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if sid != "s1" || res.Text != "new answer" {
		t.Errorf("session %q, text %q", sid, res.Text)
	}
	if len(events) != 1 {
		t.Errorf("events %+v; replayed history must not be re-emitted", events)
	}
	if want := []string{"Delete draft.txt", "Write summary.txt"}; !reflect.DeepEqual(res.Unverified, want) {
		t.Errorf("unverified %v; want %v", res.Unverified, want)
	}
}

func TestDoWillNotResumeWithoutLoadSession(t *testing.T) {
	c, _ := startTestClient(t, testConfig(), map[string]any{"loadSession": false}, allowAll{})
	_, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "s1", func(ports.AgentEvent) {})
	if err == nil || !strings.Contains(err.Error(), "loadSession") {
		t.Errorf("err = %v; want one naming loadSession", err)
	}
}
