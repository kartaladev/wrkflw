#!/usr/bin/env bash
# Lint this worktree with a cache that belongs to this worktree, and prove the
# findings belong to it too.
#
# THE DEFECT (#146). golangci-lint keys its cache by CONTENT, not by location,
# and by default that cache is ONE directory shared by every checkout on the
# machine (`golangci-lint cache status` -> Dir:). A cache entry carries the file
# path as it was FIRST seen. So when two checkouts of this repo hold a
# byte-identical file, the second one to lint it is handed the first one's entry
# and prints the FIRST one's path:
#
#   $ golangci-lint cache clean
#   $ (cd a && golangci-lint run ./...)   ->      pkg/foo.go:7:15: ... (errcheck)
#   $ (cd b && golangci-lint run ./...)   ->  ../a/pkg/foo.go:7:15: ... (errcheck)
#
# WHY IT MATTERS, IN BOTH DIRECTIONS. Not a cosmetic path bug.
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
#      root, so no two checkouts share cache entries and the misattribution
#      cannot arise. Under ${TMPDIR}, never inside the tracked tree (so no
#      .gitignore entry is needed and `git status` stays clean) and never inside
#      .git/ (so `git worktree remove` is not blocked).
#   2. DETECT -- escaping_paths asks, of every reported path, whether it belongs
#      to THIS checkout, and fails loudly if any does not. Isolation makes the
#      defect not happen; the detector makes this script fail CLOSED when the
#      isolation stops working -- a golangci-lint release that renames or
#      ignores the variable, a caller that exports a shared value, a refactor
#      that drops the export. A fix with no detector is a guard with a blind
#      spot, and what a check cannot see cannot fail.
#
# MEMBERSHIP, NOT SPELLING -- the property the detector actually tests.
# An earlier revision of this file asked whether a path was spelled `../…` or
# began with an absolute prefix outside the run root. That is a test of
# SPELLINGS, and it needed a new arm for every new shape: it missed
# `sub/../../sibling`, it missed `/repo/../other`, it was hard-coded to `\.go`
# so a reported `.s` file escaped it, and -- the one that matters here -- it was
# completely blind to `.claude/worktrees/B/pkg/foo.go`, which is the shape THIS
# repo's own layout produces, because worktrees live INSIDE the repo root and
# that path has no `../` in it at all.
#
# So the question asked is now: does the reported file belong to this checkout?
# Two clauses, jointly sufficient for every shape found so far:
#
#   a. RESOLVE PHYSICALLY, COMPARE PHYSICALLY. Resolve each reported path and
#      compare against the physical worktree root. Outside, or unresolvable at
#      all, is an escape -- a misattributed entry often names a directory that
#      no longer exists, and a path that cannot be shown to be ours is not ours.
#      Resolving physically is also what stops a symlinked checkout raising a
#      false #146 alarm on its own genuine findings.
#   b. INSIDE IS NOT SUFFICIENT. `.claude/worktrees/B/pkg/foo.go` resolves
#      inside the main clone and still belongs to another checkout. Go tooling
#      never descends into a dot-directory, so a dot-directory component BELOW
#      the run root is an escape. (Below: the root itself is routinely inside
#      `.claude/worktrees/`, and that is not an escape.)
#
# Membership does not care about file extension, so this DELETES the `\.go`
# hard-coding rather than widening it to a list.
#
# The base for relative paths is the WORKTREE ROOT, not $PWD. Measured:
# golangci-lint reports relative to the module root, so from `m/x` a finding in
# that directory prints as `x/f.go`, not `f.go`. Resolving against $PWD would
# therefore false-fail every run from a subdirectory. Both bases are tried and a
# path legitimate under either is accepted, so a layout where they differ cannot
# produce a false alarm.
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
# NON-VACUITY, IN THREE HALVES -- a green run has to prove all of them, because
# any one alone is green while the guard is dead:
#
#   * DETECTION (assert_detects) -- planted output through the SAME
#     escaping_paths the real run uses: both escape directions this repo can
#     produce, and four near-misses that must NOT fire. Deterministic; no
#     toolchain, no cache, no upstream behaviour.
#   * WIRING (assert_main_wiring) -- runs THIS SCRIPT, end to end, against a
#     stub `golangci-lint` whose output and exit status are chosen. It proves
#     the pieces named in KNOWN LIMITS' complement are connected: the detector's
#     verdict reaches the exit status, golangci-lint's own status is passed
#     through untouched, its findings are actually printed, and the derived cache
#     reaches the child's environment. It does NOT cover everything in this file;
#     what it misses is listed under KNOWN LIMITS rather than left implied.
#     Before it existed, four separate mutations of the main block -- including
#     `exit "${rc}"` -> `exit 0`, which makes this wrapper return success on
#     real findings -- passed --self-test with a cheerful OK.
#   * THE CONTROL (assert_isolation_matters) -- the two-module fixture with the
#     REAL golangci-lint, asserting the DIFFERENCE isolation makes. This is the
#     only half that depends on upstream still having the bug, and it is
#     therefore the only half that WARNS rather than fails; see below.
#
# WHY THE CONTROL WARNS AND THE OTHER TWO FAIL. self_test runs on every
# invocation and gates the real lint, and CONTRIBUTING.md makes this script the
# mandated lint command -- so a self-test that exits non-zero is a refusal to
# lint at all. The control was measured failing 3 times in 10 serial runs and 5
# in 15 parallel ones under this repo's own five-worktree load, because when
# a/'s cache entry is not yet visible, b/ legitimately computes fresh and the
# control reads that as "upstream fixed #146". Hard-gating on it converted a
# flaky observation into an intermittent denial of linting, which is worse than
# the defect this script fixes. Note carefully that warning here is NOT a
# retreat from failing closed: the property that matters is DETECTING
# misattribution, the detector half fails closed on its own, and it does not
# depend on the control. A positive demonstration that isolation is broken is
# still fatal; only an inconclusive control warns.
#
# NARROWABLE, NOT DELETABLE. If a future golangci-lint stops misattributing, the
# control stops reproducing and warns every run. That is the signal to
# re-evaluate -- narrow self_test to the deterministic halves and record the
# version that fixed it -- not to delete the file. The detector stands either
# way; it is the half that fails closed.
#
# Usage:
#   scripts/lint.sh ./...              # lint (self-test runs first, always)
#   scripts/lint.sh ./engine/...       # any golangci-lint `run` arguments
#   scripts/lint.sh --self-test        # run the checks below, lint nothing
#
# EXIT STATUS. golangci-lint's own is preserved exactly (0 clean, 1 issues
# found; 3 and 7 observed for config/argument failure and no-Go-files, and
# `--issues-exit-code` can make it anything). This script uses **9** for a
# misattributed path, and 1 for its own usage errors. 9 is outside golangci-lint
# 2.x's own 0-7 range on purpose: an earlier revision claimed exit 2 "and only
# 2", which was false -- `--issues-exit-code 2` reaches 2 with no misattribution
# whatsoever, and upstream reserves 2 for WarningInTest.
#
# KNOWN LIMITS. A documented limit has to be TRUE, so this list is derived from
# a mutation sweep of this file, not from what feels uncovered. --self-test does
# NOT detect any of these; each was mutated and survived:
#
#   * the EXIT trap in self_test (temp-directory cleanup on abnormal exit) --
#     --self-test does not simulate a signal. Verified by direct SIGTERM instead.
#   * prepare_cache_root's `mkdir -p` and its `chmod 700`, the SF3 mitigation for
#     a predictable cache path under a world-writable /tmp.
#   * the notice printed when a caller has already exported GOLANGCI_LINT_CACHE
#     (the honouring itself IS asserted; only the message is not).
#   * worktree_root's fallback: a mutant that always falls back to the script's
#     own directory passes, because the wiring fixture is a real git repo either
#     way.
#   * finding_paths' `sort -u` (duplicate offender lines, cosmetic).
#   * the fixtures' private GOCACHE.
#
# And two limits of the guard itself rather than of its tests:
#
#   * golangci-lint lints only files matching the default build tags, so a
#     misattributed finding in a tag-gated file is invisible to both the
#     detector and the self-test. Inherited from the tool, not introduced here.
#   * A reported path containing whitespace or a colon cannot be extracted
#     unambiguously and is skipped by the line parser. That is the deliberate
#     price of not matching golangci-lint's own source-context lines, which are
#     printed at the source line's indentation and reach column 0.
#
# Everything here was measured on darwin/arm64, golangci-lint 2.12.2, bash
# 3.2.57. The sha256sum and openssl branches of path_digest have never been
# exercised on this machine; only shasum has.
#
# Requires bash, git, sed, and one of shasum/sha256sum/openssl, plus
# golangci-lint and the Go toolchain. Kept bash-3.2-friendly (no mapfile, no
# associative arrays) so it runs under the bash that ships on macOS as well as
# CI's bash 5.
set -euo pipefail

