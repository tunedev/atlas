# Search contracts — design

**Supersedes nothing; changes the job-hunt pack's main path.** Atlas becomes a search interface
over job postings: you describe what you want, see it as explicit criteria, and get a ranked
table showing how each posting meets each criterion. The logprob judge
(`docs/specs/2026-09-22-judge-design.md`) leaves the main path; it is not deleted.

This spec fixes the three contracts that let four sessions build the pipeline in parallel:
the **criteria**, the **enriched posting** and the **match result**. It also specifies the one
piece of logic every session depends on, `Match`.

---

## What the user wants

Stated:

- Remove the grunt work of visiting job sources one by one.
- Make sourcing postings that match their preferences easy, with sources added as they find them.
- Show **stats on how a posting matches** their preferences, not an apply/skip verdict.
- Give each posting's application requirements in a ready-to-use form.
- Generate drafts only on demand, through the `claude` CLI, refined by `codex`. No Ollama.
- The search experience is modelled on glassdollar.com: query, visible criteria, a table of
  per-criterion checks, a match score, a shortlist.

Assumed (confirmed during design):

- Preferences become explicit criteria checked against structured fields.
- Fuzzy judgements are made once per posting at enrichment, never at search time.
- Unknown is the common outcome and is shown, never treated as failure.

## The pipeline

```
sources -> 1 ingest -> 2 enrich -> 3 index -> 4 match -> 5 search UI -> 6 act
           feed repo   per posting  SQLite     criteria   table          requirements card,
           public      model, once  FTS        pure, no   shortlist      drafts on demand
                                                model
```

| Stage | Where | Owner |
|---|---|---|
| 1 Ingest | `github.com/tunedev/feed`, unchanged schema v1 | Session A |
| 2 Enrich, 3 Index, 4 Match wiring | Atlas | Session B |
| 5 Search UI | Atlas web | Session C |
| 6 Act | Atlas packs, `agent.do` | Session D |
| Kubernetes | `infra/` | Session E, last |
| Contracts and `Match` | Atlas core | This spec |

The feed stays public and holds nothing about the user. Everything personal lives in the Atlas
store.

---

## 1. Criteria

A criteria set is one JSON document in the Atlas store. The default is `profile/criteria.json`;
a saved search is `searches/<name>.json`. Both are committed like every other store document.

```json
{
  "version": 1,
  "query": "senior backend, Go, remote or relocation, agent-native teams",
  "criteria": [
    {"id": "can-work-it", "statement": "Remote open to Nigeria, or relocation/visa offered",
     "must": true,
     "check": {"any": [
       {"field": "remote.eligible_regions", "op": "contains_any", "value": ["NG", "AFRICA", "EMEA", "WORLDWIDE"]},
       {"field": "relocation.offered", "op": "==", "value": true}]}},
    {"id": "focus", "statement": "Backend or platform role",
     "must": true,
     "check": {"field": "role.focus", "op": "in", "value": ["backend", "platform"]}},
    {"id": "timezone", "statement": "Within 3 hours of WAT, for remote roles",
     "weight": 1,
     "when": {"field": "remote.mode", "op": "==", "value": "remote"},
     "check": {"field": "remote.utc_offsets", "op": "overlaps", "value": [-2, 4]}},
    {"id": "seniority", "statement": "Senior or staff",
     "weight": 2,
     "check": {"field": "role.seniority", "op": "in", "value": ["senior", "staff"]}},
    {"id": "stack", "statement": "Go first, TypeScript second",
     "weight": 2,
     "check": {"field": "stack.languages", "op": "contains_any", "value": ["go"]},
     "partial": {"field": "stack.languages", "op": "contains_any", "value": ["typescript", "node"]}},
    {"id": "platform", "statement": "Kubernetes, cloud, observability",
     "weight": 1,
     "check": {"field": "stack.platforms", "op": "contains_any", "value": ["kubernetes", "gcp", "aws", "opentelemetry", "prometheus"]}},
    {"id": "agent-native", "statement": "Engineers already work with AI agents",
     "weight": 3,
     "check": {"field": "signals.agent_native", "op": "==", "value": true}}
  ],
  "display": ["salary", "role.employment_type"]
}
```

