package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func TestAFingerprintChangesWithEveryInputAndOnlyThem(t *testing.T) {
	qs := []ports.Question{{ID: "genre", Kind: ports.KindChoice, Ask: "Genre?", Options: []string{"fiction", "history"}}}
	rules := []app.Rule{{ID: "short", Kind: app.RuleJudged, Statement: "Short.", Threshold: 0.5}}
	inputs := map[string]any{"short": map[string]any{"field": "pages", "value": 100.0}}
	fp := func(subject string, qs []ports.Question, rules []app.Rule, inputs map[string]any, model string) string {
		t.Helper()
		s, err := app.Fingerprint(subject, qs, rules, inputs, model)
		if err != nil {
			t.Fatalf("Fingerprint: %v", err)
		}
		return s
	}
	base := fp("a novel", qs, rules, inputs, "m1")
	if again := fp("a novel", qs, rules, inputs, "m1"); again != base {
		t.Error("the same inputs gave two fingerprints")
	}
	otherQ := []ports.Question{{ID: "genre", Kind: ports.KindChoice, Ask: "Genre?", Options: []string{"fiction", "poetry"}}}
	otherR := []app.Rule{{ID: "short", Kind: app.RuleJudged, Statement: "Short.", Threshold: 0.7}}
	otherInputs := map[string]any{"short": map[string]any{"field": "pages", "value": 200.0}}
	for name, changed := range map[string]string{
		"subject":   fp("a poem", qs, rules, inputs, "m1"),
		"questions": fp("a novel", otherQ, rules, inputs, "m1"),
		"rules":     fp("a novel", qs, otherR, inputs, "m1"),
		"inputs":    fp("a novel", qs, rules, otherInputs, "m1"),
		"model":     fp("a novel", qs, rules, inputs, "m2"),
	} {
		if changed == base {
			t.Errorf("changing the %s left the fingerprint unchanged", name)
		}
	}
}
