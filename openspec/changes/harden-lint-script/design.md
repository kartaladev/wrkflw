# Design

## Context

See proposal.md, *What Changes*, for the measured premises. The probes were run in a scratch `git archive HEAD` copy with bash 3.2.57, golangci-lint v2.13.2 and go1.26.9.

| Probe | Result |
|---|---|
| `scripts/lint.sh ./definition/flow/...` with `_WRKFLW_LINT_SELF_TEST_DEPTH=1` exported | `0 issues.`, exit 0, 0 lines containing "self-test" (2 without the variable) |
| `SIGTERM` to `scripts/lint.sh --self-test` after 1 s | exit 143, `tmp.*` count 10 → 11 (one run, no positive control) |

## Goals / Non-Goals

**Goals:**
- Every self-test property touched here is shown red under a planted mutation, and green on an at-limit neighbor that it must still accept.
- Every limit the KNOWN LIMITS block documents is true, and none of them fails open.

**Non-Goals:**
- Making `scripts/lint.sh` a CI check. CONTRIBUTING.md states it is local-only, and CI stays on `golangci-lint-action`.
- Any other script in `scripts/` (owned by `harden-ci-guard-wiring` and `check-instruction-file-refs`).

## Decisions

### D1. #188: delete the environment variable, select the child by a nonce-file flag

The issue thread records this design ("Endorsed fix design"), and it is used as written. `assert_main_wiring` creates a nonce file inside the self-test's own `mktemp -d` directory and passes `--_self-test-child=<path>` to each child. The skip predicate returns *skip* only when the flag is present **and** the named file exists. Any other input returns *run*.

The skip decision is extracted into a predicate and asserted directly. An end-to-end child that runs its own self-test would recurse without bound. The four predicate cases are: no args → run; ambient `_WRKFLW_LINT_SELF_TEST_DEPTH=1` with no flag → run (the regression); a flag naming a missing file → run; a flag naming the live file → skip. Case 4 is the at-limit accept. Without it, a predicate that always returns *run* passes cases 1–3.

The flag also has to be removed from the argument list before golangci-lint sees it. Its spelling is chosen so that pflag would refuse it if it ever leaked through.

- Rejected: *require the variable to equal a magic token*. It keeps the dependency on ambient state and adds a mechanism.
- Rejected: *a marker file at a fixed path*. A stale marker would skip the self-test, which is the wrong fail direction.

### D2. #177 SF3 and SF5: verify ownership, trap the signals

- SF3: after `mkdir -p`, `prepare_cache_root` refuses (it calls `fail`, it does not warn) when the parent is a symlink, is not a directory, is not owned by `id -u`, or cannot be set to `700`. The `|| true` goes. The guard has to refuse, because a warning would still write four cache files into the attacker's target, which is the outcome the issue demonstrated.
- SF5: `self_test` traps `INT`, `TERM` and `HUP` in addition to `EXIT`. Each signal handler cleans up, then re-raises the signal so the exit status still reports it.
- Both get the positive control the original review lacked. The test has to show the leak and the symlink follow on unmodified code before the fix, or it proves nothing.

### D3. Fail-open limits are fixed; fail-closed limits stay documented, accurately

This applies to the KNOWN LIMITS block of `scripts/lint.sh`. A documented limit is checked by a mutation: it must fail closed (loud) or it gets fixed. The sibling changes apply the same rule to the headers they own.

## Risks / Trade-offs

- [A `nonce` flag adds argv surface to `lint.sh`] → It is refused unless it names a file inside a live self-test directory, and the D1 predicate test pins all four cases.
## Migration Plan

Contributor tooling only, so there is nothing to deploy. Rollback is a revert. The one contributor-visible effect is that `scripts/lint.sh` refuses a cache root it does not own (D2). Someone hit by it deletes the directory and re-runs; the refusal message says exactly that.

## Coupling

| Change | Shared surface | Owner / order |
|---|---|---|
| `harden-ci-guard-wiring` | `CONTRIBUTING.md` (its guard list at `:79-86`; this change edits `:100`) | Different paragraphs. Either order. |
| `fix-doc-rot`, `guard-scenario-outline-headers` | `CONTRIBUTING.md` | Either order; the second lander rebases. |

## Open Questions

These are decisions the maintainer has not made, so Readiness is `human`. Tasks follow each recommendation and are marked *pending Qn*. A different answer changes only that task group.

**Q1 (#177 SF1). The output-sink pin.**
- **Recommended:** refuse every caller `--output.*` flag and a bare `--`, the same treatment as `--path-prefix` (`:765-773`). The capture needs text on stdout, so no legitimate caller of this wrapper redirects output. The fix deletes the pin rather than guarding it.
- Alternative: insert the pin before `--` and refuse a trailing value-taking flag. This keeps more flexibility but adds parsing.

**Q2 (#178). Corpus versus golangci-lint version.**
- **Recommended:** record the version the corpus was captured with beside it. The self-test warns, not fails, when `golangci-lint --version` differs. The detector stays fail-closed either way, and a hard failure would block contributors on other patch versions.
- Alternative: regenerate the corpus in CI against the pinned version and fail on drift. This is stronger, but it needs a CI golangci-lint invocation outside the action.
