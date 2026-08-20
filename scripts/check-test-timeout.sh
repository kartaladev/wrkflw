#!/usr/bin/env bash
# Enforce ADR-0184's sizing rule for `require.Eventually` budgets:
#
#     eventuallyBudget x (site count in the densest package) < go test -timeout
#
# Nothing enforced this before (backlog 48). `scheduler/waitbudget_test.go` states
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
#   * lowering `-timeout` below an existing package's product;
#   * removing `-timeout` from either invocation, or letting the two disagree.
#
# SCOPE (stated so nobody over-reads a green run): it covers Eventually sites that
# pass the shared `eventuallyBudget` constant. A site passing a bare literal is not
# counted — using the constant is the convention ADR-0184 established, and a literal
# is a separate review problem. `require.Never` budgets are deliberately excluded:
# a Never budget is paid in full on every GREEN run and is governed by ADR-0184 §4,
# not by this ceiling.
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

# --- 2. per-package budget x site count --------------------------------------

status=0
printf '%-42s %8s %6s %10s %s\n' PACKAGE BUDGET SITES PRODUCT VERDICT

# Every package that declares its own budget owns its own binary and therefore its
# own timeout. `find` keeps this from rotting if a fifth waitbudget_test.go appears.
for decl in $(find . -name waitbudget_test.go -not -path './.git/*' | sort); do
  dir="$(dirname "${decl}")"
  pkg="${dir#./}"

  # `const eventuallyBudget = 10 * time.Second`
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

  product=$(( budget_s * sites ))
  verdict=ok
  if (( product >= timeout_s )); then
    verdict='OVER LIMIT'
    status=1
  fi
  printf '%-42s %7ds %6d %9ds %s\n' "${pkg}" "${budget_s}" "${sites}" "${product}" "${verdict}"
done

echo
echo "go test -timeout: ${ci_timeout} (${timeout_s}s), agreed by ${workflow} and ${coverage}"

if (( status != 0 )); then
  cat >&2 <<'EOF'

check-test-timeout: FAIL — at least one package's worst-case Eventually cost meets or
exceeds the go test binary timeout. A mass failure in that package would be reported as
"panic: test timed out" with no assertion messages at all.

Fix by one of:
  * lowering that package's eventuallyBudget (it is a FAILURE ceiling, not an expected
    latency — a green run returns as soon as its condition holds);
  * splitting the package so the sites spread across more than one test binary;
  * raising -timeout in BOTH .github/workflows/ci.yml and scripts/coverage.sh, which is a
    deliberate decision that belongs in an ADR (see ADR-0184).
EOF
fi

exit "${status}"
