#!/usr/bin/env bash
# Enforce the extraction constraint from ADR-0079: the database toolkit packages
# internal/database and internal/database/transaction must import ONLY the Go
# standard library and the database drivers — never any other wrkflw package —
# so they can be lifted out as a standalone module. Test helpers that need other
# wrkflw packages live in internal/dbtest, which is not part of this graph.
#
# Fails (exit 1) if `go list -deps ./internal/database/...` pulls in any
# github.com/kartaladev/wrkflw package other than the two toolkit packages.
#
# Usage: scripts/check-extraction.sh   (run from the repo root)
set -euo pipefail

module="github.com/kartaladev/wrkflw"

allowed="$(printf '%s\n' \
  "${module}/internal/database" \
  "${module}/internal/database/transaction" | sort)"

actual="$(go list -deps ./internal/database/... | grep "^${module}" | sort || true)"

unexpected="$(comm -13 <(printf '%s\n' "$allowed") <(printf '%s\n' "$actual"))"

if [ -n "$unexpected" ]; then
  echo "ERROR: extraction constraint violated (ADR-0079)." >&2
  echo "internal/database must depend only on the two toolkit packages, but also pulls in:" >&2
  printf '  %s\n' $unexpected >&2
  echo "Move any test helper or other code that needs these into internal/dbtest (or elsewhere)." >&2
  exit 1
fi

echo "OK: internal/database imports only the standard library, database drivers, and the two toolkit packages."
