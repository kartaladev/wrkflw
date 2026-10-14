# Design

## Context

Before this change, `CLAUDE.md` and the agent docs under `docs/agents/` told every agent that GitHub issues were the tracker, and that decisions live only in commits, PR bodies and code comments, never in a document tree. `openspec/config.yaml`, written first in the same branch, already declares OpenSpec the single source of truth, so they contradicted it.

`scripts/check-doc-refs.sh` forbids citations of deleted documents in `*.go`. It keys each rule to the tracked path whose existence retires it: a rule is active while that path is absent.

## Goals / Non-Goals

**Goals:**
- `CLAUDE.md` is the one place that says how work enters (GitHub intake), how the next change is picked, how a change finishes, and where decisions live. Nothing in the repo contradicts `openspec/config.yaml`.
- Archiving a change can never leave a dangling citation in Go source.

**Non-Goals:**
- Changing how PRs are reviewed or merged beyond adding the archive commit.
- Migrating any issue content (later changes).
- Removing GitHub labels.
- Multi-agent coordination rules.

## Decisions

### D1. GitHub issues are input to OpenSpec, with no state of their own

Outside reporters will keep filing GitHub issues whatever this repo says, so issues stay enabled. An issue has no workflow on GitHub: its content goes into an OpenSpec change (new, or one that already covers it) whose proposal cites it, and the issue is commented with the change name and closed. No triage labels are kept. Whether a change suits an agent or a human is the Coordination block's `Readiness`.

*Rejected:*
- **Triage labels on GitHub (`needs-triage`, `needs-info`, `wontfix`).** A second state machine outside OpenSpec; whether an issue is unclear or declined is recorded in the change that absorbs it.
- **Disable issues.** This loses the outside-reporter channel.
- **Keep issues open as mirrors of changes.** Two sources of truth drift; avoiding that drift is the whole reason for this decision.

### D2. Change metadata lives in the proposal, enforced by a config rule

OpenSpec 1.14.1 has no dependency edge between changes and no size field. Every proposal therefore ends *What Changes* with a **Coordination** block of four entries:
- `Size` (S/M/L/XL);
- `Depends on`;
- `Blocks`;
- `Readiness` (agent | human).

`openspec/config.yaml` requires it.

**The frontier** is every active change (status `in-progress` or `no-tasks`) whose `Depends on` names only archived changes and that no open PR names. Archived names come from `openspec list --archived --json` with the date prefix stripped, because archive renames the directory to `<date>-<name>`. `no-tasks` changes qualify because they are ready for planning; excluding them would hide every freshly proposed change. Among those, ties go to the change that unblocks the most others, counted from the dependents' `Depends on` (the authoritative edge; `Blocks` is a forecast), then to the smallest Size.

**In progress** means an open PR names the change. No separate claim record is kept, because an open PR is already visible to everyone.

*Rejected:*
- **Encode the metadata in the change name.** Names are permanent, while size and blocking are not.
- **Add a custom schema with a `meta` artifact.** That forks the default schema for four lines of data; revisit if OpenSpec gains native dependencies.

### D3. A change is archived as the last commit of its own PR

`openspec archive <name>` syncs the change's delta specs into `openspec/specs/` and moves the change into `archive/`. Doing that inside the change's PR, as its final commit, has two effects:
- the spec sync is reviewed alongside the code that justifies it, and lands in the same squash merge;
- `main` never holds a merged-but-unarchived change, so "archived" and "merged" mean the same thing to the frontier.

Out-of-scope findings from review become new changes (a proposal with a Coordination block).

Intake issues are commented and closed once, at conversion (D1); archive does not touch GitHub. Automated dependency-bump PRs are exempt from naming a change: they alter no promise, and forcing a change per weekly bump would bury the frontier.

*Rejected:*
- **Archive on `main` after merge.** This needs a direct push or a second PR per change, and it leaves a window in which the frontier sees merged work as open.

### D4. Go source must not cite a change path; capability specs may be cited

Every change ends in archive (D3), which moves its directory. So **every** `openspec/changes/...` citation in Go source is a future dangling reference, exactly the class `check-doc-refs.sh` exists for.

A new rule row, `openspec-change`, forbids the regex `openspec/changes/` in `*.go`. The script's retirement model ("active while the target path is absent") does not fit this rule, because the target path is present. The row therefore uses an explicit `-` target meaning "never retires".

`openspec/specs/<capability>/spec.md` is allowed, and the self-test's clean fixture pins that.

*Rejected:*
- **Per-citation existence check.** It passes exactly the citations that are about to dangle.
- **No guard.** The script's own header documents that a prose rule regrew five dangling citations within one PR cycle.

### D5. Decisions live in changes and specs; the code-comment rule is kept

The old rule, "decisions live in commits, PR bodies and code comments, not a document tree", is reversed:
- a decision's rationale and its rejected options live in the change's `design.md`, which is archived;
- the behavior it fixes lives in the capability spec.

