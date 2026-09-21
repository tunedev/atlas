# Increment 2 — The Provider port — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put a port between the tool that asks a model a question and the engine that answers, so which engine answers is a config line — and carry enough information through it that a later increment can read a calibrated probability.

**Architecture:** A `Provider` port owned by the core, taking a `Prompt` and returning a `Completion` that carries text, usage, latency and optional per-token log probabilities. One adapter speaks the OpenAI chat-completions shape; Ollama and vLLM differ only by base URL. An ordered chain wraps several providers with a breaker each, so a failing engine degrades a feature instead of a path. `model.complete` stops knowing which vendor answered.

**Tech Stack:** Go 1.27, the standard library, Ollama on `http://localhost:11434/v1` (present and serving), vLLM later on `http://localhost:8000/v1`.

**Spec:** `docs/specs/2026-09-17-job-hunt-harness-design.md`

**Roadmap:** `docs/plans/2026-09-17-roadmap.md` (Epic 2, stories 2.1-2.6)

**Design reference:** `docs/design/the-record.md` for the conventions this codebase already holds.

## Global Constraints

- Go 1.27. Module `github.com/tunedev/atlas`.
- **Nothing in the Go tree knows what a job posting is.** No type, field, prompt, URL or string constant naming a use-case concept outside `packs/`. `internal/arch/vocabulary_test.go` enforces it, test fixtures included.
- **No core package imports an adapter or a driver.** `internal/core/...` compiles in neither `net/http` nor `crypto/tls`. `internal/arch/arch_test.go` enforces both.
- **No adapter type in a port signature.** No `*http.Client`, no `*http.Response`, no vendor-shaped struct. A JSON schema crossing the port is a `[]byte` of standard JSON Schema, which is a standard rather than a vendor type.
- A port exists only where a second implementation is nameable. `Provider` has two: Ollama and vLLM, both OpenAI-compatible, plus a hosted key.
- `ctx context.Context` first parameter of every blocking or remote call. Never stored in a struct.
- Every remote call has a timeout. Every response body is bounded.
- Every wrapped error carries its component prefix, as `gitdocs: `, `sqlindex: ` and `duckindex: ` already do.
- Nothing operationally interesting is hardcoded past `config.defaults()`.
- No emojis. Comments describe current behaviour only — no history, no dates, no narrative about what changed.
- Tests assert behaviour. A test that cannot fail is a defect. Prove a guard by mutation, and **confirm the mutation applied** before reading anything into the result.
- The three `cmd/atlas` guards hold: no `defer` in `main()`, `os.Exit` only in `main()`, no literal in `buildRegistry`.

## What "done" means

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
go run ./cmd/atlas -pack packs/job-hunt.yaml
```

Both packs still run unaided. Then the increment's own claim, with pasted output:

> The same pack runs against two different engines with only `-model-base-url` changed.

## Measured facts this plan depends on

Both were measured against the running Ollama, not assumed. Re-measure if anything surprises you.

**Ollama returns logprobs through its OpenAI endpoint**, including `top_logprobs`, and returns them while a JSON schema constrains the output. So logprobs are not a reason to prefer one engine.

**Those logprobs are the model's pre-constraint distribution.** On a schema-constrained yes/no question:

```
token='{'    p=0.0000   alts={'Yes': 0.9589, 'The': 0.0236, 'No': 0.0069}
token='yes'  p=0.2660   alts={'Yes': 0.6719, 'yes': 0.2660, 'no': 0.0124}
```

The schema forced `{` over the prose "Yes" the model wanted. At the answer-bearing token the same answer is split across `yes` and `Yes`. Reading the chosen token's logprob reports 27% where the model is near 94%. **Task 5 exists because of this.**

## File Structure

| File | Responsibility |
|---|---|
| `internal/core/ports/provider.go` | `Provider`, `Prompt`, `Completion`, `Token`, `Alternative`, `Usage` |
| `internal/adapters/outbound/openaiprov/client.go` | One adapter for any OpenAI-compatible endpoint |
| `internal/adapters/outbound/openaiprov/wire.go` | The vendor-shaped request and response structs, unexported |
| `internal/core/app/chain.go` | Ordered `Provider` chain with a breaker per provider |
| `internal/core/app/breaker.go` | The breaker itself |
| `internal/core/app/mass.go` | Probability mass per answer class, across surface forms |
| `internal/adapters/outbound/tools/model.go` | `model.complete`, rewritten to take a `Provider` |
| `docs/notes/2026-09-21-increment-2.md` | The increment note |

---

### Task 1: The `Provider` port

**Files:**
- Create: `internal/core/ports/provider.go`
- Test: `internal/core/ports/provider_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ports.Provider` with `Name() string` and `Complete(ctx context.Context, p Prompt) (Completion, error)`; `ports.Prompt{System, User string, MaxTokens int, Schema []byte, TopLogProbs int}`; `ports.Completion{Text, Model string, Tokens []Token, Usage Usage, Latency time.Duration}`; `ports.Token{Text string, LogProb float64, Alternatives []Alternative}`; `ports.Alternative{Text string, LogProb float64}`; `ports.Usage{PromptTokens, CompletionTokens int}`.

- [ ] **Step 1: Write the failing test**

The port is types and one interface, so the test pins the shape a later task depends on rather than behaviour that does not exist yet.

Create `internal/core/ports/provider_test.go`:

```go
package ports_test

import (
	"context"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/core/ports"
)

// stubProvider exists to prove the interface is implementable from outside the
// package with no adapter type in any signature.
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }

func (stubProvider) Complete(_ context.Context, p ports.Prompt) (ports.Completion, error) {
	return ports.Completion{
		Text:    "answer to " + p.User,
		Model:   "stub-model",
		Latency: time.Millisecond,
		Usage:   ports.Usage{PromptTokens: 3, CompletionTokens: 4},
		Tokens: []ports.Token{{
			Text:    "yes",
			LogProb: -1.3,
			Alternatives: []ports.Alternative{
				{Text: "Yes", LogProb: -0.4},
				{Text: "no", LogProb: -4.4},
			},
		}},
	}, nil
}

func TestAProviderCanBeImplementedOutsideTheCore(t *testing.T) {
	var p ports.Provider = stubProvider{}

	got, err := p.Complete(context.Background(), ports.Prompt{
		System:      "be terse",
		User:        "a question",
		MaxTokens:   8,
		TopLogProbs: 3,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got.Model == "" {
		t.Error("Completion does not record which model answered")
	}
	if got.Usage.CompletionTokens == 0 {
		t.Error("Completion does not carry usage")
	}
	if got.Latency == 0 {
		t.Error("Completion does not carry latency")
	}
	if len(got.Tokens) != 1 || len(got.Tokens[0].Alternatives) != 2 {
		t.Fatalf("Completion does not carry per-token alternatives: %+v", got.Tokens)
	}
	if got.Tokens[0].Alternatives[0].Text != "Yes" {
		t.Errorf("alternative text = %q", got.Tokens[0].Alternatives[0].Text)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/core/ports/ -v`
Expected: FAIL — `undefined: ports.Provider`.

- [ ] **Step 3: Write the port**

Create `internal/core/ports/provider.go`:

```go
package ports

import (
	"context"
	"time"
)

// Alternative is one token the model could have produced at a position, with
// the log probability it assigned.
type Alternative struct {
	Text    string
	LogProb float64
}

// Token is one position in a completion. Alternatives is empty unless the
// caller asked for them.
//
// LogProb and Alternatives describe the model's distribution before any schema
// constrained the output, so the token actually produced is not always the one
// with the highest probability here.
type Token struct {
	Text         string
	LogProb      float64
	Alternatives []Alternative
}

// Usage is what the call cost, in tokens.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Prompt is one question for a model. Schema, when set, is a JSON Schema the
// answer must satisfy. TopLogProbs, when above zero, asks for that many
// alternatives per token.
type Prompt struct {
	System      string
	User        string
	MaxTokens   int
	Schema      []byte
	TopLogProbs int
}

// Completion is what a provider answered, and what it cost.
type Completion struct {
	Text    string
	Model   string
	Tokens  []Token
	Usage   Usage
	Latency time.Duration
}

// Provider turns a prompt into a completion. The caller does not know which
// vendor answered; Completion.Model records it.
type Provider interface {
	Name() string
	Complete(ctx context.Context, p Prompt) (Completion, error)
}
```

- [ ] **Step 4: Run it and watch it pass**

Run: `go test ./internal/core/ports/ -v`
Expected: PASS.

- [ ] **Step 5: Confirm the core stayed clean**

Run: `go test ./internal/arch/ -v`
Expected: PASS. `internal/core/ports` must still import only the standard library.

- [ ] **Step 6: Commit**

```bash
git add internal/core/ports/provider.go internal/core/ports/provider_test.go
git commit -m "Put a port between the tool that asks and the engine that answers"
```

---

### Task 2: One adapter for any OpenAI-compatible engine

**Files:**
- Create: `internal/adapters/outbound/openaiprov/client.go`, `internal/adapters/outbound/openaiprov/wire.go`
- Test: `internal/adapters/outbound/openaiprov/client_test.go`

**Interfaces:**
- Consumes: `ports.Provider`, `ports.Prompt`, `ports.Completion`, `ports.Token`, `ports.Alternative`, `ports.Usage`.
- Produces: `openaiprov.New(cfg openaiprov.Config) *openaiprov.Client` implementing `ports.Provider`; `openaiprov.Config{Name, BaseURL, Model, APIKey string, Timeout time.Duration, MaxBytes int64}`.

- [ ] **Step 1: Write the failing test**

Every test serves a recorded response from `httptest`. No test may require a live model.

Create `internal/adapters/outbound/openaiprov/client_test.go`:

```go
package openaiprov_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tunedev/atlas/internal/adapters/outbound/openaiprov"
	"github.com/tunedev/atlas/internal/core/ports"
)

// recorded is a real response shape, trimmed, as returned by an
// OpenAI-compatible endpoint when top_logprobs is requested.
const recorded = `{
 "model": "a-model",
 "usage": {"prompt_tokens": 11, "completion_tokens": 2},
 "choices": [{
   "message": {"content": "{\"answer\": \"yes\"}"},
   "logprobs": {"content": [
     {"token": "yes", "logprob": -1.3244,
      "top_logprobs": [
        {"token": "Yes", "logprob": -0.3975},
        {"token": "yes", "logprob": -1.3244},
        {"token": "no",  "logprob": -4.3892}]}
   ]}
 }]
}`

func serve(t *testing.T, status int, body string, capture *http.Request) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			*capture = *r.Clone(r.Context())
			b, _ := io.ReadAll(r.Body)
			capture.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func client(t *testing.T, base string) *openaiprov.Client {
	t.Helper()
	return openaiprov.New(openaiprov.Config{
		Name: "test", BaseURL: base, Model: "a-model",
		Timeout: 5 * time.Second, MaxBytes: 1 << 20,
	})
}

func TestCompletionCarriesTextModelAndUsage(t *testing.T) {
	s := serve(t, http.StatusOK, recorded, nil)
	got, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q"})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(got.Text, `"yes"`) {
		t.Errorf("text = %q", got.Text)
	}
	if got.Model != "a-model" {
		t.Errorf("model = %q, want a-model", got.Model)
	}
	if got.Usage.PromptTokens != 11 || got.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", got.Usage)
	}
	if got.Latency <= 0 {
		t.Error("latency was not measured")
	}
}

func TestAlternativesSurviveTheBoundary(t *testing.T) {
	s := serve(t, http.StatusOK, recorded, nil)
	got, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q", TopLogProbs: 3})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(got.Tokens) != 1 {
		t.Fatalf("tokens = %d, want 1", len(got.Tokens))
	}
	tok := got.Tokens[0]
	if tok.Text != "yes" {
		t.Errorf("token text = %q", tok.Text)
	}
	if len(tok.Alternatives) != 3 {
		t.Fatalf("alternatives = %d, want 3", len(tok.Alternatives))
	}
	// The chosen token is not the most probable one. That is the whole reason
	// alternatives cross the boundary.
	if math.Exp(tok.Alternatives[0].LogProb) <= math.Exp(tok.LogProb) {
		t.Errorf("expected a more probable alternative than the chosen token: %+v", tok)
	}
}

func TestTopLogProbsIsOnlyRequestedWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
		n    int
	}{{"absent", false, 0}, {"present", true, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			var captured http.Request
			s := serve(t, http.StatusOK, recorded, &captured)
			_, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q", TopLogProbs: tc.n})
			if err != nil {
				t.Fatalf("complete: %v", err)
			}
			body, _ := io.ReadAll(captured.Body)
			var sent map[string]any
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Fatalf("unmarshal request: %v", err)
			}
			_, has := sent["top_logprobs"]
			if has != tc.want {
				t.Errorf("top_logprobs present = %v, want %v; request was %s", has, tc.want, body)
			}
		})
	}
}

func TestASchemaIsSentWhenSet(t *testing.T) {
	var captured http.Request
	s := serve(t, http.StatusOK, recorded, &captured)
	schema := []byte(`{"type":"object","properties":{"answer":{"type":"string"}}}`)
	_, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q", Schema: schema})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	body, _ := io.ReadAll(captured.Body)
	if !strings.Contains(string(body), "json_schema") {
		t.Errorf("schema was not sent: %s", body)
	}
}

func TestANonSuccessStatusIsAnErrorNamingIt(t *testing.T) {
	s := serve(t, http.StatusInternalServerError, `{"error":"boom"}`, nil)
	_, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q"})
	if err == nil {
		t.Fatal("a 500 returned no error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error does not name the status: %v", err)
	}
	if !strings.Contains(err.Error(), "openaiprov: ") {
		t.Errorf("error lacks the component prefix: %v", err)
	}
}

func TestAnOverLongBodyIsRefusedRatherThanTruncated(t *testing.T) {
	big := `{"model":"a-model","choices":[{"message":{"content":"` + strings.Repeat("x", 4096) + `"}}]}`
	s := serve(t, http.StatusOK, big, nil)
	c := openaiprov.New(openaiprov.Config{
		Name: "test", BaseURL: s.URL, Model: "a-model",
		Timeout: 5 * time.Second, MaxBytes: 256,
	})
	if _, err := c.Complete(context.Background(), ports.Prompt{User: "q"}); err == nil {
		t.Fatal("an over-limit body was accepted")
	}
}

