package tools_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

// packCall is one child run the fake runner saw.
type packCall struct {
	path string
	vars map[string]string
}

// fakeRunner records every child run and fails those whose order is in fail.
type fakeRunner struct {
	calls []packCall
	fail  map[string]bool
}

func (f *fakeRunner) run(_ context.Context, path string, vars map[string]string) error {
	f.calls = append(f.calls, packCall{path: path, vars: vars})
	if f.fail[vars["order"]] {
		return errors.New("oven cold")
	}
	return nil
}

func noStage(context.Context, string) (string, error) { return "", nil }

const orderRows = `[
  {"subject_id": "order-3", "decision": "allow", "stage": "", "item": {"loaf": "rye"}},
  {"subject_id": "order-1", "decision": "allow", "stage": "", "item": {"loaf": "spelt"}},
  {"subject_id": "order-2", "decision": "deny", "stage": "", "item": {"loaf": "wheat"}}
]`

func TestPackEachRendersVarsPerRowInSubjectOrder(t *testing.T) {
	runner := &fakeRunner{}
	out, err := tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml",
		"rows": orderRows,
		"vars": "order: '[[ .item.subject_id ]]'\nloaf: '[[ .item.item.loaf ]]'",
	})
	if err != nil {
		t.Fatalf("pack.each: %v", err)
	}
	want := []packCall{
		{"packs/bake.yaml", map[string]string{"order": "order-1", "loaf": "spelt"}},
		{"packs/bake.yaml", map[string]string{"order": "order-2", "loaf": "wheat"}},
		{"packs/bake.yaml", map[string]string{"order": "order-3", "loaf": "rye"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("calls = %v", runner.calls)
	}
	result := out.(map[string]any)
	rows := result["rows"].([]any)
	first := map[string]any{"vars": map[string]string{"order": "order-1", "loaf": "spelt"}, "ok": true}
	if len(rows) != 3 || !reflect.DeepEqual(rows[0], first) {
		t.Errorf("rows = %v", rows)
	}
	meta := map[string]any{"count": 3, "ok": 3, "errors": 0, "skipped": 0}
	if !reflect.DeepEqual(result["_meta"], meta) {
		t.Errorf("_meta = %v", result["_meta"])
	}
}

func TestPackEachRunsOnlyRowsMatchingEveryConditionWithMissingAsEmpty(t *testing.T) {
	rows := `[
  {"subject_id": "order-1", "decision": "allow", "stage": ""},
  {"subject_id": "order-2", "decision": "allow"},
  {"subject_id": "order-3", "decision": "allow", "stage": "baked"},
  {"subject_id": "order-4", "decision": "deny", "stage": ""}
]`
	runner := &fakeRunner{}
	_, err := tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": rows, "match": "decision=allow,stage=",
		"vars": "order: '[[ .item.subject_id ]]'",
	})
	if err != nil {
		t.Fatalf("pack.each: %v", err)
	}
	var ran []string
	for _, c := range runner.calls {
		ran = append(ran, c.vars["order"])
	}
	if !reflect.DeepEqual(ran, []string{"order-1", "order-2"}) {
		t.Errorf("ran = %v", ran)
	}
}

func TestPackEachIsolatesAChildFailure(t *testing.T) {
	runner := &fakeRunner{fail: map[string]bool{"order-1": true}}
	out, err := tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": orderRows, "vars": "order: '[[ .item.subject_id ]]'",
	})
	if err != nil {
		t.Fatalf("pack.each: %v", err)
	}
	if len(runner.calls) != 3 {
		t.Errorf("calls = %v", runner.calls)
	}
	result := out.(map[string]any)
	failed := map[string]any{"vars": map[string]string{"order": "order-1"}, "error": "oven cold"}
	if rows := result["rows"].([]any); !reflect.DeepEqual(rows[0], failed) {
		t.Errorf("rows[0] = %v", rows[0])
	}
	meta := map[string]any{"count": 3, "ok": 2, "errors": 1, "skipped": 0}
	if !reflect.DeepEqual(result["_meta"], meta) {
		t.Errorf("_meta = %v", result["_meta"])
	}
}

