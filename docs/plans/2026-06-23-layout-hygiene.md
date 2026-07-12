# Layout Hygiene (FOLLOWUPS ①) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Evict the two genuinely non-public packages (`database`, `expreval`) from the public module root into `internal/`, add a root "front door" doc, and record the decision as an ADR — reaffirming the flat root layout and rejecting the `pkg/` reorg.

**Architecture:** Pure-refactor package moves (no behavioral change) plus two doc-only artifacts. `database/` is a testcontainers helper imported only by `_test.go` files; `expreval/` is the expr-lang wrapper imported only in-module. Both move under `internal/`, where they stay importable by every in-module package but vanish from the consumer's public import surface. Package *names* are unchanged, so only import paths move; package-qualified call sites (`database.RunTestDatabase`, `expreval.New`) are untouched.

**Tech Stack:** Go 1.25, single module `github.com/kartaladev/wrkflw`. Verification via `go build ./...`, `go vet ./...`, `golangci-lint run ./...`, and the existing test suite (testcontainers tests need a running Docker daemon).

## Global Constraints

- Go 1.25; single `go.mod` at repo root; module path `github.com/kartaladev/wrkflw`.
- No `pkg/` prefix — public packages live at the module root (ADR-0004). This sub-project **reaffirms** that.
- `internal/` is for non-exported implementation consumers must not import; it remains importable by every package in this module.
- This is a **pure refactor**: no new exported symbols, no behavior change. Per CLAUDE.md TDD discipline, pure refactors need no new tests, but the existing suite MUST pass before AND after each task. The per-task verification gate is `go build ./...` + `go vet ./...` + `golangci-lint run ./...` (and targeted `go test` where noted).
- ADRs follow the Nygard template (Status/Date, Context, Decision, Consequences) under `docs/adr/NNNN-<slug>.md`. Next free number is **0041**.
- Error sentinels (none added here) use the `workflow-<package>:` prefix convention.
- Conventional Commits scoped to the area; ask before committing per Git Discipline (the executor handles commit approval).

## File map

| Path | Action | Responsibility |
|---|---|---|
| `docs/adr/0041-evict-internal-packages-from-public-root.md` | Create | Records the decision; reaffirms ADR-0004; rejects `pkg/`. |
| `internal/database/testutils.go` | Create (move from `database/testutils.go`) | testcontainers PostgreSQL helper; package `database`. |
| `database/` | Delete (after move) | — |
| `internal/expreval/expreval.go` | Create (move from `expreval/expreval.go`) | expr-lang wrapper; package `expreval`. |
| `internal/expreval/expreval_test.go` | Create (move from `expreval/expreval_test.go`) | black-box tests for the wrapper. |
| `expreval/` | Delete (after move) | — |
| 36 `_test.go` files | Modify (import path only) | switch `.../database` → `.../internal/database`. |
| `engine/conditions.go`, `authz/authz.go`, `internal/authz/casbin/authorizer.go` | Modify (import path only) | switch `.../expreval` → `.../internal/expreval`. |
| `doc.go` | Create | Root "start here" navigation doc listing the 14 public packages. |

---

### Task 1: Record the decision (ADR-0041)

Write the ADR first so the moves in Tasks 2–3 have a rationale to reference. Doc-only; no code, no test.

**Files:**
- Create: `docs/adr/0041-evict-internal-packages-from-public-root.md`

**Interfaces:**
- Consumes: nothing.
- Produces: ADR-0041 (referenced by Task 2/3 commit messages and `doc.go`).

- [ ] **Step 1: Write the ADR**

Create `docs/adr/0041-evict-internal-packages-from-public-root.md` with exactly this content:

