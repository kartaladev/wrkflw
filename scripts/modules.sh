#!/usr/bin/env bash
# Print one Go package pattern per module in this workspace, one per line:
#
#   ./...
#   ./examples/...
#
# Every command that must cover the WHOLE repository takes its package
# arguments from here.
#
# WHY THIS EXISTS. This repository is a Go WORKSPACE (go.work), not a single
# module, and a workspace does NOT widen the `./...` pattern. Measured at the
# commit that added this script, with both `.` and `./examples` in go.work:
#
#   go list ./...                    -> 69 packages, 0 of them under examples/
#   go list ./... ./examples/...     -> 112 packages
#
# So `go build ./...`, `go test ./...` and `govulncheck ./...` at the repo root
# do not FAIL when a module is added — they succeed, having ignored it. That is
# the failure this script exists to prevent, and it is invisible in a green log.
#
# NON-VACUITY, WHICH IS THE WHOLE POINT. A loop or an argument list derived from
# an empty enumeration exits 0 too: it is the same silent pass in a new costume.
# So this script never prints a list it cannot vouch for. It fails, loudly and
# by name, when:
#
#   1. the go command is not in workspace mode — outside it `go list -m` reports
#      a single module and every caller silently narrows to the root;
#   2. go.work's `use` set and the go.mod files on disk disagree in either
#      direction, so adding a module without `go work use` is a red CI run
#      rather than a package set that quietly shrinks;
#
#      GATE 2 ALSO PROTECTS A CALLER THAT NEVER RUNS THIS SCRIPT. CodeQL's Go
#      autobuilder is workspace-aware and reads go.work itself — measured, base
#      vs head of the commit that added it: the base log says "Found no go.work
#      files ... Found 1 go.mod file(s)" and extracts with [./...]; the head log
#      says "Found go.work file(s) in: go.work ... Found 2 go.mod file(s)" and
#      extracts with [... ./... ./examples/...]; both extract the same 112
#      wrkflw packages, 43 of them under examples/, and both report "CodeQL
#      scanned 359 out of 974 Go files". So examples/ stays in CodeQL's view
#      BECAUSE go.work lists it. A module on disk but absent from go.work would
#      go unscanned with `analyze (go)` still green, and nothing in codeql.yml
#      can notice — gate 2 is the only thing that does. Do not weaken it into a
#      one-directional check;
#   3. fewer than MIN_MODULES modules resolve;
#   4. the root module is missing from the result.
#
# CALLERS MUST CAPTURE INTO A VARIABLE, NOT INLINE THE SUBSTITUTION:
#
#   mods="$(scripts/modules.sh)"   # under `set -e` this DOES abort on failure
#   go build ${mods}               # unquoted on purpose: split into N patterns
#
# `go build $(scripts/modules.sh)` is wrong. A command substitution that fails
# inside a simple command does not trip `set -e`, and `go build` with no
# arguments builds the current directory's package and exits 0 — turning the
# guard back into the silent pass it was written to remove.
#
# Usage:
#   scripts/modules.sh                 # print the patterns, one per line
#   MIN_MODULES=3 scripts/modules.sh   # raise the floor when a module is added
#
# Kept bash-3.2-friendly (no mapfile, no associative arrays) so it runs under
# the bash that ships on macOS as well as CI's bash 5.
set -euo pipefail

MIN_MODULES="${MIN_MODULES:-2}"   # root + examples

fail() { echo "modules.sh: $*" >&2; exit 1; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${repo_root}"

[[ -f go.work ]] || fail "no go.work at ${repo_root}.
  This repository is a Go workspace and every whole-repository command derives
  its package patterns from it. Without one, '${repo_root}/examples' and every
  later module are built, tested and scanned by nothing."

# --- 1. workspace mode must actually be on -----------------------------------
#
# `go env GOWORK` prints the path of the loaded go.work; it prints the literal
# string "off" when GOWORK=off is set, and the empty string when no go.work was
# found. BOTH non-path answers must be rejected, and the "off" case is the one
# worth spelling out: this was written as an emptiness test first, and that test
# passed on the string "off". Step 2 then failed anyway — but with a message
# blaming go.work for not listing a module go.work does list, which sends the
# reader to the wrong file. Fail for the real reason.
gowork="$(go env GOWORK)"
if [[ -z "${gowork}" || "${gowork}" == "off" ]]; then
  fail "the go command is not in workspace mode (go env GOWORK = '${gowork:-<empty>}').
  Expected ${repo_root}/go.work to be loaded. Outside workspace mode 'go list -m'
  reports a single module, so every caller would silently narrow to the root
  module — dropping every package outside it while still exiting 0."
fi

# --- 2. go.work's view and the disk must agree -------------------------------
#
# The declared set comes from the go command rather than from parsing go.work,
# so what is checked is what the toolchain will actually use.
declared="$(go list -m -f '{{.Dir}}' \
  | sed -e "s#^${repo_root}\$#.#" -e "s#^${repo_root}/#./#" \
  | sort)"

# A filesystem walk, not `git ls-files`, so a module added but not yet committed
# is still seen — the mismatch below is meant to fire while the change is being
# written, not only after it lands. `.claude/worktrees/` holds sibling checkouts
# of this same repository, each with its own go.mod, so it must be pruned or the
# on-disk set is another branch's rather than ours.
ondisk="$(find . -name go.mod \
            -not -path './.git/*' \
            -not -path './.claude/*' \
            -not -path '*/testdata/*' \
            -not -path '*/vendor/*' \
          | sed -e 's#^\./go\.mod$#.#' -e 's#/go\.mod$##' \
          | sort)"

unused="$(comm -13 <(printf '%s\n' "${declared}") <(printf '%s\n' "${ondisk}"))"
phantom="$(comm -23 <(printf '%s\n' "${declared}") <(printf '%s\n' "${ondisk}"))"

if [[ -n "${unused}" ]]; then
  fail "go.mod on disk but not used by go.work:
$(printf '  %s\n' ${unused})
  Add it with 'go work use <dir>' and raise MIN_MODULES in the CI steps that
  read this script. Until then its packages are built, tested, vetted and
  scanned by nothing, and every job still exits 0."
fi

if [[ -n "${phantom}" ]]; then
  fail "go.work uses a directory with no go.mod:
$(printf '  %s\n' ${phantom})
  Drop it with 'go work edit -dropuse <dir>'."
fi

# --- 3. the floor ------------------------------------------------------------
count="$(printf '%s\n' "${declared}" | grep -c .)"
if (( count < MIN_MODULES )); then
  fail "found ${count} module(s), expected at least ${MIN_MODULES}.
  An argument list built from a short enumeration does its work on a subset and
  exits 0, which reads exactly like a run that covered everything. If a module
  was removed on purpose, lower MIN_MODULES in the same commit."
fi

# --- 4. the root module must be in the result --------------------------------
printf '%s\n' "${declared}" | grep -qx '\.' \
  || fail "the root module is not among the resolved modules:
$(printf '  %s\n' ${declared})
  Every caller would then analyse only the submodules and report success for
  the library itself."

# --- 5. directories -> package patterns --------------------------------------
#
# awk, not two sed expressions: a `s#^\.$#./...#` followed by a `s#^\./#...` is
# not mutually exclusive — the second rewrites what the first just produced and
# emits `./.../...`, which `go list` reports as "matched no packages" with exit
# 0. Caught by running the output through `go list` instead of reading it.
printf '%s\n' "${declared}" | awk '{ if ($0 == ".") print "./..."; else print $0 "/..." }'
