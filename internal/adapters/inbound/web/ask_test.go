package web_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web"
	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/ports"
)

// askTool asks its registry's asker whether to shelve a logbook, the way an
// agent's permission check arrives mid-run: on its own goroutine and its own
// context. The decision is recorded on the registry and returned, unless the
// run's context ends first.
type askTool struct{ reg *fakeRegistry }

func (askTool) Name() string { return "ask" }

func (t askTool) Invoke(ctx context.Context, _ map[string]string) (any, error) {
	t.reg.mu.Lock()
	t.reg.calls = append(t.reg.calls, "ask")
	t.reg.mu.Unlock()

	decided := make(chan ports.PermissionDecision, 1)
	go func() {
		d, _ := t.reg.asker.Decide(context.Background(), ports.PermissionRequest{
			ToolName: "shelf.put", Kind: "write", Summary: "shelve the logbook",
		})
		t.reg.decisions <- d
		decided <- d
	}()
	select {
	case d := <-decided:
		return map[string]string{"decision": string(d)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var guarded = &uiv1.RunRequest{View: "shelf", Screen: "guarded", Action: -1}

// receiveAsk reads stream events until an Ask arrives.
func receiveAsk(t *testing.T, stream *connect.ServerStreamForClient[uiv1.RunResponse]) *uiv1.Ask {
	t.Helper()
	for stream.Receive() {
		if ask := stream.Msg().GetAsk(); ask != nil {
			return ask
		}
	}
	t.Fatalf("stream ended without an Ask: %v", stream.Err())
	return nil
}

func TestAskAllowsOnAnswer(t *testing.T) {
	c, origin, _ := serveShelf(t, runConfig(), web.NewAsker(time.Minute))

	stream, err := c.Run(context.Background(), withOrigin(guarded, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	ask := receiveAsk(t, stream)
	if ask.ToolName != "shelf.put" || ask.Kind != "write" || ask.Summary != "shelve the logbook" || len(ask.Id) != 32 {
		t.Fatalf("ask = %v", ask)
	}

	if _, err := c.Answer(context.Background(), withOrigin(&uiv1.AnswerRequest{Id: ask.Id, Allow: true}, origin)); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	var done *uiv1.Done
	for stream.Receive() {
		done = stream.Msg().GetDone()
	}
	if done == nil || !strings.Contains(done.StateJson, `"decision":"allow"`) {
		t.Errorf("done = %v, want the tool allowed; err = %v", done, stream.Err())
	}
}

func TestAskDeniesWithNoActiveRun(t *testing.T) {
	asker := web.NewAsker(time.Minute)

	d, err := asker.Decide(context.Background(), ports.PermissionRequest{ToolName: "shelf.put"})
	if d != ports.PermissionDeny || err != nil {
		t.Errorf("Decide = %v, %v; want deny", d, err)
	}
}

func TestAskDeniesWhenStreamCloses(t *testing.T) {
	c, origin, reg := serveShelf(t, runConfig(), web.NewAsker(time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := c.Run(ctx, withOrigin(guarded, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	receiveAsk(t, stream)
	cancel()

	select {
	case d := <-reg.decisions:
		if d != ports.PermissionDeny {
			t.Errorf("decision = %v, want deny", d)
		}
	case <-time.After(time.Second):
		t.Fatal("Decide still waiting a second after the stream closed")
	}
}

func TestAskDeniesOnTimeout(t *testing.T) {
	c, origin, _ := serveShelf(t, runConfig(), web.NewAsker(50*time.Millisecond))

	events, err := collect(t, c, origin, guarded)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	done := events[len(events)-1].GetDone()
	if done == nil || !strings.Contains(done.StateJson, `"decision":"deny"`) {
		t.Errorf("last event = %v, want Done with the tool denied", events[len(events)-1])
	}
}

func TestAnswerUnknownIDIsNotFound(t *testing.T) {
	c, origin, _ := serveShelf(t, runConfig(), web.NewAsker(time.Minute))

	_, err := c.Answer(context.Background(), withOrigin(&uiv1.AnswerRequest{Id: "feedface", Allow: true}, origin))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("err = %v, want NotFound", err)
	}
}
