# Sensible-default `ProcessDriver` construction — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `runtime.NewProcessDriver()` usable with zero arguments (in-memory store + package-global action catalog), add a package-global `action` registry, emit a DEBUG construction summary, and replace generic/misleading exported names with self-explaining ones.

**Architecture:** Two positional deps become functional options with in-memory defaults applied before the option loop. The `action` package grows a process-global default `*Registry`. A one-shot DEBUG log reports what got wired. All renames are behaviour-preserving, symbol-scoped via `gopls rename`.

**Tech Stack:** Go 1.25+ (installed 1.26.4), `log/slog`, `gopls` (semantic rename), `gofmt`.

Spec: `docs/specs/2026-07-05-sensible-default-driver-construction-design.md`

## Global Constraints

- **Go 1.25+**, single module `github.com/zakyalvan/krtlwrkflw`.
- **TDD strict** (CLAUDE.md): no production code before a failing test; every new symbol shows a red state. Pure renames are the sole exception — they carry no new behaviour, so "the existing suite stays green before and after" is the test.
- **Renames are symbol-scoped** via `gopls rename` (installed to `$(go env GOPATH)/bin/gopls`). NEVER text-global sed — `engine.Token`, `internal/persistence/store.Store`, and `internal/eventing/watermill.Publisher` must NOT be touched.
- **Error sentinel prefix**: `workflow-<pkg>: …` (existing convention; no new sentinels here).
- **Black-box tests** preferred (`package foo_test`); table tests follow the project `table-test` skill (`assert` closure form, `t.Context()`).
- **Gates:** `go build ./...`, `go test -race ./...`, touched-pkg coverage ≥ 85%, `golangci-lint run ./...` clean.
- **Commits:** Conventional Commits, scoped. End messages with the two trailer lines (Co-Authored-By + Claude-Session) per repo Git Discipline.
- **No `pkg/` prefix; no `superpowers` in any path.**

## Rename mechanics (used by Tasks 1–6)

`gopls rename` rewrites a symbol and all its references module-wide, safely
disambiguating same-named symbols in other packages. Usage:

```bash
GOPLS=$(go env GOPATH)/bin/gopls
# position = the identifier in its DECLARATION (line:col, 1-based col at the name)
$GOPLS rename -w runtime/kernel/ports.go:75:6 InstanceStore
gofmt -w ./...            # normalise (gopls already formats edited files)
go build ./...            # MUST stay green
```

Declaration positions (verify with `grep -n` before renaming; col is the 1-based
column of the identifier itself):

| Symbol | Declaration | New name |
|---|---|---|
| `kernel.Store` | `runtime/kernel/ports.go` `type Store` | `InstanceStore` |
| `kernel.MemStore` | `runtime/kernel/memstore.go` `type MemStore` | `MemInstanceStore` |
| `kernel.NewMemStore` | `runtime/kernel/memstore.go` `func NewMemStore` | `NewMemInstanceStore` |
| `kernel.MemStoreOption` | `runtime/kernel/memstore.go` `type MemStoreOption` | `MemInstanceStoreOption` |
| `kernel.CachingStore` | `runtime/kernel/caching_store.go` `type CachingStore` | `CachingInstanceStore` |
| `kernel.NewCachingStore` | `runtime/kernel/caching_store.go` `func NewCachingStore` | `NewCachingInstanceStore` |
| `kernel.CachingStoreOption` | `runtime/kernel/caching_store.go` `type CachingStoreOption` | `CachingInstanceStoreOption` |
| `persistence.Store` | `persistence/persistence.go` `type Store interface` | `InstanceStore` |
| `kernel.Token` | `runtime/kernel/ports.go` `type Token` | `Version` |
| `kernel.Outcome` | `runtime/kernel/chainlink.go` `type Outcome` | `ChainOutcome` |
| `kernel.Ownership` | `runtime/kernel/ownership.go` `type Ownership` | `InstanceOwnership` |
| `kernel.Publisher` | `runtime/kernel/publisher.go` `type Publisher` | `OutboxPublisher` |
| `persistence.Publisher` (alias) | `persistence/persistence.go` `type Publisher =` | `OutboxPublisher` |
| `action.Retryabler` | `action/retry.go` `type Retryabler` | `RetryableError` |

