package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PolicyRule allows or denies a row when every one of its conditions holds.
type PolicyRule struct {
	ID       string      `json:"id"`
	Decision string      `json:"decision"`
	When     []Condition `json:"when"`
}

// Condition compares the value at a dotted path of a row with Value, using
// the comparable-rule operators.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// PolicyDecision is what a policy decided about one row, which rules
// matched, and why, in words a person can check. Named apart from Decision
// (a person's recorded choice), which this package already defines.
type PolicyDecision struct {
	Decision string   `json:"decision"`
	Matched  []string `json:"matched"`
	Because  string   `json:"because"`
}

const (
	DecisionAllow = "allow"
	DecisionAsk   = "ask"
	DecisionDeny  = "deny"
)

// ParsePolicy decodes {"rules": [...]} and rejects a rule it could not
// apply. The rules key is required and no other top-level key is allowed,
// so a typo'd key fails loudly instead of parsing as an empty policy.
func ParsePolicy(body []byte) ([]PolicyRule, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("policy: decode: %w", err)
	}
	rulesRaw, present := top["rules"]
	if !present {
		return nil, fmt.Errorf("policy: no rules key")
	}
	delete(top, "rules")
	for key := range top {
		return nil, fmt.Errorf("policy: unknown key %q", key)
	}
	var rules []PolicyRule
	if err := json.Unmarshal(rulesRaw, &rules); err != nil {
		return nil, fmt.Errorf("policy: decode rules: %w", err)
	}

	seen := map[string]bool{}
	for i, r := range rules {
		switch {
		case r.ID == "":
			return nil, fmt.Errorf("policy: rule %d has no id", i)
		case seen[r.ID]:
			return nil, fmt.Errorf("policy: %s is listed twice", r.ID)
		case r.Decision != DecisionAllow && r.Decision != DecisionDeny:
			return nil, fmt.Errorf("policy: %s: decision must be allow or deny, got %q; ask is the default", r.ID, r.Decision)
		case len(r.When) == 0:
			return nil, fmt.Errorf("policy: %s: no conditions", r.ID)
		}
		seen[r.ID] = true
		for _, c := range r.When {
			if err := checkCondition(r.ID, c); err != nil {
				return nil, err
			}
		}
	}
	return rules, nil
}

// checkCondition applies the comparable-rule checks to one condition.
func checkCondition(ruleID string, c Condition) error {
	if c.Field == "" {
		return fmt.Errorf("policy: %s: a condition has no field", ruleID)
	}
	if !validOp(c.Op) {
		return fmt.Errorf("policy: %s: unknown operator %q", ruleID, c.Op)
	}
	if err := comparableValueError(c.Op, c.Value); err != nil {
		return fmt.Errorf("policy: %s: %w", ruleID, err)
	}
	return nil
}

// Decide applies rules to row with a fixed precedence: a tripped rule in
// the row's "rules" denies, always; a matching deny rule denies; an
// unrecognised rule state, a malformed "rules" value, or a deny rule whose
// conditions could not be checked each cap the result at ask, since a
// possible deny is never let through as an allow; a matching allow rule
// allows; otherwise ask. A row carrying "error" is not decided. An allow
// rule whose conditions could not be checked simply does not match.
func Decide(row map[string]any, rules []PolicyRule) (PolicyDecision, bool) {
	if _, failed := row["error"]; failed {
		return PolicyDecision{}, false
	}
	tripped, unknown, malformed := ruleStates(row)
	var deny, allow, unclear []string
	for _, r := range rules {
		holds, resolved, why := evalConditions(row, r.When)
		switch {
		case holds:
			if r.Decision == DecisionDeny {
				deny = append(deny, r.ID)
			} else {
				allow = append(allow, r.ID)
			}
		case !resolved && r.Decision == DecisionDeny:
			unclear = append(unclear, fmt.Sprintf("%s (%s)", r.ID, why))
		}
	}
	matched := append(append([]string{}, deny...), allow...)
	switch {
	case len(tripped) > 0:
		return PolicyDecision{DecisionDeny, matched, "tripped: " + strings.Join(tripped, ", ")}, true
	case len(deny) > 0:
		return PolicyDecision{DecisionDeny, matched, "denied by: " + strings.Join(deny, ", ")}, true
	case len(unknown) > 0:
		return PolicyDecision{DecisionAsk, matched, "unknown: " + strings.Join(unknown, ", ")}, true
	case malformed:
		return PolicyDecision{DecisionAsk, matched, "rules is malformed"}, true
	case len(unclear) > 0:
		return PolicyDecision{DecisionAsk, matched, "cannot check: " + strings.Join(unclear, ", ")}, true
	case len(allow) > 0:
		return PolicyDecision{DecisionAllow, matched, "allowed by: " + strings.Join(allow, ", ")}, true
	}
	return PolicyDecision{DecisionAsk, matched, "no rule matched"}, true
}

// ruleStates lists the ids of the row's tripped rules and every other rule
// whose state is not "clear" (an unrecognised or missing state joins the
// unknown list too, so anything the row cannot vouch for caps at ask), as
// judge.each writes them: "rules": [{"id", "state", ...}]. A row with no
// "rules" value at all carries none; one whose "rules" value is present
// but is not a list is malformed, which the caller treats like an unknown
// rule rather than silently ignoring it.
func ruleStates(row map[string]any) (tripped, unknown []string, malformed bool) {
	raw, present := row["rules"]
	if !present {
		return nil, nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, nil, true
	}
	for _, e := range list {
		m, _ := e.(map[string]any)
		id, _ := m["id"].(string)
		state, _ := m["state"].(string)
		switch state {
		case RuleClear:
		case RuleTripped:
			tripped = append(tripped, id)
		default:
			unknown = append(unknown, id)
		}
	}
	return tripped, unknown, false
}

// evalConditions reports whether every condition in when holds for row.
// resolved is false when a condition's field is absent from row or its
// value cannot be compared with the operator; holds is then meaningless,
// and why names the field responsible. Conditions are AND-ed, so the first
// one that fails or cannot be resolved stops the check.
func evalConditions(row map[string]any, when []Condition) (holds, resolved bool, why string) {
	for _, c := range when {
		got, found := lookupPath(row, c.Field)
		if !found {
			return false, false, c.Field + " missing"
		}
		ok, comparable := compare(got, c.Op, c.Value)
		if !comparable {
			return false, false, c.Field + " cannot be compared"
		}
		if !ok {
			return false, true, ""
		}
	}
	return true, true, ""
}
