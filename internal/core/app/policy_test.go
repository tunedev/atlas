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

var denyOnMissingField = app.PolicyRule{ID: "no-frontend", Decision: "deny", When: []app.Condition{{Field: "answers.focus", Op: "==", Value: "frontend"}}}
var denyOnIncomparableType = app.PolicyRule{ID: "bad-type", Decision: "deny", When: []app.Condition{{Field: "answers.size", Op: "==", Value: 5.0}}}

// TestADenyRuleThatCannotBeCheckedCapsAtAskNotAllow proves the gate does not
// fail open: a deny rule whose field is absent from the row must not be
// silently skipped while a matching allow rule goes through.
func TestADenyRuleThatCannotBeCheckedCapsAtAskNotAllow(t *testing.T) {
	row := policyRow("take", 0.9) // no answers.focus on this row
	d, ok := app.Decide(row, []app.PolicyRule{allowStrong, denyOnMissingField})
	if !ok || d.Decision != "ask" {
		t.Fatalf("%+v, %v; want ask, not allow", d, ok)
	}
	if !strings.Contains(d.Because, "no-frontend") || !strings.Contains(d.Because, "answers.focus") {
		t.Errorf("because = %q, want it to name the unchecked rule and field", d.Because)
	}
}

// TestADenyRuleWithAnIncomparableValueCapsAtAskNotAllow is the same proof
// for a deny rule whose value cannot be compared with the row's value
// (a type mismatch, such as a typo'd numeric value against a string field).
func TestADenyRuleWithAnIncomparableValueCapsAtAskNotAllow(t *testing.T) {
	row := policyRow("take", 0.9) // answers.size is the string "large"
	d, ok := app.Decide(row, []app.PolicyRule{allowStrong, denyOnIncomparableType})
	if !ok || d.Decision != "ask" {
		t.Fatalf("%+v, %v; want ask, not allow", d, ok)
	}
	if !strings.Contains(d.Because, "bad-type") {
		t.Errorf("because = %q, want it to name the unchecked rule", d.Because)
	}
}

// TestAnAllowRuleThatCannotBeCheckedSimplyDoesNotMatch is the mirror case:
// unlike a deny rule, an allow rule whose conditions cannot be evaluated
// must not cap or otherwise change the decision.
func TestAnAllowRuleThatCannotBeCheckedSimplyDoesNotMatch(t *testing.T) {
	allowOnMissingField := app.PolicyRule{ID: "allow-frontend", Decision: "allow", When: []app.Condition{{Field: "answers.focus", Op: "==", Value: "frontend"}}}
	row := policyRow("take", 0.9)
	d, ok := app.Decide(row, []app.PolicyRule{allowOnMissingField})
	if !ok || d.Decision != "ask" || d.Because != "no rule matched" {
		t.Errorf("%+v, %v; want the default ask, the rule not counted at all", d, ok)
	}
}

// TestARuleStateThatIsNotClearOrTrippedCapsAtAskNotAllow proves ruleStates
// fails safe on any state it does not recognise: a typo, an empty state,
// or a non-string state all join the unknown list rather than being
// silently ignored.
func TestARuleStateThatIsNotClearOrTrippedCapsAtAskNotAllow(t *testing.T) {
	row := policyRow("take", 0.9, "oven", "installing") // not clear, tripped or unknown
	d, ok := app.Decide(row, []app.PolicyRule{allowStrong})
	if !ok || d.Decision != "ask" {
		t.Errorf("%+v, %v; want ask for an unrecognised rule state", d, ok)
	}
}

// TestAMalformedRulesValueCapsAtAskNotAllow proves a "rules" value that is
// present but not a list is treated as unknown (cap at ask), not as no
// rules at all.
func TestAMalformedRulesValueCapsAtAskNotAllow(t *testing.T) {
	row := policyRow("take", 0.9)
	row["rules"] = "not-a-list"
	d, ok := app.Decide(row, []app.PolicyRule{allowStrong})
	if !ok || d.Decision != "ask" {
		t.Errorf("%+v, %v; want ask for a malformed rules value", d, ok)
	}
}

// TestAnAbsentRulesValueIsTreatedAsNoRules proves the absent case is
// distinct from the malformed case: a row that never set "rules" at all
// still allows normally.
func TestAnAbsentRulesValueIsTreatedAsNoRules(t *testing.T) {
	row := map[string]any{"subject_id": "s", "verdict": "take", "p": 0.9}
	d, ok := app.Decide(row, []app.PolicyRule{allowStrong})
	if !ok || d.Decision != "allow" {
		t.Errorf("%+v, %v; want allow with no rules field at all", d, ok)
	}
}

// TestConditionOrderDoesNotChangeTheDecision proves the AND of a rule's
// conditions is order-independent: a deny rule with one condition that is
// definitely false (verdict is "reject", not "apply") and one that cannot
// be checked (no "answers.focus" on the row) must not hold, whichever
// condition is written first, so a matching allow rule still allows.
func TestConditionOrderDoesNotChangeTheDecision(t *testing.T) {
	row := map[string]any{"subject_id": "s", "verdict": "reject", "p": 0.9,
		"answers": map[string]any{"size": "large"}} // no "focus" field
	unresolvedFirst := app.PolicyRule{ID: "deny-x", Decision: "deny", When: []app.Condition{
		{Field: "answers.focus", Op: "==", Value: "x"},
		{Field: "verdict", Op: "==", Value: "apply"},
	}}
	falseFirst := app.PolicyRule{ID: "deny-x", Decision: "deny", When: []app.Condition{
		{Field: "verdict", Op: "==", Value: "apply"},
		{Field: "answers.focus", Op: "==", Value: "x"},
	}}
	allowReject := app.PolicyRule{ID: "always-allow", Decision: "allow", When: []app.Condition{{Field: "verdict", Op: "==", Value: "reject"}}}

	d1, ok1 := app.Decide(row, []app.PolicyRule{unresolvedFirst, allowReject})
	d2, ok2 := app.Decide(row, []app.PolicyRule{falseFirst, allowReject})
	if !ok1 || !ok2 || d1.Decision != "allow" || d2.Decision != "allow" {
		t.Errorf("unresolved-first = %+v %v, false-first = %+v %v; want both allow", d1, ok1, d2, ok2)
	}
}

func TestParsePolicyRejectsAMalformedRule(t *testing.T) {
	for name, body := range map[string]string{
		"no id":           `{"rules":[{"decision":"deny","when":[{"field":"p","op":">","value":1}]}]}`,
		"repeated id":     `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">","value":1}]},{"id":"a","decision":"allow","when":[{"field":"p","op":">","value":1}]}]}`,
		"ask rule":        `{"rules":[{"id":"a","decision":"ask","when":[{"field":"p","op":">","value":1}]}]}`,
		"no when":         `{"rules":[{"id":"a","decision":"deny","when":[]}]}`,
		"bad op":          `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":"~","value":1}]}]}`,
		"no value":        `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">"}]}]}`,
		"string order":    `{"rules":[{"id":"a","decision":"deny","when":[{"field":"p","op":">","value":"x"}]}]}`,
		"empty field":     `{"rules":[{"id":"a","decision":"deny","when":[{"field":"","op":">","value":1}]}]}`,
		"no rules key":    `{}`,
		"null rules":      `{"rules": null}`,
		"unknown top key": `{"rule":[{"id":"a","decision":"deny","when":[{"field":"p","op":">","value":1}]}]}`,
		"not json":        `{`,
	} {
		_, err := app.ParsePolicy([]byte(body))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "policy: ") {
			t.Errorf("%s: error %q lacks the policy: prefix", name, err.Error())
		}
	}
}
