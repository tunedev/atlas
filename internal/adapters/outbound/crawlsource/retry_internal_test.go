package crawlsource

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// scripted answers /p with each status in turn (the last one repeats), and
// records when each /p request arrived.
func scripted(t *testing.T, header http.Header, statuses ...int) (*httptest.Server, *[]time.Time) {
	t.Helper()
	var at []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p" {
			http.NotFound(w, r)
			return
		}
		at = append(at, time.Now())
		status := statuses[min(len(at)-1, len(statuses)-1)]
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		io.WriteString(w, "body")
	}))
	t.Cleanup(srv.Close)
	return srv, &at
}

func retrying(retries int) *Conduct {
	cfg := testConfig()
	cfg.Retries = retries
	return newConduct(cfg, http.DefaultTransport)
}

func TestATransientFailureIsRetried(t *testing.T) {
	srv, at := scripted(t, nil, 503, 200)
	resp, err := get(t, retrying(2), srv.URL+"/p")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("resp = %v err = %v", resp, err)
	}
	if len(*at) != 2 {
		t.Errorf("requests = %d, want 2", len(*at))
	}
}

func TestANotFoundIsNotRetried(t *testing.T) {
	srv, at := scripted(t, nil, 404)
	resp, err := get(t, retrying(2), srv.URL+"/p")
	if err != nil || resp.StatusCode != 404 || len(*at) != 1 {
		t.Errorf("status = %v requests = %d err = %v", resp.StatusCode, len(*at), err)
	}
}

func TestRetriesStopAtTheLimit(t *testing.T) {
	srv, at := scripted(t, nil, 503)
	resp, err := get(t, retrying(2), srv.URL+"/p")
	if err != nil || resp.StatusCode != 503 || len(*at) != 3 {
		t.Errorf("status = %v requests = %d err = %v; want the last 503 after 3 tries", resp.StatusCode, len(*at), err)
	}
}

func TestTooManyRequestsWaitsWhatTheServerAsks(t *testing.T) {
	srv, at := scripted(t, http.Header{"Retry-After": {"1"}}, 429, 200)
	if _, err := get(t, retrying(1), srv.URL+"/p"); err != nil {
		t.Fatal(err)
	}
	if len(*at) != 2 {
		t.Fatalf("requests = %d", len(*at))
	}
	if gap := (*at)[1].Sub((*at)[0]); gap < time.Second-5*time.Millisecond {
		t.Errorf("retried after %s; the server asked for 1s", gap)
	}
}

func TestBackoffDoesNotOverflowOrPanicOnALargeAttempt(t *testing.T) {
	c := retrying(100)
	for _, attempt := range []int{34, 64, 1000} {
		wait := c.backoff(attempt, nil)
		upper := c.cfg.Delay << maxBackoffShift
		if wait < 0 || wait >= upper {
			t.Errorf("backoff(%d) = %s, want [0, %s)", attempt, wait, upper)
		}
	}
}

func TestARetryKeepsTheHostsCrawlDelay(t *testing.T) {
	log := &siteLog{}
	failed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		switch {
		case r.URL.Path == "/robots.txt":
			io.WriteString(w, "User-agent: *\nCrawl-delay: 1\n")
		case r.URL.Path == "/a" && !failed:
			failed = true
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			io.WriteString(w, "page")
		}
	}))
	t.Cleanup(srv.Close)
	c := retrying(1)
	for _, p := range []string{"/a", "/b"} {
		if _, err := get(t, c, srv.URL+p); err != nil {
			t.Fatal(err)
		}
	}
	hits := log.all()
	if len(hits) != 4 {
		t.Fatalf("hits = %v", hits)
	}
	for i := 1; i < len(hits); i++ {
		if gap := hits[i].At.Sub(hits[i-1].At); gap < time.Second-5*time.Millisecond {
			t.Errorf("%s then %s %s apart; robots.txt asked for 1s", hits[i-1].Path, hits[i].Path, gap)
		}
	}
}

func TestAnOversizedBodyIsNotRetried(t *testing.T) {
	log := &siteLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		io.WriteString(w, strings.Repeat("x", 100))
	}))
	t.Cleanup(srv.Close)
	c := retrying(2)
	c.cfg.MaxBytes = 50
	if _, err := get(t, c, srv.URL+"/p"); err == nil || log.count("/p") != 1 {
		t.Errorf("requests = %d err = %v; an over-limit body fails once", log.count("/p"), err)
	}
}
