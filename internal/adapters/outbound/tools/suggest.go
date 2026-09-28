package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// defaultMinRepeats is how many subjects must share a choice before
// policy.suggest proposes a rule, when the pack does not say.
const defaultMinRepeats = 3

// PolicySuggest proposes policy rules from repeated decisions. It reads the
// index and writes nothing.
type PolicySuggest struct {
	index ports.Index
}

func NewPolicySuggest(index ports.Index) *PolicySuggest {
	return &PolicySuggest{index: index}
}

func (t *PolicySuggest) Name() string { return "policy.suggest" }

func (t *PolicySuggest) Invoke(ctx context.Context, with map[string]string) (any, error) {
	choices, err := parseChoices(with["choices"])
	if err != nil {
		return nil, fmt.Errorf("policy.suggest: %w", err)
	}
	minRepeats, err := parseMinRepeats(with["min_repeats"])
	if err != nil {
		return nil, fmt.Errorf("policy.suggest: %w", err)
	}
	rows, err := t.index.Find(ctx, ports.Query{Kind: "decision"})
	if err != nil {
		return nil, fmt.Errorf("policy.suggest: %w", err)
	}
	candidates := app.Suggest(rows, choices, minRepeats)
	if candidates == nil {
		candidates = []app.Candidate{}
	}
	return map[string]any{"candidates": candidates}, nil
}

// parseChoices decodes the YAML map from a recorded choice to the policy
// decision it stands for. At least one choice is required.
func parseChoices(raw string) (map[string]string, error) {
	choices := map[string]string{}
	if err := yaml.Unmarshal([]byte(raw), &choices); err != nil {
		return nil, fmt.Errorf("choices: %w", err)
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("choices is empty")
	}
	return choices, nil
}

// parseMinRepeats reads min_repeats, an integer of at least 2, defaulting
// to defaultMinRepeats when empty.
func parseMinRepeats(raw string) (int, error) {
	if raw == "" {
		return defaultMinRepeats, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 2 {
		return 0, fmt.Errorf("min_repeats %q is not an integer of at least 2", raw)
	}
	return n, nil
}

// PolicyAdd appends one rule to the policy document at a record path,
// creating it when absent, and commits it with the evidence in the message.
// The whole resulting document must parse as a policy, so a duplicate id or
// a rule policy.decide could not apply is refused and nothing is written.
type PolicyAdd struct {
	docs  ports.Docs
	index ports.Index
}

func NewPolicyAdd(docs ports.Docs, index ports.Index) *PolicyAdd {
	return &PolicyAdd{docs: docs, index: index}
}

func (t *PolicyAdd) Name() string { return "policy.add" }

func (t *PolicyAdd) Invoke(ctx context.Context, with map[string]string) (any, error) {
	policyPath := with["policy"]
	var rule app.PolicyRule
	if err := json.Unmarshal([]byte(with["rule"]), &rule); err != nil {
		return nil, fmt.Errorf("policy.add: rule: %w", err)
	}
	rules, err := t.readRules(ctx, policyPath)
	if err != nil {
		return nil, fmt.Errorf("policy.add: %w", err)
	}
	body, err := json.MarshalIndent(map[string]any{"rules": append(rules, rule)}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("policy.add: encode: %w", err)
	}
	if _, err := app.ParsePolicy(body); err != nil {
		return nil, fmt.Errorf("policy.add: %w", err)
	}
	_, err = app.RecordDocument(ctx, t.docs, t.index, app.Document{
		Path:    policyPath,
		Body:    body,
		Message: fmt.Sprintf("Promote policy rule %s: %s", rule.ID, with["evidence"]),
		Kind:    "profile",
		Fields:  map[string]string{"section": "policy"},
		When:    time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("policy.add: %w", err)
	}
	return map[string]any{"path": policyPath, "id": rule.ID}, nil
}

// readRules returns the rules of the policy at policyPath, or none when no
// document is there yet.
func (t *PolicyAdd) readRules(ctx context.Context, policyPath string) ([]app.PolicyRule, error) {
	dir := path.Dir(policyPath)
	if dir == "." {
		dir = ""
	}
	paths, err := t.docs.List(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	if !slices.Contains(paths, policyPath) {
		return nil, nil
	}
	body, err := t.docs.Get(ctx, policyPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", policyPath, err)
	}
	rules, err := app.ParsePolicy(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", policyPath, err)
	}
	return rules, nil
}
