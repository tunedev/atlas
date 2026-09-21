package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

// stubProvider is a ports.Provider that returns a fixed completion, so the
// tool's tests exercise its own logic rather than an HTTP round trip.
type stubProvider struct {
	text    string
	model   string
	usage   ports.Usage
	latency time.Duration
	err     error
}

func (p stubProvider) Name() string { return "stub" }

func (p stubProvider) Complete(ctx context.Context, prompt ports.Prompt) (ports.Completion, error) {
	if p.err != nil {
		return ports.Completion{}, p.err
	}
	return ports.Completion{
		Text:    p.text,
		Model:   p.model,
		Usage:   p.usage,
		Latency: p.latency,
	}, nil
}

func TestModelReturnsTextByDefault(t *testing.T) {
	p := stubProvider{text: "an answer", model: "m"}
	out, err := tools.NewModel(p).Invoke(context.Background(),
		map[string]string{"system": "be terse", "user": "a question"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["text"] != "an answer" {
		t.Errorf("out = %#v; text should be reachable as .text", out)
	}
}

func TestModelParsesJSONWhenAsked(t *testing.T) {
	p := stubProvider{text: `{"decision":"yes","reasons":["a","b"]}`, model: "m"}
	out, err := tools.NewModel(p).Invoke(context.Background(),
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
	p := stubProvider{text: "```json\n{\"ok\":true}\n```", model: "m"}
	out, err := tools.NewModel(p).Invoke(context.Background(),
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
	p := stubProvider{text: "not json at all", model: "m"}
	if _, err := tools.NewModel(p).Invoke(context.Background(),
		map[string]string{"system": "s", "user": "u", "expect": "json"}); err == nil {
		t.Error("Invoke succeeded with expect=json and a non-JSON reply")
	}
}

func TestModelFailsWithoutAUserMessage(t *testing.T) {
	p := stubProvider{text: "x", model: "m"}
	if _, err := tools.NewModel(p).Invoke(context.Background(),
		map[string]string{"system": "s"}); err == nil {
		t.Error("Invoke succeeded with no user message")
	}
}

func TestModelName(t *testing.T) {
	if got := tools.NewModel(stubProvider{}).Name(); got != "model.complete" {
		t.Errorf("Name = %q", got)
	}
}

func TestModelPropagatesProviderError(t *testing.T) {
	p := stubProvider{err: context.DeadlineExceeded}
	if _, err := tools.NewModel(p).Invoke(context.Background(),
		map[string]string{"user": "u"}); err == nil {
		t.Error("Invoke succeeded although the provider returned an error")
	}
}

func TestTheToolRecordsWhichModelAnswered(t *testing.T) {
	p := stubProvider{text: "hello", model: "some-model"}
	out, err := tools.NewModel(p).Invoke(context.Background(), map[string]string{"user": "hi"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %T, want map[string]any", out)
	}
	if m["model"] != "some-model" {
		t.Errorf("output does not record the model: %+v", m)
	}
}