func TestPackEachFailsWhenEveryChildFails(t *testing.T) {
	runner := &fakeRunner{fail: map[string]bool{"order-1": true, "order-2": true, "order-3": true}}
	_, err := tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": orderRows, "vars": "order: '[[ .item.subject_id ]]'",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "pack.each: ") || !strings.Contains(err.Error(), "oven cold") {
		t.Errorf("err = %v", err)
	}
}

func TestPackEachSkipsARowWhoseStageBecameSetDuringTheRun(t *testing.T) {
	rows := `[
  {"subject_id": "order-1", "stage": "", "item": {"loaf": "rye"}},
  {"subject_id": "order-1", "stage": "", "item": {"loaf": "rye"}}
]`
	runner := &fakeRunner{}
	stageOf := func(_ context.Context, subjectID string) (string, error) {
		if subjectID == "order-1" && len(runner.calls) > 0 {
			return "baked", nil
		}
		return "", nil
	}
	out, err := tools.NewPackEach(runner.run, stageOf).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": rows, "match": "stage=", "vars": "order: '[[ .item.subject_id ]]'",
	})
	if err != nil {
		t.Fatalf("pack.each: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Errorf("calls = %v", runner.calls)
	}
	meta := map[string]any{"count": 1, "ok": 1, "errors": 0, "skipped": 1}
	if got := out.(map[string]any)["_meta"]; !reflect.DeepEqual(got, meta) {
		t.Errorf("_meta = %v", got)
	}
}

func TestPackEachRefusesAMalformedVarBeforeAnyChildRuns(t *testing.T) {
	runner := &fakeRunner{}
	_, err := tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": orderRows,
		"vars": "order: '[[ .item.subject_id ]]'\nloaf: '[[ .item.item.loaf '",
	})
	if err == nil || !strings.HasPrefix(err.Error(), "pack.each: ") {
		t.Errorf("err = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("calls = %v", runner.calls)
	}
	_, err = tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": "[]", "vars": "loaf: '[[ .item.item.loaf '",
	})
	if err == nil || !strings.Contains(err.Error(), "loaf") {
		t.Errorf("with no rows, err = %v", err)
	}
}

func TestPackEachMatchesNumbersAndSpacedTerms(t *testing.T) {
	rows := `[
  {"subject_id": "order-1", "count": 1000000, "decision": "allow"},
  {"subject_id": "order-2", "count": 12, "decision": "allow"}
]`
	runner := &fakeRunner{}
	_, err := tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": rows, "match": "count=1000000, decision = allow",
		"vars": "order: '[[ .item.subject_id ]]'",
	})
	if err != nil {
		t.Fatalf("pack.each: %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].vars["order"] != "order-1" {
		t.Errorf("calls = %v", runner.calls)
	}
}

func TestPackEachSortsNumericIDsAsWrittenNumbers(t *testing.T) {
	rows := `[{"subject_id": 1500000}, {"subject_id": 10000000}]`
	runner := &fakeRunner{}
	_, err := tools.NewPackEach(runner.run, noStage).Invoke(context.Background(), map[string]string{
		"pack": "packs/bake.yaml", "rows": rows, "vars": "order: '[[ .item.subject_id ]]'",
	})
	if err != nil {
		t.Fatalf("pack.each: %v", err)
	}
	if len(runner.calls) != 2 || runner.calls[0].vars["order"] != "10000000" {
		t.Errorf("calls = %v", runner.calls)
	}
}

func TestPackEachStopsWhenItsContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	run := func(context.Context, string, map[string]string) error {
		calls++
		cancel()
		return nil
	}
	_, err := tools.NewPackEach(run, noStage).Invoke(ctx, map[string]string{
		"pack": "packs/bake.yaml", "rows": orderRows, "vars": "order: '[[ .item.subject_id ]]'",
	})
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "pack.each: ") {
		t.Errorf("err = %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d", calls)
	}
}
