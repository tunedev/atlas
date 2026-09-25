package crawlsource

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/launcher"
)

func TestNoBrowserFoundIsAFailureNeverADownload(t *testing.T) {
	srv, log := site(t, map[string]string{"/app": "<html></html>"})
	cfg := testConfig()
	cfg.Render = true
	c := chrome{cfg: cfg, conduct: newConduct(cfg, http.DefaultTransport), lookPath: func() (string, bool) { return "", false }}
	u, _ := url.Parse(srv.URL + "/app")
	if _, err := c.render(context.Background(), u); !errors.Is(err, ErrNoBrowser) {
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
	if os.Getenv("ATLAS_LIVE_BROWSER") == "" {
		t.Skip("ATLAS_LIVE_BROWSER not set")
	}
	if _, has := launcher.LookPath(); !has {
		t.Skip("no local browser")
	}
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
