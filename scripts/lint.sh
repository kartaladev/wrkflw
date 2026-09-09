#!/usr/bin/env bash
# Lint this worktree with a cache that belongs to this worktree, and prove the
# findings belong to it too.
#
# THE DEFECT (#146). golangci-lint keys its cache by CONTENT, not by location,
# and by default that cache is ONE directory shared by every checkout on the
# machine (`golangci-lint cache status` -> Dir:). A cache entry carries the file
# path as it was FIRST seen. So when two git worktrees of this repo hold a
# byte-identical file, the second worktree to lint it is handed the first
# worktree's entry and prints the FIRST worktree's path. Two throwaway modules
# with identical sources, run from a common parent:
#
#   $ golangci-lint cache clean
#   $ (cd a && golangci-lint run ./...)   ->      pkg/foo.go:7:15: ... (errcheck)
#   $ (cd b && golangci-lint run ./...)   ->  ../a/pkg/foo.go:7:15: ... (errcheck)
#
# Measured 2026-09-09 on darwin/arm64 with golangci-lint 2.12.2. The mechanism
# is content-keyed lookup, not timing, so it is deterministic rather than flaky
# -- but a version or platform can change it, which is why self_test regenerates
# the reproduction on every invocation instead of trusting this comment.
#
# WHY IT MATTERS, IN BOTH DIRECTIONS. This is not a cosmetic path bug.
#   * A finding is reported against a file the developer is not editing, which
#     is a phantom to chase; and
#   * -- the expensive direction -- a REAL finding in THIS worktree is read as
#     "stale noise from that other checkout" and waved off. The first failure is
#     loud and self-correcting. The second ships.
# `.claude/worktrees/` routinely holds five checkouts at once, which is exactly
# the condition that produces it.
#
# TWO JOBS, AND THE SECOND IS NOT REDUNDANT.
#   1. ISOLATE -- lint_cache_dir derives a GOLANGCI_LINT_CACHE from the worktree
#      root, so no two worktrees share cache entries and the misattribution
#      cannot arise. Under ${TMPDIR}, never inside the tracked tree (so no
#      .gitignore entry is needed and `git status` stays clean) and never inside
#      .git/ (so `git worktree remove` is not blocked and a stale cache does not
#      outlive its worktree inside git's own metadata).
#   2. DETECT -- escaping_paths scans the run's output and fails loudly if any
#      reported path escapes the run root. The isolation makes the defect not
#      happen; the detector makes this script fail CLOSED when the isolation
#      stops working -- a golangci-lint release that renames or ignores the
#      variable, a caller that exports a shared value, a refactor that drops the
#      export. A fix with no detector is a guard with a blind spot, and what a
#      check cannot see cannot fail.
#
# SCOPE -- local and agent checkouts only. CI is NOT affected and is NOT wired
# to this script: each GitHub Actions job is a fresh runner with a single
# checkout, so there are no sibling worktrees and no entry to inherit.
# .github/workflows/ci.yml is deliberately untouched -- its lint job pins
# golangci/golangci-lint-action for reasons its own comment records (a period
# when the job exited 3 during config load and reported zero findings WITHOUT
# LINTING A SINGLE FILE), and replacing that with a script would re-open it.
# The accepted cost, stated plainly: nothing in CI exercises this file, so it
# can rot. self_test running on every invocation is what stands in for that.
#
# NON-VACUITY, IN TWO HALVES -- a green run has to prove BOTH, because either
# one alone is green while the guard is dead:
#
#   * DETECTION -- assert_detects feeds planted output through the SAME
#     escaping_paths used by the real run and asserts it reports the escaping
#     lines and none of the in-tree or near-miss ones. This half is
#     deterministic: no toolchain, no cache, no upstream behaviour.
#   * THE CONTROL -- assert_isolation_matters plants the two-module fixture and
#     asserts the DIFFERENCE: with one shared cache the b/ run misattributes to
#     ../a/, and with lint_cache_dir's per-root caches it does not. Without this
#     half the isolated runs prove nothing, because they pass identically on a
#     machine where the defect does not exist at all -- the same vacuity
#     check-doc-refs.sh's own header calls out. Asserting only that the two
#     cache dirs differ would prove the mechanism is WIRED, not that it MATTERS.
#
# Cost of running both halves: four fixture lint runs, measured at ~0.9s wall
# for the set. That is the price of every invocation, and it is why the fixture
# is one file per module with errcheck alone rather than this repo's config.
#
# NARROWABLE, NOT DELETABLE. If a future golangci-lint stops misattributing,
# the control stops reproducing and this script fails with a message saying so.
# That failure is the signal to re-evaluate -- narrow self_test to the detection
# half and record the version that fixed it -- not to delete the file. The
# detector half stands either way; it is the half that fails closed.
#
# Usage:
#   scripts/lint.sh ./...              # lint (self-test runs first, always)
#   scripts/lint.sh ./engine/...       # any golangci-lint `run` arguments
#   scripts/lint.sh --self-test        # prove the guard still works, lint nothing
#
# Exit status: golangci-lint's own is preserved (0 clean, 1 issues found; 3 and
# 7 observed for config/argument failure and no-Go-files). This script exits 2,
# and only 2, when escaping_paths reports a misattributed path -- a tooling
# failure a caller can tell apart from lint findings.
#
# Requires bash, git, sed, and one of shasum/sha256sum/openssl, plus
# golangci-lint and the Go toolchain. Kept bash-3.2-friendly (no mapfile, no
# associative arrays) so it runs under the bash that ships on macOS as well as
# CI's bash 5.
set -euo pipefail

