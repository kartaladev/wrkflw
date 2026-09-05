#!/usr/bin/env bash
# Stop dangling document references regrowing in Go source.
#
# 12630621 deleted docs/adr/ (125 ADRs), docs/specs/, SECURITY.md and
# STABILITY.md. #50 stripped the ~2,240 surviving references to them and a
# follow-up caught one it missed, so every .go file in the tree is now at zero.
# Nothing stopped new ones being written, and they already were: five dangling
# ADR citations were introduced in #55 AFTER the deletion commit, two of them on
# exported error doc comments in definition/model/validate.go that render on
# pkg.go.dev. They were caught only because someone happened to grep.
#
# The failure mode is structural, not careless. This codebase's established
# idiom for design provenance is an ADR citation and the surrounding comments
# are dense with that phrasing, so the natural way to write "this rejection
# follows the same reasoning as the scope-local compensation one" is to reach
# for the ADR number. The author of those five had ALREADY checked that
# docs/adr/ did not exist and wrote the citations anyway, by pattern-match
# against neighbouring comments. A prose rule asks every future author to
# remember; this script does not.
#
# THE RULE: strip the provenance, keep the constraint. Name the identifier
# instead — ErrScopeLocalWithCompensateRef, findDirectBoundary and
# ErrTriggerNeverDue are things a reader can jump to; ADR-0120 is not.
#
# SCOPE — Go source only (`*.go`, tracked plus untracked-not-ignored).
# This IS the escape hatch. Commit messages, CONTRIBUTING.md, docs/agents/ and
# the scripts/ directory are deliberately untouched: a commit message quoting
# history is legitimate, and three of those files still carry live references
# whose fate is the maintainer's call, not this check's. Confining the rule to
# Go source is what makes the strong tree-wide form available at all — the tree
# is at zero for *.go and is NOT at zero overall.
#
# TREE-WIDE, not added-lines-only. The gate that verified #55 before merge was
# `git diff "$BASE" | grep '^+' | ...`, which needs a merge base — awkward
# locally, absent on a push to main, and wrong after a rebase or squash. Because
# *.go is already at zero, scanning the whole tree needs no baseline, no diff,
# and no history: it is strictly stronger, and it also catches a violation that
# arrives by moving a file rather than by adding a line.
#
# NARROWABLE, NOT DELETABLE. The rule is about DANGLING references, not about
# ADRs. Each row below names the tracked path whose existence retires it, and a
# rule whose target comes back is reported as needing narrowing rather than
# silently switched off — see `rule_active` and the notice it prints.
#
# NON-VACUITY, in two halves — a green run has to prove BOTH, because either
# one alone passes green while the check is dead:
#
#   * DETECTION — `--self-test` plants each forbidden form in a fixture and
#     asserts the scanner reports it, then asserts a clean fixture reports
#     nothing. It runs on every invocation, not only under the flag.
#   * COVERAGE — `assert_covers_tree` asserts the scanned set is a superset of
#     every tracked *.go file. Working regexes pointed at the wrong file set
#     are exactly as useless as broken ones, and a plain "did we scan anything"
#     count cannot tell the difference: any non-empty subset satisfies it.
#
# This is the class of vacuous check this repo has shipped before, and which
# check-test-timeout.sh's own header calls out.
#
# Usage:
#   scripts/check-doc-refs.sh              # scan the tree (run from anywhere)
#   scripts/check-doc-refs.sh --self-test  # prove the scanner still detects
#
# Pure bash + git + grep: no Go toolchain, no Docker, no network. Kept
# bash-3.2-friendly (no mapfile, no associative arrays) so it runs under the
# bash that ships on macOS as well as CI's bash 5.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

fail() { echo "check-doc-refs: $*" >&2; exit 1; }

# --- the rules ---------------------------------------------------------------
#
# id | extended regex | tracked path that retires the rule | sample | advice
#
# The regexes are exactly the ones used to verify #55 by hand. They are
# case-sensitive on purpose: ADR-NNNN and the paths are how these documents were
# actually spelled, and widening to any casing would start flagging prose.
#
# If docs/adr/ is reinstated, the first two rows stop applying as written — a
# citation is only dangling when its target is gone. Narrow them then (per-ADR
# existence, say); do not delete the file.
rules() {
  cat <<'EOF'
adr-number|ADR-[0-9]|docs/adr|ADR-0120|name the identifier the decision constrains
adr-path|docs/adr|docs/adr|docs/adr/0171-cursor.md|name the identifier, not the deleted record
specs-path|docs/specs|docs/specs|docs/specs/retry.md|state the constraint inline
security-md|SECURITY\.md|SECURITY.md|SECURITY.md|state the constraint inline
stability-md|STABILITY\.md|STABILITY.md|STABILITY.md|state the constraint inline
EOF
}

