package crawlsource_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/crawlsource"
)

const testUA = "atlas-test/1 (+https://example.invalid/bot)"

func testConfig() crawlsource.Config {
	return crawlsource.Config{
		UserAgent: testUA, Delay: 20 * time.Millisecond, Timeout: 5 * time.Second,
		PullTimeout: 30 * time.Second, MaxBytes: 1 << 20, RenderTimeout: 10 * time.Second,
	}
}

const eventsPage = `<html><body><h1>What's on</h1><ul>
<li class="event"><h3>Tide talk</h3><span class="room">Harbour room</span><a href="/events/tide">more</a></li>
<li class="event"><h3>Knot workshop</h3><span class="room">Loft</span><a href="/events/knots">more</a></li>
</ul></body></html>`

const changedPage = `<html><body><h1>What's on</h1>` +
	`<p>This season the library hosts talks and workshops for all ages, every week, in the harbour room and the loft.</p>` +
	`<p>Our programme is being redesigned; the full listing now lives in a new layout on this page.</p></body></html>`

const shellPage = `<html><head><script src="/bundle.js"></script></head><body><div id="root"></div></body></html>`

// requests counts the requests a test server saw per path, safely.
type requests struct {
	mu sync.Mutex
	n  map[string]int
}

func (r *requests) add(p string) { r.mu.Lock(); r.n[p]++; r.mu.Unlock() }
func (r *requests) get(p string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n[p]
}

