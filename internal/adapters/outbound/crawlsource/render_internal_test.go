package crawlsource

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
)

func TestNoBrowserFoundIsAFailureNeverADownload(t *testing.T) {
	srv, log := site(t, map[string]string{"/app": "<html></html>"})
	cfg := testConfig()
	cfg.Render = true
	c := chrome{cfg: cfg, conduct: newConduct(cfg, http.DefaultTransport), lookPath: func() (string, bool) { return "", false }}
	u, _ := url.Parse(srv.URL + "/app")
	if _, _, err := c.render(context.Background(), u); !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("err = %v, want ErrNoBrowser", err)
	}
	if len(log.all()) != 0 {
		t.Error("the site was contacted although no browser could render it")
	}
}

const appPage = `<html><body><ul id="list"></ul><script>
setTimeout(function () {
  document.getElementById("list").innerHTML =
    '<li class="event"><h3>Tide talk</h3><a href="/events/tide">more</a></li>';
}, 100);
</script></body></html>`

// TestRenderAgainstALocalBrowserLive is skipped unless ATLAS_LIVE_BROWSER is
// set and a browser is installed: it proves a JavaScript-built page yields
// items through the user's own browser, identified as the crawler.
func TestRenderAgainstALocalBrowserLive(t *testing.T) {
	liveBrowser(t)
	srv, log := site(t, map[string]string{"/app": appPage})
	cfg := testConfig()
	cfg.Render = true
	ts, err := ParseTargets("- id: app\n  url: " + srv.URL + "/app\n  item: li.event\n  key: link\n  render: true\n  fields:\n    name: {css: h3}\n    link: {css: a, attr: href}\n")
	if err != nil {
		t.Fatal(err)
	}
	src, err := New(cfg, ts)
	if err != nil {
		t.Fatal(err)
	}
	items, err := src.Pull(context.Background())
	if err != nil || len(items) != 1 || !strings.Contains(string(items[0].Body), "Tide talk") {
		t.Fatalf("items = %v err = %v", items, err)
	}
	for _, h := range log.all() {
		if h.Path == "/app" && h.UA != testUA {
			t.Errorf("the rendered navigation carried UA %q, not the crawler's", h.UA)
		}
	}
	t.Logf("rendered item: %s", items[0].Body)
}

func TestPermittedChecksRobotsWithoutTakingATurn(t *testing.T) {
	srv, log := site(t, map[string]string{"/robots.txt": "User-agent: *\nDisallow: /closed\n"})
	cfg := testConfig()
	cfg.Delay = time.Second
	c := newConduct(cfg, http.DefaultTransport)
	closed, _ := url.Parse(srv.URL + "/closed")
	open, _ := url.Parse(srv.URL + "/open")
	if err := c.permitted(context.Background(), closed); !errors.Is(err, ErrDisallowed) {
		t.Errorf("err = %v, want ErrDisallowed", err)
	}
	start := time.Now()
	if err := c.permitted(context.Background(), open); err != nil {
		t.Errorf("err = %v; an allowed URL was refused", err)
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("took %s; permitted must not wait for a turn", took)
	}
	if n := len(log.all()); n != 1 {
		t.Errorf("the server saw %d requests, want only robots.txt", n)
	}
}

func TestARenderThatLandsOnADisallowedURLFails(t *testing.T) {
	srv, _ := site(t, map[string]string{"/robots.txt": "User-agent: *\nDisallow: /closed\n"})
	cfg := testConfig()
	c := chrome{cfg: cfg, conduct: newConduct(cfg, http.DefaultTransport)}
	asked, _ := url.Parse(srv.URL + "/app")
	if _, err := c.landed(context.Background(), asked, srv.URL+"/closed"); !errors.Is(err, ErrDisallowed) {
		t.Errorf("err = %v, want ErrDisallowed", err)
	}
	if _, err := c.landed(context.Background(), asked, srv.URL+"/app"); err != nil {
		t.Errorf("err = %v; a render that stayed put was refused", err)
	}
}

func TestRenderedHTMLOverTheLimitFails(t *testing.T) {
	if _, err := boundHTML(strings.Repeat("x", 101), 100); err == nil || !strings.Contains(err.Error(), "max size of 100 bytes") {
		t.Errorf("err = %v; an over-limit page must fail and name the limit", err)
	}
	if html, err := boundHTML(strings.Repeat("x", 100), 100); err != nil || len(html) != 100 {
		t.Errorf("len = %d err = %v; an at-limit page must pass whole", len(html), err)
	}
}

func TestAFailedLaunchLeavesNoProfileBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	srv, _ := site(t, map[string]string{"/app": "<html></html>"})
	cfg := testConfig()
	c := chrome{cfg: cfg, conduct: newConduct(cfg, http.DefaultTransport), lookPath: func() (string, bool) { return "/bin/false", true }}
	u, _ := url.Parse(srv.URL + "/app")
	if _, _, err := c.render(context.Background(), u); err == nil {
		t.Fatal("a browser that exits at once rendered a page")
	}
	profiles, _ := filepath.Glob(filepath.Join(tmp, "atlas-render-*"))
	if len(profiles) != 0 {
		t.Errorf("profiles left behind: %v", profiles)
	}
}

// liveBrowser skips t unless ATLAS_LIVE_BROWSER is set and a browser is
// installed.
func liveBrowser(t *testing.T) {
	t.Helper()
	if os.Getenv("ATLAS_LIVE_BROWSER") == "" {
		t.Skip("ATLAS_LIVE_BROWSER not set")
	}
	if _, has := launcher.LookPath(); !has {
		t.Skip("no local browser")
	}
}

// renderOne pulls one rendering target at u and returns its only item's
// link.
func renderOne(t *testing.T, u string) string {
	t.Helper()
	cfg := testConfig()
	cfg.Render = true
	ts, err := ParseTargets("- id: app\n  url: " + u + "\n  item: li.event\n  key: link\n  render: true\n  fields:\n    link: {css: a, attr: href}\n")
	if err != nil {
		t.Fatal(err)
	}
	src, err := New(cfg, ts)
	if err != nil {
		t.Fatal(err)
	}
	items, err := src.Pull(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %v err = %v", items, err)
	}
	var fields map[string]string
	if err := json.Unmarshal(items[0].Body, &fields); err != nil {
		t.Fatal(err)
	}
	return fields["link"]
}

// TestARenderResolvesLinksAgainstThePageItLandedOnLive is skipped unless
// ATLAS_LIVE_BROWSER is set and a browser is installed.
func TestARenderResolvesLinksAgainstThePageItLandedOnLive(t *testing.T) {
	liveBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			http.Redirect(w, r, "/careers/list/", http.StatusFound)
		case "/careers/list/":
			io.WriteString(w, `<html><body><ul><li class="event"><a href="detail/1">more</a></li></ul></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	if got, want := renderOne(t, srv.URL+"/jobs"), srv.URL+"/careers/list/detail/1"; got != want {
		t.Errorf("link = %s, want %s", got, want)
	}
}