script_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script_self="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/$(basename "${BASH_SOURCE[0]}")"

# Exit status for a misattributed path. Deliberately outside golangci-lint's own
# range; see EXIT STATUS above.
readonly ESCAPE_EXIT=9

fail() { echo "lint: $*" >&2; exit 1; }

# --- isolation ---------------------------------------------------------------

# The absolute path of the worktree whose cache we key on. `git rev-parse` from
# the CALLER's directory, so running this from a subdirectory still keys on that
# worktree and not on wherever the script file lives. The fallback covers a
# checkout exported without .git.
worktree_root() {
  git rev-parse --show-toplevel 2>/dev/null || printf '%s\n' "${script_root}"
}

# Physical form of a directory: resolves symlinks and normalises `..` without
# needing realpath(1), which stock macOS bash 3.2 environments lack. Prints
# nothing and returns non-zero when the directory does not exist.
physical_dir() { (cd "$1" 2>/dev/null && pwd -P); }

# sha256 of a string. Three implementations because no single one is present
# everywhere: shasum ships with macOS, sha256sum with coreutils, openssl with
# almost everything.
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

# Checked once, up front, by whoever is about to need a digest. lint_cache_dir
# deliberately does NOT do this itself: it is called inside `$(...)`, where a
# `fail` would exit only the subshell and leave the caller to report some later,
# unrelated assertion as the cause.
require_digest_tool() {
  path_digest probe >/dev/null 2>&1 ||
    fail "no sha256 tool found (need shasum, sha256sum or openssl); set GOLANGCI_LINT_CACHE yourself to a per-worktree path"
}