This example is the user's own criteria set and ships as `testdata/criteria.json`.

### Fields of a criterion

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Unique within the set; `[a-z0-9-]+` |
| `statement` | yes | The human sentence shown as the column header's tooltip |
| `must` | one of `must`, `weight` | `true`: an unmet outcome hides the posting |
| `weight` | one of `must`, `weight` | Positive integer: the preference's share of the score |
| `check` | yes | A condition (below) |
| `partial` | no, preferences only | A condition worth half the weight when `check` is not met |
| `when` | no | A condition; when it is `unmet` the criterion is `n/a` for that posting |

`display` lists enrichment field paths shown as plain columns and never scored.

### Conditions

A condition is either a comparison `{"field", "op", "value"}` or a combinator
`{"any": [...]}` / `{"all": [...]}` of conditions. `field` is a dotted path into the enriched
posting (section 2) and must name a criteria-checkable field.

| `op` | Field type | Met when |
|---|---|---|
| `==` | scalar | field equals value |
| `in` | scalar | field is one of the value list |
| `contains_any` | list | the field list and the value list share an element |
| `>=`, `<=` | number | numeric comparison |
| `overlaps` | `[min, max]` range | the field range and the value range intersect |

Each condition evaluates to `met`, `unmet` or `unknown`:

- A comparison is `unknown` when its field is `null`; otherwise `met` or `unmet`.
- `any`: `met` if any child is met; else `unknown` if any child is unknown; else `unmet`.
- `all`: `unmet` if any child is unmet; else `unknown` if any child is unknown; else `met`.

### Outcome of a criterion

1. If `when` is present and `unmet`: `n/a`. A `when` that is `unknown` does not skip the criterion.
2. If `check` is `met`: `met`.
3. If `partial` is present and `met`: `partial`.
4. If `check` or `partial` is `unknown`: `unknown`.
5. Otherwise: `unmet`.

### Validation

A criteria set is rejected as a whole when an `id` repeats, a criterion has both or neither of
`must` and `weight`, a `must` criterion has `partial`, a `field` is not criteria-checkable, an
`op` does not fit the field's type, or a value lies outside the field's vocabulary. Rejection
names every problem, not just the first.

Generating criteria from a query (Session C) produces this document and nothing else. A wish
it cannot express as a valid condition is returned to the user as "not checkable yet" with the
original wording, never dropped silently.

---

## 2. Enriched posting

Enrichment reads one feed posting (schema v1) and writes one document to the Atlas store:

`enriched/<source>~<board>~<external_id>/<content_hash>.json`

```json
{
  "version": 1,
  "posting_id": "greenhouse/gitlab/8490477002",
  "content_hash": "3f1c...",
  "enricher": {"engine": "claude-cli", "model": "claude-sonnet-5-5", "prompt_version": 1},
  "enriched_at": "2026-10-12T16:00:00Z",

  "role":       {"focus": "backend", "seniority": "staff", "employment_type": "full_time"},
  "remote":     {"mode": "remote", "eligible_regions": ["CA", "GB", "US"], "utc_offsets": null},
  "relocation": {"offered": null, "visa_sponsorship": null},
  "stack":      {"languages": ["ruby", "go"], "platforms": ["kubernetes", "gcp"]},
  "salary":     {"min": null, "max": null, "currency": null, "period": null},
  "signals":    {"agent_native": null},

  "requirements": {
    "apply_url": "https://job-boards.greenhouse.io/gitlab/jobs/8490477002",
    "documents": ["cv"],
    "screening_questions": ["Are you located in one of the listed countries?"],
    "deadline": null,
    "must_have_skills": ["Ruby on Rails", "PostgreSQL", "5+ years backend"],
    "nice_to_have_skills": ["Go"]
  },

  "evidence": {
    "role.focus": "Staff Backend Engineer",
    "role.seniority": "Staff Backend Engineer",
    "remote.mode": "Remote, Canada; Remote, United Kingdom; Remote, US",
    "remote.eligible_regions": "Remote, Canada; Remote, United Kingdom; Remote, US",
    "stack.languages": "Ruby on Rails and Go",
    "stack.platforms": "Kubernetes on GCP"
  }
}
```

