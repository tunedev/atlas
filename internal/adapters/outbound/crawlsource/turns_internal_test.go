package crawlsource

import (
	"context"
	"testing"
	"time"
)

// A request can go out after its turn: a timer that wakes late, a cache read,
// a browser starting. The next turn on that host must be a full gap after the
// request actually went out, not after the turn it was given.
func TestTheNextTurnCountsFromWhenARequestWentOut(t *testing.T) {
	gap := 100 * time.Millisecond
	h := newHostTurns(gap)
	ctx := context.Background()

	if err := h.wait(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond) // the request goes out 40ms after its turn
	h.sent("a.example")
	sentAt := time.Now()

	if err := h.wait(ctx, "a.example"); err != nil {
		t.Fatal(err)
	}
	if got := time.Since(sentAt); got < gap {
		t.Errorf("next turn came %s after the request went out; the gap is %s", got, gap)
	}
}
