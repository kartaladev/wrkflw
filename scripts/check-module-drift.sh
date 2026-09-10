#!/usr/bin/env bash
# Assert that no non-root workspace module raises a dependency version the ROOT
# module does not itself select.
#
# WHY. This repository is a Go workspace, and a workspace resolves the build by
# Minimal Version Selection over the UNION of its modules. So a version raised
# in examples/go.mod alone is the version the LIBRARY is compiled and tested
# against — internal/database, engine, persistence, everything — while root
# go.mod still declares the old one. A reviewer reads the diff as "just the
# demos" and it is not.
#
# The same is true of a `replace` in a non-root module, and for the same
# reason: in workspace mode every `use`d module is a main module, so its
# `replace` directives apply to the whole workspace build.
#
# NOTHING ELSE CATCHES IT, and that was measured rather than assumed. With a
# COMMITTED examples-only raise of github.com/jackc/pgx/v5 from v5.10.0 to
# v5.11.0 — examples/go.mod AND the go.sum that `go mod tidy` rewrites for it,
# committed together, which is the shape Dependabot produces — root go.mod
# untouched. (Commit examples/go.mod ALONE and this recipe does not reproduce:
# tidy rewrites examples/go.sum, so the tidy step's `git diff --exit-code`
# returns 1 and that step goes red for the wrong reason.)
#
#   go list -m github.com/jackc/pgx/v5                     -> v5.11.0
#   go list -deps ./internal/database/ | grep pgx          -> v5.11.0  (library code)
#   CI "Verify every module's go.mod is tidy"              -> EXIT 0
#   scripts/modules.sh                                     -> EXIT 0
#   go build ./... ./examples/...                          -> EXIT 0
#
# The tidy step cannot see it because a drifted go.mod is perfectly tidy —
# `go mod tidy` preserves a raise. scripts/modules.sh checks the module SET,
# never versions. The failure is silent in every existing gate.
#
# It also matters for what is NOT tracked: go.work.sum is gitignored, and while
# drift is 0 the workspace needs no go.work.sum at all. The moment drift
# appears, the checksum of the version compiled into the library is recorded
# only in that untracked file. This guard is what keeps that ignore safe.
#
# HOW. Two module graphs, compared:
#
#   workspace : go list -m all              (MVS over every workspace module)
#   root-only : GOWORK=off go list -m all   (MVS over the root module alone)
#
# keyed on path + selected version + replacement, so a `replace` that silently
# redirects the library's build is a difference rather than a match.
#
# Restricted to modules the ROOT's own graph contains — a module only the
# examples need is legitimately extra and cannot affect the library. Any
# version that differs between the two is, by construction, a version some
# other workspace module raised for the library. This measures the property
# directly rather than diffing `require` lines, so it also catches a raise
# arriving through a nested module's TRANSITIVE dependencies, which a
# line-by-line manifest comparison would miss.
#
# Usage: scripts/check-module-drift.sh   (run from anywhere)
set -euo pipefail

