package app_test

import (
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
)

func policyRow(verdict string, p float64, rules ...string) map[string]any {
	rs := []any{}
	for i := 0; i+1 < len(rules); i += 2 {
		rs = append(rs, map[string]any{"id": rules[i], "state": rules[i+1]})
	}
	return map[string]any{"subject_id": "s", "verdict": verdict, "p": p,
		"answers": map[string]any{"size": "large"}, "rules": rs}
}

var allowStrong = app.PolicyRule{ID: "strong", Decision: "allow", When: []app.Condition{{Field: "verdict", Op: "==", Value: "take"}, {Field: "p", Op: ">=", Value: 0.7}}}
var denyLarge = app.PolicyRule{ID: "no-large", Decision: "deny", When: []app.Condition{{Field: "answers.size", Op: "==", Value: "large"}}}

func TestDecidePrecedence(t *testing.T) {
	cases := []struct {
		name  string
		row   map[string]any
		rules []app.PolicyRule
		want  string
	}{
		{"tripped beats allow", policyRow("take", 0.9, "nuts", "tripped"), []app.PolicyRule{allowStrong}, "deny"},
		{"deny beats allow, allow first", policyRow("take", 0.9), []app.PolicyRule{allowStrong, denyLarge}, "deny"},
		{"deny beats allow, deny first", policyRow("take", 0.9), []app.PolicyRule{denyLarge, allowStrong}, "deny"},
		{"unknown caps at ask", policyRow("take", 0.9, "nuts", "unknown"), []app.PolicyRule{allowStrong}, "ask"},
		{"allow", policyRow("take", 0.9, "nuts", "clear"), []app.PolicyRule{allowStrong}, "allow"},
		{"allow condition not met", policyRow("take", 0.5), []app.PolicyRule{allowStrong}, "ask"},
		{"default ask", policyRow("take", 0.9), nil, "ask"},
	}
	for _, c := range cases {
		d, ok := app.Decide(c.row, c.rules)
		if !ok || d.Decision != c.want {
			t.Errorf("%s: %+v, %v; want %s", c.name, d, ok, c.want)
		}
	}
}

func TestDecideNamesWhatMatched(t *testing.T) {
	d, _ := app.Decide(policyRow("take", 0.9, "nuts", "tripped"), []app.PolicyRule{allowStrong})
	if !strings.Contains(d.Because, "nuts") || len(d.Matched) != 1 || d.Matched[0] != "strong" {
		t.Errorf("%+v", d)
	}
}

func TestDecideLeavesAnErrorRowUndecided(t *testing.T) {
	if _, ok := app.Decide(map[string]any{"subject_id": "s", "error": "boom"}, nil); ok {
		t.Error("an error row was decided")
	}
}

func TestParsePolicyRejectsAMalformedRule(t *testing.T) {
	for name, body := range map[string]string{
		"no id":        `{"rules":[{"decision":"deny","when":[{"field":"p","op":">","value":1}]}]}`,
		"repeated id":  `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">","value":1}]},{"id":"a","decision":"allow","when":[{"field":"p","op":">","value":1}]}]}`,
		"ask rule":     `{"rules":[{"id":"a","decision":"ask","when":[{"field":"p","op":">","value":1}]}]}`,
		"no when":      `{"rules":[{"id":"a","decision":"deny","when":[]}]}`,
		"bad op":       `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":"~","value":1}]}]}`,
		"no value":     `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">"}]}]}`,
		"string order": `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">","value":"x"}]}]}`,
		"not json":     `{`,
	} {
		if _, err := app.ParsePolicy([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
