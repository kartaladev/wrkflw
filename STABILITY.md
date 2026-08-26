# Stability and versioning policy

This document states the compatibility promise for the `github.com/kartaladev/wrkflw`
module. It is intentionally honest about the project's current maturity.

## Current status: pre-1.0, unreleased

The module has **no released version yet** (no `v0.x` or `v1.x` git tag). It is under
active development. Until a tagged release exists, **every exported symbol in every
root package is subject to change without notice**, and consumers should pin to a
specific commit (Go modules pseudo-version) and expect to read the diff on each bump.

Do not assume any API is frozen until this document says a version line is.

## Versioning: Semantic Versioning (SemVer 2.0.0)

Once releases begin, the module follows [Semantic Versioning](https://semver.org/):

- **MAJOR** (`vX`) — breaking changes to the public root-package API. Per Go module rules,
  `v2+` ships under a versioned module path (`/v2`, `/v3`, …); `v0`/`v1` share the base path.
- **MINOR** (`vX.Y`) — backwards-compatible additions (new functions, options, fields,
  node kinds, transports).
- **PATCH** (`vX.Y.Z`) — backwards-compatible bug fixes only.

### The `v0.y.z` (pre-1.0) phase

While the module is in `v0`, SemVer permits breaking changes in any release. We will use
this latitude responsibly:

- `v0.y.z` → `v0.(y+1).0` **may** contain breaking changes; the CHANGELOG/release notes
  will call them out explicitly.
- `v0.y.z` → `v0.y.(z+1)` is reserved for bug fixes and is intended to be safe to take.

A `v1.0.0` release is the point at which the public root-package API
(`engine/`, `definition/`, `runtime/`, `action/`, `authz/`, the transport adapters, …) is
considered stable and the full MAJOR/MINOR/PATCH promise above applies.

## What "public API" means

The compatibility promise covers only the **exported, module-root packages** — the
importable surface a consumer embeds (see the README "Package map"). It does **not** cover:

- Anything under `internal/` — these are implementation details, never importable by
  consumers, and may change at any time regardless of version.
- The `examples/` reference wiring — illustrative `main` packages, not a supported API.
- Behaviour explicitly documented as reserved, experimental, or "not yet emitted".
- The on-disk database schema and migration files as a *direct* contract — they are an
  implementation detail of each backend adapter (Postgres, MySQL, SQLite), evolved via
  the migration mechanism, not a hand-editable surface.


### Request body caps (ADR-0186, pre-v0.1.0)

`httpcore.CustomizeConfig.MaxBodyBytes` and the three adapter options
(`stdlib`/`gin`/`fiber` `WithMaxBodyBytes`) follow the library's existing bound convention, matching
`action/httpcall.WithMaxResponseSize`: a plain `int64`, a **non-positive value disables** the bound,
and the default is applied when the option is absent. New code bounding a body should use the same
shape rather than introducing a pointer or a sentinel of its own.

`httpcore.CustomizeConfig.BodyReadTimeout` and `WithBodyReadTimeout` (plus the `stdlib` and `gin`
aliases) follow the same convention: a `time.Duration`, **non-positive disables**, default applied
when the option is absent. It is armed **only when the cap is active**, and there is no `fiber`
alias because fasthttp reads the body before the route group is entered.

`httpcore.ErrRequestBodyTooLarge` classifies as **413**. ⚠ It is distinct from
`action/httpcall.ErrBodyTooLarge`, which means an *outbound response* exceeded that action's own cap
and correctly remains a **500**.

⚠ `ClassifyError`'s arms are **ordered**, and position is behaviour: an error matching two arms
resolves to whichever comes first. Any new arm must state its position relative to the arms it can
co-match and carry a test asserting the intended resolution.

### Request-actor identity (ADR-0189, pre-v0.1.0)

`httpcore.CustomizeConfig.RequestActor` and the three adapter `WithRequestActor` aliases follow the
**nil-restores-the-default** convention rather than the non-positive-disables one used by the
bounds above, because a `func` has a nil and there is no "disabled" state to preserve: identity
resolution is never optional. `WithRequestActor(nil)` therefore restores the fail-closed default,
which reads `authz.ContextWithActor` and refuses with **401** when nothing authenticated the caller.

`RequestActorTimeout` and `WithRequestActorTimeout` DO follow the bound convention — a
`time.Duration`, **non-positive disables**, default 10s, mirroring the engine's
`WithCandidateResolveTimeout`. ⚠ It bounds only a resolver that **honours `ctx` cancellation**; a
resolver that ignores it still runs to completion. The hang is narrowed, not closed.

`httpcore.ErrUnauthenticated` classifies as **401** and `httpcore.ErrIdentityUnavailable` as
**503**. ⚠ Both are the **first two arms** of `ClassifyError`, ahead of every other arm, because
`ErrIdentityUnavailable` wraps arbitrary consumer-supplied errors with `%w` and could otherwise
co-match the 404, 403 or 400 arms. A resolver returning `authz.ErrNotAuthorized` is deliberately a
**503, not a 403**: an identity resolver answers *who*, not *may*.

⚠ Actor attributes supplied through a resolver are bounded — 64 levels of nesting, 16 KiB
marshalled — and **deep-copied** at the seam. Both limits classify as 503. The bound is not
cosmetic: `encoding/json`'s encoder has no nesting limit while its decoder caps the whole stored
document at 10000, so an unbounded attribute can be written durably and then make the task row
permanently unreadable.

## Deprecation taxonomy

When an exported symbol must be retired, we deprecate before removal rather than breaking
abruptly (subject to the pre-1.0 latitude above):

1. **Mark.** The symbol gets a Go `// Deprecated:` doc comment, as the first line of a
   paragraph, naming the replacement and the reason. Tooling (`gopls`, `staticcheck`'s
   `SA1019`, IDEs) surfaces these to consumers automatically:

   ```go
   // Deprecated: use NewCacheWithConfig instead; NewCache cannot express the
   // eviction-policy option and will be removed in v2.
   func NewCache(...) *Cache { ... }
   ```

2. **Keep working.** A deprecated symbol continues to function for at least one MINOR
   release line after it is marked, so consumers have a release in which both the old and
   the replacement exist.

3. **Remove.** Deprecated symbols are removed only in a MAJOR release (or, during `v0`, in
   a MINOR release that the notes flag as breaking). The release notes list every removal.

We do not silently change the behaviour of a non-deprecated symbol; a behaviour change to
an existing symbol is treated as breaking and follows the same MAJOR/`v0`-MINOR rule.

## Go and dependency baseline

- **Go 1.25** is the minimum supported toolchain (a hard requirement; see the README
  "Locked tech stack").
- Locked dependencies (PostgreSQL 17, MySQL 8.0+, SQLite (`modernc.org/sqlite`), `expr-lang/expr`,
  `watermill`, `gocron` pinned to v2.22.0, `clockwork`, `casbin`, `samber/do` v2) are changed only
  via an ADR. A change to the minimum Go version or a locked dependency major is treated as a
  breaking change.

The SQLite backend is test/single-node-oriented — it is not supported for multi-replica deployments
(`persistence.NewSQLiteAdvisoryLockOwnership` is fail-loud). The `Deduper.Seen` signature change
(driver-tx param dropped) is one of the pre-1.0 breaking changes flagged in the CHANGELOG (ADR-0081).

## Rolling upgrades are NOT supported

There is no N-1 compatibility promise, and we do not intend to imply one. **Never run two
different `wrkflw` builds against the same instance store**, not even briefly during a
deploy.

The mechanism is structural, not a property of any one release. The store persists the
**whole** `engine.InstanceState` as a single JSON document
(`internal/persistence/store/store_core.go:78,216` — `json.Marshal(capHistory(step.State,
s.historyCap))`) and decodes it with a plain `json.Unmarshal` (`:164`) — no
`DisallowUnknownFields`. An older build therefore reads a newer snapshot **successfully**,
silently drops every field it does not know, and writes the truncated document back on the
next commit. Every future field added to `InstanceState` inherits this behaviour.

Two shipped instances of the resulting data loss:

- **ADR-0173** — an old build reading a new compensation cursor drops the three window
  fields and re-serializes without them, reinstating the double-run the ADR exists to
  prevent.
- **ADR-0175** / `engine/state.go` (the `Incident.Kind` field comment) — an old build
  round-trips a new snapshot with `Kind` dropped, degrading an `IncidentCompensationStall`
  into a resolvable `IncidentAction` that the shipped resolve-incident endpoint will then
  delete.

**Upgrade procedure:** stop accepting new work, let in-flight steps drain, stop *all*
replicas, then start the new version. A blue/green or canary rollout that has old and new
replicas sharing one instance store is unsafe by construction.

The asymmetry is worth naming: the loss happens when the **older** build reads. ADR-0173 and
ADR-0175 each state that the forward direction — new code reading a snapshot an older build
wrote — is safe *for the field that ADR added*, because its zero value is defined to mean
"pre-ADR record". That is a per-ADR guarantee those two ADRs chose to provide, **not** a
standing promise of this document; a future state-carrying change owes the same analysis in
its own ADR.
