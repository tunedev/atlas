package crawlsource

import (
	"context"
	"errors"
	"net/url"

	"github.com/PuerkitoBio/goquery"
)

var ErrNoBrowser = errors.New("rendering is on, but no Chrome or Chromium was found on this machine; atlas never downloads a browser")

type browser interface {
	render(ctx context.Context, u *url.URL) (string, error)
}

type chrome struct{}

func newChrome(Config, *Conduct) browser { return chrome{} }

func (chrome) render(context.Context, *url.URL) (string, error) { return "", errRenderingOff }

func (s *Source) rendered(ctx context.Context, t Target) (*goquery.Selection, *url.URL, error) {
	return nil, nil, errRenderingOff
}
