package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"
)

// CrawlPull crawls the targets a pack supplies as YAML and returns their
// items decoded, with every target that yielded nothing named in failures
// and logged as a warning. A crawl that reached nothing is an error. Every
// invocation builds its Source from the same Crawler, so politeness pacing
// holds across invocations, not just within one.
type CrawlPull struct {
	crawler *crawlsource.Crawler
	log     *slog.Logger
}

func NewCrawlPull(crawler *crawlsource.Crawler, log *slog.Logger) *CrawlPull {
	return &CrawlPull{crawler: crawler, log: log}
}

func (t *CrawlPull) Name() string { return "crawl.pull" }

func (t *CrawlPull) Invoke(ctx context.Context, with map[string]string) (any, error) {
	targets, err := crawlsource.ParseTargets(with["targets"])
	if err != nil {
		return nil, fmt.Errorf("crawl.pull: %w", err)
	}
	src, err := t.crawler.Source(targets)
	if err != nil {
		return nil, fmt.Errorf("crawl.pull: %w", err)
	}
	items, err := src.Pull(ctx)
	var failed crawlsource.Failures
	if err != nil && (len(items) == 0 || !errors.As(err, &failed)) {
		return nil, fmt.Errorf("crawl.pull: %w", err)
	}

	decoded := make([]any, 0, len(items))
	for _, it := range items {
		var doc any
		if err := json.Unmarshal(it.Body, &doc); err != nil {
			return nil, fmt.Errorf("crawl.pull: decode %s: %w", it.ID, err)
		}
		decoded = append(decoded, doc)
	}
	failures := make([]any, 0, len(failed))
	for _, f := range failed {
		t.log.WarnContext(ctx, "crawl target yielded nothing", "target", f.Target, "url", f.URL, "kind", f.Kind, "error", f.Err)
		failures = append(failures, map[string]any{"target": f.Target, "url": f.URL, "kind": f.Kind, "error": f.Err.Error()})
	}
	at, _ := src.LastRefreshed(ctx)
	return map[string]any{
		"items":    decoded,
		"failures": failures,
		"_meta": map[string]any{
			"count":       len(decoded),
			"failed":      len(failures),
			"revalidated": src.LastReport().Revalidated,
			"fetched_at":  at.Format(time.RFC3339),
		},
	}, nil
}