# The cache directory for a given worktree root. Three constraints, all load
# bearing, and each rules out an alternative that looks simpler:
#   * distinct per root      -- a digest of the absolute path, so two checkouts
#                               never collide (that collision IS the defect);
#   * outside the tracked tree -- <root>/.lintcache would need a .gitignore
#                               entry and would show up in `git status`;
#   * outside .git/          -- a cache there blocks `git worktree remove`.
# It deliberately does NOT depend on the worktree still existing: the digest is
# of the path string, so a removed worktree leaves only an orphaned temp
# directory the OS reclaims.
#
# $(id -u) is in the path because ${TMPDIR} is unset on most Linux distros, in
# containers and under systemd, where the fallback is a world-writable /tmp and
# the digest is entirely predictable. Ownership then separates users, and
# prepare_cache_root chmods it 700.
lint_cache_dir() {
  local root="$1" digest tmp
  digest="$(path_digest "${root}")" || return 1
  tmp="${TMPDIR:-/tmp}"
  printf '%s/wrkflw-golangci-lint-cache-%s/%s\n' "${tmp%/}" "$(id -u)" "${digest}"
}

prepare_cache_root() {
  local dir parent
  dir="$1"
  parent="$(dirname "${dir}")"
  mkdir -p "${parent}" || fail "cannot create cache root ${parent}"
  chmod 700 "${parent}" 2>/dev/null || true
}

# --- detection ---------------------------------------------------------------

# Extract the leading `path:line:` of each finding line.
#
# The accept/reject set is DERIVED FROM REAL golangci-lint OUTPUT, not from the
# shapes that seem likely -- see the corpus in assert_detects. What that corpus
# shows, and what an earlier revision of this function got wrong:
#
#   pkg/foo.go:10:11: Error return value ... (errcheck)   <- a finding
#   var unusedThing = "docs/notes:12: see this"            <- a CONTEXT line, col 0
#   	os.Remove("/tmp/z")                                   <- a context line, indented
#       ^                                                  <- a caret line
#   5 issues:                                              <- summary
#   * errcheck: 3                                          <- summary
#
# The context line is printed at THE SOURCE LINE'S OWN INDENTATION, so for every
# top-level declaration it starts at column 0. A previous version excluded
# context lines by "they are indented", which is false, and matched only the
# first character against whitespace while letting the rest of the path contain
# spaces. That parsed `var unusedThing = "docs/notes` as a reported path and
# made this script exit ESCAPE_EXIT against a GENUINE IN-TREE FINDING.
#
# The property that actually separates them: A REPORTED PATH CONTAINS NO
# WHITESPACE. Every Go source line that could reach column 0 begins with a
# keyword and a space (`var `, `func `, `type `, `const `), so it cannot satisfy
# `<no-space-run>:<digits>:`. Requiring a digit straight after the colon is what
# keeps the summary lines out.
finding_paths() {
  sed -n 's/^\([^:[:space:]][^:[:space:]]*\):[0-9][0-9]*:.*$/\1/p' | sort -u
}