If `gopls rename` reports "conflicting names" or cannot proceed, STOP and report —
do not fall back to text sed for that symbol.

---

## File Structure

- `action/default.go` — **new** — package-global default `*Registry` + `Register`/`RegisterFunc`/`MustRegister`/`MustRegisterFunc`/`DefaultCatalog`.
- `action/default_test.go` — **new** — black-box tests for the above.
- `runtime/processdriver.go` — **modify** — constructor becomes `NewProcessDriver(opts ...Option)`; defaults applied pre-loop; DEBUG summary.
- `runtime/processdriver_options.go` — **modify** — add `WithActionCatalog`, `WithInstanceStore`.
- `runtime/processdriver_defaults_test.go` — **new** — zero-arg defaults + option overrides + nil-ignore + DEBUG summary tests.
- All `NewProcessDriver(cat, store, …)` call sites (46 files, examples/tests/façade) — **modify** — migrate to options.
- `docs/adr/0096-sensible-default-driver-construction.md` — **new** — Nygard ADR.
- Renamed symbols across `runtime/kernel`, `runtime`, `internal/persistence/store`, `persistence`, `action`, `examples`, `docs` — **modify** (mechanical, gopls-driven).

---

## Task 1: Store-family rename → `InstanceStore`

**Files:** `runtime/kernel/{ports,memstore,caching_store}.go` + all references (gopls handles refs); `persistence/persistence.go` and refs.

**Interfaces:**
- Produces: `kernel.InstanceStore`, `kernel.MemInstanceStore`, `kernel.NewMemInstanceStore`, `kernel.MemInstanceStoreOption`, `kernel.CachingInstanceStore`, `kernel.NewCachingInstanceStore`, `kernel.CachingInstanceStoreOption`, `persistence.InstanceStore`.

- [ ] **Step 1: Baseline green.** `go build ./... && go test ./runtime/... ./persistence/... 2>&1 | tail -5` — confirm green before touching anything.
- [ ] **Step 2: Rename each Store-family symbol** with `gopls rename -w <decl> <NewName>` in this order: `Store`→`InstanceStore`, `MemStore`→`MemInstanceStore`, `NewMemStore`→`NewMemInstanceStore`, `MemStoreOption`→`MemInstanceStoreOption`, `CachingStore`→`CachingInstanceStore`, `NewCachingStore`→`NewCachingInstanceStore`, `CachingStoreOption`→`CachingInstanceStoreOption`, then `persistence.Store`→`InstanceStore`. Re-`grep -n` each declaration's line:col immediately before renaming it (line numbers shift after each rename).
- [ ] **Step 3: Verify build.** `gofmt -w . && go build ./...` — Expected: clean. `internal/persistence/store.Store` (the struct) must be UNCHANGED — verify: `grep -rn "type Store struct" internal/persistence/store/` still present.
- [ ] **Step 4: Verify tests.** `go test ./runtime/... ./persistence/... ./action/... 2>&1 | tail -5` — Expected: PASS (pure rename).
- [ ] **Step 5: Commit.**
```bash
git add -A && git commit -m "refactor(kernel): rename Store family to InstanceStore

kernel.Store -> InstanceStore and the MemStore/CachingStore family
(+ ctors/options); persistence.Store facade -> InstanceStore. Symbol-scoped
via gopls; internal/persistence/store.Store struct unchanged. Behaviour-preserving.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf"
```

---

## Task 2: `kernel.Token` → `kernel.Version`

**Files:** `runtime/kernel/ports.go` `type Token` + refs.

**Interfaces:**
- Produces: `kernel.Version` (was `kernel.Token`), used in `InstanceStore.Load`/`Commit` and `ProcessDriver` internals.