### Criteria-checkable fields

| Path | Type | Vocabulary |
|---|---|---|
| `role.focus` | scalar | `backend`, `platform`, `fullstack`, `frontend`, `data`, `ml`, `mobile`, `security`, `other` |
| `role.seniority` | scalar | `junior`, `mid`, `senior`, `staff`, `principal`, `manager` |
| `role.employment_type` | scalar | `full_time`, `contract`, `part_time`, `internship` |
| `remote.mode` | scalar | `remote`, `hybrid`, `onsite` |
| `remote.eligible_regions` | list | ISO 3166-1 alpha-2 codes, and `AFRICA`, `EU`, `EMEA`, `AMERICAS`, `APAC`, `WORLDWIDE` |
| `remote.utc_offsets` | range | `[min, max]` whole-hour offsets, -12 to 14 |
| `relocation.offered` | boolean | |
| `relocation.visa_sponsorship` | boolean | |
| `stack.languages` | list | lowercase canonical names |
| `stack.platforms` | list | lowercase canonical names |
| `salary.min`, `salary.max` | number | |
| `salary.currency` | scalar | ISO 4217 |
| `salary.period` | scalar | `year`, `month`, `day`, `hour` |
| `signals.agent_native` | boolean | |

Region groups are literal: a posting saying `EMEA` is not expanded to `NG`. A criteria set
that wants EMEA roles lists `EMEA`.

`stack` lists are open sets normalised through an alias table in the contracts package
(`golang` to `go`, `node.js` to `node`, `ts` to `typescript`, `k8s` to `kubernetes`).

`requirements` fields are display-only: Session D renders them, criteria cannot check them, and
they carry no evidence.

### Rules

- **`null` means the posting does not say.** The enricher never fills a field by guessing. An
  empty list is stored as `null`.
- **Every non-null checkable field has evidence.** `evidence` maps the field path to a verbatim
  quote. After the model answers, enrichment checks each quote is a substring of the posting text
  (title, location and body, whitespace collapsed); a field whose quote is absent becomes `null`
  and its evidence is removed.
- **Vocabularies are enforced.** The model's output is validated against the enrichment JSON
  Schema. A value outside a vocabulary becomes `null`; a document that fails the schema as a whole
  is stored with every field `null` and the failure recorded in `enricher.error`.
- **Content hash.** `sha256` of title, location and body text joined by newlines. For a
  `rendered` posting, whose feed body is empty, the body is the page text Atlas fetched.
- **Enrich once.** A posting is re-enriched only when `content_hash` or `prompt_version`
  changes. Raising `prompt_version` re-enriches everything on purpose.
- **Postings with no text.** When the body is empty and the page cannot be fetched, the
  enrichment is written with every field `null`; the posting stays searchable with every
  criterion `unknown`.
- **Removed postings** leave search. Their enrichments are kept.
- **`version`** increments only on a breaking change, matching the feed's rule.

### The enricher port

`Enrich(ctx context.Context, p feed.Posting) (search.Enrichment, error)`, defined by Session B
at its consumer. Its first adapter runs `claude -p` with the enrichment JSON Schema. `codex exec`
and an HTTP model API are named second implementations. Every call has a timeout; only
transport failures retry; schema-invalid output is recorded, not retried.

---

## 3. Match result

```go
func Match(c Criteria, es []Enrichment) Result
```

`Match` is pure: no I/O, no model, no clock. Identical inputs give byte-identical output when
the result is marshalled.

