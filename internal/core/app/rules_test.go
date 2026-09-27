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
  {"id": "no-spoilers", "statement": "No spoilers in the blurb.", "kind": "judged", "threshold": 0.6, "ask": "Does the blurb reveal the ending?"},
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
		"no id":                     `{"rules": [{"kind": "judged", "statement": "x"}]}`,
		"unknown kind":              `{"rules": [{"id": "a", "kind": "guessed"}]}`,
		"bad operator":              `{"rules": [{"id": "a", "kind": "comparable", "op": "~", "value": 1}]}`,
		"repeated id":               `{"rules": [{"id": "a", "kind": "judged", "ask": "Is x true?"}, {"id": "a", "kind": "judged", "ask": "Is x true?"}]}`,
		"threshold > 1":             `{"rules": [{"id": "a", "kind": "judged", "ask": "Is x true?", "threshold": 1.5}]}`,
		"judged without ask":        `{"rules": [{"id": "a", "kind": "judged"}]}`,
		"judged with blank ask":     `{"rules": [{"id": "a", "kind": "judged", "ask": "   "}]}`,
		"comparable without value":  `{"rules": [{"id": "a", "kind": "comparable", "op": ">="}]}`,
		"ordering op, string value": `{"rules": [{"id": "a", "kind": "comparable", "op": ">=", "value": "100"}]}`,
		"== with a bool value":      `{"rules": [{"id": "a", "kind": "comparable", "op": "==", "value": true}]}`,
		"not json":                  `rules: []`,
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

func TestAJudgedRuleWithZeroCoverageNotesItInEvidence(t *testing.T) {
	rule := parsed(t)[2] // no-spoilers, threshold 0.6
	a := ports.Answer{
		ID: app.RuleQuestionID(rule.ID), Kind: ports.KindNoul, Chosen: "yes",
		Distribution: map[string]float64{"yes": 0.71, "no": 0.29},
		Coverage:     ports.Coverage{Represented: 0, Declared: 2},
	}
	got := mustCheck(t, []app.Rule{rule}, nil, nil, []ports.Answer{a})[0]
	if got.State != app.RuleTripped || !strings.Contains(got.Evidence, "coverage 0 of 2") {
		t.Errorf("evidence = %q, want tripped with coverage 0 of 2 noted", got.Evidence)
	}
}

func TestAJudgedRuleWithoutItsAnswerIsAnError(t *testing.T) {
	if _, err := app.CheckRules(parsed(t)[2:3], nil, nil, nil); err == nil {
		t.Error("a judged rule with no answer was checked")
	}
}

func TestARuleQuestionUsesTheRulesAsk(t *testing.T) {
	rules := parsed(t)
	spoilers, sequels := app.RuleQuestion(rules[2]), app.RuleQuestion(rules[3])
	if spoilers.ID != "rule_no-spoilers" || spoilers.Kind != ports.KindNoul || spoilers.Ask != "Does the blurb reveal the ending?" {
		t.Errorf("no-spoilers question = %+v", spoilers)
	}
	if sequels.ID != "rule_no-sequels" || sequels.Kind != ports.KindNoul || sequels.Ask != "Is this book a sequel?" {
		t.Errorf("no-sequels question = %+v", sequels)
	}
}

func TestRuleInputsCollectsMappedFieldsOnly(t *testing.T) {
	doc := map[string]any{"pages": 412.0}
	fields := map[string]string{"min-pages": "pages", "in-print": "print.status"}
	inputs := app.RuleInputs(doc, fields)

	mapped, ok := inputs["min-pages"].(map[string]any)
	if !ok || mapped["field"] != "pages" || mapped["value"] != 412.0 {
		t.Errorf("min-pages = %+v, want field pages, value 412", inputs["min-pages"])
	}

	missing, ok := inputs["in-print"].(map[string]any)
	if !ok || missing["field"] != "print.status" || missing["value"] != nil {
		t.Errorf("in-print = %+v, want field print.status, value nil", inputs["in-print"])
	}

	if _, present := inputs["no-spoilers"]; present {
		t.Errorf("an unmapped rule id must be absent: %+v", inputs)
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
