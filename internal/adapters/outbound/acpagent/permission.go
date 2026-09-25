package acpagent

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// onRequest answers what the agent asks of the client. Only permission is
// served: Atlas advertises no file system or terminal.
func (c *Client) onRequest(method string, params json.RawMessage) (any, *rpcError) {
	if method != "session/request_permission" {
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
	var p permissionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: err.Error()}
	}

	c.mu.Lock()
	t := c.turn
	if t == nil || p.SessionID != t.sessionID {
		c.mu.Unlock()
		return cancelled(), nil
	}
	call := merge(t.calls[p.ToolCall.ToolCallID], p.ToolCall)
	c.mu.Unlock()

	ctx, cancel := c.turnContext(t)
	defer cancel()
	d, err := c.perm.Decide(ctx, c.permissionRequest(call))
	if ctx.Err() != nil {
		return cancelled(), nil
	}
	if err != nil {
		d = ports.PermissionDeny
	}
	return choose(p.Options, d), nil
}

// turnContext is cancelled when t ends or the client closes.
func (c *Client) turnContext(t *turn) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	// Owned by the request handler that called turnContext, whose deferred
	// cancel stops it at the latest.
	go func() {
		select {
		case <-t.ended:
		case <-c.closing:
		case <-ctx.Done():
		}
		cancel()
	}()
	return ctx, cancel
}

func (c *Client) permissionRequest(call sessionUpdate) ports.PermissionRequest {
	name := call.Name
	if name == "" {
		name = call.Title
	}
	summary := call.Title
	var args bytes.Buffer
	if len(call.RawInput) > 0 && json.Compact(&args, call.RawInput) == nil {
		summary += " " + args.String()
	}
	return ports.PermissionRequest{ToolName: name, Kind: call.Kind, Summary: app.BoundSummary(summary, c.cfg.SummaryBytes)}
}

// choose picks the option that carries d, preferring once over always. With
// no such option the request is answered cancelled.
func choose(options []permissionOption, d ports.PermissionDecision) permissionResult {
	want := []string{"reject_once", "reject_always"}
	if d == ports.PermissionAllow {
		want = []string{"allow_once", "allow_always"}
	}
	for _, kind := range want {
		for _, o := range options {
			if o.Kind == kind {
				return permissionResult{Outcome: permissionOutcome{Outcome: "selected", OptionID: o.OptionID}}
			}
		}
	}
	return cancelled()
}

func cancelled() permissionResult {
	return permissionResult{Outcome: permissionOutcome{Outcome: "cancelled"}}
}