func library(t *testing.T, routes map[string]string) (*httptest.Server, *requests) {
	t.Helper()
	seen := &requests{n: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.add(r.URL.Path)
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

func targets(t *testing.T, yaml string, args ...any) []crawlsource.Target {
	t.Helper()
	ts, err := crawlsource.ParseTargets(fmt.Sprintf(yaml, args...))
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	return ts
}

const oneTarget = `
- id: harbour
  url: %s%s
  item: li.event
  key: link
  fields:
    name: {css: h3}
    link: {css: a, attr: href}
  static: {venue: Harbour library}
`

func pull(t *testing.T, cfg crawlsource.Config, ts []crawlsource.Target) ([]map[string]any, []string, error) {
	t.Helper()
	src, err := crawlsource.New(cfg, ts)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	items, err := src.Pull(context.Background())
	var out []map[string]any
	var ids []string
	for _, it := range items {
		var m map[string]any
		if err := json.Unmarshal(it.Body, &m); err != nil {
			t.Fatalf("item %s body is not json: %v", it.ID, err)
		}
		out = append(out, m)
		ids = append(ids, it.ID)
	}
	return out, ids, err
}

func kinds(err error) []string {
	var fs crawlsource.Failures
	if !errors.As(err, &fs) {
		return nil
	}
	var ks []string
	for _, f := range fs {
		ks = append(ks, f.Kind)
	}
	return ks
}

func TestPullYieldsOneItemPerElement(t *testing.T) {
	srv, _ := library(t, map[string]string{"/events": eventsPage})
	items, ids, err := pull(t, testConfig(), targets(t, oneTarget, srv.URL, "/events"))
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	if items[0]["name"] != "Tide talk" || items[0]["link"] != srv.URL+"/events/tide" || items[0]["venue"] != "Harbour library" {
		t.Errorf("first = %v", items[0])
	}
	if !strings.HasPrefix(ids[0], "harbour/") || len(ids[0]) != len("harbour/")+16 || ids[0] == ids[1] {
		t.Errorf("ids = %v", ids)
	}
}

func TestAChangedPageFailsLoudlyInsteadOfYieldingNothing(t *testing.T) {
	srv, _ := library(t, map[string]string{"/events": changedPage})
	items, _, err := pull(t, testConfig(), targets(t, oneTarget, srv.URL, "/events"))
	if len(items) != 0 || err == nil {
		t.Fatalf("items = %v err = %v; zero without an error is the failure nobody notices", items, err)
	}
	if k := kinds(err); len(k) != 1 || k[0] != crawlsource.KindStale {
		t.Errorf("kinds = %v", k)
	}
}

func TestAnApplicationShellNeedsRendering(t *testing.T) {
	srv, _ := library(t, map[string]string{"/events": shellPage})
	_, _, err := pull(t, testConfig(), targets(t, oneTarget, srv.URL, "/events"))
	if k := kinds(err); len(k) != 1 || k[0] != crawlsource.KindNeedsRendering {
		t.Errorf("kinds = %v err = %v", k, err)
	}
}

func TestOneFailingTargetLeavesTheOthers(t *testing.T) {
	srv, _ := library(t, map[string]string{"/events": eventsPage})
	two := targets(t, oneTarget+`
- id: gone
  url: %s/missing
  item: li.event
  key: link
  fields:
    link: {css: a, attr: href}
`, srv.URL, "/events", srv.URL)
	items, _, err := pull(t, testConfig(), two)
	if len(items) != 2 {
		t.Errorf("items = %d, want the working target's 2", len(items))
	}
	var fs crawlsource.Failures
	if !errors.As(err, &fs) || len(fs) != 1 || fs[0].Target != "gone" || fs[0].Kind != crawlsource.KindFetch {
		t.Errorf("err = %v", err)
	}
}

func TestATargetAskingForRenderingWhileItIsOffIsLoud(t *testing.T) {
	srv, seen := library(t, map[string]string{"/events": eventsPage})
	ts := targets(t, oneTarget+"  render: true\n", srv.URL, "/events")
	_, _, err := pull(t, testConfig(), ts)
	if k := kinds(err); len(k) != 1 || k[0] != crawlsource.KindNeedsRendering {
		t.Errorf("kinds = %v", k)
	}
	if seen.get("/events") != 0 {
		t.Error("a page that needs rendering was fetched without it")
	}
}

func TestAnOversizedPageFailsRatherThanTruncating(t *testing.T) {
	srv, _ := library(t, map[string]string{"/events": eventsPage + strings.Repeat(" ", 4096)})
	cfg := testConfig()
	cfg.MaxBytes = 1024
	items, _, err := pull(t, cfg, targets(t, oneTarget, srv.URL, "/events"))
	if len(items) != 0 {
		t.Errorf("a truncated page yielded items: %v", items)
	}
	if k := kinds(err); len(k) != 1 || k[0] != crawlsource.KindFetch {
		t.Errorf("kinds = %v", k)
	}
}

func TestAHostThatNeverAnswersIsBoundedByThePullTimeout(t *testing.T) {
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer stall.Close()
	cfg := testConfig()
	cfg.PullTimeout = 300 * time.Millisecond
	start := time.Now()
	_, _, err := pull(t, cfg, targets(t, oneTarget, stall.URL, "/events"))
	if err == nil || time.Since(start) > 2*time.Second {
		t.Errorf("err = %v after %s; a stalled host must fail within the pull timeout", err, time.Since(start))
	}
}

func TestLastRefreshedIsUnknownUntilACrawlCompletes(t *testing.T) {
	srv, _ := library(t, map[string]string{"/events": eventsPage})
	src, err := crawlsource.New(testConfig(), targets(t, oneTarget, srv.URL, "/events"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.LastRefreshed(context.Background()); err == nil {
		t.Error("a source that never crawled reported a refresh time")
	}
	if _, err := src.Pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if at, err := src.LastRefreshed(context.Background()); err != nil || time.Since(at) > time.Minute {
		t.Errorf("at = %v err = %v", at, err)
	}
}

func TestARerunRevalidatesInsteadOfRefetching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, eventsPage)
	}))
	defer srv.Close()
	cfg := testConfig()
	cfg.CacheDir = t.TempDir()
	ts := targets(t, oneTarget, srv.URL, "/events")
	first, _, err := pull(t, cfg, ts)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := crawlsource.New(cfg, ts)
	again, err := src.Pull(context.Background())
	if err != nil || len(again) != len(first) {
		t.Fatalf("rerun items = %d err = %v", len(again), err)
	}
	if src.LastReport().Revalidated != 1 {
		t.Errorf("revalidated = %d, want 1", src.LastReport().Revalidated)
	}
}

func TestNewRefusesAConfigThatWouldCrawlImpolitely(t *testing.T) {
	ts := targets(t, oneTarget, "https://library.example", "/events")
	for name, mutate := range map[string]func(*crawlsource.Config){
		"no delay":      func(c *crawlsource.Config) { c.Delay = 0 },
		"no user agent": func(c *crawlsource.Config) { c.UserAgent = "" },
		"no timeout":    func(c *crawlsource.Config) { c.Timeout = 0 },
		"no max bytes":  func(c *crawlsource.Config) { c.MaxBytes = 0 },
		"no pull bound": func(c *crawlsource.Config) { c.PullTimeout = 0 },
	} {
		cfg := testConfig()
		mutate(&cfg)
		if _, err := crawlsource.New(cfg, ts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := crawlsource.New(testConfig(), nil); err == nil {
		t.Error("no targets: accepted")
	}
}
