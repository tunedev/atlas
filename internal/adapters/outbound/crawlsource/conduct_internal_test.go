package crawlsource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	return recordedSite(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, body)
	})
}

// recordedSite serves h and records every request.
func recordedSite(t *testing.T, h http.HandlerFunc) (*httptest.Server, *siteLog) {
	t.Helper()
	log := &siteLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func get(t *testing.T, c *Conduct, url string) (*http.Response, error) {
	t.Helper()
	return getIn(t, context.Background(), c, url)
}

func getIn(t *testing.T, ctx context.Context, c *Conduct, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

func TestAllowRefusesAURLCarryingCredentials(t *testing.T) {
	srv, log := site(t, map[string]string{"/a": "a"})
	u, _ := url.Parse(strings.Replace(srv.URL, "http://", "http://me:pw@", 1) + "/a")
	if err := newConduct(testConfig(), http.DefaultTransport).Allow(context.Background(), u); !errors.Is(err, ErrCredential) {
		t.Errorf("err = %v, want ErrCredential", err)
	}
	if n := len(log.all()); n != 0 {
		t.Errorf("the server saw %d requests, want none", n)
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

func TestAHostAskingForMoreThanTheCrawlHasFailsAtOnceAndSparesTheRest(t *testing.T) {
	slow, _ := site(t, map[string]string{"/robots.txt": "User-agent: *\nCrawl-delay: 86400\n", "/1": eventsPage, "/2": eventsPage})
	fast, _ := site(t, map[string]string{"/1": eventsPage})
	var yaml string
	for i, u := range []string{slow.URL + "/1", slow.URL + "/2", fast.URL + "/1"} {
		yaml += fmt.Sprintf("- id: t%d\n  url: %s\n  item: li.event\n  key: link\n  fields:\n    link: {css: a, attr: href}\n", i, u)
	}
	ts, err := ParseTargets(yaml)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.PullTimeout = time.Second
	src, err := New(cfg, ts)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	items, err := src.Pull(context.Background())
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("took %s; a wait past the deadline must fail at once", took)
	}
	if len(items) == 0 || !strings.HasPrefix(items[0].ID, "t2/") {
		t.Errorf("items = %v; the fast host's target must succeed", items)
	}
	var failed Failures
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v", err)
	}
	t.Logf("failures: %v", failed)
	var second *Failure
	for i := range failed {
		if failed[i].Target == "t1" {
			second = &failed[i]
		}
	}
	if second == nil || !errors.Is(second.Err, errWaitPastDeadline) || !strings.Contains(second.Err.Error(), slow.Listener.Addr().String()) {
		t.Errorf("failures = %v; the slow host's second target must fail naming the host", failed)
	}
}

// robotsSite answers robots.txt with each status in turn (the last one
// repeats), an allow-everything body on a 200, and every other path with a
// page.
func robotsSite(t *testing.T, statuses ...int) (*httptest.Server, *siteLog) {
	t.Helper()
	log := &siteLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		if r.URL.Path != "/robots.txt" {
			io.WriteString(w, "page")
			return
		}
		status := statuses[min(log.count("/robots.txt")-1, len(statuses)-1)]
		w.WriteHeader(status)
		io.WriteString(w, "User-agent: *\nAllow: /\n")
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func TestARobotsTxtAnsweringTooManyRequestsDisallows(t *testing.T) {
	srv, log := robotsSite(t, http.StatusTooManyRequests)
	if _, err := get(t, newConduct(testConfig(), http.DefaultTransport), srv.URL+"/open"); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("err = %v; a 429 robots.txt must disallow the host", err)
	}
	if log.count("/open") != 0 {
		t.Error("a page was requested from a host whose robots.txt answered 429")
	}
}

func TestAFailedRobotsTxtIsFetchedAgainForTheNextTarget(t *testing.T) {
	srv, log := robotsSite(t, http.StatusServiceUnavailable, http.StatusOK)
	c := newConduct(testConfig(), http.DefaultTransport)
	if _, err := get(t, c, srv.URL+"/a"); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("err = %v; a 503 robots.txt must disallow the first target", err)
	}
	if _, err := get(t, c, srv.URL+"/b"); err != nil {
		t.Fatalf("err = %v; the second target must see the robots.txt that now answers 200", err)
	}
	if n := log.count("/robots.txt"); n != 2 {
		t.Errorf("robots.txt fetched %d times, want 2", n)
	}
	if log.count("/a") != 0 || log.count("/b") != 1 {
		t.Errorf("/a requested %d times, /b %d times; want 0 and 1", log.count("/a"), log.count("/b"))
	}
}

// stalledRobots serves a robots.txt that answers only once release is
// called, signalling entered on each request for it.
func stalledRobots(t *testing.T) (srv *httptest.Server, log *siteLog, entered chan struct{}, release func()) {
	t.Helper()
	log = &siteLog{}
	entered = make(chan struct{}, 8)
	released := make(chan struct{})
	release = sync.OnceFunc(func() { close(released) })
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		if r.URL.Path == "/robots.txt" {
			entered <- struct{}{}
			<-released
		}
		io.WriteString(w, "User-agent: *\nAllow: /\n")
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(release)
	return srv, log, entered, release
}

func TestASlowRobotsTxtOnOneHostDoesNotDelayAnother(t *testing.T) {
	slow, _, entered, release := stalledRobots(t)
	fast, _ := site(t, map[string]string{"/robots.txt": "User-agent: *\nAllow: /\n"})
	c := newConduct(testConfig(), http.DefaultTransport)
	slowURL, _ := url.Parse(slow.URL + "/a")
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.permitted(context.Background(), slowURL)
	}()
	defer func() { release(); <-done }()
	<-entered

	fastURL, _ := url.Parse(fast.URL + "/b")
	start := time.Now()
	if err := c.permitted(context.Background(), fastURL); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("host B's check took %s while host A's robots.txt stalled", took)
	}
}

