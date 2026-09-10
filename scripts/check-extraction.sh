#!/usr/bin/env bash
# Enforce the extraction constraint: the database toolkit packages
# internal/database and internal/database/transaction must import ONLY the Go
# standard library and the database drivers — never any other wrkflw package —
# so they can be lifted out as a standalone module. Test helpers that need other
# wrkflw packages live in internal/dbtest, which is not part of this graph.
#
# Fails (exit 1) if `go list -deps` on the toolkit pulls in any
# github.com/kartaladev/wrkflw package other than the two toolkit packages.
#
# TWO ANCHORS, AND THEY MOVE TOGETHER. This check is pinned to the toolkit by
# two independent strings, and a module or path move breaks BOTH at once:
#
#   * ${toolkit_pattern}  — the `go list -deps` TARGET. If the toolkit moves to
#     another directory or into another module, this pattern stops resolving
#     (loudly) or resolves to nothing.
#   * ^${module}          — the grep that decides which dependencies are "ours".
#     If the toolkit's module path changes, or the constraint is re-anchored to
#     a sibling module, this prefix stops matching the packages it is meant to
#     catch — and a grep that matches nothing produces an EMPTY unexpected set,
#     which is indistinguishable from a satisfied constraint.
#
# The second failure is the dangerous one, because it is green. Both anchors are
# therefore hoisted to the top of this file, both are named in the failure
# message, and this job's check-run name carries the path as well
# ("extraction constraint (internal/database)") — rename it in the same commit
# that moves the toolkit, or the guard will go red while naming a package that
# no longer exists and send the reader hunting an extraction regression instead
# of the module move that actually caused it.
#
# Usage: scripts/check-extraction.sh   (run from anywhere)
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${repo_root}"

# --- the two anchors ---------------------------------------------------------
module="github.com/kartaladev/wrkflw"
toolkit_dir="internal/database"
toolkit_pattern="./${toolkit_dir}/..."

allowed="$(printf '%s\n' \
  "${module}/${toolkit_dir}" \
  "${module}/${toolkit_dir}/transaction" | sort)"

# The prefix is anchored at a path boundary and its dots are escaped. Sibling
# module paths under github.com/kartaladev/ now exist (…/examples, and …/
# persistence next), so a bare "^${module}" — with `.` matching any character —
# would also match an unrelated `github.com/kartaladevX/wrkflwXYZ`. It errs
# towards over-reporting, which is the safe direction, but the honest spelling
# costs nothing.
module_re="^$(printf '%s' "${module}" | sed 's/\./\\./g')(/|\$)"

actual="$(go list -deps "${toolkit_pattern}" | grep -E "${module_re}" | sort || true)"

if [ -z "${actual}" ]; then
  echo "ERROR: extraction constraint could not be evaluated." >&2
  echo "  'go list -deps ${toolkit_pattern}' returned no package matching ${module_re}," >&2
  echo "  not even the toolkit packages themselves. That is not a satisfied" >&2
  echo "  constraint — it is a check that no longer looks at anything." >&2
  echo "  A module or path move breaks BOTH anchors at once:" >&2
  echo "    * the go list target   : ${toolkit_pattern}" >&2
  echo "    * the module prefix    : ${module_re}" >&2
  echo "  Update 'module' and 'toolkit_dir' at the top of $0 together, and rename" >&2
  echo "  the 'extraction constraint (internal/database)' job in" >&2
  echo "  .github/workflows/ci.yml to match the new path." >&2
  exit 1
fi

unexpected="$(comm -13 <(printf '%s\n' "$allowed") <(printf '%s\n' "$actual"))"

if [ -n "$unexpected" ]; then
  echo "ERROR: extraction constraint violated." >&2
  echo "${toolkit_dir} must depend only on the two toolkit packages, but also pulls in:" >&2
  printf '  %s\n' $unexpected >&2
  echo "Move any test helper or other code that needs these into internal/dbtest (or elsewhere)." >&2
  echo >&2
  echo "If the toolkit itself MOVED rather than gaining a dependency, this message" >&2
  echo "is misleading and both anchors need updating together:" >&2
  echo "    * the go list target   : ${toolkit_pattern}" >&2
  echo "    * the module prefix    : ${module_re}" >&2
  echo "  plus the 'extraction constraint (internal/database)' job name in" >&2
  echo "  .github/workflows/ci.yml, which carries the old path." >&2
  exit 1
fi

echo "OK: ${toolkit_dir} imports only the standard library, database drivers, and the two toolkit packages."
