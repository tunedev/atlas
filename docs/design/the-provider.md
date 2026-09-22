# The provider

How a model call works today. Companion to `docs/design/the-record.md`, which
covers storage; this covers the model boundary.

## The shape

`ports.Provider` is one interface, two methods:

```go
type Provider interface {
	Name() string
	Complete(ctx context.Context, p Prompt) (Completion, error)
}
```

`Prompt` carries a system and user message, a max token count, an optional
JSON Schema the answer must satisfy, and `TopLogProbs`, which asks for that
many alternatives per output token. `Completion` carries the answer text, the
model name that produced it, per-token data when requested, token usage, and
latency. Nothing vendor-shaped crosses this boundary: no HTTP type, no
OpenAI-specific field name.

`openaiprov.Client` is the one adapter, over any OpenAI-compatible chat
completions endpoint. Ollama, vLLM, and a hosted key all speak that protocol,
so switching among them is a base URL and a model name
(`-model-base-url`, `-model-name`), never a Go change. `internal/config`
already exposes both as flags/env with defaults pointed at a local Ollama.

`tools.Model` (`model.complete`) is the one caller in the tool registry. It
takes `system`/`user`/`expect` from a pack step, calls the provider, and
either returns the raw text under `"text"` or parses it as JSON when
`expect: json` is set. Either way the response carries `"model"`, so a pack's
output records which engine actually answered. `model.complete` does not set
`Schema` or `TopLogProbs` on the `Prompt` it builds — those fields exist for
callers that need a constrained, log-prob-bearing answer, and nothing in the
tool registry is that caller yet.

## The chain

`app.Chain` implements `ports.Provider` over an ordered list of providers,
each behind its own `app.Breaker`. `Complete` tries each provider in order:
a provider whose breaker is open is skipped; a provider that answers closes
its breaker and its completion is returned; a provider that fails opens its
breaker further and the next provider is tried. A failure that is the
caller's context ending (`ctx.Err() != nil` after the call) is returned
immediately, untouched by breaker state and without trying the next
provider — the client left, no provider failed. If every provider is
skipped or fails, `Complete` returns a single error naming every provider's
outcome.

`app.Breaker` opens after `threshold` consecutive failures and allows one
probe call once `cooldown` has elapsed since it last opened; a failed probe
re-arms the cooldown rather than leaving the breaker permanently probing.

**`Chain` has no production caller.** `cmd/atlas`'s `buildRegistry` builds one
`openaiprov.Client` from config and hands it directly to `tools.NewModel`;
only one provider is configurable today, so there is nothing yet for a chain
to order. Wiring multiple providers through `cmd/atlas` needs a config shape
for more than one engine, which is a later increment's work.

## Reading a probability

A `Prompt` with `TopLogProbs > 0` gets back a `Completion.Tokens` slice: one
`ports.Token` per output position, each with its own log probability and up
to `TopLogProbs` alternatives (also text plus log probability) at that
position.

`app.MassPerClass(c ports.Completion, classes map[string][]string)` turns
that per-token data into a normalised probability per class. It finds the
**answer-bearing token** — the last token in the completion whose own text is
a member of some class — then sums `math.Exp(logprob)` across that token's
alternatives for every alternative whose text matches a class's surface
forms, adding the token's own probability only if its own text is not
already among its alternatives (an OpenAI-compatible `top_logprobs` array
already includes the chosen token, so re-adding it would double-count that
surface form). The result is scaled to sum to one.

Reading "last class-bearing token" rather than "the final token of the
completion" matters because a schema can force a structural token after the
answer (a closing `}`, for instance); the final token's own text is not a
class member and would carry no useful distribution.

`MassPerClass` returns an error rather than a guess when `Completion.Tokens`
is empty (no per-token data was requested) or when no token's text matches
any class (there is nothing to normalise honestly).

## Known gaps

- The chain is built and tested but not wired into `cmd/atlas`; only one
  provider is configurable in production.
- `model.complete` never requests `TopLogProbs` or sets `Schema`; only tests
  exercise `MassPerClass` against a live completion.
- `openaiprov.Client.Complete` discards the response body on a non-2xx
  status; a caller sees only `"<name> returned status <code>"`, with no
  detail about why the engine failed.
- Retry, per-provider load, and a `Judge` port that would consume
  `MassPerClass` are out of scope for this increment.
