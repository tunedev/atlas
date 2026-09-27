package app_test

import (
	"slices"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func answer(id, chosen string, p float64, represented, declared int) ports.Answer {
	return ports.Answer{
		ID: id, Chosen: chosen, Distribution: map[string]float64{chosen: p},
		Coverage: ports.Coverage{Represented: represented, Declared: declared},
	}
}

func TestAssessOrdersReasonsAndLeavesTheVerdictAlone(t *testing.T) {
	answers := []ports.Answer{
		answer("verdict", "keep", 0.55, 3, 3),
		answer("genre", "history", 1.0, 0, 5),
		answer("length", "long", 0.82, 3, 4),
		answer(app.RuleQuestionID("no-spoilers"), "yes", 0.71, 2, 2),
	}
	rules := []app.RuleResult{
		{ID: "language", State: app.RuleClear, Evidence: "language is en, rule needs == en"},
		{ID: "no-spoilers", State: app.RuleTripped, Evidence: "p(yes) 0.71 >= 0.60"},
		{ID: "min-pages", State: app.RuleUnknown, Evidence: "stats.pages not in the subject"},
	}
	got, err := app.Assess("verdict", answers, rules)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Verdict != "keep" || got.P != 0.55 {
		t.Errorf("verdict = %q %.2f, want keep 0.55: a tripped rule flags, it does not override", got.Verdict, got.P)
	}
	want := []string{
		"no-spoilers tripped: p(yes) 0.71 >= 0.60",
		"min-pages unknown: stats.pages not in the subject",
		"genre: history (1.00, coverage 0 of 5)",
		"length: long (0.82)",
		"language clear: language is en, rule needs == en",
	}
	if !slices.Equal(got.Reasons, want) {
		t.Errorf("reasons =\n%q\nwant\n%q", got.Reasons, want)
	}
	if len(got.Rules) != 3 {
		t.Errorf("rules = %v, want all three results carried", got.Rules)
	}
}

func TestAssessNotesZeroCoverageOnTheVerdictBeforeTheOtherAnswers(t *testing.T) {
	answers := []ports.Answer{
		answer("verdict", "keep", 0.55, 0, 3),
		answer("genre", "history", 0.9, 3, 5),
	}
	got, err := app.Assess("verdict", answers, nil)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	want := []string{
		"verdict: keep (0.55, coverage 0 of 3)",
		"genre: history (0.90)",
	}
	if !slices.Equal(got.Reasons, want) {
		t.Errorf("reasons =\n%q\nwant\n%q", got.Reasons, want)
	}
}

func TestAssessSaysNothingAboutTheVerdictsCoverageWhenItIsRepresented(t *testing.T) {
	answers := []ports.Answer{answer("verdict", "keep", 0.55, 3, 3)}
	got, err := app.Assess("verdict", answers, nil)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if len(got.Reasons) != 0 {
		t.Errorf("reasons = %q, want none", got.Reasons)
	}
}

func TestAssessWithoutTheVerdictAnswerIsAnError(t *testing.T) {
	if _, err := app.Assess("verdict", []ports.Answer{answer("genre", "history", 1, 1, 5)}, nil); err == nil {
		t.Error("an assessment was built with no verdict answer")
	}
}
