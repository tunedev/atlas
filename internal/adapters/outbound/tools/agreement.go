package tools

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Agreement reports how often a decision agreed with the verdict before it.
type Agreement struct {
	index ports.Index
}

func NewAgreement(index ports.Index) *Agreement { return &Agreement{index: index} }

func (t *Agreement) Name() string { return "decision.agreement" }

func (t *Agreement) Invoke(ctx context.Context, _ map[string]string) (any, error) {
	a, err := app.AgreementRate(ctx, t.index)
	if err != nil {
		return nil, fmt.Errorf("decision.agreement: %w", err)
	}
	headline := fmt.Sprintf("n=%d, too few for a rate (needs %d)", a.N, app.MinSample)
	var rate any
	if a.Rate != nil {
		rate = *a.Rate
		headline = fmt.Sprintf("decisions agreed with the verdict %.0f%% of the time (n=%d)", *a.Rate*100, a.N)
	}
	return map[string]any{"n": a.N, "agreed": a.Agreed, "rate": rate, "headline": headline}, nil
}
