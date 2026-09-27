package app

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Agreement is how often a recorded decision matched the verdict the model
// had given when the person decided. Rate is nil below MinSample.
type Agreement struct {
	N, Agreed int
	Rate      *float64
}

// AgreementRate counts the decisions that followed a judgement and how many
// of them chose what the verdict said. The two are compared as strings, so
// the rate means something only when a pack's choices and its verdict
// question share option names.
func AgreementRate(ctx context.Context, index ports.Index) (Agreement, error) {
	rows, err := index.Find(ctx, ports.Query{Kind: "decision"})
	if err != nil {
		return Agreement{}, fmt.Errorf("agreement: find: %w", err)
	}
	var a Agreement
	for _, r := range rows {
		verdict := r.Fields["verdict_at_decision"]
		if verdict == "" {
			continue
		}
		a.N++
		if r.Fields["decision"] == verdict {
			a.Agreed++
		}
	}
	if a.N >= MinSample {
		rate := float64(a.Agreed) / float64(a.N)
		a.Rate = &rate
	}
	return a, nil
}
