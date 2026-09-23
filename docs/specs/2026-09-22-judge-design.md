# The `Judge` port — design

**Supersedes nothing.** This design details Epic 3 of `docs/plans/2026-09-17-roadmap.md` and
sits under `docs/specs/2026-09-17-job-hunt-harness-design.md`, which it does not contradict.

**Goal:** judgement is a value with a probability attached, produced in one call, and recorded
so that a later increment can check the probability against what actually happened.

## What this rests on

Increment 2 shipped `ports.Provider` (a prompt in, a completion with per-token log
probabilities out) and `app.MassPerClass`, which sums probability across the surface forms of
an answer at one token position. Two measured facts from that increment govern this design.

The log probabilities an engine returns are the model's **pre-constraint** distribution. On a
schema-constrained yes/no question the answer arrived as `yes` at p=0.266 while `Yes` held
p=0.672. Reading the emitted token's own probability reports 27% where the model is near 94%,
so mass must be summed across surface forms that mean the same thing.

`MassPerClass` reads exactly one position per completion: the last token whose own text is a
class member. Many questions in one completion therefore need a token located per question,
which is the central mechanism this design adds.

## The types

Core-owned, in the core's vocabulary. Nothing here names a use-case concept;
`internal/arch/vocabulary_test.go` enforces that.

```go
type Kind string // noul, choice, score

// Question is one typed question. Options are the allowed answers: for choice the
// options themselves, for score the ordered levels, and for noul the implicit yes
// and no. Forms adds surface forms that count as an option, such as Yes for yes.
type Question struct {
    ID      string
    Kind    Kind
    Ask     string
    Options []string
    Forms   map[string][]string
}

// Answer is what the model answered and how much probability mass each option
// held. Distribution sums to one. Expected is set for score alone: the levels'
// positions weighted by their mass.
type Answer struct {
    ID           string
    Kind         Kind
    Chosen       string
    Distribution map[string]float64
    Expected     float64
}

// Judgement is every answer to one subject, and which model produced them.
type Judgement struct {
    Subject string
    Model   string
    When    time.Time
    Answers []Answer
}
```

A **noul**'s probability of yes is `Distribution["yes"]`. One answer shape serves all three
kinds rather than three parallel types; the kind says how to read it.

## The port

```go
type Judge interface {
    Ask(ctx context.Context, subject string, qs []Question) (Judgement, error)
}
```

A port exists where a second implementation is nameable. This one has two: the local
implementation over `Provider`, and TypeSafe, which the harness spec names as hosted and
opt-in per workspace. `Judge` and `Provider` stay separate because `Judge` speaks questions
and probabilities while `Provider` speaks prompts and tokens.

## The local implementation

It lives in `internal/core/app`, beside `Chain`, because it composes two core concerns and
performs no I/O of its own: it calls `Provider` and reads mass. Only the standard library and
`internal/core/ports` are imported, so the architecture test still holds. A core package
implementing a core port over another core port is the same shape `Chain` already has.

`Ask` does one round trip:

1. **Build the schema.** A JSON object whose properties are the question ids, each an enum of
   that question's options, all required, no additional properties. The enum is what makes a
   `choice` unable to return anything else.
2. **Build the prompt.** The subject and the questions, each question's id and text listed.
   `TopLogProbs` is set so alternatives come back. Temperature is pinned to 0 and a seed is
   set, so the same question against the same engine gives the same answer.
3. **Locate each answer token.** Walk `Completion.Tokens`, tracking position within the JSON:
   which field name was last seen, and whether the tokens now arriving are that field's value.
   The first token of a field's value is that question's answer token, because that is where
   the alternatives distinguish one option from another.
4. **Read the mass** at each located position, against that question's own options and surface
   forms, and normalise per question.
5. **Derive `Expected`** for a score: the sum over levels of (level index x its mass).

Two cases are errors naming the question id, never a filled-in number: a question whose field
never appeared in the completion, and an answer token carrying no alternatives. Inventing a
distribution is the failure `MassPerClass` exists to prevent.

### Sampling controls