func TestNoChoicesIsAnErrorRatherThanAnEmptyCompletion(t *testing.T) {
	s := serve(t, http.StatusOK, `{"model":"a-model","choices":[]}`, nil)
	if _, err := client(t, s.URL).Complete(context.Background(), ports.Prompt{User: "q"}); err == nil {
		t.Fatal("an empty choices array produced no error")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/adapters/outbound/openaiprov/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the wire types**

Create `internal/adapters/outbound/openaiprov/wire.go`. These are unexported and stop at this package; nothing here crosses the port.

```go
package openaiprov

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	LogProbs       bool            `json:"logprobs,omitempty"`
	TopLogProbs    int             `json:"top_logprobs,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *namedSchema `json:"json_schema,omitempty"`
}

type namedSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema map[string]any  `json:"schema"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Usage   usage  `json:"usage"`
	Choices []struct {
		Message  chatMessage `json:"message"`
		LogProbs *struct {
			Content []struct {
				Token       string  `json:"token"`
				LogProb     float64 `json:"logprob"`
				TopLogProbs []struct {
					Token   string  `json:"token"`
					LogProb float64 `json:"logprob"`
				} `json:"top_logprobs"`
			} `json:"content"`
		} `json:"logprobs"`
	} `json:"choices"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
```

- [ ] **Step 4: Write the client**

Create `internal/adapters/outbound/openaiprov/client.go`. It must:

- Hold a `*http.Client` with `Timeout: cfg.Timeout`, never `http.DefaultClient`.
- Build the request with `http.NewRequestWithContext`, posting to `strings.TrimSuffix(cfg.BaseURL, "/") + "/chat/completions"`.
- Set `Authorization: Bearer <key>` only when `cfg.APIKey` is non-empty, so a local engine needs no key.
- Send `logprobs: true` and `top_logprobs: N` **only** when `p.TopLogProbs > 0`. The third test fails if `top_logprobs` is sent unconditionally.
- Unmarshal `p.Schema` into `map[string]any` and send it as `response_format.json_schema` with `strict: true`, only when `p.Schema` is non-empty. An unparseable schema is an error naming the field, not a silently dropped constraint.
- Measure latency around the round trip with `time.Since`.
- Read the body through a bounded reader that **errors** above `cfg.MaxBytes` rather than truncating. Reuse the shape already used by the tools package: read `MaxBytes+1` and fail if the result exceeds `MaxBytes`.
- Check the status **before** reading the body, so an over-long error body cannot mask a 500.
- Treat an empty `choices` array as an error.
- Prefix every wrapped error with `openaiprov: `.

`Name()` returns `cfg.Name`, which is how the chain in Task 4 names the provider that answered.

- [ ] **Step 5: Run them and watch them pass**

Run: `go test ./internal/adapters/outbound/openaiprov/ -v`
Expected: PASS, all seven.

- [ ] **Step 6: Prove it against the real engine**

Ollama is serving on `http://localhost:11434/v1` with `qwen2.5-coder:7b`. Add a test that is **skipped** unless `ATLAS_LIVE_PROVIDER` is set, so CI never depends on it:

```go
func TestAgainstALiveEngine(t *testing.T) {
	if os.Getenv("ATLAS_LIVE_PROVIDER") == "" {
		t.Skip("ATLAS_LIVE_PROVIDER not set")
	}
	c := openaiprov.New(openaiprov.Config{
		Name: "live", BaseURL: "http://localhost:11434/v1", Model: "qwen2.5-coder:7b",
		Timeout: 3 * time.Minute, MaxBytes: 10 << 20,
	})
	got, err := c.Complete(context.Background(), ports.Prompt{
		User: "Reply with the single word: yes", MaxTokens: 5, TopLogProbs: 3,
	})
	if err != nil {
		t.Fatalf("live complete: %v", err)
	}
	if len(got.Tokens) == 0 {
		t.Fatal("a live engine returned no per-token data when asked for it")
	}
	t.Logf("live: text=%q model=%q latency=%s tokens=%d", got.Text, got.Model, got.Latency, len(got.Tokens))
}
```

Run it once with `ATLAS_LIVE_PROVIDER=1 go test ./internal/adapters/outbound/openaiprov/ -run Live -v` and paste the output.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/outbound/openaiprov
git commit -m "Speak one protocol, and let the base URL choose the engine"
```

---

### Task 3: `model.complete` moves behind the port

**Files:**
- Modify: `internal/adapters/outbound/tools/model.go`, `internal/adapters/outbound/tools/model_test.go`, `cmd/atlas/main.go`, `internal/config/config.go`, `internal/config/layers.go`
- Test: `internal/adapters/outbound/tools/model_test.go`

**Interfaces:**
- Consumes: `ports.Provider`, `ports.Prompt`, `ports.Completion`.
- Produces: `tools.NewModel(p ports.Provider) *tools.Model`, unchanged `Name() == "model.complete"` and unchanged config keys `system`, `user`, `expect`.

The tool stops holding a base URL, a model name, a timeout and a byte limit. It holds a `Provider`. Those settings move to the provider's construction at the composition root.

- [ ] **Step 1: Rewrite the tool's tests against a stub provider**

The existing tests drive an `httptest` server. Replace that with a stub `ports.Provider`, because the tool no longer speaks HTTP. Keep every behaviour the old tests asserted: `expect: json` parses the reply into fields, a non-JSON reply under `expect: json` is an error, and a plain reply comes back under `text`.

Add one new test that could not exist before:

```go
func TestTheToolRecordsWhichModelAnswered(t *testing.T) {
	p := stubProvider{text: "hello", model: "some-model"}
	out, err := tools.NewModel(p).Invoke(context.Background(), map[string]string{"user": "hi"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %T, want map[string]any", out)
	}
	if m["model"] != "some-model" {
		t.Errorf("output does not record the model: %+v", m)
	}
}
```

The product design requires the platform to state which provider produced any given output. This is where that starts being true.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/adapters/outbound/tools/ -run Model -v`
Expected: FAIL — `NewModel` still takes four arguments.

- [ ] **Step 3: Rewrite the tool**

`Invoke` builds a `ports.Prompt` from `with["system"]` and `with["user"]`, calls `Complete`, and returns a map carrying `text` and `model`. Under `expect: json` it parses `Completion.Text` into fields and returns those, plus `model`.

Keep the existing unfencing behaviour if the current code strips a markdown fence before parsing; a model that wraps JSON in a fence is common and that behaviour is load-bearing.

- [ ] **Step 4: Add a span carrying cost and latency**

In the same `Invoke`, record on the current span:

```go
span := trace.SpanFromContext(ctx)
span.SetAttributes(
	attribute.String("gen_ai.response.model", c.Model),
	attribute.Int("gen_ai.usage.input_tokens", c.Usage.PromptTokens),
	attribute.Int("gen_ai.usage.output_tokens", c.Usage.CompletionTokens),
	attribute.Int64("gen_ai.latency_ms", c.Latency.Milliseconds()),
)
```

Imports are `go.opentelemetry.io/otel/attribute` and `go.opentelemetry.io/otel/trace`. This is an adapter, so those imports are fine here; they must not reach `internal/core`.

Do not start a new span. The runner already opens one per step, and this attaches to it.

- [ ] **Step 5: Wire the composition root**

In `cmd/atlas/main.go`, construct an `openaiprov.Client` from config and pass it to `tools.NewModel`. `buildRegistry` must still contain no literal — the three `cmd/atlas` guard tests enforce that and will fail you.

Add `Model.APIKey` to config with a flag and an environment variable, defaulting to empty so a local engine needs none. **A key is a secret**: it must never be logged, and it must not appear in the effective-config output if one exists.

- [ ] **Step 6: Run everything**

```bash
go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
go run ./cmd/atlas -pack packs/job-hunt.yaml
```

Both packs must still run unaided on default config. Paste the output.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/outbound/tools cmd/atlas internal/config
git commit -m "Stop the tool knowing which vendor answered"
```

---

### Task 4: An ordered chain, with a breaker per provider

**Files:**
- Create: `internal/core/app/breaker.go`, `internal/core/app/chain.go`
- Test: `internal/core/app/breaker_test.go`, `internal/core/app/chain_test.go`

**Interfaces:**
- Consumes: `ports.Provider`, `ports.Prompt`, `ports.Completion`.
- Produces: `app.NewChain(providers ...ports.Provider) *app.Chain` implementing `ports.Provider`; `app.NewBreaker(threshold int, cooldown time.Duration) *app.Breaker` with `Allow() bool`, `Success()`, `Failure()`.

This is Tenet 3 applied to the one outbound call the harness now owns. A failing engine must degrade a feature, not a path.

- [ ] **Step 1: Write the breaker's failing tests**

```go
func TestABreakerOpensAfterItsThreshold(t *testing.T) {
	b := app.NewBreaker(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("breaker opened after %d failures, threshold is 3", i)
		}
		b.Failure()
	}
	if b.Allow() {
		t.Error("breaker did not open at its threshold")
	}
}

func TestASuccessResetsTheCount(t *testing.T) {
	b := app.NewBreaker(3, time.Minute)
	b.Failure()
	b.Failure()
	b.Success()
	b.Failure()
	b.Failure()
	if !b.Allow() {
		t.Error("a success did not reset the failure count")
	}
}

func TestAnOpenBreakerProbesAfterItsCooldown(t *testing.T) {
	b := app.NewBreaker(1, 10*time.Millisecond)
	b.Failure()
	if b.Allow() {
		t.Fatal("breaker did not open")
	}
	time.Sleep(15 * time.Millisecond)
	if !b.Allow() {
		t.Error("breaker never closes again; it has no recovery probe")
	}
}

func TestABreakerIsSafeUnderConcurrentUse(t *testing.T) {
	b := app.NewBreaker(100, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Allow()
			b.Failure()
			b.Success()
		}()
	}
	wg.Wait()
}
```

The third test is the one that matters most. A breaker with no recovery probe never closes again, which the spec names as the trap.

The fourth exists because a chain is shared across goroutines the moment anything scores in parallel. Run the suite with `-race`.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/core/app/ -run Breaker -v`
Expected: FAIL — `undefined: app.NewBreaker`.

