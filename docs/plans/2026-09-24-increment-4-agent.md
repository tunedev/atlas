# Increment 4 — The `Agent` port, over ACP — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a pack step delegates a turn of work to a real coding agent over ACP. The agent can
reach a chosen set of Atlas's own tools, every permission it asks for goes through Atlas's
allow/ask/deny engine, and the session can be resumed later from a pointer kept in the record.

**Architecture:** `ports.Agent` and `ports.Permission` are owned by the core. One outbound
adapter, `acpagent`, is a hand-written ACP client (JSON-RPC 2.0, newline-delimited, over a
subprocess's stdio) whose command is config. The agent reaches Atlas's tools through one inbound
adapter, `mcpserve`: a loopback MCP server over the same registry instance the pack runner uses,
advertised to the agent in `session/new`. A config-rule `PermissionPolicy` in `internal/core/app`
decides what it can by rule and passes `ask` to a terminal prompt adapter. An `agent.do` tool
puts all of this behind a pack step and records a session pointer through `ports.Docs`.

**Tech Stack:** Go 1.27, the standard library, `github.com/modelcontextprotocol/go-sdk` v1.8.0
(new, MCP server side only), `@agentclientprotocol/claude-agent-acp` for the live run (a human
installs it).

**Spec:** `docs/specs/2026-09-24-epic-4-agent.md`. **This plan's Rulings section overrides the
spec where they disagree.** The spec's tool bridge in particular does not work as written (R1).

**Harness spec:** `docs/specs/2026-09-17-job-hunt-harness-design.md`

**Roadmap:** `docs/plans/2026-09-17-roadmap.md` (Epic 4, stories 4.1-4.7)

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a job posting is.** `internal/arch/vocabulary_test.go`
  enforces it, test fixtures included. Prompts in tests talk about notes, books and weather.
- **No core package imports an adapter or a driver.** `internal/core/...` compiles in neither
  `net/http` nor `crypto/tls`, nor anything under `github.com/modelcontextprotocol`.
  `internal/arch/arch_test.go` enforces it.
- **No ACP or MCP concept crosses into the core.** Ports speak `AgentTask`, `AgentEvent`,
  `PermissionRequest`. Wire types stay in their adapter package.
- `ctx context.Context` is the first parameter of every blocking or remote call. **Never stored
  in a struct**: a long-lived lifetime is a `chan struct{}` closed on shutdown.
- Every goroutine has one owner and one stop condition, written in a comment beside the `go`
  statement.
- Every remote call has a timeout. Every inbound payload is bounded: an ACP line by
  `Agent.MaxMessageBytes`, a tool result handed to the agent by `Agent.MaxToolResultBytes`,
  and a permission summary by `Permission.SummaryBytes`.
- Every wrapped error carries its component prefix: `acpagent: `, `mcpserve: `,
  `termprompt: `, `permission: `, `agent session: `, `agent.do: `, `config: `.
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis. Comments describe current behaviour only: no history, no dates, no narrative.
- Tests assert behaviour. A test that cannot fail is a defect. Prove a guard by mutation, and
  **confirm the mutation applied** before reading anything into the result.
- The `cmd/atlas` guards hold: no `defer` in `main()`, `os.Exit` only in `main()`, no literal in
  `buildRegistry` **or `startAgent`**. `cmd/atlas/main_test.go` enforces them.
- Every test runs with no network and no real agent. The live run in Task 13 is the only one
  that touches `claude-agent-acp`.
- **Environment rule:** no agent session runs `npm install`. If `claude-agent-acp` is not on
  `PATH` at Task 13, stop and hand the human the install command.

## What "done" means

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
```

Then the increment's claim, with pasted output from Task 13:

> A pack step drove Claude Code over ACP. The agent called an Atlas tool through the registry,
> its permission requests were decided by Atlas's engine and prompted on the terminal, and a
> second step resumed the same session from the recorded pointer.

## Measured facts this plan depends on

Each was checked against a primary source before writing this plan. Check again if anything
surprises you.

- **The agent executes its own tool calls.** ACP docs, "Tool Calls": *"it generates tool calls
  that the Agent executes on its behalf."* `session/update` `tool_call` and `tool_call_update`
  are agent-to-client **reports**. No message lets a client return a tool result.
- **A client offers its own tools through MCP servers passed in `session/new`.** ACP v2's RFD
  drops the client `fs/*` and `terminal/*` methods and says clients wanting to expose
  capabilities should *"provide an MCP server to the session."*
- **`claude-agent-acp`'s `initialize` reply**, read from `src/acp-agent.ts` at
  `github.com/agentclientprotocol/claude-agent-acp` HEAD: `protocolVersion: 1`,
  `promptCapabilities: {image: true, embeddedContext: true}`,
  `mcpCapabilities: {http: true, sse: true}`, `loadSession: true`.
- **It emits the `tool_call` before any `session/request_permission` that refers to it**
  (`ensureToolCallEmitted`), so a request that carries only a `toolCallId` can be resolved
  against the earlier notification.
- **Permission response shape:** `{"outcome": {"outcome": "selected", "optionId": "..."}}` or
  `{"outcome": {"outcome": "cancelled"}}`.
- **`session/new` in ACP v1 requires `cwd` (absolute) and `mcpServers` (an array, may be
  empty).** An HTTP MCP server is `{"type": "http", "name", "url", "headers": [{"name", "value"}]}`.
- **MCP versions have diverged.** The 2026-07-28 revision replaces the session-based Streamable
  HTTP transport of 2025-03-26 through 2025-11-25. We cannot see which revision Claude's MCP
  client speaks, so the server side uses the official Go SDK, which negotiates across them
  (R2).
- `go list -m -versions github.com/modelcontextprotocol/go-sdk` ends at `v1.8.0`.

## Rulings

The spec left these open or got them wrong. Each is decided here, and the implementation
follows the ruling.

| # | Question | Ruling |
|---|---|---|
| R1 | How does an agent reach `ports.Tool` (4.4)? The spec maps `tool_call` to the registry and returns results in `tool_call_update`. ACP has no such path (see Measured facts). | **Human ruling, 2026-09-24:** an in-process MCP server (`internal/adapters/inbound/mcpserve`) serves a configured subset of the **same registry instance**, advertised in `session/new`. This reverses the spec's MCP exclusion. The spec's reason for that exclusion, "one path in, not two", still holds, because MCP is now the only path. The spec's `ToolNames` table becomes `Agent.Tools`, a list of registry names to offer. |
| R2 | Hand-write MCP too, like ACP? | No. Use `github.com/modelcontextprotocol/go-sdk` v1.8.0, because MCP's transport changed between revisions and the SDK negotiates across them. ACP stays hand-written because it has no Go SDK. Both sit behind ports, so either choice can be reversed. |
| R3 | Registry names contain `.`; model APIs restrict tool names to `[A-Za-z0-9_-]`. | Publish `http.request` as `http_request` (dot to underscore). Two registry names that collide once published are a startup error. |
| R4 | Who may call the MCP server? | Only the agent. It listens on loopback (`Agent.MCPAddr`, default `127.0.0.1:0`) and requires a per-run bearer token from `crypto/rand.Text()`, sent to the agent in the `session/new` headers. |
| R5 | "Ask `ports.Permission` before invoking any tool, regardless of whether the agent asked" | Atlas can gate only what Atlas executes. **Atlas tools** are gated in the MCP `tools/call` handler, which is authoritative, cannot be bypassed, and uses `Kind: "atlas"`. **The agent's own tools** (file edits, shell) are gated only when the agent sends `session/request_permission`. Built-in calls the agent's own settings allow without asking never reach Atlas. When an Atlas tool's rule is `ask` and the agent also asks, the human is prompted twice. Give Atlas tools an explicit allow or deny rule to avoid that. |
| R6 | `PermissionConfig` matches on kind, but `PermissionRequest` has no kind | `PermissionRequest` gains `Kind string`. |
| R7 | What does `Ask` mean coming out of `Decide`? | `app.PermissionPolicy` returns only allow or deny: a rule that says ask is resolved by its human `Permission`. A human answer of ask, or a human error, becomes deny. Every adapter treats anything but allow as deny (fail closed). |
| R8 | Rule evaluation order | First match wins, `*` matches any value, and no match means ask, as the spec says. Epic 10.2's "deny beats allow" changes evaluation when that epic lands, not before. |
| R9 | When does the subprocess start, and where? The spec says both "launched with `Dir` = `task.WorkDir`" and "outlives one `Do`". | The agent is **enabled iff `Agent.Command` is set**. It is launched and initialized at startup in `cmd/atlas`, and fails loudly there. Without a command, `agent.do` is not registered. The process inherits Atlas's cwd. `task.WorkDir` becomes the session's `cwd` and must be absolute. An empty `workdir` falls back to `Agent.WorkDir`, which defaults to Atlas's working directory. |
| R10 | Which capabilities are required at startup? | `protocolVersion` must equal 1. `mcpCapabilities.http` is required if `Agent.Tools` is non-empty. `promptCapabilities` requires nothing, because Atlas sends only text and every agent must accept text. `loadSession` is checked in `Do` when a session id is passed, which fails with a named error and sends no RPC. |
| R11 | "`ctx` is the adapter's own long-lived context" breaks the rule "never store a Context" | Launch with `exec.Command`, not `CommandContext`. The adapter's lifetime is a `closing chan struct{}`, and `Close` kills explicitly. |
| R12 | The spec claims "one goroutine" | That is not achievable: permission prompts block, so they cannot run on the read loop. Goroutines: the read loop (stops at stdout EOF), one per agent-to-client request (tracked by a `WaitGroup`, cancelled when its turn ends or the client closes), one waiter per in-flight call inside `Do` (joined before `Do` returns), `os/exec`'s stderr copier (joined by `Wait`), and the terminal prompt's stdin reader (stops at stdin EOF). |
| R13 | What happens on `Do` timeout? | Send the `session/cancel` notification, return `ctx.Err()`, and keep the process. A permission request still pending is answered `cancelled`. A late response is dropped. |
| R14 | A malformed or over-size stdout line | Ends the conversation loudly: every waiting call fails with an error naming the cause, and `Close` then kills the process. |
| R15 | `fs/*`, `terminal/*`, `elicitation/create` callbacks | Atlas advertises `fs.readTextFile: false`, `fs.writeTextFile: false`, `terminal: false`. Any agent-to-client request other than `session/request_permission` gets JSON-RPC `-32601`. |
| R16 | The resume replay, and "treat in-progress tool calls as unverified" | Replayed updates are consumed but **not** passed to `onEvent`. `AgentResult` gains `Unverified []string`: the titles of replayed tool calls whose last status was pending or in progress, so a pack can check them. |
| R17 | The session pointer's command, args and protocol version are adapter facts the core cannot see | The `agent.do` tool is built at the composition root with `Command`, `Args` and `client.ProtocolVersion()` as plain values, and passes them into `app.RecordAgentSession`. Resuming is opt-in (`resume: "true"`) and refuses a pointer recorded by a different command or args. |
| R18 | The subprocess environment | Atlas's environment minus every `ATLAS_*` variable, so `ATLAS_MODEL_API_KEY` never reaches the agent (`config.AgentEnv`). |
| R19 | Concurrent `Do` calls | Serialized per client. Pack steps are sequential anyway. |
| R20 | Can the agent call `agent.do` through MCP? | No. The MCP server is built over the base registry before `agent.do` is added, so the recursion is impossible by construction. |
| R21 | A JSON `null` tool argument | Rejected, like a nested value: it has no string form a tool can tell apart from absent. |
| R22 | ACP `authenticate` | Not implemented. The agent uses the login its own CLI already holds. An auth error from `session/new` surfaces with its message. |
| R23 | Story 4.7 names Gemini CLI | Amended in the roadmap to "the configured agent (Claude Code via `claude-agent-acp`)". |
| R24 | "stderr copied to Atlas's logger": Atlas has no logger | The agent's stderr is copied line by line to Atlas's stderr, each line prefixed with the command's base name. |
| R25 | What does the terminal show during a turn? | `tool_call` and `plan` events print one line each to stderr. Message chunks do not print, because they arrive in fragments. The full text is in the step's result. |

## Review Focus

These are the failure modes most likely to hit someone using this, which the spec implies but no
story names. Each one is pinned by a test in the task that owns it.

1. **A long resumed session replays one line bigger than the buffer.** Expected: a named error,
   not a hang or a silently truncated message. Task 3, `TestOverSizeLineFailsWaitingCalls`.
2. **The turn times out while a permission prompt is on screen.** Expected: the prompt stops
   waiting, the agent gets `cancelled`, and no goroutine is left blocked. Task 7,
   `TestPendingPermissionIsCancelledWhenTheTurnEnds`.
3. **The agent sends updates for another session, or after `Do` returned.** Expected: dropped,
   and the read loop never blocks on them. Task 5, `TestDoStreamsATurnInOrder` (foreign session)
   and `TestDoTimeoutCancelsTheTurnAndKeepsTheAgent` (late reply).
4. **The agent calls `http_request` and the registry has `http.request`.** Expected: the name
   round-trips. Task 8, `TestAgentCallsARegistryToolByItsPublishedName`.
5. **Two permission requests arrive at once.** Expected: prompts are shown one at a time, and
   each answer goes to the request it was shown for. Task 2,
   `TestConcurrentPromptsDoNotInterleave`.

## File Structure

| File | Responsibility |
|---|---|
| `internal/core/ports/agent.go` | `Agent`, `AgentTask`, `AgentEvent`, `AgentResult`, `Permission`, `PermissionRequest` |
| `internal/core/app/permission.go` | `PermissionPolicy` (rules, then human) and `BoundSummary` |
| `internal/core/app/agentsession.go` | Record and load the session pointer through `Docs` |
| `internal/adapters/outbound/termprompt/prompt.go` | Interactive `Permission` on stdin and stderr |
| `internal/adapters/outbound/acpagent/rpc.go` | JSON-RPC connection: framing, ids, dispatch, failure |
| `internal/adapters/outbound/acpagent/wire.go` | ACP message shapes |
| `internal/adapters/outbound/acpagent/client.go` | Launch, `initialize`, `Close`, stderr prefixing |
| `internal/adapters/outbound/acpagent/turn.go` | `Do`: new or load, prompt, updates to events |
| `internal/adapters/outbound/acpagent/permission.go` | `session/request_permission` to `ports.Permission` |
| `internal/adapters/inbound/mcpserve/server.go` | MCP handler over the registry, token, listen |
| `internal/adapters/inbound/mcpserve/flatten.go` | JSON arguments to `map[string]string` |
| `internal/adapters/outbound/tools/agent.go` | The `agent.do` tool |
| `internal/adapters/outbound/tools/registry.go` | Gains `With` |
| `internal/config/config.go`, `layers.go` | `AgentConfig`, `PermissionConfig`, `AgentEnv` |
| `cmd/atlas/main.go` | `startAgent`, wiring |
| `internal/arch/arch_test.go` | Forbids the MCP SDK in the core |
| `packs/agent-hn.yaml` | The live pack |
| `docs/notes/2026-09-24-increment-4.md` | The increment note |

---

### Task 1: The ports and the permission policy

**Files:**
- Create: `internal/core/ports/agent.go`
- Create: `internal/core/app/permission.go`
- Test: `internal/core/app/permission_test.go`
- Modify: `internal/arch/arch_test.go` (forbidden prefix list)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `ports.AgentTask{Prompt, WorkDir string}`
  - `ports.AgentEventKind` with `AgentEventMessage = "message"`, `AgentEventToolCall = "tool_call"`, `AgentEventPlan = "plan"`
  - `ports.AgentEvent{Kind AgentEventKind; Text string}`
  - `ports.AgentResult{Text, StopReason string; Unverified []string}`
  - `ports.Agent` with `Do(ctx, task AgentTask, sessionID string, onEvent func(AgentEvent)) (AgentResult, string, error)`
  - `ports.PermissionRequest{ToolName, Kind, Summary string}`
  - `ports.PermissionDecision` with `PermissionAllow`, `PermissionAsk`, `PermissionDeny`
  - `ports.Permission` with `Decide(ctx, PermissionRequest) (PermissionDecision, error)`
  - `app.PermissionRule{ToolName, Kind string; Decision ports.PermissionDecision}`
  - `app.NewPermissionPolicy(rules []app.PermissionRule, human ports.Permission) *app.PermissionPolicy`
  - `app.BoundSummary(s string, budget int) string`

- [ ] **Step 1: Write the ports**

`internal/core/ports/agent.go`:

```go
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
```

- [ ] **Step 2: Write the failing policy tests**

`internal/core/app/permission_test.go`:

```go
package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

type scriptedHuman struct {
	answer ports.PermissionDecision
	err    error
	asked  []ports.PermissionRequest
}

func (h *scriptedHuman) Decide(_ context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	h.asked = append(h.asked, req)
	return h.answer, h.err
}

func TestPolicyFirstMatchingRuleWins(t *testing.T) {
	human := &scriptedHuman{answer: ports.PermissionAllow}
	p := app.NewPermissionPolicy([]app.PermissionRule{
		{ToolName: "*", Kind: "delete", Decision: ports.PermissionDeny},
		{ToolName: "notes.write", Kind: "*", Decision: ports.PermissionAllow},
		{ToolName: "*", Kind: "*", Decision: ports.PermissionDeny},
	}, human)

	cases := []struct {
		req  ports.PermissionRequest
		want ports.PermissionDecision
	}{
		{ports.PermissionRequest{ToolName: "notes.write", Kind: "delete"}, ports.PermissionDeny},
		{ports.PermissionRequest{ToolName: "notes.write", Kind: "edit"}, ports.PermissionAllow},
		{ports.PermissionRequest{ToolName: "weather.get", Kind: "fetch"}, ports.PermissionDeny},
	}
	for _, c := range cases {
		got, err := p.Decide(context.Background(), c.req)
		if err != nil || got != c.want {
			t.Errorf("Decide(%+v) = %q, %v; want %q", c.req, got, err, c.want)
		}
	}
	if len(human.asked) != 0 {
		t.Errorf("a rule decided every case, yet the human was asked %d times", len(human.asked))
	}
}

func TestPolicyAsksTheHumanWhenNoRuleMatches(t *testing.T) {
	human := &scriptedHuman{answer: ports.PermissionAllow}
	p := app.NewPermissionPolicy(nil, human)
	req := ports.PermissionRequest{ToolName: "weather.get", Kind: "fetch", Summary: "Oslo"}

	got, err := p.Decide(context.Background(), req)
	if err != nil || got != ports.PermissionAllow {
		t.Fatalf("Decide = %q, %v; want allow", got, err)
	}
	if len(human.asked) != 1 || human.asked[0] != req {
		t.Errorf("human asked %+v; want exactly %+v", human.asked, req)
	}
}

func TestPolicyAsksTheHumanForAnAskRule(t *testing.T) {
	human := &scriptedHuman{answer: ports.PermissionDeny}
	p := app.NewPermissionPolicy([]app.PermissionRule{{ToolName: "*", Kind: "execute", Decision: ports.PermissionAsk}}, human)

	got, _ := p.Decide(context.Background(), ports.PermissionRequest{ToolName: "shell", Kind: "execute"})
	if got != ports.PermissionDeny || len(human.asked) != 1 {
		t.Errorf("Decide = %q after %d asks; want deny after 1", got, len(human.asked))
	}
}

func TestPolicyFailsClosed(t *testing.T) {
	cases := map[string]*scriptedHuman{
		"human error":   {answer: ports.PermissionAllow, err: errors.New("terminal gone")},
		"human says ask": {answer: ports.PermissionAsk},
	}
	for name, human := range cases {
		p := app.NewPermissionPolicy(nil, human)
		got, err := p.Decide(context.Background(), ports.PermissionRequest{ToolName: "t", Kind: "k"})
		if got != ports.PermissionDeny {
			t.Errorf("%s: Decide = %q; want deny", name, got)
		}
		if (human.err != nil) != (err != nil) {
			t.Errorf("%s: err = %v; want the human's error surfaced, and only then", name, err)
		}
	}
}

func TestBoundSummary(t *testing.T) {
	cases := []struct {
		in     string
		budget int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"a longer summary", 8, "a longer..."},
		{"naïve", 3, "na..."}, // never splits the two-byte ï
	}
	for _, c := range cases {
		if got := app.BoundSummary(c.in, c.budget); got != c.want {
			t.Errorf("BoundSummary(%q, %d) = %q; want %q", c.in, c.budget, got, c.want)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/core/app/ -run 'Policy|BoundSummary' -v`
Expected: FAIL, `undefined: app.NewPermissionPolicy`.

- [ ] **Step 4: Implement the policy**

`internal/core/app/permission.go`:

```go
package app

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/tunedev/atlas/internal/core/ports"
)

// PermissionRule decides requests whose tool name and kind both match. "*"
// matches any value.
type PermissionRule struct {
	ToolName string
	Kind     string
	Decision ports.PermissionDecision
}

// PermissionPolicy decides by the first matching rule. A request no rule
// matches, or one whose rule says ask, is put to human. It returns only allow
// or deny: a human error, or a human answer of ask, is a deny.
type PermissionPolicy struct {
	rules []PermissionRule
	human ports.Permission
}

func NewPermissionPolicy(rules []PermissionRule, human ports.Permission) *PermissionPolicy {
	return &PermissionPolicy{rules: rules, human: human}
}

func (p *PermissionPolicy) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	if d := p.match(req); d != ports.PermissionAsk {
		return d, nil
	}
	d, err := p.human.Decide(ctx, req)
	if err != nil {
		return ports.PermissionDeny, fmt.Errorf("permission: %w", err)
	}
	if d != ports.PermissionAllow {
		return ports.PermissionDeny, nil
	}
	return ports.PermissionAllow, nil
}

// match returns the first matching rule's decision, or ask when none match.
func (p *PermissionPolicy) match(req ports.PermissionRequest) ports.PermissionDecision {
	for _, r := range p.rules {
		if matches(r.ToolName, req.ToolName) && matches(r.Kind, req.Kind) {
			return r.Decision
		}
	}
	return ports.PermissionAsk
}

func matches(pattern, value string) bool { return pattern == "*" || pattern == value }

// BoundSummary returns s cut to at most budget bytes on a rune boundary,
// marked with "..." when anything was cut.
func BoundSummary(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
```

- [ ] **Step 5: Forbid the MCP SDK in the core**

In `internal/arch/arch_test.go`, add `"github.com/modelcontextprotocol"` to `forbiddenPrefix`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/core/... ./internal/arch/ -race`
Expected: PASS.

Mutation check: change `matches` to `return pattern == value`, then confirm
`TestPolicyFirstMatchingRuleWins` fails. Revert.

- [ ] **Step 7: Commit**

```bash
git add internal/core/ports/agent.go internal/core/app/permission.go internal/core/app/permission_test.go internal/arch/arch_test.go
git commit -m "Name the Agent and Permission ports, and decide permission by rule then human"
```

---

### Task 2: The terminal prompt

**Files:**
- Create: `internal/adapters/outbound/termprompt/prompt.go`
- Test: `internal/adapters/outbound/termprompt/prompt_test.go`

**Interfaces:**
- Consumes: `ports.Permission`, `ports.PermissionRequest` (Task 1).
- Produces: `termprompt.New(in io.Reader, out io.Writer) *termprompt.Prompt`, which implements `ports.Permission`.

- [ ] **Step 1: Write the failing tests**

`internal/adapters/outbound/termprompt/prompt_test.go`:

```go
package termprompt_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/termprompt"
	"github.com/tunedev/atlas/internal/core/ports"
)

// syncBuffer is a bytes.Buffer safe to write from one goroutine and read
// from another.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuffer) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

var req = ports.PermissionRequest{ToolName: "notes.write", Kind: "edit", Summary: "Write notes.txt"}

func TestAnswers(t *testing.T) {
	cases := map[string]ports.PermissionDecision{
		"y\n": ports.PermissionAllow, "YES\n": ports.PermissionAllow, " yes \n": ports.PermissionAllow,
		"n\n": ports.PermissionDeny, "\n": ports.PermissionDeny, "maybe\n": ports.PermissionDeny,
		"": ports.PermissionDeny, // EOF
	}
	for in, want := range cases {
		out := &syncBuffer{}
		got, err := termprompt.New(strings.NewReader(in), out).Decide(context.Background(), req)
		if err != nil || got != want {
			t.Errorf("answer %q: Decide = %q, %v; want %q", in, got, err, want)
		}
		for _, part := range []string{req.ToolName, req.Kind, req.Summary} {
			if !strings.Contains(out.String(), part) {
				t.Errorf("prompt %q does not show %q", out.String(), part)
			}
		}
	}
}

func TestCancelledContextDenies(t *testing.T) {
	in, _ := io.Pipe() // never answers
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	got, err := termprompt.New(in, &syncBuffer{}).Decide(ctx, req)
	if got != ports.PermissionDeny || err == nil {
		t.Errorf("Decide = %q, %v; want deny with the context's error", got, err)
	}
}

func TestConcurrentPromptsDoNotInterleave(t *testing.T) {
	in, answer := io.Pipe()
	out := &syncBuffer{}
	p := termprompt.New(in, out)

	a := ports.PermissionRequest{ToolName: "a.tool", Kind: "read", Summary: "A"}
	b := ports.PermissionRequest{ToolName: "b.tool", Kind: "read", Summary: "B"}
	got := map[string]ports.PermissionDecision{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range []ports.PermissionRequest{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, _ := p.Decide(context.Background(), r)
			mu.Lock()
			got[r.ToolName] = d
			mu.Unlock()
		}()
	}

	// Answer yes to whichever prompt shows first, no to the second.
	first := waitForPrompt(t, out, 1)
	_, _ = io.WriteString(answer, "y\n")
	waitForPrompt(t, out, 2)
	_, _ = io.WriteString(answer, "n\n")
	wg.Wait()

	second := "b.tool"
	if first == "b.tool" {
		second = "a.tool"
	}
	if got[first] != ports.PermissionAllow || got[second] != ports.PermissionDeny {
		t.Errorf("decisions %v; want %s allowed and %s denied", got, first, second)
	}
}

// waitForPrompt waits until out holds n prompts and returns the tool name
// in the most recent one.
func waitForPrompt(t *testing.T, out *syncBuffer, n int) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := out.String()
		if strings.Count(s, "allow? [y/N]") == n {
			last := s[strings.LastIndex(s, "run ")+len("run "):]
			return last[:strings.Index(last, " ")]
		}
		if strings.Count(s, "allow? [y/N]") > n {
			t.Fatalf("a second prompt appeared before the first was answered:\n%s", s)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("prompt %d never appeared:\n%s", n, out.String())
	return ""
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/termprompt/ -v`
Expected: FAIL, package does not compile.

- [ ] **Step 3: Implement**

`internal/adapters/outbound/termprompt/prompt.go`:

```go
// Package termprompt asks a human on the terminal whether a tool call may
// run.
package termprompt

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Prompt shows one request at a time on out and reads the answer from in.
// Only y or yes allows; any other answer, end of input, or a cancelled
// context denies.
type Prompt struct {
	out   io.Writer
	lines chan string
	mu    sync.Mutex
}

// New starts the reader goroutine that owns in. It stops when in reaches
// end of input.
func New(in io.Reader, out io.Writer) *Prompt {
	p := &Prompt{out: out, lines: make(chan string)}
	go p.read(in)
	return p
}

func (p *Prompt) read(in io.Reader) {
	s := bufio.NewScanner(in)
	for s.Scan() {
		p.lines <- s.Text()
	}
	close(p.lines)
}

func (p *Prompt) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	fmt.Fprintf(p.out, "atlas: the agent wants to run %s (%s)\n  %s\nallow? [y/N] ", req.ToolName, req.Kind, req.Summary)
	select {
	case line, ok := <-p.lines:
		if ok && isYes(line) {
			return ports.PermissionAllow, nil
		}
		return ports.PermissionDeny, nil
	case <-ctx.Done():
		fmt.Fprintln(p.out)
		return ports.PermissionDeny, fmt.Errorf("termprompt: %w", ctx.Err())
	}
}

func isYes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/outbound/termprompt/ -race -count=5`
Expected: PASS all five runs.

Mutation check: delete `p.mu.Lock()` and its `defer`. Confirm
`TestConcurrentPromptsDoNotInterleave` fails with "a second prompt appeared". Revert.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/termprompt
git commit -m "Ask a human on the terminal, one permission at a time"
```

---

### Task 3: The JSON-RPC connection

**Files:**
- Create: `internal/adapters/outbound/acpagent/rpc.go`
- Test: `internal/adapters/outbound/acpagent/rpc_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces (package-private, used by Tasks 4-7):
  - `type message struct{ JSONRPC string; ID json.RawMessage; Method string; Params, Result json.RawMessage; Error *rpcError }`
  - `type rpcError struct{ Code int; Message string }` (implements `error`)
  - `type handler interface{ onNotify(method string, params json.RawMessage); onRequest(method string, params json.RawMessage) (any, *rpcError) }`
  - `newConn(r io.Reader, w io.Writer, maxBytes int, h handler) *conn`
  - `(*conn).call(ctx, method string, params, result any) error`
  - `(*conn).send(method string, params any) error` (a notification)
  - `(*conn).wait()`: blocks until the read loop has stopped and every request handler has returned
  - `(*conn).done` (`chan struct{}`), closed when the read loop stops
  - `var errExited`, the error every waiting call gets when the agent's stdout closes

- [ ] **Step 1: Write the failing tests**

`internal/adapters/outbound/acpagent/rpc_test.go`:

```go
package acpagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a handler that records notifications and answers requests.
type recorder struct {
	mu     sync.Mutex
	notes  []string
	answer func(method string) (any, *rpcError)
}

func (r *recorder) onNotify(method string, params json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, method+" "+string(params))
}

func (r *recorder) onRequest(method string, _ json.RawMessage) (any, *rpcError) {
	return r.answer(method)
}

// peer is the far end of a conn: it reads what the conn wrote, line by line,
// and writes raw lines back.
type peer struct {
	in  *bufio.Scanner
	out io.WriteCloser
}

func newPair(t *testing.T, maxBytes int, h handler) (*conn, *peer) {
	t.Helper()
	connIn, peerOut := io.Pipe()
	peerIn, connOut := io.Pipe()
	c := newConn(connIn, connOut, maxBytes, h)
	t.Cleanup(func() { _ = peerOut.Close(); _ = connOut.Close() })
	return c, &peer{in: bufio.NewScanner(peerIn), out: peerOut}
}

func (p *peer) line(t *testing.T) string {
	t.Helper()
	if !p.in.Scan() {
		t.Fatalf("peer: no line: %v", p.in.Err())
	}
	return p.in.Text()
}

func (p *peer) write(s string) { _, _ = io.WriteString(p.out, s+"\n") }

func TestCallsAreMatchedToResponsesById(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	type res struct{ V string }
	var a, b res
	errs := make(chan error, 2)
	go func() { errs <- c.call(context.Background(), "first", nil, &a) }()
	first := p.line(t)
	go func() { errs <- c.call(context.Background(), "second", nil, &b) }()
	second := p.line(t)

	// Answer out of order.
	p.write(`{"jsonrpc":"2.0","id":` + idOf(t, second) + `,"result":{"V":"two"}}`)
	p.write(`{"jsonrpc":"2.0","id":` + idOf(t, first) + `,"result":{"V":"one"}}`)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if a.V != "one" || b.V != "two" {
		t.Errorf("got %q and %q; want one and two", a.V, b.V)
	}
}

func TestEveryMessageIsOneLine(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	go func() { _ = c.send("note", map[string]string{"text": "two\nlines"}) }()
	got := p.line(t)
	var m message
	if err := json.Unmarshal([]byte(got), &m); err != nil || m.Method != "note" {
		t.Fatalf("line %q is not one complete message: %v", got, err)
	}
}

func TestNotificationsArriveInOrder(t *testing.T) {
	r := &recorder{}
	c, p := newPair(t, 1<<20, r)
	p.write(`{"jsonrpc":"2.0","method":"a","params":1}`)
	p.write(`{"jsonrpc":"2.0","method":"b","params":2}`)
	p.out.Close()
	c.wait()
	if strings.Join(r.notes, ",") != "a 1,b 2" {
		t.Errorf("notes %v; want a then b", r.notes)
	}
}

func TestIncomingRequestsAreAnswered(t *testing.T) {
	r := &recorder{answer: func(method string) (any, *rpcError) {
		if method == "known" {
			return map[string]int{"n": 7}, nil
		}
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}}
	_, p := newPair(t, 1<<20, r)

	p.write(`{"jsonrpc":"2.0","id":"x1","method":"known"}`)
	if got := p.line(t); got != `{"jsonrpc":"2.0","id":"x1","result":{"n":7}}` {
		t.Errorf("reply %s", got)
	}
	p.write(`{"jsonrpc":"2.0","id":9,"method":"unknown"}`)
	if got := p.line(t); !strings.Contains(got, `"code":-32601`) || !strings.Contains(got, `"id":9`) {
		t.Errorf("reply %s; want -32601 for id 9", got)
	}
}

func TestCancelledCallReturnsAndItsLateResponseIsDropped(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- c.call(ctx, "slow", nil, nil) }()
	id := idOf(t, p.line(t))
	if err := <-errc; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want deadline exceeded", err)
	}

	// The late response must not block the read loop: a later call still works.
	p.write(`{"jsonrpc":"2.0","id":` + id + `,"result":{}}`)
	go func() { errc <- c.call(context.Background(), "next", nil, nil) }()
	p.write(`{"jsonrpc":"2.0","id":` + idOf(t, p.line(t)) + `,"result":{}}`)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestPeerExitFailsWaitingCalls(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	errc := make(chan error, 1)
	go func() { errc <- c.call(context.Background(), "never", nil, nil) }()
	p.line(t)
	p.out.Close()
	if err := <-errc; !errors.Is(err, errExited) {
		t.Errorf("err = %v; want errExited", err)
	}
	if err := c.call(context.Background(), "after", nil, nil); !errors.Is(err, errExited) {
		t.Errorf("call after exit: err = %v; want errExited", err)
	}
}

func TestMalformedLineFailsWaitingCalls(t *testing.T) {
	c, p := newPair(t, 1<<20, &recorder{})
	errc := make(chan error, 1)
	go func() { errc <- c.call(context.Background(), "x", nil, nil) }()
	p.line(t)
	p.write(`Loading model...`)
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "Loading model") {
		t.Errorf("err = %v; want one naming the line", err)
	}
}

func TestOverSizeLineFailsWaitingCalls(t *testing.T) {
	c, p := newPair(t, 64, &recorder{})
	errc := make(chan error, 1)
	go func() { errc <- c.call(context.Background(), "x", nil, nil) }()
	p.line(t)
	go p.write(`{"jsonrpc":"2.0","method":"n","params":"` + strings.Repeat("a", 200) + `"}`)
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "64 bytes") {
		t.Errorf("err = %v; want one naming the 64-byte limit", err)
	}
}

func idOf(t *testing.T, line string) string {
	t.Helper()
	var m message
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("line %q: %v", line, err)
	}
	return string(m.ID)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/acpagent/ -v`
Expected: FAIL, package does not compile.

- [ ] **Step 3: Implement**

`internal/adapters/outbound/acpagent/rpc.go`:

```go
// Package acpagent drives a coding agent over the Agent Client Protocol:
// JSON-RPC 2.0, one message per line, over the agent subprocess's stdio.
package acpagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// errExited is what every waiting call gets once the agent's stdout closes.
var errExited = errors.New("acpagent: agent process exited")

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// handler receives what the agent sends unprompted. onNotify runs on the
// read loop, in arrival order. onRequest runs on its own goroutine, so it
// may block.
type handler interface {
	onNotify(method string, params json.RawMessage)
	onRequest(method string, params json.RawMessage) (any, *rpcError)
}

// conn is one JSON-RPC conversation. Its read loop is the only reader of r.
type conn struct {
	w    io.Writer
	wmu  sync.Mutex
	h    handler
	next atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan message
	err     error

	requests sync.WaitGroup
	done     chan struct{}
}

// newConn starts the read loop. It owns r and stops when r ends, when r
// yields a line that is not JSON-RPC, or when a line exceeds maxBytes.
func newConn(r io.Reader, w io.Writer, maxBytes int, h handler) *conn {
	c := &conn{w: w, h: h, pending: map[int64]chan message{}, done: make(chan struct{})}
	go c.readLoop(r, maxBytes)
	return c
}

// call sends a request and waits for its response, ctx, or the end of the
// conversation, whichever comes first.
func (c *conn) call(ctx context.Context, method string, params, result any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("acpagent: encode %s: %w", method, err)
	}
	id := c.next.Add(1)
	ch := make(chan message, 1)

	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(message{ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: p}); err != nil {
		return err
	}
	select {
	case m := <-ch:
		return decode(method, m, result)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		select {
		case m := <-ch:
			return decode(method, m, result)
		default:
			return c.failure()
		}
	}
}

// send writes a notification.
func (c *conn) send(method string, params any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("acpagent: encode %s: %w", method, err)
	}
	return c.write(message{Method: method, Params: p})
}

// wait blocks until the read loop has stopped and every request handler has
// returned.
func (c *conn) wait() {
	<-c.done
	c.requests.Wait()
}

func (c *conn) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// write sends m as one line. json.Marshal never emits a raw newline, so the
// line cannot be split.
func (c *conn) write(m message) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("acpagent: encode: %w", err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("acpagent: write: %w", err)
	}
	return nil
}

func (c *conn) readLoop(r io.Reader, maxBytes int) {
	err := c.dispatchAll(r, maxBytes)
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
	close(c.done)
}

func (c *conn) dispatchAll(r io.Reader, maxBytes int) error {
	s := bufio.NewScanner(r)
	s.Buffer(nil, maxBytes)
	for s.Scan() {
		var m message
		if err := json.Unmarshal(s.Bytes(), &m); err != nil {
			return fmt.Errorf("acpagent: agent wrote a line that is not JSON-RPC: %q", s.Text())
		}
		c.dispatch(m)
	}
	if errors.Is(s.Err(), bufio.ErrTooLong) {
		return fmt.Errorf("acpagent: agent sent a message over %d bytes", maxBytes)
	}
	if s.Err() != nil {
		return fmt.Errorf("acpagent: read: %w", s.Err())
	}
	return errExited
}

func (c *conn) dispatch(m message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		c.requests.Add(1)
		// Owned by the conn; wait joins it. It stops when onRequest returns.
		go func() {
			defer c.requests.Done()
			c.answer(m)
		}()
	case m.Method != "":
		c.h.onNotify(m.Method, m.Params)
	default:
		c.deliver(m)
	}
}

func (c *conn) answer(m message) {
	result, rerr := c.h.onRequest(m.Method, m.Params)
	reply := message{ID: m.ID, Error: rerr}
	if rerr == nil {
		b, err := json.Marshal(result)
		if err != nil {
			reply.Error = &rpcError{Code: -32603, Message: err.Error()}
		} else {
			reply.Result = b
		}
	}
	_ = c.write(reply) // an agent that has gone cannot be answered
}

// deliver hands a response to the call waiting for it. A response nobody is
// waiting for, because its call gave up, is dropped.
func (c *conn) deliver(m message) {
	id, err := strconv.ParseInt(string(m.ID), 10, 64)
	if err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	c.mu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- m:
	default:
	}
}

func decode(method string, m message, result any) error {
	if m.Error != nil {
		return fmt.Errorf("acpagent: %s: %w", method, m.Error)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(m.Result, result); err != nil {
		return fmt.Errorf("acpagent: decode %s result: %w", method, err)
	}
	return nil
}
```

`TestIncomingRequestsAreAnswered` asserts an exact line, so field order matters:
`message`'s field order (`jsonrpc`, `id`, `result`) is what produces it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/outbound/acpagent/ -race -count=3`
Expected: PASS.

Mutation check: in `deliver`, replace the `select` with a bare `ch <- m`. Confirm
`TestCancelledCallReturnsAndItsLateResponseIsDropped` still passes, because the buffer of one
absorbs it. Then also remove `delete(c.pending, id)` from `call`'s defer and confirm the test
now hangs or fails under `-timeout 10s`. Revert both.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/acpagent/rpc.go internal/adapters/outbound/acpagent/rpc_test.go
git commit -m "Speak line-delimited JSON-RPC, failing every waiting call when the peer goes"
```

---

### Task 4: Launch, negotiate, shut down (stories 4.1, 4.2)

**Files:**
- Create: `internal/adapters/outbound/acpagent/wire.go`
- Create: `internal/adapters/outbound/acpagent/client.go`
- Create: `internal/adapters/outbound/acpagent/fake_test.go` (the scripted stub, shared by Tasks 4-7)
- Create: `internal/adapters/outbound/acpagent/main_test.go` (a subprocess stub mode of the test binary)
- Test: `internal/adapters/outbound/acpagent/client_test.go`

**Interfaces:**
- Consumes: `conn`, `handler`, `errExited` (Task 3), `ports.Permission` (Task 1).
- Produces:
  - `acpagent.Config{Command string; Args, Env []string; Stderr io.Writer; MaxMessageBytes, SummaryBytes int; MCP *MCPServer}`
  - `acpagent.MCPServer{Name, URL string; Headers map[string]string}`
  - `acpagent.New(ctx, Config, ports.Permission) (*Client, error)`
  - `(*Client).ProtocolVersion() int`, `(*Client).Close(ctx) error`
  - package-private `newClient(r io.Reader, w io.WriteCloser, cfg Config, perm ports.Permission) *Client` and `(*Client).initialize(ctx) error`, the seam every in-memory test uses
  - package-private `(*Client).onNotify` and `(*Client).onRequest`, stubbed here and filled by Tasks 5 and 7

- [ ] **Step 1: Write the wire types**

`internal/adapters/outbound/acpagent/wire.go`:

```go
package acpagent

import "encoding/json"

// protocolVersion is the one ACP major version this client speaks.
const protocolVersion = 1

const (
	clientName    = "atlas"
	clientVersion = "0"
)

type initializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
	ClientInfo         implementation     `json:"clientInfo"`
}

// clientCapabilities advertises no file system and no terminal: the agent
// uses its own, and reaches Atlas only through MCP.
type clientCapabilities struct {
	FS       fsCapabilities `json:"fs"`
	Terminal bool           `json:"terminal"`
}

type fsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities agentCapabilities `json:"agentCapabilities"`
}

type agentCapabilities struct {
	LoadSession     bool            `json:"loadSession"`
	MCPCapabilities mcpCapabilities `json:"mcpCapabilities"`
}

type mcpCapabilities struct {
	HTTP bool `json:"http"`
}

type mcpServer struct {
	Type    string       `json:"type"`
	Name    string       `json:"name"`
	URL     string       `json:"url"`
	Headers []httpHeader `json:"headers"`
}

type httpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type newSessionParams struct {
	CWD        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

type newSessionResult struct {
	SessionID string `json:"sessionId"`
}

type loadSessionParams struct {
	SessionID  string      `json:"sessionId"`
	CWD        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

type cancelParams struct {
	SessionID string `json:"sessionId"`
}

type sessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

// sessionUpdate is every session/update variant in one shape. Content is
// raw because a message chunk carries one content block and a tool call
// carries a list.
type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Name          string          `json:"name,omitempty"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	Entries       []planEntry     `json:"entries,omitempty"`
}

type planEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type permissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  sessionUpdate      `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionResult struct {
	Outcome permissionOutcome `json:"outcome"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}
```

- [ ] **Step 2: Write the scripted stub**

`internal/adapters/outbound/acpagent/fake_test.go`:

```go
package acpagent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// fakeAgent is the agent side of an ACP conversation over in-memory pipes.
// A test scripts it from its own goroutine; failures use t.Errorf because
// t.Fatal may not be called off the test goroutine.
type fakeAgent struct {
	t   *testing.T
	in  *bufio.Scanner
	out io.WriteCloser
}

func pipes(t *testing.T) (io.Reader, io.WriteCloser, *fakeAgent) {
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	return clientIn, clientOut, &fakeAgent{t: t, in: bufio.NewScanner(agentIn), out: agentOut}
}

func (f *fakeAgent) next() message {
	if !f.in.Scan() {
		f.t.Errorf("fake agent: client stopped writing: %v", f.in.Err())
		return message{}
	}
	var m message
	if err := json.Unmarshal(f.in.Bytes(), &m); err != nil {
		f.t.Errorf("fake agent: bad line %q", f.in.Text())
	}
	return m
}

func (f *fakeAgent) expect(method string) message {
	m := f.next()
	if m.Method != method {
		f.t.Errorf("fake agent: got %q, want %q", m.Method, method)
	}
	return m
}

func (f *fakeAgent) write(v any) {
	b, _ := json.Marshal(v)
	_, _ = f.out.Write(append(b, '\n'))
}

func (f *fakeAgent) reply(m message, result any) {
	f.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
}

func (f *fakeAgent) update(sessionID string, u map[string]any) {
	f.write(map[string]any{"jsonrpc": "2.0", "method": "session/update",
		"params": map[string]any{"sessionId": sessionID, "update": u}})
}

// ask sends the client a request and returns the client's reply.
func (f *fakeAgent) ask(id int, method string, params any) message {
	f.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return f.next()
}

func (f *fakeAgent) handshake(caps map[string]any) {
	m := f.expect("initialize")
	f.reply(m, map[string]any{"protocolVersion": 1, "agentCapabilities": caps})
}

func (f *fakeAgent) hangUp() { _ = f.out.Close() }

func testConfig() Config {
	return Config{Command: "fake", Stderr: io.Discard, MaxMessageBytes: 1 << 20, SummaryBytes: 200}
}

// startTestClient returns an initialized client talking to a fake agent.
func startTestClient(t *testing.T, cfg Config, caps map[string]any, perm ports.Permission) (*Client, *fakeAgent) {
	t.Helper()
	r, w, fa := pipes(t)
	c := newClient(r, w, cfg, perm)
	go fa.handshake(caps)
	if err := c.initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	t.Cleanup(func() {
		fa.hangUp()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Close(ctx)
	})
	return c, fa
}

// allowAll allows every request.
type allowAll struct{}

func (allowAll) Decide(context.Context, ports.PermissionRequest) (ports.PermissionDecision, error) {
	return ports.PermissionAllow, nil
}
```

- [ ] **Step 3: Write the subprocess stub mode**

`internal/adapters/outbound/acpagent/main_test.go`:

```go
package acpagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMain makes the test binary double as a minimal ACP agent when
// ACPAGENT_STUB names a mode, so launch and shutdown are tested against a
// real subprocess.
//
//	clean     answers initialize, exits at stdin EOF
//	stubborn  answers initialize, ignores stdin EOF
//	die       answers initialize, exits on the next request without replying
func TestMain(m *testing.M) {
	if mode := os.Getenv("ACPAGENT_STUB"); mode != "" {
		runStub(mode)
		return
	}
	os.Exit(m.Run())
}

func runStub(mode string) {
	fmt.Fprintln(os.Stderr, "stub ready")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var m message
		_ = json.Unmarshal(in.Bytes(), &m)
		if m.Method == "initialize" {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID,
				"result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}})
			fmt.Println(string(b))
			continue
		}
		if mode == "die" {
			os.Exit(3)
		}
	}
	if mode == "stubborn" {
		time.Sleep(time.Hour)
	}
}
```

- [ ] **Step 4: Write the failing tests**

`internal/adapters/outbound/acpagent/client_test.go`:

```go
package acpagent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stubConfig(mode string, stderr *bytes.Buffer) Config {
	cfg := testConfig()
	cfg.Command = os.Args[0]
	cfg.Env = append(os.Environ(), "ACPAGENT_STUB="+mode)
	cfg.Stderr = stderr
	return cfg
}

func TestInitializeNegotiatesVersionAndCapabilities(t *testing.T) {
	c, _ := startTestClient(t, testConfig(), map[string]any{"loadSession": true}, allowAll{})
	if c.ProtocolVersion() != 1 || !c.caps.LoadSession {
		t.Errorf("version %d, caps %+v; want 1 with loadSession", c.ProtocolVersion(), c.caps)
	}
}

func TestInitializeRejectsAnUnsupportedVersion(t *testing.T) {
	r, w, fa := pipes(t)
	c := newClient(r, w, testConfig(), allowAll{})
	go func() {
		m := fa.expect("initialize")
		fa.reply(m, map[string]any{"protocolVersion": 2, "agentCapabilities": map[string]any{}})
	}()
	err := c.initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Errorf("err = %v; want one naming version 2", err)
	}
}

func TestInitializeRequiresHTTPMCPWhenToolsAreOffered(t *testing.T) {
	cfg := testConfig()
	cfg.MCP = &MCPServer{Name: "atlas", URL: "http://127.0.0.1:1/mcp"}
	r, w, fa := pipes(t)
	c := newClient(r, w, cfg, allowAll{})
	go fa.handshake(map[string]any{"mcpCapabilities": map[string]any{"http": false}})
	if err := c.initialize(context.Background()); err == nil || !strings.Contains(err.Error(), "MCP") {
		t.Errorf("err = %v; want one naming MCP", err)
	}
}

func TestNewLaunchesAndClosesCleanly(t *testing.T) {
	var stderr bytes.Buffer
	c, err := New(context.Background(), stubConfig("clean", &stderr), allowAll{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	want := filepath.Base(os.Args[0]) + ": stub ready\n"
	if stderr.String() != want {
		t.Errorf("stderr %q; want %q", stderr.String(), want)
	}
}

func TestCloseKillsAnAgentThatIgnoresEOF(t *testing.T) {
	c, err := New(context.Background(), stubConfig("stubborn", &bytes.Buffer{}), allowAll{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = c.Close(ctx)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close err = %v; want it to report the deadline", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("Close took %s; the kill did not happen", time.Since(start))
	}
}

func TestNewFailsLoudlyForAMissingCommand(t *testing.T) {
	cfg := testConfig()
	cfg.Command = filepath.Join(t.TempDir(), "no-such-agent")
	_, err := New(context.Background(), cfg, allowAll{})
	if err == nil || !strings.Contains(err.Error(), "no-such-agent") {
		t.Errorf("err = %v; want one naming the command", err)
	}
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/acpagent/ -v`
Expected: FAIL, `undefined: newClient`.

- [ ] **Step 6: Implement**

`internal/adapters/outbound/acpagent/client.go`:

```go
package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"

	"github.com/tunedev/atlas/internal/core/ports"
)

// Config says which agent to run and how to bound it. The agent is any
// command that speaks ACP on its stdio. MCP, when set, is the one server
// through which the agent reaches Atlas's tools.
type Config struct {
	Command         string
	Args            []string
	Env             []string
	Stderr          io.Writer
	MaxMessageBytes int
	SummaryBytes    int
	MCP             *MCPServer
}

// MCPServer is an HTTP MCP server the agent is told to connect to.
type MCPServer struct {
	Name    string
	URL     string
	Headers map[string]string
}

// Client is one running agent subprocess. It implements ports.Agent.
type Client struct {
	conn  *conn
	stdin io.Closer
	cmd   *exec.Cmd
	perm  ports.Permission
	cfg   Config

	version int
	caps    agentCapabilities

	closing   chan struct{}
	closeOnce sync.Once

	turnMu sync.Mutex // one turn at a time
	mu     sync.Mutex // guards turn
	turn   *turn
}

// New launches the agent and runs initialize, bounded by ctx. An agent that
// speaks another protocol version, or cannot reach an HTTP MCP server when
// one is configured, fails here, never partway through a turn.
func New(ctx context.Context, cfg Config, perm ports.Permission) (*Client, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = cfg.Env
	cmd.Stderr = &prefixWriter{w: cfg.Stderr, prefix: filepath.Base(cfg.Command) + ": "}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acpagent: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acpagent: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acpagent: start %s: %w", cfg.Command, err)
	}

	c := newClient(stdout, stdin, cfg, perm)
	c.cmd = cmd
	if err := c.initialize(ctx); err != nil {
		c.kill()
		c.conn.wait()
		_ = cmd.Wait()
		return nil, err
	}
	return c, nil
}

func newClient(r io.Reader, w io.WriteCloser, cfg Config, perm ports.Permission) *Client {
	c := &Client{stdin: w, perm: perm, cfg: cfg, closing: make(chan struct{})}
	c.conn = newConn(r, w, cfg.MaxMessageBytes, c)
	return c
}

func (c *Client) initialize(ctx context.Context) error {
	var res initializeResult
	err := c.conn.call(ctx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      implementation{Name: clientName, Version: clientVersion},
	}, &res)
	if err != nil {
		return fmt.Errorf("acpagent: initialize: %w", err)
	}
	if res.ProtocolVersion != protocolVersion {
		return fmt.Errorf("acpagent: agent speaks protocol version %d; atlas speaks %d", res.ProtocolVersion, protocolVersion)
	}
	if c.cfg.MCP != nil && !res.AgentCapabilities.MCPCapabilities.HTTP {
		return errors.New("acpagent: atlas tools are configured, but the agent cannot reach an HTTP MCP server")
	}
	c.version = res.ProtocolVersion
	c.caps = res.AgentCapabilities
	return nil
}

// ProtocolVersion is the ACP version agreed at initialize.
func (c *Client) ProtocolVersion() int { return c.version }

// Close closes the agent's stdin, which ACP agents treat as shutdown, and
// waits for it to exit. If ctx ends first the process is killed.
func (c *Client) Close(ctx context.Context) error {
	c.closeOnce.Do(func() { close(c.closing) })
	_ = c.stdin.Close()

	stopped := make(chan error, 1)
	// Owned by Close; stops once the read loop, request handlers and the
	// process have all finished, which the kill below guarantees.
	go func() {
		c.conn.wait()
		stopped <- c.reap()
	}()
	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
		c.kill()
		return fmt.Errorf("acpagent: agent did not exit after stdin closed, so it was killed: %w", errors.Join(ctx.Err(), <-stopped))
	}
}

func (c *Client) reap() error {
	if c.cmd == nil {
		return nil
	}
	if err := c.cmd.Wait(); err != nil {
		return fmt.Errorf("acpagent: agent exit: %w", err)
	}
	return nil
}

func (c *Client) kill() {
	if c.cmd != nil {
		_ = c.cmd.Process.Kill()
	}
}

// mcpServers is the MCP list sent with every session/new and session/load.
func (c *Client) mcpServers() []mcpServer {
	if c.cfg.MCP == nil {
		return []mcpServer{}
	}
	names := make([]string, 0, len(c.cfg.MCP.Headers))
	for n := range c.cfg.MCP.Headers {
		names = append(names, n)
	}
	sort.Strings(names)
	headers := make([]httpHeader, len(names))
	for i, n := range names {
		headers[i] = httpHeader{Name: n, Value: c.cfg.MCP.Headers[n]}
	}
	return []mcpServer{{Type: "http", Name: c.cfg.MCP.Name, URL: c.cfg.MCP.URL, Headers: headers}}
}

func (c *Client) onNotify(string, json.RawMessage) {}

func (c *Client) onRequest(method string, _ json.RawMessage) (any, *rpcError) {
	return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
}

// prefixWriter writes each line of the agent's stderr with prefix in front.
type prefixWriter struct {
	w      io.Writer
	prefix string
	mid    bool
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	out := make([]byte, 0, len(b)+len(p.prefix))
	for _, ch := range b {
		if !p.mid {
			out = append(out, p.prefix...)
			p.mid = true
		}
		out = append(out, ch)
		if ch == '\n' {
			p.mid = false
		}
	}
	if _, err := p.w.Write(out); err != nil {
		return 0, err
	}
	return len(b), nil
}
```

Also add a `turn` placeholder so the package compiles. Task 5 replaces it:

```go
// in turn.go
package acpagent

type turn struct{}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/adapters/outbound/acpagent/ -race -count=3`
Expected: PASS.

Mutation check: in `initialize`, change `!=` to `==` in the version check, and confirm
`TestInitializeRejectsAnUnsupportedVersion` fails. In `Close`, remove `c.kill()`, and confirm
`TestCloseKillsAnAgentThatIgnoresEOF` hangs past `-timeout 20s`. Revert both.

- [ ] **Step 8: Commit**

```bash
git add internal/adapters/outbound/acpagent
git commit -m "Launch an ACP agent, agree a version at startup, and shut it down or kill it"
```

---

### Task 5: A turn: new session, prompt, streamed events (story 4.3)

**Files:**
- Modify: `internal/adapters/outbound/acpagent/turn.go` (replace the placeholder)
- Modify: `internal/adapters/outbound/acpagent/client.go` (remove the stub `onNotify`)
- Test: `internal/adapters/outbound/acpagent/turn_test.go`
- Modify: `internal/adapters/outbound/acpagent/client_test.go` (the `ports.Agent` assertion)

**Interfaces:**
- Consumes: Task 4's `Client`, wire types and `fakeAgent`.
- Produces: `(*Client).Do(ctx, ports.AgentTask, sessionID string, onEvent func(ports.AgentEvent)) (ports.AgentResult, string, error)`.
  Package-private, used by Tasks 6 and 7: `turn{sessionID string; updates chan sessionUpdate; ended chan struct{}; calls map[string]sessionUpdate}`,
  `(*Client).begin(sessionID) *turn`, `(*Client).end(*turn)`, `merge(prev, u sessionUpdate) sessionUpdate`,
  `(*Client).await(ctx, t, method, params, result, handle func(sessionUpdate)) error`.

- [ ] **Step 1: Write the failing tests**

`internal/adapters/outbound/acpagent/turn_test.go`:

```go
package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

func chunk(text string) map[string]any {
	return map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}
}

func TestDoStreamsATurnInOrder(t *testing.T) {
	cfg := testConfig()
	cfg.MCP = &MCPServer{Name: "atlas", URL: "http://127.0.0.1:9/mcp", Headers: map[string]string{"Authorization": "Bearer k"}}
	c, fa := startTestClient(t, cfg, map[string]any{"mcpCapabilities": map[string]any{"http": true}}, allowAll{})
	dir := t.TempDir()

	go func() {
		m := fa.expect("session/new")
		var p newSessionParams
		_ = json.Unmarshal(m.Params, &p)
		if p.CWD != dir || len(p.MCPServers) != 1 || p.MCPServers[0].Headers[0].Value != "Bearer k" {
			t.Errorf("session/new params %+v", p)
		}
		fa.reply(m, map[string]any{"sessionId": "s1"})
		pr := fa.expect("session/prompt")
		fa.update("s1", chunk("Hel"))
		fa.update("someone-else", chunk("XX"))
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Read notes.txt", "kind": "read", "status": "pending"})
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed"})
		fa.update("s1", map[string]any{"sessionUpdate": "plan", "entries": []any{
			map[string]any{"content": "read the notes", "status": "completed", "priority": "high"},
			map[string]any{"content": "summarise", "status": "pending", "priority": "high"},
		}})
		fa.update("s1", map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "hmm"}})
		fa.update("s1", chunk("lo"))
		fa.reply(pr, map[string]any{"stopReason": "end_turn"})
	}()

	var events []ports.AgentEvent
	res, sid, err := c.Do(context.Background(), ports.AgentTask{Prompt: "summarise my notes", WorkDir: dir}, "",
		func(e ports.AgentEvent) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello" || res.StopReason != "end_turn" || sid != "s1" {
		t.Errorf("result %+v, session %q", res, sid)
	}
	want := []ports.AgentEvent{
		{Kind: ports.AgentEventMessage, Text: "Hel"},
		{Kind: ports.AgentEventToolCall, Text: "Read notes.txt [pending]"},
		{Kind: ports.AgentEventToolCall, Text: "Read notes.txt [completed]"},
		{Kind: ports.AgentEventPlan, Text: "[completed] read the notes\n[pending] summarise"},
		{Kind: ports.AgentEventMessage, Text: "lo"},
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("events\n got %+v\nwant %+v", events, want)
	}
}

func TestDoRejectsARelativeWorkDir(t *testing.T) {
	c, _ := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	_, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: "notes"}, "", func(ports.AgentEvent) {})
	if err == nil || !strings.Contains(err.Error(), "notes") {
		t.Errorf("err = %v; want one naming the directory", err)
	}
}

func TestDoTimeoutCancelsTheTurnAndKeepsTheAgent(t *testing.T) {
	c, fa := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	cancelled := make(chan string, 1)
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		pr := fa.expect("session/prompt")
		var p cancelParams
		_ = json.Unmarshal(fa.expect("session/cancel").Params, &p)
		cancelled <- p.SessionID
		fa.update("s1", chunk("late"))
		fa.reply(pr, map[string]any{"stopReason": "cancelled"})

		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s2"})
		fa.reply(fa.expect("session/prompt"), map[string]any{"stopReason": "end_turn"})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, sid, err := c.Do(ctx, ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if !errors.Is(err, context.DeadlineExceeded) || sid != "s1" {
		t.Fatalf("Do = %q, %v; want s1 and deadline exceeded", sid, err)
	}
	if got := <-cancelled; got != "s1" {
		t.Errorf("session/cancel for %q; want s1", got)
	}

	_, sid, err = c.Do(context.Background(), ports.AgentTask{Prompt: "again", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if err != nil || sid != "s2" {
		t.Errorf("second Do = %q, %v; the agent should still be usable", sid, err)
	}
}

func TestDoFailsWhenTheAgentDiesMidTurn(t *testing.T) {
	c, fa := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		fa.expect("session/prompt")
		fa.hangUp()
	}()
	_, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if !errors.Is(err, errExited) {
		t.Errorf("err = %v; want errExited", err)
	}
}

func TestARealSubprocessDyingMidTurnFailsTheTurn(t *testing.T) {
	c, err := New(context.Background(), stubConfig("die", &bytes.Buffer{}), allowAll{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	_, _, err = c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})
	if !errors.Is(err, errExited) {
		t.Errorf("err = %v; want errExited", err)
	}
}
```

Add `"bytes"` to the imports for the last test.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/acpagent/ -run Do -v`
Expected: FAIL, `c.Do undefined`.

- [ ] **Step 3: Implement**

Delete `onNotify` from `client.go`. Replace `turn.go`:

```go
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
```

`t.sessionID` is written under `c.mu` in `open` and read without it on the `Do` goroutine. That
is safe because `Do` is the only writer. `onNotify` reads it under `c.mu`.

Add to `client_test.go` (and import `ports`): `var _ ports.Agent = (*Client)(nil)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/outbound/acpagent/ -race -count=5`
Expected: PASS.

Mutation check: in `onNotify`, delete `|| n.SessionID != t.sessionID`, and confirm
`TestDoStreamsATurnInOrder` fails with an `XX` event. Replace `case <-t.ended:` with nothing (a
bare send), and confirm `TestDoTimeoutCancelsTheTurnAndKeepsTheAgent` hangs under `-timeout 20s`.
Revert both.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/acpagent
git commit -m "Run a turn: open a session, prompt, stream events on the caller's goroutine"
```

---

### Task 6: Resume (story 4.6)

**Files:**
- Modify: `internal/adapters/outbound/acpagent/turn.go` (`open`, plus `unverified`)
- Test: `internal/adapters/outbound/acpagent/resume_test.go`

**Interfaces:**
- Consumes: Task 5's `turn`, `await`, `open`.
- Produces: `Do` with a non-empty `sessionID` sends `session/load` and fills
  `AgentResult.Unverified`.

- [ ] **Step 1: Write the failing tests**

`internal/adapters/outbound/acpagent/resume_test.go`:

```go
package acpagent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tunedev/atlas/internal/core/ports"
)

func TestDoResumesAndDoesNotReplayOldEvents(t *testing.T) {
	c, fa := startTestClient(t, testConfig(), map[string]any{"loadSession": true}, allowAll{})
	dir := t.TempDir()
	go func() {
		m := fa.expect("session/load")
		var p loadSessionParams
		_ = json.Unmarshal(m.Params, &p)
		if p.SessionID != "s1" || p.CWD != dir {
			t.Errorf("session/load params %+v", p)
		}
		fa.update("s1", map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "first ask"}})
		fa.update("s1", chunk("old answer"))
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "a", "title": "Read notes.txt", "status": "completed"})
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "b", "title": "Write summary.txt", "status": "in_progress"})
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c", "title": "Delete draft.txt"})
		fa.reply(m, nil)

		pr := fa.expect("session/prompt")
		fa.update("s1", chunk("new answer"))
		fa.reply(pr, map[string]any{"stopReason": "end_turn"})
	}()

	var events []ports.AgentEvent
	res, sid, err := c.Do(context.Background(), ports.AgentTask{Prompt: "continue", WorkDir: dir}, "s1",
		func(e ports.AgentEvent) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if sid != "s1" || res.Text != "new answer" {
		t.Errorf("session %q, text %q", sid, res.Text)
	}
	if len(events) != 1 {
		t.Errorf("events %+v; replayed history must not be re-emitted", events)
	}
	if want := []string{"Delete draft.txt", "Write summary.txt"}; !reflect.DeepEqual(res.Unverified, want) {
		t.Errorf("unverified %v; want %v", res.Unverified, want)
	}
}

func TestDoWillNotResumeWithoutLoadSession(t *testing.T) {
	c, _ := startTestClient(t, testConfig(), map[string]any{"loadSession": false}, allowAll{})
	_, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "s1", func(ports.AgentEvent) {})
	if err == nil || !strings.Contains(err.Error(), "loadSession") {
		t.Errorf("err = %v; want one naming loadSession", err)
	}
}
```

`TestDoWillNotResumeWithoutLoadSession` passes only if no RPC is sent: the fake does not read
anything, so a `session/load` would block the write and the test would time out.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/acpagent/ -run Resume -v`
Expected: FAIL, `session/new` is sent where `session/load` was expected.

- [ ] **Step 3: Implement**

Replace `open` in `turn.go` and add `unverified`:

```go
// open starts a new session, or loads t.sessionID. A load replays the whole
// conversation before it returns. The replay is consumed, not re-emitted,
// and open returns the replayed tool calls still pending or in progress.
func (c *Client) open(ctx context.Context, t *turn, workDir string) ([]string, error) {
	if t.sessionID != "" {
		err := c.await(ctx, t, "session/load", loadSessionParams{
			SessionID: t.sessionID, CWD: workDir, MCPServers: c.mcpServers(),
		}, nil, nil)
		if err != nil {
			return nil, err
		}
		return c.unverified(t), nil
	}

	var res newSessionResult
	if err := c.await(ctx, t, "session/new", newSessionParams{CWD: workDir, MCPServers: c.mcpServers()}, &res, nil); err != nil {
		return nil, err
	}
	c.mu.Lock()
	t.sessionID = res.SessionID
	c.mu.Unlock()
	return nil, nil
}

// unverified is the sorted titles of t's tool calls whose last status was
// pending or in progress. An absent status is pending.
func (c *Client) unverified(t *turn) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, call := range t.calls {
		switch call.Status {
		case "", "pending", "in_progress":
			out = append(out, call.Title)
		}
	}
	sort.Strings(out)
	return out
}
```

Add `"sort"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/outbound/acpagent/ -race -count=3`
Expected: PASS.

Mutation check: remove `case "",`'s empty string, and confirm the `Delete draft.txt`
expectation fails. Revert.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/acpagent
git commit -m "Resume a session by replay, and name the tool calls the replay cannot vouch for"
```

---

### Task 7: The agent's permission requests (story 4.5)

**Files:**
- Create: `internal/adapters/outbound/acpagent/permission.go`
- Modify: `internal/adapters/outbound/acpagent/client.go` (remove the stub `onRequest`)
- Test: `internal/adapters/outbound/acpagent/permission_test.go`

**Interfaces:**
- Consumes: Task 5's `turn` and `merge`, `ports.Permission`, `app.BoundSummary` (Task 1).
- Produces: `(*Client).onRequest`, answering `session/request_permission` through
  `ports.Permission` and returning `-32601` for any other method.

- [ ] **Step 1: Write the failing tests**

`internal/adapters/outbound/acpagent/permission_test.go`:

```go
package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// recordingPermission answers with decision (after block, when set) and
// records what it was asked and whether its context was cancelled.
type recordingPermission struct {
	mu        sync.Mutex
	decision  ports.PermissionDecision
	err       error
	block     chan struct{}
	asked     []ports.PermissionRequest
	cancelled bool
}

func (p *recordingPermission) Decide(ctx context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	p.mu.Lock()
	p.asked = append(p.asked, req)
	p.mu.Unlock()
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			p.mu.Lock()
			p.cancelled = true
			p.mu.Unlock()
			return ports.PermissionDeny, ctx.Err()
		}
	}
	return p.decision, p.err
}

var fourOptions = []any{
	map[string]any{"optionId": "yes-once", "name": "Allow", "kind": "allow_once"},
	map[string]any{"optionId": "yes-always", "name": "Always", "kind": "allow_always"},
	map[string]any{"optionId": "no-once", "name": "Reject", "kind": "reject_once"},
	map[string]any{"optionId": "no-always", "name": "Never", "kind": "reject_always"},
}

// askDuringTurn runs a turn in which the agent reports tool call t1, then
// asks permission for it by id alone, and returns the client's reply.
func askDuringTurn(t *testing.T, perm ports.Permission, options []any) permissionResult {
	t.Helper()
	c, fa := startTestClient(t, testConfig(), map[string]any{}, perm)
	replies := make(chan message, 1)
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		pr := fa.expect("session/prompt")
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1",
			"title": "Write notes.txt", "kind": "edit", "rawInput": map[string]any{"path": "notes.txt", "text": strings.Repeat("x", 500)}})
		replies <- fa.ask(100, "session/request_permission", map[string]any{
			"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "t1"}, "options": options})
		fa.reply(pr, map[string]any{"stopReason": "end_turn"})
	}()
	if _, _, err := c.Do(context.Background(), ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {}); err != nil {
		t.Fatal(err)
	}
	var res permissionResult
	if err := json.Unmarshal((<-replies).Result, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPermissionDecisionsSelectTheMatchingOption(t *testing.T) {
	cases := []struct {
		perm *recordingPermission
		want permissionOutcome
	}{
		{&recordingPermission{decision: ports.PermissionAllow}, permissionOutcome{Outcome: "selected", OptionID: "yes-once"}},
		{&recordingPermission{decision: ports.PermissionDeny}, permissionOutcome{Outcome: "selected", OptionID: "no-once"}},
		{&recordingPermission{decision: ports.PermissionAsk}, permissionOutcome{Outcome: "selected", OptionID: "no-once"}},
		{&recordingPermission{decision: ports.PermissionAllow, err: errors.New("broken")}, permissionOutcome{Outcome: "selected", OptionID: "no-once"}},
	}
	for _, c := range cases {
		if got := askDuringTurn(t, c.perm, fourOptions).Outcome; got != c.want {
			t.Errorf("decision %q err %v: outcome %+v; want %+v", c.perm.decision, c.perm.err, got, c.want)
		}
	}
}

func TestPermissionFallsBackToAlwaysOrCancelled(t *testing.T) {
	alwaysOnly := []any{fourOptions[1], fourOptions[3]}
	if got := askDuringTurn(t, &recordingPermission{decision: ports.PermissionAllow}, alwaysOnly).Outcome; got.OptionID != "yes-always" {
		t.Errorf("allow with only always options: %+v", got)
	}
	allowOnly := []any{fourOptions[0]}
	if got := askDuringTurn(t, &recordingPermission{decision: ports.PermissionDeny}, allowOnly).Outcome; got.Outcome != "cancelled" {
		t.Errorf("deny with no reject option: %+v; want cancelled", got)
	}
}

func TestPermissionShowsTheReportedToolCallBounded(t *testing.T) {
	perm := &recordingPermission{decision: ports.PermissionAllow}
	askDuringTurn(t, perm, fourOptions)
	if len(perm.asked) != 1 {
		t.Fatalf("asked %d times", len(perm.asked))
	}
	got := perm.asked[0]
	if got.ToolName != "Write notes.txt" || got.Kind != "edit" {
		t.Errorf("request %+v; want the title and kind from the earlier tool_call", got)
	}
	if !strings.HasPrefix(got.Summary, `Write notes.txt {"path":"notes.txt"`) || len(got.Summary) > 200+len("...") {
		t.Errorf("summary %q (%d bytes); want the title and arguments within 200 bytes", got.Summary, len(got.Summary))
	}
}

func TestPendingPermissionIsCancelledWhenTheTurnEnds(t *testing.T) {
	perm := &recordingPermission{decision: ports.PermissionAllow, block: make(chan struct{})}
	c, fa := startTestClient(t, testConfig(), map[string]any{}, perm)
	replies := make(chan message, 1)
	go func() {
		fa.reply(fa.expect("session/new"), map[string]any{"sessionId": "s1"})
		fa.expect("session/prompt")
		fa.update("s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Run tests", "kind": "execute"})
		fa.write(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "session/request_permission",
			"params": map[string]any{"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "t1"}, "options": fourOptions}})
		fa.expect("session/cancel")
		replies <- fa.next()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, _ = c.Do(ctx, ports.AgentTask{Prompt: "p", WorkDir: t.TempDir()}, "", func(ports.AgentEvent) {})

	var res permissionResult
	_ = json.Unmarshal((<-replies).Result, &res)
	if res.Outcome.Outcome != "cancelled" {
		t.Errorf("outcome %+v; want cancelled", res.Outcome)
	}
	perm.mu.Lock()
	defer perm.mu.Unlock()
	if !perm.cancelled {
		t.Error("the permission engine's context was never cancelled")
	}
}

func TestOtherAgentRequestsAreRefused(t *testing.T) {
	_, fa := startTestClient(t, testConfig(), map[string]any{}, allowAll{})
	reply := fa.ask(7, "fs/read_text_file", map[string]any{"sessionId": "s1", "path": "/etc/hosts"})
	if reply.Error == nil || reply.Error.Code != -32601 {
		t.Errorf("reply %+v; want -32601", reply)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/acpagent/ -run Permission -v`
Expected: FAIL, every permission request gets `-32601` from the stub.

- [ ] **Step 3: Implement**

Delete `onRequest` from `client.go`. Create `permission.go`:

```go
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
```

An adapter importing `internal/core/app` for `BoundSummary` is allowed, because
dependencies point inward. `tools/judge.go` already imports `app`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/outbound/acpagent/ -race -count=5`
Expected: PASS.

Mutation check: in `onRequest`, delete the `if ctx.Err() != nil` block, and confirm
`TestPendingPermissionIsCancelledWhenTheTurnEnds` fails with a `selected` outcome. Revert.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/acpagent
git commit -m "Put the agent's permission requests to Atlas's engine, and cancel them with the turn"
```

---

### Task 8: The MCP tool server (story 4.4)

**Files:**
- Create: `internal/adapters/inbound/mcpserve/flatten.go`
- Create: `internal/adapters/inbound/mcpserve/server.go`
- Test: `internal/adapters/inbound/mcpserve/flatten_test.go`, `internal/adapters/inbound/mcpserve/server_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `ports.Registry`, `ports.Tool`, `ports.Permission` (Task 1), `app.BoundSummary`.
- Produces:
  - `mcpserve.ServerName` (`"atlas"`) and `mcpserve.Kind` (`"atlas"`), the permission kind of every Atlas tool call
  - `mcpserve.Config{Tools []string; MaxResultBytes, SummaryBytes int; Token string}`
  - `mcpserve.NewHandler(reg ports.Registry, perm ports.Permission, cfg Config) (http.Handler, error)`
  - `mcpserve.NewToken() string`, `mcpserve.AuthHeader(token string) map[string]string`
  - `mcpserve.Listen(addr string, h http.Handler, headerTimeout time.Duration) (*http.Server, string, error)`, which returns the endpoint URL
  - `mcpserve.PublishedName(registryName string) string`

- [ ] **Step 1: Add the dependency and read its API**

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.8.0
go doc github.com/modelcontextprotocol/go-sdk/mcp.Server.AddTool
go doc github.com/modelcontextprotocol/go-sdk/mcp.Tool
go doc github.com/modelcontextprotocol/go-sdk/mcp.CallToolParamsRaw
go doc github.com/modelcontextprotocol/go-sdk/mcp.StreamableHTTPOptions
go doc github.com/modelcontextprotocol/go-sdk/mcp.StreamableClientTransport
```

The code below assumes: `AddTool(*Tool, ToolHandler)`, `Tool.InputSchema` accepting a JSON
object value, `CallToolRequest.Params.Arguments` as `json.RawMessage`, and
`StreamableHTTPOptions{Stateless, JSONResponse bool}`. **If `go doc` shows otherwise, follow
`go doc`** and note the difference in the increment note. Do not add adapters to bridge an
assumed API.

- [ ] **Step 2: Write the failing flatten tests**

`internal/adapters/inbound/mcpserve/flatten_test.go`:

```go
package mcpserve

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFlattenScalars(t *testing.T) {
	got, err := flatten(json.RawMessage(`{"city":"Oslo","days":3,"ratio":0.25,"big":12345678901234567890,"metric":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"city": "Oslo", "days": "3", "ratio": "0.25", "big": "12345678901234567890", "metric": "true"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

func TestFlattenEmpty(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`} {
		got, err := flatten(json.RawMessage(raw))
		if err != nil || len(got) != 0 {
			t.Errorf("flatten(%q) = %v, %v; want empty", raw, got, err)
		}
	}
}

func TestFlattenRejectsWhatHasNoStringForm(t *testing.T) {
	cases := map[string]string{
		`{"where":{"city":"Oslo"}}`: `"where"`,
		`{"days":[1,2]}`:            `"days"`,
		`{"city":null}`:             `"city"`,
		`["Oslo"]`:                  "object",
	}
	for raw, named := range cases {
		_, err := flatten(json.RawMessage(raw))
		if err == nil || !strings.Contains(err.Error(), named) {
			t.Errorf("flatten(%s) err = %v; want one naming %s", raw, err, named)
		}
	}
}
```

- [ ] **Step 3: Implement flatten**

`internal/adapters/inbound/mcpserve/flatten.go`:

```go
package mcpserve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// flatten turns a tool call's JSON arguments into the string map every
// ports.Tool takes. A scalar becomes its string form. A nested value or null
// is an error naming the argument, because it has no string form a tool
// could tell apart.
func flatten(raw json.RawMessage) (map[string]string, error) {
	with := map[string]string{}
	if len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		return with, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	for k, v := range args {
		switch v := v.(type) {
		case string:
			with[k] = v
		case json.Number:
			with[k] = v.String()
		case bool:
			with[k] = strconv.FormatBool(v)
		case nil:
			return nil, fmt.Errorf("argument %q is null", k)
		default:
			return nil, fmt.Errorf("argument %q is nested; only scalar arguments reach a tool", k)
		}
	}
	return with, nil
}
```

Run: `go test ./internal/adapters/inbound/mcpserve/ -run Flatten -v`. Expected: PASS.

- [ ] **Step 4: Write the failing server tests**

`internal/adapters/inbound/mcpserve/server_test.go`:

```go
package mcpserve_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tunedev/atlas/internal/adapters/inbound/mcpserve"
	"github.com/tunedev/atlas/internal/core/ports"
)

type echoTool struct{ got map[string]string }

func (e *echoTool) Name() string { return "weather.get" }
func (e *echoTool) Invoke(_ context.Context, with map[string]string) (any, error) {
	e.got = with
	return map[string]any{"city": with["city"], "temp_c": 11}, nil
}

type registry map[string]ports.Tool

func (r registry) Lookup(name string) (ports.Tool, bool) { t, ok := r[name]; return t, ok }

type fixed struct {
	d     ports.PermissionDecision
	asked []ports.PermissionRequest
}

func (f *fixed) Decide(_ context.Context, req ports.PermissionRequest) (ports.PermissionDecision, error) {
	f.asked = append(f.asked, req)
	return f.d, nil
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func serve(t *testing.T, perm ports.Permission, maxResult int) (*mcp.ClientSession, *echoTool) {
	t.Helper()
	tool := &echoTool{}
	token := mcpserve.NewToken()
	h, err := mcpserve.NewHandler(registry{"weather.get": tool, "hidden.tool": tool}, perm,
		mcpserve.Config{Tools: []string{"weather.get"}, MaxResultBytes: maxResult, SummaryBytes: 200, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, tool
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestOnlyConfiguredToolsArePublished(t *testing.T) {
	session, _ := serve(t, &fixed{d: ports.PermissionAllow}, 1<<20)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "weather_get" {
		t.Errorf("tools %+v; want only weather_get", res.Tools)
	}
}

func TestAgentCallsARegistryToolByItsPublishedName(t *testing.T) {
	perm := &fixed{d: ports.PermissionAllow}
	session, tool := serve(t, perm, 1<<20)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo", "days": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || text(res) != `{"city":"Oslo","temp_c":11}` {
		t.Errorf("result %q (error %v)", text(res), res.IsError)
	}
	if tool.got["days"] != "2" {
		t.Errorf("tool received %v", tool.got)
	}
	if len(perm.asked) != 1 || perm.asked[0].ToolName != "weather.get" || perm.asked[0].Kind != mcpserve.Kind {
		t.Errorf("permission asked %+v; want one request for weather.get of kind %s", perm.asked, mcpserve.Kind)
	}
}

func TestDeniedCallDoesNotRun(t *testing.T) {
	session, tool := serve(t, &fixed{d: ports.PermissionDeny}, 1<<20)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(text(res), "denied") || tool.got != nil {
		t.Errorf("result %q error=%v; tool ran with %v", text(res), res.IsError, tool.got)
	}
}

func TestBadArgumentsAndOversizeResultsAreToolErrors(t *testing.T) {
	session, _ := serve(t, &fixed{d: ports.PermissionAllow}, 10)
	res, _ := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"where": map[string]any{"city": "Oslo"}}})
	if !res.IsError || !strings.Contains(text(res), `"where"`) {
		t.Errorf("nested: %q", text(res))
	}
	res, _ = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "weather_get", Arguments: map[string]any{"city": "Oslo"}})
	if !res.IsError || !strings.Contains(text(res), "10 bytes") {
		t.Errorf("oversize: %q", text(res))
	}
}

func TestRequestsWithoutTheTokenAreRefused(t *testing.T) {
	h, _ := mcpserve.NewHandler(registry{}, &fixed{}, mcpserve.Config{Token: mcpserve.NewToken()})
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d; want 401", resp.StatusCode)
	}
}

func TestConfigErrorsFailAtConstruction(t *testing.T) {
	tool := &echoTool{}
	if _, err := mcpserve.NewHandler(registry{}, &fixed{}, mcpserve.Config{Tools: []string{"missing.tool"}}); err == nil || !strings.Contains(err.Error(), "missing.tool") {
		t.Errorf("missing tool: err = %v", err)
	}
	clash := registry{"a.b": tool, "a_b": tool}
	if _, err := mcpserve.NewHandler(clash, &fixed{}, mcpserve.Config{Tools: []string{"a.b", "a_b"}}); err == nil || !strings.Contains(err.Error(), "a_b") {
		t.Errorf("clash: err = %v", err)
	}
}
```

- [ ] **Step 5: Implement the server**

`internal/adapters/inbound/mcpserve/server.go`:

```go
// Package mcpserve offers a chosen set of Atlas's registry tools to an agent
// as an MCP server over HTTP. The agent drives it, so it is an inbound
// adapter. Every call is put to ports.Permission before the tool runs.
package mcpserve

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// ServerName is the name the agent knows this server by.
const ServerName = "atlas"

// Kind is the permission kind of every call to an Atlas tool.
const Kind = "atlas"

const (
	endpoint = "/mcp"
	version  = "0"
)

// inputSchema accepts any object of scalars: a ports.Tool takes strings,
// and flatten renders numbers and booleans as strings.
var inputSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": map[string]any{"type": []string{"string", "number", "boolean"}},
}

// Config names the registry tools to offer, bounds what goes back to the
// agent, and carries the token every request must present.
type Config struct {
	Tools          []string
	MaxResultBytes int
	SummaryBytes   int
	Token          string
}

// NewHandler builds the MCP endpoint over reg. A configured tool the
// registry lacks, or two tools that publish under the same name, fail here.
func NewHandler(reg ports.Registry, perm ports.Permission, cfg Config) (http.Handler, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: ServerName, Version: version}, nil)
	published := map[string]string{}
	for _, name := range cfg.Tools {
		tool, ok := reg.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("mcpserve: no tool named %q to offer the agent", name)
		}
		pub := PublishedName(name)
		if other, taken := published[pub]; taken {
			return nil, fmt.Errorf("mcpserve: %q and %q both publish as %q", other, name, pub)
		}
		published[pub] = name
		server.AddTool(&mcp.Tool{
			Name:        pub,
			Description: fmt.Sprintf("Atlas tool %s. Pass its arguments as top-level string, number or boolean fields.", name),
			InputSchema: inputSchema,
		}, handle(tool, perm, cfg))
	}

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.Handle(endpoint, requireToken(cfg.Token, h))
	return mux, nil
}

// PublishedName is a registry name as the agent sees it. Model APIs limit
// tool names to letters, digits, underscore and hyphen.
func PublishedName(registryName string) string {
	return strings.ReplaceAll(registryName, ".", "_")
}

func handle(tool ports.Tool, perm ports.Permission, cfg Config) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.Params.Arguments
		with, err := flatten(args)
		if err != nil {
			return failed("%s: %v", tool.Name(), err), nil
		}
		d, err := perm.Decide(ctx, ports.PermissionRequest{
			ToolName: tool.Name(),
			Kind:     Kind,
			Summary:  app.BoundSummary(tool.Name()+" "+string(args), cfg.SummaryBytes),
		})
		if err != nil || d != ports.PermissionAllow {
			return failed("permission denied for %s", tool.Name()), nil
		}
		out, err := tool.Invoke(ctx, with)
		if err != nil {
			return failed("%v", err), nil
		}
		text, err := render(out)
		if err != nil {
			return failed("%s: %v", tool.Name(), err), nil
		}
		if len(text) > cfg.MaxResultBytes {
			return failed("result of %s exceeds %d bytes", tool.Name(), cfg.MaxResultBytes), nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

// render is a tool result as text: a string as itself, anything else as
// JSON.
func render(out any) (string, error) {
	if s, ok := out.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(b), nil
}

// failed is a tool error the agent sees and can correct, not a protocol
// error.
func failed(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewToken is a fresh secret for one run.
func NewToken() string { return rand.Text() }

// AuthHeader is the header the agent must send.
func AuthHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// Listen serves h on addr and returns the server and its MCP endpoint URL.
// The serving goroutine is owned by the returned server and stops at its
// Shutdown or Close.
func Listen(addr string, h http.Handler, headerTimeout time.Duration) (*http.Server, string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("mcpserve: listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: headerTimeout}
	go func() { _ = srv.Serve(ln) }()
	return srv, "http://" + ln.Addr().String() + endpoint, nil
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/adapters/inbound/mcpserve/ ./internal/arch/ -race -v`
Expected: PASS. The arch test proves the core still does not import the SDK.

Mutation check: in `handle`, make the permission check `if err != nil` only (ignore `d`), and
confirm `TestDeniedCallDoesNotRun` fails. Revert.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/adapters/inbound/mcpserve
git commit -m "Offer chosen registry tools to the agent over MCP, each call gated by permission"
```

---

### Task 9: The session pointer

**Files:**
- Create: `internal/core/app/agentsession.go`
- Test: `internal/core/app/agentsession_test.go`

**Interfaces:**
- Consumes: `ports.Docs`. The `fakeDocs` in `internal/core/app/judgementrecord_test.go` (package `app_test`) is reused.
- Produces:
  - `app.AgentSession{SessionID, Command string; Args []string; ProtocolVersion int; CreatedAt time.Time}`
  - `app.RecordAgentSession(ctx, docs ports.Docs, subjectID string, s app.AgentSession) (string, error)`: returns the path
  - `app.LoadAgentSession(ctx, docs ports.Docs, subjectID string) (app.AgentSession, error)`

- [ ] **Step 1: Write the failing tests**

`internal/core/app/agentsession_test.go`:

```go
package app_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
)

func TestAgentSessionRoundTrips(t *testing.T) {
	docs := newFakeDocs()
	s := app.AgentSession{
		SessionID: "sess-1", Command: "/usr/bin/agent", Args: []string{"--quiet"},
		ProtocolVersion: 1, CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC),
	}
	path, err := app.RecordAgentSession(context.Background(), docs, "weather-oslo", s)
	if err != nil {
		t.Fatal(err)
	}
	if path != "agent-sessions/weather-oslo.json" {
		t.Errorf("path %q", path)
	}
	body := string(docs.put[path])
	for _, key := range []string{`"session_id"`, `"command"`, `"args"`, `"protocol_version"`, `"created_at"`} {
		if !strings.Contains(body, key) {
			t.Errorf("document lacks %s:\n%s", key, body)
		}
	}
	got, err := app.LoadAgentSession(context.Background(), docs, "weather-oslo")
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Errorf("loaded %+v, %v; want %+v", got, err, s)
	}
}

func TestAgentSessionRejectsEmptyInputs(t *testing.T) {
	docs := newFakeDocs()
	if _, err := app.RecordAgentSession(context.Background(), docs, "", app.AgentSession{SessionID: "s"}); err == nil {
		t.Error("recorded under an empty subject id")
	}
	if _, err := app.RecordAgentSession(context.Background(), docs, "x", app.AgentSession{}); err == nil {
		t.Error("recorded an empty session id")
	}
	if _, err := app.LoadAgentSession(context.Background(), docs, "never-recorded"); err == nil || !strings.Contains(err.Error(), "never-recorded") {
		t.Errorf("load of a missing pointer: err = %v; want one naming the subject", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/app/ -run AgentSession -v`
Expected: FAIL, `undefined: app.AgentSession`.

- [ ] **Step 3: Implement**

`internal/core/app/agentsession.go`:

```go
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// AgentSession points at a session an agent holds: its id, and which agent
// command produced it, since only that agent can resume it.
type AgentSession struct {
	SessionID       string
	Command         string
	Args            []string
	ProtocolVersion int
	CreatedAt       time.Time
}

// agentSessionDoc is an AgentSession as it is written to the record.
type agentSessionDoc struct {
	SessionID       string    `json:"session_id"`
	Command         string    `json:"command"`
	Args            []string  `json:"args"`
	ProtocolVersion int       `json:"protocol_version"`
	CreatedAt       time.Time `json:"created_at"`
}

// RecordAgentSession writes s as the session for subjectID and returns the
// path written. Recording the same session again is a no-op revision.
func RecordAgentSession(ctx context.Context, docs ports.Docs, subjectID string, s AgentSession) (string, error) {
	if subjectID == "" {
		return "", errors.New("agent session: subject id is empty")
	}
	if s.SessionID == "" {
		return "", errors.New("agent session: session id is empty")
	}
	body, err := json.MarshalIndent(agentSessionDoc(s), "", "  ")
	if err != nil {
		return "", fmt.Errorf("agent session: encode: %w", err)
	}
	path := agentSessionPath(subjectID)
	if _, err := docs.Put(ctx, path, body, "Record agent session for "+subjectID); err != nil {
		return "", fmt.Errorf("agent session: put: %w", err)
	}
	return path, nil
}

// LoadAgentSession reads the session recorded for subjectID.
func LoadAgentSession(ctx context.Context, docs ports.Docs, subjectID string) (AgentSession, error) {
	body, err := docs.Get(ctx, agentSessionPath(subjectID))
	if err != nil {
		return AgentSession{}, fmt.Errorf("agent session: no session recorded for %s: %w", subjectID, err)
	}
	if len(body) == 0 {
		return AgentSession{}, fmt.Errorf("agent session: no session recorded for %s", subjectID)
	}
	var d agentSessionDoc
	if err := json.Unmarshal(body, &d); err != nil {
		return AgentSession{}, fmt.Errorf("agent session: decode %s: %w", subjectID, err)
	}
	return AgentSession(d), nil
}

func agentSessionPath(subjectID string) string {
	return "agent-sessions/" + subjectID + ".json"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/core/... ./internal/arch/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/app/agentsession.go internal/core/app/agentsession_test.go
git commit -m "Keep a pointer to an agent's session in the record, beside the agent that owns it"
```

---

### Task 10: The `agent.do` tool

**Files:**
- Create: `internal/adapters/outbound/tools/agent.go`
- Modify: `internal/adapters/outbound/tools/registry.go` (`With`)
- Test: `internal/adapters/outbound/tools/agent_test.go`, `internal/adapters/outbound/tools/registry_test.go`

**Interfaces:**
- Consumes: `ports.Agent` (Task 1), `app.RecordAgentSession` and `app.LoadAgentSession` (Task 9).
- Produces:
  - `tools.AgentSettings{Command string; Args []string; ProtocolVersion int; WorkDir string; Timeout time.Duration}`
  - `tools.NewAgent(a ports.Agent, docs ports.Docs, progress io.Writer, s AgentSettings) *tools.Agent`, with `Name() == "agent.do"`
  - `(tools.Registry).With(t ports.Tool) tools.Registry`: a copy with `t` added
  - Pack inputs: `prompt` (required), `subject_id` (required), `workdir` (optional, absolute), `resume` (`"true"` to resume)
  - Result: `{"text", "stop_reason", "session_id", "unverified" ([]string), "path"}`

- [ ] **Step 1: Write the failing tests**

`internal/adapters/outbound/tools/agent_test.go`:

```go
package tools_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/gitdocs"
	"github.com/tunedev/atlas/internal/adapters/outbound/tools"
	"github.com/tunedev/atlas/internal/core/ports"
)

type scriptedAgent struct {
	gotTask    ports.AgentTask
	gotSession string
	deadline   bool
}

func (s *scriptedAgent) Do(ctx context.Context, task ports.AgentTask, sessionID string, onEvent func(ports.AgentEvent)) (ports.AgentResult, string, error) {
	s.gotTask, s.gotSession = task, sessionID
	_, s.deadline = ctx.Deadline()
	onEvent(ports.AgentEvent{Kind: ports.AgentEventMessage, Text: "fragment"})
	onEvent(ports.AgentEvent{Kind: ports.AgentEventToolCall, Text: "Read notes.txt [completed]"})
	onEvent(ports.AgentEvent{Kind: ports.AgentEventPlan, Text: "[pending] summarise"})
	sid := sessionID
	if sid == "" {
		sid = "sess-new"
	}
	return ports.AgentResult{Text: "done", StopReason: "end_turn", Unverified: []string{"Write out.txt"}}, sid, nil
}

func newAgentTool(t *testing.T, a ports.Agent, command string) (*tools.Agent, ports.Docs, *bytes.Buffer) {
	t.Helper()
	docs, err := gitdocs.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var progress bytes.Buffer
	return tools.NewAgent(a, docs, &progress, tools.AgentSettings{
		Command: command, Args: []string{"--acp"}, ProtocolVersion: 1, WorkDir: "/work", Timeout: time.Minute,
	}), docs, &progress
}

func TestAgentDoStartsASessionAndRecordsIt(t *testing.T) {
	agent := &scriptedAgent{}
	tool, _, progress := newAgentTool(t, agent, "agent-bin")
	out, err := tool.Invoke(context.Background(), map[string]string{"prompt": "summarise", "subject_id": "notes-1"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["text"] != "done" || res["session_id"] != "sess-new" || res["path"] != "agent-sessions/notes-1.json" {
		t.Errorf("result %v", res)
	}
	if !reflect.DeepEqual(res["unverified"], []string{"Write out.txt"}) {
		t.Errorf("unverified %v", res["unverified"])
	}
	if agent.gotSession != "" || agent.gotTask.WorkDir != "/work" || !agent.deadline {
		t.Errorf("agent got session %q, workdir %q, deadline %v", agent.gotSession, agent.gotTask.WorkDir, agent.deadline)
	}
	want := "agent tool_call: Read notes.txt [completed]\nagent plan: [pending] summarise\n"
	if progress.String() != want {
		t.Errorf("progress %q; want %q", progress.String(), want)
	}
}

func TestAgentDoResumesTheRecordedSession(t *testing.T) {
	agent := &scriptedAgent{}
	tool, _, _ := newAgentTool(t, agent, "agent-bin")
	if _, err := tool.Invoke(context.Background(), map[string]string{"prompt": "a", "subject_id": "notes-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Invoke(context.Background(), map[string]string{"prompt": "b", "subject_id": "notes-1", "resume": "true", "workdir": "/elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if agent.gotSession != "sess-new" || agent.gotTask.WorkDir != "/elsewhere" {
		t.Errorf("resumed with session %q in %q", agent.gotSession, agent.gotTask.WorkDir)
	}
}

func TestAgentDoRefusesAnotherAgentsSession(t *testing.T) {
	first, docs, _ := newAgentTool(t, &scriptedAgent{}, "agent-bin")
	if _, err := first.Invoke(context.Background(), map[string]string{"prompt": "a", "subject_id": "notes-1"}); err != nil {
		t.Fatal(err)
	}
	other := tools.NewAgent(&scriptedAgent{}, docs, &bytes.Buffer{}, tools.AgentSettings{Command: "other-bin", ProtocolVersion: 1, WorkDir: "/work", Timeout: time.Minute})
	_, err := other.Invoke(context.Background(), map[string]string{"prompt": "b", "subject_id": "notes-1", "resume": "true"})
	if err == nil || !strings.Contains(err.Error(), "agent-bin") {
		t.Errorf("err = %v; want one naming the agent that owns the session", err)
	}
}

func TestAgentDoRequiresPromptAndSubject(t *testing.T) {
	tool, _, _ := newAgentTool(t, &scriptedAgent{}, "agent-bin")
	for _, with := range []map[string]string{{"subject_id": "s"}, {"prompt": "p"}} {
		if _, err := tool.Invoke(context.Background(), with); err == nil {
			t.Errorf("Invoke(%v) succeeded", with)
		}
	}
}
```

Add to `internal/adapters/outbound/tools/registry_test.go`:

```go
func TestWithAddsWithoutChangingTheOriginal(t *testing.T) {
	base := tools.NewRegistry(tools.NewHTTP(time.Second, 1))
	extended := base.With(tools.NewModel(nil))
	if _, ok := extended.Lookup("model.complete"); !ok {
		t.Error("With did not add the tool")
	}
	if _, ok := base.Lookup("model.complete"); ok {
		t.Error("With changed the registry it was called on")
	}
}
```

Match the imports that `registry_test.go` already uses. Add `time` if it is missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/outbound/tools/ -run 'AgentDo|With' -v`
Expected: FAIL, `undefined: tools.NewAgent`.

- [ ] **Step 3: Implement**

Add to `registry.go`:

```go
// With returns a copy of r that also holds t. r itself is unchanged, so a
// registry handed out earlier never gains tools behind its holder's back.
func (r Registry) With(t ports.Tool) Registry {
	out := make(Registry, len(r)+1)
	maps.Copy(out, r)
	out[t.Name()] = t
	return out
}
```

(import `maps`).

`internal/adapters/outbound/tools/agent.go`:

```go
package tools

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/tunedev/atlas/internal/core/app"
	"github.com/tunedev/atlas/internal/core/ports"
)

// AgentSettings are the facts about the configured agent that a session
// pointer records, plus the defaults a step may leave out.
type AgentSettings struct {
	Command         string
	Args            []string
	ProtocolVersion int
	WorkDir         string
	Timeout         time.Duration
}

// Agent hands one turn of work to a ports.Agent, prints its tool calls and
// plans as they happen, and records the session it ran in.
type Agent struct {
	agent    ports.Agent
	docs     ports.Docs
	progress io.Writer
	s        AgentSettings
}

func NewAgent(a ports.Agent, docs ports.Docs, progress io.Writer, s AgentSettings) *Agent {
	return &Agent{agent: a, docs: docs, progress: progress, s: s}
}

func (t *Agent) Name() string { return "agent.do" }

func (t *Agent) Invoke(ctx context.Context, with map[string]string) (any, error) {
	subjectID := with["subject_id"]
	if subjectID == "" {
		return nil, fmt.Errorf("agent.do: no subject id")
	}
	if with["prompt"] == "" {
		return nil, fmt.Errorf("agent.do: no prompt")
	}
	workDir := with["workdir"]
	if workDir == "" {
		workDir = t.s.WorkDir
	}

	session := app.AgentSession{Command: t.s.Command, Args: t.s.Args, ProtocolVersion: t.s.ProtocolVersion, CreatedAt: time.Now().UTC()}
	if with["resume"] == "true" {
		prev, err := app.LoadAgentSession(ctx, t.docs, subjectID)
		if err != nil {
			return nil, fmt.Errorf("agent.do: %w", err)
		}
		if prev.Command != t.s.Command || !slices.Equal(prev.Args, t.s.Args) {
			return nil, fmt.Errorf("agent.do: session for %s belongs to %s %v, not the configured %s %v",
				subjectID, prev.Command, prev.Args, t.s.Command, t.s.Args)
		}
		session = prev
	}

	ctx, cancel := context.WithTimeout(ctx, t.s.Timeout)
	defer cancel()
	res, sessionID, err := t.agent.Do(ctx, ports.AgentTask{Prompt: with["prompt"], WorkDir: workDir}, session.SessionID, t.report)
	if err != nil {
		return nil, fmt.Errorf("agent.do: %w", err)
	}

	session.SessionID = sessionID
	path, err := app.RecordAgentSession(ctx, t.docs, subjectID, session)
	if err != nil {
		return nil, fmt.Errorf("agent.do: %w", err)
	}
	return map[string]any{
		"text":        res.Text,
		"stop_reason": res.StopReason,
		"session_id":  sessionID,
		"unverified":  res.Unverified,
		"path":        path,
	}, nil
}

// report prints tool calls and plans as they happen. Message text arrives
// in fragments and is returned whole in the result instead.
func (t *Agent) report(ev ports.AgentEvent) {
	if ev.Kind == ports.AgentEventMessage {
		return
	}
	fmt.Fprintf(t.progress, "agent %s: %s\n", ev.Kind, ev.Text)
}
```

If `Do` fails after a session was opened, no pointer is written. A failed turn can only be
retried as a new session. The note records this (see Task 13).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/outbound/tools/ ./internal/arch/ -race`
Expected: PASS. The vocabulary test runs over the new fixtures too.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/outbound/tools
git commit -m "Put a turn of agent work behind a pack step, resumable from its recorded session"
```

---

### Task 11: Config

**Files:**
- Modify: `internal/config/config.go`, `internal/config/layers.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `config.Config.Agent` of type `config.AgentConfig{Command string; Args []string; WorkDir string; Tools []string; MCPAddr string; StartTimeout, TurnTimeout, CloseTimeout, MCPHeaderTimeout time.Duration; MaxMessageBytes, MaxToolResultBytes int}`
  - `config.Config.Permission` of type `config.PermissionConfig{Rules []config.PermissionRule; SummaryBytes int}`
  - `config.PermissionRule{ToolName, Kind, Decision string}`
  - `config.AgentEnv(environ []string) []string`
  - Env: `ATLAS_AGENT_COMMAND`, `ATLAS_AGENT_ARGS` (space-separated), `ATLAS_AGENT_WORKDIR`,
    `ATLAS_AGENT_TOOLS` (comma-separated), `ATLAS_AGENT_MCP_ADDR`, `ATLAS_AGENT_START_TIMEOUT`,
    `ATLAS_AGENT_TURN_TIMEOUT`, `ATLAS_AGENT_CLOSE_TIMEOUT`, `ATLAS_AGENT_MCP_HEADER_TIMEOUT`,
    `ATLAS_AGENT_MAX_MESSAGE_BYTES`, `ATLAS_AGENT_MAX_TOOL_RESULT_BYTES`,
    `ATLAS_PERMISSION_RULES` (`tool:kind:decision,...`), `ATLAS_PERMISSION_SUMMARY_BYTES`
  - Flags: `-agent-command`, `-agent-args`, `-agent-workdir`, `-agent-tools`,
    `-agent-turn-timeout`, `-permission-rules`

Defaults: `MCPAddr "127.0.0.1:0"`, `StartTimeout 60s`, `TurnTimeout 30m`, `CloseTimeout 10s`,
`MCPHeaderTimeout 10s`, `MaxMessageBytes 16 MiB`, `MaxToolResultBytes 1 MiB`,
`SummaryBytes 200`. `WorkDir` empty resolves to `os.Getwd()` at load, and `~` expands like the
store paths.

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go`. Follow its existing style for setting env (`t.Setenv`)
and building args:

```go
func TestAgentIsOffWithoutACommand(t *testing.T) {
	cfg, err := config.Load([]string{"-pack", "p.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Command != "" {
		t.Errorf("agent command %q; want none by default", cfg.Agent.Command)
	}
	wd, _ := os.Getwd()
	if cfg.Agent.WorkDir != wd {
		t.Errorf("workdir %q; want the process's %q", cfg.Agent.WorkDir, wd)
	}
}

func TestAgentConfigFromEnvAndFlags(t *testing.T) {
	t.Setenv("ATLAS_AGENT_COMMAND", "agent-bin")
	t.Setenv("ATLAS_AGENT_ARGS", "--acp  --quiet")
	t.Setenv("ATLAS_AGENT_TOOLS", "http.request, judge.ask")
	t.Setenv("ATLAS_PERMISSION_RULES", "http.request:atlas:allow, *:execute:ask,*:*:deny")
	cfg, err := config.Load([]string{"-pack", "p.yaml", "-agent-turn-timeout", "2m", "-agent-workdir", "/srv/work"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Agent.Args, []string{"--acp", "--quiet"}) ||
		!reflect.DeepEqual(cfg.Agent.Tools, []string{"http.request", "judge.ask"}) ||
		cfg.Agent.TurnTimeout != 2*time.Minute || cfg.Agent.WorkDir != "/srv/work" {
		t.Errorf("agent config %+v", cfg.Agent)
	}
	want := []config.PermissionRule{
		{ToolName: "http.request", Kind: "atlas", Decision: "allow"},
		{ToolName: "*", Kind: "execute", Decision: "ask"},
		{ToolName: "*", Kind: "*", Decision: "deny"},
	}
	if !reflect.DeepEqual(cfg.Permission.Rules, want) {
		t.Errorf("rules %+v", cfg.Permission.Rules)
	}
}

func TestBadAgentConfigFailsAtLoad(t *testing.T) {
	cases := map[string]map[string]string{
		"bad decision":      {"ATLAS_PERMISSION_RULES": "*:*:maybe"},
		"short rule":        {"ATLAS_PERMISSION_RULES": "*:allow"},
		"relative workdir":  {"ATLAS_AGENT_COMMAND": "a", "ATLAS_AGENT_WORKDIR": "work"},
		"zero turn timeout": {"ATLAS_AGENT_COMMAND": "a", "ATLAS_AGENT_TURN_TIMEOUT": "0s"},
		"tools, no addr":    {"ATLAS_AGENT_COMMAND": "a", "ATLAS_AGENT_TOOLS": "x", "ATLAS_AGENT_MCP_ADDR": " "},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := config.Load([]string{"-pack", "p.yaml"}); err == nil {
				t.Error("loaded")
			}
		})
	}
}

func TestAgentEnvDropsAtlasVariables(t *testing.T) {
	got := config.AgentEnv([]string{"HOME=/home/u", "ATLAS_MODEL_API_KEY=secret", "PATH=/bin", "ATLAS_PACK=p"})
	if !reflect.DeepEqual(got, []string{"HOME=/home/u", "PATH=/bin"}) {
		t.Errorf("env %v", got)
	}
}
```

For the "tools, no addr" case, `strings.TrimSpace` the address when reading it from env, so a
blank value counts as empty.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -v`
Expected: FAIL, `cfg.Agent undefined`.

- [ ] **Step 3: Implement**

In `config.go`, add `Agent AgentConfig` and `Permission PermissionConfig` to `Config`, then:

```go
// AgentConfig configures the one coding agent atlas can drive. The agent is
// enabled only when Command is set. Tools names the registry tools offered
// to it. WorkDir is the absolute default directory it works in.
type AgentConfig struct {
	Command            string
	Args               []string
	WorkDir            string
	Tools              []string
	MCPAddr            string
	StartTimeout       time.Duration
	TurnTimeout        time.Duration
	CloseTimeout       time.Duration
	MCPHeaderTimeout   time.Duration
	MaxMessageBytes    int
	MaxToolResultBytes int
}

// PermissionConfig holds the rules that decide an agent's tool calls, first
// match wins, and bounds the summary a human is shown.
type PermissionConfig struct {
	Rules        []PermissionRule
	SummaryBytes int
}

// PermissionRule matches a tool name and kind, either of which may be "*".
type PermissionRule struct {
	ToolName string
	Kind     string
	Decision string
}

// AgentEnv is environ without atlas's own variables, so no atlas secret
// reaches the agent process.
func AgentEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "ATLAS_") {
			out = append(out, kv)
		}
	}
	return out
}
```

Add to `validate()`:

```go
	for _, r := range c.Permission.Rules {
		switch r.Decision {
		case "allow", "ask", "deny":
		default:
			return fmt.Errorf("config: permission rule %s:%s has unknown decision %q", r.ToolName, r.Kind, r.Decision)
		}
	}
	if c.Permission.SummaryBytes <= 0 {
		return fmt.Errorf("config: permission summary bytes must be positive, got %d", c.Permission.SummaryBytes)
	}
	if c.Agent.Command != "" {
		if err := c.Agent.validate(); err != nil {
			return err
		}
	}
```

and:

```go
func (a AgentConfig) validate() error {
	if !filepath.IsAbs(a.WorkDir) {
		return fmt.Errorf("config: agent workdir must be absolute, got %q", a.WorkDir)
	}
	for name, d := range map[string]time.Duration{
		"start timeout": a.StartTimeout, "turn timeout": a.TurnTimeout,
		"close timeout": a.CloseTimeout, "mcp header timeout": a.MCPHeaderTimeout,
	} {
		if d <= 0 {
			return fmt.Errorf("config: agent %s must be positive, got %s", name, d)
		}
	}
	if a.MaxMessageBytes <= 0 || a.MaxToolResultBytes <= 0 {
		return fmt.Errorf("config: agent max message and tool result bytes must be positive")
	}
	if len(a.Tools) > 0 && a.MCPAddr == "" {
		return fmt.Errorf("config: agent tools are configured but the MCP address is empty")
	}
	return nil
}
```

In `layers.go`:
- Add the defaults listed above to `defaults()`, with `Permission: PermissionConfig{SummaryBytes: 200}`.
- In `applyEnv`, add one block per variable in the existing style. Use `strings.Fields` for
  args, `splitList` for tools, and `parseRules` for rules.
- In `applyFlags`, add the six flags. Use `fs.StringVar` for command and workdir, and
  `fs.DurationVar` for the turn timeout. For args, tools and rules, use `fs.Func`, each parsing
  with the same helper as its env variable.
- In `Load`, after `expandStorePaths`, call `resolveWorkDir(&cfg)`. It expands `~` with
  `expandHome`, and replaces an empty value with `os.Getwd()`.

```go
// splitList splits a comma-separated list, trimming space and dropping
// empty entries.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseRules reads "tool:kind:decision" entries from a comma-separated list.
func parseRules(s string) ([]PermissionRule, error) {
	var rules []PermissionRule
	for _, entry := range splitList(s) {
		parts := strings.Split(entry, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("config: permission rule %q is not tool:kind:decision", entry)
		}
		rules = append(rules, PermissionRule{ToolName: parts[0], Kind: parts[1], Decision: parts[2]})
	}
	return rules, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -race -v`
Expected: PASS, the existing tests included.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "Configure the agent, the tools it may reach, and the rules that gate it"
```

---

### Task 12: The composition root

**Files:**
- Modify: `cmd/atlas/main.go`
- Test: `cmd/atlas/main_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `startAgent(ctx, cfg config.Config, base ports.Registry, docs ports.Docs) (ports.Tool, func() error, error)`, which returns the `agent.do` tool and a stop function.

- [ ] **Step 1: Write the failing tests**

In `cmd/atlas/main_test.go`, generalize `TestBuildRegistryPassesNoLiterals` to walk both
`buildRegistry` and `startAgent`. Keep it a single test over a name list, so a third function
is one more string:

```go
func TestCompositionPassesNoLiterals(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, name := range []string{"buildRegistry", "startAgent"} {
		found := false
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			found = true
			ast.Inspect(fn, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok {
					t.Errorf("%s contains the literal %s; every value must come from config", name, lit.Value)
				}
				return true
			})
		}
		if !found {
			t.Errorf("%s not found in main.go", name)
		}
	}
}
```

Delete `TestBuildRegistryPassesNoLiterals`, which this replaces. Add:

```go
func TestStartAgentFailsLoudlyForAMissingCommand(t *testing.T) {
	docs, index := testStore(t)
	cfg := config.Config{}
	cfg.Agent.Command = filepath.Join(t.TempDir(), "no-such-agent")
	cfg.Agent.Tools = []string{"http.request"}
	cfg.Agent.MCPAddr = "127.0.0.1:0"
	cfg.Agent.StartTimeout = time.Second
	cfg.Agent.MCPHeaderTimeout = time.Second
	cfg.Agent.MaxMessageBytes = 1 << 20
	cfg.Agent.MaxToolResultBytes = 1 << 20
	cfg.Permission.SummaryBytes = 200

	_, _, err := startAgent(context.Background(), cfg, buildRegistry(cfg, docs, index), docs)
	if err == nil || !strings.Contains(err.Error(), "no-such-agent") {
		t.Errorf("err = %v; want one naming the command", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/atlas/ -v`
Expected: FAIL, `undefined: startAgent`.

- [ ] **Step 3: Implement**

In `main.go`, add:

```go
// startAgent launches the configured agent, offers it the configured tools
// from base over MCP, and returns the agent.do tool with a function that
// stops both. base is the registry without agent.do, so the agent cannot
// reach itself.
func startAgent(ctx context.Context, cfg config.Config, base ports.Registry, docs ports.Docs) (ports.Tool, func() error, error) {
	perm := app.NewPermissionPolicy(permissionRules(cfg.Permission.Rules), termprompt.New(os.Stdin, os.Stderr))

	var srv *http.Server
	var server *acpagent.MCPServer
	if len(cfg.Agent.Tools) > 0 {
		token := mcpserve.NewToken()
		handler, err := mcpserve.NewHandler(base, perm, mcpserve.Config{
			Tools:          cfg.Agent.Tools,
			MaxResultBytes: cfg.Agent.MaxToolResultBytes,
			SummaryBytes:   cfg.Permission.SummaryBytes,
			Token:          token,
		})
		if err != nil {
			return nil, nil, err
		}
		var url string
		srv, url, err = mcpserve.Listen(cfg.Agent.MCPAddr, handler, cfg.Agent.MCPHeaderTimeout)
		if err != nil {
			return nil, nil, err
		}
		server = &acpagent.MCPServer{Name: mcpserve.ServerName, URL: url, Headers: mcpserve.AuthHeader(token)}
	}

	startCtx, cancel := context.WithTimeout(ctx, cfg.Agent.StartTimeout)
	defer cancel()
	client, err := acpagent.New(startCtx, acpagent.Config{
		Command:         cfg.Agent.Command,
		Args:            cfg.Agent.Args,
		Env:             config.AgentEnv(os.Environ()),
		Stderr:          os.Stderr,
		MaxMessageBytes: cfg.Agent.MaxMessageBytes,
		SummaryBytes:    cfg.Permission.SummaryBytes,
		MCP:             server,
	}, perm)
	if err != nil {
		if srv != nil {
			_ = srv.Close()
		}
		return nil, nil, err
	}

	stop := func() error {
		stopCtx, cancel := context.WithTimeout(context.Background(), cfg.Agent.CloseTimeout)
		defer cancel()
		err := client.Close(stopCtx)
		if srv != nil {
			err = errors.Join(err, srv.Shutdown(stopCtx))
		}
		return err
	}
	tool := tools.NewAgent(client, docs, os.Stderr, tools.AgentSettings{
		Command:         cfg.Agent.Command,
		Args:            cfg.Agent.Args,
		ProtocolVersion: client.ProtocolVersion(),
		WorkDir:         cfg.Agent.WorkDir,
		Timeout:         cfg.Agent.TurnTimeout,
	})
	return tool, stop, nil
}

// permissionRules converts configured rules into the policy's own type.
func permissionRules(rules []config.PermissionRule) []app.PermissionRule {
	out := make([]app.PermissionRule, len(rules))
	for i, r := range rules {
		out[i] = app.PermissionRule{ToolName: r.ToolName, Kind: r.Kind, Decision: ports.PermissionDecision(r.Decision)}
	}
	return out
}
```

`stop` returns its error rather than printing it, because printing needs a format literal and
`TestCompositionPassesNoLiterals` would flag it. `run` prints it instead.

In `run()`, after `registry := buildRegistry(...)`:

```go
	if cfg.Agent.Command != "" {
		agentTool, stop, err := startAgent(ctx, cfg, registry, docs)
		if err != nil {
			return err
		}
		defer func() {
			if err := stop(); err != nil {
				fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
			}
		}()
		registry = registry.With(agentTool)
	}
```

- [ ] **Step 4: Run everything**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
```

Expected: the build is clean, `gofmt -l` lists nothing, every test passes, and
`hn-summary` runs exactly as before, with no agent started because no command is configured.

- [ ] **Step 5: Commit**

```bash
git add cmd/atlas
git commit -m "Start the agent at startup when one is configured, and register agent.do"
```

---

### Task 13: A real agent completes a real pack (story 4.7), and the note

**Files:**
- Create: `packs/agent-hn.yaml`
- Create: `docs/notes/2026-09-24-increment-4.md`
- Modify: `docs/plans/2026-09-17-roadmap.md` (story 4.7's acceptance text, per R23)

**Interfaces:**
- Consumes: the whole increment.
- Produces: pasted evidence.

- [ ] **Step 1: Write the pack**

`packs/agent-hn.yaml`:

```yaml
name: agent-hn
vars:
  item: "8863"

steps:
  - id: brief
    tool: agent.do
    with:
      subject_id: "agent-hn-{{ .vars.item }}"
      prompt: |
        Call the atlas tool http_request with the argument
        url=https://hacker-news.firebaseio.com/v0/item/{{ .vars.item }}.json
        to fetch one Hacker News story. Then create a file named
        story-{{ .vars.item }}.md in the current working directory holding the
        story's title, its author, and one plain sentence saying what it is about.
        Reply with the file name and nothing else.

  - id: check
    tool: agent.do
    with:
      subject_id: "agent-hn-{{ .vars.item }}"
      resume: "true"
      prompt: |
        Read back the file you just wrote and reply with its first line only.
```

- [ ] **Step 2: Check the agent is installed. Stop if it is not**

Run: `command -v claude-agent-acp`

If it prints nothing, **stop**. Report to the human, and do not attempt the install:

> `claude-agent-acp` is not installed. Please run
> `! npm install -g @agentclientprotocol/claude-agent-acp`
> (it needs Node.js and your existing Claude Code login), then tell me to continue.

- [ ] **Step 3: Run it for real, answering prompts on the terminal**

Atlas's terminal prompt reads stdin, so this must run in the human's terminal session. Ask
the human to run it with the `!` prefix, or run it yourself only if the session can answer
prompts:

```bash
WORK=$(mktemp -d)
ATLAS_AGENT_COMMAND=claude-agent-acp \
ATLAS_AGENT_TOOLS=http.request \
ATLAS_AGENT_WORKDIR=$WORK \
ATLAS_PERMISSION_RULES='*:read:allow' \
go run ./cmd/atlas -pack packs/agent-hn.yaml
ls -l $WORK && cat $WORK/story-8863.md
git -C ~/.atlas/workspace log --oneline -3 -- agent-sessions/
cat ~/.atlas/workspace/agent-sessions/agent-hn-8863.json
```

Expected:
- At least one `atlas: the agent wants to run http.request (atlas)` prompt, answered `y`.
- At least one prompt for the file write (kind `edit`), answered `y`.
- `agent tool_call: ... [completed]` progress lines.
- A JSON result whose `brief.text` names the file and whose `check.text` is its first line,
  with `check.session_id` equal to `brief.session_id`.
- The file on disk and the session pointer committed.

Then run once more and answer `n` to the `http.request` prompt. Paste the evidence that the
agent reported the denial rather than fetching.

- [ ] **Step 4: Amend the roadmap**

In `docs/plans/2026-09-17-roadmap.md`, change story 4.7's acceptance to:

`| 4.7 A real agent completes a real pack | The configured agent (Claude Code via claude-agent-acp), end to end, evidence pasted |`

- [ ] **Step 5: Write the note**

`docs/notes/2026-09-24-increment-4.md`, in the shape of the earlier notes: what the pattern
was, what surprised you, what you would do differently, and what you still do not understand.
It must contain:
- The pasted transcript from Step 3, both the allowed run and the denied run.
- R1 stated plainly: the spec's tool bridge assumed a client executes an agent's tool calls,
  ACP does not work that way, and MCP became the only path.
- Any place where `go doc` for the MCP SDK differed from Task 8's assumptions.
- The known limits: a failed turn leaves no pointer (Task 10), a double prompt appears when an
  Atlas tool's rule is `ask` (R5), and the agent's own built-in calls that its settings allow
  never reach Atlas (R5).
- Whether `session/load` in `claude-agent-acp` replayed tool calls with final statuses, as
  observed in the `check` step's `unverified`.

- [ ] **Step 6: Run the done checks**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
```

- [ ] **Step 7: Commit**

```bash
git add packs/agent-hn.yaml docs/notes/2026-09-24-increment-4.md docs/plans/2026-09-17-roadmap.md
git commit -m "Drive Claude Code through a real pack over ACP, and record what it taught"
```

---

## Deliberately not in this increment

| Out | Why |
|---|---|
| The `suspend` permission outcome | As the spec says: epic 4.6 is the seam, and occupying it needs a human surface that only epic 12 brings |
| Persisting "always allow" or "always deny" | Epic 10.3 promotes a decision into a rule, deliberately |
| Nested or structured tool arguments | No shipped tool needs them (R21) |
| `sessionCapabilities.resume` | Not universally implemented. `session/load` is the interoperable path |
| Per-tool input schemas and descriptions on MCP | `ports.Tool` does not describe its arguments. The pack's prompt tells the agent how to call a tool. Revisit when a second pack needs it |
| ACP `authenticate` | R22: the agent uses its own existing login |
| Advertising `fs` or `terminal` to the agent | ACP v2 removes them. MCP is the one way in (R1, R15) |
| Suppressing the double prompt for Atlas tools | Correlating an agent's permission request with the MCP call that follows it needs a vendor-specific name convention (R5) |
| Gemini CLI or Codex verified end to end | Only Claude Code is run. The adapter is config-driven, so either one is a config change to try |