`ports.Prompt` gains `Temperature *float64` and `Seed *int`, both nil-able so an unset value
keeps the engine's default and no existing caller changes behaviour. The adapter sends them
only when set. This exists because increment 2 measured vLLM and Ollama disagreeing on the
same advert under the same nominal prompt, traced to vLLM applying its own generation config.
A probability read under unknown sampling is not a calibrated number.

Neither engine promises bitwise determinism from a seed. The claim here is narrower: sampling
is stated rather than inherited, and the same inputs give the same answer in practice.

## The record

Story 3.5 of the roadmap: the question, the answer, the probability and the eventual outcome
slot are persisted together, and it lands with this epic rather than later, because judgements
made before it exists can never be checked.

Git is the system of record, so a judgement is a document. The indices stay derived.

- **Document:** `judgements/<subject-id>/<timestamp>.json` through `ports.Docs`, carrying the
  judgement, the questions as asked, the model, and `"outcome": null`.
- **Index row:** through `ports.Index`, `kind=judgement`, with the subject id, the question
  ids and the outcome state as flat string fields, so "every judgement still awaiting an
  outcome" is a query rather than a directory walk.
- **Identity:** the caller supplies the subject id — the id the outside world already uses for
  that subject. Judging the same subject twice adds a sibling document; nothing is overwritten.
  A missing subject id is an error, not a generated fallback, because an unattachable
  judgement cannot be checked later.
- **The outcome slot** is filled by a later revision of the same document, which is why the
  document rather than the index is the record.

Recording is a use case in `internal/core/app` composing `Docs` and `Index`. `Judge.Ask` does
not write: keeping the port pure means any implementation records identically, and the two are
tested independently.

## Reaching a pack

A `judge.ask` tool wraps the port, registered in `cmd/atlas`'s composition root beside
`model.complete`. A step names it:

```yaml
- id: verdict
  tool: judge.ask
  with:
    subject_id: "{{ .steps.role.id }}"
    subject: "{{ .steps.role.content }}"
    questions: |
      - id: stretch
        type: noul
        ask: Is this role a stretch for the candidate?
      - id: seniority
        type: score
        levels: [junior, mid, senior]
        ask: How senior is this role?
```

`with` is `map[string]string`, so the questions arrive as a YAML block the tool parses. The
pack format itself does not change. In that block a `choice` names `options` and a `score`
names `levels`; both fill `Question.Options`, and a `noul` names neither.

The tool returns the answers keyed by question id, each carrying `chosen`, `p` (the chosen
option's mass, which for a noul is the probability of yes), `distribution` and, for a score,
`expected`. A later step selects `{{ .steps.verdict.stretch.p }}` by path, the way pack steps
already narrow a tool's result.

## Testing

Every test runs with no network, which is story 3.6.

- The field-walking scanner is tested over recorded token streams: a value spanning several
  tokens, fields emitted in a different order from the request, a field absent, an answer
  token with no alternatives.
- The three kinds are tested for their answer shapes, including a score's expected position
  and a noul's probability coming from summed mass rather than the emitted token.
- The one-call requirement is asserted by counting calls on a stub `ports.Provider`.
- Constrained decoding (story 3.3) is asserted two ways: offline, that the enum schema is on
  the wire; and against a live engine told to answer outside its options, skipped unless
  `ATLAS_LIVE_PROVIDER` is set, since only a real engine proves the constraint holds.
- Recording is tested against the existing `Docs` and `Index` adapters, including that the
  outcome slot exists and is empty, and that a second judgement of the same subject does not
  overwrite the first.

## Deliberately not in this increment

| Out | Why |
|---|---|
| Checking a probability against an outcome | Needs applications with results. Epic 11 closes the loop; this increment only leaves the slot |
| The fit judgement (apply / stretch / skip, deal-breakers) | Epic 7, which is this epic pointed at real postings |
| TypeSafe | Hosted, opt-in, and a second adapter behind a port this increment defines |
| Error classification and wiring `Chain` | Carried from increment 2; it belongs with the increment that gives the chain a production caller |
| Batch judgement of many subjects in one call | Story 3.2 is many questions about one subject. Many subjects is epic 7's scoring of a board |
