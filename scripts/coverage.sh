#!/usr/bin/env bash
# Report Go test coverage EXCLUDING generated files, so the coverage floor in
# CLAUDE.md (Verification) is measured over hand-written code only.
#
# Generated files carry the standard `// Code generated ... DO NOT EDIT.` marker
# — today the mockgen `*_mock.go` doubles — and are 0%-covered boilerplate that
# otherwise drags a package's reported total far below its real coverage (e.g.
# the `service` package reads 49.9% raw vs 89.3% excluding the four mock files).
# This mirrors .golangci.yml's `exclusions: generated: lax`, which already drops
# the same files from linting.
#
# Usage:
#   scripts/coverage.sh              # run the race suite, then print the filtered total
#   scripts/coverage.sh cover.out    # reuse an existing coverprofile, print the filtered total
#
# MULTI-MODULE NOTE. Coverprofile rows carry full IMPORT paths
# (github.com/kartaladev/wrkflw/examples/migrate/main.go:...), while the
# generated-file list below holds repo-RELATIVE paths (examples/migrate/...).
# The filter is `grep -vF`, a fixed SUBSTRING match, and every module path here
# is the repo path plus the module's directory, so the relative path is always
# a suffix of the import path and the filter keeps working across modules
# unchanged. It would break only if a module's path stopped following that
# rule. The `grep -r . ` that builds the list walks the whole worktree, so it
# already sees generated files in every module (today: none under examples/).
#
# Kept POSIX-bash-friendly (no mapfile/associative arrays) so it runs under the
# bash 3.2 that ships on macOS as well as CI's bash 5.
set -euo pipefail

# Explicit go test binary timeout. Go's implicit default is also 600s, but
# leaving it implicit meant nothing could enforce the sizing rule
# (eventuallyBudget x densest-package site count < timeout) — and nothing did.
# scripts/check-test-timeout.sh parses this literal and the identical one in
# .github/workflows/ci.yml, and fails if they disagree or if the rule is violated.
# Raising it is a deliberate decision: change BOTH, and record why.
GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-600s}"

profile="${1:-}"
if [[ -z "${profile}" ]]; then
  profile="cover.out"
  # `./...` alone would measure the ROOT MODULE ONLY. This repository is a Go
  # workspace (go.work: `.` and `./examples`), and a workspace does not widen
  # `./...` — measured, 69 packages here against 112 across both patterns. A
  # coverage total computed over a subset reads exactly like one computed over
  # the whole tree, so the pattern list comes from scripts/modules.sh, which
  # refuses to emit an empty or short one.
  #
  # ONE invocation, so ONE profile: `go test -coverprofile` spanning patterns
  # from several modules writes a single merged coverprofile (verified — rows
  # from both modules, one `mode:` header). No per-module profiles and no merge
  # step, which also keeps the measured DOMAIN identical to what a pre-split
  # `./...` covered, so the reported number stays comparable across the split.
  patterns="$("$(dirname "${BASH_SOURCE[0]}")/modules.sh")"
  go test -race -timeout="${GO_TEST_TIMEOUT}" -coverprofile="${profile}" ${patterns}
fi

if [[ ! -f "${profile}" ]]; then
  echo "coverage.sh: profile not found: ${profile}" >&2
  exit 1
fi

genlist="$(mktemp)"
filtered="$(mktemp)"
trap 'rm -f "${genlist}" "${filtered}"' EXIT

# Repo-relative paths of generated Go files (the standard Go marker), each with a
# trailing ':' so the filter below matches the coverprofile path boundary and not
# an unrelated file that merely shares the suffix. The trailing `|| true` keeps
# `set -e`/`pipefail` from aborting when grep finds no generated files (exit 1) —
# an empty genlist then takes the cp fallback.
grep -rlE '^// Code generated .* DO NOT EDIT\.' --include='*.go' . | sed -e 's#^\./##' -e 's#$#:#' > "${genlist}" || true

if [[ -s "${genlist}" ]]; then
  # Coverprofile rows are "<import-path>/<file>.go:<lines> <stmts> <count>"; drop
  # every row whose path (up to the ':') is a generated file. The `mode:` header
  # line matches no pattern, so it is preserved as the first line.
  grep -vFf "${genlist}" "${profile}" > "${filtered}" || true
else
  cp "${profile}" "${filtered}"
fi

go tool cover -func="${filtered}" | tail -1
