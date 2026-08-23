#!/usr/bin/env bash
# Regenerate the "Data at rest" block in SECURITY.md from the migration schema
# and the stated column classification (ADR-0187).
#
# The classification itself is a JUDGEMENT and lives in
# internal/atrest/classification.go. Everything else in the block is derived
# by internal/atrest.Render from the migrations embedded in this module.
#
# TestSecurityMdInSync (internal/atrest/render_test.go) is the drift guard:
# it fails when SECURITY.md's generated block no longer matches what Render
# produces right now. Run this script whenever a migration or the
# classification changes, and commit the resulting SECURITY.md diff.
#
# Usage: scripts/gen-at-rest.sh   (run from anywhere; cd's to the repo root)
set -euo pipefail
cd "$(dirname "$0")/.."

TEST_NAME='TestSecurityMdInSync'

# ⚠ `go test -run` on a name that matches NOTHING exits 0 ("no tests to run"),
# which is CLAUDE.md Common Pitfall #5. Anchoring the regex prevents matching
# the WRONG test; it does nothing about matching NO test. So a rename of
# TestSecurityMdInSync would have made this script print "regenerated and
# verified" having neither regenerated nor verified anything — while
# SECURITY.md tells its readers that this exact test is what fails the build.
# Both invocations therefore assert on the PASS line, not on the exit code.
run_sync_test() {
	local label="$1"
	shift

	local out status
	set +e
	out=$(go test ./internal/atrest/ -run "^${TEST_NAME}\$" -count=1 -v "$@" 2>&1)
	status=$?
	set -e

	printf '%s\n' "$out"

	if [ "$status" -ne 0 ]; then
		echo "gen-at-rest: ${label} failed (go test exit ${status})." >&2
		exit 1
	fi
	if ! printf '%s\n' "$out" | grep -q "^--- PASS: ${TEST_NAME}"; then
		echo "gen-at-rest: ${label} did not RUN ${TEST_NAME} — 'go test -run' exits 0 when the" >&2
		echo "name matches nothing. Was the test renamed? Nothing was regenerated or verified." >&2
		exit 1
	fi
}

run_sync_test "the regeneration pass" -update
run_sync_test "the verification pass"

echo "SECURITY.md at-rest block regenerated and verified."