The ADR directory's deliberate deletion is kept as history. ADRs were free-floating; changes are tied to a lifecycle and archived with the specs they produced.

The rule kept is: **state the constraint in a comment on the code it governs, naming an identifier**.

### D6. CLAUDE.md is the single home of the workflow rules

The agent docs that restated the tracker, the triage labels and the domain rules were configuration for a plugin the repo no longer enables. Keeping them would leave a second description of the workflow that drifts. They are removed, at the user's direction, and the rules the project still uses move into two `CLAUDE.md` sections:

| Rule | Home |
|---|---|
| GitHub issues as input (D1); frontier, in-progress, archive-in-PR, Dependabot exemption, review findings (D2, D3) | `CLAUDE.md`, *Work tracking* |
| Where decisions live, Go citations (D4, D5) | `CLAUDE.md`, *Domain and decisions* |
| The Coordination block (D2) | `openspec/config.yaml` proposal rule |

Test-first is stated as a practice, not routed through a plugin skill: red → green → refactor and "defects need proof" are unchanged. The uncommitted multi-agent brief at the repo root is also removed, at the user's direction; its tracker mechanics are replaced by D2 and D3.

*Rejected:* **Keep the docs as plain reference.** They were written to answer the plugin's lookups, so without the plugin they read as instructions to a reader who no longer exists.

### D7. A CI job checks that OpenSpec reads the config as written

Measured on CLI 1.14.1, `openspec validate --all --strict` exits 0 with invalid YAML in `openspec/config.yaml`, a misspelled artifact or operation id, an unquoted ": " inside a rule, or an unknown top-level key. The first four warn only on stderr of `openspec instructions`; the last is silent. `scripts/check-openspec.sh` probes every artifact's instructions in a scratch copy of `openspec/` holding a throwaway change, fails on any stderr, allows only the documented top-level keys, and then runs `validate --all --strict` and `validate --archived`. Its self-test plants each of the five mistakes and requires a failure, and requires the real config to pass. The CLI is pinned to 1.14.1 so an upgrade is a reviewed change.

*Rejected:*
- **Run only `openspec validate`.** It never reads the config, so every case above passes.
- **Parse the YAML with a YAML library.** It would check our reading of the config, not OpenSpec's; asking the CLI is the only way to see what it actually ignores.

### D8. Gherkin replaces the template's WHEN/THEN bullets, deliberately

The built-in spec instruction and template write scenarios as `- **WHEN** / - **THEN**` bullets. The project rule writes them as a fenced Gherkin block under the same `#### Scenario:` header, because `Scenario Outline` with `Examples` tables is how boundary rows are enumerated without near-duplicate scenarios. Measured: `validate --strict` accepts it, `show --json` keeps the block intact, a `#### Scenario:` line inside a fence is not split off, archive syncs it unchanged, and the verify skill finds scenarios by header. The rule states the override explicitly so the agent is not left choosing between two formats. Requirement text still needs SHALL or MUST and stays under 500 characters, which strict validation enforces.

*Rejected:* **Fork the schema to change the template.** That takes on maintaining a forked schema across CLI upgrades for a formatting preference the rules already express.

### D9. The baseline departs from OpenSpec's brownfield guidance, narrowly

OpenSpec's guidance for existing projects is to write specs only for what a change is about to touch, and it warns that a bulk conversion "tends to produce a large, stale spec nobody trusts". `establish-baseline-specs` departs from it because the 85-issue migration ADDs and MODIFIES requirements, and a MODIFIED delta needs a baseline requirement to modify. The departure is kept narrow: a baseline spec records only decided, documented promises (closed issues, the #20 register, godoc contracts); undecided or merely observed behavior gets a spec when a change touches it. The `establish-baseline-specs` design restates this.

## Risks / Trade-offs

- **[Coordination blocks drift from reality]** → The frontier reads them on every selection, so a wrong `Depends on` surfaces as a wrong frontier immediately. The proposal rule makes the block mandatory.
- **[Committed generated files drift]** `openspec update` regenerates the committed skills and commands from each contributor's global profile; on the default `core` profile it deletes five of them. → `CONTRIBUTING.md` names the profile to select first, and a deletion shows up in review as a removed file.
- **[The guard's always-active mode has no retirement path]** → Accepted: a change path is dangling by construction, so there is nothing to retire into. The script header says so.
- **[The interim state]** Between this change merging and the migration finishing, 85 open GitHub issues still carry work. → *Work tracking* states that open `wayfinder:*` issues are pending migration and must not be worked directly.

## Migration Plan

Docs, settings and one CI guard row. It lands as one PR whose last commit archives this change (D3). Rollback is a revert of that squash merge. `openspec/config.yaml` would then disagree with the docs again, so revert both together.
