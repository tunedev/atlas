package tools_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func TestJudgeAssessRebuildsReasonsFromARecordedJudgement(t *testing.T) {
	ctx := context.Background()
	docs, index := store(t)
	qs := []ports.Question{
		{ID: "pitch", Kind: ports.KindChoice, Ask: "Pitch the tent?", Options: []string{"yes", "no"}},
		{ID: "ground", Kind: ports.KindChoice, Ask: "Ground?", Options: []string{"soft", "hard"}},
	}
	j := ports.Judgement{
		Subject: "a meadow", Model: "m", Provider: "prov", When: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
		Answers: []ports.Answer{
			{ID: "pitch", Kind: ports.KindChoice, Chosen: "yes", Distribution: map[string]float64{"yes": 0.9, "no": 0.1},
				Coverage: ports.Coverage{Represented: 0, Declared: 2}},
			{ID: "ground", Kind: ports.KindChoice, Chosen: "soft", Distribution: map[string]float64{"soft": 0.6, "hard": 0.4},
				Coverage: ports.Coverage{Represented: 2, Declared: 2}},
		},
	}
	rules := []app.RuleResult{
		{ID: "no-flood-plain", State: app.RuleTripped, Evidence: "river bank"},
		{ID: "has-water", State: app.RuleClear, Evidence: "stream nearby"},
	}
	path, err := app.RecordAssessedJudgement(ctx, docs, index, "meadow-1", qs, j,
		app.Assessed{Fingerprint: "fp", Verdict: "yes", Rules: rules})
	if err != nil {
		t.Fatal(err)
	}

	out, err := tools.NewJudgeAssess(docs).Invoke(ctx, map[string]string{"path": path, "verdict": "pitch"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["verdict"] != "yes" {
		t.Errorf("verdict = %v; want yes", m["verdict"])
	}
	reasons := m["reasons"].([]any)
	if !slices.ContainsFunc(reasons, func(r any) bool { return strings.Contains(r.(string), "coverage 0 of 2") }) {
		t.Errorf("reasons = %v; want one naming zero coverage", reasons)
	}
	if tripped := m["tripped"].([]any); len(tripped) != 1 || tripped[0] != "no-flood-plain" {
		t.Errorf("tripped = %v; want [no-flood-plain]", tripped)
	}
}

func TestJudgeAssessRefusesMissingInputs(t *testing.T) {
	docs, _ := store(t)
	assess := tools.NewJudgeAssess(docs)
	for name, with := range map[string]map[string]string{
		"no path":    {"verdict": "pitch"},
		"no verdict": {"path": "judgements/a/b.json"},
		"no doc":     {"path": "judgements/a/b.json", "verdict": "pitch"},
	} {
		if _, err := assess.Invoke(context.Background(), with); err == nil || !strings.HasPrefix(err.Error(), "judge.assess: ") {
			t.Errorf("%s: err = %v, want a judge.assess error", name, err)
		}
	}
}
