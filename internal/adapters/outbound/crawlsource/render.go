package crawlsource

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

var ErrNoBrowser = errors.New("rendering is on, but no Chrome or Chromium was found on this machine; atlas never downloads a browser")

// settle is how long the DOM must stay unchanged before a rendered page is
// read.
const settle = 300 * time.Millisecond

type browser interface {
	render(ctx context.Context, u *url.URL) (string, error)
}

// chrome renders a page in a browser already installed on this machine,
// found by lookPath. It never downloads one: every launcher is given Bin,
// which turns Rod's download off. Each render uses a fresh temporary
// profile, so no cookie of the user's reaches a site, and overrides the
// browser's user agent with the crawler's. Only the page's own navigation
// passes the Conduct; what the page then loads, the browser fetches itself.
type chrome struct {
	cfg      Config
	conduct  *Conduct
	lookPath func() (string, bool)
}

func newChrome(cfg Config, conduct *Conduct) browser {
	return chrome{cfg: cfg, conduct: conduct, lookPath: launcher.LookPath}
}

func (c chrome) render(ctx context.Context, u *url.URL) (string, error) {
	bin, ok := c.lookPath()
	if !ok {
		return "", ErrNoBrowser
	}
	if err := c.conduct.Allow(ctx, u); err != nil {
		return "", err
	}
	l := launcher.New().Bin(bin).Headless(true).Context(ctx)
	control, err := l.Launch()
	if err != nil {
		return "", fmt.Errorf("launch %s: %w", bin, err)
	}
	defer l.Cleanup()
	defer l.Kill()

	b := rod.New().ControlURL(control).Context(ctx)
	if err := b.Connect(); err != nil {
		return "", fmt.Errorf("connect to %s: %w", bin, err)
	}
	defer b.Close()

	page, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return "", fmt.Errorf("open page: %w", err)
	}
	if err := (proto.NetworkSetUserAgentOverride{UserAgent: c.cfg.UserAgent}).Call(page); err != nil {
		return "", fmt.Errorf("set user agent: %w", err)
	}
	page = page.Timeout(c.cfg.RenderTimeout)
	if err := page.Navigate(u.String()); err != nil {
		return "", fmt.Errorf("navigate %s: %w", u, err)
	}
	if err := page.WaitLoad(); err != nil {
		return "", fmt.Errorf("load %s: %w", u, err)
	}
	if err := page.WaitDOMStable(settle, 0); err != nil {
		return "", fmt.Errorf("settle %s: %w", u, err)
	}
	return page.HTML()
}

// rendered reads t's page through the browser.
func (s *Source) rendered(ctx context.Context, t Target) (*goquery.Selection, *url.URL, error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", t.URL, err)
	}
	html, err := s.browser.render(ctx, u)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", t.URL, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: parse rendered page: %w", t.URL, err)
	}
	return doc.Selection, u, nil
}
