package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/tunedev/atlas/internal/adapters/inbound/web"
	"github.com/tunedev/atlas/internal/adapters/inbound/web/uiv1"
	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/domain"
	"github.com/tunedev/atlas/internal/core/ports"
)

// shelfView declares a screen over echo, a screen over slow, an action on
// the echo screen that binds the screen's cached state, and a screen over
// ask.
const shelfView = `
title: Shelf
screens:
  - id: home
    params: [label]
    run: echo.yaml
    vars:
      label: param.label
      root: server.store_root
  - id: wait
    run: slow.yaml
    actions:
      - label: Label
        run: echo.yaml
        input:
          - name: mood
            options: [calm, rough]
        vars:
          label: input.mood
          root: server.store_root
  - id: shelved
    run: echo.yaml
    vars:
      label: const.first
      root: server.store_root
    actions:
      - label: Relabel
        run: echo.yaml
        vars:
          label: state.rows.label
          root: server.store_root
  - id: guarded
    run: ask.yaml
`

// fakeRegistry holds an echo tool that returns its config, a slow tool
// that blocks until release closes or its context ends, and an ask tool
// that asks asker for permission. It records every invocation.
type fakeRegistry struct {
	release   chan struct{}
	asker     *web.Asker
	decisions chan ports.PermissionDecision
	mu        sync.Mutex
	calls     []string
}

func (r *fakeRegistry) Lookup(name string) (ports.Tool, bool) {
	switch name {
	case "echo", "slow":
		return fakeTool{name: name, reg: r}, true
	case "ask":
		return askTool{reg: r}, true
	}
	return nil, false
}

func (r *fakeRegistry) invoked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type fakeTool struct {
	name string
	reg  *fakeRegistry
}

func (t fakeTool) Name() string { return t.name }

