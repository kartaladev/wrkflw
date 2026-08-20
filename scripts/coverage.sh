#!/usr/bin/env bash
# Report Go test coverage EXCLUDING generated files, so the coverage floor in
# CLAUDE.md (Verification) is measured over hand-written code only.
#
# Generated files carry the standard `// Code generated ... DO NOT EDIT.` marker
# — today the mockgen `*_mock.go` doubles — and are 0%-covered boilerplate that
# otherwise drags a package's reported total far below its real coverage (e.g.
# the `service` package reads 49.9% raw vs 89.3% excluding the four mock files).
# This mirrors .golangci.yml's `exclusions: generated: lax`, which already drops
# the same files from linting. See ADR-0143.
#
# Usage:
#   scripts/coverage.sh              # run the race suite, then print the filtered total
#   scripts/coverage.sh cover.out    # reuse an existing coverprofile, print the filtered total
#
# Kept POSIX-bash-friendly (no mapfile/associative arrays) so it runs under the
# bash 3.2 that ships on macOS as well as CI's bash 5.
set -euo pipefail

# Explicit go test binary timeout (backlog 48). Go's implicit default is also 600s,
# but leaving it implicit meant nothing could enforce ADR-0184's sizing rule
# (eventuallyBudget x densest-package site count < timeout) — and nothing did.
# scripts/check-test-timeout.sh parses this literal and the identical one in
# .github/workflows/ci.yml, and fails if they disagree or if the rule is violated.
# Raising it is a deliberate decision: change BOTH, and say why in an ADR.
GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-600s}"

profile="${1:-}"
if [[ -z "${profile}" ]]; then
  profile="cover.out"
  go test -race -timeout="${GO_TEST_TIMEOUT}" -coverprofile="${profile}" ./...
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
