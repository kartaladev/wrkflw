# CLAUDE.md

## Agent skills

### Issue tracker

Issues live as GitHub issues on `kartaladev/wrkflw`, driven via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its name (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one root `CONTEXT.md`, created lazily. Decisions live in commit messages, PR bodies and code comments, not a document tree. See `docs/agents/domain.md`.

### Eventually waits

Wait for every state the assertions read, not a signal that merely correlates with them. See `docs/agents/eventually-waits.md`.

## Coding discipline

**Route first.** Every Go task — writing, reviewing, debugging, setup — starts with `cc-skills-golang:golang-how-to`, which selects the golang skills that task needs.

**Red → green → refactor.** Write code through `mattpocock-skills:tdd`. At the refactor step, consider `/simplify`.

**Hot paths carry test cases.** No hard coverage gate; 90%+ is the number to aim at.

**Defects need proof.** An error or code defect counts as real once a failing test reproduces it — write that test first, watch it go red, then fix. Until then it is a hypothesis, and gets reported as one.
