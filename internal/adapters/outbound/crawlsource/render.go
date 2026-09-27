package crawlsource

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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

// browser renders u and returns the page's HTML with the URL it ended on.
type browser interface {
	render(ctx context.Context, u *url.URL) (string, *url.URL, error)
}

// chrome renders a page in a browser already installed on this machine,
// found by lookPath. It never downloads one: every launcher is given Bin,
// which turns Rod's download off. Each render uses a fresh temporary
// profile that blocks every cookie, so neither a cookie of the user's nor
// one the site sets is sent, and overrides the browser's user agent with the
// crawler's. Only the page's own navigation passes the Conduct. The URL a
// redirect ends on is checked against robots.txt after the browser has
// requested it, so a disallowed landing page fails the render but has been
// fetched once; what the page then loads, the browser fetches itself.
type chrome struct {
	cfg      Config
	conduct  *Conduct
	lookPath func() (string, bool)
}

func newChrome(cfg Config, conduct *Conduct) browser {
	return chrome{cfg: cfg, conduct: conduct, lookPath: launcher.LookPath}
}

func (c chrome) render(ctx context.Context, u *url.URL) (string, *url.URL, error) {
	bin, ok := c.lookPath()
	if !ok {
		return "", nil, ErrNoBrowser
	}
	if err := c.conduct.Allow(ctx, u); err != nil {
		return "", nil, err
	}
	profile, err := os.MkdirTemp("", "atlas-render-*")
	if err != nil {
		return "", nil, fmt.Errorf("make a profile: %w", err)
	}
	defer os.RemoveAll(profile)
	if err := blockCookies(profile); err != nil {
		return "", nil, fmt.Errorf("make a profile: %w", err)
	}
	l := launcher.New().Bin(bin).UserDataDir(profile).Headless(true).Context(ctx)
	control, err := l.Launch()
	if err != nil {
		// Launch's leakless branch can return an error after starting the
		// browser process and setting the PID, without killing it or
		// closing the exit channel Cleanup waits on. Kill is a no-op when
		// no process started (PID 0); Cleanup is not called here because it
		// would block forever on that unclosed channel.
		l.Kill()
		return "", nil, fmt.Errorf("launch %s: %w", bin, err)
	}
	defer l.Cleanup()
	defer l.Kill()

	b := rod.New().ControlURL(control).Context(ctx)
	if err := b.Connect(); err != nil {
		return "", nil, fmt.Errorf("connect to %s: %w", bin, err)
	}
	defer b.Close()

	page, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return "", nil, fmt.Errorf("open page: %w", err)
	}
	if err := (proto.NetworkSetUserAgentOverride{UserAgent: c.cfg.UserAgent}).Call(page); err != nil {
		return "", nil, fmt.Errorf("set user agent: %w", err)
	}
	page = page.Timeout(c.cfg.RenderTimeout)
	c.conduct.turns.sent(u.Host)
	if err := page.Navigate(u.String()); err != nil {
		return "", nil, fmt.Errorf("navigate %s: %w", u, err)
	}
	if err := page.WaitLoad(); err != nil {
		return "", nil, fmt.Errorf("load %s: %w", u, err)
	}
	info, err := page.Info()
	if err != nil {
		return "", nil, fmt.Errorf("read the URL of %s: %w", u, err)
	}
	final, err := c.landed(ctx, u, info.URL)
	if err != nil {
		return "", nil, err
	}
	if err := page.WaitDOMStable(settle, 0); err != nil {
		return "", nil, fmt.Errorf("settle %s: %w", u, err)
	}
	html, err := page.HTML()
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", u, err)
	}
	if html, err = boundHTML(html, c.cfg.MaxBytes); err != nil {
		return "", nil, err
	}
	return html, final, nil
}

// cookiesBlocked is a Chrome profile's Preferences with every site's cookies
// blocked: content setting 2 is "block".
const cookiesBlocked = `{"profile":{"default_content_setting_values":{"cookies":2}}}`

// blockCookies writes a Preferences file into the Chrome profile at dir that
// blocks every cookie, so a site's cookie is neither stored nor sent back.
func blockCookies(dir string) error {
	def := filepath.Join(dir, "Default")
	if err := os.MkdirAll(def, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(def, "Preferences"), []byte(cookiesBlocked), 0o600)
}

// boundHTML returns html, or fails when it is longer than max bytes.
func boundHTML(html string, max int64) (string, error) {
	if int64(len(html)) > max {
		return "", fmt.Errorf("%w of %d bytes", errTooLarge, max)
	}
	return html, nil
}

// landed returns the URL a render of u ended on, checking it against
// robots.txt when it differs from u.
func (c chrome) landed(ctx context.Context, u *url.URL, final string) (*url.URL, error) {
	if final == u.String() {
		return u, nil
	}
	f, err := url.Parse(final)
	if err != nil {
		return nil, fmt.Errorf("render of %s ended on %q: %w", u, final, err)
	}
	if err := c.conduct.permitted(ctx, f); err != nil {
		return nil, fmt.Errorf("render of %s ended on %s: %w", u, f.Redacted(), err)
	}
	return f, nil
}

// rendered reads t's page through the browser, returning it with the URL
// the browser ended on.
func (s *Source) rendered(ctx context.Context, t Target) (*goquery.Selection, *url.URL, error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", t.URL, err)
	}
	html, final, err := s.browser.render(ctx, u)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", t.URL, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: parse rendered page: %w", t.URL, err)
	}
	return doc.Selection, final, nil
}
