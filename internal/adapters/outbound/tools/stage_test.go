package tools_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

func TestStageDeclareRecordsAStageAndReportsTheChange(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	declare := tools.NewStageDeclare(docs, index)
	with := map[string]string{"subject_id": "beam-3", "stage": "lit", "when": "2026-09-27T20:00:00Z", "declared_by": "keeper"}

	out, err := declare.Invoke(ctx, with)
	if err != nil {
		t.Fatalf("stage.declare: %v", err)
	}
	want := map[string]any{"path": "applications/beam-3/stage.json", "stage": "lit", "changed": true}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("out = %v", out)
	}
	rows, err := index.Find(ctx, ports.Query{Kind: "stage", Match: map[string]string{"subject_id": "beam-3"}})
	if err != nil || len(rows) != 1 || rows[0].Fields["lit_at"] != "2026-09-27T20:00:00Z" || rows[0].Fields["declared_by"] != "keeper" {
		t.Errorf("rows = %v, %v", rows, err)
	}
	again, err := declare.Invoke(ctx, with)
	if err != nil {
		t.Fatalf("stage.declare again: %v", err)
	}
	if again.(map[string]any)["changed"] != false {
		t.Errorf("again = %v", again)
	}
}

func TestStageDeclareWithNoTimeDeclaresNow(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	before := time.Now().UTC().Truncate(time.Second)
	if _, err := tools.NewStageDeclare(docs, index).Invoke(ctx, map[string]string{"subject_id": "beam-3", "stage": "lit"}); err != nil {
		t.Fatalf("stage.declare: %v", err)
	}
	after := time.Now().UTC()
	rows, err := index.Find(ctx, ports.Query{Kind: "stage", Match: map[string]string{"subject_id": "beam-3"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	lit, err := time.Parse(time.RFC3339, rows[0].Fields["lit_at"])
	if err != nil || lit.Before(before) || lit.After(after) {
		t.Errorf("lit_at = %q, want between %s and %s", rows[0].Fields["lit_at"], before, after)
	}
}

func TestStageDeclareNamesABadTime(t *testing.T) {
	docs, index := store(t)
	_, err := tools.NewStageDeclare(docs, index).Invoke(context.Background(), map[string]string{"subject_id": "beam-3", "stage": "lit", "when": "dusk"})
	if err == nil || !strings.HasPrefix(err.Error(), "stage.declare: ") || !strings.Contains(err.Error(), `"dusk"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestStageAttachSetsEachRowsStage(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	if _, err := tools.NewStageDeclare(docs, index).Invoke(ctx, map[string]string{"subject_id": "beam-3", "stage": "lit", "when": "2026-09-27T20:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	rows := `[{"subject_id": "beam-3", "log": "clear"}, {"subject_id": "beam-4"}, {"log": "fog"}]`

	out, err := tools.NewStageAttach(index).Invoke(ctx, map[string]string{"rows": rows})
	if err != nil {
		t.Fatalf("stage.attach: %v", err)
	}
	want := []any{
		map[string]any{"subject_id": "beam-3", "log": "clear", "stage": "lit"},
		map[string]any{"subject_id": "beam-4", "stage": ""},
		map[string]any{"log": "fog"},
	}
	if got := out.(map[string]any)["rows"]; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v", got)
	}
}

func TestStageAttachRefusesRowsThatAreNotAList(t *testing.T) {
	_, index := store(t)
	_, err := tools.NewStageAttach(index).Invoke(context.Background(), map[string]string{"rows": "{}"})
	if err == nil || !strings.HasPrefix(err.Error(), "stage.attach: ") {
		t.Fatalf("err = %v", err)
	}
}
