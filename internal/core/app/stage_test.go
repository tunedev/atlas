package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

var bakeNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func aStage(stage string, when time.Time) app.Stage {
	return app.Stage{SubjectID: "order-7", Stage: stage, When: when, DeclaredBy: "baker", Note: "rye loaf"}
}

func declare(t *testing.T, docs ports.Docs, index ports.Index, s app.Stage) (string, bool) {
	t.Helper()
	path, changed, err := app.DeclareStage(context.Background(), docs, index, s, bakeNow)
	if err != nil {
		t.Fatalf("declare %s: %v", s.Stage, err)
	}
	return path, changed
}

func stageRow(t *testing.T, index ports.Index, subject string) ports.Record {
	t.Helper()
	rows, err := index.Find(context.Background(), ports.Query{Kind: "stage", Match: map[string]string{"subject_id": subject}})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("stage rows for %s = %d, want 1", subject, len(rows))
	}
	return rows[0]
}

func TestFirstDeclarationWritesTheDocumentAndRow(t *testing.T) {
	docs, index := record(t)
	path, changed := declare(t, docs, index, aStage("kneaded", bakeNow.Add(-2*time.Hour)))
	if path != "applications/order-7/stage.json" || !changed {
		t.Fatalf("path = %q, changed = %v", path, changed)
	}
	body, err := docs.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("document: %v", err)
	}
	reached, _ := doc["reached"].(map[string]any)
	if doc["subject_id"] != "order-7" || doc["stage"] != "kneaded" || doc["declared_by"] != "baker" ||
		doc["note"] != "rye loaf" || reached["kneaded"] != "2026-09-30T10:00:00Z" || len(reached) != 1 {
		t.Errorf("doc = %v", doc)
	}
	row := stageRow(t, index, "order-7")
	if row.Path != path || row.Fields["stage"] != "kneaded" || row.Fields["declared_by"] != "baker" ||
		row.Fields["kneaded_at"] != "2026-09-30T10:00:00Z" {
		t.Errorf("row = %+v", row)
	}
}

func TestALaterStageAddsToReachedAndSetsItsTime(t *testing.T) {
	docs, index := record(t)
	declare(t, docs, index, aStage("kneaded", bakeNow.Add(-2*time.Hour)))
	path, changed := declare(t, docs, index, aStage("baked", bakeNow.Add(-time.Hour)))
	if !changed {
		t.Fatal("a new stage reported no change")
	}
	row := stageRow(t, index, "order-7")
	if row.Fields["stage"] != "baked" || row.Fields["kneaded_at"] != "2026-09-30T10:00:00Z" ||
		row.Fields["baked_at"] != "2026-09-30T11:00:00Z" {
		t.Errorf("row = %+v", row)
	}
	history, err := docs.History(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Errorf("revisions = %d, want 2", len(history))
	}
}

func TestDeclareRefusesATimeBeforeTheCurrentStage(t *testing.T) {
	docs, index := record(t)
	declare(t, docs, index, aStage("kneaded", bakeNow.Add(-2*time.Hour)))
	_, _, err := app.DeclareStage(context.Background(), docs, index, aStage("baked", bakeNow.Add(-3*time.Hour)), bakeNow)
	if err == nil {
		t.Fatal("a stage dated before the current stage was accepted")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "stage: ") || !strings.Contains(msg, "2026-09-30T09:00:00Z") || !strings.Contains(msg, "2026-09-30T10:00:00Z") {
		t.Errorf("error = %q, want both times", msg)
	}
	if got := stageRow(t, index, "order-7").Fields["stage"]; got != "kneaded" {
		t.Errorf("stage = %q after a refused declaration", got)
	}
}

func TestDeclareRefusesAFutureTime(t *testing.T) {
	docs, index := record(t)
	_, _, err := app.DeclareStage(context.Background(), docs, index, aStage("kneaded", bakeNow.Add(time.Minute)), bakeNow)
	if err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("err = %v, want a future-time refusal", err)
	}
}

func TestRedeclaringTheCurrentStageMakesNoRevision(t *testing.T) {
	docs, index := record(t)
	path, _ := declare(t, docs, index, aStage("kneaded", bakeNow.Add(-2*time.Hour)))
	again, changed := declare(t, docs, index, aStage("kneaded", bakeNow.Add(-time.Hour)))
	if changed || again != path {
		t.Errorf("changed = %v, path = %q", changed, again)
	}
	history, err := docs.History(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Errorf("revisions = %d, want 1", len(history))
	}
}

func TestABackdatedLaterStageAfterTheCurrentIsAccepted(t *testing.T) {
	docs, index := record(t)
	declare(t, docs, index, aStage("drafted", bakeNow.Add(-48*time.Hour)))
	declare(t, docs, index, aStage("sent", bakeNow.Add(-24*time.Hour)))
	if got := stageRow(t, index, "order-7").Fields["sent_at"]; got != "2026-09-29T12:00:00Z" {
		t.Errorf("sent_at = %q", got)
	}
}

func TestSentCanBeTheFirstStage(t *testing.T) {
	docs, index := record(t)
	declare(t, docs, index, aStage("sent", bakeNow.Add(-24*time.Hour)))
	row := stageRow(t, index, "order-7")
	if row.Fields["stage"] != "sent" || row.Fields["sent_at"] != "2026-09-29T12:00:00Z" {
		t.Errorf("row = %+v", row)
	}
}

func TestDeclareRefusesASubjectIDWithASlash(t *testing.T) {
	docs, index := record(t)
	s := aStage("kneaded", bakeNow)
	s.SubjectID = "order/7"
	if _, _, err := app.DeclareStage(context.Background(), docs, index, s, bakeNow); err == nil {
		t.Fatal("a subject id with a slash was accepted")
	}
}

func TestDeclareWithNoTimeUsesNow(t *testing.T) {
	docs, index := record(t)
	declare(t, docs, index, aStage("kneaded", time.Time{}))
	if got := stageRow(t, index, "order-7").Fields["kneaded_at"]; got != "2026-09-30T12:00:00Z" {
		t.Errorf("kneaded_at = %q", got)
	}
}

func TestCurrentStageReturnsTheLatestAndEmptyForAnUnknownSubject(t *testing.T) {
	docs, index := record(t)
	declare(t, docs, index, aStage("kneaded", bakeNow.Add(-2*time.Hour)))
	declare(t, docs, index, aStage("baked", bakeNow.Add(-time.Hour)))
	ctx := context.Background()
	if got, err := app.CurrentStage(ctx, index, "order-7"); err != nil || got != "baked" {
		t.Errorf("current = %q, %v", got, err)
	}
	if got, err := app.CurrentStage(ctx, index, "order-8"); err != nil || got != "" {
		t.Errorf("unknown subject = %q, %v", got, err)
	}
}
