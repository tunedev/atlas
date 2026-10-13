# Search contracts — design

**Supersedes nothing; changes the job-hunt pack's main path.** Atlas becomes a search interface
over job postings: you describe what you want, see it as explicit criteria, and get a ranked
table showing how each posting meets each criterion. The logprob judge
(`docs/specs/2026-09-22-judge-design.md`) leaves the main path; it is not deleted.

This spec fixes the three contracts that let four sessions build the pipeline in parallel:
the **criteria**, the **enriched document** and the **match result**. It also specifies the one
piece of logic every session depends on, `Match`.

The contracts are generic. The harness searches *subjects* described by *fields*; a pack
declares what the fields are. Job postings are the first pack, `packs/job-search/`. A second
kind of search (apartments, conferences) is a new pack and needs no Go.

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

### Harness and pack

| Harness (`internal/core/search`, generic) | Pack (`packs/job-search/`, config) |
|---|---|
| Criteria format, conditions, operators, outcomes | `fields.json`: every field the enricher fills, its kind and vocabulary |
| Field declaration format and its loader | `criteria.example.json`: the user's criteria set |
| Enriched document envelope | `examples/`: three enrichments and the expected match result |
| `Match`, scoring, ordering, evidence | |
| The enrichment JSON Schema, generated from a pack's declarations | |

The architecture test `TestGoTreeIsFreeOfUseCaseVocabulary` holds for the whole package: no
job vocabulary appears under `internal/`.

---

## 1. Field declarations

A pack declares its fields in one JSON document. The enricher fills exactly these fields;
criteria may check the checkable ones; the result may display any of them.

```json
{
  "version": 1,
  "fields": {
    "role.focus":              {"kind": "scalar", "enum": ["backend", "platform", "fullstack", "frontend", "data", "ml", "mobile", "security", "other"]},
    "remote.eligible_regions": {"kind": "list", "pattern": "^([A-Z]{2}|AFRICA|EU|EMEA|AMERICAS|APAC|WORLDWIDE)$"},
    "remote.utc_offsets":      {"kind": "range", "min": -12, "max": 14},
    "stack.languages":         {"kind": "list", "canonical": true},
    "salary.min":              {"kind": "number"},
    "relocation.offered":      {"kind": "bool"},
    "requirements.apply_url":  {"kind": "text"}
  },
  "aliases": {"golang": "go", "node.js": "node", "k8s": "kubernetes"}
}
```

| Kind | Value | Checkable | Ops |
|---|---|---|---|
| `scalar` | a string | yes | `==`, `in` |
| `bool` | `true` or `false` | yes | `==` |
| `number` | a number | yes | `==`, `>=`, `<=` |
| `list` | a non-empty list of strings | yes | `contains_any` |
| `range` | `[min, max]` integers | yes | `overlaps` |
| `text` | a string | no, display only | |
| `texts` | a non-empty list of strings | no, display only | |

A `scalar` or `list` field may restrict its strings with `enum` or `pattern` (an RE2 regular
expression), not both. `canonical: true` requires every string to be its own canonical form:
trimmed, lowercased and mapped through `aliases`. A `range` field requires `min` and `max`
bounds. A field path is dotted lowercase segments, `[a-z0-9_]+`; no path is a prefix of
another.

Every field may be `null`, which means the subject does not say.

Loading rejects a document with an unknown key, an unknown kind, both `enum` and `pattern`, an invalid
pattern, `enum`, `pattern` or `canonical` on a kind other than `scalar` and `list`, `min` or `max`
on a kind other than `range`, a range without bounds or with `min` above `max`, or a path that is
malformed or prefixes another, or an alias whose key or target is not trimmed lowercase or whose
target is itself an alias, naming every problem. Keys match case-sensitively, and trailing data
after the document is an error.

`Normalize(name)` returns the canonical form of a string under a declaration's aliases. The
enricher normalises the model's `canonical` list values with it before validation.

---

## 2. Criteria

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

