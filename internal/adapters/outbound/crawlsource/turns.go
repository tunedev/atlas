package crawlsource

import (
	"context"
	"sync"
	"time"
)

// hostTurns spaces requests to one host by at least gap, or by a longer
// floor the host asks for. Hosts never wait on each other.
type hostTurns struct {
	gap  time.Duration
	mu   sync.Mutex
	next map[string]time.Time
}

func newHostTurns(gap time.Duration) *hostTurns {
	return &hostTurns{gap: gap, next: map[string]time.Time{}}
}

// wait blocks until host's turn, then books the following one.
func (h *hostTurns) wait(ctx context.Context, host string, floor time.Duration) error {
	gap := max(h.gap, floor)
	h.mu.Lock()
	now := time.Now()
	at := h.next[host]
	if at.Before(now) {
		at = now
	}
	h.next[host] = at.Add(gap)
	h.mu.Unlock()

	return sleep(ctx, time.Until(at))
}

// sleep blocks for d, or until ctx is done.
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
