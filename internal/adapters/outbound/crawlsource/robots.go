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

// fetchFunc reads the robots.txt of u's host and reports whether the result
// may be kept for later checks.
type fetchFunc func(ctx context.Context, u *url.URL) (data *robotstxt.RobotsData, keep bool, err error)

// data returns the robots.txt data for u's host, fetching it with fetch until
// a result worth keeping is cached.
func (r *robotsCache) data(ctx context.Context, u *url.URL, fetch fetchFunc) (*robotstxt.RobotsData, error) {
	key := u.Scheme + "://" + u.Host
	r.mu.Lock()
	defer r.mu.Unlock()
	data, ok := r.byHost[key]
	if !ok {
		var keep bool
		var err error
		if data, keep, err = fetch(ctx, u); err != nil {
			return nil, err
		}
		if keep {
			r.byHost[key] = data
		}
	}
	return data, nil
}