# A rule is active while its target is absent from the tree. `git ls-files`
# rather than `test -e`, so an untracked local file cannot silently disable a
# check that CI is relying on.
rule_active() {
  [ -z "$(git ls-files -- "$1" | head -1)" ]
}

# --- the scanner -------------------------------------------------------------
#
# One code path, shared by the real scan and the self-test: read a
# NUL-separated file list, print `file:line:text` for every match.
scan() {
  local regex="$1" list="$2"
  # Deliberately NO -I. Go source is text — Go rejects a NUL byte outright — so
  # -I bought nothing here and cost the guard its main claim: a .go file
  # carrying a NUL was silently skipped while still being counted, which is a
  # quiet bypass of a check whose whole pitch is that it has none. Without the
  # flag such a file reports "Binary file ... matches", which fails loudly.
  xargs -0 grep -n -H -E -e "${regex}" -- < "${list}" 2>/dev/null || true
}

go_file_list() {
  # Tracked plus untracked-not-ignored, so a developer's brand-new file is
  # covered locally. In CI the two sets are identical.
  git ls-files -z --cached --others --exclude-standard -- '*.go' > "$1"
}

# The self-test proves the REGEXES detect. It cannot prove the FILE LIST covers
# the tree, because the list is built from the real repository — and a scan of
# the wrong set passes green no matter how good the regexes are. Narrowing the
# pathspec in go_file_list from '*.go' to 'engine/*.go' reads as a harmless
# scoping tweak and silently drops 947 files to 161, taking a live citation on
# an exported doc comment with it. A `count > 0` guard does not notice: any
# non-empty subset satisfies it.
#
# So assert coverage directly. SUPERSET, not equality: --others legitimately
# adds untracked files, so equality would false-fail on any dirty tree.
assert_covers_tree() {
  local list="$1" tracked="$2.tracked" scanned="$2.scanned" missing count
  git ls-files -z -- '*.go' | tr '\0' '\n' | sort > "${tracked}"
  tr '\0' '\n' < "${list}" | sort > "${scanned}"

  missing="$(comm -23 "${tracked}" "${scanned}")"
  [ -n "${missing}" ] || return 0

  count="$(printf '%s\n' "${missing}" | wc -l | tr -d ' ')"
  {
    echo "check-doc-refs: FAIL — the scan does not cover the tree."
    echo "${count} tracked *.go file(s) are absent from the scanned set, so a citation in any of"
    echo "them would pass unseen. The pathspec in go_file_list has been narrowed, or the list"
    echo "was built from the wrong root. First few missing:"
    printf '%s\n' "${missing}" | head -5 | sed 's/^/  /'
  } >&2
  exit 1
}

