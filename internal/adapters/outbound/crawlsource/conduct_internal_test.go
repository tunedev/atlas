package crawlsource

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testUA = "atlas-test/1 (+https://example.invalid/bot)"

func testConfig() Config {
	return Config{
		UserAgent: testUA, Delay: 20 * time.Millisecond, Timeout: 5 * time.Second,
		PullTimeout: 30 * time.Second, MaxBytes: 1 << 20, RenderTimeout: 10 * time.Second,
	}
}

// hit is one request a test server saw.
type hit struct {
	Path, UA, Cookie, Auth string
	At                     time.Time
}

// siteLog records every request a test server sees.
type siteLog struct {
	mu   sync.Mutex
	hits []hit
}

func (l *siteLog) record(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits = append(l.hits, hit{Path: r.URL.Path, UA: r.UserAgent(), Cookie: r.Header.Get("Cookie"), Auth: r.Header.Get("Authorization"), At: time.Now()})
}

func (l *siteLog) all() []hit {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]hit(nil), l.hits...)
}

func (l *siteLog) count(path string) int {
	n := 0
	for _, h := range l.all() {
		if h.Path == path {
			n++
		}
	}
	return n
}

// site serves routes (path to body) and records every request. A route not
// listed answers 404.
func site(t *testing.T, routes map[string]string) (*httptest.Server, *siteLog) {
	t.Helper()
	log := &siteLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func get(t *testing.T, c *Conduct, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return (&http.Client{Transport: c}).Do(req)
}

func TestConductRefusesAPathRobotsDisallows(t *testing.T) {
	srv, log := site(t, map[string]string{"/robots.txt": "User-agent: *\nDisallow: /closed\n", "/closed": "x", "/open": "y"})
	c := newConduct(testConfig(), http.DefaultTransport)
	if _, err := get(t, c, srv.URL+"/closed"); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("err = %v, want ErrDisallowed", err)
	}
	if log.count("/closed") != 0 {
		t.Error("a disallowed path was requested")
	}
	resp, err := get(t, c, srv.URL+"/open")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("an allowed path failed: %v", err)
	}
	if log.count("/robots.txt") != 1 {
		t.Errorf("robots.txt fetched %d times, want once per host", log.count("/robots.txt"))
	}
}

func TestConductAllowsEverythingWhenRobotsIsMissing(t *testing.T) {
	srv, _ := site(t, map[string]string{"/open": "y"})
	if _, err := get(t, newConduct(testConfig(), http.DefaultTransport), srv.URL+"/open"); err != nil {
		t.Fatalf("a 404 robots.txt blocked the host: %v", err)
	}
}

func TestConductDisallowsEverythingWhenRobotsFails(t *testing.T) {
	log := &siteLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		if r.URL.Path == "/robots.txt" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "page")
	}))
	defer srv.Close()
	if _, err := get(t, newConduct(testConfig(), http.DefaultTransport), srv.URL+"/open"); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("err = %v; a 5xx robots.txt must disallow the host", err)
	}
	if log.count("/open") != 0 {
		t.Error("a page was requested from a host whose robots.txt failed")
	}
}

func TestConductIdentifiesEveryRequestIncludingRobots(t *testing.T) {
	srv, log := site(t, map[string]string{"/robots.txt": "User-agent: *\nAllow: /\n", "/a": "a"})
	if _, err := get(t, newConduct(testConfig(), http.DefaultTransport), srv.URL+"/a"); err != nil {
		t.Fatal(err)
	}
	for _, h := range log.all() {
		if h.UA != testUA {
			t.Errorf("%s carried UA %q", h.Path, h.UA)
		}
	}
}

func TestConductOnlyReadsAndNeverSendsACredential(t *testing.T) {
	srv, log := site(t, map[string]string{"/a": "a"})
	c := newConduct(testConfig(), http.DefaultTransport)
	client := &http.Client{Transport: c}

	post, _ := http.NewRequest(http.MethodPost, srv.URL+"/a", strings.NewReader("x"))
	if _, err := client.Do(post); !errors.Is(err, ErrMethod) {
		t.Errorf("POST err = %v, want ErrMethod", err)
	}
	for _, h := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/a", nil)
		req.Header.Set(h, "secret")
		if _, err := client.Do(req); !errors.Is(err, ErrCredential) {
			t.Errorf("%s err = %v, want ErrCredential", h, err)
		}
	}
	withUser := strings.Replace(srv.URL, "http://", "http://me:pw@", 1) + "/a"
	req, _ := http.NewRequest(http.MethodGet, withUser, nil)
	if _, err := client.Do(req); !errors.Is(err, ErrCredential) {
		t.Errorf("userinfo URL err = %v, want ErrCredential", err)
	}
	if n := log.count("/a"); n != 0 {
		t.Errorf("a refused request reached the server %d times", n)
	}
}

func TestConductSpacesRequestsToOneHostButNotAcrossHosts(t *testing.T) {
	cfg := testConfig()
	cfg.Delay = 150 * time.Millisecond
	a, logA := site(t, map[string]string{"/1": "1", "/2": "2"})
	b, logB := site(t, map[string]string{"/1": "1"})
	c := newConduct(cfg, http.DefaultTransport)

	for _, u := range []string{a.URL + "/1", b.URL + "/1", a.URL + "/2"} {
		if _, err := get(t, c, u); err != nil {
			t.Fatal(err)
		}
	}
	hitsA := logA.all()
	for i := 1; i < len(hitsA); i++ {
		if gap := hitsA[i].At.Sub(hitsA[i-1].At); gap < cfg.Delay-5*time.Millisecond {
			t.Errorf("host A: %s then %s only %s apart, want at least %s", hitsA[i-1].Path, hitsA[i].Path, gap, cfg.Delay)
		}
	}
	firstB := logB.all()[0].At
	lastA1 := hitsA[1].At // robots.txt, then /1
	if firstB.Sub(lastA1) >= cfg.Delay {
		t.Errorf("host B waited %s behind host A; turns must be per host", firstB.Sub(lastA1))
	}
}

func TestConductHonoursCrawlDelay(t *testing.T) {
	srv, log := site(t, map[string]string{"/robots.txt": "User-agent: *\nCrawl-delay: 1\n", "/1": "1", "/2": "2"})
	c := newConduct(testConfig(), http.DefaultTransport)
	for _, p := range []string{"/1", "/2"} {
		if _, err := get(t, c, srv.URL+p); err != nil {
			t.Fatal(err)
		}
	}
	hits := log.all()
	if gap := hits[2].At.Sub(hits[1].At); gap < time.Second-5*time.Millisecond {
		t.Errorf("pages %s apart; robots.txt asked for 1s", gap)
	}
}

func TestConductFailsAnOversizedBodyRatherThanTruncating(t *testing.T) {
	srv, _ := site(t, map[string]string{"/big": strings.Repeat("x", 101), "/edge": strings.Repeat("x", 100)})
	cfg := testConfig()
	cfg.MaxBytes = 100
	c := newConduct(cfg, http.DefaultTransport)
	if _, err := get(t, c, srv.URL+"/big"); err == nil || !strings.Contains(err.Error(), "100") {
		t.Errorf("err = %v; an over-limit body must fail and name the limit", err)
	}
	resp, err := get(t, c, srv.URL+"/edge")
	if err != nil {
		t.Fatalf("an at-limit body failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 100 {
		t.Errorf("at-limit body = %d bytes", len(body))
	}
}
