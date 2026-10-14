# Proposal

## Why

`CLAUDE.md` says every PR archives the change it implements as its last commit, but a PR that only adds or revises change artifacts (planning) implements nothing and archives nothing; #245 had to break the rule, and the issue migration will open several more such PRs.

## What Changes

- **Define planning PRs in `CLAUDE.md` *Work tracking*.** A planning PR adds or revises change artifacts only: its body lists the changes it adds, it archives nothing, and it changes no code. After it merges, each GitHub issue its changes cite is commented with the change name and closed (conversion).
- **Keep implementation PRs as they are.** A PR that changes code still implements exactly one change and archives it as its last commit.

**Out of scope**
- Migrating any issue; the migration waves follow this change.

**Coordination**
- Size: S.
- Depends on: nothing.
- Blocks: nothing.
- Readiness: agent.

## Capabilities

### New Capabilities

None. Contributor workflow documentation only; `skip_specs` is set.

### Modified Capabilities

None.

## Impact

- **`CLAUDE.md`:** one paragraph in *Work tracking*.
- **Not affected:** library behavior, persisted formats, the default security posture and any consumer's `go.mod`. Nothing is BREAKING.
