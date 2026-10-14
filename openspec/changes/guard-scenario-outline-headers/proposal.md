# Proposal

## Why

`openspec validate --strict` accepts a scenario header that is not the `#### Scenario: <name>` form the project rule requires, and some of those headers silently drop or rehome scenarios. A malformed header reached a draft once during the baseline (finding 6 of D7 in the archived `establish-baseline-specs` design); nothing in CI catches the next one.

Re-measured on CLI 1.14.1 by renaming the second scenario header of `openspec/specs/retry-policy/spec.md` in a scratch copy (20 scenarios before; `validate --all --strict` exits 0 in every row):

| Header written | Parsed scenarios | What OpenSpec does |
|---|---|---|
| `#### Scenario Outline: X` | 20 | keeps it, but names it `Scenario Outline: X` |
| `#### scenario: X`, `#### Scenarios: X` | 20 | keeps it |
| `##### Scenario: X`, `####Scenario: X` | 19 | folds its body into the previous scenario |
| `### Scenario: X` | 19 | opens a phantom requirement named `Scenario: X` |

The finding's premise, that a `#### Scenario Outline:` header is folded into the previous scenario, did not reproduce: it parses as a scenario with a misleading name. Scenarios are folded by the wrong-depth and missing-space headers. All of these break the header rule, so the guard covers the whole family, not only the Outline spelling.

## What Changes

- `scripts/check-openspec.sh` gains a scenario-format check over every `spec.md` under `openspec/specs/` and under `openspec/changes/` (archived changes included). Outside fenced blocks, it fails on:
  - any Markdown header whose text starts with `scenario` (ignoring case) and is not exactly `#### Scenario: <name>`;
  - any `#### Scenario:` whose body holds no fenced block, more than one, or one that is not ```` ```gherkin ````;
  - any file that ends inside an open fence.
- Each new failure mode gets a planted mutation in the script's self-test. Every mutation must fail the check, and the unmutated tree must still pass. The unmutated tree already holds 700 `#### Scenario:` headers (main specs and archived deltas), each with exactly one gherkin fence.
- The script's header comment and the `scripts/check-openspec.sh` line in `CONTRIBUTING.md` describe the new check.
- No library behavior changes, so no capability spec changes (`skip_specs: true`).
- Not BREAKING. No impact on persisted formats, default security posture, or a consumer's go.mod.
- No GitHub issue is absorbed. The source is finding 6 of D7 in the archived `establish-baseline-specs` design.

Decisions this change is specified with:
- **Scope:** archived changes are checked too. They pass today, and checking them adds no exception path.
- **Header rule:** case-insensitive near-miss match on any header depth. A lowercase or plural spelling fails even when OpenSpec happens to parse it.
- **Fence rule:** exactly one fence per scenario, and it must be ```` ```gherkin ````. A second fence of any language fails.

Out of scope:
- Near-miss `### Requirement:` headers, `~~~` fences, and checking the Gherkin keywords inside a fence. Any of these needs its own change.
- The other D7 findings, which are owned by their own follow-up changes.

**Coordination**
- Size: S.
- Depends on: `establish-baseline-specs`.
- Blocks: nothing.
- Readiness: agent.

## Capabilities

### New Capabilities

None. This is CI tooling (`skip_specs: true`).

### Modified Capabilities

None.

## Impact

- `scripts/check-openspec.sh`: a new check function, an extended self-test, and an updated header comment.
- `CONTRIBUTING.md`: the one-line description of the script.
- The CI job `Check OpenSpec config and changes` in `.github/workflows/ci.yml` already runs the script. Its workflow is unchanged.
