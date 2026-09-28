package web

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/domain"
)

// Run executes a declared screen or action: it resolves the request against
// the view files, waits for the single run slot, streams each step's
// progress and ends with Done carrying the run's state. A screen's state is
// cached for its actions to bind.
func (s *Server) Run(ctx context.Context, req *connect.Request[uiv1.RunRequest], stream *connect.ServerStream[uiv1.RunResponse]) error {
	msg := req.Msg
	t, err := s.find(msg)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if run, _ := t.run(); run == "" {
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("web: screen %q has nothing to run", msg.Screen))
	}
	vars, err := s.bind(t, msg)
	if err != nil {
		return err
	}
	bp, err := s.blueprint(t, vars)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	out := &runStream{stream: stream}
	release, err := s.queue(ctx, out)
	if err != nil {
		return err
	}
	defer release()

	state, err := s.execute(ctx, bp, out)
	if err != nil {
		return connect.NewError(connect.CodeUnknown, err)
	}
	stateJSON, err := json.Marshal(state.Outputs())
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("web: %w", err))
	}
	if t.action == nil {
		s.cache.put(cacheKey(msg), stateJSON)
	}
	return out.send(&uiv1.RunResponse{Event: &uiv1.RunResponse_Done{Done: &uiv1.Done{StateJson: string(stateJSON)}}})
}

// target is the screen, and optionally the action, a request names.
type target struct {
	view   View
	screen Screen
	action *Action
}

func (t target) run() (string, map[string]string) {
	if t.action != nil {
		return t.action.Run, t.action.Vars
	}
	return t.screen.Run, t.screen.Vars
}

func (t target) inputs() []string {
	if t.action == nil {
		return nil
	}
	names := make([]string, len(t.action.Input))
	for i, in := range t.action.Input {
		names[i] = in.Name
	}
	return names
}

func (s *Server) find(msg *uiv1.RunRequest) (target, error) {
	vi := slices.IndexFunc(s.deps.Views, func(v View) bool { return v.ID == msg.View })
	if vi < 0 {
		return target{}, fmt.Errorf("web: no view %q", msg.View)
	}
	v := s.deps.Views[vi]
	si := slices.IndexFunc(v.Screens, func(sc Screen) bool { return sc.ID == msg.Screen })
	if si < 0 {
		return target{}, fmt.Errorf("web: view %q has no screen %q", msg.View, msg.Screen)
	}
	t := target{view: v, screen: v.Screens[si]}
	if msg.Action < 0 {
		return t, nil
	}
	if int(msg.Action) >= len(t.screen.Actions) {
		return target{}, fmt.Errorf("web: screen %q has no action %d", msg.Screen, msg.Action)
	}
	t.action = &t.screen.Actions[msg.Action]
	return t, nil
}

// bind resolves the target's vars from the request, the server bindings
// and, for an action that binds state, the screen's cached state.
func (s *Server) bind(t target, msg *uiv1.RunRequest) (map[string]string, error) {
	if err := undeclared("param", msg.Params, t.screen.Params); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := undeclared("input", msg.Inputs, t.inputs()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	b := bindings{params: msg.Params, inputs: msg.Inputs, server: s.deps.Server}
	if t.action != nil {
		if err := t.action.checkInputs(msg.Inputs); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if bindsState(t.action.Vars) {
			state, ok := s.cache.get(cacheKey(msg))
			if !ok {
				err := fmt.Errorf("web: screen %q has no state yet; reload the screen", msg.Screen)
				return nil, connect.NewError(connect.CodeFailedPrecondition, err)
			}
			b.state = state
		}
	}

	_, vars := t.run()
	resolved, err := resolve(vars, b)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return resolved, nil
}

func undeclared(kind string, values map[string]string, declared []string) error {
	for name := range values {
		if !slices.Contains(declared, name) {
			return fmt.Errorf("web: %s %q is not declared", kind, name)
		}
	}
	return nil
}

func bindsState(vars map[string]string) bool {
	for _, binding := range vars {
		if strings.HasPrefix(binding, "state.") {
			return true
		}
	}
	return false
}

func (s *Server) blueprint(t target, vars map[string]string) (domain.Blueprint, error) {
	run, _ := t.run()
	bp, err := s.deps.Load(filepath.Join(t.view.dir, run))
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("web: %w", err)
	}
	bp, err = bp.WithVars(vars)
	if err != nil {
		return domain.Blueprint{}, fmt.Errorf("web: %w", err)
	}
	return bp, nil
}

// queue sends Queued with the number of runs ahead, then waits for the run
// slot. The returned func gives the slot back. pending counts runs waiting
// for or holding the slot.
func (s *Server) queue(ctx context.Context, out *runStream) (func(), error) {
	ahead := s.pending.Add(1) - 1
	if err := out.send(&uiv1.RunResponse{Event: &uiv1.RunResponse_Queued{Queued: &uiv1.Queued{Ahead: ahead}}}); err != nil {
		s.pending.Add(-1)
		return nil, err
	}
	select {
	case s.slot <- struct{}{}:
		return func() {
			<-s.slot
			s.pending.Add(-1)
		}, nil
	case <-ctx.Done():
		s.pending.Add(-1)
		return nil, fmt.Errorf("web: %w", ctx.Err())
	}
}

// execute runs bp under the configured timeout, streaming each step event.
func (s *Server) execute(ctx context.Context, bp domain.Blueprint, out *runStream) (*domain.State, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.RunTimeout)
	defer cancel()
	runner := s.deps.Runner.WithProgress(func(e domain.StepEvent) {
		_ = out.send(stepEvent(e)) // a closed stream cancels ctx, which ends the run
	})
	state, err := runner.Run(ctx, bp)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return state, nil
}

func stepEvent(e domain.StepEvent) *uiv1.RunResponse {
	step := &uiv1.Step{StepId: e.StepID, Tool: e.Tool, Status: string(e.Status)}
	if e.Err != nil {
		step.Error = e.Err.Error()
	}
	return &uiv1.RunResponse{Event: &uiv1.RunResponse_Step{Step: step}}
}

// runStream serialises sends on one run's stream, which several goroutines
// may write to.
type runStream struct {
	mu     sync.Mutex
	stream *connect.ServerStream[uiv1.RunResponse]
}

func (r *runStream) send(msg *uiv1.RunResponse) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.stream.Send(msg); err != nil {
		return fmt.Errorf("web: %w", err)
	}
	return nil
}

// stateCache holds each screen's last state as JSON, keyed by cacheKey.
type stateCache struct {
	mu     sync.Mutex
	states map[string][]byte
}

func (c *stateCache) put(key string, stateJSON []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.states == nil {
		c.states = make(map[string][]byte)
	}
	c.states[key] = stateJSON
}

// get returns the state cached under key, decoded from JSON.
func (c *stateCache) get(key string) (any, bool) {
	c.mu.Lock()
	raw, ok := c.states[key]
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	var state any
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, false
	}
	return state, true
}

// cacheKey is view|screen|params, with params JSON-encoded in sorted key
// order.
func cacheKey(msg *uiv1.RunRequest) string {
	params := []byte("{}")
	if len(msg.Params) > 0 {
		params, _ = json.Marshal(msg.Params) // a string map always encodes
	}
	return msg.View + "|" + msg.Screen + "|" + string(params)
}
