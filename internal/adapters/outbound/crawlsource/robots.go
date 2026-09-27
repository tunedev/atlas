package crawlsource

import (
	"context"
	"net/url"
	"sync"

	"github.com/temoto/robotstxt"
)

// robotsCache holds each host's robots.txt for the life of one crawler. Each
// host has its own entry, so a slow robots.txt on one host never delays a
// check on another.
type robotsCache struct {
	mu     sync.Mutex
	byHost map[string]*robotsEntry
}

// robotsEntry is one host's robots.txt. Its lock holds a single token: a
// check takes it for the whole fetch, so concurrent checks on one host fetch
// once.
type robotsEntry struct {
	lock chan struct{}
	data *robotstxt.RobotsData
}

func newRobotsCache() *robotsCache {
	return &robotsCache{byHost: map[string]*robotsEntry{}}
}

// fetchFunc reads the robots.txt of u's host and reports whether the result
// may be kept for later checks.
type fetchFunc func(ctx context.Context, u *url.URL) (data *robotstxt.RobotsData, keep bool, err error)

// data returns the robots.txt data for u's host, fetching it with fetch until
// a result worth keeping is cached.
func (r *robotsCache) data(ctx context.Context, u *url.URL, fetch fetchFunc) (*robotstxt.RobotsData, error) {
	e := r.entry(u.Scheme + "://" + u.Host)
	select {
	case e.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-e.lock }()
	if e.data != nil {
		return e.data, nil
	}
	data, keep, err := fetch(ctx, u)
	if err != nil {
		return nil, err
	}
	if keep {
		e.data = data
	}
	return data, nil
}

// entry returns key's entry, adding it the first time key is seen.
func (r *robotsCache) entry(key string) *robotsEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byHost[key]
	if !ok {
		e = &robotsEntry{lock: make(chan struct{}, 1)}
		r.byHost[key] = e
	}
	return e
}
