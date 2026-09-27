# Epic 10 — The apply loop — design

**Supersedes nothing.** This design details Epic 10 of `docs/plans/2026-09-17-roadmap.md`. It
rests on Epic 6 (profile, deal-breaker rules, decision log), Epic 7 (`judge.each`, fit
judgement), Epic 9 (tailoring, `packs/tailor.yaml`, `render.run`) and is the vocabulary Epic 11's
second increment waits on (`docs/specs/2026-09-27-epic-11-tracking.md`, stories 11.1 and 11.2).

**Goal:** one run turns a board of scored postings into drafted application packages for the
postings the user's policy allows, a clear list of the ones it asks about, and a logged skip for
the ones it denies — and never submits anything. The user declares when a package was sent, and
that declaration starts the clock Epic 11 measures from.

## The refusal, and its reversal condition

Drafting is the product. **Atlas does not submit an application, by API, form post or browser
automation, and no tool, pack or agent path can.** Submitting is the one action whose mistakes
cannot be undone and whose consequences land on the user's reputation, and the platform cannot
see enough of a board's own flow to do it safely.

The refusal is enforced, not asserted. Measured before designing: `http.request` today accepts any
`method` a pack or an agent (Epic 4, through MCP) passes it, so a pack could send a `DELETE` or a
bodiless `POST`. This epic restricts `http.request` to `GET` and `HEAD` and pins that with a test,
and an architecture test fails if any registered tool advertises a write to a remote host.

**Reversal condition.** Submission is reconsidered only when a board offers an official
submission API that requires an explicit, per-submission human confirmation, and even then never
through browser automation. Until a spec says that condition holds for a named board, the
refusal stands.

## Stages and the send clock (10.4)

The platform cannot see the boundary between drafted and sent, so the user declares it.

- **One document per application,** `applications/<subject_id>/stage.json`, beside Epic 9's
  `tailored.json`. Each change of stage is a new revision of that document, so git history is the
  application's timeline. The index row (kind `stage`) carries the current `stage` and a
  `<stage>_at` field for every stage reached, so "everything sent more than 14 days ago" is one
  `Index.Find`.
- **Go knows nothing about applications.** A stage is a free string a pack defines, with a time
  and who declared it. `stage.declare` takes `subject_id`, `stage`, optional `when` (RFC 3339,
  default now), `note`, and `declared_by`. It enforces only rules true of any timeline:
  - `when` is not in the future;
  - `when` is not before the current stage's time;
  - declaring the current stage again is a no-op (no new revision).
- **Who declares what, fixed by the packs:**

  | Stage | Declared by | When |
  |---|---|---|
  | `drafted` | `packs/apply.yaml` | Only after every package document rendered and verified |
  | `sent` | `packs/sent.yaml`, run by the user | When the user says they submitted; `-var when=` may backdate, never before `drafted` |
  | `not_applied` | `packs/skip.yaml` | When the policy denies a posting, or the user skips an asked one |

- **The clock.** The `sent_at` field is what Epic 11.2 measures staleness from; the `stage` field
  is Epic 11.1's "where each one is". Epic 11's outcomes stay on the judgement document: stages
  describe the path to a result, outcomes the result itself. `not_applied` here and Epic 11's
  `not_applied` outcome state describe the same fact from the two documents, and the skip pack
  writes the stage; attaching the outcome remains the user's call through Epic 11's pack.

## The policy (10.2)

- **The policy document,** `profile/policy.json`, user-authored beside `profile/dealbreakers.json`:

  ```json
  {"rules": [
    {"id": "strong-fit", "decision": "allow",
     "when": [{"field": "verdict", "op": "==", "value": "apply"},
              {"field": "p", "op": ">=", "value": 0.7}]},
    {"id": "no-frontend", "decision": "deny",
     "when": [{"field": "answers.focus", "op": "==", "value": "frontend"}]}
  ]}
  ```

  Conditions reuse Epic 6's comparable-rule operators and value checks (`app.rules`), over a
  field path of one scored row. A rule matches when all its conditions hold. There is no second
  rule language.
- **Evaluation,** `app.Decide` (pure) behind `policy.decide`, which reads `judge.each`'s rows and
  the policy and adds `decision`, `matched` (rule ids) and `because` to each row. Precedence is
  fixed, not order-dependent:
  1. any deal-breaker `tripped` → **deny**, always; no allow rule can override it;
  2. any matching deny rule → **deny** (deny beats allow);
  3. any deal-breaker `unknown` → at most **ask** (a possible deal-breaker is never auto-drafted);
  4. any matching allow rule → **allow**;
  5. otherwise → **ask** (the default).
  An error row from `judge.each` gets no decision and stays an error.
