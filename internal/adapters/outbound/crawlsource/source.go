// Package crawlsource implements ports.Source by crawling web pages. What to
// crawl and how to read each page is data a pack supplies; how the crawler
// behaves on the network is fixed in Conduct and has no switch.
package crawlsource

import "time"

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
