package crawlsource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/temoto/robotstxt"
)

var (
	ErrDisallowed = errors.New("robots.txt disallows it")
	ErrCredential = errors.New("the crawler never sends a credential")
	ErrMethod     = errors.New("the crawler only reads")

	errTooLarge = errors.New("body exceeds max size")
)

// Conduct is the crawler's only way onto the network. Every request is a GET
// or HEAD that carries no credential; it is checked against its host's
// robots.txt, waits for its host's turn, carries the configured user agent,
// and has its body bounded. There is no option to switch any of it off.
type Conduct struct {
	cfg         Config
	next        http.RoundTripper
	robots      *robotsCache
	turns       *hostTurns
	cache       revalidator
	revalidated atomic.Int64
}

func newConduct(cfg Config, next http.RoundTripper) *Conduct {
	return &Conduct{
		cfg: cfg, next: next, robots: newRobotsCache(), turns: newHostTurns(cfg.Delay),
		cache: revalidator{dir: cfg.CacheDir},
	}
}

func (c *Conduct) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := refuse(req); err != nil {
		return nil, err
	}
	if err := c.Allow(req.Context(), req.URL); err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	cached, ok := c.cached(req)
	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}
	if ok && resp.StatusCode == http.StatusNotModified {
		if int64(len(cached.Body)) > c.cfg.MaxBytes {
			return nil, fmt.Errorf("crawlsource: %s: body exceeds max size of %d bytes", req.URL.Redacted(), c.cfg.MaxBytes)
		}
		resp.StatusCode, resp.Status = http.StatusOK, "200 OK"
		resp.Body = io.NopCloser(bytes.NewReader(cached.Body))
		resp.ContentLength = int64(len(cached.Body))
		c.revalidated.Add(1)
		return resp, nil
	}
	if resp.StatusCode == http.StatusOK && c.cfg.CacheDir != "" && req.Method == http.MethodGet {
		// resp.Body is send's in-memory reader; this read cannot fail.
		body, _ := io.ReadAll(resp.Body)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if err := c.cache.save(req.URL.String(), resp, body); err != nil {
			return nil, fmt.Errorf("crawlsource: cache %s: %w", req.URL.Redacted(), err)
		}
	}
	return resp, nil
}

// cached makes req conditional when a GET for its URL was cached, returning
// what was cached.
func (c *Conduct) cached(req *http.Request) (stored, bool) {
	if c.cfg.CacheDir == "" || req.Method != http.MethodGet {
		return stored{}, false
	}
	s, ok := c.cache.load(req.URL.String())
	if ok {
		c.cache.condition(req, s)
	}
	return s, ok
}

// Revalidated is how many requests were answered "not modified" and served
// from the cache.
func (c *Conduct) Revalidated() int64 { return c.revalidated.Load() }

// Allow refuses a URL carrying userinfo, checks u against its host's
// robots.txt, then waits for the host's turn. A Crawl-delay in robots.txt lengthens the turn and never shortens it.
//
// The disallow check goes through RobotsData.TestAgent rather than
// FindGroup+Group.Test: when robots.txt could not be read, fetchRobots hands
// back the library's disallow-everything sentinel, whose FindGroup falls
// back to the library's empty-rules group (Group.Test on it defaults to
// allow, per the "no restrictions by default" rule) rather than reporting
// the sentinel's disallow-all state. TestAgent is the one entry point that
// consults that state.
func (c *Conduct) Allow(ctx context.Context, u *url.URL) error {
	if u.User != nil {
		return fmt.Errorf("crawlsource: %s carries userinfo: %w", u.Redacted(), ErrCredential)
	}
	data, err := c.robots.data(ctx, u, c.fetchRobots)
	if err != nil {
		return err
	}
	if !data.TestAgent(u.RequestURI(), c.cfg.UserAgent) {
		return fmt.Errorf("crawlsource: %s: %w", u, ErrDisallowed)
	}
	c.turns.setFloor(u.Host, data.FindGroup(c.cfg.UserAgent).CrawlDelay)
	return c.turns.wait(ctx, u.Host)
}

// fetchRobots reads the robots.txt of u's host, taking the host's turn like
// any other request and following redirects. A 4xx allows everything; a 5xx,
// an unexpected status or an unreachable host disallows everything.
func (c *Conduct) fetchRobots(ctx context.Context, u *url.URL) (*robotstxt.RobotsData, error) {
	if err := c.turns.wait(ctx, u.Host); err != nil {
		return nil, err
	}
	robotsURL := url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/robots.txt"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("crawlsource: %s: %w", robotsURL.String(), err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := (&http.Client{Transport: c.next, Timeout: c.cfg.Timeout}).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return disallowAll(), nil
	}
	body, err := readBounded(resp.Body, c.cfg.MaxBytes)
	if err != nil {
		return disallowAll(), nil
	}
	data, err := robotstxt.FromStatusAndBytes(resp.StatusCode, body)
	if err != nil {
		return disallowAll(), nil
	}
	return data, nil
}

// disallowAll is the robots.txt a host gets when its own cannot be read.
func disallowAll() *robotstxt.RobotsData {
	data, _ := robotstxt.FromStatusAndBytes(http.StatusServiceUnavailable, nil)
	return data
}

// refuse rejects anything but a plain read: another method, or a credential
// in a header.
func refuse(req *http.Request) error {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return fmt.Errorf("crawlsource: %s %s: %w", req.Method, req.URL.Redacted(), ErrMethod)
	}
	for _, h := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
		if req.Header.Get(h) != "" {
			return fmt.Errorf("crawlsource: %s carries %s: %w", req.URL.Redacted(), h, ErrCredential)
		}
	}
	return nil
}

// bounded replaces resp's body with its bytes read in full, failing if there
// are more than max of them.
func bounded(resp *http.Response, max int64) (*http.Response, error) {
	body, err := readBounded(resp.Body, max)
	if err != nil {
		return nil, fmt.Errorf("crawlsource: %s: %w", resp.Request.URL.Redacted(), err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

// readBounded reads r in full and closes it, failing past max bytes.
func readBounded(r io.ReadCloser, max int64) ([]byte, error) {
	defer r.Close()
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%w of %d bytes", errTooLarge, max)
	}
	return body, nil
}
