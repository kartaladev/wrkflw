#!/usr/bin/env bash
# Enforce the sizing rule for `require.Eventually` budgets:
#
#     eventuallyBudget x (site count in the densest package) < go test -timeout
#
# Nothing enforced this before. `scheduler/waitbudget_test.go` states
# the rule in a doc comment and both `go test` invocations ran at Go's implicit
# 600s default, so a budget raise or a new batch of Eventually sites could push a
# mass failure past the binary timeout silently. When that happens the binary dies
# with "panic: test timed out" plus a goroutine dump and prints NO assertion
# messages — every broken site loses its name, which is the failure mode the rule
# exists to prevent.
#
# EVERY NUMBER HERE IS DERIVED FROM SOURCE. Nothing is hard-coded:
#   * the timeout comes from the `-timeout=` flag actually written in
#     .github/workflows/ci.yml and scripts/coverage.sh (and the two must agree);
#   * each budget comes from the `const eventuallyBudget` in that package's
#     waitbudget_test.go;
#   * each site count is grepped out of that package's *_test.go files.
# A version that hard-coded today's 10s / 34 sites would pass forever regardless
# of what the tests do, which is precisely the class of vacuous check this repo
# has shipped before.
#
# What makes this script FAIL:
#   * raising any `eventuallyBudget` far enough that budget x sites >= timeout;
#   * adding enough new `eventuallyBudget` Eventually sites to one package to
#     cross the same product;
#   * adding a raw `time.After` deadline, or enough of them, to push one
#     package's total over the timeout;
#   * writing a `time.After` whose duration this cannot parse — a variable or a
#     computed expression — which stops the build rather than being skipped;
#   * lowering `-timeout` below an existing package's total;
#   * removing `-timeout` from either invocation, or letting the two disagree.
#
# SCOPE (stated so nobody over-reads a green run):
#
#   COVERED — Eventually sites passing the shared `eventuallyBudget` constant,
#   and raw `select { case <-time.After(d) }` deadlines in test files (added by
#   #66: 45 of them across 27 files sat outside the old count entirely, because
#   a raw select carries no identifier to grep and so fell outside even this
#   caveat's original wording).
#
#   NOT COVERED — an Eventually site passing a bare literal instead of the
#   constant. Using the constant is the established convention and a literal is
#   a separate review problem.
#
#   NOT COVERED, AND DELIBERATELY SO — `require.Never` budgets, and the raw
#   negative windows that are their hand-rolled equivalent (see section 3).
#   A Never budget is paid in full on every GREEN run: it wants to be SHORT, so
#   a ceiling is the wrong instrument for it and raising one is pure cost.
#
# ⚠ WHAT THIS GUARD CANNOT DO, stated because #66 was filed off a flake it does
# not address. This bounds deadlines from ABOVE, so a mass failure still prints
# assertion messages instead of "panic: test timed out". The flake that prompted
# #66 was a deadline too small from BELOW — 3s of real wall-clock against
# container I/O under CI load. A ceiling cannot ask "is this one deadline
# generous enough?", only "is the sum small enough?". Those are opposite
# directions, and no widening of this script reaches the second one.
#
# Usage: scripts/check-test-timeout.sh   (from the repo root; no Docker, no network)
#
# Kept POSIX-bash-friendly (no mapfile/associative arrays) so it runs under the
# bash 3.2 that ships on macOS as well as CI's bash 5.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

workflow=".github/workflows/ci.yml"
coverage="scripts/coverage.sh"

fail() { echo "check-test-timeout: $*" >&2; exit 1; }

# --- 1. the configured timeout, read from both invocations -------------------

# Two accepted spellings, both of which must resolve to a literal duration:
#   * the flag written inline           -> `-timeout=600s` / `-timeout 600s`
#   * a shell default feeding the flag  -> `GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-600s}"`
# Comment lines are stripped first, so the prose above each invocation (which
# mentions "600s") can never be mistaken for the configured value.
extract_timeout() {
  local body
  body="$(grep -v -E '^[[:space:]]*#' "$1" | sed -e 's/#.*$//')"

  local inline
  inline="$(printf '%s\n' "${body}" | grep -oE -- '-timeout[= ]+"?[0-9]+[smh]' | grep -oE '[0-9]+[smh]' | head -1)"
  if [[ -n "${inline}" ]]; then
    printf '%s\n' "${inline}"
    return 0
  fi

  # Indirect form: the flag must actually be passed, and the variable must carry
  # a literal default. Requiring both stops a bare `-timeout="$UNSET_VAR"` from
  # reading as configured when it would in fact expand to nothing.
  printf '%s\n' "${body}" | grep -qE -- '-timeout[= ]+"?\$\{?GO_TEST_TIMEOUT' || return 0
  printf '%s\n' "${body}" \
    | grep -oE 'GO_TEST_TIMEOUT[:=]-?"?\$\{GO_TEST_TIMEOUT:-[0-9]+[smh]' \
    | grep -oE '[0-9]+[smh]$' \
    | head -1
}