# Is this reported path a file belonging to the checkout rooted at $2, when read
# relative to base $3? Returns 0 for "yes, ours".
path_is_ours() {
  local path="$1" root="$2" base="$3" abs resolved rel
  case "${path}" in
    /*) abs="${path}" ;;
    *) abs="${base}/${path}" ;;
  esac

  # Resolve the directory physically. Unresolvable means it cannot be shown to
  # be ours -- a misattributed entry frequently names a checkout that has since
  # been deleted -- so it is not ours.
  resolved="$(physical_dir "$(dirname "${abs}")")" || return 1
  [ -n "${resolved}" ] || return 1

  case "${resolved}" in
    "${root}") rel="" ;;
    "${root}"/*) rel="${resolved#"${root}"}" ;;
    *) return 1 ;;   # outside the root. The exact-match arm above is what keeps
                     # a sibling named /repo-evil from matching root /repo.
  esac

  # A dot-directory component BELOW the root belongs to another checkout (or to
  # tooling), never to a legitimate Go lint of this one.
  case "${rel}/$(basename "${abs}")/" in
    */.*/*) return 1 ;;
  esac
  return 0
}

# Read golangci-lint's text output on stdin; print every reported path that does
# not belong to this checkout. `root` is the physical worktree root; `pwd_phys`
# is the physical current directory, tried as a second base so a layout where
# golangci-lint reports relative to something other than the worktree root
# cannot produce a false alarm.
escaping_paths() {
  local root="$1" pwd_phys="$2" path
  finding_paths | while IFS= read -r path; do
    if path_is_ours "${path}" "${root}" "${root}"; then continue; fi
    if [ "${pwd_phys}" != "${root}" ] && path_is_ours "${path}" "${root}" "${pwd_phys}"; then continue; fi
    printf '%s\n' "${path}"
  done
}

# --- self-test: half one, detection ------------------------------------------

# Deterministic: exercises escaping_paths itself, with no toolchain, no cache
# and no dependence on golangci-lint's behaviour.
assert_detects() {
  local tmp="$1" root="${1}/root" got want
  # Real directories, because the membership test resolves paths physically.
  mkdir -p "${root}/engine" "${root}/.claude/worktrees/B/pkg" "${tmp}/sibling-worktree/engine"
  root="$(physical_dir "${root}")"

  # Two escapes -- one per direction this repo can actually produce -- and a
  # near-miss set TRANSCRIBED FROM REAL golangci-lint OUTPUT rather than invented.
  #
  # The transcription matters. Every earlier version of this fixture planted the
  # shapes someone thought of, and each time the domain was set by imagination
  # the guard was wrong about it: first only sibling `../` escapes were planted
  # (so the nested shape was invisible), then only a TAB-INDENTED context line
  # (so the column-0 shape was invisible, and the parser matched a genuine
  # finding's source text as a path). The block below is a verbatim corpus from
  # `golangci-lint run` over a fixture with errcheck and unused findings --
  # findings, context lines at column 0 AND indented, caret lines, and both
  # summary forms.
  cat > "${tmp}/planted" <<EOF
engine/state.go:10:2: Error return value of \`os.Remove\` is not checked (errcheck)
	os.Remove("/tmp/z")
	         ^
engine/state.go:12:2: var unusedThing is unused (unused)
var unusedThing = "docs/notes:12: see this"
    ^
engine/state.go:14:2: var t is unused (unused)
func (t T) M() { os.Remove("/tmp/w") }
                          ^
${root}/engine/state.go:16:2: an in-tree ABSOLUTE path is ours (errcheck)
engine/state.go:11:2: could not import ../gone/pkg/foo.go:1:1 (typecheck)
engine/../engine/state.go:13:2: a climb that lands back inside is ours (errcheck)
../sibling-worktree/engine/state.go:10:2: Error return value is not checked (errcheck)
.claude/worktrees/B/pkg/foo.go:6:11: Error return value is not checked (errcheck)
5 issues:
* errcheck: 3
* unused: 2
EOF

  # NOTE the second escape: it has no "../" anywhere. It is the shape this
  # repo's own layout produces, and the spelling-based detector this replaced
  # was silent on it.
  # Sorted on both sides: finding_paths sorts, and where these two shapes fall
  # relative to each other is a locale question this assertion must not depend on.
  want="$(printf '%s\n' '.claude/worktrees/B/pkg/foo.go' '../sibling-worktree/engine/state.go' | sort)"
  got="$(escaping_paths "${root}" "${root}" < "${tmp}/planted" | sort)"
  if [ "${got}" != "${want}" ]; then
    {
      echo "self-test: escaping_paths does not report exactly the paths that are not ours."
      echo "  expected: $(printf '%s' "${want}" | tr '\n' ' ')"
      echo "  got:      $(printf '%s' "${got}" | tr '\n' ' ')"
    } >&2
    return 1
  fi

  # The sibling-prefix case: /root-evil must not read as inside /root. This is
  # the case a bare prefix comparison gets wrong, and it was measured
  # load-bearing before this rewrite, so it keeps a planted line of its own.
  mkdir -p "${root}-evil"
  printf '%s\n' "${root}-evil/x.go:1:2: nope (errcheck)" > "${tmp}/planted-evil"
  got="$(escaping_paths "${root}" "${root}" < "${tmp}/planted-evil")"
  if [ "${got}" != "${root}-evil/x.go" ]; then
    echo "self-test: a sibling directory sharing the root's name prefix was treated as inside it (got '${got}')" >&2
    return 1
  fi

  # Empty input must produce nothing, or a run that emitted no findings at all
  # would be reported as misattributed.
  got="$(escaping_paths "${root}" "${root}" < /dev/null)"
  [ -z "${got}" ] || { echo "self-test: escaping_paths reports '${got}' on empty input" >&2; return 1; }
}

# --- self-test: half two, main-path wiring -----------------------------------

# Runs THIS SCRIPT end to end against a stub `golangci-lint` whose stdout and
# exit status we choose, so every assertion here is deterministic: no real
# linting, no cache, no dependence on the upstream defect. This is the half that
# proves the pieces are connected -- detector verdict to exit status, upstream
# status passed through, derived cache reaching the child's environment.
assert_main_wiring() {
  local tmp="$1" stub="$1/stub" work="$1/wire" rc out want_cache

  mkdir -p "${stub}"
  cat > "${stub}/golangci-lint" <<'STUB'
#!/usr/bin/env bash
# Records what the wrapper handed it, then prints and exits as instructed.
printf '%s\n' "${GOLANGCI_LINT_CACHE:-<unset>}" > "${WRKFLW_STUB_DIR}/cache-seen"
printf '%s\n' "$*" > "${WRKFLW_STUB_DIR}/args-seen"
if [ -n "${WRKFLW_STUB_OUT:-}" ]; then printf '%s\n' "${WRKFLW_STUB_OUT}"; fi
exit "${WRKFLW_STUB_RC:-0}"
STUB
  chmod +x "${stub}/golangci-lint"

  # A real git repo, so worktree_root resolves here rather than falling back to
  # the repo this script lives in.
  mkdir -p "${work}"
  ( cd "${work}" && git init -q . ) 2>/dev/null || { echo "self-test: could not git init the wiring fixture" >&2; return 1; }
  mkdir -p "${work}/pkg" "${work}/.claude/worktrees/B/pkg" "${tmp}/outside/pkg"
  work="$(physical_dir "${work}")"

  # $1 stub stdout, $2 stub exit status; echoes the wrapper's exit status.
  wired() {
    local o="$1" r="$2" e=0
    # env -u: a child that inherits the caller's environment is not testing this
    # script, it is testing the caller. Two variables, found one at a time and
    # swept together here: GOLANGCI_LINT_CACHE, which made assertion 6 fail (and
    # so refused to lint) for anyone who had it set; and GOWORK, which a go.work
    # in the environment uses to break every fixture run permanently. Three
    # child-invocation sites in this file, all three cleaned.
    ( cd "${work}" &&
      env -u GOLANGCI_LINT_CACHE -u GOWORK \
      WRKFLW_STUB_DIR="${stub}" WRKFLW_STUB_OUT="${o}" WRKFLW_STUB_RC="${r}" \
      _WRKFLW_LINT_SELF_TEST_DEPTH=1 PATH="${stub}:${PATH}" TMPDIR="${tmp}" \
      bash "${script_self}" ./... ) > "${tmp}/wired.out" 2> "${tmp}/wired.err" || e=$?
    printf '%s' "${e}"
  }

  check() {
    local label="$1" want="$2" got="$3"
    [ "${want}" = "${got}" ] && return 0
    {
      echo "self-test: main-path wiring, ${label}: expected the wrapper to exit ${want}, got ${got}."
      sed 's/^/      /' "${tmp}/wired.err" | head -6
    } >&2
    return 1
  }

  # 1. In-tree findings: golangci-lint's own status must survive untouched.
  #    This is the assertion that catches `exit "${rc}"` -> `exit 0`, i.e. the
  #    wrapper silently turning a repo-wide quality gate green.
  rc="$(wired 'pkg/f.go:1:2: something (errcheck)' 1)"
  check "in-tree findings pass golangci-lint's exit 1 through" 1 "${rc}" || return 1
  # ...and the findings must actually REACH the developer. Deleting the replay
  # of the captured output leaves the exit status correct and prints nothing at
  # all, which no status assertion can see.
  if ! grep -q 'pkg/f.go:1:2: something' "${tmp}/wired.out"; then
    {
      echo "self-test: the wrapper returned the right status but did not print golangci-lint's"
      echo "           findings. Its output is captured so the detector can read it, and the"
      echo "           replay of that capture is what the developer actually sees."
    } >&2
    return 1
  fi

  # 2. Clean run.
  rc="$(wired '' 0)"
  check "a clean run exits 0" 0 "${rc}" || return 1

  # 3. A non-lint failure must also pass through, not be rewritten.
  rc="$(wired '' 3)"
  check "a golangci-lint failure (3) passes through" 3 "${rc}" || return 1

  # 4. Sibling escape -> ESCAPE_EXIT.
  rc="$(wired '../outside/pkg/f.go:1:2: something (errcheck)' 1)"
  check "a ../ escape is caught" "${ESCAPE_EXIT}" "${rc}" || return 1
  grep -q 'lint: FAIL' "${tmp}/wired.err" || { echo "self-test: the escape exit was returned without the lint: FAIL explanation" >&2; return 1; }

  # 5. Nested escape, the shape this repo's layout produces -> ESCAPE_EXIT.
  rc="$(wired '.claude/worktrees/B/pkg/f.go:1:2: something (errcheck)' 1)"
  check "a nested .claude/worktrees escape is caught" "${ESCAPE_EXIT}" "${rc}" || return 1

  # 6. The derived cache must actually reach the child's environment. Without
  #    this, dropping `export GOLANGCI_LINT_CACHE` leaves #146 fully live while
  #    every other assertion here still passes.
  want_cache="$(TMPDIR="${tmp}" lint_cache_dir "${work}")" || { echo "self-test: lint_cache_dir failed" >&2; return 1; }
  rc="$(wired '' 0)"
  out="$(cat "${stub}/cache-seen")"
  if [ "${out}" != "${want_cache}" ]; then
    {
      echo "self-test: the isolated cache never reached golangci-lint's environment."
      echo "  expected GOLANGCI_LINT_CACHE=${want_cache}"
      echo "  golangci-lint saw       =${out}"
    } >&2
    return 1
  fi

  # 7. The output sink must be pinned, or a caller flag can send findings
  #    somewhere the detector cannot read while the run still looks healthy.
  case "$(cat "${stub}/args-seen")" in
    *--output.text.path=stdout) ;;
    *) echo "self-test: the forced text output sink is not the last argument passed to golangci-lint (got '$(cat "${stub}/args-seen")')" >&2; return 1 ;;
  esac

  # 9. An already-set GOLANGCI_LINT_CACHE must be HONOURED and the run must still
  #    proceed. Regression guard: an earlier draft of this very function let the
  #    caller's value leak into the children above, so assertion 6 failed and the
  #    script refused to lint at all for anyone who had the variable exported.
  rc=0
  ( cd "${work}" && GOLANGCI_LINT_CACHE="${tmp}/caller-chosen" \
      WRKFLW_STUB_DIR="${stub}" WRKFLW_STUB_OUT="" WRKFLW_STUB_RC=0 \
      _WRKFLW_LINT_SELF_TEST_DEPTH=1 PATH="${stub}:${PATH}" TMPDIR="${tmp}" \
      bash "${script_self}" ./... ) >/dev/null 2>"${tmp}/wired.err" || rc=$?
  check "an already-set GOLANGCI_LINT_CACHE is honoured and the run proceeds" 0 "${rc}" || return 1
  out="$(cat "${stub}/cache-seen")"
  if [ "${out}" != "${tmp}/caller-chosen" ]; then
    echo "self-test: an explicitly set GOLANGCI_LINT_CACHE was not honoured (golangci-lint saw '${out}')" >&2
    return 1
  fi

  # 10. The escape status must stay OUTSIDE golangci-lint's own range, which is
  #     the actual contract -- "exit 2, and only 2" was false precisely because
  #     it was asserted as a number rather than as a property. Written against
  #     ESCAPE_EXIT's value, this assertion would pass for any value at all.
  case "${ESCAPE_EXIT}" in
    0|1|2|3|4|5|6|7)
      echo "self-test: ESCAPE_EXIT is ${ESCAPE_EXIT}, inside golangci-lint's own 0-7 range, so a caller cannot tell a misattributed path from the tool's own status" >&2
      return 1 ;;
  esac

  # 8. --path-prefix rewrites paths, so it must be refused rather than pinned.
  rc=0
  ( cd "${work}" && env -u GOLANGCI_LINT_CACHE -u GOWORK _WRKFLW_LINT_SELF_TEST_DEPTH=1 \
      PATH="${stub}:${PATH}" TMPDIR="${tmp}" \
      bash "${script_self}" --path-prefix vendored ./... ) >/dev/null 2>"${tmp}/wired.err" || rc=$?
  if [ "${rc}" != "1" ] || ! grep -q 'path-prefix' "${tmp}/wired.err"; then
    echo "self-test: --path-prefix was not refused (exit ${rc})" >&2
    return 1
  fi
  # ...and in a NON-FIRST position, because the check is a loop over "$@" and an
  # assertion that only ever passes it first cannot tell that loop from a test of
  # $1. A mutant narrowing it to $1 survived this function until this case existed.
  rc=0
  ( cd "${work}" && env -u GOLANGCI_LINT_CACHE -u GOWORK _WRKFLW_LINT_SELF_TEST_DEPTH=1 \
      PATH="${stub}:${PATH}" TMPDIR="${tmp}" \
      bash "${script_self}" ./... --path-prefix=vendored ) >/dev/null 2>"${tmp}/wired.err" || rc=$?
  if [ "${rc}" != "1" ] || ! grep -q 'path-prefix' "${tmp}/wired.err"; then
    echo "self-test: --path-prefix was not refused when passed after the packages (exit ${rc})" >&2
    return 1
  fi
}

# --- self-test: half three, the control --------------------------------------

# One byte-identical throwaway module. Identical content is the precondition of
# the defect: the cache entry is keyed by what is in the file, so two modules
# that differ do not collide and the fixture would prove nothing.
plant_fixture_module() {
  local dir="$1"
  mkdir -p "${dir}/pkg"
  cat > "${dir}/go.mod" <<'EOF'
module example.com/lintcachefixture

go 1.21
EOF
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

# $4 is a FILE the failure cause is written to, not a shell variable. Three of
# this function's four call sites are command substitutions, so a global
# assigned in here dies with the subshell and the caller reports an empty cause
# -- measured at 1 run in 20. A file crosses the subshell boundary; the earlier
# global did not, which is why "stop swallowing" was only a quarter applied.
#
# --allow-parallel-runners: golangci-lint takes a FILE LOCK in os.TempDir(), so
# a concurrent run elsewhere on the machine makes the fixture exit 3 with
# "parallel golangci-lint is running". That lock is not in GOLANGCI_LINT_CACHE,
# so no amount of cache isolation avoids it. Measured, 20 runs 5-way parallel:
# without the flag the control loses its proof 9 times, with it 0.
#
# GOWORK=off: a go.work in the caller's environment applies to these throwaway
# modules and makes every fixture run fail, permanently rather than
# intermittently. Same class as the GOLANGCI_LINT_CACHE leak below.
run_fixture() {
  local dir="$1" cache="$2" gocache="$3" errfile="$4" rc=0
  : > "${errfile}"
  ( cd "${dir}" &&
    env -u GOLANGCI_LINT_CACHE GOWORK=off \
      GOLANGCI_LINT_CACHE="${cache}" GOCACHE="${gocache}" \
      golangci-lint run --allow-parallel-runners --config "${dir}/.golangci.yml" ./... ) \
    > "${dir}/.run.out" 2> "${dir}/.run.err" || rc=$?
  # 0 = clean, 1 = findings. Anything else means the run did not complete, and
  # its stdout says nothing about caches.
  if [ "${rc}" != "0" ] && [ "${rc}" != "1" ]; then
    {
      printf 'golangci-lint exited %s in %s:\n' "${rc}" "${dir}"
      sed 's/^/        /' "${dir}/.run.err" | head -5
    } > "${errfile}"
    return 1
  fi
  finding_paths < "${dir}/.run.out"
}

# Reports a fixture run that did not complete, naming which half died and
# carrying the captured cause. "<no findings>" must never be printable without
# one of these: an assertion that cannot tell "my input did nothing" from "my
# tool died" names the wrong cause every time.
warn_fixture_died() {
  local what="$1" errfile="$2"
  {
    echo "lint: NOTE -- ${what} could not run, so it proved nothing this invocation:"
    if [ -s "${errfile}" ]; then
      sed 's/^/lint:         /' "${errfile}"
    else
      echo "lint:         (no cause captured -- this is itself a defect in run_fixture)"
    fi
    echo "lint:         The detector half is unaffected and still fails closed. Continuing."
  } >&2
}

# Asserts the DIFFERENCE isolation makes, with the real tool. Warns rather than
# fails when it cannot reach a verdict; see WHY THE CONTROL WARNS above. Returns
# 0 for "proved" and for "inconclusive", 1 only for a positive demonstration
# that isolation does not work.
assert_isolation_matters() {
  local tmp="$1" shared_b iso_a iso_b cache_a cache_b
  local gocache="$1/gocache" errfile="$1/fixture-error"

  plant_fixture_module "${tmp}/a"
  plant_fixture_module "${tmp}/b"
  mkdir -p "${gocache}"

  # THE CONTROL. One cache, a/ first, then b/. The shared cache is a dedicated
  # temp directory, NOT the developer's real one: a self-test that ran
  # `golangci-lint cache clean` every invocation would throw away the cache the
  # very next real run needs.
  #
  # There is no retry here. Round 1 added one for the case where a/'s entry is
  # not yet visible; measured, it fired exactly once and never engaged on the
  # dominant failure, which was the parallel-runner lock. --allow-parallel-runners
  # addresses that at the source, so the retry was a mechanism bought for nothing
  # and is gone. Prefer the fix that deletes a dependency.
  if ! run_fixture "${tmp}/a" "${tmp}/shared-cache" "${gocache}" "${errfile}" >/dev/null ||
     ! shared_b="$(run_fixture "${tmp}/b" "${tmp}/shared-cache" "${gocache}" "${errfile}")"; then
    warn_fixture_died "the shared-cache control" "${errfile}"
    return 0
  fi

  case "${shared_b}" in
    ../a/*) ;;   # reproduced: the control can fail, so the assertion below means something
    *)
      {
        echo "lint: NOTE -- the self-test could not reproduce #146 this run, so the isolation"
        echo "lint:         half proved nothing. Linting b/ through a cache already holding a/"
        echo "lint:         should have reported a path under ../a/ and reported: ${shared_b:-<no findings>}"
        echo "lint:         The fixture runs themselves SUCCEEDED, so this is not a tooling"
        echo "lint:         failure -- b/ genuinely computed fresh. Do NOT read one occurrence"
        echo "lint:         as upstream having fixed #146, and do not delete"
        echo "lint:         assert_isolation_matters on the strength of it. If it never"
        echo "lint:         reproduces across many runs on an idle machine, THEN re-evaluate."
        echo "lint:         The detector half is unaffected and still fails closed. Continuing."
      } >&2
      return 0 ;;
  esac

  cache_a="$(TMPDIR="${tmp}" lint_cache_dir "${tmp}/a")" || return 1
  cache_b="$(TMPDIR="${tmp}" lint_cache_dir "${tmp}/b")" || return 1
  if [ "${cache_a}" = "${cache_b}" ]; then
    echo "self-test: lint_cache_dir maps two distinct roots to '${cache_a}'" >&2
    return 1
  fi

  if ! iso_a="$(run_fixture "${tmp}/a" "${cache_a}" "${gocache}" "${errfile}")" ||
     ! iso_b="$(run_fixture "${tmp}/b" "${cache_b}" "${gocache}" "${errfile}")"; then
    # Deliberately a DIFFERENT message from the control's. These are two states
    # and one sentence for both named the wrong cause.
    warn_fixture_died "the isolated runs" "${errfile}"
    return 0
  fi

  # Positive demonstration that isolation does not work: fatal.
  if [ "${iso_a}" != "pkg/foo.go" ] || [ "${iso_b}" != "pkg/foo.go" ]; then
    {
      echo "self-test: per-root caches did NOT keep the fixture runs in-tree, though the"
      echo "           shared-cache control reproduced normally. Isolation is broken."
      echo "  a/ reported: ${iso_a:-<no findings>}"
      echo "  b/ reported: ${iso_b:-<no findings>}"
      echo "  expected 'pkg/foo.go' from each."
    } >&2
    return 1
  fi
  control_proved=1
}

# --- self-test ---------------------------------------------------------------

control_proved=0

self_test() {
  local tmp status=0
  command -v golangci-lint >/dev/null 2>&1 ||
    fail "golangci-lint is not on PATH; see CONTRIBUTING.md for the version this repo expects"
  require_digest_tool

  tmp="$(mktemp -d)"
  # A trap, not straight-line cleanup: an interrupt between here and the rm
  # leaked the directory, measured under SIGTERM.
  trap 'rm -rf "${tmp}"' EXIT

  assert_detects "${tmp}" || status=1
  assert_main_wiring "${tmp}" || status=1
  assert_isolation_matters "${tmp}" || status=1

  rm -rf "${tmp}"
  trap - EXIT

  if [ "${status}" != "0" ]; then
    echo "lint: SELF-TEST FAILED -- this script can no longer be shown to do what it claims." >&2
    exit 1
  fi
  # States exactly what was proved, and no more. In particular it distinguishes
  # the two deterministic halves from the control, which may have been
  # inconclusive this run (it says so on stderr when it was).
  if [ "${control_proved}" = "1" ]; then
    echo "lint: self-test OK -- escaping_paths reports both escape shapes and none of the near-misses; this script, run end to end against a stub, passes golangci-lint's exit status through, returns ${ESCAPE_EXIT} on a misattributed path, pins the output sink and exports its per-worktree cache; and a deliberately shared cache still misattributes the fixture while per-root caches do not."
  else
    echo "lint: self-test OK -- escaping_paths reports both escape shapes and none of the near-misses; this script, run end to end against a stub, passes golangci-lint's exit status through, returns ${ESCAPE_EXIT} on a misattributed path, pins the output sink and exports its per-worktree cache. The shared-cache control was inconclusive this run; see the note above."
  fi
}

# --- main --------------------------------------------------------------------

case "${1:-}" in
  --self-test)
    [ "$#" -eq 1 ] || fail "--self-test takes no other arguments (got: $*)"
    self_test
    exit 0
    ;;
esac

# --path-prefix rewrites every reported path, which blinds the detector AND the
# reader: unlike a redirected output sink, there is nothing left in the output
# for a human to notice. Refused rather than worked around.
for arg in "$@"; do
  case "${arg}" in
    --path-prefix|--path-prefix=*)
      fail "refusing --path-prefix: it rewrites reported paths, so neither this script nor you could see a finding attributed to another checkout (#146). Run without it." ;;
  esac
done

# --help and --version lint nothing, so there is no output to misattribute and
# nothing for the guard to protect -- and the self-test costs a cold fixture
# compile (~3s, occasionally more) that they would otherwise pay on every call.
# Narrow on purpose: ONLY when such a flag is the sole argument, so it cannot be
# used to smuggle a real run past the guard.
case "${1:-}" in
  --help|-h)
    [ "$#" -eq 1 ] && exec golangci-lint run --help
    ;;
  --version)
    # NOT `run --version`: --version is a top-level flag, not a run flag.
    [ "$#" -eq 1 ] && exec golangci-lint --version
    ;;
esac

# Skipped only for the child processes assert_main_wiring spawns, which would
# otherwise recurse forever.
if [ -z "${_WRKFLW_LINT_SELF_TEST_DEPTH:-}" ]; then
  self_test
fi

root="$(physical_dir "$(worktree_root)")" || fail "cannot resolve the worktree root"
pwd_phys="$(physical_dir "${PWD}")" || fail "cannot resolve the current directory"

# An explicitly exported value is honoured rather than overridden: a caller that
# set it made a deliberate choice, and silently discarding it would be a worse
# surprise than the one this script fixes. The guarantee is then theirs -- which
# is precisely the case escaping_paths below is here to catch.
if [ -n "${GOLANGCI_LINT_CACHE:-}" ]; then
  echo "lint: GOLANGCI_LINT_CACHE is already set to '${GOLANGCI_LINT_CACHE}'; honouring it. Per-worktree isolation is the caller's to guarantee." >&2
else
  require_digest_tool
  GOLANGCI_LINT_CACHE="$(lint_cache_dir "${root}")" || fail "could not derive a cache directory"
  prepare_cache_root "${GOLANGCI_LINT_CACHE}"
  export GOLANGCI_LINT_CACHE
fi

out="$(mktemp)"
trap 'rm -f "${out}"' EXIT

# Not `exec`: the output has to be read back before this process can exit.
# stdout is captured (which also drops golangci-lint's colour, since it is no
# longer a tty) and replayed; stderr streams through untouched.
#
# The trailing --output.text.path=stdout is not cosmetic. Without it a caller
# passing --output.json.path stdout or --output.text.path stderr moves the
# findings somewhere this capture cannot read, and the detector goes silent
# while the run still looks healthy -- measured exit 1 with a live
# misattribution on screen and no warning. Last flag wins, so this pins it back.
rc=0
golangci-lint run "$@" --output.text.path=stdout > "${out}" || rc=$?
cat "${out}"

escaped="$(escaping_paths "${root}" "${pwd_phys}" < "${out}")"
if [ -n "${escaped}" ]; then
  {
    echo
    echo "lint: FAIL -- golangci-lint reported findings against files that do not belong to"
    echo "      this checkout (${root}):"
    printf '%s\n' "${escaped}" | sed 's/^/    /'
    echo
    echo "That is #146: the cache is keyed by content and shared across checkouts, so an"
    echo "entry first written by another checkout is replayed here carrying THAT checkout's"
    echo "path. The findings above are attributed to the wrong tree, and the expensive half"
    echo "of the mistake is the opposite one -- a real finding in this worktree read as"
    echo "noise from another and waved off."
    echo
    echo "This script exists to make that impossible, so seeing it means the isolation did"
    echo "not take. Check GOLANGCI_LINT_CACHE (currently '${GOLANGCI_LINT_CACHE}'): if a"
    echo "caller exported a shared value, stop doing that; if it is per-worktree and this"
    echo "still happened, golangci-lint's cache handling has changed and lint_cache_dir"
    echo "needs revisiting. Re-derive every finding above inside this worktree before"
    echo "acting on it -- re-derive, not dismiss."
  } >&2
  exit "${ESCAPE_EXIT}"
fi

exit "${rc}"
