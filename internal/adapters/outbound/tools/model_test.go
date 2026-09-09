package tools_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func modelServer(t *testing.T, reply string, captured *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if captured != nil {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, captured)
		}
		w.Header().Set("Content-Type", "application/json")
		out, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
		})
		_, _ = w.Write(out)
	}))
}

func TestModelReturnsTextByDefault(t *testing.T) {
	var sent map[string]any
	srv := modelServer(t, "an answer", &sent)
	defer srv.Close()

	out, err := tools.NewModel(srv.URL, "m", 5*time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"system": "be terse", "user": "a question"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["text"] != "an answer" {
		t.Errorf("out = %#v; text should be reachable as .text", out)
	}
	msgs, ok := sent["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("sent messages = %#v", sent["messages"])
	}
}

func TestModelParsesJSONWhenAsked(t *testing.T) {
	srv := modelServer(t, `{"decision":"yes","reasons":["a","b"]}`, nil)
	defer srv.Close()

	out, err := tools.NewModel(srv.URL, "m", 5*time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u", "expect": "json"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["decision"] != "yes" {
		t.Errorf("out = %#v; expect json should parse the reply into fields", out)
	}
}

func TestModelStripsAFenceBeforeParsingJSON(t *testing.T) {
	// Small local models wrap JSON in a markdown fence routinely. Failing on
	// formatting rather than on substance would be the wrong reason to fail.
	srv := modelServer(t, "```json\n{\"ok\":true}\n```", nil)
	defer srv.Close()

	out, err := tools.NewModel(srv.URL, "m", 5*time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u", "expect": "json"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["ok"] != true {
		t.Errorf("out = %#v", out)
	}
}

func TestModelFailsWhenJSONIsExpectedAndNotReturned(t *testing.T) {
	srv := modelServer(t, "not json at all", nil)
	defer srv.Close()

	if _, err := tools.NewModel(srv.URL, "m", 5*time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u", "expect": "json"}); err == nil {
		t.Error("Invoke succeeded with expect=json and a non-JSON reply")
	}
}

func TestModelFailsWithoutAUserMessage(t *testing.T) {
	srv := modelServer(t, "x", nil)
	defer srv.Close()

	if _, err := tools.NewModel(srv.URL, "m", time.Second, 1<<20).Invoke(context.Background(),
		map[string]string{"system": "s"}); err == nil {
		t.Error("Invoke succeeded with no user message")
	}
}

func TestModelName(t *testing.T) {
	if got := tools.NewModel("http://x", "m", time.Second, 1<<20).Name(); got != "model.complete" {
		t.Errorf("Name = %q", got)
	}
}

func rawBodyServer(body []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func TestModelFailsWhenBodyExceedsMaxBytes(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"ok"}}]}`)
	srv := rawBodyServer(body)
	defer srv.Close()

	if _, err := tools.NewModel(srv.URL, "m", 5*time.Second, int64(len(body)-1)).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u"}); err == nil {
		t.Error("Invoke succeeded with a body larger than maxBytes")
	}
}

func TestModelSucceedsWhenBodyIsExactlyAtMaxBytes(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"ok"}}]}`)
	srv := rawBodyServer(body)
	defer srv.Close()

	out, err := tools.NewModel(srv.URL, "m", 5*time.Second, int64(len(body))).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["text"] != "ok" {
		t.Errorf("out = %#v; a body exactly at maxBytes should still succeed", out)
	}
}
