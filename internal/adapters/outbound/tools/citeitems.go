package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/app"
)

// ItemsCite pairs each grounded item with the spans a citing step named for
// it, so every item appears exactly once: an uncited item survives with no
// spans, never silently vanishes.
type ItemsCite struct{}

func NewItemsCite() *ItemsCite { return &ItemsCite{} }

func (i *ItemsCite) Name() string { return "items.cite" }

func (i *ItemsCite) Invoke(_ context.Context, with map[string]string) (any, error) {
	var items []any
	if err := json.Unmarshal([]byte(with["items"]), &items); err != nil {
		return nil, fmt.Errorf("items.cite: items: %w", err)
	}
	var cited []any
	if err := json.Unmarshal([]byte(with["cited"]), &cited); err != nil {
		return nil, fmt.Errorf("items.cite: cited: %w", err)
	}
	out, dropped := app.CiteItems(items, cited)
	return map[string]any{"items": out, "dropped": dropped}, nil
}
