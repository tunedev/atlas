package app_test

import (
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

const shelfRules = `{"rules": [
  {"id": "min-pages", "statement": "At least 100 pages.", "kind": "comparable", "op": ">=", "value": 100},
  {"id": "language", "statement": "Written in English.", "kind": "comparable", "op": "==", "value": "en"},
  {"id": "no-spoilers", "statement": "No spoilers in the blurb.", "kind": "judged", "threshold": 0.6},
  {"id": "no-sequels", "statement": "Not a sequel.", "kind": "judged", "ask": "Is this book a sequel?"}
]}`

func parsed(t *testing.T) []app.Rule {
	t.Helper()
	rules, err := app.ParseRules([]byte(shelfRules))
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	return rules
}

func yes(id string, p float64) ports.Answer {
	return ports.Answer{ID: id, Kind: ports.KindNoul, Chosen: "yes", Distribution: map[string]float64{"yes": p, "no": 1 - p}}
}

func byID(results []app.RuleResult) map[string]app.RuleResult {
	m := map[string]app.RuleResult{}
	for _, r := range results {
		m[r.ID] = r
	}
	return m
}

func TestParseRulesDefaultsAThresholdAndRejectsBadRules(t *testing.T) {
	rules := parsed(t)
	if rules[3].Threshold != 0.5 {
		t.Errorf("absent threshold = %v, want 0.5", rules[3].Threshold)
	}
	for name, body := range map[string]string{
		"no id":         `{"rules": [{"kind": "judged", "statement": "x"}]}`,
		"unknown kind":  `{"rules": [{"id": "a", "kind": "guessed"}]}`,
		"bad operator":  `{"rules": [{"id": "a", "kind": "comparable", "op": "~", "value": 1}]}`,
		"repeated id":   `{"rules": [{"id": "a", "kind": "judged"}, {"id": "a", "kind": "judged"}]}`,
		"threshold > 1": `{"rules": [{"id": "a", "kind": "judged", "threshold": 1.5}]}`,
		"not json":      `rules: []`,
	} {
		if _, err := app.ParseRules([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestComparableRulesTripWhenTheComparisonFails(t *testing.T) {
	fields := map[string]string{"min-pages": "stats.pages", "language": "language"}
	doc := map[string]any{"stats": map[string]any{"pages": 90.0}, "language": "en"}
	got := byID(mustCheck(t, parsed(t)[:2], doc, fields, nil))
	if got["min-pages"].State != app.RuleTripped || !strings.Contains(got["min-pages"].Evidence, "stats.pages is 90") {
		t.Errorf("min-pages = %+v, want tripped with the value quoted", got["min-pages"])
	}
	if got["language"].State != app.RuleClear {
		t.Errorf("language = %+v, want clear", got["language"])
	}
}

func TestAMissingFieldIsUnknownNotTripped(t *testing.T) {
	fields := map[string]string{"min-pages": "stats.pages"}
	got := byID(mustCheck(t, parsed(t)[:2], map[string]any{"language": 7.0}, fields, nil))
	if got["min-pages"].State != app.RuleUnknown || !strings.Contains(got["min-pages"].Evidence, "stats.pages not in the subject") {
		t.Errorf("missing field = %+v, want unknown", got["min-pages"])
	}
	if got["language"].State != app.RuleUnknown {
		t.Errorf("unmapped rule = %+v, want unknown", got["language"])
	}
}

func TestAValueOfTheWrongTypeIsUnknown(t *testing.T) {
	fields := map[string]string{"min-pages": "stats.pages"}
	doc := map[string]any{"stats": map[string]any{"pages": "many"}}
	if got := byID(mustCheck(t, parsed(t)[:1], doc, fields, nil)); got["min-pages"].State != app.RuleUnknown {
		t.Errorf("string against a number = %+v, want unknown", got["min-pages"])
	}
}

func TestEveryOperatorCompares(t *testing.T) {
	cases := []struct {
		op    string
		value any
		got   any
		clear bool
	}{
		{">=", 10.0, 10.0, true}, {">=", 10.0, 9.0, false},
		{"<=", 10.0, 10.0, true}, {"<=", 10.0, 11.0, false},
		{">", 10.0, 11.0, true}, {">", 10.0, 10.0, false},
		{"<", 10.0, 9.0, true}, {"<", 10.0, 10.0, false},
		{"==", "a", "a", true}, {"==", "a", "b", false},
		{"!=", "a", "b", true}, {"!=", 3.0, 3.0, false},
	}
	for _, c := range cases {
		r := app.Rule{ID: "r", Kind: app.RuleComparable, Op: c.op, Value: c.value}
		got := mustCheck(t, []app.Rule{r}, map[string]any{"v": c.got}, map[string]string{"r": "v"}, nil)[0]
		want := app.RuleTripped
		if c.clear {
			want = app.RuleClear
		}
		if got.State != want {
			t.Errorf("%v %s %v = %s, want %s", c.got, c.op, c.value, got.State, want)
		}
	}
}

func TestAJudgedRuleTripsAtItsThreshold(t *testing.T) {
	rule := parsed(t)[2] // threshold 0.6
	for p, want := range map[float64]string{0.59: app.RuleClear, 0.6: app.RuleTripped, 0.9: app.RuleTripped} {
		got := mustCheck(t, []app.Rule{rule}, nil, nil, []ports.Answer{yes(app.RuleQuestionID(rule.ID), p)})[0]
		if got.State != want {
			t.Errorf("p=%v: %s, want %s", p, got.State, want)
		}
	}
}

func TestAJudgedRuleWithoutItsAnswerIsAnError(t *testing.T) {
	if _, err := app.CheckRules(parsed(t)[2:3], nil, nil, nil); err == nil {
		t.Error("a judged rule with no answer was checked")
	}
}

func TestARuleQuestionUsesAskOrDerivesFromTheStatement(t *testing.T) {
	rules := parsed(t)
	derived, explicit := app.RuleQuestion(rules[2]), app.RuleQuestion(rules[3])
	if derived.ID != "rule_no-spoilers" || derived.Kind != ports.KindNoul || derived.Ask != `Does this break the rule "No spoilers in the blurb."?` {
		t.Errorf("derived = %+v", derived)
	}
	if explicit.Ask != "Is this book a sequel?" {
		t.Errorf("explicit ask = %q", explicit.Ask)
	}
}

func mustCheck(t *testing.T, rules []app.Rule, doc any, fields map[string]string, answers []ports.Answer) []app.RuleResult {
	t.Helper()
	got, err := app.CheckRules(rules, doc, fields, answers)
	if err != nil {
		t.Fatalf("CheckRules: %v", err)
	}
	return got
}
