# Proposal

## Why

wrkflw is moving its work and promises from 148 GitHub issues into OpenSpec, but the agent instructions still name GitHub issues as the tracker. This change makes the instructions, config and CI agree first, because the agents doing the baseline and the migration read them.

## What Changes

- **State the OpenSpec workflow in `CLAUDE.md`.** A *Work tracking* section covers:
  - OpenSpec as the single source of truth;
  - GitHub issues as input only: each goes into an OpenSpec change that cites it, then is commented with the change name and closed; no labels or triage states on GitHub;
  - how the next change to work on is selected (the frontier);
  - when a change is in progress;
  - archive as the last commit of the change's own PR;
  - the Dependabot exemption, and out-of-scope review findings becoming new changes.
- **State where decisions live in `CLAUDE.md`.** A *Domain and decisions* section reverses the old "no document tree" rule:
  - a decision's rationale lives in its change's archived `design.md`;
  - the behavior it fixes lives in `openspec/specs/`;
  - the constraint is still stated in a comment on the code it governs.
- **Declare change metadata in the proposal.** OpenSpec has no native dependency edge between changes and no size field. `openspec/config.yaml` gains a proposal rule: every proposal ends *What Changes* with a Coordination block that declares Size (S/M/L/XL), Depends on, Blocks, and Readiness (agent/human). The frontier reads it.
- **Forbid Go comments that cite a change path.** A change's directory moves when the change is archived, so a `*.go` comment citing `openspec/changes/<name>` breaks at archive time. That is the same dangling-reference failure `scripts/check-doc-refs.sh` exists to stop for `docs/adr`. The guard gains a row that forbids such citations in Go source; capability specs under `openspec/specs/` stay citable.
- **Remove the superseded agent docs and the plugin that read them.** `docs/agents/` keeps only `eventually-waits.md` and `test-deadlines.md`, and `.claude/settings.json` stops enabling the one plugin whose setup skill generated the rest. Test-first stays the rule in `CLAUDE.md` and `openspec/config.yaml`, without routing through any plugin skill. The uncommitted multi-agent brief at the repo root is removed as well.
- **Check the OpenSpec config in CI.** OpenSpec ignores a broken `openspec/config.yaml` with only a warning (or none), and `openspec validate` never reads it. `scripts/check-openspec.sh` fails on any warning from `openspec instructions` and on unknown top-level keys, then runs `openspec validate --all --strict` and `--archived`; a new `openspec` CI job runs it with the CLI pinned to 1.14.1.
- **Commit the OpenSpec tooling.** The tooling already generated in the working tree (`openspec/`, `.claude/skills/openspec-*`, `.claude/commands/opsx/`) is committed with this change, so every agent sees the same workflow.

**Out of scope**
- Writing any capability spec. That is the next change, `establish-baseline-specs`, which records promises only, drawn from the 63 closed issues and the #20 register.
- Migrating, commenting on, or closing any GitHub issue, including the decided dispositions of #195, #175, #201, #179 and group-21. Those happen when the migration changes are created and archived.
- Any change to GitHub labels. The `wayfinder:*` labels stay; each issue carrying one is closed once a change records it, and no label is deleted.
- A contributor's own Claude Code configuration (`~/.claude`).
- `.opencode/`. It is untracked tooling for another agent runtime and is left as it is.

**Coordination**
- Size: M.
- Depends on: nothing.
- Blocks: `establish-baseline-specs`.
- Readiness: agent.

## Capabilities

### New Capabilities

None. This change alters contributor and agent workflow documentation, one CI guard over Go comments, and OpenSpec configuration. No externally observable library behavior changes, so the change sets `skip_specs: true`.

### Modified Capabilities

None.

## Impact

- **`CLAUDE.md`:** new *Work tracking* and *Domain and decisions* sections, and the TDD line, which no longer names a skill.
- **`CONTRIBUTING.md`:** the *Design decisions* expectation points at OpenSpec; the prerequisites name the OpenSpec CLI and the workflow profile to select before running `openspec update`.
- **`docs/agents/`:** reduced to `eventually-waits.md` and `test-deadlines.md`.
- **`scripts/check-doc-refs.sh`:** one new rule row and its self-test fixture.
- **CI:** new `scripts/check-openspec.sh` and an `openspec` job in `.github/workflows/ci.yml` (Node plus `@fission-ai/openspec@1.14.1`).
- **`openspec/config.yaml`:** new file: project context, per-artifact rules (including the Coordination proposal rule and the Gherkin scenario rules), and apply guidance that states test-first as a practice.
- **`.claude/settings.json`:** the retired plugin's entry removed, and `extraKnownMarketplaces` added for `gitlab-skills` (git, gitlab.com/gitlab-org/ai/skills) and `samber` (GitHub samber/cc), so a fresh clone can resolve the two enabled plugins that come from them.
- **Committed tooling:** `openspec/`, `.claude/skills/openspec-*` and `.claude/commands/opsx/`.
- **Not affected:** library code, persisted formats, the default security posture, and any consumer's `go.mod`. Nothing breaks for consumers.
