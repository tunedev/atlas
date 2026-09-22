package app

import (
	"sync"
	"time"
)

// Breaker tracks one provider's recent failures and stops calling it once
// they cross a threshold, allowing calls again once a cooldown has elapsed.
// A failure after the cooldown re-arms it immediately. It is the app's half
// of Tenet 3: the app classifies and reacts, the mesh does not know a
// provider exists.
type Breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	failures  int
	openedAt  time.Time
}

// NewBreaker builds a Breaker that opens after threshold consecutive
// failures and allows calls again once cooldown has elapsed since it
// opened.
func NewBreaker(threshold int, cooldown time.Duration) *Breaker {
	return &Breaker{threshold: threshold, cooldown: cooldown}
}

// Allow reports whether a call may proceed: the breaker is closed, or it is
// open but the cooldown has elapsed since it opened. Every caller is allowed
// once the cooldown passes, not only a single probe; a subsequent Failure
// re-arms the cooldown immediately.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.failures < b.threshold {
		return true
	}
	return time.Since(b.openedAt) >= b.cooldown
}

// Success resets the failure count, closing the breaker.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures = 0
}

// Failure records a failure, opening the breaker once it reaches threshold.
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures++
	if b.failures >= b.threshold {
		b.openedAt = time.Now()
	}
}
