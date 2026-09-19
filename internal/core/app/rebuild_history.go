package app

import (
	"context"
	"fmt"

	"github.com/tunedev/atlas/internal/core/ports"
)

// RebuildHistory discards the index and derives it again from every
// revision of every path in the record, not just the current one. It is the
// counterpart to Rebuild for a revision-keyed index such as duckindex: where
// Rebuild reads the current body of each path once, RebuildHistory walks
// each path's full History and reads every revision with GetAt, so a
// revision-keyed index recovers every row it held, not only the newest one
// per path.
//
// extract is called once per revision with that revision's body. Its
// returned Rev and When are overwritten from the revision's own DocMeta
// before Upsert, so extract never needs to know which revision it was
// handed; it only interprets the body.
func RebuildHistory(ctx context.Context, docs ports.Docs, idx ports.Index, extract func(path string, body []byte) (ports.Record, bool)) error {
	if err := idx.Reset(ctx); err != nil {
		return fmt.Errorf("rebuild history: reset: %w", err)
	}
	paths, err := docs.List(ctx, "")
	if err != nil {
		return fmt.Errorf("rebuild history: list: %w", err)
	}
	for _, p := range paths {
		history, err := docs.History(ctx, p)
		if err != nil {
			return fmt.Errorf("rebuild history: history %s: %w", p, err)
		}
		for _, meta := range history {
			body, err := docs.GetAt(ctx, p, meta.Rev)
			if err != nil {
				return fmt.Errorf("rebuild history: read %s@%s: %w", p, meta.Rev, err)
			}
			rec, ok := extract(p, body)
			if !ok {
				continue
			}
			rec.Rev = meta.Rev
			rec.When = meta.When
			if err := idx.Upsert(ctx, rec); err != nil {
				return fmt.Errorf("rebuild history: index %s@%s: %w", p, meta.Rev, err)
			}
		}
	}
	return nil
}
