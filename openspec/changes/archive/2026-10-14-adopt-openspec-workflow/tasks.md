# Tasks

## 1. Guard: forbid change-path citations in Go source (D4)

- [x] 1.1 Red, taken at the tree level: the self-test is table-driven and plants every row automatically, so a fixture-level red cannot fail once the row exists. Instead, a scratch `internal/zz_probe/probe.go` citing `openspec/changes/foo/design.md` made `scripts/check-doc-refs.sh` exit 0 (the defect). Pre-check: `git grep 'openspec/changes' -- '*.go'` finds no existing citations.
- [x] 1.2 Add the `openspec-change` rule row (regex `openspec/changes/`) and an explicit always-active retirement value that `rule_active` honours. Verify with three commands:
  - `scripts/check-doc-refs.sh --self-test` passes (green);
  - `scripts/check-doc-refs.sh` passes on the tree;
  - a temporary scratch `.go` file citing a change path makes the plain scan exit non-zero, and the file is removed afterwards.
- [x] 1.3 Add a negative control to the self-test: `openspec/specs/x/spec.md` in a fixture is NOT reported. Run `--self-test` and confirm it passes. Update the script header's SCOPE and rules commentary to describe the always-active mode and why change paths dangle by construction. Discrimination proved by mutation: widening the regex to `openspec/` fires the clean-fixture false-positive assertion.
- [x] 1.4 Audit fixes. Pin `openspec-change` in a `NEVER_RETIRE` list, and add two self-test probes:
  - `rule_active -` stays active in a scratch repo whose index holds a file literally named `-`;
  - every pinned row exists, is guarded by `-`, and reads as active.

  Correct the header: an existence check fails the archiving PR, not the citing one; archive paths are refused under strip-the-provenance, not because they dangle. Red by mutation: flipping the sentinel, swapping the guard for a path, deleting the sentinel line, or deleting the row each fails a named self-test assertion. The unmutated script passes `--self-test` and the tree scan.

## 2. Work tracking (D1, D2, D3)

- [x] 2.1 Add the Coordination proposal rule to `openspec/config.yaml` (Size, Depends on, Blocks, Readiness). Verify with `openspec instructions proposal --change adopt-openspec-workflow --json | jq '.rules|length'`: it rose from 5 to 6, and no warning is printed.
- [x] 2.2 Write `CLAUDE.md` *Work tracking*. It covers:
  - OpenSpec as the source of truth;
  - GitHub issues as input to OpenSpec changes, with no labels;
  - the frontier and the meaning of "in progress";
  - archive as the PR's last commit;
  - the interim rule for open `wayfinder:*` issues.

  Verify `grep -n "GitHub issues" CLAUDE.md` finds only the intake description.

## 3. Domain and decisions (D4, D5)

- [x] 3.1 Write `CLAUDE.md` *Domain and decisions*: where decisions live (archived `design.md` and `openspec/specs/`), the code-comment rule with its `ErrScopeLocalWithCompensateRef` example, the ADR history, and the Go-citation rule. Verify the section contains those four rules.

## 4. Remove superseded docs and settings (D6)

- [x] 4.1 Reduce `docs/agents/` to `eventually-waits.md` and `test-deadlines.md` (`git rm -f`). Repoint the self-test's clean fixture at `docs/agents/eventually-waits.md`. Verify with `ls docs/agents`, and confirm `scripts/check-doc-refs.sh` passes.
- [x] 4.2 Remove the uncommitted multi-agent brief at the repo root, after confirming that a byte-identical copy (`cmp`) was kept outside the repo. Verify that `git status` shows no entry for it.
- [x] 4.3 State test-first as a practice in `CLAUDE.md` and `openspec/config.yaml`, naming no TDD skill, and drop plugin entries the repo no longer uses from `.claude/settings.json`. Verify three things:
  - `jq empty .claude/settings.json` succeeds;
  - `openspec instructions apply --change adopt-openspec-workflow --json | jq '.operationGuidance'` says test-first and names no TDD skill (`golang-how-to` is the Go router and stays);
  - a repo-wide grep for the removed doc and plugin names finds nothing outside `.git` and `.opencode`.

## 5. Commit the OpenSpec tooling and integrate

- [x] 5.1 Stage `openspec/`, `.claude/skills/openspec-*`, `.claude/commands/opsx/`, `.claude/settings.json`, `CLAUDE.md`, `docs/agents/`, `scripts/check-doc-refs.sh` and the deletions. Leave `.opencode/` unstaged. Verify with `git status --short` that only `.opencode/` stays untracked.
- [x] 5.2 Run `openspec validate adopt-openspec-workflow --strict` and the five repo checks that need no Docker (`check-extraction`, `check-test-timeout`, `check-doc-refs`, `check-module-drift`, `check-lint-modules`); all must exit 0.

- [x] 5.3 Docs and config audit fixes:
  - intake issues are commented and closed once, at conversion (the tasks rule and archive guidance are removed);
  - the frontier is computable (`--archived` with the date prefix stripped, `no-tasks` changes count, open-PR changes excluded);
  - `CONTRIBUTING.md` points at OpenSpec;
  - the parity claim allows a divergence named by a code comment;
  - Dependabot PRs are exempt;
  - the out-of-scope findings rule is in `CLAUDE.md`;
  - wording fixes in the proposal, design, Gherkin rule and script header.

  Verify that `openspec validate adopt-openspec-workflow --strict` passes, that the rules count per artifact is 6/7/2/2, and that the five repo checks exit 0.

- [x] 5.4 Conformance audit fixes (OpenSpec docs):
  - Red: with `check_config` stubbed to pass, `scripts/check-openspec.sh --self-test` fails all five planted config mistakes (invalid YAML, unknown top-level key, misspelled artifact, misspelled operation, unquoted colon rule). Green: the real script passes `--self-test` and the repo check, and leaves the tree untouched.
  - Add the `openspec` CI job (D7).
  - The Gherkin rule states that it replaces the template's WHEN/THEN bullets; a new rule requires SHALL/MUST and under 500 characters in requirement text (D8).
  - The baseline rule excludes undecided or merely observed behavior (D9).
  - `CLAUDE.md`: `openspec archive <change> --yes`, and a `complete` change with no open PR is reported as an anomaly.
  - `CONTRIBUTING.md`: the OpenSpec CLI prerequisite and the workflow profile to select before `openspec update`.
  - The proposal's *Why* is cut to two sentences.

## Workflow follow-up

- Commit, push, and open the PR naming this change (after confirmation).
- Last commit of the PR: `openspec archive adopt-openspec-workflow --yes` (D3; no specs to sync, because `skip_specs` is set).
- Next change: `establish-baseline-specs` (promises only, from the 63 closed issues and the #20 register).