ci_timeout="$(extract_timeout "${workflow}" || true)"
cov_timeout="$(extract_timeout "${coverage}" || true)"

[[ -n "${ci_timeout}" ]]  || fail "no -timeout flag found in ${workflow}; go test would fall back to the implicit 600s default and this rule would go unenforced"
[[ -n "${cov_timeout}" ]] || fail "no -timeout flag found in ${coverage}; go test would fall back to the implicit 600s default and this rule would go unenforced"

if [[ "${ci_timeout}" != "${cov_timeout}" ]]; then
  fail "timeout mismatch: ${workflow} says ${ci_timeout} but ${coverage} says ${cov_timeout}. The two must agree, or a locally-green coverage run proves nothing about CI."
fi

to_seconds() {
  local v="$1" n="${1%[smh]}" u="${1: -1}"
  case "${u}" in
    s) echo "${n}" ;;
    m) echo $(( n * 60 )) ;;
    h) echo $(( n * 3600 )) ;;
    *) fail "unparsable duration: ${v}" ;;
  esac
}

timeout_s="$(to_seconds "${ci_timeout}")"

# --- 2. duration parsing -----------------------------------------------------

# duration_ms EXPR — EXPR is a Go duration literal with the spaces already
# stripped ("2*time.Second", "500*time.Millisecond", "time.Second"). Prints its
# value in milliseconds, or NOTHING if it cannot parse EXPR.
#
# ⚠ It reports the failure by printing nothing rather than by calling `fail`,
# and the CALLER turns that into a build failure. That is not a style choice:
# this function is invoked in a command substitution, so an `exit` inside it
# only kills the subshell — the parent loop carries on and the script exits 0.
# The first draft of this did exactly that, printing a clear diagnostic and then
# passing anyway, which is the precise defect the parse-strictness below exists
# to prevent. Verified by planting `time.After(d)` and checking $? is 1.
#
# Parse strictness itself is deliberate: a guard that silently drops what it
# cannot read reports a number that looks derived but under-counts, which is the
# failure mode this repo has now hit repeatedly. A deadline this cannot parse is
# a deadline nobody is bounding, so it stops the build and asks for a literal.
duration_ms() {
  local e="$1" n unit
  case "${e}" in
    *\**) n="${e%%\**}"; unit="${e##*\*}" ;;
    *)    n=1;           unit="${e}" ;;
  esac
  case "${n}" in
    ''|*[!0-9]*) return 0 ;;
  esac
  case "${unit}" in
    time.Millisecond) echo $(( n )) ;;
    time.Second)      echo $(( n * 1000 )) ;;
    time.Minute)      echo $(( n * 60000 )) ;;
    *) return 0 ;;
  esac
}

# --- 3. per-package worst-case failure cost -----------------------------------
#
# Two families contribute to the wall-clock a MASS FAILURE in one package burns,
# and until #66 the script could only see one of them:
#
#   * Eventually sites passing eventuallyBudget -> budget x count.
#   * raw `select { case <-time.After(d) }` deadlines -> the sum of their d's.
#     These are not Eventually sites at all, so they carried no identifier for
#     the old grep to find and sat outside even its stated caveat.
#
# ⚠ THE RAW SUM DELIBERATELY OVER-APPROXIMATES. It counts every time.After in
# the package's test files, including the two families that are NOT paid on the
# failing path:
#
#   * NEGATIVE WINDOWS (6, 2.2s) — `case <-ch: t.Fatal(...)` opposite a bare
#     `case <-time.After(d):`. A hand-rolled require.Never: paid in full on every
#     GREEN run and wanting to stay SHORT, so bounding it from above is not what
#     it needs.
#   * A DRAIN LOOP (1, 0.2s) — the deadline is a loop's only exit. Not a negative
#     window (nothing fails) but the same cost profile.
#   * FIXTURE FALLBACKS (3, 6.0s) — time.After inside a test double or helper (a
#     fake blocking action; a resolver that turns a hang into a readable
#     failure). Not a deadline on an assertion at all.
#   * AN ASSERTING BRANCH (1, 2.0s) — the deadline clause cancels and then
#     asserts, so it is part of what the test exercises.
#
# Counting them anyway costs 10.4s repo-wide and buys a rule with no classifier
# in it. Over-approximating a CEILING is the safe direction — it can only make
# the guard stricter, never laxer — whereas a bash classifier for "does the
# sibling comm-clause call t.Fatal" would be exactly the kind of clever
# line-oriented heuristic that silently mis-reads and under-counts. See
# docs/agents/test-deadlines.md for the five constructs and their counts.

