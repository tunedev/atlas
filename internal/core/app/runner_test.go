package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

type fakeTool struct {
	name   string
	result any
	err    error
	seen   []map[string]string
}

func (f *fakeTool) Name() string { return f.name }

func (f *fakeTool) Invoke(_ context.Context, with map[string]string) (any, error) {
	f.seen = append(f.seen, with)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakeRegistry map[string]ports.Tool

func (r fakeRegistry) Lookup(name string) (ports.Tool, bool) {
	t, ok := r[name]
	return t, ok
}

func TestRunExecutesStepsInOrderAndRecordsOutput(t *testing.T) {
	first := &fakeTool{name: "a", result: map[string]any{"value": "one"}}
	second := &fakeTool{name: "b", result: map[string]any{"value": "two"}}

	r := app.NewRunner(fakeRegistry{"a": first, "b": second})
	state, err := r.Run(context.Background(), domain.Blueprint{
		Name: "test",
		Steps: []domain.Step{
			{ID: "s1", Tool: "a", With: map[string]string{}},
			{ID: "s2", Tool: "b", With: map[string]string{}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.Outputs()["s1"] == nil || state.Outputs()["s2"] == nil {
		t.Errorf("outputs = %#v", state.Outputs())
	}
}

func TestRunRendersConfigAgainstEarlierOutput(t *testing.T) {
	first := &fakeTool{name: "a", result: map[string]any{"title": "Widget"}}
	second := &fakeTool{name: "b", result: "ok"}

	r := app.NewRunner(fakeRegistry{"a": first, "b": second})
	_, err := r.Run(context.Background(), domain.Blueprint{
		Name: "test",
		Vars: map[string]string{"who": "someone"},
		Steps: []domain.Step{
			{ID: "s1", Tool: "a", With: map[string]string{}},
			{ID: "s2", Tool: "b", With: map[string]string{
				"prompt": "{{ .vars.who }} wants {{ .steps.s1.title }}",
			}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(second.seen) != 1 {
		t.Fatalf("second tool invoked %d times", len(second.seen))
	}
	if got := second.seen[0]["prompt"]; got != "someone wants Widget" {
		t.Errorf("prompt = %q", got)
	}
}

func TestRunAppliesSelectBeforeStoring(t *testing.T) {
	only := &fakeTool{name: "a", result: map[string]any{
		"items": []any{map[string]any{"name": "first"}},
	}}

	r := app.NewRunner(fakeRegistry{"a": only})
	state, err := r.Run(context.Background(), domain.Blueprint{
		Name:  "test",
		Steps: []domain.Step{{ID: "s1", Tool: "a", With: map[string]string{"select": "items.0"}}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	stored, ok := state.Outputs()["s1"].(map[string]any)
	if !ok || stored["name"] != "first" {
		t.Errorf("stored = %#v; select was not applied", state.Outputs()["s1"])
	}
}

func TestRunDoesNotPassSelectToTheTool(t *testing.T) {
	// select is the runner's instruction, not the tool's business.
	only := &fakeTool{name: "a", result: map[string]any{"items": []any{"x"}}}
	r := app.NewRunner(fakeRegistry{"a": only})
	if _, err := r.Run(context.Background(), domain.Blueprint{
		Name:  "test",
		Steps: []domain.Step{{ID: "s1", Tool: "a", With: map[string]string{"select": "items.0"}}},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, leaked := only.seen[0]["select"]; leaked {
		t.Error("select reached the tool")
	}
}

func TestRunFailsOnAnUnknownTool(t *testing.T) {
	r := app.NewRunner(fakeRegistry{})
	_, err := r.Run(context.Background(), domain.Blueprint{
		Name:  "test",
		Steps: []domain.Step{{ID: "s1", Tool: "nope", With: map[string]string{}}},
	})
	if err == nil {
		t.Error("Run succeeded with an unregistered tool")
	}
}

func TestRunStopsAtTheFailingStep(t *testing.T) {
	boom := errors.New("boom")
	bad := &fakeTool{name: "a", err: boom}
	after := &fakeTool{name: "b", result: "unused"}

	r := app.NewRunner(fakeRegistry{"a": bad, "b": after})
	_, err := r.Run(context.Background(), domain.Blueprint{
		Name: "test",
		Steps: []domain.Step{
			{ID: "s1", Tool: "a", With: map[string]string{}},
			{ID: "s2", Tool: "b", With: map[string]string{}},
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom wrapped", err)
	}
	if len(after.seen) != 0 {
		t.Error("a later step ran after a failure")
	}
}