- [ ] **Step 3: Implement the breaker**

A mutex, a failure count, a threshold, a cooldown and the time the breaker opened. `Allow` returns true when closed, or when the cooldown has elapsed since opening — that elapsed case is the recovery probe. `Success` resets the count and closes. `Failure` increments and opens at the threshold.

Do not store a `Context`. Do not use a package-level clock you cannot control from a test.

- [ ] **Step 4: Write the chain's failing tests**

```go
func TestTheChainUsesTheFirstProviderThatAnswers(t *testing.T) {
	failing := &scriptedProvider{name: "first", err: errors.New("down")}
	working := &scriptedProvider{name: "second", text: "answered"}

	got, err := app.NewChain(failing, working).Complete(context.Background(), ports.Prompt{User: "q"})
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

	_, err := app.NewChain(a, b).Complete(context.Background(), ports.Prompt{User: "q"})
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
	chain := app.NewChain(failing, working)

	for i := 0; i < 10; i++ {
		if _, err := chain.Complete(context.Background(), ports.Prompt{User: "q"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if failing.calls >= 10 {
		t.Errorf("failing provider was called %d times in 10; its breaker never opened", failing.calls)
	}
}

func TestACancelledContextStopsTheChainRatherThanFallingThrough(t *testing.T) {
	a := &scriptedProvider{name: "alpha", err: context.Canceled}
	b := &scriptedProvider{name: "bravo", text: "answered"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := app.NewChain(a, b).Complete(ctx, ports.Prompt{User: "q"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if b.calls != 0 {
		t.Errorf("second provider was called %d times after cancellation; a dead client hammers every provider", b.calls)
	}
}
```