fail() { echo "check-module-drift: $*" >&2; exit 1; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${repo_root}"

# The comparison is only meaningful in workspace mode; modules.sh is the gate
# that says so, and it also fails when go.work and the go.mod files disagree.
"${repo_root}/scripts/modules.sh" > /dev/null

ws="$(mktemp)"; ro="$(mktemp)"; drift="$(mktemp)"
trap 'rm -f "${ws}" "${ro}" "${drift}"' EXIT

# --- the comparator, as a function, so the self-test below can exercise it ---
# Emits one line per module whose workspace-selected version differs from the
# version the root module would select alone. Restricted to the root's own set.
compare() {   # compare <root-only-file> <workspace-file>
  join -j 1 -o 0,1.2,2.2 \
    <(sort -k1,1 "$1") \
    <(sort -k1,1 "$2") \
  | awk '$2 != $3 { print $1, $2, $3 }'
}

# --- SELF-TEST: prove the comparator can fail, before trusting it to pass ----
#
# A comparison that passes because both inputs were empty, or because join
# matched nothing, is indistinguishable from a satisfied invariant. So run it
# once over a planted difference and require exactly that difference back.
st_ro="$(mktemp)"; st_ws="$(mktemp)"
printf '%s\n' 'example.com/a v1.0.0' 'example.com/b v2.0.0' > "${st_ro}"
printf '%s\n' 'example.com/a v1.0.0' 'example.com/b v2.1.0' 'example.com/c v9.9.9' > "${st_ws}"
st_out="$(compare "${st_ro}" "${st_ws}")"
rm -f "${st_ro}" "${st_ws}"
[ "${st_out}" = "example.com/b v2.0.0 v2.1.0" ] || fail "self-test failed: the comparator did not report a planted drift (got '${st_out}').
  Refusing to report a clean tree from an instrument that cannot detect a dirty one."

# --- the real measurement --------------------------------------------------
# The key is path + selected version + ANY REPLACEMENT. The replacement half is
# not decoration: `{{.Version}}` prints the SELECTED version, which a `replace`
# does not change, so a format without it compared v5.10.0 against v5.10.0 and
# printed "every one selects the same version" over a tree where the library
# was compiling v5.11.0. In workspace mode every `use`d module is a main
# module, so a `replace` in examples/go.mod redirects the ROOT library's build.
# That is worse than having no guard for the case: a green check-run becomes
# affirmative evidence for a property that does not hold.
#
# The local `replace github.com/kartaladev/wrkflw => ../` in examples/go.mod
# does not false-positive on this, and that direction was tested too: the
# examples module is not in the root module's own graph, and the comparison
# below is an inner join over that graph.
FMT='{{.Path}} {{.Version}}{{with .Replace}}=>{{.Path}}@{{.Version}}{{end}}'
go list -m -f "${FMT}" all > "${ws}"
GOWORK=off go list -m -f "${FMT}" all > "${ro}"

ws_n="$(grep -c . "${ws}" || true)"
ro_n="$(grep -c . "${ro}" || true)"
[ "${ws_n}" -gt 0 ] || fail "'go list -m all' listed no modules in workspace mode"
[ "${ro_n}" -gt 0 ] || fail "'GOWORK=off go list -m all' listed no modules for the root module"

compare "${ro}" "${ws}" > "${drift}"
shared="$(join -j 1 -o 0 <(sort -k1,1 "${ro}") <(sort -k1,1 "${ws}") | grep -c . || true)"
[ "${shared}" -gt 0 ] || fail "the two module graphs share no module at all (${ro_n} root-only, ${ws_n} workspace).
  That is a measurement failure, not a clean result — the comparison had nothing to compare."

if [ -s "${drift}" ]; then
  echo "ERROR: a non-root workspace module makes the build resolve differently from the root module." >&2
  echo "(a raised version, or a 'replace' redirecting one — the right-hand column shows which)" >&2
  echo >&2
  printf '  %-48s %-24s %s\n' 'MODULE' 'ROOT-ALONE SELECTS' 'WORKSPACE SELECTS' >&2
  while read -r m rv wv; do
    printf '  %-48s %-24s %s\n' "${m}" "${rv}" "${wv}" >&2
  done < "${drift}"
  echo >&2
  echo "This is not a demo-only change. go.work resolves the build by MVS over the" >&2
  echo "UNION of the workspace, so the 'workspace' column is the version the LIBRARY" >&2
  echo "is compiled and tested against — internal/database, engine, persistence — while" >&2
  echo "the root go.mod still declares the 'root-alone' column. Nothing else catches it:" >&2
  echo "a drifted go.mod is perfectly tidy, so the 'Verify every module's go.mod is tidy'" >&2
  echo "step passes, scripts/modules.sh checks the module set and never versions, and the" >&2
  echo "build is green. The checksum of the version actually used is recorded only in" >&2
  echo "go.work.sum, which is gitignored." >&2
  echo >&2
  echo "Fix by moving BOTH modules together — raise the root go.mod to the same version," >&2
  echo "or give it the same replace (deliberately, with the library's tests run against" >&2
  echo "it) — or revert the other module. Never leave the two apart." >&2
  exit 1
fi

echo "OK: ${shared} modules shared between the root module's graph and the workspace's; every one resolves to the same version and the same replacement. Root-alone graph ${ro_n} modules, workspace graph ${ws_n}."