```markdown
# 41. Evict test/internal-only packages from the public root; reaffirm flat layout

- Status: Accepted
- Date: 2026-06-23

## Context

A design follow-up proposed introducing a `pkg/` directory and moving
"plumbing" out of the module root so the root holds "only workflow code". A
review (docs/specs/2026-06-23-followups-resolution-design.md) found:

- The repository already implements the façade pattern: `internal/` holds the
  concrete implementations (authz, eventing, observability, persistence,
  scheduling) and the matching root packages are thin consumer-facing façades.
  Plumbing is therefore already hidden.
- Re-introducing `pkg/` would reverse ADR-0004 (flat root layout, an explicit
  owner decision), break the import path of every moved package — the precise
  harm the library-first rule exists to prevent — and `pkg/X` is still public,
  so it would not encapsulate anything.
- Exactly two root packages are not part of the public API:
  - `database` — a single testcontainers helper (`RunTestDatabase`) imported
    only by `_test.go` files. Sitting in the public root, it drags the heavy
    testcontainers dependency into any consumer's import graph.
  - `expreval` — the expr-lang wrapper. It exposes only `New()` and
    `EvalBool/EvalDuration/EvalString(code, env)`; the engine drives it with
    process variables. It has no consumer extension surface (no custom-function
    registration, no options).

## Decision

We will **reject the `pkg/` reorg** and instead move only the two non-public
packages under `internal/`:

- `database/` → `internal/database` (package name stays `database`).
- `expreval/` → `internal/expreval` (package name stays `expreval`).

All other root packages remain flat at the module root. This **reaffirms
ADR-0004**. A root `doc.go` provides a "start here" overview so consumers can
tell the ~5 entry-point packages from the supporting façades — solving the
"unclear entry point" pain with documentation rather than directory surgery.

## Consequences

- The public import surface drops from 16 to 14 packages; testcontainers no
  longer appears in a consumer's import graph.
- `internal/database` and `internal/expreval` remain importable by every
  in-module package, so all existing callers keep working after only their
  import paths are updated; package-qualified call sites are unchanged.
- ADR-0004's flat layout stands; future work must not re-introduce `pkg/`.
- `engine` and `authz` now import `internal/expreval`. This still satisfies the
  rule that engine/workflow code reaches the expression vendor only through the
  in-repo wrapper — the wrapper merely moved into `internal/`, which is more
  honest about it being an implementation detail.
```

- [ ] **Step 2: Verify it renders and the number is unused**

Run: `ls docs/adr/0041-* && head -1 docs/adr/0041-evict-internal-packages-from-public-root.md`
Expected: the file exists and the first line is `# 41. Evict test/internal-only packages from the public root; reaffirm flat layout`.

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0041-evict-internal-packages-from-public-root.md
git commit -m "docs(adr): reject pkg/ reorg, evict database+expreval to internal (ADR-0041)"
```

---

### Task 2: Move `database/` → `internal/database`

Pure refactor. `database` is package-`database` and imported only by `_test.go` files, so moving it changes import paths only.

**Files:**
- Move: `database/testutils.go` → `internal/database/testutils.go`
- Modify (import path only): the 36 `_test.go` files importing `github.com/kartaladev/wrkflw/database` (across `casbinauthz/`, `internal/authz/casbin/`, `internal/persistence/postgres/`, `persistence/`).

**Interfaces:**
- Consumes: ADR-0041 (rationale).
- Produces: package `database` at import path `github.com/kartaladev/wrkflw/internal/database`, exporting unchanged: `RunTestDatabase(t *testing.T, opts ...TestOption) *pgxpool.Pool`, `TestOption`, `WithDBName(string) TestOption`, `WithUser(string) TestOption`, `WithPassword(string) TestOption`.

- [ ] **Step 1: Confirm the suite builds before the move (baseline)**

Run: `go build ./... && go vet ./...`
Expected: exit 0, no output. (This is the "before" green state for the refactor.)

- [ ] **Step 2: Move the package directory with git**

Run:
```bash
mkdir -p internal/database
git mv database/testutils.go internal/database/testutils.go
rmdir database
```
Expected: `database/` no longer exists; `internal/database/testutils.go` exists. The package clause inside stays `package database` (do not edit it).

- [ ] **Step 3: Rewrite the import path in every importer**

Run:
```bash
grep -rl '"github.com/kartaladev/wrkflw/database"' --include="*.go" . \
  | xargs sed -i '' 's#github.com/kartaladev/wrkflw/database#github.com/kartaladev/wrkflw/internal/database#g'
