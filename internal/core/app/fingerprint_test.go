package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func TestAFingerprintChangesWithEveryInputAndOnlyThem(t *testing.T) {
	qs := []ports.Question{{ID: "genre", Kind: ports.KindChoice, Ask: "Genre?", Options: []string{"fiction", "history"}}}
	rules := []app.Rule{{ID: "short", Kind: app.RuleJudged, Statement: "Short.", Threshold: 0.5}}
	fp := func(subject string, qs []ports.Question, rules []app.Rule, model string) string {
		t.Helper()
		s, err := app.Fingerprint(subject, qs, rules, model)
		if err != nil {
			t.Fatalf("Fingerprint: %v", err)
		}
		return s
	}
	base := fp("a novel", qs, rules, "m1")
	if again := fp("a novel", qs, rules, "m1"); again != base {
		t.Error("the same inputs gave two fingerprints")
	}
	otherQ := []ports.Question{{ID: "genre", Kind: ports.KindChoice, Ask: "Genre?", Options: []string{"fiction", "poetry"}}}
	otherR := []app.Rule{{ID: "short", Kind: app.RuleJudged, Statement: "Short.", Threshold: 0.7}}
	for name, changed := range map[string]string{
		"subject":   fp("a poem", qs, rules, "m1"),
		"questions": fp("a novel", otherQ, rules, "m1"),
		"rules":     fp("a novel", qs, otherR, "m1"),
		"model":     fp("a novel", qs, rules, "m2"),
	} {
		if changed == base {
			t.Errorf("changing the %s left the fingerprint unchanged", name)
		}
	}
}