- [ ] **Step 1:** Re-grep `grep -n "type Token" runtime/kernel/ports.go` for the current line; note the col of `Token`.
- [ ] **Step 2:** `GOPLS rename -w runtime/kernel/ports.go:<line>:<col> Version`.
- [ ] **Step 3: Verify `engine.Token` untouched.** `grep -rn "type Token struct" engine/state.go` — MUST still exist. `grep -rn "kernel.Token" .` — MUST return nothing.
- [ ] **Step 4:** `gofmt -w . && go build ./... && go test ./runtime/... ./persistence/... 2>&1 | tail -5` — Expected: PASS.
- [ ] **Step 5: Commit** `refactor(kernel): rename optimistic-concurrency Token to Version` (+ trailers). Body: notes the collision with engine.Token (the BPMN execution token) that motivated it.

---

## Task 3: `kernel.Outcome` → `kernel.ChainOutcome`

**Files:** `runtime/kernel/chainlink.go` `type Outcome` + refs.

- [ ] **Step 1:** `grep -n "type Outcome" runtime/kernel/chainlink.go` for line:col.
- [ ] **Step 2:** `GOPLS rename -w runtime/kernel/chainlink.go:<line>:<col> ChainOutcome`.
- [ ] **Step 3:** `gofmt -w . && go build ./... && go test ./runtime/... 2>&1 | tail -5` — Expected: PASS.
- [ ] **Step 4: Commit** `refactor(kernel): rename chain-link Outcome to ChainOutcome` (+ trailers).

---

## Task 4: `kernel.Ownership` → `kernel.InstanceOwnership`

**Files:** `runtime/kernel/ownership.go` `type Ownership` + refs (incl. `persistence` return types / assertions).

- [ ] **Step 1:** `grep -n "type Ownership interface" runtime/kernel/ownership.go` for line:col.
- [ ] **Step 2:** `GOPLS rename -w runtime/kernel/ownership.go:<line>:<col> InstanceOwnership`. (`AlwaysOwn` keeps its name.)
- [ ] **Step 3:** `gofmt -w . && go build ./... && go test ./runtime/... ./persistence/... 2>&1 | tail -5` — Expected: PASS. Verify `grep -rn "kernel.Ownership" .` returns nothing.
- [ ] **Step 4: Commit** `refactor(kernel): rename Ownership to InstanceOwnership` (+ trailers).

---

## Task 5: `kernel.Publisher` → `kernel.OutboxPublisher`

**Files:** `runtime/kernel/publisher.go` `type Publisher` + refs; `persistence/persistence.go` alias `type Publisher = kernel.Publisher`.

- [ ] **Step 1:** `grep -n "type Publisher interface" runtime/kernel/publisher.go` for line:col.
- [ ] **Step 2:** `GOPLS rename -w runtime/kernel/publisher.go:<line>:<col> OutboxPublisher`. gopls updates the `persistence` alias's RHS automatically; then rename the alias LHS: `grep -n "type Publisher =" persistence/persistence.go`, `GOPLS rename -w persistence/persistence.go:<line>:<col> OutboxPublisher`.
- [ ] **Step 3: Verify `watermill.Publisher` untouched.** `grep -rn "type Publisher struct" internal/eventing/watermill/` — MUST still exist. `grep -rn "kernel.Publisher" .` — MUST return nothing.
- [ ] **Step 4:** `gofmt -w . && go build ./... && go test ./runtime/... ./persistence/... ./eventing/... 2>&1 | tail -5` — Expected: PASS.
- [ ] **Step 5: Commit** `refactor(kernel): rename Publisher to OutboxPublisher` (+ trailers).

---

## Task 6: `action.Retryabler` → `action.RetryableError`

**Files:** `action/retry.go` `type Retryabler` + refs.

- [ ] **Step 1:** `grep -n "type Retryabler" action/retry.go` for line:col.
- [ ] **Step 2:** `GOPLS rename -w action/retry.go:<line>:<col> RetryableError`.
- [ ] **Step 3:** `gofmt -w . && go build ./... && go test ./action/... ./runtime/... 2>&1 | tail -5` — Expected: PASS.
- [ ] **Step 4: Commit** `refactor(action): rename Retryabler to RetryableError` (+ trailers).

---

## Task 7: `action` package-global default catalog

