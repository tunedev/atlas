package app_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
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

func recordProgress(events *[]domain.StepEvent) func(domain.StepEvent) {
	return func(e domain.StepEvent) { *events = append(*events, e) }
}

func TestProgressReportsEachStepStartAndDoneInOrder(t *testing.T) {
	reg := fakeRegistry{
		"a": &fakeTool{name: "a", result: map[string]any{"v": "1"}},
		"b": &fakeTool{name: "b", result: map[string]any{"v": "2"}},
	}
	var events []domain.StepEvent
	bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: "one", Tool: "a"}, {ID: "two", Tool: "b"}}}
	if _, err := app.NewRunner(reg).WithProgress(recordProgress(&events)).Run(context.Background(), bp); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []domain.StepEvent{
		{StepID: "one", Tool: "a", Status: domain.StepStarted},
		{StepID: "one", Tool: "a", Status: domain.StepDone},
		{StepID: "two", Tool: "b", Status: domain.StepStarted},
		{StepID: "two", Tool: "b", Status: domain.StepDone},
	}
	if !slices.Equal(events, want) {
		t.Errorf("events = %+v, want %+v", events, want)
	}
}

func TestProgressReportsAFailedStepWithItsErrorAndStops(t *testing.T) {
	boom := errors.New("boom")
	reg := fakeRegistry{"a": &fakeTool{name: "a", err: boom}, "b": &fakeTool{name: "b", result: map[string]any{}}}
	var events []domain.StepEvent
	bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: "one", Tool: "a"}, {ID: "two", Tool: "b"}}}
	if _, err := app.NewRunner(reg).WithProgress(recordProgress(&events)).Run(context.Background(), bp); err == nil {
		t.Fatal("Run succeeded with a failing step")
	}
	if len(events) != 2 || events[0].Status != domain.StepStarted || events[1].Status != domain.StepFailed || events[1].StepID != "one" {
		t.Fatalf("events = %+v, want one started and one failed, and nothing for the step after", events)
	}
	if !errors.Is(events[1].Err, boom) {
		t.Errorf("failed event's Err = %v, want it to wrap the tool's error", events[1].Err)
	}
}

func TestAMissingToolReportsStartedThenFailed(t *testing.T) {
	var events []domain.StepEvent
	bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: "one", Tool: "absent"}}}
	_, _ = app.NewRunner(fakeRegistry{}).WithProgress(recordProgress(&events)).Run(context.Background(), bp)
	if len(events) != 2 || events[0].Status != domain.StepStarted || events[1].Status != domain.StepFailed || events[1].Err == nil {
		t.Errorf("events = %+v, want started then failed with an error", events)
	}
}

// constTool holds no state, so any number of runs may share it.
type constTool struct{}

func (constTool) Name() string { return "c" }

func (constTool) Invoke(context.Context, map[string]string) (any, error) {
	return map[string]any{}, nil
}

func TestASharedRunnerKeepsEachCallersProgressApart(t *testing.T) {
	base := app.NewRunner(fakeRegistry{"c": constTool{}})
	const callers = 16
	got := make([][]domain.StepEvent, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: strconv.Itoa(i), Tool: "c"}}}
			_, _ = base.WithProgress(recordProgress(&got[i])).Run(context.Background(), bp)
		})
	}
	wg.Wait()
	for i, events := range got {
		id := strconv.Itoa(i)
		want := []domain.StepEvent{
			{StepID: id, Tool: "c", Status: domain.StepStarted},
			{StepID: id, Tool: "c", Status: domain.StepDone},
		}
		if !slices.Equal(events, want) {
			t.Errorf("caller %d saw %+v, want only its own run's events", i, events)
		}
	}
}

func TestWithProgressLeavesTheBaseRunnerUnchanged(t *testing.T) {
	base := app.NewRunner(fakeRegistry{"c": constTool{}})
	var derived []domain.StepEvent
	_ = base.WithProgress(recordProgress(&derived))
	bp := domain.Blueprint{Name: "p", Steps: []domain.Step{{ID: "one", Tool: "c"}}}
	if _, err := base.Run(context.Background(), bp); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(derived) != 0 {
		t.Errorf("the base Runner reported %+v to a callback set on a derived one", derived)
	}
}
