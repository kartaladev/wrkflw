# Proposal

## Why

Agents and contributors act on the instruction files (`CLAUDE.md`, `CONTRIBUTING.md`, `docs/agents/*`, `.claude/skills/**`, `.claude/commands/**`, `openspec/config.yaml`) as written, and nothing checks that what they cite exists. A skill file already tells agents to call a package, options and files that do not exist. Without a guard, the next rename produces the next dangling instruction silently.

## What Changes

Every premise below was re-measured on `main` at `4763d36e`.

- **kartaladev/wrkflw#172 (absorbed; one row stale).** Holds and is wider. `.claude/skills/use-testcontainers/SKILL.md` names `database.RunTestDatabase` and `*gorm.DB`. The real helper is `internal/dbtest.RunTestDatabase`, returning `*pgxpool.Pool` (`internal/dbtest/postgres.go:127`). It also names `database.WithMigrations`/`WithInitScripts`/`WithFinalizeScripts` (`dbtest` exports only `WithDBName`, `WithUser`, `WithPassword`), `database/testutils.go` and `database/transaction/transaction_test.go`, and none of these exist. **Stale:** the `internal/contract/repository.go` citation is no longer in the file.
- **kartaladev/wrkflw#173 (absorbed).** Holds in kind. **Stale premises:** it cites `PROMPT.md` rules, but `PROMPT.md` does not exist. Its instance 3 (`CONTRIBUTING.md` Go version) was fixed by #153. The domain was re-measured over the 30 tracked instruction files (`CLAUDE.md`, `CONTRIBUTING.md`, `docs/agents/*`, `.claude/skills/**`, `.claude/commands/**`, `openspec/config.yaml`). Dangling repo-relative paths are the #172 rows and nothing else. The other non-resolving strings are illustrative (`examples/examples/migrate/main.go` names a phantom path on purpose; there is a template `specs/billing/…`, and a mockgen `--destination`). So a naive path check is not at zero, and the guard needs an explicit exemption form (design, Q1). The `docs/agents/issue-tracker.md` and plugin skill references that the OpenSpec migration removed appear in **none** of the 30 files.
- A new reference check over the instruction files, with its own self-test, invoked once from CI.
- No library behavior changes. One new script, one skill file, the CI invocation and `CONTRIBUTING.md` only, so this sets `skip_specs`.
- **BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected (contributor tooling only). **Consumer go.mod:** not affected.

**Decisions this change cannot be finished without.** The options and recommendation are in design.md, Open Questions. Tasks follow the recommendation and are marked *pending Q1*.
- **Q1 (#173).** What the instruction-file reference guard resolves (paths only, or also import paths and `pkg.Ident`), and how an illustrative path is exempted.

This change was split out of `harden-ci-guard-wiring` along file lines; it owns the instruction files and the new reference check.

**Out of scope**
- Fixing dangling references in Go comments, the `check-doc-refs.sh` header and scope (#91), the description-truth rule (#100), and doc-link resolution (#123, #130) are owned by `fix-doc-rot`.
- Asserting that the new check stays wired into CI (#233) is owned by `harden-ci-guard-wiring`. This change adds the check and its single invocation.
- `scripts/lint.sh` (#188, #177, #178) is owned by `harden-lint-script`; the structural Go tests (#189, #180) by `harden-structural-go-tests`.
- Removing casbin, and with it the casbin paths in `docs/agents/`, is owned by `remove-casbin` / `replace-casbin-with-composed-deciders`. Q1 decides whether those historical citations are exempt.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Only a CI guard and contributor/agent docs change, so `skip_specs: true` is set.

## Impact

- **Scripts:** a new instruction-file reference check (Q1).
- **CI:** `.github/workflows/ci.yml` (one invocation of the new check).
- **Docs:** `.claude/skills/use-testcontainers/SKILL.md`, and the guard list in `CONTRIBUTING.md`.
- **Shared files:** `ci.yml` and the `CONTRIBUTING.md` guard list with `harden-ci-guard-wiring`; `CONTRIBUTING.md` also with `harden-lint-script`, `fix-doc-rot` and `guard-scenario-outline-headers`. There is no ordering dependency; whichever lands second rebases.
- When archived, comment on and close kartaladev/wrkflw#172 and #173.
