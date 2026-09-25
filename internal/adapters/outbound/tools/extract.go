package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Extract turns text into a value shaped by a JSON Schema the pack supplies.
// What the schema describes is the pack's business.
type Extract struct {
	extractor ports.Extractor
}

func NewExtract(e ports.Extractor) *Extract { return &Extract{extractor: e} }

func (x *Extract) Name() string { return "extract.run" }

func (x *Extract) Invoke(ctx context.Context, with map[string]string) (any, error) {
	raw, err := x.extractor.Extract(ctx, with["text"], []byte(with["schema"]))
	if err != nil {
		return nil, fmt.Errorf("extract.run: %w", err)
	}
	var fields any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("extract.run: decode: %w", err)
	}
	return map[string]any{"fields": fields}, nil
}
