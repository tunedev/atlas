package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Chain tries each provider in order, skipping any whose breaker is open,
// and returns the first completion that succeeds. It is a ports.Provider
// itself, so a failing engine degrades a feature rather than the path that
// calls it.
type Chain struct {
	providers []ports.Provider
	breakers  []*Breaker
}

// NewChain builds a Chain over providers, in the order given, each guarded
// by its own Breaker built from threshold and cooldown.
func NewChain(threshold int, cooldown time.Duration, providers ...ports.Provider) *Chain {
	breakers := make([]*Breaker, len(providers))
	for i := range providers {
		breakers[i] = NewBreaker(threshold, cooldown)
	}
	return &Chain{providers: providers, breakers: breakers}
}

// Name identifies the chain itself, not whichever provider answered.
func (c *Chain) Name() string { return "chain" }

// Complete tries each provider in order. A provider whose breaker is open is
// skipped. A provider that succeeds closes its breaker and its completion is
// returned. A provider that fails opens its breaker further, unless the
// failure is the caller's context ending, in which case that error is
// returned immediately without touching the breaker or trying the next
// provider -- the client left, the provider did not fail.
func (c *Chain) Complete(ctx context.Context, p ports.Prompt) (ports.Completion, error) {
	var errs []string
	for i, provider := range c.providers {
		breaker := c.breakers[i]
		if !breaker.Allow() {
			errs = append(errs, fmt.Sprintf("%s: breaker open", provider.Name()))
			continue
		}

		completion, err := provider.Complete(ctx, p)
		if err == nil {
			breaker.Success()
			return completion, nil
		}
		if ctx.Err() != nil {
			return ports.Completion{}, ctx.Err()
		}

		breaker.Failure()
		errs = append(errs, fmt.Sprintf("%s: %v", provider.Name(), err))
	}
	return ports.Completion{}, fmt.Errorf("chain: %s", strings.Join(errs, "; "))
}