```
(Note: `sed -i ''` is the macOS/BSD form. On GNU/Linux use `sed -i` without the `''`.)

- [ ] **Step 4: Verify no stale import path remains**

Run: `grep -rn '"github.com/kartaladev/wrkflw/database"' --include="*.go" . || echo CLEAN`
Expected: `CLEAN` (zero matches for the old path).

- [ ] **Step 5: Verify the suite still builds and vets (after)**

Run: `go build ./... && go vet ./...`
Expected: exit 0, no output. Imports resolve through the new `internal/database` path.

- [ ] **Step 6: Smoke-test one testcontainers consumer (requires Docker)**

Run: `go test ./internal/persistence/postgres/ -run '^TestMigrate' -count=1`
Expected: PASS (proves `database.RunTestDatabase` still resolves and runs from its new location). If Docker is unavailable, skip this step and note it in the task report.

- [ ] **Step 7: Lint**

Run: `golangci-lint run ./internal/database/... ./internal/persistence/... ./casbinauthz/... ./persistence/...`
Expected: no findings.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(database): move testcontainers helper to internal/database (ADR-0041)"
```

---

### Task 3: Move `expreval/` → `internal/expreval`

Pure refactor. `expreval` is package-`expreval`; 3 non-test importers plus its own black-box test. Moving changes import paths only.

**Files:**
- Move: `expreval/expreval.go` → `internal/expreval/expreval.go`; `expreval/expreval_test.go` → `internal/expreval/expreval_test.go`
- Modify (import path only): `engine/conditions.go`, `authz/authz.go`, `internal/authz/casbin/authorizer.go`, and `internal/expreval/expreval_test.go` (black-box, imports the package path).

**Interfaces:**
- Consumes: ADR-0041 (rationale).
- Produces: package `expreval` at import path `github.com/kartaladev/wrkflw/internal/expreval`, exporting unchanged: `Evaluator`, `New() *Evaluator`, and the `(*Evaluator).EvalBool/EvalDuration/EvalString(code string, env map[string]any)` methods.

- [ ] **Step 1: Move the package directory with git**

Run:
```bash
mkdir -p internal/expreval
git mv expreval/expreval.go internal/expreval/expreval.go
git mv expreval/expreval_test.go internal/expreval/expreval_test.go
rmdir expreval
```
Expected: `expreval/` no longer exists; both files now under `internal/expreval/`. Package clauses stay `package expreval` / `package expreval_test` (do not edit them).

- [ ] **Step 2: Rewrite the import path in every importer**

Run:
```bash
grep -rl '"github.com/kartaladev/wrkflw/expreval"' --include="*.go" . \
  | xargs sed -i '' 's#github.com/kartaladev/wrkflw/expreval#github.com/kartaladev/wrkflw/internal/expreval#g'
```
(GNU/Linux: drop the `''` after `-i`.)

- [ ] **Step 3: Verify no stale import path remains**

Run: `grep -rn '"github.com/kartaladev/wrkflw/expreval"' --include="*.go" . || echo CLEAN`
Expected: `CLEAN`.

- [ ] **Step 4: Verify build + vet**

Run: `go build ./... && go vet ./...`
Expected: exit 0, no output.

- [ ] **Step 5: Run the affected packages' tests (no Docker needed)**

Run: `go test ./internal/expreval/... ./engine/... ./authz/... -count=1`
Expected: PASS. These exercise the wrapper directly (`internal/expreval`) and its two production consumers (`engine/conditions.go`, `authz/authz.go`).

- [ ] **Step 6: Lint**

