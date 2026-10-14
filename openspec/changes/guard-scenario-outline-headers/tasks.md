# Tasks

No capability scenarios apply (`skip_specs: true`). Each task names the self-test mutations it covers; they are numbered as in design.md D5. Verification runs the script directly; it needs the openspec CLI 1.14.1 that CONTRIBUTING.md names.

## 1. Harness (no behavior change)

- [ ] 1.1 Give `expect_fail` a first argument naming the check function to run, and pass `check_config` at the five existing config mutations. Verify: `bash scripts/check-openspec.sh --self-test` prints `check-openspec: self-test ok`, and the five config mutations still run.

## 2. Header near-misses (red, then green)

- [ ] 2.1 Red. Add a `check_scenarios` stub that returns 0 for every tree. Add mutations 1-5 and 10, targeting `check_scenarios`. Add the unmutated-tree `check_scenarios` pass to the self-test. Verify: `bash scripts/check-openspec.sh --self-test` exits non-zero, and it names each of mutations 1-5 and 10 with `passed the check; it must fail`.
- [ ] 2.2 Green. Implement the D2 header rule and the D3 file scope in `check_scenarios`: fence-aware, POSIX awk, and each problem reported as `<file>:<line>`. Wire it into the main path after `check_config`. Verify: `bash scripts/check-openspec.sh --self-test` prints `check-openspec: self-test ok`, and `bash scripts/check-openspec.sh` prints `check-openspec: ok` on the unmutated tree.

## 3. One gherkin fence per scenario (red, then green)

- [ ] 3.1 Red. Add mutations 6-9 (no fence, a second gherkin fence, a `text` fence, an unclosed fence). Verify: `bash scripts/check-openspec.sh --self-test` exits non-zero, and it names each of mutations 6-9.
- [ ] 3.2 Green. Extend `check_scenarios` with the D2 fence rule: exactly one fence per scenario, opening with ```` ```gherkin ````, and no fence left open at end of file. Verify: `bash scripts/check-openspec.sh --self-test` prints `check-openspec: self-test ok`, and `bash scripts/check-openspec.sh` prints `check-openspec: ok`.
- [ ] 3.3 Refactor. Consider `/simplify` on the script. Keep the awk portable to both BSD and GNU, and fail loudly when a mutation's target file is missing (D5). Verify both commands from 3.2 again on macOS, and with `awk` resolving to mawk or gawk (the CI runner).

## 4. Docs

- [ ] 4.1 Update the script's WHY header comment to state the measured header table from proposal.md, and the scope and fence rule. Update the `scripts/check-openspec.sh` line in `CONTRIBUTING.md` to say it also checks scenario headers and fences. Verify: `bash scripts/check-doc-refs.sh` passes, and `bash scripts/check-openspec.sh` prints `check-openspec: ok`.

## Workflow follow-up

- Open the PR naming `guard-scenario-outline-headers`. Its last commit runs `openspec archive guard-scenario-outline-headers`.
- No GitHub intake issue to close.
