package app_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestAgentSessionRoundTrips(t *testing.T) {
	docs := newFakeDocs()
	s := app.AgentSession{
		SessionID: "sess-1", Command: "/usr/bin/agent", Args: []string{"--quiet"},
		ProtocolVersion: 1, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	}
	path, err := app.RecordAgentSession(context.Background(), docs, "weather-oslo", s)
	if err != nil {
		t.Fatal(err)
	}
	if path != "agent-sessions/weather-oslo.json" {
		t.Errorf("path %q", path)
	}
	body := string(docs.put[path])
	for _, key := range []string{`"session_id"`, `"command"`, `"args"`, `"protocol_version"`, `"created_at"`} {
		if !strings.Contains(body, key) {
			t.Errorf("document lacks %s:\n%s", key, body)
		}
	}
	got, err := app.LoadAgentSession(context.Background(), docs, "weather-oslo")
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Errorf("loaded %+v, %v; want %+v", got, err, s)
	}
}

func TestAgentSessionRejectsEmptyInputs(t *testing.T) {
	docs := newFakeDocs()
	if _, err := app.RecordAgentSession(context.Background(), docs, "", app.AgentSession{SessionID: "s"}); err == nil {
		t.Error("recorded under an empty subject id")
	}
	if _, err := app.RecordAgentSession(context.Background(), docs, "x", app.AgentSession{}); err == nil {
		t.Error("recorded an empty session id")
	}
	if _, err := app.LoadAgentSession(context.Background(), docs, "never-recorded"); err == nil || !strings.Contains(err.Error(), "never-recorded") {
		t.Errorf("load of a missing pointer: err = %v; want one naming the subject", err)
	}
}
