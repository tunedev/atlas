package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
)

func aDecision() app.Decision {
	return app.Decision{
		SubjectID: "subject-1", Choice: "read", Reason: "short and well reviewed",
		When: time.Date(2026, 9, 24, 10, 3, 0, 0, time.UTC),
	}
}

func TestRecordDecisionWritesTheDocumentAndARow(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	path, err := app.RecordDecision(context.Background(), docs, index, aDecision())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if path != "decisions/subject-1/2026-09-24T10-03-00.000Z.json" {
		t.Errorf("path = %q", path)
	}
	var doc map[string]any
	if err := json.Unmarshal(docs.put[path], &doc); err != nil {
		t.Fatalf("document: %v", err)
	}
	if doc["decision"] != "read" || doc["reason"] != "short and well reviewed" || doc["when"] != "2026-09-24T10:03:00.000Z" {
		t.Errorf("doc = %v", doc)
	}
	if _, present := doc["outcome"]; present {
		t.Error("a decision carries an outcome slot; only the judgement owns that")
	}
	row := index.rows[0]
	if row.Kind != "decision" || row.Fields["subject_id"] != "subject-1" || row.Fields["decision"] != "read" {
		t.Errorf("row = %+v", row)
	}
}

func TestASecondDecisionAboutOneSubjectDoesNotOverwriteTheFirst(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	first, second := aDecision(), aDecision()
	second.Choice, second.When = "skip", first.When.Add(time.Minute)
	p1, _ := app.RecordDecision(context.Background(), docs, index, first)
	p2, _ := app.RecordDecision(context.Background(), docs, index, second)
	if p1 == p2 || len(docs.put) != 2 {
		t.Errorf("paths %q %q, documents %d", p1, p2, len(docs.put))
	}
}

func TestRecordDecisionRejectsASubjectIDWithADotDotSegment(t *testing.T) {
	d := aDecision()
	d.SubjectID = "../profile"
	if _, err := app.RecordDecision(context.Background(), newFakeDocs(), &fakeIndex{}, d); err == nil {
		t.Fatal("a subject id with a dot-dot segment was recorded")
	}
}

func TestADecisionNeedsASubjectAndAChoice(t *testing.T) {
	for _, d := range []app.Decision{{Choice: "read"}, {SubjectID: "subject-1"}} {
		if _, err := app.RecordDecision(context.Background(), newFakeDocs(), &fakeIndex{}, d); err == nil {
			t.Errorf("decision %+v was recorded", d)
		}
	}
}

func TestVerdictAtReadsTheChosenAnswerFromTheJudgement(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err != nil {
		t.Fatalf("judgement: %v", err)
	}
	got, err := app.VerdictAt(context.Background(), docs, "subject-1", path, "readable")
	if err != nil || got != "yes" {
		t.Errorf("verdict = %q, err = %v", got, err)
	}
	if _, err := app.VerdictAt(context.Background(), docs, "subject-1", path, "absent"); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Errorf("a question the judgement never asked gave err = %v", err)
	}
	if _, err := app.VerdictAt(context.Background(), docs, "subject-1", "judgements/none.json", "readable"); err == nil {
		t.Error("a judgement that does not exist gave a verdict")
	}
}

func TestVerdictAtRejectsAJudgementAboutADifferentSubject(t *testing.T) {
	docs, index := newFakeDocs(), &fakeIndex{}
	path, err := app.RecordJudgement(context.Background(), docs, index, "subject-1", recordQuestions(), aJudgement())
	if err != nil {
		t.Fatalf("judgement: %v", err)
	}
	_, err = app.VerdictAt(context.Background(), docs, "subject-2", path, "readable")
	if err == nil {
		t.Fatal("a judgement about a different subject gave a verdict")
	}
	if !strings.Contains(err.Error(), "subject-1") || !strings.Contains(err.Error(), "subject-2") {
		t.Errorf("error does not name both subjects: %v", err)
	}
}