This example is the user's own criteria set and ships as `packs/job-search/criteria.example.json`.

### Fields of a criterion

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Unique within the set; `[a-z0-9-]+` |
| `statement` | yes | The human sentence shown as the column header's tooltip |
| `must` | one of `must`, `weight` | `true`: an unmet outcome hides the subject |
| `weight` | one of `must`, `weight` | Positive integer: the preference's share of the score |
| `check` | yes | A condition (below) |
| `partial` | no, preferences only | A condition worth half the weight when `check` is not met |
| `when` | no | A condition; when it is `unmet` the criterion is `n/a` for that subject |

`display` lists paths shown as plain columns and never scored. A display path is a declared
field or a prefix of declared fields (`salary` shows the `salary.*` group).

### Conditions

A condition is either a comparison `{"field", "op", "value"}` or a combinator
`{"any": [...]}` / `{"all": [...]}` of conditions. `field` must name a checkable declared field,
and `op` must be one its kind allows.

| `op` | Met when |
|---|---|
| `==` | field equals value |
| `in` | field is one of the value list |
| `contains_any` | the field list and the value list share an element |
| `>=`, `<=` | numeric comparison |
| `overlaps` | the field range and the value range intersect |

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

Criteria are validated against a field declaration. A set is rejected as a whole when it has an
unknown key, an `id` repeats, a `statement` is blank, a criterion has both or neither of `must` and `weight`, a `must` criterion has
`partial`, a `field` is not a checkable declared field, an `op` does not fit the field's kind,
a value lies outside the field's `enum`, `pattern`, `canonical` form or range bounds, or a
`display` path is not declared. Rejection names every problem, not just the first.

Generating criteria from a query (Session C) produces this document and nothing else. A wish
it cannot express as a valid condition is returned to the user as "not checkable yet" with the
original wording, never dropped silently.

---

## 3. Enriched document

Enrichment reads one subject (for the job pack, a feed posting, schema v1) and writes one
document to the Atlas store:

`enriched/<subject_id>/<content_hash>.json`, with `/` in the subject id written as `~`.

```json
{
  "version": 1,
  "subject_id": "greenhouse/gitlab/8490477002",
  "content_hash": "3f1c...",
  "enricher": {"engine": "claude-cli", "model": "claude-sonnet-5-5", "prompt_version": 1},
  "enriched_at": "2026-10-12T16:00:00Z",
  "values": {
    "role":       {"focus": "backend", "seniority": "staff", "employment_type": "full_time"},
    "remote":     {"mode": "remote", "eligible_regions": ["CA", "GB", "US"], "utc_offsets": null},
    "stack":      {"languages": ["ruby", "go"], "platforms": ["kubernetes", "gcp"]},
    "requirements": {"apply_url": "https://job-boards.greenhouse.io/gitlab/jobs/8490477002"}
  },
  "evidence": {
    "role.focus": "Staff Backend Engineer",
    "stack.languages": "Ruby on Rails and Go"
  }
}
```

`values` nests the declared fields by path segment. `evidence` maps a checkable field path to
a verbatim quote. The envelope is the harness's; everything inside `values` is the pack's.

### The enrichment JSON Schema

`Schema(fields)` generates the JSON Schema of `values` from a declaration: every declared field
is required and nullable, `enum`, `pattern` and range bounds become schema keywords, a
`canonical` list rejects uppercase, and no other property is allowed. The enricher hands this
schema to the model (`claude -p --json-schema`) and validates the answer against it. The
envelope has its own embedded schema, `schema/enrichment.json`.

### The job pack's fields

`packs/job-search/fields.json` declares:

