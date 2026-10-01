package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/ports"
)

// Asker is a ports.Permission that asks the person watching the active run.
// With no active run, a closed stream or no answer within the timeout, it
// denies.
type Asker struct {
	mu      sync.Mutex
	active  *activeRun
	pending map[string]chan bool
	timeout time.Duration
}

// activeRun is the run holding the slot: send writes to its stream, and done
// closes when the run ends.
type activeRun struct {
	send func(*uiv1.RunResponse) error
	done chan struct{}
}

// NewAsker returns an Asker that waits up to timeout for each answer.
func NewAsker(timeout time.Duration) *Asker {
	return &Asker{pending: make(map[string]chan bool), timeout: timeout}
}

// Decide sends req to the active run's stream as an Ask and waits for its
// Answer. Only an answer that allows returns PermissionAllow.
func (a *Asker) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	id, answer, done, ok := a.ask(req)
	if !ok {
		return ports.PermissionDeny, nil
	}
	defer a.forget(id)

	select {
	case allow := <-answer:
		if allow {
			return ports.PermissionAllow, nil
		}
		return ports.PermissionDeny, nil
	case <-ctx.Done():
		return ports.PermissionDeny, fmt.Errorf("web: %w", ctx.Err())
	case <-done:
		return ports.PermissionDeny, nil
	case <-time.After(a.timeout):
		return ports.PermissionDeny, nil
	}
}

// ask registers a pending answer and sends the Ask on the active run's
// stream. The send holds the lock, so it never reaches a run that has ended.
// ok is false when there is no active run or the send fails.
func (a *Asker) ask(req ports.PermissionRequest) (id string, answer chan bool, done chan struct{}, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return "", nil, nil, false
	}
	id = newAskID()
	msg := &uiv1.Ask{Id: id, ToolName: req.ToolName, Kind: req.Kind, Summary: req.Summary}
	if err := a.active.send(&uiv1.RunResponse{Event: &uiv1.RunResponse_Ask{Ask: msg}}); err != nil {
		return "", nil, nil, false
	}
	answer = make(chan bool, 1)
	a.pending[id] = answer
	return id, answer, a.active.done, true
}

func (a *Asker) forget(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, id)
}

// begin makes send the active run's stream. The returned func clears the
// active run and closes its done channel.
func (a *Asker) begin(send func(*uiv1.RunResponse) error) func() {
	run := &activeRun{send: send, done: make(chan struct{})}
	a.mu.Lock()
	a.active = run
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		a.active = nil
		a.mu.Unlock()
		close(run.done)
	}
}

// answer delivers allow to the ask named id without blocking. It reports
// whether that ask is pending.
func (a *Asker) answer(id string, allow bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, ok := a.pending[id]
	if !ok {
		return false
	}
	select {
	case ch <- allow:
	default:
	}
	return true
}

func newAskID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("web: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Answer delivers the browser's answer to a pending ask.
func (s *Server) Answer(_ context.Context, req *connect.Request[uiv1.AnswerRequest]) (*connect.Response[uiv1.AnswerResponse], error) {
	if s.deps.Asker == nil || !s.deps.Asker.answer(req.Msg.Id, req.Msg.Allow) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("web: no pending ask %q", req.Msg.Id))
	}
	return connect.NewResponse(&uiv1.AnswerResponse{}), nil
}
