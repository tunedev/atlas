# Search contracts: a generic search core with the job fields in a pack

This increment ships `internal/core/search` and `packs/job-search/`, the contracts four parallel
sessions build against (`docs/specs/2026-10-12-search-contracts-design.md`).

## The pattern

The harness/pack split, applied to search. Go owns the criteria language, the field declaration
format, the enriched document envelope, `Match`, and the JSON Schema generated from a
declaration. The pack owns the field table: paths, kinds, vocabularies and aliases, in
`packs/job-search/fields.json`.

One declaration drives three things: criteria validation, the schema the model fills, and what
the result may display. They cannot drift because there is nothing to keep in sync. The
package's own tests run on a toy bakery domain, which shows a new kind of search needs no Go.

## What surprised me

- The first spec put the job field table in Go, a typed struct with `salary` and `relocation`
  fields, and the design review did not catch it. `TestGoTreeIsFreeOfUseCaseVocabulary` caught it
  on the first prototype. A deny-list of words enforced a design rule better than reading the
  design did.
- `packs/*/testdata/*.json` was already reserved: `TestPackTemplateFixturesRender` decodes every
  file there as a render fixture. The search examples moved to `examples/`.
- `json.Unmarshal` ignores unknown keys, so a misspelt `"partail"` would have silently dropped a
  preference. Criteria and declarations now decode strictly.

## What I would do differently

Prototype against the architecture tests before writing a spec's deliverable section. The
spec's error was cheap to find with code and was missed in prose.

## What I still do not understand

- How well a model fills `canonical` lists before `Normalize` runs, and whether the alias table
  needs to grow from real postings. Session B measures this.
- How often a posting gives any evidence for `signals.agent_native`. If the answer is almost
  never, that preference is `unknown` on every row and adds no ranking signal.