Run: `golangci-lint run ./internal/expreval/... ./engine/... ./authz/... ./internal/authz/...`
Expected: no findings.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(expreval): move expr-lang wrapper to internal/expreval (ADR-0041)"
```

---

### Task 4: Add the root front-door doc (`doc.go`)

A doc-only package at the module root whose package comment is the "start here" map. Lists the 14 public packages grouped by purpose, so a consumer can tell the entry points from the supporting façades. No exported symbols, no test.

**Files:**
- Create: `doc.go`

**Interfaces:**
- Consumes: the final public package set after Tasks 2–3 (14 packages: `action`, `authz`, `casbinauthz`, `clock`, `engine`, `eventing`, `humantask`, `model`, `observability`, `persistence`, `runtime`, `scheduling`, `service`, `transport`).
- Produces: package `wrkflw` (doc-only) at the module root.

- [ ] **Step 1: Write `doc.go`**

The root package is named `wrkflw` (the product name used throughout the codebase, e.g. the `wrkflw_definitions` table). It is intentionally doc-only — it exports nothing and exists so `go doc github.com/kartaladev/wrkflw` and pkg.go.dev show a navigation landing. Create `doc.go` with exactly:

```go
// Package wrkflw is the documentation landing for the wrkflw workflow engine —
// an importable Go library (not a daemon) that a consumer embeds in their own
// application. This package exports nothing; it exists only as a "start here"
// map of the public packages. See CLAUDE.md for the project intent and the
// locked architectural properties.
//
// # Start here
//
//   - model        Define a process: nodes, gateways, sequence flows, the
//                  ProcessDefinition template. Pure data plus validation.
//   - runtime      Run a process: the reference driver that performs engine
//                  commands and resolves definitions.
//   - engine       The core token state machine. Pure of transport, storage
//                  vendor, and event-bus specifics; depends on interfaces only.
//
// # Activities and people
//
//   - action       The service-action catalog: named, interface-based actions
//                  referenced from definition nodes.
//   - humantask    Human-task model and the ports that drive human work.
//
// # Authorization
//
//   - authz        The pluggable Authorizer abstraction (role, resource, and
//                  attribute-based) evaluated at human-task nodes.
//   - casbinauthz  The consumer-facing façade for the casbin-backed authorizer.
//
// # Expose it (mount in your server)
//
//   - transport    REST http.Handler factories and gRPC service registrations
//                  a consumer mounts in their own server (transport/rest,
//                  transport/grpc).
//
// # Supporting ports and façades
//
//   - persistence  The persistence façade over the SQL/Postgres store.
//   - eventing     The eventing façade for publishing domain events (outbox).
//   - scheduling   The façade over the timer/SLA scheduler.
//   - observability Metrics, traces, and slog wiring at the runtime boundary.
//   - clock        The clock.Clock time abstraction; supply a fake in tests.
//   - service      The service facade and error classification.
//
// Implementation details a consumer must not import live under internal/.
package wrkflw
```

- [ ] **Step 2: Verify it builds and `go doc` renders the map**

Run: `go build ./... && go doc .`
Expected: build exit 0; `go doc .` prints the "Start here" overview.

- [ ] **Step 3: Lint**

Run: `golangci-lint run .`
Expected: no findings (the package has a doc comment beginning "Package wrkflw", satisfying the package-comment rule).

- [ ] **Step 4: Final full-suite gate (no regressions elsewhere)**

Run: `go build ./... && go vet ./...`
Expected: exit 0. (A full `go test ./...` requires Docker for testcontainers packages; run it if Docker is available, otherwise rely on Tasks 2–3's targeted runs and note the deferral.)

- [ ] **Step 5: Commit**

```bash
git add doc.go
git commit -m "docs(root): add front-door package overview (ADR-0041)"
```

---

## Verification checklist (whole sub-project)

- [ ] `database/` and `expreval/` no longer exist at the module root; they live under `internal/`.
- [ ] `grep -rn 'wrkflw/database"' --include=*.go .` and the same for `/expreval"` return zero matches (only the `internal/...` paths remain).
- [ ] `go build ./...` and `go vet ./...` are clean.
- [ ] `golangci-lint run ./...` is clean.
- [ ] Targeted tests pass: `./internal/expreval/...`, `./engine/...`, `./authz/...` (no Docker), and at least one testcontainers package (`./internal/persistence/postgres/ -run '^TestMigrate'`) if Docker is available.
- [ ] `go doc .` prints the front-door map; the 14 listed packages match the actual public root packages.
- [ ] ADR-0041 exists and is referenced by the move commits.
- [ ] The public root package count is 14 (down from 16).

## Out of scope (handled by later sub-projects)

- README (sub-project 4) — `doc.go` is the in-code front door; the README is separate.
- Any `model.Node` / DSL / YAML work (sub-project 2).
- Instance serialization DTO (sub-project 3).
- BPMN-wording sweep (sub-project 4).
- Renaming `engine` (dropped, ADR/spec).