Write `scriptedProvider` in the same file: a `ports.Provider` recording its call count and returning either a fixed text or a fixed error.

The fourth matters because treating a cancellation as a provider failure would hammer every provider in the chain on behalf of a client that has already gone away. Imports for these tests are `context`, `errors`, `strings`, `sync`, `testing`, `time` and the project's `ports` package.

- [ ] **Step 5: Run them and watch them fail**

Run: `go test ./internal/core/app/ -run Chain -v`
Expected: FAIL — `undefined: app.NewChain`.

- [ ] **Step 6: Implement the chain**

Try each provider in order, skipping any whose breaker is open. On success, record it and return. On failure, record it and continue. If the context is done, return its error immediately rather than trying the next provider.

The returned error when every provider fails must name each provider and its error, so `chain: first: down; second: down` rather than a single opaque failure.

`Name()` returns something stable such as `chain`. Breaker thresholds and cooldowns come from config, not literals.

- [ ] **Step 7: Run the suite under the race detector**

Run: `go test ./internal/core/app/ -race -v`
Expected: PASS, all breaker and chain tests.

- [ ] **Step 8: Commit**

```bash
git add internal/core/app/breaker.go internal/core/app/chain.go internal/core/app/breaker_test.go internal/core/app/chain_test.go
git commit -m "Let one failing engine degrade a feature rather than a path"
```

---

### Task 5: Read a probability correctly

**Files:**
- Create: `internal/core/app/mass.go`
- Test: `internal/core/app/mass_test.go`

**Interfaces:**
- Consumes: `ports.Completion`, `ports.Token`, `ports.Alternative`.
- Produces: `app.MassPerClass(c ports.Completion, classes map[string][]string) (map[string]float64, error)`.

This task exists because of a measured fact. Given a schema-constrained yes/no answer, the returned logprobs are the model's **pre-constraint** distribution, and the same answer is split across surface forms:

```
token='yes'  p=0.2660   alts={'Yes': 0.6719, 'yes': 0.2660, 'no': 0.0124}
```

Reading the chosen token's logprob reports 27% confidence where the model is near 94%. Epic 3's `Judge` depends on getting this right, so it is proven here before anything is built on it.

- [ ] **Step 1: Write the failing tests**

