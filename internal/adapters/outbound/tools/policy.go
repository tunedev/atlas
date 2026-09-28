package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// PolicyDecide adds a decision to every scored row a pack hands it, by
// applying a policy document read from the record. An empty policy path
// applies no rules: a tripped rule in a row still denies, an unknown rule
// still caps at ask, and everything else asks.
type PolicyDecide struct {
	docs ports.Docs
}

func NewPolicyDecide(docs ports.Docs) *PolicyDecide {
	return &PolicyDecide{docs: docs}
}

func (t *PolicyDecide) Name() string { return "policy.decide" }

func (t *PolicyDecide) Invoke(ctx context.Context, with map[string]string) (any, error) {
	var rules []app.PolicyRule
	if path := with["policy"]; path != "" {
		body, err := t.docs.Get(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("policy.decide: %s: %w", path, err)
		}
		rules, err = app.ParsePolicy(body)
		if err != nil {
			return nil, fmt.Errorf("policy.decide: %s: %w", path, err)
		}
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(with["rows"]), &rows); err != nil {
		return nil, fmt.Errorf("policy.decide: rows: %w", err)
	}

	out := make([]any, len(rows))
	for i, row := range rows {
		out[i] = decideRow(row, rules)
	}
	return map[string]any{"rows": out}, nil
}

// decideRow copies row and, unless it is an error row, adds its decision,
// the rule ids that matched, and why.
func decideRow(row map[string]any, rules []app.PolicyRule) map[string]any {
	copied := maps.Clone(row)
	d, ok := app.Decide(row, rules)
	if !ok {
		return copied
	}
	copied["decision"] = d.Decision
	copied["matched"] = d.Matched
	copied["because"] = d.Because
	return copied
}