script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() { echo "lint: $*" >&2; exit 1; }

# --- isolation ---------------------------------------------------------------

# The absolute path of the worktree whose cache we are about to key on. `git
# rev-parse` from the CALLER's directory, so running this from a subdirectory of
# a worktree still keys on that worktree and not on wherever the script file
# lives. The fallback covers a checkout exported without .git; script_root is
# the same answer whenever the script is run from inside its own worktree.
worktree_root() {
  git rev-parse --show-toplevel 2>/dev/null || printf '%s\n' "${script_root}"
}

# sha256 of a string. Three implementations because no single one is present
# everywhere: shasum ships with macOS, sha256sum with coreutils, openssl with
# almost everything. If none exists we FAIL rather than fall back to a weaker
# digest or to a path-mangling scheme -- a scheme that maps two distinct roots
# to one directory would silently re-create the very defect this file exists to
# remove, and it would do it invisibly.
path_digest() {
  if command -v shasum >/dev/null 2>&1; then
    printf '%s' "$1" | shasum -a 256 | cut -d' ' -f1
  elif command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | cut -d' ' -f1
  elif command -v openssl >/dev/null 2>&1; then
    printf '%s' "$1" | openssl dgst -sha256 | sed 's/.*[= ]//'
  else
    return 1
  fi
}

# The cache directory for a given worktree root. Three constraints, all load
# bearing, and each one rules out an alternative that looks simpler:
#   * distinct per root      -- a digest of the absolute path, so two worktrees
#                               never collide (that collision IS the defect);
#   * outside the tracked tree -- <root>/.lintcache would need a .gitignore
#                               entry and would show up in `git status` and in
#                               every `git clean -xdn`;
#   * outside .git/          -- a cache there blocks `git worktree remove` and
#                               survives inside metadata that is meant to be
#                               disposable.
# It deliberately does NOT depend on the worktree still existing: the digest is
# of the path string, so a removed worktree leaves only an orphaned temp
# directory that the OS reclaims.
lint_cache_dir() {
  local root="$1" digest tmp
  digest="$(path_digest "${root}")" ||
    fail "no sha256 tool found (need shasum, sha256sum or openssl); set GOLANGCI_LINT_CACHE yourself to a per-worktree path"
  tmp="${TMPDIR:-/tmp}"
  printf '%s/wrkflw-golangci-lint-cache/%s\n' "${tmp%/}" "${digest}"
}

# --- detection ---------------------------------------------------------------

