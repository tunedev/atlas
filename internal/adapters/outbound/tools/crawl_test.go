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
	out, err := tools.NewCrawlPull(crawlConfig(), slog.New(slog.NewTextHandler(&logs, nil))).Invoke(context.Background(), map[string]string{
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
	_, err := tools.NewCrawlPull(crawlConfig(), slog.Default()).Invoke(context.Background(), map[string]string{"targets": only})
	if err == nil || !strings.HasPrefix(err.Error(), "crawl.pull: ") {
		t.Errorf("err = %v; a crawl that reached nothing must fail the step", err)
	}
}

func TestCrawlPullRejectsTargetsItCannotParse(t *testing.T) {
	_, err := tools.NewCrawlPull(crawlConfig(), slog.Default()).Invoke(context.Background(), map[string]string{"targets": "- id: [unclosed"})
	if err == nil || !strings.HasPrefix(err.Error(), "crawl.pull: ") {
		t.Errorf("err = %v", err)
	}
}