| Path | Kind | Vocabulary |
|---|---|---|
| `role.focus` | scalar | `backend`, `platform`, `fullstack`, `frontend`, `data`, `ml`, `mobile`, `security`, `other` |
| `role.seniority` | scalar | `junior`, `mid`, `senior`, `staff`, `principal`, `manager` |
| `role.employment_type` | scalar | `full_time`, `contract`, `part_time`, `internship` |
| `remote.mode` | scalar | `remote`, `hybrid`, `onsite` |
| `remote.eligible_regions` | list | ISO 3166-1 alpha-2 codes, and `AFRICA`, `EU`, `EMEA`, `AMERICAS`, `APAC`, `WORLDWIDE` |
| `remote.utc_offsets` | range | whole-hour offsets, -12 to 14 |
| `relocation.offered` | bool | |
| `relocation.visa_sponsorship` | bool | |
| `stack.languages` | list | canonical |
| `stack.platforms` | list | canonical |
| `salary.min`, `salary.max` | number | |
| `salary.currency` | scalar | ISO 4217, `^[A-Z]{3}$` |
| `salary.period` | scalar | `year`, `month`, `day`, `hour` |
| `signals.agent_native` | bool | |
| `requirements.apply_url`, `requirements.deadline` | text | |
| `requirements.documents`, `requirements.screening_questions`, `requirements.must_have_skills`, `requirements.nice_to_have_skills` | texts | |

Aliases: `golang`→`go`, `node.js` and `nodejs`→`node`, `ts`→`typescript`, `js`→`javascript`,
`k8s`→`kubernetes`, `postgres`→`postgresql`, `gcloud`→`gcp`, `otel`→`opentelemetry`,
`amazon web services`→`aws`.

Region groups are literal: a posting saying `EMEA` is not expanded to `NG`. A criteria set that
wants EMEA roles lists `EMEA`. `requirements.*` fields are what Session D renders.

### Rules

- **`null` means the subject does not say.** The enricher never fills a field by guessing. An
  empty list is stored as `null`.
- **Every non-null checkable field has evidence.** After the model answers, enrichment checks
  each quote is a substring of the subject text (for the job pack: title, location and body,
  whitespace collapsed); a field whose quote is absent becomes `null` and its evidence is
  removed.
- **Vocabularies are enforced.** A value outside its field's vocabulary becomes `null`; an
  answer that fails the schema as a whole is stored with every field `null` and the failure
  recorded in `enricher.error`.
- **Content hash.** `sha256` of the subject text. For the job pack: title, location and body
  joined by newlines; for a `rendered` posting, whose feed body is empty, the body is the page
  text Atlas fetched.
- **Enrich once.** A subject is re-enriched only when `content_hash` or `prompt_version`
  changes. Raising `prompt_version` re-enriches everything on purpose.
- **Subjects with no text.** When there is no text to read, the enrichment is written with
  every field `null`; the subject stays searchable with every criterion `unknown`.
- **Removed subjects** leave search. Their enrichments are kept.
- **`version`** increments only on a breaking change, matching the feed's rule.

### The enricher port

`Enrich(ctx context.Context, p feed.Posting) (search.Enrichment, error)`, defined by Session B
at its consumer. Its first adapter runs `claude -p` with the generated schema. `codex exec`
and an HTTP model API are named second implementations. Every call has a timeout; only
transport failures retry; schema-invalid output is recorded, not retried.

---

## 4. Match result

```go
func Match(c Criteria, es []Enrichment) Result
```

`Match` is pure: no I/O, no model, no clock. Identical inputs give byte-identical output when
the result is marshalled. It needs no field declaration: `ParseCriteria` has already checked the
criteria against one.

```json
{
  "version": 1,
  "criteria_hash": "9a0b...",
  "checked": 761,
  "hidden": 703,
  "rows": [
    {
      "subject_id": "ashby/linear/abc123",
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
      "display": {"salary": {"min": null, "max": null, "currency": null, "period": null}, "role.employment_type": "full_time"}
    }
  ]
}
```

Title, company, URL and dates are not copied into rows; the UI joins them from the index by
`subject_id`.

### Rules

- **Hidden.** A subject with any must-have `unmet` is counted in `hidden` and has no row.
- **Score.** Over preferences that are not `n/a`: (weight of `met` + half the weight of
  `partial`) / total weight. `unknown` and `unmet` add nothing. With no applicable preference,
  the score is 0.
