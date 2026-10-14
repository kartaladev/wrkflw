# Tasks

Every guard fix is test-first. Plant the mutation, watch the self-test pass it (red: the guard fails open), fix, and watch the same mutation go red while an at-limit neighbor stays green. No task needs Go code or Docker. This change sets `skip_specs`, so tasks name the absorbed issue and the design decision instead of scenarios.

## 1. Self-test cannot be disabled (#188)

- [ ] 1.1 Extract the self-test skip decision into a predicate, then add the four-case table from design D1 to `--self-test`. Verify that, before the fix, case 2 (ambient `_WRKFLW_LINT_SELF_TEST_DEPTH=1`, no flag) reports *skip* and fails the self-test.
- [ ] 1.2 Replace the variable with the nonce-file flag (D1) at all four setters and at the reader. Remove the flag from the args before golangci-lint sees it. Verify that all four cases pass, that `grep -c _WRKFLW_LINT_SELF_TEST_DEPTH scripts/lint.sh` is 0, and that mutating the predicate back to the environment test turns case 2 red.
- [ ] 1.3 Make `CONTRIBUTING.md:100` ("runs on every invocation anyway") true as written, or say exactly what is guaranteed. Verify by re-running the #188 probe: with the old variable exported, the run's output contains "self-test".

## 2. Output sink, cache root and temp directories (#177)

- [ ] 2.1 SF1 red (pending Q1): add self-test rows for a trailing `--output.json.path` with no value, and for `-- ` followed by a redirect. Verify that both currently bypass the pin while assertion 7 stays green. If neither reproduces, record SF1 as unreproduced in the KNOWN LIMITS block and skip 2.2.
- [ ] 2.2 SF1 fix (pending Q1): refuse caller `--output.*` flags and a bare `--` the same way `--path-prefix` is refused. Verify that both 2.1 rows now exit 1 naming the flag, and that `scripts/lint.sh ./...` and `scripts/lint.sh --timeout=5m ./...` still lint (at-limit accept).
- [ ] 2.3 SF3 red: in a scratch `TMPDIR`, pre-create the cache parent as a symlink to a foreign directory, and as a directory with mode `777`. Verify that cache files land in the symlink target today (positive control). Then fix per D2 and verify that both are refused with a message naming the path, and that a fresh `TMPDIR` still works.
- [ ] 2.4 SF5 red: send `SIGTERM` to `--self-test` mid-run in a scratch `TMPDIR`, with a positive control: the same run without the signal leaves 0 directories. Verify the leak on unmodified code, then trap `INT`/`TERM`/`HUP` per D2 and verify 0 leaked directories across 3 runs, and exit status 143.

## 3. Untested self-test paths (#178)

- [ ] 3.1 Add a test that makes `warn_fixture_died` fire (a fixture whose golangci-lint stub exits 3 with a cause on stderr). Verify that its NOTE names the cause, and that it names "no cause captured" when stderr is empty.
- [ ] 3.2 Add self-test rows pinning the fast path. `-v ./...`, `--help ./...` and `--version x` must run the self-test, and a lone `--help` and a lone `--version` must not. Verify by mutating the fast path to accept `-v`: the `-v` row goes red.
- [ ] 3.3 (pending Q2) Record beside the near-miss corpus the golangci-lint version it was captured with, and warn when `golangci-lint --version` differs. Verify with a stub reporting `v2.0.0`: one warning, and the lint still runs.

## 4. Limits and integration

- [ ] 4.1 Update the KNOWN LIMITS block so that every listed limit is still true after groups 1–3, each with the direction measured (D3). Verify with `bash scripts/lint.sh --self-test`, and with `scripts/lint.sh ./...` run in the root and `examples` modules.

## Workflow follow-up

- The PR's last commit runs `openspec archive harden-lint-script`.
- After merge, comment on and close kartaladev/wrkflw#188, #177 and #178.