func TestARobotsTxt429DefersTheHostsNextTurn(t *testing.T) {
	srv, log := recordedSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c := newConduct(testConfig(), http.DefaultTransport)
	u, _ := url.Parse(srv.URL + "/open")

	start := time.Now()
	if err := c.permitted(context.Background(), u); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("first check err = %v, want ErrDisallowed", err)
	}
	if err := c.permitted(context.Background(), u); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("second check err = %v, want ErrDisallowed", err)
	}
	if took := time.Since(start); took < time.Second-5*time.Millisecond {
		t.Errorf("second robots check happened after %s; a 429 asked for 1s before the host's next turn", took)
	}
	if n := log.count("/robots.txt"); n != 2 {
		t.Errorf("robots.txt fetched %d times, want 2; a 429 must not be cached as permission", n)
	}
}

func TestARobotsTxt429WithoutRetryAfterDoesNotDeferTheNextTurn(t *testing.T) {
	srv, log := robotsSite(t, http.StatusTooManyRequests)
	c := newConduct(testConfig(), http.DefaultTransport)
	u, _ := url.Parse(srv.URL + "/open")

	start := time.Now()
	if err := c.permitted(context.Background(), u); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("first check err = %v, want ErrDisallowed", err)
	}
	if err := c.permitted(context.Background(), u); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("second check err = %v, want ErrDisallowed", err)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("took %s; without Retry-After the next check must not be deferred", took)
	}
	if n := log.count("/robots.txt"); n != 2 {
		t.Errorf("robots.txt fetched %d times, want 2", n)
	}
}

func TestARobotsTxt429DeferralIsCappedLikeARetryAfter(t *testing.T) {
	srv, _ := recordedSite(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "99999999999")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c := newConduct(testConfig(), http.DefaultTransport)
	u, _ := url.Parse(srv.URL + "/open")

	before := time.Now()
	if err := c.permitted(context.Background(), u); !errors.Is(err, ErrDisallowed) {
		t.Fatalf("err = %v, want ErrDisallowed", err)
	}
	c.turns.mu.Lock()
	until := c.turns.last[u.Host]
	c.turns.mu.Unlock()
	if wait := until.Sub(before); wait < time.Hour || wait > maxRetryAfter*time.Second+time.Second {
		t.Errorf("deferred by %s; want capped near maxRetryAfter (%ds), not the raw header value", wait, maxRetryAfter)
	}
}

func TestConcurrentChecksOnOneHostFetchRobotsOnce(t *testing.T) {
	srv, log, entered, release := stalledRobots(t)
	c := newConduct(testConfig(), http.DefaultTransport)
	u, _ := url.Parse(srv.URL + "/a")
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := c.permitted(context.Background(), u); err != nil {
				t.Error(err)
			}
		})
	}
	<-entered
	time.Sleep(50 * time.Millisecond)
	release()
	wg.Wait()
	if n := log.count("/robots.txt"); n != 1 {
		t.Errorf("robots.txt fetched %d times, want once", n)
	}
}
