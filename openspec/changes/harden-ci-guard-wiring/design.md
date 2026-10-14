# Design

## Context

See proposal.md, *Why* and *What Changes*, for the measured premises. The probes were run in a scratch `git archive HEAD` copy with bash 3.2.57, golangci-lint v2.13.2 and go1.26.9.

| Probe | Result |
|---|---|
| `check-lint-modules.sh` on unmodified `ci.yml` | `OK`, exit 0 |
| …with `continue-on-error: true`, `args: ./nothing/...`, `version: v2.0.0` planted on the `examples` step | `OK`, exit 0 (fails open on all three) |
| `grep -l 'ci\.yml' scripts/check-*.sh`, read per hit | 3 hits (extraction, test-timeout, lint-modules). None reads its own job: extraction only names `ci.yml` in an error message, test-timeout reads `-timeout`, lint-modules reads lint steps |

## Goals / Non-Goals

**Goals:**
- Every guard touched here is shown red under a planted mutation of the property it guards, and green on an at-limit neighbor that it must still accept.
- Every limit `check-lint-modules.sh` documents is true, and none of them fails open.
- Deleting any guard's CI invocation fails CI.

**Non-Goals:**
- New guards beyond the wiring check (Q1).
- Reviewing guards that no absorbed issue names (`check-extraction.sh`, `check-test-timeout.sh`, `check-openspec.sh`, `check-doc-refs.sh`, `check-module-drift.sh`). They come in only as members of Q1's wiring domain.

## Decisions

### D1. Fail-open limits are fixed; fail-closed limits stay documented, accurately

This applies to `check-lint-modules.sh`'s header and to the new wiring check's header. A documented limit is checked by a mutation: it must fail closed (loud) or it gets fixed. `check-lint-modules.sh`'s header currently calls `strategy.matrix`, `if:`, anchors and flow-style mappings "mis-reads". Each one is re-classified by a planted fixture. `if:` is fail-open (see proposal) and is fixed under Q2. The rest are recorded with the direction measured. The sibling changes apply the same rule to the headers they own.

## Risks / Trade-offs

- [Q1's wiring check is one more guard that could itself be unwired] → Its invocation is chosen so that removing it means removing something whose absence is loud (Q1). The regress stops there, and the header says so.

## Migration Plan

Contributor tooling only, so there is nothing to deploy. Rollback is a revert.

## Coupling

| Change | Shared surface | Owner / order |
|---|---|---|
| `split-persistence-modules` | `ci.yml` lint steps, `check-lint-modules.sh`, `check-module-drift.sh` | That change owns new modules. Either order; the second lander rebases, and Q2's version-skew rule then applies to its new lint steps. |
| `check-instruction-file-refs` | its new `scripts/check-*.sh` and its `ci.yml` invocation; the `CONTRIBUTING.md` guard list | That change owns the guard and its invocation; this change's wiring check covers it. Either order. |
| `harden-lint-script` | `CONTRIBUTING.md` (`:100`, a different paragraph) | Either order. `scripts/lint.sh` is local-only and outside Q1's domain. |
| `fix-doc-rot` | `CONTRIBUTING.md` guard list, `check-doc-refs.sh` (Q1 domain), a possible doc-link guard | That change owns `check-doc-refs.sh`'s scope and header. This change owns the wiring of every guard, including theirs. |
| `decide-module-denylist-policy` | a possible new guard script | Covered by Q1 if one is added. |
| `guard-scenario-outline-headers` | `check-openspec.sh` (Q1 domain), `CONTRIBUTING.md` | Either order. |

## Open Questions

These are decisions the maintainer has not made, so Readiness is `human`. Tasks follow each recommendation and are marked *pending Qn*. A different answer changes only that task group.

**Q1 (#233). What asserts guard wiring, and where the regress stops.**
- (a) **Recommended:** one wiring check whose domain is derived, not listed: every `scripts/check-*.sh` must appear in a `run:` line of a `ci.yml` job, and that step and job may carry no `if:` and no truthy `continue-on-error`. It runs as a Go test in the root module (`go test ./...` already runs in `build-test`). Removing it then means deleting a test file, which shows in review. Deleting `build-test` drops the whole suite, which is loud. A planted fixture deletes the `module-drift` job and must go red. Renaming that job while keeping the `run:` line must stay green (the issue's at-limit accept: a name is not a property).
- (b) The issue's option 2, folding each guard into a job that cannot plausibly be removed. It deletes the dependency for one guard but does not generalize to six, and it merges failure names, which every job comment argues against.
- (c) Branch protection's required checks. This is out-of-repo configuration that no review sees. It is complementary, not a substitute.
- Each script asserting its own job is rejected outright: a removed job never runs the assertion.

**Q2 (#232). What `check-lint-modules.sh` refuses on a lint step.**
- **Recommended:** refuse a truthy `continue-on-error` on the step or the job. Refuse any `if:` on the step or the job. Refuse `args:` containing a positional (non-flag) token or `--new`/`--new-from-*`/`--whole-files=false`, and accept flag-only `args` such as `--timeout=5m` as the at-limit row. Refuse steps whose `version:` values differ, and a step with no `version:`.
- Alternative: refuse any `args:` at all. This is simpler, but it forbids harmless tuning.
