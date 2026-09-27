package app_test

import (
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

func request(t *testing.T, subject string, qs []ports.Question) []byte {
	t.Helper()
	b, err := app.JudgeRequest(subject, qs)
	if err != nil {
		t.Fatalf("JudgeRequest: %v", err)
	}
	return b
}

func fingerprint(t *testing.T, request []byte, rules []app.Rule, inputs map[string]any, verdictID, model string) string {
	t.Helper()
	s, err := app.Fingerprint(request, rules, inputs, verdictID, model)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	return s
}

func TestAFingerprintChangesWithEveryInputAndOnlyThem(t *testing.T) {
	qs := []ports.Question{
		{ID: "genre", Kind: ports.KindChoice, Ask: "Genre?", Options: []string{"fiction", "history"}},
		{ID: "length", Kind: ports.KindChoice, Ask: "Length?", Options: []string{"short", "long"}},
	}
	rules := []app.Rule{{ID: "short", Kind: app.RuleJudged, Statement: "Short.", Threshold: 0.5, Ask: "Is it short?"}}
	inputs := map[string]any{"short": map[string]any{"field": "pages", "value": 100.0}}

	baseRequest := request(t, "a novel", qs)
	base := fingerprint(t, baseRequest, rules, inputs, "genre", "m1")
	if again := fingerprint(t, request(t, "a novel", qs), rules, inputs, "genre", "m1"); again != base {
		t.Error("the same inputs gave two fingerprints")
	}

	reordered := []ports.Question{qs[1], qs[0]}
	otherR := []app.Rule{{ID: "short", Kind: app.RuleJudged, Statement: "Short.", Threshold: 0.7, Ask: "Is it short?"}}
	otherInputs := map[string]any{"short": map[string]any{"field": "pages", "value": 200.0}}
	for name, changed := range map[string]string{
		"subject":                 fingerprint(t, request(t, "a poem", qs), rules, inputs, "genre", "m1"),
		"declared order (schema)": fingerprint(t, request(t, "a novel", reordered), rules, inputs, "genre", "m1"),
		"rules":                   fingerprint(t, baseRequest, otherR, inputs, "genre", "m1"),
		"inputs":                  fingerprint(t, baseRequest, rules, otherInputs, "genre", "m1"),
		"verdict id":              fingerprint(t, baseRequest, rules, inputs, "length", "m1"),
		"model":                   fingerprint(t, baseRequest, rules, inputs, "genre", "m2"),
	} {
		if changed == base {
			t.Errorf("changing the %s left the fingerprint unchanged", name)
		}
	}
}
