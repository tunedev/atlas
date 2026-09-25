package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tunedev/atlas/internal/core/app"
)

// Dedupe merges lists of items from any number of sources, counting an item
// once when its key fields agree. What the fields mean is the pack's
// business.
type Dedupe struct{}

func NewDedupe() *Dedupe { return &Dedupe{} }

func (d *Dedupe) Name() string { return "items.dedupe" }

func (d *Dedupe) Invoke(_ context.Context, with map[string]string) (any, error) {
	fields := keyFields(with["key"])
	if len(fields) == 0 {
		return nil, errors.New("items.dedupe: no key fields")
	}
	var lists [][]any
	if err := json.Unmarshal([]byte(with["lists"]), &lists); err != nil {
		return nil, fmt.Errorf("items.dedupe: lists must be a JSON array of arrays: %w", err)
	}
	var all []any
	for _, l := range lists {
		all = append(all, l...)
	}
	kept, merged := app.Dedupe(all, fields)
	return map[string]any{
		"items": kept,
		"_meta": map[string]any{"in": len(all), "out": len(kept), "merged": merged},
	}, nil
}

func keyFields(raw string) []string {
	var out []string
	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