status=0
printf '%-44s %7s %6s %8s %6s %8s %9s %s\n' \
  PACKAGE BUDGET EV_SITES EV_COST RAW_N RAW_COST TOTAL VERDICT

for dir in $(find . -name '*_test.go' -not -path './.git/*' -not -path './.claude/*' \
             | sed -e 's:/[^/]*$::' | sort -u); do
  pkg="${dir#./}"

  # -- Eventually contribution (only packages that declare their own budget) --
  budget_s=0
  sites=0
  decl="${dir}/waitbudget_test.go"
  if [[ -f "${decl}" ]]; then
    budget_line="$(grep -E 'const[[:space:]]+eventuallyBudget[[:space:]]*=' "${decl}" || true)"
    [[ -n "${budget_line}" ]] || fail "${decl} declares no 'const eventuallyBudget'"

    budget_n="$(echo "${budget_line}" | grep -oE '=[[:space:]]*[0-9]+' | grep -oE '[0-9]+')"
    budget_unit="$(echo "${budget_line}" | grep -oE 'time\.(Second|Millisecond|Minute)' | head -1)"
    case "${budget_unit}" in
      time.Second)      budget_s="${budget_n}" ;;
      time.Minute)      budget_s=$(( budget_n * 60 )) ;;
      time.Millisecond) budget_s=$(( (budget_n + 999) / 1000 )) ;;  # round up; a sub-second budget still costs >=1s of ceiling
      *) fail "${decl}: cannot parse the unit of '${budget_line}'" ;;
    esac

    # Count real uses: every `eventuallyBudget` occurrence in the package's test
    # files, minus comment lines and the const declaration itself. Occurrences are
    # counted, not lines, so two on one line count twice.
    sites="$(cat "${dir}"/*_test.go 2>/dev/null \
      | grep -v -E '^[[:space:]]*//' \
      | grep -v -E 'const[[:space:]]+eventuallyBudget' \
      | grep -oh 'eventuallyBudget' \
      | wc -l | tr -d ' ')"
  fi
  ev_cost=$(( budget_s * sites ))

  # -- raw select-deadline contribution ---------------------------------------
  raw_ms=0
  raw_n=0
  for expr in $(cat "${dir}"/*_test.go 2>/dev/null \
                | grep -v -E '^[[:space:]]*//' \
                | grep -oh 'time\.After([^)]*)' \
                | sed -e 's/^time\.After(//' -e 's/)$//' -e 's/[[:space:]]//g'); do
    ms="$(duration_ms "${expr}")"
    case "${ms}" in
      ''|*[!0-9]*)
        fail "cannot parse the duration '${expr}' in ${pkg}: the guard counts integer literals like '2*time.Second' or '500*time.Millisecond'. (The quoted text stops at the first ')', so a nested call such as time.Duration(n)*time.Second reads truncated here.) A duration it cannot read is a deadline nobody is bounding — spell it as a literal, or hoist the wait to eventuallyBudget." ;;
    esac
    raw_ms=$(( raw_ms + ms ))
    raw_n=$(( raw_n + 1 ))
  done
  raw_s=$(( (raw_ms + 999) / 1000 ))  # round up, matching the budget rule above

  (( sites > 0 || raw_n > 0 )) || continue

  total=$(( ev_cost + raw_s ))
  verdict=ok
  if (( total >= timeout_s )); then
    verdict='OVER LIMIT'
    status=1
  fi
  printf '%-44s %6ds %6d %7ds %6d %7ds %8ds %s\n' \
    "${pkg}" "${budget_s}" "${sites}" "${ev_cost}" "${raw_n}" "${raw_s}" "${total}" "${verdict}"
done
echo
echo "go test -timeout: ${ci_timeout} (${timeout_s}s), agreed by ${workflow} and ${coverage}"

if (( status != 0 )); then
  cat >&2 <<'EOF'

check-test-timeout: FAIL — at least one package's worst-case failure cost (its
Eventually budget x sites, PLUS the sum of its raw time.After deadlines) meets or exceeds
the go test binary timeout. A mass failure in that package would be reported as
"panic: test timed out" with no assertion messages at all.

Fix by one of:
  * lowering that package's eventuallyBudget (it is a FAILURE ceiling, not an expected
    latency — a green run returns as soon as its condition holds);
  * shortening or removing raw time.After deadlines in that package — but read section 3
    first: a NEGATIVE window (the deadline is the benign exit, a sibling case calls
    t.Fatal) is paid on every green run and is already as short as it should be, so it is
    the wrong thing to cut;
  * splitting the package so the sites spread across more than one test binary;
  * raising -timeout in BOTH .github/workflows/ci.yml and scripts/coverage.sh, which is a
    deliberate decision: record why alongside the change.
EOF
fi

exit "${status}"
