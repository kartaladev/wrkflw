#!/usr/bin/env bash
# Assert that the `lint` job in .github/workflows/ci.yml runs golangci-lint in
# EVERY workspace module — by reading the job's actual steps, not a list that
# says it does.
#
# WHY THIS SHAPE, AND WHY THE OBVIOUS ONE IS WORSE. The first version of this
# check diffed scripts/modules.sh against a hand-written literal inside the
# step:
#
#     linted="$(printf '%s\n' '.' 'examples')"
#
# That literal is a THIRD independent copy of the module list, next to go.work
# and the golangci-lint steps themselves, and the check never read the steps.
# Deleting the entire `working-directory: examples` step and leaving the
# literal alone left the check exiting 0 — measured, not argued. It failed open
# in precisely the scenario its own error message named, and the ablation that
# was reported for it (shortening the literal) tested the `diff`, not the
# property. Rule: a criterion that is a count verifies presence, never
# placement, and a name is not a property.
#
# So this parses the steps. golangci-lint has no workspace-wide mode, and this
# job is the ONLY path by which govet, gofmt and gosec reach CI at all — they
# arrive through .golangci.yml's `default: standard`, its `formatters` block
# and its `enable` list, never as steps of their own. A module with no lint
# step is a module with no vet, no gofmt and no SAST, reported green.
#
# The steps are deliberately one-per-module rather than one invocation with a
# cross-module pattern list: the action gives each working-directory its own
# golangci-lint cache key, which is what stops the #146 content-keyed cache
# from re-prefixing a module directory onto a path it already contains and
# silently dropping that file's //nolint directives.
#
# WHAT THE PARSER CANNOT DO, stated because that is where guards hide. It reads
# YAML by structure, not with a YAML parser: it finds the `lint:` job, then
# every step in it mentioning `golangci/golangci-lint-action`, and takes that
# step's `working-directory:` (default `.`). It therefore does NOT understand
# `strategy.matrix`, `if:` conditions, anchors, or flow-style mappings. Each of
# those would make it MIS-READ rather than fail, so the self-test below pins
# the two spellings actually used, and finding zero steps is a hard error
# rather than an empty comparison.
#
# Usage: scripts/check-lint-modules.sh   (run from anywhere)
set -euo pipefail

fail() { echo "check-lint-modules: $*" >&2; exit 1; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${repo_root}"

workflow=".github/workflows/ci.yml"
[ -f "${workflow}" ] || fail "no ${workflow}"

parser="$(mktemp)"
trap 'rm -f "${parser}"' EXIT
cat > "${parser}" <<'AWK'
function flush() {
  if (started && isgcl) print (wd == "" ? "." : wd)
  started = 0; isgcl = 0; wd = ""
}
/^  lint:[[:space:]]*$/            { injob = 1; next }
injob && /^  [^ [:space:]]/        { flush(); injob = 0 }
!injob                             { next }
/^      - /                        { flush(); started = 1 }
started && /golangci\/golangci-lint-action/ { isgcl = 1 }
started && /^[[:space:]]*working-directory:[[:space:]]*/ {
  w = $0
  sub(/^[[:space:]]*working-directory:[[:space:]]*/, "", w)
  gsub(/["'\'']/, "", w)
  sub(/[[:space:]]+$/, "", w)
  wd = w
}
END { flush() }
AWK

lint_dirs() { awk -f "${parser}" "$1"; }

# --- SELF-TEST: prove the parser both SEES a step and MISSES a deleted one ---
#
# A parser that returned the right answer by accident, or that silently matches
# nothing, would make this whole check vacuous a second time. So run it over
# two fixtures with known answers before trusting it on the real file. The
# second fixture is the exact mutation the previous version of this guard
# survived.
st_a="$(mktemp)"; st_b="$(mktemp)"
cat > "${st_a}" <<'FIX'
jobs:
  lint:
    name: golangci-lint
    steps:
      - uses: actions/checkout@v7
      - uses: golangci/golangci-lint-action@v9
        with:
          version: v2.13.2
      - name: Lint the examples module
        uses: golangci/golangci-lint-action@v9
        with:
          version: v2.13.2
          working-directory: examples
  govulncheck:
    steps:
      - uses: golangci/golangci-lint-action@v9
        with:
          working-directory: not-the-lint-job
FIX
cat > "${st_b}" <<'FIX'
jobs:
  lint:
    name: golangci-lint
    steps:
      - uses: actions/checkout@v7
      - uses: golangci/golangci-lint-action@v9
        with:
          version: v2.13.2
  govulncheck:
    steps: []
FIX
st_a_out="$(lint_dirs "${st_a}" | tr '\n' ' ')"
st_b_out="$(lint_dirs "${st_b}" | tr '\n' ' ')"
rm -f "${st_a}" "${st_b}"
[ "${st_a_out}" = ". examples " ] || fail "self-test A failed: expected '. examples ', got '${st_a_out}'.
  The parser can no longer read the two step spellings this workflow uses, or it
  is reading steps from the wrong job. Refusing to vouch for the real file."
[ "${st_b_out}" = ". " ] || fail "self-test B failed: expected '. ', got '${st_b_out}'.
  The parser did not notice a DELETED lint step — which is the exact failure the
  previous version of this check shipped. Refusing to vouch for the real file."

# --- the real comparison ----------------------------------------------------
linted="$(lint_dirs "${workflow}" | sort -u)"
n_linted="$(printf '%s\n' "${linted}" | grep -c . || true)"

if [ "${n_linted}" -eq 0 ]; then
  fail "found no golangci-lint step in the 'lint' job of ${workflow}.
  The self-test above passed, so the parser works — which means the job was
  renamed, restructured, or the steps were removed. This is a parser/workflow
  mismatch, NOT a satisfied constraint: reporting it as 'every module is
  linted' would be a green run over a job that lints nothing."
fi

expected="$("${repo_root}/scripts/modules.sh" \
  | awk '{ sub(/\/\.\.\.$/, ""); sub(/^\.\//, ""); print ($0 == "" ? "." : $0) }' \
  | sort -u)"

missing="$(comm -13 <(printf '%s\n' "${linted}") <(printf '%s\n' "${expected}"))"
extra="$(comm -23 <(printf '%s\n' "${linted}") <(printf '%s\n' "${expected}"))"

if [ -n "${missing}" ]; then
  echo "ERROR: a workspace module has no golangci-lint step in ${workflow}:" >&2
  printf '  %s\n' ${missing} >&2
  echo >&2
  echo "Its packages would be linted by nothing — and because govet, gofmt and gosec" >&2
  echo "reach CI only through this job, they would be unscoped for that module too," >&2
  echo "with every check still green. Add a step:" >&2
  echo >&2
  echo "      - uses: golangci/golangci-lint-action@v9" >&2
  echo "        with:" >&2
  echo "          version: <the same version as the others>" >&2
  echo "          working-directory: <the module directory>" >&2
  exit 1
fi

if [ -n "${extra}" ]; then
  echo "ERROR: ${workflow}'s lint job runs golangci-lint in a directory that is not a workspace module:" >&2
  printf '  %s\n' ${extra} >&2
  echo "Either add it to go.work, or drop the step." >&2
  exit 1
fi

echo "OK: every workspace module has a golangci-lint step in ${workflow} — $(printf '%s' "${linted}" | tr '\n' ' ')"
