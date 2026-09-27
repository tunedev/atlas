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

// ItemsGather pairs each grounded item with the sentences an answering step
// wrote for it, so every item appears exactly once: an unanswered item
// survives as a gap, never silently vanishes.
type ItemsGather struct{}

func NewItemsGather() *ItemsGather { return &ItemsGather{} }

func (i *ItemsGather) Name() string { return "items.gather" }

func (i *ItemsGather) Invoke(_ context.Context, with map[string]string) (any, error) {
	var items []any
	if err := json.Unmarshal([]byte(with["items"]), &items); err != nil {
		return nil, fmt.Errorf("items.gather: items: %w", err)
	}
	var answered []any
	if err := json.Unmarshal([]byte(with["answered"]), &answered); err != nil {
		return nil, fmt.Errorf("items.gather: answered: %w", err)
	}
	out, dropped := app.GatherItems(items, answered)
	return map[string]any{"items": out, "dropped": dropped}, nil
}

// TextLines turns text into one grounded item per non-blank line, so a list
// a person typed becomes items without a model reading it.
type TextLines struct{}

func NewTextLines() *TextLines { return &TextLines{} }

func (l *TextLines) Name() string { return "text.lines" }

func (l *TextLines) Invoke(_ context.Context, with map[string]string) (any, error) {
	return map[string]any{"items": app.LineItems(with["text"])}, nil
}
