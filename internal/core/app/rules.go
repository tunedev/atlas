package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

const (
	RuleComparable = "comparable"
	RuleJudged     = "judged"

	RuleTripped = "tripped"
	RuleClear   = "clear"
	RuleUnknown = "unknown"

	defaultRuleThreshold = 0.5
	ruleQuestionPrefix   = "rule_"
)

// Rule is one condition a subject must not meet. A comparable rule is
// checked in code against a field of the subject's document; a judged rule
// is asked of the Judge as the yes/no question in Ask, and trips when p(yes)
// reaches Threshold.
type Rule struct {
	ID        string  `json:"id"`
	Statement string  `json:"statement"`
	Kind      string  `json:"kind"`
	Op        string  `json:"op,omitempty"`
	Value     any     `json:"value,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
	Ask       string  `json:"ask,omitempty"`
}

// RuleResult is what checking one rule found. State is tripped, clear or
// unknown; Evidence says why, in words a person can check.
type RuleResult struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Evidence string `json:"evidence"`
}

// ParseRules decodes a rules document, {"rules": [...]}. It rejects a rule
// with no id, a repeated id, an unknown kind, a comparable rule whose
// operator it cannot apply, and a judged threshold outside [0, 1]. A judged
// rule with no threshold gets the default of 0.5.
func ParseRules(body []byte) ([]Rule, error) {
	var doc struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("rules: decode: %w", err)
	}
	seen := make(map[string]bool, len(doc.Rules))
	for i, r := range doc.Rules {
		if r.ID == "" {
			return nil, fmt.Errorf("rules: rule %d has no id", i)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("rules: %s is listed twice", r.ID)
		}
		seen[r.ID] = true
		switch r.Kind {
		case RuleComparable:
			if !validOp(r.Op) {
				return nil, fmt.Errorf("rules: %s: unknown operator %q", r.ID, r.Op)
			}
			if err := checkComparableValue(r); err != nil {
				return nil, err
			}
		case RuleJudged:
			if strings.TrimSpace(r.Ask) == "" {
				return nil, fmt.Errorf("rules: %s: a judged rule needs ask, the yes/no question whose yes means it is broken", r.ID)
			}
			if r.Threshold < 0 || r.Threshold > 1 {
				return nil, fmt.Errorf("rules: %s: threshold %v is outside [0, 1]", r.ID, r.Threshold)
			}
			if r.Threshold == 0 {
				doc.Rules[i].Threshold = defaultRuleThreshold
			}
		default:
			return nil, fmt.Errorf("rules: %s: unknown kind %q", r.ID, r.Kind)
		}
	}
	return doc.Rules, nil
}

// checkComparableValue rejects a comparable rule whose value can never trip
// it: none at all, a non-number under an ordering operator, or, under ==
// or !=, a value that is neither a number nor a string.
func checkComparableValue(r Rule) error {
	if r.Value == nil {
		return fmt.Errorf("rules: %s: comparable rule has no value", r.ID)
	}
	_, isNumber := r.Value.(float64)
	switch r.Op {
	case ">=", "<=", ">", "<":
		if !isNumber {
			return fmt.Errorf("rules: %s: %s needs a number, got %v", r.ID, r.Op, r.Value)
		}
	case "==", "!=":
		_, isString := r.Value.(string)
		if !isNumber && !isString {
			return fmt.Errorf("rules: %s: %s needs a number or a string, got %v", r.ID, r.Op, r.Value)
		}
	}
	return nil
}

func validOp(op string) bool {
	switch op {
	case ">=", "<=", ">", "<", "==", "!=":
		return true
	}
	return false
}

// RuleQuestionID is the question id a judged rule is asked under.
func RuleQuestionID(ruleID string) string { return ruleQuestionPrefix + ruleID }

// RuleQuestion is the yes/no question a judged rule is asked as, worded by
// the rule's own Ask.
func RuleQuestion(r Rule) ports.Question {
	return ports.Question{ID: RuleQuestionID(r.ID), Kind: ports.KindNoul, Ask: r.Ask}
}

// CheckRules checks every rule against one subject: comparable rules
// against doc through fields (rule id to a dotted path), judged rules
// against their answers. A judged rule whose answer is missing is an error,
// since its question should have been asked in the same call.
func CheckRules(rules []Rule, doc any, fields map[string]string, answers []ports.Answer) ([]RuleResult, error) {
	byID := make(map[string]ports.Answer, len(answers))
	for _, a := range answers {
		byID[a.ID] = a
	}
	results := make([]RuleResult, 0, len(rules))
	for _, r := range rules {
		if r.Kind == RuleComparable {
			results = append(results, checkComparable(r, doc, fields[r.ID]))
			continue
		}
		a, ok := byID[RuleQuestionID(r.ID)]
		if !ok {
			return nil, fmt.Errorf("rules: %s: no answer to its question", r.ID)
		}
		results = append(results, checkJudged(r, a))
	}
	return results, nil
}

// checkComparable trips r when the value at field fails r's comparison: a
// floor of ">= 100" trips on 90. A rule with no field, a field absent from
// doc, or a value that cannot be compared with r.Value is unknown, never
// tripped: a missing number is not a failing one.
func checkComparable(r Rule, doc any, field string) RuleResult {
	if field == "" {
		return RuleResult{ID: r.ID, State: RuleUnknown, Evidence: "no field is mapped to this rule"}
	}
	got, ok := lookupPath(doc, field)
	if !ok {
		return RuleResult{ID: r.ID, State: RuleUnknown, Evidence: field + " not in the subject"}
	}
	holds, ok := compare(got, r.Op, r.Value)
	if !ok {
		return RuleResult{ID: r.ID, State: RuleUnknown, Evidence: fmt.Sprintf("%s is %s, which cannot be compared with %s", field, show(got), show(r.Value))}
	}
	state := RuleTripped
	if holds {
		state = RuleClear
	}
	return RuleResult{ID: r.ID, State: state, Evidence: fmt.Sprintf("%s is %s, rule needs %s %s", field, show(got), r.Op, show(r.Value))}
}

// checkJudged trips r when the probability of yes reaches its threshold. An
// answer whose alternatives named none of its declared options gets that
// noted in its evidence, since the probability then measured nothing.
func checkJudged(r Rule, a ports.Answer) RuleResult {
	p := a.Distribution["yes"]
	state, evidence := RuleClear, fmt.Sprintf("p(yes) %.2f < %.2f", p, r.Threshold)
	if p >= r.Threshold {
		state, evidence = RuleTripped, fmt.Sprintf("p(yes) %.2f >= %.2f", p, r.Threshold)
	}
	if a.Coverage.Declared > 0 && a.Coverage.Represented == 0 {
		evidence += fmt.Sprintf(", coverage 0 of %d", a.Coverage.Declared)
	}
	return RuleResult{ID: r.ID, State: state, Evidence: evidence}
}

// RuleInputs collects, for every rule id mapped in fields, the field path
// and its value as resolved from doc: {"field": path, "value": v}, with v
// nil when the field is absent. A rule id with no mapping in fields is
// absent from the result. It feeds Fingerprint, so a judgement is never
// reused across a changed mapping or a changed field value.
func RuleInputs(doc any, fields map[string]string) map[string]any {
	inputs := make(map[string]any, len(fields))
	for id, path := range fields {
		v, ok := lookupPath(doc, path)
		if !ok {
			v = nil
		}
		inputs[id] = map[string]any{"field": path, "value": v}
	}
	return inputs
}

// lookupPath follows a dotted path through nested objects. A null, a
// non-object on the way, or an absent key is not found.
func lookupPath(doc any, path string) (any, bool) {
	cur := doc
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[key]; !ok || cur == nil {
			return nil, false
		}
	}
	return cur, true
}

// compare reports whether got op want holds. Numbers take every operator;
// strings take == and != only. Any other pairing cannot be compared.
func compare(got any, op string, want any) (holds, ok bool) {
	if g, gok := got.(float64); gok {
		w, wok := want.(float64)
		if !wok {
			return false, false
		}
		switch op {
		case ">=":
			return g >= w, true
		case "<=":
			return g <= w, true
		case ">":
			return g > w, true
		case "<":
			return g < w, true
		case "==":
			return g == w, true
		case "!=":
			return g != w, true
		}
		return false, false
	}
	g, gok := got.(string)
	w, wok := want.(string)
	if !gok || !wok {
		return false, false
	}
	switch op {
	case "==":
		return g == w, true
	case "!=":
		return g != w, true
	}
	return false, false
}

// show renders a compared value the way a person would write it.
func show(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
