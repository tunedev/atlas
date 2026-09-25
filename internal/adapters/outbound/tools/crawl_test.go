package tools_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
)

func crawlConfig() crawlsource.Config {
	return crawlsource.Config{
		UserAgent: "atlas-test/1 (+https://example.invalid/bot)", Delay: 10 * time.Millisecond,
		Timeout: 5 * time.Second, PullTimeout: 30 * time.Second, MaxBytes: 1 << 20, RenderTimeout: 10 * time.Second,
	}
}

func newCrawler(t *testing.T, cfg crawlsource.Config) *crawlsource.Crawler {
	t.Helper()
	c, err := crawlsource.NewCrawler(cfg)
	if err != nil {
		t.Fatalf("new crawler: %v", err)
	}
	return c
}

const listing = `<html><body><ul><li class="event"><h3>Tide talk</h3><a href="/tide">x</a></li></ul></body></html>`

const crawlTargets = `
- id: good
  url: %[1]s/events
  item: li.event
  key: link
  fields:
    name: {css: h3}
    link: {css: a, attr: href}
- id: changed
  url: %[1]s/changed
  item: li.event
  key: link
  fields:
    link: {css: a, attr: href}
`

func eventsSite(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			io.WriteString(w, listing)
		case "/changed":
			io.WriteString(w, "<html><body>"+strings.Repeat("<p>The programme moved to a new layout this season.</p>", 6)+"</body></html>")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCrawlPullReturnsItemsAndNamesEveryFailure(t *testing.T) {
	srv := eventsSite(t)
	var logs bytes.Buffer
	out, err := tools.NewCrawlPull(newCrawler(t, crawlConfig()), slog.New(slog.NewTextHandler(&logs, nil))).Invoke(context.Background(), map[string]string{
		"targets": fmt.Sprintf(crawlTargets, srv.URL),
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m := out.(map[string]any)
	items := m["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "Tide talk" {
		t.Errorf("items = %v", items)
	}
	failures := m["failures"].([]any)
	if len(failures) != 1 {
		t.Fatalf("failures = %v", failures)
	}
	f := failures[0].(map[string]any)
	if f["target"] != "changed" || f["kind"] != crawlsource.KindStale || f["error"] == "" {
		t.Errorf("failure = %v", f)
	}
	meta := m["_meta"].(map[string]any)
	if meta["count"] != 1 || meta["failed"] != 1 {
		t.Errorf("_meta = %v", meta)
	}
	if !strings.Contains(logs.String(), "changed") || !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("the failure was not logged as a warning: %s", logs.String())
	}
}

func TestCrawlPullFailsWhenEveryTargetFails(t *testing.T) {
	srv := eventsSite(t)
	only := "- id: changed\n  url: " + srv.URL + "/changed\n  item: li.event\n  key: link\n  fields:\n    link: {css: a, attr: href}\n"
	_, err := tools.NewCrawlPull(newCrawler(t, crawlConfig()), slog.Default()).Invoke(context.Background(), map[string]string{"targets": only})
	if err == nil || !strings.HasPrefix(err.Error(), "crawl.pull: ") {
		t.Errorf("err = %v; a crawl that reached nothing must fail the step", err)
	}
}

func TestCrawlPullRejectsTargetsItCannotParse(t *testing.T) {
	_, err := tools.NewCrawlPull(newCrawler(t, crawlConfig()), slog.Default()).Invoke(context.Background(), map[string]string{"targets": "- id: [unclosed"})
	if err == nil || !strings.HasPrefix(err.Error(), "crawl.pull: ") {
		t.Errorf("err = %v", err)
	}
}

// TestCrawlPullPacesAcrossInvocationsSharingOneCrawler proves that politeness
// pacing is a property of the Crawler, not of one Invoke: two calls against
// the same host, made back to back through one Crawler, must still be spaced
// by at least its Delay.
func TestCrawlPullPacesAcrossInvocationsSharingOneCrawler(t *testing.T) {
	var mu sync.Mutex
	var hits []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, time.Now())
		mu.Unlock()
		if r.URL.Path == "/events" {
			io.WriteString(w, listing)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	cfg := crawlConfig()
	cfg.Delay = 150 * time.Millisecond
	crawler := newCrawler(t, cfg)
	pull := tools.NewCrawlPull(crawler, slog.Default())

	one := "- id: good\n  url: " + srv.URL + "/events\n  item: li.event\n  key: link\n  fields:\n    link: {css: a, attr: href}\n"
	if _, err := pull.Invoke(context.Background(), map[string]string{"targets": one}); err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	mu.Lock()
	n1 := len(hits)
	mu.Unlock()

	if _, err := pull.Invoke(context.Background(), map[string]string{"targets": one}); err != nil {
		t.Fatalf("second invoke: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if n1 == 0 || len(hits) <= n1 {
		t.Fatalf("hits before second call = %d, after = %d", n1, len(hits))
	}
	if gap := hits[n1].Sub(hits[n1-1]); gap < cfg.Delay-5*time.Millisecond {
		t.Errorf("first request of the second call came %s after the last request of the first; want at least %s (pacing must hold across crawl.pull invocations)", gap, cfg.Delay)
	}
}
