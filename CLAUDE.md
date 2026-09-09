# Atlas

The Go platform repo of the Forge. Personal tooling built on a resource-kernel
architecture: a small uniform resource interface, agents over it, and domain slices on top.

**The engineering tenets live in `../CLAUDE.md` and govern this repo.** Hexagonal
architecture, the five cloud native attributes, stability and concurrency patterns, the
fallacies, total configurability, and the pragmatic rules are all defined there and are not
restated here. This file covers only what is specific to Atlas.

## Status

Design, no code. `internal/` does not exist. Step 3 in the Forge build order is unblocked:
`infra/` and the `hello` workload are built, so Atlas deploys onto a platform already
proven end to end.

The first slice is job hunting, modelled on the fork at
`github.com/tunedev/ai-job-search`, using the crudest storage that works. The resource
kernel is extracted afterwards, shaped by what that slice actually needed rather than by
what we imagined it would need.

## Layout

Structure follows the hexagonal layout in `../CLAUDE.md` Tenet 1: `internal/core` owning
its ports, `internal/adapters` implementing them, and `cmd/<binary>/main.go` as the sole
composition root.

Specs go in `docs/specs/`, increment notes in `docs/notes/`.