```json
{
  "version": 1,
  "criteria_hash": "9a0b...",
  "checked": 761,
  "hidden": 703,
  "rows": [
    {
      "posting_id": "ashby/linear/abc123",
      "score": 0.83,
      "known": 0.71,
      "must_unknown": ["can-work-it"],
      "results": [
        {"id": "can-work-it", "outcome": "unknown"},
        {"id": "focus", "outcome": "met", "evidence": "Backend Engineer"},
        {"id": "timezone", "outcome": "n/a"},
        {"id": "stack", "outcome": "partial", "evidence": "TypeScript, Node"},
        {"id": "agent-native", "outcome": "met", "evidence": "we use Claude Code daily"}
      ],
      "display": {"salary": null, "role.employment_type": "full_time"}
    }
  ]
}
```

Title, company, URL and dates are not copied into rows; the UI joins them from the index by
`posting_id`.

### Rules

- **Hidden.** A posting with any must-have `unmet` is counted in `hidden` and has no row.
- **Score.** Over preferences that are not `n/a`: (weight of `met` + half the weight of
  `partial`) / total weight. `unknown` and `unmet` add nothing. With no applicable preference,
  the score is 0.
- **Known.** The share of applicable preference weight whose outcome is `met`, `partial` or
  `unmet`.
- **Order.** Rows with an empty `must_unknown` first, then rows with unknown must-haves. Within
  each group: score descending, known descending, then `posting_id` ascending.
- **Evidence** on a result is the enrichment's evidence for the field that decided it: for a
  combinator, the first child that produced the outcome.
- **`criteria_hash`** is the `sha256` of the criteria document as stored, so a saved result
  can be matched to the criteria that produced it.
- Only open, enriched postings are passed to `Match`. Counting postings still awaiting
  enrichment is the index's job.

---

## 4. Deliverable of this spec

A contracts package at `internal/core/search`. Its non-test code imports only the standard
library; its tests validate against the schemas with `github.com/google/jsonschema-go`, already
in the module graph as an indirect dependency.

| Item | Content |
|---|---|
| Types | `Criteria`, `Criterion`, `Condition`, `Enrichment`, `Result`, `Row`, `Outcome` |
| `Validate(Criteria) error` | Every rule in section 1's Validation, reporting all problems |
| `Match(Criteria, []Enrichment) Result` | Section 3 |
| `Normalize` for stack aliases | Section 2's alias table |
| JSON Schemas | `schema/criteria.json`, `schema/enrichment.json`, `schema/result.json`, embedded with `go:embed` so adapters and tests share them |
| Example files | `testdata/criteria.json` (the set above), three enrichments, and the expected `result.json` |

### Tests that decide it is done

Golden cases through `Match`, using the example files:

1. A must-have `unmet` hides the posting and increments `hidden`.
2. A must-have `unknown` keeps the posting, lists it in `must_unknown`, and orders it after full passes.
3. `partial` scores half its weight.
4. A criterion whose `when` is `unmet` is `n/a` and leaves both score and known.
5. A posting with every field `null` has score 0, known 0, and a row.
6. `any` over a `null` field and a met field is `met`; `all` over the same is `unknown`.
7. Marshalling the result twice gives identical bytes; shuffling the input does not change it.
8. Every example file validates against its JSON Schema, and the expected result equals `Match`'s output.
9. `Validate` rejects a criteria set with an unknown field, a bad vocabulary value, and a `must` with `partial`, naming all three.

The existing architecture test (`internal/arch/arch_test.go`) covers `internal/core/...`, so an
adapter import into the package fails the build.

## Out of scope

- Model evaluation of free-text criteria at search time.
- Learned weights, and learning from shortlist or skip clicks.
- Company-level signals shared across postings; `agent_native` is per posting.
- De-duplication across sources (Session A).
- Embeddings and semantic search.
- Pagination of results.
- Deploying any of this to Kubernetes (Session E).
