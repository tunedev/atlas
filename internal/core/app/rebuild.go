package app

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/ports"
)

// pathIndex is an Index that holds one row per path, keyed on path alone.
// Rebuild's current-state walk is only correct against this shape: pointed
// at a revision-keyed index it would silently keep the wrong revision (see
// RebuildHistory). Declaring the marker here, at the consumer, is what makes
// the mismatch a compile error instead of a runtime data-loss bug.
type pathIndex interface {
	ports.Index
	PathKeyed()
}

// Rebuild discards the index and derives it again from the record. An index
// that cannot be rebuilt is not derived; it is a second system of record.
func Rebuild(ctx context.Context, docs ports.Docs, idx pathIndex, extract func(path string, body []byte) (ports.Record, bool)) error {
	if err := idx.Reset(ctx); err != nil {
		return fmt.Errorf("rebuild: reset: %w", err)
	}
	paths, err := docs.List(ctx, "")
	if err != nil {
		return fmt.Errorf("rebuild: list: %w", err)
	}
	for _, p := range paths {
		body, err := docs.Get(ctx, p)
		if err != nil {
			return fmt.Errorf("rebuild: read %s: %w", p, err)
		}
		rec, ok := extract(p, body)
		if !ok {
			continue
		}
		if err := idx.Upsert(ctx, rec); err != nil {
			return fmt.Errorf("rebuild: index %s: %w", p, err)
		}
	}
	return nil
}