```go
func recordedYesNo() ports.Completion {
	return ports.Completion{
		Text: `{"answer": "yes"}`,
		Tokens: []ports.Token{
			{Text: "{", LogProb: -11.5, Alternatives: []ports.Alternative{
				{Text: "Yes", LogProb: -0.042}, {Text: "The", LogProb: -3.75}, {Text: "No", LogProb: -4.98}}},
			{Text: "answer", LogProb: -4.95, Alternatives: []ports.Alternative{
				{Text: "text", LogProb: -1.35}, {Text: "response", LogProb: -1.63}}},
			{Text: "yes", LogProb: -1.3244, Alternatives: []ports.Alternative{
				{Text: "Yes", LogProb: -0.3975}, {Text: "yes", LogProb: -1.3244}, {Text: "no", LogProb: -4.3892}}},
		},
	}
}

var yesNo = map[string][]string{
	"yes": {"yes", "Yes", "YES", "true"},
	"no":  {"no", "No", "NO", "false"},
}

func TestMassSumsAcrossSurfaceForms(t *testing.T) {
	got, err := app.MassPerClass(recordedYesNo(), yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	// "yes" (0.266) plus "Yes" (0.672) against "no" (0.012), normalised.
	if got["yes"] < 0.90 {
		t.Errorf("yes = %.4f, want at least 0.90; surface forms are not being summed", got["yes"])
	}
	if got["no"] > 0.05 {
		t.Errorf("no = %.4f, want below 0.05", got["no"])
	}
}

func TestMassIsNotTheChosenTokensProbability(t *testing.T) {
	got, err := app.MassPerClass(recordedYesNo(), yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	chosen := math.Exp(-1.3244) // the "yes" token as emitted
	if math.Abs(got["yes"]-chosen) < 0.01 {
		t.Errorf("yes = %.4f, which is the chosen token's own probability; the split across surface forms was ignored", got["yes"])
	}
}

// The recorded fixture cannot separate "read the structural token" from "read
// the answer token": both favour yes, within a percentage point of each other.
// These two fixtures are built so each rule fails visibly when broken.

func TestAStructuralTokenIsSkippedEvenWhenItsAlternativesLookLikeClasses(t *testing.T) {
	c := ports.Completion{Tokens: []ports.Token{
		// A brace the schema forced. Its alternatives are the prose the model
		// wanted, and they point the opposite way to the real answer.
		{Text: "{", LogProb: math.Log(0.001), Alternatives: []ports.Alternative{
			{Text: "No", LogProb: math.Log(0.97)},
			{Text: "Yes", LogProb: math.Log(0.02)}}},
		{Text: "yes", LogProb: math.Log(0.88), Alternatives: []ports.Alternative{
			{Text: "yes", LogProb: math.Log(0.88)},
			{Text: "no", LogProb: math.Log(0.04)}}},
	}}
	got, err := app.MassPerClass(c, yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	if got["yes"] < 0.9 {
		t.Errorf("yes = %.4f; the structural token was read instead of the answer token", got["yes"])
	}
}

func TestTheLastClassBearingTokenWins(t *testing.T) {
	c := ports.Completion{Tokens: []ports.Token{
		{Text: "yes", LogProb: math.Log(0.5), Alternatives: []ports.Alternative{
			{Text: "yes", LogProb: math.Log(0.5)},
			{Text: "no", LogProb: math.Log(0.5)}}},
		{Text: "no", LogProb: math.Log(0.93), Alternatives: []ports.Alternative{
			{Text: "no", LogProb: math.Log(0.93)},
			{Text: "yes", LogProb: math.Log(0.05)}}},
	}}
	got, err := app.MassPerClass(c, yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	if got["no"] < 0.9 {
		t.Errorf("no = %.4f; an earlier class-bearing token was read instead of the last", got["no"])
	}
}

func TestMassSumsToOne(t *testing.T) {
	got, err := app.MassPerClass(recordedYesNo(), yesNo)
	if err != nil {
		t.Fatalf("mass: %v", err)
	}
	total := 0.0
	for _, v := range got {
		total += v
	}
	if math.Abs(total-1.0) > 1e-9 {
		t.Errorf("mass sums to %.6f, want 1", total)
	}
}

func TestACompletionWithNoTokensIsAnError(t *testing.T) {
	if _, err := app.MassPerClass(ports.Completion{Text: "yes"}, yesNo); err == nil {
		t.Fatal("a completion carrying no per-token data returned no error")
	}
}

func TestNoMatchingTokenIsAnError(t *testing.T) {
	c := ports.Completion{Tokens: []ports.Token{{Text: "banana", Alternatives: []ports.Alternative{{Text: "apple", LogProb: -1}}}}}
	if _, err := app.MassPerClass(c, yesNo); err == nil {
		t.Fatal("a completion with no token matching any class returned no error")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/core/app/ -run Mass -v`
Expected: FAIL — `undefined: app.MassPerClass`.

- [ ] **Step 3: Implement it**

Create `internal/core/app/mass.go`.

Choosing the answer-bearing token is the whole problem, and the third test exists to stop the obvious wrong answer. Use this rule and state it in the doc comment:

> The answer-bearing token is the **last** token whose own text belongs to a class. Structural tokens such as `{` may carry class-looking alternatives because the model intended prose before the schema intervened; their own text is not a class member, so they are skipped.

Then, at that token, sum `math.Exp(logprob)` over every alternative whose text matches a class, case-sensitively against the supplied surface forms. Normalise across classes so the result sums to one.

