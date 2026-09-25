// Package crawlsource implements ports.Source by crawling web pages. What to
// crawl and how to read each page is data a pack supplies; how the crawler
// behaves on the network is fixed in Conduct and has no switch.
package crawlsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Config is how the crawler behaves: its identity, the least gap between two
// requests to one host, the bounds on a request, a page and a whole crawl,
// how many times to retry a read, where to keep revalidation state, and
// whether rendering is allowed at all.
type Config struct {
	UserAgent     string
	Delay         time.Duration
	Timeout       time.Duration
	PullTimeout   time.Duration
	MaxBytes      int64
	Retries       int
	CacheDir      string
	Render        bool
	RenderTimeout time.Duration
}

// Crawler holds the Conduct and browser a process crawls through, built
// once. Every Source it builds shares them, so the robots.txt cache and the
// per-host pacing hold across every Source's Pull, not just within one.
type Crawler struct {
	cfg     Config
	conduct *Conduct
	browser browser
}

var _ ports.Source = (*Source)(nil)

// Source crawls its targets one after another through a Conduct, reading
// each page with Colly, or with a browser when a target asks for rendering
// and rendering is on.
type Source struct {
	cfg     Config
	targets []Target
	conduct *Conduct
	browser browser

	mu     sync.Mutex
	last   time.Time
	report Report
}

// Report is what the last Pull did beyond yielding items.
type Report struct {
	Revalidated int
}

// Failure is one target that yielded nothing, and why.
type Failure struct {
	Target string
	URL    string
	Kind   string
	Err    error
}

// Failures is every target of a Pull that yielded nothing.
type Failures []Failure

func (fs Failures) Error() string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = fmt.Sprintf("%s (%s): %v", f.Target, f.Kind, f.Err)
	}
	return "crawlsource: " + strings.Join(parts, "; ")
}

var errRenderingOff = errors.New("this target needs rendering, and rendering is off; enable it with -crawl-render")

// emptyError is a page that was fetched and yielded no item.
type emptyError struct {
	kind, url, item string
}

func (e *emptyError) Error() string {
	if e.kind == KindNeedsRendering {
		return fmt.Sprintf("%s matched no %q and looks like an application shell; it needs rendering", e.url, e.item)
	}
	return fmt.Sprintf("%s matched no %q; the page no longer matches its rules", e.url, e.item)
}

// NewCrawler checks cfg and builds a Crawler: its Conduct and browser. It
// does no I/O.
func NewCrawler(cfg Config) (*Crawler, error) {
	switch {
	case cfg.UserAgent == "":
		return nil, errors.New("crawlsource: no user agent")
	case cfg.Delay <= 0:
		return nil, errors.New("crawlsource: delay must be positive")
	case cfg.Timeout <= 0, cfg.PullTimeout <= 0:
		return nil, errors.New("crawlsource: timeouts must be positive")
	case cfg.MaxBytes <= 0:
		return nil, errors.New("crawlsource: max bytes must be positive")
	}
	conduct := newConduct(cfg, http.DefaultTransport.(*http.Transport).Clone())
	return &Crawler{cfg: cfg, conduct: conduct, browser: newChrome(cfg, conduct)}, nil
}

// Source builds a Source over targets that shares this Crawler's Conduct and
// browser, so pacing holds across every Source the Crawler builds.
func (c *Crawler) Source(targets []Target) (*Source, error) {
	if len(targets) == 0 {
		return nil, errors.New("crawlsource: no targets")
	}
	return &Source{cfg: c.cfg, targets: targets, conduct: c.conduct, browser: c.browser}, nil
}

// New checks cfg and targets and builds a Source over a fresh Crawler. It
// does no I/O.
func New(cfg Config, targets []Target) (*Source, error) {
	c, err := NewCrawler(cfg)
	if err != nil {
		return nil, err
	}
	return c.Source(targets)
}

// Pull crawls every target. It returns the items it reached, with a Failures
// error naming each target that yielded none; it returns the error alone
// when every target failed.
func (s *Source) Pull(ctx context.Context) ([]ports.Item, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.PullTimeout)
	defer cancel()
	before := s.conduct.Revalidated()

	var items []ports.Item
	var failed Failures
	for _, t := range s.targets {
		got, err := s.crawl(ctx, t)
		if err != nil {
			failed = append(failed, Failure{Target: t.ID, URL: t.URL, Kind: kindOf(err), Err: err})
			continue
		}
		items = append(items, got...)
	}

	s.mu.Lock()
	s.last = time.Now().UTC()
	s.report = Report{Revalidated: int(s.conduct.Revalidated() - before)}
	s.mu.Unlock()

	switch {
	case len(failed) == len(s.targets):
		return nil, failed
	case len(failed) > 0:
		return items, failed
	}
	return items, nil
}

