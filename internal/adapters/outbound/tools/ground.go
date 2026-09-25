package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/app"
)

// QuoteGround marks every quote-bearing object in a JSON document as
// grounded or needs_review against source text, so a value the source does
// not support is shown as a gap rather than stated as fact.
type QuoteGround struct{}

func NewQuoteGround() *QuoteGround { return &QuoteGround{} }

func (q *QuoteGround) Name() string { return "quote.ground" }

func (q *QuoteGround) Invoke(_ context.Context, with map[string]string) (any, error) {
	if strings.TrimSpace(with["source"]) == "" {
		return nil, fmt.Errorf("quote.ground: no source text")
	}
	fields := with["fields"]
	if !json.Valid([]byte(fields)) {
		return nil, fmt.Errorf("quote.ground: fields is not valid json")
	}
	tree, flagged, err := app.Annotate(json.RawMessage(fields), with["source"])
	if err != nil {
		return nil, fmt.Errorf("quote.ground: %w", err)
	}
	return map[string]any{"fields": tree, "needs_review": flagged}, nil
}
