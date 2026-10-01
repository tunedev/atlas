package tools

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// JudgeAssess re-assesses a recorded judgement: its verdict, the reasons
// behind it, and the ids of the rules it tripped, built by the same Assess
// judge.each uses. with carries path (the judgement document) and verdict
// (the id of the question whose answer is the verdict).
type JudgeAssess struct{ docs ports.Docs }

func NewJudgeAssess(docs ports.Docs) *JudgeAssess { return &JudgeAssess{docs: docs} }

func (t *JudgeAssess) Name() string { return "judge.assess" }

func (t *JudgeAssess) Invoke(ctx context.Context, with map[string]string) (any, error) {
	if with["path"] == "" {
		return nil, fmt.Errorf("judge.assess: no path")
	}
	if with["verdict"] == "" {
		return nil, fmt.Errorf("judge.assess: no verdict")
	}
	stored, err := app.ReadJudgement(ctx, t.docs, with["path"])
	if err != nil {
		return nil, fmt.Errorf("judge.assess: %w", err)
	}
	a, err := app.Assess(with["verdict"], stored.Answers, stored.Rules)
	if err != nil {
		return nil, fmt.Errorf("judge.assess: %w", err)
	}
	reasons := make([]any, len(a.Reasons))
	for i, r := range a.Reasons {
		reasons[i] = r
	}
	tripped := []any{}
	for _, r := range a.Rules {
		if r.State == app.RuleTripped {
			tripped = append(tripped, r.ID)
		}
	}
	return map[string]any{"verdict": a.Verdict, "reasons": reasons, "tripped": tripped}, nil
}
