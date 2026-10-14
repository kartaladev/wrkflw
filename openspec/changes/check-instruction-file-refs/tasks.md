# Tasks

The guard is test-first. Plant a dangling reference, watch the self-test report it, and keep an at-limit neighbor green. No task needs Go code or Docker. This change sets `skip_specs`, so tasks name the absorbed issue and the design decision instead of scenarios.

## 1. Testcontainers skill file (#172)

- [ ] 1.1 Correct `.claude/skills/use-testcontainers/SKILL.md`. Use `internal/dbtest.RunTestDatabase` returning `*pgxpool.Pool`, name the real options (`WithDBName`, `WithUser`, `WithPassword`), the per-dialect helpers `RunTestMySQL` and `RunTestSQLite`, and `forEachDialect` in `internal/persistence/store/conformance_test.go` as the three-dialect harness. Remove or replace the `database/testutils.go` and `database/transaction/transaction_test.go` citations and the `With{Migrations,InitScripts,FinalizeScripts}` claims. Verify that every identifier and path the file names resolves (`go doc ./internal/dbtest RunTestDatabase` and friends; `ls` for paths).

## 2. Instruction-file reference check (#173)

- [ ] 2.1 Red (pending Q1): write the reference check's self-test first. Plant a dangling path, a dangling `../` relative path, and a dangling `github.com/kartaladev/wrkflw/...` import path in fixtures. Each must be reported. Plant a resolving path, an illustrative fenced block and a placeholder path. Each must not be reported (negative controls). Verify against the pre-1.1 skill file that it reports `database/testutils.go`.
- [ ] 2.2 Fix (pending Q1): implement the check over the tracked instruction files, with the matched domain stated in its own header. Verify that it is at zero on the tree after 1.1, and that reverting 1.1 turns it red.
- [ ] 2.3 Invoke the check once from an existing `ci.yml` job, and add it to the `CONTRIBUTING.md` guard list. Verify that CI runs it, and, once `harden-ci-guard-wiring` has landed, that its wiring check covers it.

## Workflow follow-up

- The PR's last commit runs `openspec archive check-instruction-file-refs`.
- After merge, comment on and close kartaladev/wrkflw#172 and #173.
