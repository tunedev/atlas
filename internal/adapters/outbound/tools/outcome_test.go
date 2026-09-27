package tools_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func weatherJudgement(subject string, p float64, when time.Time) ports.Judgement {
	return ports.Judgement{
		Subject: subject, Model: "m", Provider: "prov", When: when,
		Answers: []ports.Answer{{ID: "rain", Kind: ports.KindNoul, Chosen: "yes",
			Distribution: map[string]float64{"yes": p, "no": 1 - p},
			Coverage:     ports.Coverage{Represented: 2, Declared: 2}}},
	}
}

var rainQ = []ports.Question{{ID: "rain", Kind: ports.KindNoul, Ask: "Will it rain?"}}

// TestTheLoopClosesThroughTheTools records forecasts, attaches outcomes
// through judge.outcome, and reads them back through judge.calibrate.
func TestTheLoopClosesThroughTheTools(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	base := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	for i := range 32 {
		subject := fmt.Sprintf("fete-%d", i)
		if _, err := app.RecordJudgement(ctx, docs, index, subject, rainQ, weatherJudgement(subject, 0.8, base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
		state := "wet"
		if i%4 == 0 {
			state = "dry"
		}
		out, err := tools.NewOutcome(docs, index).Invoke(ctx, map[string]string{"subject_id": subject, "state": state, "when": "2026-09-29"})
		if err != nil {
			t.Fatalf("judge.outcome: %v", err)
		}
		if got := out.(map[string]any)["attached"].([]string); len(got) != 1 {
			t.Fatalf("attached = %v", got)
		}
	}

	out, err := tools.NewCalibrate(docs, index).Invoke(ctx, map[string]string{
		"question": "rain", "options": "yes", "positive": "wet", "negative": "dry",
	})
	if err != nil {
		t.Fatalf("judge.calibrate: %v", err)
	}
	pooled := out.(map[string]any)["pooled"].(map[string]any)
	if pooled["n"] != 32 || pooled["brier"] == nil {
		t.Errorf("pooled = %v", pooled)
	}
	if h, _ := pooled["headline"].(string); !strings.Contains(h, "Brier") || !strings.Contains(h, "n=32") {
		t.Errorf("headline = %q", h)
	}
	bin := pooled["bins"].([]any)[8].(map[string]any)
	if reads, _ := bin["reads"].(string); !strings.Contains(reads, "80% judgements came true 75% of the time") {
		t.Errorf("0.8 bin reads %q", reads)
	}
	engines := out.(map[string]any)["by_engine"].([]any)
	if len(engines) != 1 || engines[0].(map[string]any)["provider"] != "prov" {
		t.Errorf("by_engine = %v", engines)
	}
}

func TestAReportTooSmallToScoreSaysSo(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	out, err := tools.NewCalibrate(docs, index).Invoke(ctx, map[string]string{
		"question": "rain", "options": "yes", "positive": "wet", "negative": "dry",
	})
	if err != nil {
		t.Fatal(err)
	}
	pooled := out.(map[string]any)["pooled"].(map[string]any)
	if pooled["brier"] != nil || !strings.Contains(pooled["headline"].(string), "too few") {
		t.Errorf("pooled = %v", pooled)
	}
}

func TestTheOutcomeToolRefusesAmbiguousOrIncompleteInput(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	if _, err := app.RecordJudgement(ctx, docs, index, "a", rainQ, weatherJudgement("a", 0.5, time.Now())); err != nil {
		t.Fatal(err)
	}
	for name, with := range map[string]map[string]string{
		"neither target": {"state": "wet"},
		"both targets":   {"subject_id": "a", "judgement_path": "judgements/a/x.json", "state": "wet"},
		"no state":       {"subject_id": "a"},
		"bad when":       {"subject_id": "a", "state": "wet", "when": "last tuesday"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tools.NewOutcome(docs, index).Invoke(ctx, with)
			if err == nil || !strings.HasPrefix(err.Error(), "judge.outcome: ") {
				t.Fatalf("err = %v", err)
			}
			if name == "no state" && !strings.Contains(err.Error(), "state") {
				t.Errorf("err = %v, want it to mention state", err)
			}
		})
	}
}

func TestTheAgreementToolReportsACount(t *testing.T) {
	docs, index := store(t)
	ctx := context.Background()
	if _, err := app.RecordDecision(ctx, docs, index, app.Decision{SubjectID: "a", Choice: "yes", VerdictAtDecision: "no", When: time.Now()}); err != nil {
		t.Fatal(err)
	}
	out, err := tools.NewAgreement(index).Invoke(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["n"] != 1 || m["agreed"] != 0 || m["rate"] != nil || !strings.Contains(m["headline"].(string), "too few") {
		t.Errorf("agreement = %v", m)
	}
}
