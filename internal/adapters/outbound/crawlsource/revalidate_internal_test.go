package crawlsource

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
