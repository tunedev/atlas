package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tunedev/atlas/internal/core/ports"
)

// turn is the one Do in progress. The read loop hands it updates for its
// session through updates; ended closes when Do returns, which releases a
// read loop blocked on a send nobody will receive.
type turn struct {
	sessionID string
	updates   chan sessionUpdate
	ended     chan struct{}
	calls     map[string]sessionUpdate // guarded by Client.mu
}

func (c *Client) Do(ctx context.Context, task ports.AgentTask, sessionID string, onEvent func(ports.AgentEvent)) (ports.AgentResult, string, error) {
	if !filepath.IsAbs(task.WorkDir) {
		return ports.AgentResult{}, "", fmt.Errorf("acpagent: work dir %q is not absolute", task.WorkDir)
	}
	if sessionID != "" && !c.caps.LoadSession {
		return ports.AgentResult{}, "", errors.New("acpagent: cannot resume a session: the agent does not offer loadSession")
	}

	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	t := c.begin(sessionID)
	defer c.end(t)

	unverified, err := c.open(ctx, t, task.WorkDir)
	if err != nil {
		return ports.AgentResult{}, c.sessionOf(t), err
	}

	var text strings.Builder
	var res promptResult
	err = c.await(ctx, t, "session/prompt", promptParams{
		SessionID: t.sessionID,
		Prompt:    []contentBlock{{Type: "text", Text: task.Prompt}},
	}, &res, func(u sessionUpdate) {
		if ev, ok := toEvent(u); ok {
			onEvent(ev)
		}
		text.WriteString(messageText(u))
	})
	if err != nil {
		return ports.AgentResult{}, t.sessionID, err
	}
	return ports.AgentResult{Text: text.String(), StopReason: res.StopReason, Unverified: unverified}, t.sessionID, nil
}

// open starts a new session. Task 6 extends it to load an existing one.
func (c *Client) open(ctx context.Context, t *turn, workDir string) ([]string, error) {
	var res newSessionResult
	if err := c.await(ctx, t, "session/new", newSessionParams{CWD: workDir, MCPServers: c.mcpServers()}, &res, nil); err != nil {
		return nil, err
	}
	c.mu.Lock()
	t.sessionID = res.SessionID
	c.mu.Unlock()
	return nil, nil
}

func (c *Client) sessionOf(t *turn) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return t.sessionID
}

func (c *Client) begin(sessionID string) *turn {
	t := &turn{sessionID: sessionID, updates: make(chan sessionUpdate), ended: make(chan struct{}), calls: map[string]sessionUpdate{}}
	c.mu.Lock()
	c.turn = t
	c.mu.Unlock()
	return t
}

func (c *Client) end(t *turn) {
	c.mu.Lock()
	c.turn = nil
	c.mu.Unlock()
	close(t.ended)
}

// await makes one call and feeds each update for t to handle, on the
// caller's goroutine, until the call returns. updates is unbuffered and the
// read loop is sequential, so every update sent before the response has been
// handled by the time the response is seen. A cancelled ctx sends
// session/cancel.
func (c *Client) await(ctx context.Context, t *turn, method string, params, result any, handle func(sessionUpdate)) error {
	errc := make(chan error, 1)
	// Owned by await, which receives from errc before returning. It stops
	// when the call returns, which ctx or the end of the conversation bounds.
	go func() { errc <- c.conn.call(ctx, method, params, result) }()
	for {
		select {
		case u := <-t.updates:
			if handle != nil {
				handle(u)
			}
		case err := <-errc:
			if err != nil && ctx.Err() != nil && t.sessionID != "" {
				_ = c.conn.send("session/cancel", cancelParams{SessionID: t.sessionID})
			}
			return err
		}
	}
}

// onNotify runs on the read loop. It records tool call state for the active
// turn, then hands the update over. An update for no turn, or for another
// session, is dropped.
func (c *Client) onNotify(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var n sessionNotification
	if err := json.Unmarshal(params, &n); err != nil {
		return
	}
	c.mu.Lock()
	t := c.turn
	if t == nil || n.SessionID != t.sessionID {
		c.mu.Unlock()
		return
	}
	u := n.Update
	if u.ToolCallID != "" {
		u = merge(t.calls[u.ToolCallID], u)
		t.calls[u.ToolCallID] = u
	}
	c.mu.Unlock()

	select {
	case t.updates <- u:
	case <-t.ended:
	}
}

// merge lays the fields u carries over prev: a tool_call_update carries only
// what changed.
func merge(prev, u sessionUpdate) sessionUpdate {
	prev.SessionUpdate = u.SessionUpdate
	prev.ToolCallID = u.ToolCallID
	if u.Content != nil {
		prev.Content = u.Content
	}
	if u.Name != "" {
		prev.Name = u.Name
	}
	if u.Title != "" {
		prev.Title = u.Title
	}
	if u.Kind != "" {
		prev.Kind = u.Kind
	}
	if u.Status != "" {
		prev.Status = u.Status
	}
	if u.RawInput != nil {
		prev.RawInput = u.RawInput
	}
	return prev
}

// toEvent translates the updates a caller can act on. Anything else, such
// as thoughts or command lists, is not an event.
func toEvent(u sessionUpdate) (ports.AgentEvent, bool) {
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if text := messageText(u); text != "" {
			return ports.AgentEvent{Kind: ports.AgentEventMessage, Text: text}, true
		}
	case "tool_call", "tool_call_update":
		status := u.Status
		if status == "" {
			status = "pending"
		}
		return ports.AgentEvent{Kind: ports.AgentEventToolCall, Text: fmt.Sprintf("%s [%s]", u.Title, status)}, true
	case "plan":
		lines := make([]string, len(u.Entries))
		for i, e := range u.Entries {
			lines[i] = fmt.Sprintf("[%s] %s", e.Status, e.Content)
		}
		return ports.AgentEvent{Kind: ports.AgentEventPlan, Text: strings.Join(lines, "\n")}, true
	}
	return ports.AgentEvent{}, false
}

// messageText is the text of an agent message chunk, or "" for anything else.
func messageText(u sessionUpdate) string {
	if u.SessionUpdate != "agent_message_chunk" {
		return ""
	}
	var b contentBlock
	if err := json.Unmarshal(u.Content, &b); err != nil || b.Type != "text" {
		return ""
	}
	return b.Text
}
