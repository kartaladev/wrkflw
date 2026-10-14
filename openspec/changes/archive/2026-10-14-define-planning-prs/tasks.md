# Tasks

## 1. Define planning PRs

- [x] 1.1 Add a *Planning work* paragraph to `CLAUDE.md` *Work tracking*, after *Finishing work*: a planning PR adds or revises change artifacts only, lists the changes it adds, archives nothing and changes no code; after merge, each issue its changes cite is commented with the change name and closed. Verify: `grep -n 'Planning work' CLAUDE.md` finds it, and `scripts/check-openspec.sh` exits 0.

## Workflow follow-up

- Last commit of the PR: `openspec archive define-planning-prs --yes`.