**Do not add the chosen token's own probability on top.** An OpenAI-compatible `top_logprobs` array already contains the chosen token, so adding it again double-counts one surface form and inflates its class. Include the token's own probability only when its text does not appear among its alternatives.

An empty `Tokens` slice is an error naming that per-token data was not requested. A completion where no token's text matches any class is an error too — returning a uniform distribution there would be an invented number, which is the exact failure this function exists to prevent.

Comparison is against the caller's surface-form list. Do not lowercase and match loosely: the caller states what counts, so `"Yes"` counts only because the caller listed it.

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/core/app/ -run Mass -v`
Expected: PASS, all seven.

- [ ] **Step 5: Prove it against the live engine**

Add a live check, skipped unless `ATLAS_LIVE_PROVIDER` is set, that asks a question with an obvious answer through the real provider with a schema and `TopLogProbs: 5`, then asserts `MassPerClass` puts the obvious answer above 0.8. Paste its output.

This is the increment's real evidence: a probability read from a live model that matches what a human would say.

- [ ] **Step 6: Commit**

```bash
git add internal/core/app/mass.go internal/core/app/mass_test.go
git commit -m "Sum the mass an answer actually holds, not the token that carried it"
```

---

### Task 6: vLLM, and the increment note

**BLOCKED at the time of writing.** `nvidia-smi` reports `Driver/library version mismatch` — NVML library 580.178 against kernel module 580.173.02. No GPU work can run until that is resolved, which is the machine owner's to do, usually with a reboot.

**Do not attempt to install vLLM, change driver packages, or reboot.** If the GPU is still unavailable, do the note and record the block.

**Files:**
- Modify: `internal/config/layers.go`, `docs/design/the-record.md`
- Create: `docs/notes/2026-09-21-increment-2.md`

- [ ] **Step 1: Check whether the GPU is back**

```bash
nvidia-smi --query-gpu=name,memory.total,memory.used --format=csv
```

If this still reports a version mismatch, skip to Step 4 and record it.

- [ ] **Step 2: Bring vLLM up, if the GPU is available**

This is the machine owner's step, not yours. Report the commands and stop:

```bash
pip install vllm
vllm serve Qwen/Qwen2.5-Coder-7B-Instruct-AWQ --max-model-len 8192 --gpu-memory-utilization 0.90
```

8 GB of VRAM bounds this to roughly a 7B at four bits or a 3-4B at full precision, plus KV cache. `--max-model-len` is the lever when it will not fit.

- [ ] **Step 3: Prove the port's claim, if vLLM is serving**

Run the same pack against both engines, changing only the base URL:

```bash
go run ./cmd/atlas -pack packs/job-hunt.yaml -model-base-url http://localhost:11434/v1 -model-name qwen2.5-coder:7b
go run ./cmd/atlas -pack packs/job-hunt.yaml -model-base-url http://localhost:8000/v1  -model-name Qwen/Qwen2.5-Coder-7B-Instruct-AWQ
```

Both must succeed with no Go change. Paste both outputs. **This is story 2.2's acceptance and the increment's headline claim.**

- [ ] **Step 4: Write the increment note**

Create `docs/notes/2026-09-21-increment-2.md` with four sections: what the pattern was, what surprised you, what you would do differently, what you still do not understand.

Record honestly:

- Whether vLLM was actually run, or whether the GPU blocked it and the claim rests on Ollama alone.
- What `MassPerClass` returned against a live model versus what the chosen token's logprob would have said.
- Whether the "last token whose own text is a class member" rule held up, or whether a real answer shape broke it.
- Whether `Prompt` and `Completion` are the right shapes, or whether the first real `Judge` will want something they cannot express.

A note saying everything went fine is worthless. If everything did go fine, say what that suggests is under-tested.

- [ ] **Step 5: Update the design reference**

Add a section to `docs/design/the-record.md` — or a sibling document if the record file is the wrong home — covering the `Provider` port, the chain, and how a probability is read. Describe current behaviour, not the edits.

- [ ] **Step 6: Run everything and commit**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
go run ./cmd/atlas -pack packs/hn-summary.yaml
go run ./cmd/atlas -pack packs/job-hunt.yaml
git add -A
git commit -m "Record what the provider port cost and what reading a probability taught"
```

---

## Deliberately not in this increment

| Out | Why |
|---|---|
| The `Judge` port | Epic 3. Task 5 proves a probability can be read; it does not build typed questions or speculative fan-out |
| Checking a probability against an outcome | Needs applications with results. Epic 11 closes the loop |
| Per-pack or per-step model selection | A real gap the first two packs exposed, but it changes the pack format; its own increment |
| Retry with backoff | The chain gives failover. Retry needs error classification, and nothing yet classifies |
| Wiring `Rebuild` to a production caller | Carried from increment 1, and it belongs to whatever first needs the index |
| Token cost in currency | Needs per-model pricing that nothing yet holds |
