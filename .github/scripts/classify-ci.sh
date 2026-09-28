#!/usr/bin/env bash
# Classifies a change set into a CI tier so build.yml can scale its work to
# what actually changed:
#
#   docs  - only docs/specs/notes/plans/markdown changed; skip build and test
#   code  - Go code, go.mod, go.sum, packs, or the workflow itself changed;
#           build + vet + race tests on ubuntu only
#   full  - cgo-dependent or platform-specific code changed (or the caller
#           forced it, e.g. a push to main); build + vet + race tests on the
#           full ubuntu/macos/windows matrix
#
# Usage:
#   classify-ci.sh <base> <head>              # diff base..head and classify
#   classify-ci.sh - - <tier> <reason>         # skip the diff, force <tier>
#
# Prints the changed files and the reason to stderr, the tier alone to
# stdout, and (when GITHUB_OUTPUT is set) writes tier=<tier> there.
set -euo pipefail

BASE="$1"
HEAD="$2"
FORCE_TIER="${3:-}"
FORCE_REASON="${4:-}"

# Packages whose tests depend on cgo (DuckDB, via marcboeker/go-duckdb/v2) or
# that carry platform-specific source files (proc_unix.go / proc_windows.go
# and friends), so they can only be proven by building and testing on every
# OS in the matrix, never by cross-compiling from one.
FULL_MATRIX_PATHS='^internal/adapters/outbound/duckindex/|^internal/pidlock/|^internal/adapters/outbound/acpagent/'

# A changed path counts as "docs" only if it lives under docs/ or ends in
# .md. Anything else (Go source, go.mod/go.sum, packs/, the workflow itself,
# or anything unrecognized) is treated as code, deliberately erring toward
# running tests rather than skipping them.
DOCS_ONLY_PATTERN='^docs/|\.md$'

if [ -n "$FORCE_TIER" ]; then
  TIER="$FORCE_TIER"
  echo "Forced tier: $TIER ($FORCE_REASON)" >&2
else
  CHANGED=$(git diff --name-only "$BASE" "$HEAD")

  if [ -z "$CHANGED" ]; then
    TIER=docs
    echo "No files changed between $BASE and $HEAD; treating as docs-only." >&2
  else
    echo "Changed files ($BASE..$HEAD):" >&2
    echo "$CHANGED" | sed 's/^/  /' >&2

    if echo "$CHANGED" | grep -Eq "$FULL_MATRIX_PATHS"; then
      TIER=full
      echo "Reason: touches cgo-dependent or platform-specific code -> full matrix." >&2
    elif echo "$CHANGED" | grep -Evq "$DOCS_ONLY_PATTERN"; then
      TIER=code
      echo "Reason: touches Go code, build files, or other non-docs paths -> ubuntu-only build+vet+test." >&2
    else
      TIER=docs
      echo "Reason: only docs/specs/notes/plans/markdown changed -> skip build and test." >&2
    fi
  fi
fi

echo "$TIER"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "tier=$TIER" >> "$GITHUB_OUTPUT"
fi
