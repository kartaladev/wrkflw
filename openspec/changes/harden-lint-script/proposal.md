# Proposal

## Why

`scripts/lint.sh` wraps golangci-lint with a self-test that is meant to prove, on every run, that the wrapper still detects what it claims to. That self-test can be switched off by an inherited environment variable, its output-sink pin can be bypassed, its cache root and temp directories can be hijacked or leaked, and parts of it (the near-miss corpus, the died-fixture warning, the `--help`/`--version` fast path) have no test at all. Each is a green run that proves nothing, which is the defect class the wrapper exists to prevent.

## What Changes

Every premise below was re-measured on `main` at `4763d36e`. Probes ran in a scratch `git archive` copy, never in the working tree.

- **kartaladev/wrkflw#188 (absorbed; the issue records the fix design).** Holds. The setters are at `scripts/lint.sh:443`, `:521`, `:542` and `:553`, and the reader is at `:792`. With `_WRKFLW_LINT_SELF_TEST_DEPTH=1` exported, a run printed `0 issues.`, exited 0, and its output held 0 occurrences of "self-test" (2 without the variable). The claim has moved from `CONTRIBUTING.md:50` to `:100`. The design the thread endorses is used: a flag naming a per-run nonce file replaces the variable.
- **kartaladev/wrkflw#177 (absorbed).** SF1 holds by reading: the output-sink pin is still appended (`:825`). It is a hypothesis until the first task reproduces the swallow. SF3 holds by reading: `mkdir -p` then `chmod 700 … || true` (`:249-250`), with no owner or symlink check. SF5 reproduced once: a `SIGTERM` during `--self-test` under bash 3.2.57 left one more `tmp.*` directory, with no positive control. The script's own KNOWN LIMITS block (`:139-153`) already lists SF3 and SF5 as unasserted by the self-test.
- **kartaladev/wrkflw#178 (absorbed).** Holds. The near-miss corpus in `assert_detects` records no golangci-lint version (`grep 'v2\.[0-9]'` finds 0 hits in `scripts/lint.sh`). Nothing makes `warn_fixture_died` (`:632`) fire. The `--help`/`--version` fast path (`:775-788`) has no test that pins `-v` to a real run.
- No library behavior changes. `scripts/lint.sh` and one `CONTRIBUTING.md` paragraph only, so this sets `skip_specs`.
- **BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected (contributor tooling only). **Consumer go.mod:** not affected.

**Decisions this change cannot be finished without.** The options and recommendations are in design.md, Open Questions. Tasks follow the recommendations and are marked *pending Qn*.
- **Q1 (#177 SF1).** Refuse caller output-sink flags and `--`, or reposition the pin.
- **Q2 (#178).** How the near-miss corpus is tied to a golangci-lint version.

This change was split out of `harden-ci-guard-wiring` along file lines; it owns `scripts/lint.sh` and nothing else that the split names.

**Out of scope**
- CI guard wiring and `scripts/check-lint-modules.sh` (#233, #232) are owned by `harden-ci-guard-wiring`. `scripts/lint.sh` is not a CI check, so the wiring check does not cover it.
- The instruction-file reference guard and the testcontainers skill file (#172, #173) are owned by `check-instruction-file-refs`.
- The structural Go tests (#189, #180) are owned by `harden-structural-go-tests`.
- Making `scripts/lint.sh` a CI check. `CONTRIBUTING.md` states it is local-only, and CI stays on `golangci-lint-action`.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Only contributor tooling and docs change, so `skip_specs: true` is set.

## Impact

- **Scripts:** `scripts/lint.sh` (self-test predicate, nonce flag, output-sink refusal, cache-root ownership, signal traps, corpus version, KNOWN LIMITS block).
- **Docs:** `CONTRIBUTING.md:100` (the `lint.sh` paragraph).
- **Shared files:** `CONTRIBUTING.md` with `harden-ci-guard-wiring` (`:79-86`), `check-instruction-file-refs`, `fix-doc-rot` and `guard-scenario-outline-headers`. Different paragraphs; there is no ordering dependency, and whichever lands second rebases.
- When archived, comment on and close kartaladev/wrkflw#188, #177 and #178.