# --- self-test ---------------------------------------------------------------
self_test() {
  local tmp status=0 checked=0 hits declared
  tmp="$(mktemp -d)"

  # A clean file: near-misses that must NOT trip any rule.
  cat > "${tmp}/clean.go" <<'EOF'
// Package clean names identifiers instead of documents.
package clean

// ErrScopeLocalWithCompensateRef and findDirectBoundary are things a reader can
// jump to. Working documents such as docs/agents/domain.md are not dangling.
const note = "ADRIFT is not a citation, and neither is adrenaline"
EOF

  while IFS='|' read -r id regex guard sample advice; do
    [ -n "${id}" ] || continue
    checked=$(( checked + 1 ))

    # Plant the forbidden form in the three places it actually appears: a
    # package doc comment, a line comment, and a string literal.
    cat > "${tmp}/${id}.go" <<EOF
// Package fixture cites ${sample} in a doc comment.
package fixture

// helper repeats ${sample} in a line comment.
const helper = "see ${sample} for the rule"
EOF

    printf '%s\0' "${tmp}/${id}.go" > "${tmp}/list"
    hits="$(scan "${regex}" "${tmp}/list" | wc -l | tr -d ' ')"
    if [ "${hits}" != "3" ]; then
      echo "self-test: rule '${id}' (${regex}) found ${hits} of the 3 planted '${sample}' lines" >&2
      status=1
    fi

    printf '%s\0' "${tmp}/clean.go" > "${tmp}/list"
    hits="$(scan "${regex}" "${tmp}/list" | wc -l | tr -d ' ')"
    if [ "${hits}" != "0" ]; then
      echo "self-test: rule '${id}' (${regex}) reported ${hits} false positives on the clean fixture" >&2
      status=1
    fi
  done <<EOF
$(rules)
EOF

  rm -rf "${tmp}"

  # Derived from the table, never pinned to today's row count. A literal floor
  # of 5 would hard-fail a sanctioned narrowing — merging the two ADR rows into
  # one, say — while claiming "the table has been emptied" about a table holding
  # four rules. That is the same over-specification the rule_active probes below
  # avoid. What actually needs asserting is that the loop exercised every row it
  # was given (catching a truncated read) and that there is at least one.
  declared="$(rules | grep -c '[^[:space:]]' || true)"
  if [ "${declared}" -lt 1 ]; then
    echo "self-test: the rules table is empty; nothing would ever be checked" >&2
    status=1
  elif [ "${checked}" != "${declared}" ]; then
    echo "self-test: the table declares ${declared} rules but only ${checked} were exercised" >&2
    status=1
  fi

  # Both branches of the retirement guard. Deliberately NOT asserted against
  # docs/adr: that path's state is the thing allowed to change, and pinning
  # today's answer would break this self-test on the very day the directory is
  # reinstated — the one case the guard exists to handle. These two probes have
  # a state that cannot drift: CONTRIBUTING.md is tracked, and the other cannot
  # be added without editing this line.
  if rule_active CONTRIBUTING.md; then
    echo "self-test: CONTRIBUTING.md is tracked, yet rule_active reports its rule as active" >&2
    status=1
  fi
  if ! rule_active .check-doc-refs-no-such-path; then
    echo "self-test: an absent path reads as present, so every rule would retire" >&2
    status=1
  fi

  if [ "${status}" != "0" ]; then
    echo "check-doc-refs: SELF-TEST FAILED — the scanner can no longer be shown to detect what it claims to." >&2
    exit 1
  fi
  # States exactly what was proved, and no more. Detection is proved here; that
  # the scan is pointed at the whole tree is a separate claim, proved against
  # the real repository by assert_covers_tree.
  echo "check-doc-refs: self-test OK — ${checked} rules each detect a planted citation in a doc comment, a line comment and a string, and none fires on clean source (detection only; tree coverage is asserted separately)."
}

# --- main --------------------------------------------------------------------
case "${1:-}" in
  --self-test) self_test; exit 0 ;;
  "") ;;
  *) fail "unknown argument '$1' (expected nothing, or --self-test)" ;;
esac

self_test

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
go_file_list "${tmp}/list"

files="$(tr -cd '\0' < "${tmp}/list" | wc -c | tr -d ' ')"
[ "${files}" -gt 0 ] || fail "no *.go files found under ${repo_root}; the scan would pass vacuously"

# A non-empty list is not a covering one — see assert_covers_tree.
assert_covers_tree "${tmp}/list" "${tmp}/cov"

status=0
active=0
findings="${tmp}/findings"
: > "${findings}"

while IFS='|' read -r id regex guard sample advice; do
  [ -n "${id}" ] || continue

  if ! rule_active "${guard}"; then
    echo "check-doc-refs: NOTICE — '${guard}' is back in the tree, so rule '${id}' is not applied."
    echo "                A citation is only dangling when its target is gone. NARROW this rule to check"
    echo "                that each cited document resolves; do not leave it retired, and do not delete it."
    continue
  fi
  active=$(( active + 1 ))

  hits="$(scan "${regex}" "${tmp}/list")"
  if [ -n "${hits}" ]; then
    status=1
    printf '\n  %s — %s (%s):\n' "${id}" "${advice}" "${regex}" >> "${findings}"
    printf '%s\n' "${hits}" | sed 's/^/    /' >> "${findings}"
  fi
done <<EOF
$(rules)
EOF

[ "${active}" -gt 0 ] || fail "every rule is retired; nothing was checked. Narrow the table rather than emptying it."

if [ "${status}" != "0" ]; then
  {
    echo
    echo "check-doc-refs: FAIL — Go source cites documents that do not exist in this repository."
    echo "Deleted in 12630621; the ~2,240 surviving references were stripped by #50."
    cat "${findings}"
    cat <<'EOF'

Strip the provenance, keep the constraint. Name the identifier instead:
ErrScopeLocalWithCompensateRef, findDirectBoundary and ErrTriggerNeverDue are
things a reader can jump to; ADR-0120 is not. Doc comments on exported
identifiers render on pkg.go.dev, where a citation points a consumer at a
document they cannot open.

If one of these documents has genuinely been reinstated, this script narrows
rather than gets deleted — see the rules table in scripts/check-doc-refs.sh.
EOF
  } >&2
  exit 1
fi

echo "check-doc-refs: OK — ${files} Go files (every tracked *.go covered), ${active} rules active, no dangling document references."
