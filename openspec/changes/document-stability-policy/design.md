## Context

Measured on `main` at `eebe55ba`. The motivation is in proposal.md, *Why*.

**What a consumer meets today.**
- No root `README.md`. pkg.go.dev shows the root package's `doc.go` and nothing else. Root `doc.go` has eight sections, including `# Start here` (`:16`) and `# Trust boundary` (`:103`).
- `examples/readme_quickstart/main.go` says it "mirrors the README quickstart end to end", in three blocks: builder, YAML and run. The README it mirrors is the one that was deleted.
- The rolling-upgrade warning survives only on `engine.Incident.Kind` (`engine/state.go:302-307`).

**The mechanism the warning describes.** It is unchanged since `STABILITY.md` was deleted in `12630621`. The store writes the whole `engine.InstanceState` as one JSON document on every `Create` and `Commit`, and decodes it with a plain `json.Unmarshal` (`internal/persistence/store/store_core.go:175`). Two tests pin that `DisallowUnknownFields` is absent (`engine/state_arms_wire_test.go:199`, `engine/state_token_provenance_test.go:31`). A build that does not know a field reads a snapshot carrying it, drops the field, and writes the snapshot back without it on its next commit. Fields that carry this hazard today include `Incident.Kind` and `Token.ArrivalFlow` (`engine/state.go:172`, added by #120). The compensation-cursor instance that `STABILITY.md` named was not re-measured here. Task 2.2 re-derives it before it is quoted.

**History growth (#37).** The history-cap default, its benchmark evidence and its `instance-store` requirements live in `decide-history-cap-default`. This change only states, in the README persistence section, the default that change settles, the growth shape and the benchmark's name.

## Goals / Non-Goals

**Goals:**
- A root `README.md` that renders on pkg.go.dev, keeps its quickstart identical to `examples/readme_quickstart`, and links `# Trust boundary` and the stability statement.
- A stability statement a consumer meets on pkg.go.dev, carrying the rolling-upgrade warning, the upgrade procedure and the downgrade posture.
- A README persistence section that states the history-cap default settled by `decide-history-cap-default`, and the growth shape.

**Non-Goals:**
- Changing the snapshot decode, adding a snapshot format version, or removing the quadratic growth.
- Deciding or specifying the history-cap default (`decide-history-cap-default`).
- A guard that fails the build when the README quickstart drifts from the example. If one is wanted, it is a separate change.
- Release notes, which belong to `cut-v0-release`.

## Decisions

### D1. Restore the rolling-upgrade section and its procedure
The section and the procedure come back substantively as `f524e82a:STABILITY.md` wrote them: never run two builds against one instance store, and to upgrade, stop accepting new work, drain in-flight steps, stop every replica, then start the new version. The section also keeps its note on the asymmetry: the loss happens when the **older** build reads. The maintainer's note on #158 instructs this ("Restore the rolling-upgrade section, and restore the upgrade procedure with it"), and the mechanism is still live (Context).
*Rejected:* a softer "mixed versions are not recommended". The measured behavior is silent data loss, and the project rule is that silence is never correct.

### D2. Cite public identifiers, not file paths
The statement names `engine.InstanceState`, `engine.Incident.Kind` and `engine.Token.ArrivalFlow`. It names no `internal/` path and no line number. `split-persistence-modules` moves `internal/persistence/store` under `persistence/internal/` (its design names this change as one that must re-derive paths). The old document's own citations had already drifted from `:164` to `:175`. Exported identifiers survive both.
*Rejected:* quoting `store_core.go:175`, as #158 item 2 assumes. That would be rewritten at the split, and a consumer cannot open an `internal/` file from pkg.go.dev anyway.

### D3. State the two data-loss instances inline
#158 item 4 says to do this. `scripts/check-doc-refs.sh` refuses citations of the deleted ADRs, and its own remedy column says to state the substance inline. The `Incident.Kind` instance is re-measured (Context). The compensation-cursor instance is re-derived at apply (task 1.2), and dropped if its fields no longer exist.

### D4. Carry only the still-true parts of the deleted document
Carried: the SemVer policy, the `v0.y.z` latitude, what "public API" covers (excluding `internal/`, `examples/`, behavior documented as reserved or experimental, and the schema as a direct contract), and the deprecation taxonomy.
Dropped, each for a measured reason: the `Go 1.25` floor (`go.mod` says `go 1.26.9`; the README states the floor by pointing at `go.mod`), the locked-dependency list (watermill is gone; casbin goes in `remove-casbin`), the ADR-0186 and ADR-0189 HTTP option notes (they are option conventions and belong in godoc), and every ADR citation (D3).

### D5. The README quickstart is the example
The README's quickstart blocks are copied from `examples/readme_quickstart/main.go`, which compiles in CI as part of the `examples` module. The example is the source and the README is the copy. The README states no benchmark figures. It states the growth shape and names the benchmark, because figures drift.

### D6. The README carries the pre-release database statement
`encode-persisted-enums-by-name` (design, Risks) assigns "a database from an earlier build must be recreated" to "the README's persistence section". That change has no task that writes it, and the README is created here. This change writes the section. That change's PR, or `cut-v0-release` if it lands later, checks the wording.

## Risks / Trade-offs

- [The README quickstart drifts from the example] → D5 makes the example the source. No guard is added (Non-Goals). The risk is recorded so that a later change can add one.
- [The statement is written before the module split and names old module paths] → This change depends on `split-persistence-modules`, so the install section names the post-split modules.
- [A consumer reads "rolling upgrades are not supported" as a v1 promise] → The statement scopes it to the v0.x line and to the mechanism (Context). It does not promise that a later line keeps it.
- [The history-cap default leaves a resource-growth hazard on, if `decide-history-cap-default` keeps it unbounded] → Only consumers who call `WarnUnsafeConfig` see its log line, so the README persistence section states the growth too.

## Cross-change coupling

| Change | Coupling | Owner / order |
| --- | --- | --- |
| `decide-history-cap-default` (#37) | Settles the history-cap default, which the README persistence section states. | It lands first (this change's Depends on). |
| `split-persistence-modules` | Moves `internal/persistence/store` and creates `persistence/pgx`, `persistence/sql` and the binding modules that the README install section names. | It lands first (this change's Depends on). |
| `cut-v0-release` | Its task 1.2 checks that the README exists and links `# Trust boundary` and the stability statement. Its notes link the downgrade posture this change states. | This change lands first (Blocks). |
| `encode-persisted-enums-by-name` | D6: the README persistence section carries its "recreate a pre-release database" statement. | Either order. If this lands first, that change edits the sentence when needed. |
| `authorize-task-triggers-in-driver` | It edits the `# Trust boundary` section of root `doc.go`. If Q2 picks a `doc.go` section, both edit that file. | Either order. The second lander rebases. The README links the heading, not a line. |
| `fix-doc-rot` (#130) | It fixes root `doc.go` link targets. #158 calls the two cheaper together. | Either order. No dependency edge. |
| `guard-authz-field-totality` | Its design defers unknown-key decoding to #158. This change documents the hazard and does not change the decode. | No order. |

## Migration Plan

Documentation only, so there is nothing to migrate.

## Open Questions

These need the maintainer before apply (Readiness: human).

**Q1. Downgrade posture (#48, raised from #212).** It changes the statement's text only.
- (a) **Downgrade is unsupported in v0.x (recommended).** This follows from the same mechanism as D1: an older build reading a newer snapshot loses data. Stating anything weaker would be untrue.
- (b) A one-minor-version rollback window. That is a real commitment, and it would constrain every persisted-shape change from here on.
- (c) Say nothing, and record that choice. Rejected as a recommendation: #48's comment calls discovering the answer from a support ticket indefensible.

**Q2. Where the statement lives (#158).** It changes which files the tasks touch.
- (a) **A `# Stability and upgrades` section in root `doc.go`, next to `# Trust boundary` (recommended), with a short README section linking to it.** pkg.go.dev renders it for the root package. It follows the #43 precedent, which declined to recreate `SECURITY.md` and put the posture in `doc.go`, and it does not live in a file that a docs collapse can delete as collateral.
- (b) `STABILITY.md`. GitHub renders it, but pkg.go.dev does not, and it was deleted once already.
- (c) A README section only. pkg.go.dev renders it, but gopls and `go doc` do not.
