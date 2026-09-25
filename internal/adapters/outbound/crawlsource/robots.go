package crawlsource

import (
	"context"
	"net/url"
	"sync"

	"github.com/temoto/robotstxt"
)

// robotsCache holds each host's robots.txt for the life of one crawler.
type robotsCache struct {
	mu     sync.Mutex
	byHost map[string]*robotstxt.RobotsData
}

func newRobotsCache() *robotsCache {
	return &robotsCache{byHost: map[string]*robotstxt.RobotsData{}}
}

// data returns the robots.txt data for u's host, fetching it with fetch the
// first time the host is seen.
func (r *robotsCache) data(ctx context.Context, u *url.URL, fetch func(context.Context, *url.URL) (*robotstxt.RobotsData, error)) (*robotstxt.RobotsData, error) {
	key := u.Scheme + "://" + u.Host
	r.mu.Lock()
	defer r.mu.Unlock()
	data, ok := r.byHost[key]
	if !ok {
		var err error
		if data, err = fetch(ctx, u); err != nil {
			return nil, err
		}
		r.byHost[key] = data
	}
	return data, nil
}
