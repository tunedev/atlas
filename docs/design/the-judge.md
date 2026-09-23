# The judge

How a typed judgement works today. Companion to `docs/design/the-provider.md`,
which covers the model boundary this builds on, and `docs/design/the-record.md`,
which covers the storage a judgement is written to.

## The shape

```go
type Question struct {
	ID      string
	Kind    Kind // noul, choice, or score
	Ask     string
	Options []string
	Forms   map[string][]string
}

type Answer struct {
	ID           string
	Kind         Kind
	Chosen       string
	Distribution map[string]float64
	Expected     float64 // set for a score alone
}

type Judgement struct {
	Subject string
	Model   string
	When    time.Time
	Answers []Answer
}

type Judge interface {
	Ask(ctx context.Context, subject string, qs []Question) (Judgement, error)
}
```

A noul carries a probability of yes; a choice carries a distribution over its
options with the top one as `Chosen`; a score carries the same over ordered
levels, plus `Expected`, the levels' positions weighted by their mass. A noul
with no `Options` falls back to `ports.NoulOptions()`/`NoulForms()`, so "Yes"
and "yes" count as one answer without a pack having to say so.

`app.Judge` is the one implementation, built by `app.NewJudge(provider,
cfg)` over any `ports.Provider`. `app.JudgeConfig` pins `Temperature`, `Seed`,
`TopLogProbs`, and `MaxTokens` — fixed sampling so the same subject answers
the same way across runs, which is what makes a mass reading comparable at
all.

## One call, one schema, every answer at once

`Judge.Ask` builds one `ports.Prompt` for every question in `qs`: a system
message telling the model each field must be exactly one of its allowed
options, a user message naming the subject and every question's id and text,
and `Schema` from `app.AnswerSchema(qs)` — standard JSON Schema with one
required string property per question id, each constrained to that
question's options by an enum, `additionalProperties: false`. One call
answers every question, so answers about the same subject cannot contradict
each other, and the schema is what makes an out-of-schema answer impossible
rather than merely unlikely.

`internal/adapters/outbound/openaiprov/judge_live_test.go` proves this
against a real engine (story 3.3): the subject text itself carries an
embedded instruction ordering the model to answer one question with a word
outside its options. The schema holds regardless — the live run's `sky`
answer landed on `blue`, one of its four declared options, never on the word
the subject asked for. A schema is not evadable by asking nicely inside the
content it constrains.

## The answer-token rule

A completion answering several questions in one JSON object packs all their
answers into one text. `MassPerClass` (`docs/design/the-provider.md`) finds
the *last* class-bearing token in a whole completion — right when there is
one answer, wrong when there are several, since a later question's answer
would get read as an earlier one's.

`app.AnswerTokens(c, ids)` locates each question's own answer token instead.
It reconstructs the completion's full text from its tokens (recording each
token's starting byte offset), walks that text as JSON with
`encoding/json.Decoder` to get the byte range each top-level key's value
occupies, then returns the *first token whose own text carries a character
other than whitespace, `"`, `:` or `,`* that starts inside that key's value
range. The punctuation around a value is structure; the first token with any
content is where a real tokenizer's alternatives at that position actually
diverge between options.

## Option matching: exact, then prefix, then ambiguity

The answer-token rule selects a value's *first* token, but a real tokenizer
does not always split one option into one token: `backend` can tokenise as
`back` + `end`. Reading that first token's mass by matching alternatives
against a class's *whole* surface string, as `MassAtToken`/`MassPerClass`
correctly do for their own single-token contract, misses every multi-token
option outright — `"back"` never equals `"backend"`.

`app.massForToken` reads mass for the answer token using `optionMatch`
instead: trim the alternative's text of surrounding whitespace and one layer
of `"` quotes, then

1. an **exact** match against any of an option's surface forms wins
   outright, or else
2. a **non-empty prefix** match against a form counts, provided the trimmed
   text prefixes exactly one option's forms.

A trimmed text that prefixes two or more different options' forms is an
error — `judge: <question id>: "<text>" is ambiguous between <a> and <b>` —
not a guess. The same double-count guard `MassAtToken` uses applies here:
the answer token's own probability is folded in only when its own text is
not already among its alternatives.

## The judgement record

`app.RecordJudgement(ctx, docs, index, subjectID, qs, j)` writes a
`Judgement` under `judgements/<subjectID>/<millisecond-timestamp>.json` (git,
via `ports.Docs`) and indexes it as one row keyed by that path (via
`ports.Index`), same pattern as every other document `docs/design/the-record.md`
describes. The document holds the subject text, the model name, every
question asked, every answer with its distribution, and an `outcome` field
carried as `any` and marshalled as JSON `null` — present, not omitted, so the
key reads `null` until a later increment (checking a probability against a
real outcome, epic 11) fills it in. The index row mirrors this with a flat
string field, `"outcome": "pending"`, since the row cannot hold a nested
distribution.

`tools.Judge` (`judge.ask`) is the one caller: it parses a pack's YAML
questions block into `[]ports.Question`, calls `Judge.Ask`, then
`RecordJudgement`, and returns each answer keyed by question id (`chosen`,
`p`, `distribution`, `expected` for a score) plus the path written, under the
key `"path"` — reserved, so a pack cannot name a question `path` and collide
with it.

## Known gaps

- `optionMatch`'s trim strips surrounding whitespace, then one layer of `"`
  quotes, in that order — so `" x "` (space, x, space, inside quotes) keeps
  its inner spaces rather than trimming again after the quotes come off. No
  engine has been observed emitting this; it is speculative, not reproduced.
- Every live figure this increment comes from one engine (Ollama,
  `qwen2.5-coder:7b`). Sampling is pinned (temperature 0, a fixed seed), but
  that only makes one engine repeatable — it says nothing about whether a
  second engine reads the same subject's mass the same way. Increment 2's
  vLLM-versus-Ollama disagreement on the same prompt is unretested for the
  judge path.
- `Judge` is built over one `openaiprov.Client` directly in
  `cmd/atlas.buildRegistry`, not over `app.Chain`. The chain-wiring gap in
  `docs/design/the-provider.md` applies here unchanged: there is exactly one
  provider to fail over from.
- `Question`/`Answer` express one flat field per question, each a member of
  a fixed enum. Nothing here has asked a free-text question or a question
  with more than one answer field; the answer-token rule's "first content
  token of the field's value" is defined for a single scalar value; it does
  not yet have a definition for a value that is itself an array or object.