func (t fakeTool) Invoke(ctx context.Context, with map[string]string) (any, error) {
	t.reg.mu.Lock()
	t.reg.calls = append(t.reg.calls, t.name)
	t.reg.mu.Unlock()
	if t.name == "slow" {
		select {
		case <-t.reg.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return with, nil
}

// runLoader returns fixed blueprints: echo.yaml runs echo over label and
// root, slow.yaml runs slow, ask.yaml runs ask.
func runLoader(path string) (domain.Blueprint, error) {
	switch filepath.Base(path) {
	case "echo.yaml":
		return domain.Blueprint{
			Name: "echo",
			Vars: map[string]string{"label": "", "root": ""},
			Steps: []domain.Step{{ID: "rows", Tool: "echo", With: map[string]string{
				"label": "{{.vars.label}}",
				"root":  "{{.vars.root}}",
			}}},
		}, nil
	case "slow.yaml":
		return domain.Blueprint{Name: "slow", Steps: []domain.Step{{ID: "wait", Tool: "slow"}}}, nil
	case "ask.yaml":
		return domain.Blueprint{Name: "ask", Steps: []domain.Step{{ID: "asked", Tool: "ask"}}}, nil
	}
	return domain.Blueprint{}, fmt.Errorf("no pack at %s", path)
}

// newRunClient serves the shelf view over a fresh fake registry and returns
// an authenticated client, its Origin and the registry.
func newRunClient(t *testing.T, cfg web.Config) (uiv1.UIServiceClient, string, *fakeRegistry) {
	t.Helper()
	return serveShelf(t, cfg, nil)
}

// serveShelf is newRunClient with asker as the server's and the ask tool's
// asker.
func serveShelf(t *testing.T, cfg web.Config, asker *web.Asker) (uiv1.UIServiceClient, string, *fakeRegistry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shelf.ui.yaml")
	if err := os.WriteFile(path, []byte(shelfView), 0o600); err != nil {
		t.Fatal(err)
	}
	views, err := web.LoadViews([]string{path}, runLoader)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}

	reg := &fakeRegistry{release: make(chan struct{}), asker: asker, decisions: make(chan ports.PermissionDecision, 1)}
	t.Cleanup(func() { close(reg.release) })
	deps := web.Deps{
		Views:  views,
		Load:   runLoader,
		Runner: app.NewRunner(reg),
		Asker:  asker,
		Server: map[string]string{"store_root": "/store"},
	}
	ts, host, token := newTestServer(t, deps, cfg)
	client, origin := authedClient(t, ts.URL, host, token)
	return uiv1.NewUIServiceClient(client, ts.URL), origin, reg
}

// collect runs req to completion and returns every event and the stream's
// final error.
func collect(t *testing.T, c uiv1.UIServiceClient, origin string, req *uiv1.RunRequest) ([]*uiv1.RunResponse, error) {
	t.Helper()
	stream, err := c.Run(context.Background(), withOrigin(req, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var events []*uiv1.RunResponse
	for stream.Receive() {
		events = append(events, stream.Msg())
	}
	return events, stream.Err()
}

func runConfig() web.Config { return web.Config{RunTimeout: 5 * time.Second} }

func TestRunStreamsStepsThenDone(t *testing.T) {
	c, origin, _ := newRunClient(t, runConfig())

	events, err := collect(t, c, origin, &uiv1.RunRequest{
		View: "shelf", Screen: "home", Action: -1, Params: map[string]string{"label": "oak"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4: %v", len(events), events)
	}
	if q := events[0].GetQueued(); q == nil || q.Ahead != 0 {
		t.Errorf("events[0] = %v, want Queued{0}", events[0])
	}
	if s := events[1].GetStep(); s == nil || s.StepId != "rows" || s.Tool != "echo" || s.Status != "started" {
		t.Errorf("events[1] = %v, want Step rows started", events[1])
	}
	if s := events[2].GetStep(); s == nil || s.Status != "done" {
		t.Errorf("events[2] = %v, want Step done", events[2])
	}
	done := events[3].GetDone()
	if done == nil {
		t.Fatalf("events[3] = %v, want Done", events[3])
	}
	var state map[string]map[string]string
	if err := json.Unmarshal([]byte(done.StateJson), &state); err != nil {
		t.Fatalf("state_json: %v", err)
	}
	if got := state["rows"]; got["label"] != "oak" || got["root"] != "/store" {
		t.Errorf("state rows = %v, want label oak and root /store", got)
	}
}

func TestRunRefusesUndeclaredPackOrVar(t *testing.T) {
	cases := []struct {
		name string
		req  *uiv1.RunRequest
		code connect.Code
	}{
		{"an undeclared param", &uiv1.RunRequest{View: "shelf", Screen: "home", Action: -1,
			Params: map[string]string{"label": "oak", "path": "/etc"}}, connect.CodeInvalidArgument},
		{"an input to a screen", &uiv1.RunRequest{View: "shelf", Screen: "home", Action: -1,
			Params: map[string]string{"label": "oak"}, Inputs: map[string]string{"mood": "calm"}}, connect.CodeInvalidArgument},
		{"an undeclared input", &uiv1.RunRequest{View: "shelf", Screen: "wait", Action: 0,
			Inputs: map[string]string{"mood": "calm", "path": "/etc"}}, connect.CodeInvalidArgument},
		{"an off-list option", &uiv1.RunRequest{View: "shelf", Screen: "wait", Action: 0,
			Inputs: map[string]string{"mood": "giddy"}}, connect.CodeInvalidArgument},
		{"an unknown view", &uiv1.RunRequest{View: "attic", Screen: "home", Action: -1}, connect.CodeNotFound},
		{"an unknown screen", &uiv1.RunRequest{View: "shelf", Screen: "cellar", Action: -1}, connect.CodeNotFound},
		{"an unknown action", &uiv1.RunRequest{View: "shelf", Screen: "wait", Action: 3}, connect.CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, origin, reg := newRunClient(t, runConfig())
			events, err := collect(t, c, origin, tc.req)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("err = %v, want %v", err, tc.code)
			}
			if len(events) != 0 {
				t.Errorf("events = %v, want none", events)
			}
			if calls := reg.invoked(); len(calls) != 0 {
				t.Errorf("tools ran: %v", calls)
			}
		})
	}
}

func TestActionReadsStateFromTheServerCache(t *testing.T) {
	c, origin, reg := newRunClient(t, runConfig())
	relabel := &uiv1.RunRequest{View: "shelf", Screen: "shelved", Action: 0}

	_, err := collect(t, c, origin, relabel)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "reload the screen") {
		t.Fatalf("before the screen ran: err = %v, want FailedPrecondition asking to reload", err)
	}
	if calls := reg.invoked(); len(calls) != 0 {
		t.Fatalf("tools ran: %v", calls)
	}

	if _, err := collect(t, c, origin, &uiv1.RunRequest{View: "shelf", Screen: "shelved", Action: -1}); err != nil {
		t.Fatalf("screen run: %v", err)
	}

	events, err := collect(t, c, origin, relabel)
	if err != nil {
		t.Fatalf("after the screen ran: %v", err)
	}
	done := events[len(events)-1].GetDone()
	if done == nil || !strings.Contains(done.StateJson, `"label":"first"`) {
		t.Errorf("last event = %v, want Done carrying the cached label", events[len(events)-1])
	}
}

func TestRunsAreSerialised(t *testing.T) {
	c, origin, reg := newRunClient(t, runConfig())
	ctx := context.Background()
	wait := &uiv1.RunRequest{View: "shelf", Screen: "wait", Action: -1}

	first, err := c.Run(ctx, withOrigin(wait, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	for first.Receive() {
		if s := first.Msg().GetStep(); s != nil && s.Status == "started" {
			break
		}
	}

	second, err := c.Run(ctx, withOrigin(wait, origin))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !second.Receive() {
		t.Fatalf("second run ended: %v", second.Err())
	}
	if q := second.Msg().GetQueued(); q == nil || q.Ahead != 1 {
		t.Fatalf("second run's first event = %v, want Queued{ahead: 1}", second.Msg())
	}

	next := make(chan *uiv1.RunResponse, 1)
	go func() {
		if second.Receive() {
			next <- second.Msg()
		}
		close(next)
	}()
	select {
	case msg := <-next:
		t.Fatalf("second run sent %v while the first held the slot", msg)
	case <-time.After(100 * time.Millisecond):
	}
	if calls := reg.invoked(); len(calls) != 1 {
		t.Fatalf("tools ran %v while the first run held the slot, want one", calls)
	}

	reg.release <- struct{}{}
	msg := <-next
	if s := msg.GetStep(); s == nil || s.Status != "started" {
		t.Errorf("second run's next event = %v, want Step started", msg)
	}
}

func TestRunTimeoutEndsARun(t *testing.T) {
	c, origin, _ := newRunClient(t, web.Config{RunTimeout: 50 * time.Millisecond})

	_, err := collect(t, c, origin, &uiv1.RunRequest{View: "shelf", Screen: "wait", Action: -1})
	if connect.CodeOf(err) != connect.CodeUnknown {
		t.Fatalf("err = %v, want CodeUnknown", err)
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("err = %v, want it to carry the deadline", err)
	}
}
