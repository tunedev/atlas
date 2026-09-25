package crawlsource_test

// These tests assert the crawler's conduct (stories 8.2 and 8.6) end to end,
// through Pull, against servers that record every request they receive.

import (
	"context"
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

type seen struct {
	Path, UA, Cookie, Auth, Method string
	At                             time.Time
}

type recorder struct {
	mu   sync.Mutex
	hits []seen
}

func (r *recorder) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits = append(r.hits, seen{req.URL.Path, req.UserAgent(), req.Header.Get("Cookie"), req.Header.Get("Authorization"), req.Method, time.Now()})
		r.mu.Unlock()
		h(w, req)
	}
}

func (r *recorder) all() []seen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seen(nil), r.hits...)
}

func (r *recorder) count(path string) int {
	n := 0
	for _, h := range r.all() {
		if h.Path == path {
			n++
		}
	}
	return n
}

func recorded(t *testing.T, h http.HandlerFunc) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(rec.wrap(h))
	t.Cleanup(srv.Close)
	return srv, rec
}

func routes(m map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := m[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, body)
	}
}

const target = `
- id: %s
  url: %s
  item: li.event
  key: link
  fields:
    link: {css: a, attr: href}
`

func crawl(t *testing.T, cfg crawlsource.Config, yaml string) ([]string, error) {
	t.Helper()
	ts, err := crawlsource.ParseTargets(yaml)
	if err != nil {
		t.Fatal(err)
	}
	src, err := crawlsource.New(cfg, ts)
	if err != nil {
		t.Fatal(err)
	}
	items, err := src.Pull(context.Background())
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids, err
}

func TestConductNeverRequestsAPathRobotsDisallows(t *testing.T) {
	srv, rec := recorded(t, routes(map[string]string{"/robots.txt": "User-agent: *\nDisallow: /closed\n", "/closed": eventsPage, "/open": eventsPage}))
	ids, err := crawl(t, testConfig(), fmt.Sprintf(target, "open", srv.URL+"/open")+fmt.Sprintf(target, "closed", srv.URL+"/closed"))
	if rec.count("/closed") != 0 {
		t.Fatal("a disallowed page was requested")
	}
	if len(ids) != 2 {
		t.Errorf("the allowed page's items = %v", ids)
	}
	if k := kinds(err); len(k) != 1 || k[0] != crawlsource.KindDisallowed {
		t.Errorf("kinds = %v", k)
	}
}

func TestConductTreatsAFailingRobotsTxtAsDisallowingEverything(t *testing.T) {
	srv, rec := recorded(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, eventsPage)
	})
	_, err := crawl(t, testConfig(), fmt.Sprintf(target, "a", srv.URL+"/events"))
	if rec.count("/events") != 0 {
		t.Fatal("a page was requested from a host whose robots.txt failed")
	}
	if k := kinds(err); len(k) != 1 || k[0] != crawlsource.KindDisallowed {
		t.Errorf("kinds = %v", k)
	}
}

func TestConductChecksARedirectAgainstItsNewHost(t *testing.T) {
	closed, closedRec := recorded(t, routes(map[string]string{"/robots.txt": "User-agent: *\nDisallow: /\n", "/events": eventsPage}))
	start, _ := recorded(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, closed.URL+"/events", http.StatusFound)
	})
	_, err := crawl(t, testConfig(), fmt.Sprintf(target, "a", start.URL+"/events"))
	if closedRec.count("/events") != 0 {
		t.Fatal("a redirect reached a page its own host's robots.txt disallows")
	}
	if k := kinds(err); len(k) != 1 || k[0] != crawlsource.KindDisallowed {
		t.Errorf("kinds = %v", k)
	}
}

