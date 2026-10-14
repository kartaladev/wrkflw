# Tasks

Every guard fix is test-first. Plant the mutation, watch the guard pass it (red: the guard fails open), fix, and watch the same mutation go red while an at-limit neighbor stays green. Go tasks start with the `cc-skills-golang:golang-how-to` skill and run with `GOTOOLCHAIN=go1.26.9`. No task needs Docker. This change sets `skip_specs`, so tasks name the absorbed issue and the design decision instead of scenarios.

## 1. `check-lint-modules.sh` (#232)

- [ ] 1.1 Red: add self-test fixtures to `check-lint-modules.sh`, one per refusal in Q2: `continue-on-error: true` on a step, the same on the job, `if: false` on a step, `args: ./nothing/...`, `version` skew between steps, a missing `version`. Verify that each fixture currently passes (fail-open; the combined probe already did).
- [ ] 1.2 Fix (pending Q2): refuse each 1.1 fixture with a message naming the key and the module. Verify that every 1.1 fixture is red, and that the at-limit accepts stay green: `continue-on-error: false`, `args: --timeout=5m`, and identical versions.
- [ ] 1.3 Re-classify the header's limits (`strategy.matrix`, anchors, flow-style) by planted fixture, and rewrite the limits block with the direction measured (D1). Verify that the header names no limit that a fixture shows failing open.

## 2. CI wiring (#233)

- [ ] 2.1 Red (pending Q1): write the wiring check's fixture first. It deletes the `module-drift` job from a copy of `ci.yml`, and separately adds `if: false` to the `doc-refs` step. Verify that it runs against today's tree with no check in place (nothing fails), which proves the gap.
- [ ] 2.2 Fix (pending Q1): implement the wiring check over the derived domain `scripts/check-*.sh` as a root-module Go test. Verify that both 2.1 mutations are red with a message naming the script. Verify that renaming the `module-drift` job while keeping its `run:` line stays green, and that a new `scripts/check-x.sh` with no CI step is red without editing the test.
- [ ] 2.3 Update `CONTRIBUTING.md:79-86`: the count of repo-specific checks is derived or removed (it says five, CI runs six), and the wiring check is described. Verify that the checks listed equal `ls scripts/check-*.sh`.

## 3. Integration

- [ ] 3.1 Run every CI guard and the wiring check. Verify that `bash scripts/check-lint-modules.sh`, `check-module-drift.sh`, `check-doc-refs.sh`, `check-openspec.sh`, `check-test-timeout.sh` and `check-extraction.sh` (plus any `scripts/check-*.sh` a sibling change has landed), and `GOTOOLCHAIN=go1.26.9 go test .` all pass.

## Workflow follow-up

- The PR's last commit runs `openspec archive harden-ci-guard-wiring`.
- After merge, comment on and close kartaladev/wrkflw#233 and #232.
