package app_test

import (
	"sync"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestABreakerOpensAfterItsThreshold(t *testing.T) {
	b := app.NewBreaker(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("breaker opened after %d failures, threshold is 3", i)
		}
		b.Failure()
	}
	if b.Allow() {
		t.Error("breaker did not open at its threshold")
	}
}

func TestASuccessResetsTheCount(t *testing.T) {
	b := app.NewBreaker(3, time.Minute)
	b.Failure()
	b.Failure()
	b.Success()
	b.Failure()
	b.Failure()
	if !b.Allow() {
		t.Error("a success did not reset the failure count")
	}
}

func TestAnOpenBreakerProbesAfterItsCooldown(t *testing.T) {
	b := app.NewBreaker(1, 10*time.Millisecond)
	b.Failure()
	if b.Allow() {
		t.Fatal("breaker did not open")
	}
	time.Sleep(15 * time.Millisecond)
	if !b.Allow() {
		t.Error("breaker never closes again; it has no recovery probe")
	}
}

func TestABreakerIsSafeUnderConcurrentUse(t *testing.T) {
	b := app.NewBreaker(100, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Allow()
			b.Failure()
			b.Success()
		}()
	}
	wg.Wait()
}