func TestConductWaitsItsTurnPerHost(t *testing.T) {
	cfg := testConfig()
	cfg.Delay = 150 * time.Millisecond
	srv, rec := recorded(t, routes(map[string]string{"/1": eventsPage, "/2": eventsPage, "/3": eventsPage}))
	yaml := fmt.Sprintf(target, "one", srv.URL+"/1") + fmt.Sprintf(target, "two", srv.URL+"/2") + fmt.Sprintf(target, "three", srv.URL+"/3")
	if _, err := crawl(t, cfg, yaml); err != nil {
		t.Fatal(err)
	}
	hits := rec.all()
	if len(hits) != 4 {
		t.Fatalf("hits = %v", hits)
	}
	for i := 1; i < len(hits); i++ {
		if gap := hits[i].At.Sub(hits[i-1].At); gap < cfg.Delay-5*time.Millisecond {
			t.Errorf("%s then %s %s apart; the host's turn is %s", hits[i-1].Path, hits[i].Path, gap, cfg.Delay)
		}
	}
}

func TestConductIdentifiesItselfOnEveryRequest(t *testing.T) {
	srv, rec := recorded(t, routes(map[string]string{"/robots.txt": "User-agent: *\nAllow: /\n", "/events": eventsPage}))
	if _, err := crawl(t, testConfig(), fmt.Sprintf(target, "a", srv.URL+"/events")); err != nil {
		t.Fatal(err)
	}
	for _, h := range rec.all() {
		if h.UA != testUA {
			t.Errorf("%s carried %q", h.Path, h.UA)
		}
	}
}

func TestConductOnlyEverReads(t *testing.T) {
	srv, rec := recorded(t, routes(map[string]string{"/events": eventsPage}))
	if _, err := crawl(t, testConfig(), fmt.Sprintf(target, "a", srv.URL+"/events")); err != nil {
		t.Fatal(err)
	}
	for _, h := range rec.all() {
		if h.Method != http.MethodGet && h.Method != http.MethodHead {
			t.Errorf("%s %s", h.Method, h.Path)
		}
	}
}

func TestConductNeverSendsACookieBackOrACredential(t *testing.T) {
	srv, rec := recorded(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		routes(map[string]string{"/1": eventsPage, "/2": eventsPage})(w, r)
	})
	if _, err := crawl(t, testConfig(), fmt.Sprintf(target, "one", srv.URL+"/1")+fmt.Sprintf(target, "two", srv.URL+"/2")); err != nil {
		t.Fatal(err)
	}
	for _, h := range rec.all() {
		if h.Cookie != "" || h.Auth != "" {
			t.Errorf("%s carried cookie %q auth %q", h.Path, h.Cookie, h.Auth)
		}
	}

	// A cookie set on a redirect must not be replayed on the request that
	// follows it, even though both requests share one Visit (and so, absent
	// DisableCookies, one cookie jar).
	rsrv, rrec := recorded(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
			http.Redirect(w, r, "/events", http.StatusFound)
			return
		}
		routes(map[string]string{"/events": eventsPage})(w, r)
	})
	ids, err := crawl(t, testConfig(), fmt.Sprintf(target, "a", rsrv.URL+"/start"))
	if err != nil || len(ids) == 0 {
		t.Fatalf("err = %v ids = %v; a cookie set on a redirect must not break the crawl", err, ids)
	}
	for _, h := range rrec.all() {
		if h.Path == "/events" && (h.Cookie != "" || h.Auth != "") {
			t.Errorf("%s carried cookie %q auth %q", h.Path, h.Cookie, h.Auth)
		}
	}
}

func TestConductRefusesATargetURLCarryingCredentials(t *testing.T) {
	srv, rec := recorded(t, routes(map[string]string{"/events": eventsPage}))
	withUser := strings.Replace(srv.URL, "http://", "http://me:secret@", 1) + "/events"
	_, err := crawl(t, testConfig(), fmt.Sprintf(target, "a", withUser))
	if err == nil || len(rec.all()) != 0 {
		t.Errorf("err = %v hits = %d; a URL with credentials must never be requested", err, len(rec.all()))
	}
}

func TestATargetCannotExpressAHeaderOrCookie(t *testing.T) {
	for _, extra := range []string{"  headers: {Authorization: x}\n", "  cookies: {session: x}\n", "  auth: {user: x}\n"} {
		if _, err := crawlsource.ParseTargets(fmt.Sprintf(target, "a", "https://library.example/") + extra); err == nil {
			t.Errorf("a target carrying %q was accepted", strings.TrimSpace(extra))
		}
	}
}
