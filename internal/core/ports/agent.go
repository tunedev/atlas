package ports

import "context"

// AgentTask is one unit of delegated work: a prompt and the absolute
// directory the agent works in.
type AgentTask struct {
	Prompt  string
	WorkDir string
}

// AgentEventKind names what a caller can act on in an AgentEvent.
type AgentEventKind string

const (
	AgentEventMessage  AgentEventKind = "message"
	AgentEventToolCall AgentEventKind = "tool_call"
	AgentEventPlan     AgentEventKind = "plan"
)

// AgentEvent is one step of progress an agent reported during a turn.
type AgentEvent struct {
	Kind AgentEventKind
	Text string
}

// AgentResult is what a turn produced and why the agent stopped. Unverified
// names tool calls a resumed session last reported as pending or in
// progress: the conversation does not say whether their effects happened.
type AgentResult struct {
	Text       string
	StopReason string
	Unverified []string
}

// Agent drives a coding agent through one turn of work. onEvent is called
// synchronously, in order, on the caller's goroutine, for every update the
// agent reports during the turn.
//
// A non-empty sessionID resumes a session this Agent produced before; empty
// starts a new one. The returned session id is always set once a session
// exists, so a caller can resume it later.
type Agent interface {
	Do(ctx context.Context, task AgentTask, sessionID string, onEvent func(AgentEvent)) (AgentResult, string, error)
}

// PermissionRequest is one decision a tool call needs before it runs.
// ToolName is the name the call is known by, Kind is what sort of action it
// is, and Summary is a bounded rendering of what it would do.
type PermissionRequest struct {
	ToolName string
	Kind     string
	Summary  string
}

// PermissionDecision is the outcome of a permission check.
type PermissionDecision string

const (
	PermissionAllow PermissionDecision = "allow"
	PermissionAsk   PermissionDecision = "ask"
	PermissionDeny  PermissionDecision = "deny"
)

// Permission decides whether one tool call may run. Callers treat anything
// but PermissionAllow as a refusal.
type Permission interface {
	Decide(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
}
