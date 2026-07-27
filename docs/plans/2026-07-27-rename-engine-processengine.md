# Plan: rename `service.Engine` → `service.ProcessEngine` (ADR-0141)

> Mechanical, behavior-preserving public rename. NO new tests — the existing
> suites are the safety net (like a pure refactor). Spec:
> `docs/specs/2026-07-27-rename-engine-processengine.md`. ADR:
> `docs/adr/0141-rename-engine-to-processengine.md`.

**Goal:** Rename the exported `service.Engine` type → `ProcessEngine` and
`NewEngine` → `NewProcessEngine` everywhere, in one feature bundle, no alias.

**Architecture:** Single atomic rename across ~86 sites, almost all in `service/`
plus a handful of external callers, examples, an internal test harness, a slog
string, and doc prose. Existing tests are the safety net.

## Global constraints

- Module `github.com/kartaladev/wrkflw`, Go 1.25.
- **Behavior-preserving: no new tests, no logic change.** The only `_test.go`
  edits are the mechanical `Engine`→`ProcessEngine` / `newEngine`→`newProcessEngine`
  swaps and the return-type updates. Existing suites must pass unchanged.
- **Disambiguation (critical):** rename ONLY the `service`-package `Engine` type /
  `NewEngine` constructor and their qualified `service.Engine`/`service.NewEngine`
  uses. **Never touch `engine.`-qualified references** (root `engine` package) or
  common-English `Engine` test-function names (e.g.
  `TestSQLiteDurableProviderPowersEngine`).
- Sweep gates (must hold after): `grep -rn "\bservice\.Engine\b\|\bservice\.NewEngine\b" --include="*.go"` empty; no bare `Engine`/`NewEngine` in `service/` referring to the renamed type; `go build ./...` clean.
- `go test ./...` green (Docker for persistence/transport suites), `golangci-lint run ./...` clean, `gofmt -l .` clean.

---

### Task 1: atomic rename

**Files (production):**
- `service/service.go` — `type Engine`→`ProcessEngine` (:112), `NewEngine`→`NewProcessEngine` (:136), all `*Engine` receivers/internal uses (~25), `var _ Service = (*Engine)(nil)` (:301), slog msg string `"service.Engine constructed"`→`"service.ProcessEngine constructed"` (:291), and the type's doc comment (note the `runtime.ProcessDriver` relationship per ADR-0141).
- `runtime/driver_shutdown.go:41` — doc-comment `service.Engine`→`service.ProcessEngine`.
- `internal/transporttest/harness.go:88` — `service.NewEngine`→`service.NewProcessEngine`.
- `examples/production_wiring/main.go:170`, `examples/sqlite_wiring/main.go:262`, `examples/mysql_wiring/main.go:246`, `examples/cache_wiring/main.go:217` (comment) — `service.NewEngine`→`service.NewProcessEngine`.
- `service/README.md:35` — `Service is implemented by *Engine` → `*ProcessEngine` (the one current-state line the rename breaks). Do NOT touch the stale `service.New(...)`/`EngineOption`/`WithEngineClock` sections (:67,:106,:113 — pre-existing rot, filed as follow-up).
- Prose comments naming the type "Engine" in touched `.go` files (`service/request.go:93`, `service.go` comments, and the test-helper comments in files already edited) → "ProcessEngine".

**Files (test — mechanical swaps only, no assertion/behavior change):**
- `service/*_test.go` (service_test, black-box): every `service.Engine`/`service.NewEngine`; the private helpers `newEngine`→`newProcessEngine` (`service_test.go:42`) and `newOwnedEngine`→`newOwnedProcessEngine` (`shutdown_test.go:21`) and all ~28 call sites; `segregation_test.go` six `*service.Engine` assertions; `t.Fatalf("NewEngine: %v", err)` message strings → `NewProcessEngine`.
- `persistence/durableprovider_test.go` — `service.NewEngine`→`service.NewProcessEngine`.

- [ ] **Step 1: rename the type + constructor + internal uses in `service/service.go`.**
  Apply `Engine`→`ProcessEngine` / `NewEngine`→`NewProcessEngine` within `service.go` ONLY to the `service`-package `Engine` symbol — leave every `engine.`-qualified identifier untouched. Update the slog string and the doc comment. Run `go build ./service/...` — expect failures in callers (next step) but `service.go` itself compiles.

- [ ] **Step 2: update all callers + examples + harness + docs.**
  Apply the qualified `service.Engine`/`service.NewEngine`→`service.ProcessEngine`/`service.NewProcessEngine` swaps in `runtime/driver_shutdown.go`, `internal/transporttest/harness.go`, the 4 `examples/*_wiring`, and `persistence/durableprovider_test.go`. Fix `service/README.md:35` (`*Engine`→`*ProcessEngine`) — that line ONLY; leave the stale `service.New` sections (follow-up). Run `go build ./...` → clean.

- [ ] **Step 3: update the `service` black-box tests.**
  Rename `newEngine`→`newProcessEngine`, `newOwnedEngine`→`newOwnedProcessEngine`, their return types, all call sites, the `segregation_test.go` assertions, and any `NewEngine` error-message strings. These are mechanical — no assertion logic changes.

- [ ] **Step 4: sweep + build + full suite (safety net).**
  Run the sweep greps (both must be empty for the renamed symbol); `go build ./...`; `go test ./...` → all green (this is the behavior-preserving proof). Fix any missed site until green. Confirm no `engine.`-qualified reference was altered (`git diff` review of `service.go` imports/uses).

- [ ] **Step 5: race + lint + gofmt.**
  `go test -race ./service/... ./persistence/... ./internal/transporttest/...` green; `golangci-lint run ./...` clean; `gofmt -l .` empty.

- [ ] **Step 6: CHANGELOG + commit.**
  Add a CHANGELOG breaking-change entry (rename `service.Engine`/`NewEngine` → `ProcessEngine`/`NewProcessEngine`, migration one-liner, ADR-0141). Commit:
  ```
  git commit -m "refactor(service): rename Engine to ProcessEngine (ADR-0141)"
  ```

---

## Self-review
- **Spec coverage:** type/constructor rename → Steps 1-3; slog string → Step 1; private helpers → Step 3; prose comments → Steps 1/3; examples/harness/`service/README.md:35` → Step 2; `engine.` disambiguation → Steps 1/4 (diff review + sweep); CHANGELOG → Step 6. No spec section unmapped.
- **Behavior safety:** mechanical rename; existing suites green before/after are the proof (no new tests, per convention for a pure rename; audit confirmed no `Example_` doc-tests reference the old name).
- **Over-reach guard:** sweep greps + `go build` + explicit `service.go` diff review ensure no `engine.`-qualified name, private `engineConfig`/`validateEngineLeaves`, or common-English `Engine` test name was touched.
- **Enumeration:** every touched FILE is enumerated; per-line sites within `service/*_test.go` are resolved by the wildcard + compiler (the exact line list is not load-bearing — the compiler fails on any miss). Helper call sites ≈ 30.
- **Out of scope (audit-adjudicated):** pre-existing README doc-rot (stale `service.New`/`EngineOption`/`WithEngineClock` in both READMEs) filed as a follow-up, not folded; private `engineConfig`/`validateEngineLeaves` unchanged.
