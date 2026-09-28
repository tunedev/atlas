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

// ParsePolicy decodes {"rules": [...]} and rejects a rule it could not apply.
func ParsePolicy(body []byte) ([]PolicyRule, error) {
	var doc struct {
		Rules []PolicyRule `json:"rules"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("policy: decode: %w", err)
	}
	seen := map[string]bool{}
	for i, r := range doc.Rules {
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
	return doc.Rules, nil
}

// checkCondition applies the comparable-rule checks to one condition.
func checkCondition(ruleID string, c Condition) error {
	if c.Field == "" {
		return fmt.Errorf("policy: %s: a condition has no field", ruleID)
	}
	if !validOp(c.Op) {
		return fmt.Errorf("policy: %s: unknown operator %q", ruleID, c.Op)
	}
	return checkComparableValue(Rule{ID: ruleID, Op: c.Op, Value: c.Value})
}

// Decide applies rules to row with a fixed precedence: a tripped rule in
// the row's "rules" denies; a matching deny rule denies; an unknown rule
// caps the result at ask; a matching allow rule allows; otherwise ask. A
// row carrying "error" is not decided.
func Decide(row map[string]any, rules []PolicyRule) (PolicyDecision, bool) {
	if _, failed := row["error"]; failed {
		return PolicyDecision{}, false
	}
	tripped, unknown := ruleStates(row)
	var deny, allow []string
	for _, r := range rules {
		if !conditionsHold(row, r.When) {
			continue
		}
		if r.Decision == DecisionDeny {
			deny = append(deny, r.ID)
		} else {
			allow = append(allow, r.ID)
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
	case len(allow) > 0:
		return PolicyDecision{DecisionAllow, matched, "allowed by: " + strings.Join(allow, ", ")}, true
	}
	return PolicyDecision{DecisionAsk, matched, "no rule matched"}, true
}

// ruleStates lists the ids of the row's tripped and unknown rules, as
// judge.each writes them: "rules": [{"id", "state", ...}].
func ruleStates(row map[string]any) (tripped, unknown []string) {
	list, _ := row["rules"].([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		id, _ := m["id"].(string)
		switch m["state"] {
		case RuleTripped:
			tripped = append(tripped, id)
		case RuleUnknown:
			unknown = append(unknown, id)
		}
	}
	return tripped, unknown
}

// conditionsHold reports whether every condition holds for row; a missing
// field does not hold.
func conditionsHold(row map[string]any, when []Condition) bool {
	for _, c := range when {
		got, ok := lookupPath(row, c.Field)
		if !ok {
			return false
		}
		if holds, comparable := compare(got, c.Op, c.Value); !comparable || !holds {
			return false
		}
	}
	return true
}
