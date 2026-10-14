# Proposal

## Why

The repo relies on bash guards in `scripts/` that CI runs to keep its promises true. Nothing notices when a guard's CI job is deleted or switched off, and `check-lint-modules.sh` reports success on lint steps that cannot fail. Each is a green run that proves nothing, which is the defect class these guards exist to prevent.

## What Changes

Every premise below was re-measured on `main` at `4763d36e`. Probes ran in a scratch `git archive` copy, never in the working tree.

- **kartaladev/wrkflw#233 (absorbed, widened).** Holds. No script references the `module-drift` job (`.github/workflows/ci.yml:283`, run at `:302`). The four claims that go false with it have drifted to `.github/dependabot.yml:17`, `.gitignore:17`, `CONTRIBUTING.md:73` and `:85`. The issue comment says `check-test-timeout.sh` and `check-lint-modules.sh` already "assert their own wiring". **That premise is stale.** They read `ci.yml` for other properties: the `-timeout` flag, and the golangci-lint steps of the `lint` job. **None** of the six CI-run guards (`check-extraction`, `check-test-timeout`, `check-doc-refs`, `check-openspec`, `check-lint-modules`, `check-module-drift`) asserts that it is wired. A script also cannot detect that its own job was removed, because then it never runs. So wiring is asserted once, for every guard, by a check that something else runs (design, Q1).
- **kartaladev/wrkflw#232 (absorbed).** Holds. `grep -ci` finds `continue-on-error` 0 times and `args` 0 times in `scripts/check-lint-modules.sh`. In a planted copy of `ci.yml`, the `examples` lint step was given `continue-on-error: true`, `args: ./nothing/...` and `version: v2.0.0`, and the guard still printed `OK` with exit 0. The `version:` skew the issue left unverified is therefore **confirmed**. The header's limits block (`:33-40`) also lists `if:` as a mis-read, but an `if: false` step is skipped, so it fails open too. Fail-open limits get fixed. The fail-closed ones stay documented, and documented accurately.
- No library behavior changes. Scripts, a root-module test file, CI wiring and `CONTRIBUTING.md` only, so this sets `skip_specs`.
- **BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected (contributor tooling only). **Consumer go.mod:** not affected.

**Decisions this change cannot be finished without.** The options and recommendations are in design.md, Open Questions. Tasks follow the recommendations and are marked *pending Qn*.
- **Q1 (#233).** What asserts that every guard is wired, and where the regress stops.
- **Q2 (#232).** Which lint-step keys `check-lint-modules.sh` refuses, and which it accepts.

**Split.** This change originally absorbed nine issues touching five separate guards. It was cut along file lines into four independent changes with no ordering between them. This one keeps CI wiring and `check-lint-modules.sh`. The other three are `harden-lint-script` (#188, #177, #178), `check-instruction-file-refs` (#172, #173) and `harden-structural-go-tests` (#189, #180).

**Out of scope**
- `scripts/lint.sh` (#188, #177, #178) is owned by `harden-lint-script`.
- The testcontainers skill file and the new instruction-file reference guard (#172, #173) are owned by `check-instruction-file-refs`. This change's wiring check covers that guard once it is a `scripts/check-*.sh` run by CI.
- The kind walks and the internal-leak guard (#189, #180) are owned by `harden-structural-go-tests`.
- Fixing dangling references in Go comments, the `check-doc-refs.sh` header and scope (#91), the description-truth rule (#100), and doc-link resolution (#123, #130) are owned by `fix-doc-rot`. This change owns the wiring of any guard `fix-doc-rot` adds.
- Extending `check-lint-modules.sh` and `check-module-drift.sh` to new modules is owned by `split-persistence-modules` (its task 3.4).
- A module-denylist guard (#124, #125) is owned by `decide-module-denylist-policy`. If that change picks a script, Q1's wiring check covers it.
- The scenario-header check in `check-openspec.sh` is owned by `guard-scenario-outline-headers`.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Only CI guards and contributor docs change, so `skip_specs: true` is set.

## Impact

- **Scripts:** `scripts/check-lint-modules.sh`, and a new CI-wiring check (Q1).
- **CI:** `.github/workflows/ci.yml` (the wiring check's domain).
- **Docs:** `CONTRIBUTING.md` (the guard list at `:79-86`, which still says "five" checks while CI runs six).
- **Shared files:** `ci.yml` and `scripts/check-lint-modules.sh` with `split-persistence-modules`. `ci.yml` and the `CONTRIBUTING.md` guard list with `check-instruction-file-refs`. `CONTRIBUTING.md` with `harden-lint-script`, `fix-doc-rot` and `guard-scenario-outline-headers`. There is no ordering dependency; whichever lands second rebases.
- When archived, comment on and close kartaladev/wrkflw#233 and #232.
