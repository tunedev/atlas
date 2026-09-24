package tools_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

type fakeSource struct {
	items     []ports.Item
	refreshed time.Time
	err       error
}

func (f fakeSource) Pull(context.Context) ([]ports.Item, error) { return f.items, f.err }
func (f fakeSource) LastRefreshed(context.Context) (time.Time, error) {
	return f.refreshed, nil
}

func shelf(age time.Duration) fakeSource {
	return fakeSource{
		refreshed: time.Now().Add(-age),
		items: []ports.Item{
			{ID: "shelf/a/one", Body: []byte(`{"title":"Dune","state":"open"}`)},
			{ID: "shelf/a/two", Body: []byte(`{"title":"Emma","state":"lent"}`)},
			{ID: "shelfish/x", Body: []byte(`{"title":"Ulysses","state":"open"}`)},
		},
	}
}

func pull(t *testing.T, src ports.Source, with map[string]string) (map[string]any, string) {
	t.Helper()
	var logs bytes.Buffer
	out, err := tools.NewSourcePull(src, 24*time.Hour, slog.New(slog.NewTextHandler(&logs, nil))).
		Invoke(context.Background(), with)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	return out.(map[string]any), logs.String()
}

func TestSourcePullDecodesItemsAndReportsFreshness(t *testing.T) {
	out, logs := pull(t, shelf(2*time.Hour), map[string]string{})
	items := out["items"].([]any)
	meta := out["_meta"].(map[string]any)
	if len(items) != 3 || meta["count"] != 3 {
		t.Errorf("items = %d, count = %v", len(items), meta["count"])
	}
	if items[0].(map[string]any)["title"] != "Dune" {
		t.Errorf("first item = %v", items[0])
	}
	if age := meta["age_hours"].(float64); age < 1.9 || age > 2.1 || meta["stale"] != false {
		t.Errorf("meta = %v", meta)
	}
	if logs != "" {
		t.Errorf("a fresh source logged: %s", logs)
	}
}

func TestSourcePullNarrowsByPrefixAndMatch(t *testing.T) {
	out, _ := pull(t, shelf(time.Hour), map[string]string{"prefix": "shelf", "match": "state=open"})
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Dune" {
		t.Errorf("items = %v; prefix is a directory boundary and match an exact field", items)
	}
}

func TestSourcePullNeverDecodesAnItemOutsideThePrefix(t *testing.T) {
	src := fakeSource{
		refreshed: time.Now(),
		items: []ports.Item{
			{ID: "shelf/a/one", Body: []byte(`{"title":"Dune","state":"open"}`)},
			{ID: "elsewhere/broken", Body: []byte(`not json`)},
		},
	}
	out, _ := pull(t, src, map[string]string{"prefix": "shelf"})
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Dune" {
		t.Errorf("items = %v; an undecodable item outside the prefix must not fail the pull", items)
	}
}

func TestSourcePullWarnsWhenStaleAndStillReturns(t *testing.T) {
	out, logs := pull(t, shelf(30*time.Hour), map[string]string{})
	if out["_meta"].(map[string]any)["stale"] != true || len(out["items"].([]any)) != 3 {
		t.Errorf("out = %v", out)
	}
	if !strings.Contains(logs, "source is stale") {
		t.Errorf("no warning logged for a 30h-old source: %q", logs)
	}
}

func TestSourcePullRejectsAMalformedMatchAndPropagatesFailure(t *testing.T) {
	tool := tools.NewSourcePull(shelf(time.Hour), time.Hour, slog.Default())
	if _, err := tool.Invoke(context.Background(), map[string]string{"match": "state"}); err == nil {
		t.Error("match without = was accepted")
	}
	broken := tools.NewSourcePull(fakeSource{err: errors.New("offline")}, time.Hour, slog.Default())
	if _, err := broken.Invoke(context.Background(), map[string]string{}); err == nil {
		t.Error("a failed pull was reported as success")
	}
}

// A pack reaches the result through templates; this runs it through the
// real runner so _meta and item fields are proven reachable.
func TestSourcePullResultIsReachableFromTemplates(t *testing.T) {
	echo := &echoTool{}
	reg := tools.NewRegistry(tools.NewSourcePull(shelf(time.Hour), 24*time.Hour, slog.Default()), echo)
	bp := domain.Blueprint{Name: "shelf", Steps: []domain.Step{
		{ID: "feed", Tool: "source.pull", With: map[string]string{"match": "state=open"}},
		{ID: "show", Tool: "echo", With: map[string]string{
			"text": `{{ (index .steps.feed.items 0).title }} stale={{ .steps.feed._meta.stale }}`,
		}},
	}}
	if _, err := app.NewRunner(reg).Run(context.Background(), bp); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if echo.seen != "Dune stale=false" {
		t.Errorf("rendered %q", echo.seen)
	}
}

type echoTool struct{ seen string }

func (e *echoTool) Name() string { return "echo" }
func (e *echoTool) Invoke(_ context.Context, with map[string]string) (any, error) {
	e.seen = with["text"]
	return map[string]any{}, nil
}
