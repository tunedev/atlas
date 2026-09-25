package crawlsource

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// send makes req, retrying up to cfg.Retries times after a network error, a
// 429 or a 5xx. It waits what a Retry-After asks, or else an exponential
// backoff from cfg.Delay with full jitter, and every retry takes its host's
// turn again. The last response or error is returned as it came.
func (c *Conduct) send(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.next.RoundTrip(req)
		if attempt >= c.cfg.Retries || !transient(resp, err) || req.Context().Err() != nil {
			return resp, err
		}
		wait := c.backoff(attempt, resp)
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		if err := sleep(req.Context(), wait); err != nil {
			return nil, err
		}
		if err := c.turns.wait(req.Context(), req.URL.Host, 0); err != nil {
			return nil, err
		}
	}
}

// transient reports whether an outcome is worth another try.
func transient(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
}

// backoff is how long to wait before retrying after attempt.
func (c *Conduct) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return rand.N(c.cfg.Delay << attempt)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