- **Known.** The share of applicable preference weight whose outcome is `met`, `partial` or
  `unmet`.
- **Order.** Rows with an empty `must_unknown` first, then rows with unknown must-haves. Within
  each group: score descending, known descending, then `subject_id` ascending.
- **Evidence** on a result is the enrichment's evidence for the field that decided it: for a
  combinator, the first child that produced the outcome.
- **Display** values are copied from `values` as stored: a field's value, or the object under a
  prefix.
- **A value of the wrong type** (a stored enrichment that no longer fits its declaration), or a
  range whose min is above its max, is treated as `null`, so the comparison is `unknown`, never a crash.
- **`criteria_hash`** is the `sha256` of the criteria document as stored, so a saved result
  can be matched to the criteria that produced it.
- Only open, enriched subjects are passed to `Match`. Counting subjects still awaiting
  enrichment is the index's job.

---

## 5. Deliverable of this spec

A contracts package at `internal/core/search`, and the job pack's declaration and examples.
The package's non-test code imports only the standard library; its tests validate against the
schemas with `github.com/google/jsonschema-go`, already in the module graph.

| Item | Content |
|---|---|
| Types | `Fields`, `Field`, `Criteria`, `Criterion`, `Condition`, `Enrichment`, `Result`, `Row`, `Outcome` |
| `ParseFields([]byte) (Fields, error)` | Section 1, reporting all problems |
| `Fields.Normalize(string) string` | Section 1's aliases |
| `Fields.Schema() []byte` | Section 3's generated JSON Schema of `values` |
| `ParseCriteria([]byte, Fields) (Criteria, error)` | Section 2's validation, reporting all problems, and the hash |
| `Match(Criteria, []Enrichment) Result` | Section 4 |
| Envelope schemas | `schema/criteria.json`, `schema/enrichment.json`, `schema/result.json`, embedded with `go:embed` |
| Job pack | `packs/job-search/fields.json`, `criteria.example.json`, `examples/` with three enrichments and the expected `result.json` |

The package's own tests use a deliberately unrelated toy domain, so they show a pack's fields
need no Go. One test globs `packs/*/fields.json` and checks every pack that ships examples,
naming no pack.

### Tests that decide it is done

Through `Match`, on the toy domain:

1. A must-have `unmet` hides the subject and increments `hidden`.
2. A must-have `unknown` keeps the subject, lists it in `must_unknown`, and orders it after full passes.
3. `partial` scores half its weight.
4. A criterion whose `when` is `unmet` is `n/a` and leaves both score and known.
5. A subject with every field `null` has score 0, known 0, and a row.
6. `any` over a `null` field and a met field is `met`; `all` over the same is `unknown`.
7. Marshalling the result twice gives identical bytes; shuffling the input does not change it.
8. Rows order by score, then known, then `subject_id`.
9. A value of the wrong type makes its comparison `unknown`.

Declarations, criteria and schemas:

10. `ParseFields` rejects a bad kind, `enum` with `pattern`, a range without bounds and a path that prefixes another, naming all four.
11. `ParseCriteria` rejects an undeclared field, a value outside the vocabulary, and a `must` with `partial`, naming all three.
12. The generated schema accepts a valid `values` and rejects a value outside each kind of vocabulary.

Every shipped pack:

13. Its `fields.json` parses, its `criteria.example.json` parses against it, each `examples/` enrichment validates against the envelope and generated schemas, and `Match`'s output equals `examples/result.json` and validates against the result schema.

The existing architecture tests cover `internal/core/...`: an adapter import or a job word in
the package fails the build.

## Out of scope

- Model evaluation of free-text criteria at search time.
- Learned weights, and learning from shortlist or skip clicks.
- Company-level signals shared across postings; `agent_native` is per posting.
- De-duplication across sources (Session A).
- Embeddings and semantic search.
- Pagination of results.
- Deploying any of this to Kubernetes (Session E).
