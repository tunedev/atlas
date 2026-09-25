package crawlsource

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// validatingSite serves one page with an ETag and a Last-Modified, answering
// 304 to a matching conditional request, and counts full responses.
func validatingSite(t *testing.T, etag, lastModified string) (*httptest.Server, *int) {
	t.Helper()
	full := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if (etag != "" && r.Header.Get("If-None-Match") == etag) ||
			(lastModified != "" && r.Header.Get("If-Modified-Since") == lastModified) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if etag != "" {
			w.Header().Set("ETag", etag)
		}
		if lastModified != "" {
			w.Header().Set("Last-Modified", lastModified)
		}
		full++
		io.WriteString(w, "the page")
	}))
	t.Cleanup(srv.Close)
	return srv, &full
}

func cachedConduct(t *testing.T) *Conduct {
	cfg := testConfig()
	cfg.CacheDir = t.TempDir()
	return newConduct(cfg, http.DefaultTransport)
}

func readAll(t *testing.T, resp *http.Response, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return string(b)
}

func TestARevalidatedPageIsServedFromTheCache(t *testing.T) {
	for name, v := range map[string][2]string{
		"etag":          {`"v1"`, ""},
		"last-modified": {"", "Wed, 24 Sep 2026 10:00:00 GMT"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, full := validatingSite(t, v[0], v[1])
			c := cachedConduct(t)
			resp1, err1 := get(t, c, srv.URL+"/p")
			first := readAll(t, resp1, err1)
			resp2, err2 := get(t, c, srv.URL+"/p")
			second := readAll(t, resp2, err2)
			if first != "the page" || second != "the page" {
				t.Errorf("bodies %q %q", first, second)
			}
			if *full != 1 {
				t.Errorf("full responses = %d, want 1; the second read must revalidate", *full)
			}
			if c.Revalidated() != 1 {
				t.Errorf("revalidated = %d, want 1", c.Revalidated())
			}
		})
	}
}

func TestAPageWithoutValidatorsIsRefetched(t *testing.T) {
	srv, full := validatingSite(t, "", "")
	c := cachedConduct(t)
	for i := 0; i < 2; i++ {
		resp, err := get(t, c, srv.URL+"/p")
		readAll(t, resp, err)
	}
	if *full != 2 || c.Revalidated() != 0 {
		t.Errorf("full = %d revalidated = %d; nothing to revalidate against", *full, c.Revalidated())
	}
}

func TestACorruptCacheEntryIsIgnored(t *testing.T) {
	srv, full := validatingSite(t, `"v1"`, "")
	c := cachedConduct(t)
	resp1, err1 := get(t, c, srv.URL+"/p")
	readAll(t, resp1, err1)
	entries, _ := os.ReadDir(c.cfg.CacheDir)
	if len(entries) != 1 {
		t.Fatalf("cache entries = %d, want 1", len(entries))
	}
	os.WriteFile(c.cfg.CacheDir+"/"+entries[0].Name(), []byte("{not json"), 0o600)
	resp2, err2 := get(t, c, srv.URL+"/p")
	if got := readAll(t, resp2, err2); got != "the page" {
		t.Errorf("body = %q", got)
	}
	if *full != 2 {
		t.Errorf("full = %d; a corrupt entry must be refetched, not served", *full)
	}
}

// TestARevalidatedBodyOverTheNewLimitFails proves a cached body is still
// bounded by MaxBytes: a body cached under a larger limit must not be served
// whole once MaxBytes is lowered, even though the cache is on disk and
// outlives any one Conduct.
func TestARevalidatedBodyOverTheNewLimitFails(t *testing.T) {
	body := strings.Repeat("x", 200)
	full := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			// No body: robots.txt must fit under the small MaxBytes used below.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		full++
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()

	bigCfg := testConfig()
	bigCfg.CacheDir = dir
	bigCfg.MaxBytes = 1000
	big := newConduct(bigCfg, http.DefaultTransport)
	resp, err := get(t, big, srv.URL+"/p")
	readAll(t, resp, err)

	smallCfg := testConfig()
	smallCfg.CacheDir = dir
	smallCfg.MaxBytes = 10
	small := newConduct(smallCfg, http.DefaultTransport)
	resp, err = get(t, small, srv.URL+"/p")
	if err == nil {
		resp.Body.Close()
		t.Fatal("want an error; the cached body exceeds the new MaxBytes")
	}
	if !strings.Contains(err.Error(), "10") {
		t.Errorf("err = %v; want it to name the limit", err)
	}
	if small.Revalidated() != 0 {
		t.Errorf("revalidated = %d, want 0; an over-limit cached body must not count as revalidated", small.Revalidated())
	}
	if full != 1 {
		t.Errorf("full responses = %d, want 1; the second request must be answered 304", full)
	}
}

func TestARevalidatedPageStillParsesWhenThe304CarriesEntityHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, eventsPage)
	}))
	t.Cleanup(srv.Close)
	ts, err := ParseTargets("- id: a\n  url: " + srv.URL + "/p\n  item: li.event\n  key: link\n  fields:\n    link: {css: a, attr: href}\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.CacheDir = t.TempDir()
	src, err := New(cfg, ts)
	if err != nil {
		t.Fatal(err)
	}
	for run := range 2 {
		if items, err := src.Pull(context.Background()); err != nil || len(items) == 0 {
			t.Fatalf("run %d: items = %d err = %v", run, len(items), err)
		}
	}
	if got := src.LastReport().Revalidated; got != 1 {
		t.Errorf("revalidated = %d, want 1", got)
	}
}

func TestARevalidatedPageKeepsItsContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "the page")
	}))
	t.Cleanup(srv.Close)
	c := cachedConduct(t)
	resp, err := get(t, c, srv.URL+"/p")
	readAll(t, resp, err)
	resp, err = get(t, c, srv.URL+"/p")
	if body := readAll(t, resp, err); body != "the page" {
		t.Errorf("body = %q", body)
	}
	if ct, ce := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding"); ct != "text/html; charset=utf-8" || ce != "" {
		t.Errorf("Content-Type %q Content-Encoding %q; the cached body's own headers must be served", ct, ce)
	}
}
