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
// passes the Conduct, and the URL it ends on is checked against robots.txt;
// what the page then loads, the browser fetches itself.
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
		// Launch's leakless branch can return an error after starting the
		// browser process and setting the PID, without killing it or
		// closing the exit channel Cleanup waits on. Kill is a no-op when
		// no process started (PID 0); Cleanup is not called here because it
		// would block forever on that unclosed channel.
		l.Kill()
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
	info, err := page.Info()
	if err != nil {
		return "", fmt.Errorf("read the URL of %s: %w", u, err)
	}
	if err := c.landed(ctx, u, info.URL); err != nil {
		return "", err
	}
	if err := page.WaitDOMStable(settle, 0); err != nil {
		return "", fmt.Errorf("settle %s: %w", u, err)
	}
	html, err := page.HTML()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", u, err)
	}
	return boundHTML(html, c.cfg.MaxBytes)
}

// boundHTML returns html, or fails when it is longer than max bytes.
func boundHTML(html string, max int64) (string, error) {
	if int64(len(html)) > max {
		return "", fmt.Errorf("%w of %d bytes", errTooLarge, max)
	}
	return html, nil
}

// landed checks the URL a render of u ended on against robots.txt when it
// differs from u.
func (c chrome) landed(ctx context.Context, u *url.URL, final string) error {
	if final == u.String() {
		return nil
	}
	f, err := url.Parse(final)
	if err != nil {
		return fmt.Errorf("render of %s ended on %q: %w", u, final, err)
	}
	if err := c.conduct.permitted(ctx, f); err != nil {
		return fmt.Errorf("render of %s ended on %s: %w", u, f.Redacted(), err)
	}
	return nil
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