**Files:**
- Create: `action/default.go`
- Test: `action/default_test.go`

**Interfaces:**
- Produces: `action.DefaultCatalog() *Registry`, `action.Register(name string, a Action) error`, `action.RegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) error`, `action.MustRegister(name string, a Action)`, `action.MustRegisterFunc(name string, fn …)`.
- Consumes: existing `action.NewRegistry`, `*Registry` methods.

- [ ] **Step 1: Write the failing test** `action/default_test.go`:
```go
package action_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
)

func TestDefaultCatalog_RegisterAndResolve(t *testing.T) {
	t.Parallel()
	const name = "test-default-catalog-register" // unique: global registry, no reset
	if err := action.Register(name, action.ActionFunc(
		func(ctx context.Context, in map[string]any) (map[string]any, error) { return in, nil },
	)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := action.DefaultCatalog().Resolve(name)
	if !ok || got == nil {
		t.Fatalf("Resolve(%q) after Register = (%v,%v), want hit", name, got, ok)
	}
}

func TestDefaultCatalog_RegisterFuncNil(t *testing.T) {
	t.Parallel()
	if err := action.RegisterFunc("x-nil-fn", nil); !errors.Is(err, action.ErrNilAction) {
		t.Fatalf("RegisterFunc(nil) = %v, want ErrNilAction", err)
	}
}

func TestDefaultCatalog_Identity(t *testing.T) {
	t.Parallel()
	if action.DefaultCatalog() != action.DefaultCatalog() {
		t.Fatal("DefaultCatalog() must return the same process-global registry")
	}
}
```
- [ ] **Step 2: Run — verify FAIL.** `go test ./action/ -run TestDefaultCatalog 2>&1 | tail` — Expected: build failure (`undefined: action.Register` / `action.DefaultCatalog`).
- [ ] **Step 3: Implement** `action/default.go`:
```go
package action

import "context"

// defaultCatalog is the process-global action catalog used by a ProcessDriver
// constructed without runtime.WithActionCatalog. It is concurrency-safe (it is a
// *Registry, guarded by an RWMutex). Populate it from application init code via
// Register / MustRegister; a zero-config driver resolves service-action names
// against it.
//
// Because it is process-global and (like any Registry) rejects duplicate names,
// tests that need isolation should construct their own registry with NewRegistry
// and pass it via runtime.WithActionCatalog rather than registering here.
var defaultCatalog = NewRegistry()

// DefaultCatalog returns the process-global action registry.
func DefaultCatalog() *Registry { return defaultCatalog }

// Register adds a to the process-global catalog under name. See Registry.Register
// for the returned errors (ErrEmptyActionName, ErrNilAction, ErrActionExists).
func Register(name string, a Action) error { return defaultCatalog.Register(name, a) }

// RegisterFunc wraps fn as an ActionFunc and registers it in the process-global
// catalog. A nil fn is rejected with ErrNilAction.
func RegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) error {
	return defaultCatalog.RegisterFunc(name, fn)
}

// MustRegister calls Register and panics on error (init-time wiring).
func MustRegister(name string, a Action) { defaultCatalog.MustRegister(name, a) }

// MustRegisterFunc calls RegisterFunc and panics on error (init-time wiring).
func MustRegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) {
	defaultCatalog.MustRegisterFunc(name, fn)
}
```
- [ ] **Step 4: Run — verify PASS.** `go test ./action/ -run TestDefaultCatalog -race 2>&1 | tail` — Expected: PASS.
- [ ] **Step 5: Add a testable Example** in `action/default_test.go` (godoc for consumers):
```go
func ExampleRegister() {
	_ = action.Register("send-welcome-email", action.ActionFunc(
		func(ctx context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"sent": true}, nil
		},
	))
	_, ok := action.DefaultCatalog().Resolve("send-welcome-email")
	fmt.Println(ok)
	// Output: true
}
```
(add `"fmt"` import). Run `go test ./action/ 2>&1 | tail` — Expected: PASS.
- [ ] **Step 6: Commit** `feat(action): add process-global default catalog + Register helpers` (+ trailers).

---

