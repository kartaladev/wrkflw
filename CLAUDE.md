# CLAUDE.md

## Work tracking

**OpenSpec is the single source of truth.** Work lives as changes under `openspec/changes/`, and what the library promises lives as capability specs under `openspec/specs/`. Drive them with the `openspec` CLI and the `/opsx:*` commands. Project context and per-artifact rules are in `openspec/config.yaml`. Never create a change directory by hand; use `openspec new change`.

**GitHub issues on `kartaladev/wrkflw` are input, not a tracker.** Nobody works an issue directly. Its content goes into an OpenSpec change whose proposal cites `kartaladev/wrkflw#N` (a new change, or one that already covers it); then the issue is commented with the change name and closed. No labels or triage states are kept on GitHub.

Open issues carrying a `wayfinder:*` label predate the move and are **pending migration**; do not work them directly.

**Picking work.** Every proposal ends *What Changes* with a Coordination block (Size, Depends on, Blocks, Readiness). The frontier is computed like this:
- active changes come from `openspec list --json`; a change counts as unfinished whether its status is `in-progress` or `no-tasks` (a change with no tasks yet is ready for planning, not done);
- archived change names come from `openspec list --archived --json`, with the `YYYY-MM-DD-` date prefix stripped;
- a change is on the frontier when it is unfinished, every name in its `Depends on` is archived, and no open PR names it.

Prefer the change that unblocks the most others, counted from the `Depends on` lines of active changes (`Blocks` is the author's forecast), then the smallest. A change is in progress when an open PR names it. When a dependency is abandoned, edit the dependents' `Depends on` in the same PR that removes it. A change with status `complete` (every task ticked, not archived) and no open PR is an anomaly, usually a PR merged without its archive commit: report it rather than working around it, because its dependents stay blocked until it is archived.

**Finishing work.** The PR body names the change it implements. Its last commit runs `openspec archive <change> --yes` (without `--yes` the command waits for a confirmation an agent cannot give whenever the change has spec deltas), so the spec sync is reviewed with the code and "merged" means "archived". Intake issues were already closed at conversion; archiving does not touch GitHub. Automated dependency-bump PRs (Dependabot) are exempt: they name no change unless a bump alters library behavior, in which case a change is opened for it.

**Review findings.** A finding outside the change's scope becomes a new change (a proposal with a Coordination block), not an addition to the open PR.

## Agent docs

### Domain and decisions

**Where decisions live.**
- A decision's rationale, and the options it rejected, live in the `design.md` of the OpenSpec change that made it, archived under `openspec/changes/archive/`.
- The behavior the decision fixes lives in the capability spec it updated, under `openspec/specs/`.
- The constraint it produces is **also** stated in a comment on the code it governs, naming an identifier a reader can jump to (for example `ErrScopeLocalWithCompensateRef`).

The old ADR directory was deleted deliberately and stays deleted. Change artifacts replace it because each is tied to a change and archived with its specs.

**Citations in Go source.** A comment may cite a capability spec (`openspec/specs/...`), never a change path (`openspec/changes/...`), because archiving moves it. `scripts/check-doc-refs.sh` fails the build on a change-path citation and on any citation of the deleted ADRs.

### Eventually waits

Wait for every state the assertions read, not a signal that merely correlates with them. See `docs/agents/eventually-waits.md`.

### Raw `time.After` deadlines

Classify a `select` deadline by what its clause does, not by how long it is; every raw deadline is also counted against a per-package CI ceiling that rejects non-literal durations. See `docs/agents/test-deadlines.md`.

## Coding discipline

**Route first.** Every Go task — writing, reviewing, debugging, setup — starts with `cc-skills-golang:golang-how-to`, which selects the golang skills that task needs.

**Red → green → refactor.** Write the failing test first, watch it go red, make it pass, then refactor. At the refactor step, consider `/simplify`.

**Hot paths carry test cases.** No hard coverage gate; 90%+ is the number to aim at.

**Defects need proof.** An error or code defect counts as real once a failing test reproduces it — write that test first, watch it go red, then fix. Until then it is a hypothesis, and gets reported as one.