- **Rows carry what the policy needs.** `judge.each` rows gain `answers` (question id → chosen
  option) and `source_id` (the item's original id). Additive; existing consumers ignore them.

## Promoting a repeated decision (10.3)

- **`policy.suggest`** (read-only) groups the decision log by the verdict recorded at the time and
  the deal-breakers that were tripped, and where the user made the same choice at least N times
  (configurable, default 3) prints a candidate rule with its evidence: the subject ids. It never
  writes.
- **`packs/policy-promote.yaml`** takes the candidate as a var, validates it with the same parser
  `policy.decide` uses, appends it to `profile/policy.json`, and commits with a message naming the
  decisions it came from. Nothing becomes a rule unless the user runs it.

## One action, one package (10.1)

- **`pack.each`** (generic tool). Inputs: a pack path, `rows` (JSON from an earlier step),
  `match` (comma-separated `field=value` conditions, all of which must hold; a missing field
  counts as empty), and `vars` (one `[[ ]]` template per child var, rendered per row with `.item`
  bound, as `judge.each` does); shared values pass through the parent's `{{ }}`. Each matching row
  gets a fresh run of the child pack, one at a time, in id order. A child failure becomes an error
  row; the rest continue. Output: one row per child run with its vars, `ok` or `error`, and the
  child's final state. The child registry is the parent's without `pack.each`, so nesting is one
  level by construction. Pack loading and the runner are injected at the composition root, so the
  tools package does not import the pack-file adapter. Child steps are traced under the parent's
  span.
- **`stage.attach`** (generic tool) adds each row's current `stage` from the index, so
  `match: decision=allow,stage=` sends only postings with no stage yet to `apply.yaml`.
- **`packs/apply.yaml`** — vars `subject_id`, `source_id`, `store_root`, `out_dir`, optional
  `questions`:
  1. fetch the posting by `source_id` through `source.pull` (title, company, url, text);
  2. the tailoring steps of `packs/tailor.yaml`, producing `tailored.json`, `cv.pdf`,
     `letter.pdf`, `review.pdf`;
  3. render `instructions.pdf` from a fourth template, with no model: the posting's `url`, which
     file goes where, each answer to paste, the review sheet's gaps to check before sending, and
     the exact `packs/sent.yaml` command to run afterwards;
  4. `stage.declare drafted`.
- **`packs/job-hunt.yaml`** becomes: `judge.each` → `policy.decide` → `stage.attach` →
  `pack.each apply.yaml` over `decision=allow,stage=` → `pack.each skip.yaml` over
  `decision=deny,stage=`. Ask rows are listed in its output; the user runs `apply.yaml` or
  `skip.yaml` on them by hand. Re-running it redrafts and re-skips nothing.

## Testing

Every test runs offline except the live run.

| What | How |
|---|---|
| `app.Decide` precedence | Tripped deal-breaker beats allow; deny beats allow in either order; unknown deal-breaker caps at ask; default ask; error row gets no decision |
| Policy parsing | Reuses Epic 6's operator tests; a malformed policy fails before any posting is scored |
| `stage.declare` | Future time refused; time before the current stage refused; same stage twice is a no-op; backdated `sent` accepted; each change is a new revision; index fields set |
| `pack.each` | Child failure isolated; nesting refused; per-row vars; multi-condition match with missing-as-empty; id order |
| `stage.attach` | Current stage attached; no stage is empty |
| `policy.suggest` | Groups repeated choices; respects N; the store is unchanged after it runs |
| Refusal | `http.request` refuses every method but GET and HEAD; architecture test over the registry |
| End to end | Stub feed and stub judge: one allowed posting drafted (all four PDFs, stage `drafted`), one denied (skip decision, stage `not_applied`), one asked (untouched); a second run changes nothing |
| Live | One run on a fabricated record and fabricated postings with `qwen2.5-coder:7b` and Typst, recorded in the increment note |

## The contract Epic 11 reads

| Field | Where | Meaning |
|---|---|---|
| `stage` | index, kind `stage`, path `applications/<subject_id>/stage.json` | The current stage, a pack-defined string |
| `<stage>_at` | same row | RFC 3339 time each stage was reached; `sent_at` is the clock |
| `declared_by` | the document | Who declared the current stage (`user` or the pack's name) |

## Deliberately not in this increment

| Out | Why |
|---|---|
| Submitting anything | Refused, with the reversal condition above |
| Inferring rules silently | 10.3: promotion is deliberate |
| A UI for asked postings | Epic 12; until then the user runs `apply.yaml` or `skip.yaml` |
| Reminders or nudges from the clock | Epic 11.2 reads the clock; this epic only starts it |
| Editing a declared stage other than by declaring a later one | Git history is the timeline; a correction is a later declaration |
| Parallel drafting | `pack.each` runs one child at a time; one person's board does not need more |
