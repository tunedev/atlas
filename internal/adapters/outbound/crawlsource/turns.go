package crawlsource

import (
	"context"
	"sync"
	"time"
)

// hostTurns spaces requests to one host by at least gap, or by a longer
// floor the host asks for. Hosts never wait on each other.
type hostTurns struct {
	gap   time.Duration
	mu    sync.Mutex
	last  map[string]time.Time
	floor map[string]time.Duration
}

func newHostTurns(gap time.Duration) *hostTurns {
	return &hostTurns{gap: gap, last: map[string]time.Time{}, floor: map[string]time.Duration{}}
}

// setFloor makes every later turn on host at least d after the one before.
func (h *hostTurns) setFloor(host string, d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.floor[host] = d
}

// wait blocks until host's turn and books it.
func (h *hostTurns) wait(ctx context.Context, host string) error {
	h.mu.Lock()
	at := time.Now()
	if next := h.last[host].Add(max(h.gap, h.floor[host])); next.After(at) {
		at = next
	}
	h.last[host] = at
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
