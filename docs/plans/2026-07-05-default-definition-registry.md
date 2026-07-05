# Default DefinitionRegistry — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use `- [ ]`.

**Goal:** A process-global default `DefinitionRegistry` used by `NewProcessDriver` automatically, populated via `runtime.RegisterDefinition(def)` — mirroring `action.DefaultCatalog`/`action.Register`.

**Architecture:** New mutable registry type in `runtime/kernel`; process-global default + ergonomic API in `runtime`; driver defaults `defsReg` to it; `WithDefinitions` nil-guarded.

Spec: `docs/specs/2026-07-05-default-definition-registry-design.md` (read it — it has the full API, sentinels, ripple analysis, and test list).

## Global Constraints

- Go 1.25+, module `github.com/zakyalvan/krtlwrkflw`. TDD strict (failing test first, observe red). Black-box tests (`_test` package), `t.Context()`, project table-test `assert`-closure style.
- Error sentinels `workflow-runtime:`-prefixed. Coverage ≥85% touched (kernel, runtime). `go build`, `go test -race`, `golangci-lint` clean.
- Commits: Conventional Commits, ending with:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` / `Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf`.
- No `pkg/`; no `superpowers` in paths.

---

## Task 1: `kernel.MemDefinitionRegistry` (mutable, concurrency-safe)

**Files:** Create `runtime/kernel/mem_definition_registry.go` + `runtime/kernel/mem_definition_registry_test.go`.

**Interfaces produced:** `kernel.NewMemDefinitionRegistry() *MemDefinitionRegistry`; methods `Register(def *model.ProcessDefinition) error`, `MustRegister(def *model.ProcessDefinition)`, `Lookup(ctx, defRef string) (*model.ProcessDefinition, error)` (satisfies `kernel.DefinitionRegistry`). Sentinels `ErrNilDefinition`, `ErrEmptyDefinitionID`, `ErrDefinitionExists`.

Behaviour (per spec D1): `Register` indexes under both `def.ID` and `fmt.Sprintf("%s:%d", def.ID, def.Version)`. Nil def → `ErrNilDefinition`; empty `def.ID` → `ErrEmptyDefinitionID`; duplicate exact `<ID>:<Version>` → `ErrDefinitionExists` (wrapped with the key). The bare `<ID>` key is overwritten to the latest registered version (documented). `Lookup` returns `(nil, ErrDefinitionNotFound)` (the existing kernel sentinel) on miss. Concurrency-safe via `sync.RWMutex`; add a `noCopy` per the `action.Registry` pattern if that pattern is used in kernel, else document "do not copy".

- [ ] **Step 1: Write failing tests** (`package kernel_test`): (a) register a def (ID "sub", Version 2), Lookup "sub" and "sub:2" both return it; (b) nil def → `ErrNilDefinition`; (c) empty ID → `ErrEmptyDefinitionID`; (d) duplicate "sub:2" → `errors.Is(err, ErrDefinitionExists)`; (e) register "sub" v1 then v2 → Lookup "sub" returns v2 (latest), "sub:1" still returns v1; (f) Lookup miss → `errors.Is(err, ErrDefinitionNotFound)`; (g) a `-race` concurrent Register/Lookup goroutine test. Build a `*model.ProcessDefinition` via the existing test helpers/builder (check `runtime/kernel/*_test.go` and `definition` package for how definitions are constructed in kernel tests — reuse that; a minimal `&model.ProcessDefinition{ID: "sub", Version: 2}` is acceptable if the struct fields are exported).
- [ ] **Step 2: Run — verify RED.** `go test ./runtime/kernel/ -run MemDefinitionRegistry 2>&1 | tail` — build failure (undefined).
- [ ] **Step 3: Implement** `mem_definition_registry.go` per spec D1.
- [ ] **Step 4: Run — verify GREEN.** `go test ./runtime/kernel/ -run MemDefinitionRegistry -race 2>&1 | tail`.
- [ ] **Step 5:** `gofmt -w runtime/kernel/ && go build ./... && go vet ./runtime/kernel/`.
- [ ] **Step 6: Commit** `feat(kernel): add mutable MemDefinitionRegistry`.

---

## Task 2: `runtime` default registry + API + driver wiring

**Files:** Create `runtime/definition_registry.go` (global + `RegisterDefinition`/`MustRegisterDefinition`/`DefaultDefinitionRegistry`) + tests in `runtime/definition_registry_test.go`. Modify `runtime/processdriver.go` (default `defsReg`, DEBUG `definitions` label, StartSubInstance error reword) and `runtime/processdriver_options.go` (`WithDefinitions` nil-guard).

**Interfaces consumed:** `kernel.NewMemDefinitionRegistry`, `kernel.MemDefinitionRegistry`, `model.ProcessDefinition`.
**Interfaces produced:** `runtime.RegisterDefinition(def *model.ProcessDefinition) error`, `runtime.MustRegisterDefinition(def *model.ProcessDefinition)`, `runtime.DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry`.

- [ ] **Step 1: Write failing tests** (`package runtime_test`): (a) `RegisterDefinition` a unique sub-def then `DefaultDefinitionRegistry().Lookup` finds it; `DefaultDefinitionRegistry()` returns the same instance across calls (bind to locals to avoid staticcheck SA4000); (b) DRIVER DEFAULT: build `NewProcessDriver()` zero-arg, register (via `runtime.RegisterDefinition`) a uniquely-named sub-definition, run a parent definition containing a `KindCallActivity` referencing that sub by DefRef, assert the parent completes and the sub ran (use `sync.Once` + unique IDs like the existing `processdriver_defaults_test.go` global-catalog tests — read that file for the pattern); (c) `WithDefinitions(nil)` ignored → default still used; `WithDefinitions(custom)` overrides; (d) DEBUG summary: zero-config shows `definitions=default-global`, `WithDefinitions(custom)` shows `definitions=custom` (extend the existing summary test / capturing slog handler in `processdriver_defaults_test.go`). Reference `runtime/async_callactivity_test.go` or `runtime/subprocess_*_test.go` for how to build a parent+call-activity definition and drive it.
- [ ] **Step 2: Run — verify RED.** `go test ./runtime/ -run 'DefinitionRegistry|RegisterDefinition|Summary|WithDefinitions' 2>&1 | tail`.
- [ ] **Step 3: Implement:**
  - `runtime/definition_registry.go`: `var defaultDefinitionRegistry = kernel.NewMemDefinitionRegistry()`; `DefaultDefinitionRegistry()`, `RegisterDefinition`, `MustRegisterDefinition` (thin delegates). Full godoc incl. the test-isolation caveat.
  - `processdriver.go`: set `r.defsReg = defaultDefinitionRegistry` before the option loop (alongside the other defaults). Change DEBUG `definitions` attr to `default-global`/`custom` (compare `r.defsReg == DefaultDefinitionRegistry()`; use a `defOrigin` helper mirroring store/catalog). Reword the StartSubInstance lookup-miss error to point at `runtime.RegisterDefinition` / `WithDefinitions`. Keep or drop the `defsReg == nil` guard (it is now unreachable given the nil-guard below) — if kept, it stays as defensive code.
  - `processdriver_options.go`: `WithDefinitions` becomes nil-guarded (`if reg != nil { r.defsReg = reg }`); update its godoc (no longer "without this option call activities error" — now "overrides the process-global default registry").
- [ ] **Step 4: Run — verify GREEN.** `go test ./runtime/ -run 'DefinitionRegistry|RegisterDefinition|Summary|WithDefinitions' -race 2>&1 | tail`.
- [ ] **Step 5: Full regression + graceful degradation.** `go test ./runtime/... 2>&1 | tail` — all green (confirms cancel-propagation/timer paths unaffected by the now-non-nil default; if any existing test asserted the old "no definition registry configured" StartSubInstance message or the old `definitions=off` DEBUG value, update it to the new behaviour).
- [ ] **Step 6:** `gofmt -w . && go build ./... && golangci-lint run ./runtime/...`.
- [ ] **Step 7: Commit** `feat(runtime): default DefinitionRegistry + RegisterDefinition`.

---

## Task 3: ADR-0097 + docs (parallelizable — docs only)

**Files:** Create `docs/adr/0097-default-definition-registry.md`; update `README.md`, `INTERACTIONS.md`, `CHANGELOG.md`.

- [ ] **Step 1:** Read `docs/adr/0001-*.md` (template) and `docs/adr/0096-*.md` (predecessor). Write ADR-0097 (Nygard): Context (no definition-side default; asymmetry with action.DefaultCatalog), Decision (kernel.MemDefinitionRegistry + runtime.RegisterDefinition/DefaultDefinitionRegistry + driver default + WithDefinitions nil-guard + DEBUG label; definition-package ruled out by layering), Consequences (zero-config call activities; `defsReg` always non-nil ripple degrades gracefully; global-registry test-isolation caveat).
- [ ] **Step 2:** README/INTERACTIONS: document `runtime.RegisterDefinition` + default registry in the driver/quickstart sections; add to any constructor/error tables. CHANGELOG: add entry under the current section (default DefinitionRegistry + RegisterDefinition; WithDefinitions now nil-guarded).
- [ ] **Step 3:** `go build ./... && go test ./... 2>&1 | tail -3` (docs-only, confirm nothing broke). Commit `docs: default DefinitionRegistry + ADR-0097`.

---

## Task 4: Final verification (orchestrator)

- [ ] `go build ./...`; `go test -race ./...`; coverage kernel + runtime ≥85%; `golangci-lint run ./...` clean.
- [ ] Update spec Status → Implemented; commit.
