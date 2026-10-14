## Why

wrkflw has no `README.md` and no stability statement. pkg.go.dev renders nothing for the module root, and the rolling-upgrade warning that the deleted `STABILITY.md` carried is now reachable only from a Go struct field comment. That warning describes data loss: an older build reads a newer instance snapshot, silently drops the fields it does not know, and writes the truncated document back. The first tag (`cut-v0-release`) cannot ship without both documents.

## What Changes

Every premise below was re-measured on `main` (`eebe55ba`) before it was listed.

- **kartaladev/wrkflw#158 (folded in).** Write a root `README.md` and a consumer-facing stability statement.
  - `README.md` does not exist at `main` (`git ls-tree` shows none). Only `processtest/README.md` and `runtime/README.md` exist. `examples/readme_quickstart/main.go` says it "mirrors the README quickstart", so the README quickstart must match that example.
  - The README links the `# Trust boundary` section of root `doc.go` (present at `doc.go:103`). This is the acceptance criterion carried from #43 onto #48.
  - The stability statement restores the deleted `STABILITY.md` (last content at `f524e82a`): the SemVer and `v0.y.z` policy, what "public API" covers (`internal/` and `examples/` excluded), the deprecation taxonomy, and **the rolling-upgrade section with its upgrade procedure**. The mechanism is still live. `internal/persistence/store/store_core.go:175` decodes the snapshot with a plain `json.Unmarshal(snap, &stateOut)`. Two tests pin that `DisallowUnknownFields` is absent: `engine/state_arms_wire_test.go:199` and `engine/state_token_provenance_test.go:31`. The surviving warning sits on `Incident.Kind` at `engine/state.go:302-307`.
  - The statement does not carry the deleted document's stale parts: its `Go 1.25` floor (go.mod now says `go 1.26.9`), its locked-dependency list (watermill and casbin have gone or are going), its ADR citations, which `scripts/check-doc-refs.sh` refuses, or its per-option HTTP convention notes, which belong in godoc.
  - It states the two data-loss instances inline, as #158 instructs, using public identifiers rather than internal file paths.
- **kartaladev/wrkflw#37 (moved).** Its decision half, the history-cap default with its benchmark evidence and the `instance-store` spec delta, moved to `decide-history-cap-default`, which cites #37. This change keeps only the README persistence section that states the default that change settles, the growth shape and the benchmark name.
- **The rollback (downgrade) posture** raised on kartaladev/wrkflw#48 for #212 is recorded alongside the rolling-upgrade section, because the same mechanism makes a downgrade lossy. Which posture to state is open (design, Open Questions). #48 itself is `cut-v0-release`'s.

**Stale premises found on re-measurement**
- #158 cites the surviving warning at `engine/state.go:304-307`. It is now `:302-307`.
- #48's comment on #120 names the new token field `ArrivalFlowID`. The field is `Token.ArrivalFlow` (`engine/state.go:172`).
- The deleted document's line citations (`store_core.go:78,216,164`) are stale. The live decode is at `:175`.

**Decisions this change cannot be finished without (unresolved, Readiness: human)**
1. #48 / #212: the downgrade posture to state (design Q1). The recommendation is that downgrade is unsupported in v0.x.
2. #158: where the stability statement lives: `STABILITY.md`, a README section, or a root `doc.go` section next to `# Trust boundary`. #158 leaves this to the PR (design Q2).

The history-cap default (#37) is decided in `decide-history-cap-default`, not here.

**Out of scope**
- Making the snapshot decode refuse unknown fields, or adding a format version to the snapshot. #158 records the silent drop as the design, and `guard-authz-field-totality` explicitly does not reopen it either. A fix would be its own change.
- The history-cap default, its `instance-store` requirements and their tests. Owned by `decide-history-cap-default` (kartaladev/wrkflw#37).
- The snapshot redesign that would remove the quadratic growth (whole-document rewrite, per-step `cloneState`). #37 puts it out of scope.
- Release notes, the first version number, the Go floor line in the notes, tagging, and pkg.go.dev resolution of every module. Owned by `cut-v0-release` (kartaladev/wrkflw#48).
- Fixing doc-link targets in root `doc.go` (#130). Owned by `fix-doc-rot`. #158 calls the two cheaper together but names no dependency.
- `SECURITY.md` and `internal/atrest` output (#52). Not reopened.
- Per-module READMEs for the nested modules `split-persistence-modules` creates. Their package `doc.go` files carry that.

**BREAKING:** none. Documentation only. **Persisted formats:** not affected; the statement documents the existing snapshot hazard and does not change it. **Default security posture:** not affected. The README states the history-cap default's resource growth as `decide-history-cap-default` settles it. **Consumer go.mod:** not affected.

**Coordination**
- Size: M
- Depends on: decide-history-cap-default, split-persistence-modules
- Blocks: cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. The README and the stability statement are documentation and add no requirement, so `skip_specs` is set. The `instance-store` delta for the history-cap default lives in `decide-history-cap-default`.

## Impact

- **Docs:** new `README.md`; the stability statement (location per decision 2); possibly root `doc.go` (decision 2); the `Incident.Kind` field comment.
- **Tests:** none.
- **Code:** none.
- **Coupling:** `split-persistence-modules` moves `internal/persistence/store` and creates the modules the README's install section names. `encode-persisted-enums-by-name` assigns its "recreate a pre-release database" statement to "the README's persistence section". This change writes that section. `decide-history-cap-default` settles the history-cap default that section states.
