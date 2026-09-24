package tools_test

import (
	"context"
	"testing"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

func TestDecisionSnapshotsTheVerdictOfTheJudgementItPointsAt(t *testing.T) {
	docs, index := store(t)
	judged, err := tools.NewJudge(&stubJudge{}, docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "subject": "a short book", "questions": questionsYAML,
	})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	jpath := judged.(map[string]any)["path"].(string)

	out, err := tools.NewDecision(docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "choice": "skip", "reason": "too long",
		"judgement_path": jpath, "verdict_question": "readable",
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if out.(map[string]any)["verdict_at_decision"] != "yes" {
		t.Errorf("out = %v", out)
	}
	rows, _ := index.Find(context.Background(), ports.Query{Kind: "decision", Match: map[string]string{"subject_id": "subject-1"}, Limit: 10})
	if len(rows) != 1 || rows[0].Fields["verdict_at_decision"] != "yes" || rows[0].Fields["decision"] != "skip" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestDecisionRecordsNothingForADanglingOrHalfSpecifiedJudgement(t *testing.T) {
	docs, index := store(t)
	for name, with := range map[string]map[string]string{
		"missing judgement":     {"subject_id": "s", "choice": "skip", "judgement_path": "judgements/s/none.json", "verdict_question": "readable"},
		"path without question": {"subject_id": "s", "choice": "skip", "judgement_path": "judgements/s/none.json"},
		"question without path": {"subject_id": "s", "choice": "skip", "verdict_question": "readable"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tools.NewDecision(docs, index).Invoke(context.Background(), with); err == nil {
				t.Fatal("recorded")
			}
		})
	}
	if paths, _ := docs.List(context.Background(), "decisions"); len(paths) != 0 {
		t.Errorf("decisions written anyway: %v", paths)
	}
}

func TestADecisionWithNoJudgementIsRecorded(t *testing.T) {
	docs, index := store(t)
	out, err := tools.NewDecision(docs, index).Invoke(context.Background(), map[string]string{
		"subject_id": "subject-1", "choice": "read", "reason": "a friend lent it",
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if out.(map[string]any)["verdict_at_decision"] != "" {
		t.Errorf("out = %v", out)
	}
}
