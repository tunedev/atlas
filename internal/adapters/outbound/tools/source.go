package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// SourcePull returns what a ports.Source holds, decoded, with how fresh it
// is. with["prefix"] keeps items whose id sits under that directory;
// with["match"], a "field=value" pair, keeps items whose top-level field
// equals value. Both are optional.
//
// The result is {"items": [...], "_meta": {"fetched_at", "age_hours",
// "stale", "count"}}. A source older than staleAfter is still returned and
// reported: marked stale and logged as a warning. What to do about stale
// data is the pack's decision.
type SourcePull struct {
	source     ports.Source
	staleAfter time.Duration
	log        *slog.Logger
}

func NewSourcePull(source ports.Source, staleAfter time.Duration, log *slog.Logger) *SourcePull {
	return &SourcePull{source: source, staleAfter: staleAfter, log: log}
}

func (t *SourcePull) Name() string { return "source.pull" }

func (t *SourcePull) Invoke(ctx context.Context, with map[string]string) (any, error) {
	keep, err := filter(with["prefix"], with["match"])
	if err != nil {
		return nil, fmt.Errorf("source.pull: %w", err)
	}
	items, err := t.source.Pull(ctx)
	if err != nil {
		return nil, fmt.Errorf("source.pull: %w", err)
	}
	refreshed, err := t.source.LastRefreshed(ctx)
	if err != nil {
		return nil, fmt.Errorf("source.pull: %w", err)
	}

	kept := []any{}
	for _, it := range items {
		var doc any
		if err := json.Unmarshal(it.Body, &doc); err != nil {
			return nil, fmt.Errorf("source.pull: decode %s: %w", it.ID, err)
		}
		if keep(it.ID, doc) {
			kept = append(kept, doc)
		}
	}

	age := time.Since(refreshed)
	stale := age > t.staleAfter
	if stale {
		t.log.WarnContext(ctx, "source is stale",
			"age", age.Round(time.Minute).String(),
			"stale_after", t.staleAfter.String(),
			"last_refreshed", refreshed.UTC().Format(time.RFC3339))
	}
	return map[string]any{
		"items": kept,
		"_meta": map[string]any{
			"fetched_at": refreshed.UTC().Format(time.RFC3339),
			"age_hours":  math.Round(age.Hours()*10) / 10,
			"stale":      stale,
			"count":      len(kept),
		},
	}, nil
}

// filter builds the predicate for prefix and match. prefix is a directory
// boundary, as Docs.List treats one: "a" keeps "a/one" but not "ab/two".
func filter(prefix, match string) (func(id string, doc any) bool, error) {
	boundary := strings.TrimSuffix(prefix, "/")
	field, value, hasPair := strings.Cut(match, "=")
	if match != "" && (!hasPair || field == "") {
		return nil, fmt.Errorf("match %q must be field=value", match)
	}
	return func(id string, doc any) bool {
		if boundary != "" && !strings.HasPrefix(id, boundary+"/") {
			return false
		}
		if match == "" {
			return true
		}
		fields, ok := doc.(map[string]any)
		return ok && fields[field] == value
	}, nil
}
