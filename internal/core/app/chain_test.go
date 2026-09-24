package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// scriptedProvider is a ports.Provider that records how many times it was
// called and returns either a fixed completion or a fixed error.
type scriptedProvider struct {
	name  string
	text  string
	err   error
	calls int
}

func (p *scriptedProvider) Name() string { return p.name }

func (p *scriptedProvider) Complete(ctx context.Context, prompt ports.Prompt) (ports.Completion, error) {
	p.calls++
	if p.err != nil {
		return ports.Completion{}, p.err
	}
	return ports.Completion{Text: p.text}, nil
}

func TestTheChainUsesTheFirstProviderThatAnswers(t *testing.T) {
	failing := &scriptedProvider{name: "first", err: errors.New("down")}
	working := &scriptedProvider{name: "second", text: "answered"}

	got, err := app.NewChain(3, time.Minute, failing, working).Complete(context.Background(), ports.Prompt{User: "q"})
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if got.Text != "answered" {
		t.Errorf("text = %q, want answered", got.Text)
	}
	if working.calls != 1 {
		t.Errorf("second provider called %d times, want 1", working.calls)
	}
}

func TestAChainWithEveryProviderFailingNamesAllOfThem(t *testing.T) {
	a := &scriptedProvider{name: "alpha", err: errors.New("refused")}
	b := &scriptedProvider{name: "bravo", err: errors.New("timed out")}

	_, err := app.NewChain(3, time.Minute, a, b).Complete(context.Background(), ports.Prompt{User: "q"})
	if err == nil {
		t.Fatal("every provider failed and the chain returned no error")
	}
	for _, want := range []string{"alpha", "refused", "bravo", "timed out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func TestARepeatedlyFailingProviderIsSkipped(t *testing.T) {
	failing := &scriptedProvider{name: "first", err: errors.New("down")}
	working := &scriptedProvider{name: "second", text: "answered"}
	chain := app.NewChain(3, time.Minute, failing, working)

	for i := 0; i < 10; i++ {
		if _, err := chain.Complete(context.Background(), ports.Prompt{User: "q"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if failing.calls != 3 {
		t.Errorf("failing provider was called %d times, want 3 (its threshold); the breaker should stop calling it after that", failing.calls)
	}
}

func TestAChainWithNoProvidersNamesThatRatherThanReturningAnEmptyMessage(t *testing.T) {
	_, err := app.NewChain(3, time.Minute).Complete(context.Background(), ports.Prompt{User: "q"})
	if err == nil {
		t.Fatal("a chain with no providers returned no error")
	}
	if err.Error() != "chain: no providers" {
		t.Errorf("err = %q, want %q", err.Error(), "chain: no providers")
	}
}

func TestACancelledContextStopsTheChainRatherThanFallingThrough(t *testing.T) {
	a := &scriptedProvider{name: "alpha", err: context.Canceled}
	b := &scriptedProvider{name: "bravo", text: "answered"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := app.NewChain(3, time.Minute, a, b).Complete(ctx, ports.Prompt{User: "q"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("error does not name the provider whose call was cancelled: %v", err)
	}
	if b.calls != 0 {
		t.Errorf("second provider was called %d times after cancellation; a dead client hammers every provider", b.calls)
	}
}