// LastRefreshed is when the last Pull finished. It fails before any has.
func (s *Source) LastRefreshed(_ context.Context) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.IsZero() {
		return time.Time{}, errors.New("crawlsource: nothing crawled yet")
	}
	return s.last, nil
}

// LastReport is what the last Pull did beyond yielding items.
func (s *Source) LastReport() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report
}

func (s *Source) crawl(ctx context.Context, t Target) ([]ports.Item, error) {
	page, base, err := s.fetch(ctx, t)
	if err != nil {
		return nil, err
	}
	fields := extract(page, t, base)
	if len(fields) == 0 {
		return nil, &emptyError{kind: emptyKind(page), url: t.URL, item: t.Item}
	}
	return itemsOf(t, fields, time.Now().UTC())
}

func (s *Source) fetch(ctx context.Context, t Target) (*goquery.Selection, *url.URL, error) {
	if t.Render {
		if !s.cfg.Render {
			return nil, nil, fmt.Errorf("%s: %w", t.URL, errRenderingOff)
		}
		return s.rendered(ctx, t)
	}
	return s.fetched(ctx, t)
}

// fetched reads t's page with Colly, through the Conduct.
func (s *Source) fetched(ctx context.Context, t Target) (*goquery.Selection, *url.URL, error) {
	c := colly.NewCollector(colly.StdlibContext(ctx), colly.AllowURLRevisit())
	// NewCollector applies COLLY_* environment settings; these pin every one.
	c.UserAgent = s.cfg.UserAgent
	c.CacheDir = ""
	c.ParseHTTPErrorResponse = false
	c.AllowedDomains, c.DisallowedDomains = nil, nil
	c.DetectCharset = false
	c.MaxDepth, c.MaxRequests = 0, 0
	c.TraceHTTP = false
	c.SetRedirectHandler(nil)
	c.IgnoreRobotsTxt = true // the Conduct checks robots.txt, for both fetch paths
	c.MaxBodySize = 0        // the Conduct bounds the body, failing rather than truncating
	c.DisableCookies()
	c.SetRequestTimeout(0) // the Conduct times each attempt; PullTimeout bounds the crawl
	c.WithTransport(s.conduct)

	var page *goquery.Selection
	var base *url.URL
	var fetchErr error
	c.OnHTML("html", func(e *colly.HTMLElement) { page, base = e.DOM, e.Request.URL })
	c.OnError(func(r *colly.Response, err error) {
		fetchErr = fmt.Errorf("%s: status %d: %w", t.URL, r.StatusCode, err)
	})
	if err := c.Visit(t.URL); err != nil && fetchErr == nil {
		fetchErr = fmt.Errorf("%s: %w", t.URL, err)
	}
	if fetchErr != nil {
		return nil, nil, fetchErr
	}
	if page == nil {
		return nil, nil, fmt.Errorf("%s: response is not HTML", t.URL)
	}
	return page, base, nil
}

func itemsOf(t Target, fields []map[string]string, when time.Time) ([]ports.Item, error) {
	seen := map[string]bool{}
	var out []ports.Item
	for _, f := range fields {
		sum := sha256.Sum256([]byte(f[t.Key]))
		id := t.ID + "/" + hex.EncodeToString(sum[:8])
		if seen[id] {
			continue
		}
		seen[id] = true
		body, err := json.Marshal(f)
		if err != nil {
			return nil, fmt.Errorf("crawlsource: encode %s: %w", id, err)
		}
		out = append(out, ports.Item{ID: id, Body: body, When: when})
	}
	return out, nil
}

// kindOf names why a target failed.
func kindOf(err error) string {
	var empty *emptyError
	switch {
	case errors.As(err, &empty):
		return empty.kind
	case errors.Is(err, ErrDisallowed):
		return KindDisallowed
	case errors.Is(err, errRenderingOff):
		return KindNeedsRendering
	case errors.Is(err, ErrNoBrowser):
		return KindRenderingUnavailable
	}
	return KindFetch
}