## Task 8: All-optional `NewProcessDriver` + `WithInstanceStore` / `WithActionCatalog`

**Files:**
- Modify: `runtime/processdriver.go` (constructor), `runtime/processdriver_options.go` (two new options)
- Test: `runtime/processdriver_defaults_test.go` (new)

**Interfaces:**
- Consumes: `action.DefaultCatalog` (Task 7), `kernel.NewMemInstanceStore` (Task 1), `kernel.InstanceStore` (Task 1).
- Produces: `runtime.NewProcessDriver(opts ...Option) (*ProcessDriver, error)`, `runtime.WithInstanceStore(store kernel.InstanceStore) Option`, `runtime.WithActionCatalog(cat action.Catalog) Option`.

- [ ] **Step 1: Write failing tests** `runtime/processdriver_defaults_test.go` (black-box). Cover: (a) zero-arg constructs a usable driver whose default catalog is `action.DefaultCatalog()` — register a unique action, build `NewProcessDriver()`, run a one-node service-task definition, assert the action ran; (b) `WithActionCatalog(custom)` overrides; (c) `WithInstanceStore(custom)` overrides (use a `kernel.NewMemInstanceStore()` and assert instance is retrievable via `Load`); (d) `WithActionCatalog(nil)` / `WithInstanceStore(nil)` are ignored (defaults stand — driver still constructs and runs). Use `t.Context()` and the `table-test` closure form where cases share a call shape. (Reference an existing runtime test e.g. `runtime/runner_test.go` for the minimal definition + drive helpers, and `runtime/internal/runtimetest` constructors.)
- [ ] **Step 2: Run — verify FAIL.** `go test ./runtime/ -run 'Default|WithInstanceStore|WithActionCatalog' 2>&1 | tail` — Expected: build failure (`too many arguments` / `undefined: WithInstanceStore`).
- [ ] **Step 3: Implement.** In `processdriver_options.go` add:
```go
// WithActionCatalog sets the service-action catalog. A nil cat is ignored, so
// the process-global action.DefaultCatalog() registry remains in effect.
func WithActionCatalog(cat action.Catalog) Option {
	return func(r *ProcessDriver) {
		if cat != nil {
			r.cat = cat
		}
	}
}

// WithInstanceStore sets the transactional instance store. A nil store is
// ignored, so the default in-memory MemInstanceStore remains in effect.
func WithInstanceStore(store kernel.InstanceStore) Option {
	return func(r *ProcessDriver) {
		if store != nil {
			r.store = store
		}
	}
}
```
In `processdriver.go` replace the constructor signature/body:
```go
func NewProcessDriver(opts ...Option) (*ProcessDriver, error) {
	memStore, err := kernel.NewMemInstanceStore()
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: default instance store: %w", err)
	}
	r := &ProcessDriver{
		cat:           action.DefaultCatalog(),
		clk:           clock.System(),
		store:         memStore,
		jitter:        kernel.NewJitterSource(),
		actionTimeout: defaultActionTimeout,
		msgWaiters:    make(map[msgKey]string),
	}
	for _, o := range opts {
		o(r)
	}
	r.obs = newDriverObs(r.logOpt, r.tpOpt, r.mpOpt)
	return r, nil
}
```
Update the constructor doc comment: no positionals; describe the in-memory + default-catalog defaults and the `WithInstanceStore`/`WithActionCatalog` overrides. Update the `ProcessDriver.store` field type doc if it references the old name.
- [ ] **Step 4: Run — verify PASS.** `go test ./runtime/ -run 'Default|WithInstanceStore|WithActionCatalog' -race 2>&1 | tail` — Expected: PASS. (Other `runtime` tests still fail to compile until Task 9 migrates call sites — that's expected; scope this run to the new tests.)
- [ ] **Step 5: Commit** `feat(runtime): all-optional NewProcessDriver with in-memory defaults` (+ trailers).

---

## Task 9: Migrate all `NewProcessDriver` call sites to options

**Files (46):** every file from the call-site inventory — `examples/**/main.go`, `examples/scenarios/**/main.go`, `internal/transporttest/harness.go`, `runtime/internal/runtimetest/constructors.go`, `processtest/harness.go`, `persistence/{persistence,mysql,sqlite}.go`, and all `*_test.go` listed in the plan header inventory.

**Interfaces:**
- Consumes: `WithActionCatalog`, `WithInstanceStore` (Task 8).

- [ ] **Step 1: Find every call.** `grep -rn "NewProcessDriver(" --include="*.go" . | grep -v "processdriver.go:104"`.
- [ ] **Step 2: Mechanical migration.** For each `NewProcessDriver(cat, store, opts...)` rewrite to `NewProcessDriver(append([]runtime.Option{runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store)}, opts...)...)` — or, more idiomatically, inline the two options first: `NewProcessDriver(WithActionCatalog(cat), WithInstanceStore(store), <existing opts…>)`. Prefer the inline form. Where the old first arg was `action.NewMapCatalog(nil)` (no-service-task processes), DROP it entirely (the default catalog covers it) unless the test asserts on a specific catalog. Where the store was the positional, wrap in `WithInstanceStore(store)`.
- [ ] **Step 3: Build.** `gofmt -w . && go build ./...` — Expected: clean.
- [ ] **Step 4: Full test.** `go test ./... 2>&1 | tail -20` — Expected: PASS (this is where the whole suite comes back green).
- [ ] **Step 5: Commit** `refactor: migrate NewProcessDriver call sites to functional options` (+ trailers).

---

## Task 10: DEBUG construction summary

**Files:**
- Modify: `runtime/processdriver.go` (emit after `r.obs` built)
- Test: extend `runtime/processdriver_defaults_test.go`

**Interfaces:**
- Consumes: `r.obs.tel.Logger` (existing `*slog.Logger`), the populated driver fields.

- [ ] **Step 1: Write failing test.** Add a test that installs a capturing `slog.Handler` (records `slog.Record`s at `LevelDebug`) via `WithLogger(slog.New(handler))`, constructs a zero-config driver, and asserts: one record with message `"ProcessDriver constructed"` at `LevelDebug`, carrying attrs `store=in-memory(non-durable)`, `catalog=default-global`, and feature flags `humanTasks=off`/`scheduler=off`/etc. Then a second driver with `WithScheduler(...)` + a custom store asserts `store=custom`, `scheduler=on`. (Handler must set `Level: slog.LevelDebug`.)
- [ ] **Step 2: Run — verify FAIL.** `go test ./runtime/ -run Summary 2>&1 | tail` — Expected: FAIL (no such record).
- [ ] **Step 3: Implement.** Add an unexported helper `func (r *ProcessDriver) logConstructionSummary(ctx context.Context)` (or inline) called at the end of `NewProcessDriver` after `r.obs` is set. Build via `r.obs.tel.Logger.LogAttrs(context.Background(), slog.LevelDebug, "ProcessDriver constructed", …)` with attrs derived from struct fields:
  - `store`: `"custom"` if the store differs from the auto-created MemInstanceStore else `"in-memory(non-durable)"` — track with a bool flag set when `WithInstanceStore` overrode it. Simplest: record whether any override happened by comparing after the loop, OR set a private `storeIsDefault`/`catalogIsDefault` bool inside the option. Prefer: capture the default sentinels before the loop, compare identity after.
  - `catalog`: `"default-global"` if `r.cat == action.DefaultCatalog()` else `"custom"`.
  - flags: `humanTasks` = `r.tasks != nil`, `scheduler` = `r.sched != nil`, `signalBus` = `r.sigbus != nil`, `definitions` = `r.defsReg != nil`, `callLinks` = `r.callLinks != nil`, `timerStore` = `r.timerStore != nil`; each rendered `"on"`/`"off"`.
  - `actionTimeout` = `r.actionTimeout.String()`; `retryDefault` = `r.defaultRetryPolicy != nil`; `exprTimeout` = `r.conditionEval != nil`.
  - `hint` = `"in-memory store is not durable; for production wire persistence.OpenPostgres/OpenMySQL/OpenSQLite + runtime.WithInstanceStore, and enable WithScheduler/WithTimerStore/WithCallLinkStore as needed"`.
- [ ] **Step 4: Run — verify PASS.** `go test ./runtime/ -run Summary -race 2>&1 | tail` — Expected: PASS.
- [ ] **Step 5: Full regression.** `go test ./runtime/... 2>&1 | tail` — Expected: PASS.
- [ ] **Step 6: Commit** `feat(runtime): DEBUG construction summary + production-readiness hint` (+ trailers).

---

## Task 11: ADR-0096 (parallelizable — docs only, no code deps)

**Files:** Create `docs/adr/0096-sensible-default-driver-construction.md`.

- [ ] **Step 1:** Read `docs/adr/0001-record-architecture-decisions.md` (canonical Nygard template) and `docs/adr/0083-*.md` (fail-fast constructors) for cross-reference.
- [ ] **Step 2: Write the ADR** (Nygard: Status/Date, Context, Decision, Consequences). Content:
  - **Status:** Accepted, 2026-07-05.
  - **Context:** `NewProcessDriver` required two positionals (`cat`, `store`) with fail-fast nil guards (ADR-0083), creating friction for the zero-config path; the `Store` port name and several others (`Token`, `Outcome`, `Ownership`, `Publisher`, `Retryabler`) were generic or collided conceptually (notably `kernel.Token` vs the BPMN execution token `engine.Token`).
  - **Decision:** (1) `NewProcessDriver(opts ...Option)` with in-memory defaults (MemInstanceStore + `action.DefaultCatalog()`), overridable via `WithInstanceStore`/`WithActionCatalog`; this narrows ADR-0083's fail-fast rule for the driver's `cat`/`store` only. (2) A process-global default action registry with `action.Register`/`DefaultCatalog`. (3) A DEBUG construction summary. (4) The D4 renames (list them). Note the deferred `WithDurableStore` and why (would pull DB drivers into every `runtime` consumer).
  - **Consequences:** ergonomic zero-config path; clearer names; breaking (call sites migrated); global-catalog test-isolation caveat; `runtime` stays free of persistence/DB-driver imports.
- [ ] **Step 3: Commit** `docs(adr): 0096 sensible-default ProcessDriver construction + naming audit` (+ trailers).

---

## Task 12: Final verification (orchestrator)

- [ ] **Step 1:** `go build ./...` — clean.
- [ ] **Step 2:** `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` — all green; spot-check touched-pkg coverage ≥ 85% (`action`, `runtime`): `go tool cover -func=cover.out | grep -E "action/|runtime/" | tail`.
- [ ] **Step 3:** `golangci-lint run ./...` — clean (fix any findings in touched files).
- [ ] **Step 4: Docs sweep.** `grep -rn "kernel.Store\b\|kernel.Token\b\|kernel.Ownership\b\|kernel.Publisher\b\|NewMemStore\|NewCachingStore\|action.Default()\b\|Retryabler" --include="*.go" --include="*.md" . | grep -v docs/adr` — Expected: no stale references outside historical ADRs. Update `runtime/README.md`, `action/README.md`, `persistence` godoc, and example comments as needed; commit `docs: refresh for InstanceStore/Version renames + zero-config driver`.
- [ ] **Step 5: Update spec status** to "Implemented" in `docs/specs/2026-07-05-sensible-default-driver-construction-design.md`; commit.

---

## Verification checklist (maps to spec)

- [ ] Zero-arg `NewProcessDriver()` runs a definition (Task 8).
- [ ] `WithActionCatalog` / `WithInstanceStore` override + nil-ignore (Task 8).
- [ ] `action.DefaultCatalog` + `Register`/friends (Task 7).
- [ ] DEBUG summary emitted + asserted (Task 10).
- [ ] All D4 renames complete, `engine.Token` / `store.Store` / `watermill.Publisher` untouched (Tasks 1–6).
- [ ] All call sites migrated; no positional `NewProcessDriver` (Task 9).
- [ ] ADR-0096 written (Task 11).
- [ ] `go build`, `go test -race`, coverage ≥ 85% touched, `golangci-lint` clean (Task 12).