# Read golangci-lint's text output on stdin; print, one per line, every reported
# file path that is not under the run root.
#
# The sed extracts the leading `path:line:` of a finding line. `[^:]*` stops at
# the first colon, so a message that itself mentions a `foo.go:1:1` path cannot
# be mistaken for the finding's own path, and the tab-indented source/caret
# context lines are excluded by the leading non-space class.
#
# Two escape shapes are reported, not one. `../` is what the defect actually
# produces today; an out-of-root ABSOLUTE path is what it would produce if a
# future version emitted absolute paths, and a detector that only knew about
# `../` would go quietly blind on that day.
escaping_paths() {
  local run_root="$1" path
  sed -n 's/^\([^:[:space:]][^:]*\.go\):[0-9][0-9]*:.*$/\1/p' |
    while IFS= read -r path; do
      case "${path}" in
        ../*) printf '%s\n' "${path}" ;;
        /*) case "${path}" in "${run_root%/}"/*) ;; *) printf '%s\n' "${path}" ;; esac ;;
      esac
    done | sort -u
}

# --- self-test ---------------------------------------------------------------

# Half one. Deterministic: it exercises escaping_paths itself, with no
# toolchain, no cache and no dependence on golangci-lint's behaviour.
assert_detects() {
  local tmp="$1" got want
  mkdir -p "${tmp}/root"

  # Two escaping lines, and four near-misses that must NOT be reported: an
  # in-tree relative path, an in-tree absolute path, a finding whose MESSAGE
  # quotes an escaping path, and a tab-indented context line.
  cat > "${tmp}/planted" <<EOF
engine/state.go:10:2: Error return value is not checked (errcheck)
${tmp}/root/pkg/ok.go:3:1: Error return value is not checked (errcheck)
engine/state.go:11:2: could not import ../gone/pkg/foo.go:1:1 (typecheck)
../sibling-worktree/engine/state.go:10:2: Error return value is not checked (errcheck)
/somewhere/else/engine/state.go:10:2: Error return value is not checked (errcheck)
	os.Remove("../not/a/finding.go:1:1: and neither is this")
2 issues:
* errcheck: 2
EOF

  want='../sibling-worktree/engine/state.go
/somewhere/else/engine/state.go'
  got="$(escaping_paths "${tmp}/root" < "${tmp}/planted")"
  if [ "${got}" != "${want}" ]; then
    {
      echo "self-test: escaping_paths does not detect what this script claims."
      echo "  expected: $(printf '%s' "${want}" | tr '\n' ' ')"
      echo "  got:      $(printf '%s' "${got}" | tr '\n' ' ')"
    } >&2
    return 1
  fi

  # An empty input must produce no findings -- otherwise a run that emitted
  # nothing at all would be reported as misattributed.
  got="$(escaping_paths "${tmp}/root" < /dev/null)"
  [ -z "${got}" ] || { echo "self-test: escaping_paths reports '${got}' on empty input" >&2; return 1; }
}

# One byte-identical throwaway module. Identical content is the precondition of
# the defect: the cache entry is keyed by what is in the file, so two modules
# that differ do not collide and would make this fixture prove nothing.
plant_fixture_module() {
  local dir="$1"
  mkdir -p "${dir}/pkg"
  cat > "${dir}/go.mod" <<'EOF'
module example.com/lintcachefixture

go 1.21
EOF
  # errcheck alone, and `default: none`, so the fixture runs in well under a
  # second and does not inherit this repo's linter set.
  cat > "${dir}/.golangci.yml" <<'EOF'
version: "2"
linters:
  default: none
  enable:
    - errcheck
EOF
  cat > "${dir}/pkg/foo.go" <<'EOF'
package pkg

import "os"

func Foo() {
	os.Remove("/tmp/wrkflw-lint-fixture")
}
EOF
}

# Run the fixture in `dir` with the given cache, and echo the findings' paths.
run_fixture() {
  local dir="$1" cache="$2"
  (
    cd "${dir}" &&
      GOLANGCI_LINT_CACHE="${cache}" golangci-lint run --config "${dir}/.golangci.yml" ./... 2>/dev/null || true
  ) | sed -n 's/^\([^:[:space:]][^:]*\.go\):[0-9][0-9]*:.*$/\1/p' | sort -u
}

# Half two, and the half that is easy to write vacuously. It asserts the
# DIFFERENCE the isolation makes, not merely that isolation was configured.
assert_isolation_matters() {
  local tmp="$1" shared_b iso_a iso_b cache_a cache_b
  plant_fixture_module "${tmp}/a"
  plant_fixture_module "${tmp}/b"

  # THE CONTROL. One cache, a/ first, then b/. This must reproduce the defect,
  # or the isolated runs below are asserting nothing. The shared cache is a
  # dedicated temp directory, NOT the developer's real one: a self-test that ran
  # `golangci-lint cache clean` on every invocation would throw away the cache
  # the very next real run needs.
  run_fixture "${tmp}/a" "${tmp}/shared-cache" >/dev/null
  shared_b="$(run_fixture "${tmp}/b" "${tmp}/shared-cache")"
  case "${shared_b}" in
    ../a/*)
      : # reproduced -- the control can fail, so the assertions below can pass
      ;;
    *)
      {
        echo "self-test: the control no longer reproduces #146, so the isolation half proves nothing."
        echo "  linting ${tmp}/b through a cache already holding ${tmp}/a should report"
        echo "  a path under ../a/, and reported: ${shared_b:-<nothing>}"
        echo "  golangci-lint: $(golangci-lint --version 2>&1 | head -1)"
        echo
        echo "  Either the fixture drifted, or this golangci-lint no longer misattributes."
        echo "  If it is the latter, that is good news and this file NARROWS rather than"
        echo "  gets deleted: keep lint_cache_dir and escaping_paths, drop"
        echo "  assert_isolation_matters, and record the version that fixed it here."
      } >&2
      return 1
      ;;
  esac

  # The isolated runs go through lint_cache_dir -- the same function the real
  # invocation below uses. Hand-written cache paths here would test a scheme
  # this script does not actually run.
  #
  # TMPDIR is overridden so the fixture caches land inside this self-test's own
  # temp directory and are reclaimed by the `rm -rf` in self_test. Without the
  # override they land in the shared cache root under the real TMPDIR, and since
  # each self-test gets a fresh mktemp path they are keyed differently every
  # time: two orphaned directories per invocation, growing without bound. It is
  # set only for these two substitutions, so the digest, the TMPDIR handling and
  # the trailing-slash trim are all still the real ones under test.
  cache_a="$(TMPDIR="${tmp}" lint_cache_dir "${tmp}/a")"
  cache_b="$(TMPDIR="${tmp}" lint_cache_dir "${tmp}/b")"
  if [ "${cache_a}" = "${cache_b}" ]; then
    echo "self-test: lint_cache_dir maps two distinct roots to '${cache_a}'" >&2
    return 1
  fi

  iso_a="$(run_fixture "${tmp}/a" "${cache_a}")"
  iso_b="$(run_fixture "${tmp}/b" "${cache_b}")"
  if [ "${iso_a}" != "pkg/foo.go" ] || [ "${iso_b}" != "pkg/foo.go" ]; then
    {
      echo "self-test: per-root caches did not keep the fixture runs in-tree."
      echo "  a/ reported: ${iso_a:-<nothing>}"
      echo "  b/ reported: ${iso_b:-<nothing>}"
      echo "  expected 'pkg/foo.go' from each."
    } >&2
    return 1
  fi
}

self_test() {
  local tmp status=0
  command -v golangci-lint >/dev/null 2>&1 ||
    fail "golangci-lint is not on PATH; see CONTRIBUTING.md for the version this repo expects"

  tmp="$(mktemp -d)"
  assert_detects "${tmp}" || status=1
  assert_isolation_matters "${tmp}" || status=1
  rm -rf "${tmp}"

  if [ "${status}" != "0" ]; then
    echo "lint: SELF-TEST FAILED -- this script can no longer be shown to do what it claims." >&2
    exit 1
  fi
  # States exactly what was proved, and no more.
  # Deliberately spells no literal "../": this line is printed on the SUCCESS
  # path, and a success banner that trips every `grep '\.\./'` would train the
  # exact reflex escaping_paths exists to stop -- reading a real escaped path as
  # background noise.
  echo "lint: self-test OK -- escaping_paths reports both planted escapes and none of the four near-misses, and a deliberately shared cache still misattributes the b/ fixture to the sibling a/ module while lint_cache_dir's per-root caches do not (isolation proved to matter, not merely wired)."
}

# --- main --------------------------------------------------------------------

case "${1:-}" in
  --self-test)
    [ "$#" -eq 1 ] || fail "--self-test takes no other arguments (got: $*)"
    self_test
    exit 0
    ;;
esac

self_test

run_root="${PWD}"
root="$(worktree_root)"

# An explicitly exported value is honoured rather than overridden: a caller that
# set it made a deliberate choice, and silently discarding it would be a worse
# surprise than the one this script fixes. The guarantee then belongs to them --
# which is precisely the case escaping_paths below is here to catch.
if [ -n "${GOLANGCI_LINT_CACHE:-}" ]; then
  echo "lint: GOLANGCI_LINT_CACHE is already set to '${GOLANGCI_LINT_CACHE}'; honouring it. Per-worktree isolation is the caller's to guarantee."
else
  GOLANGCI_LINT_CACHE="$(lint_cache_dir "${root}")"
  export GOLANGCI_LINT_CACHE
fi

out="$(mktemp)"
trap 'rm -f "${out}"' EXIT

# Not `exec`: the output has to be read back before this process can exit. stdout
# is captured (which also drops golangci-lint's colour, since it is no longer a
# tty) and replayed; stderr streams through untouched.
rc=0
golangci-lint run "$@" > "${out}" || rc=$?
cat "${out}"

escaped="$(escaping_paths "${run_root}" < "${out}")"
if [ -n "${escaped}" ]; then
  {
    echo
    echo "lint: FAIL -- golangci-lint reported findings against files outside ${run_root}:"
    printf '%s\n' "${escaped}" | sed 's/^/    /'
    echo
    echo "That is #146: the cache is keyed by content and shared across checkouts, so an"
    echo "entry first written by a sibling worktree is replayed here carrying THAT"
    echo "worktree's path. The findings above are attributed to the wrong tree, and the"
    echo "expensive half of the mistake is the opposite one -- a real finding in this"
    echo "worktree read as noise from another and waved off."
    echo
    echo "This script exists to make that impossible, so seeing it means the isolation"
    echo "did not take. Check GOLANGCI_LINT_CACHE (currently '${GOLANGCI_LINT_CACHE}'):"
    echo "if a caller exported a shared value, stop doing that; if it is per-worktree and"
    echo "this still happened, golangci-lint's cache handling has changed and"
    echo "lint_cache_dir needs revisiting. Re-derive every finding above inside this"
    echo "worktree before acting on it -- re-derive, not dismiss."
  } >&2
  exit 2
fi

exit "${rc}"
