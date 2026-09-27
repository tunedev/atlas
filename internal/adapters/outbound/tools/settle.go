package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
)

// ClaimsSettle keeps each claim's checked citations and marks a claim with
// none left as a gap, so an unsupported claim is shown as missing, never
// stated.
type ClaimsSettle struct{}

func NewClaimsSettle() *ClaimsSettle { return &ClaimsSettle{} }

func (s *ClaimsSettle) Name() string { return "claims.settle" }

func (s *ClaimsSettle) Invoke(_ context.Context, with map[string]string) (any, error) {
	var tree any
	if err := json.Unmarshal([]byte(with["fields"]), &tree); err != nil {
		return nil, fmt.Errorf("claims.settle: fields: %w", err)
	}
	settled := app.Settle(tree)
	return map[string]any{"fields": settled.Tree, "kept": settled.Kept, "gaps": settled.Gaps, "gaps_text": settled.GapsText, "gaps_all": settled.GapsAll}, nil
}
