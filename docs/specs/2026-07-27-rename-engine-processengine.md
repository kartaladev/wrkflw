# Spec: rename `service.Engine` → `service.ProcessEngine`

**Status:** design complete → implementation. Decision:
[ADR-0141](../adr/0141-rename-engine-to-processengine.md). Convention prior art:
ADR-0098/0107/0108 (hard rename, no alias, pre-1.0).

## Problem

The public facade type `service.Engine` and its constructor `service.NewEngine`
carry a weak name that collides conceptually with the pure `engine` package and
`runtime.ProcessDriver`. Rename to `ProcessEngine`/`NewProcessEngine`.

## Change (mechanical, behavior-preserving)

| From | To | Site |
|------|-----|------|
| `type Engine struct` | `type ProcessEngine struct` | `service/service.go:112` |
| `func NewEngine(opts ...Option) (*Engine, error)` | `func NewProcessEngine(opts ...Option) (*ProcessEngine, error)` | `service/service.go:136` |
| all `*Engine` receivers / internal uses in `service.go` | `*ProcessEngine` | `service/service.go` (~25 sites) |
| `var _ Service = (*Engine)(nil)` | `(*ProcessEngine)(nil)` | `service/service.go:301` |
| `"service.Engine constructed"` (slog msg) | `"service.ProcessEngine constructed"` | `service/service.go:291` |
| `service.Engine` / `service.NewEngine` (qualified) | `service.ProcessEngine` / `service.NewProcessEngine` | `runtime/driver_shutdown.go:41` (doc), `internal/transporttest/harness.go:88`, `examples/{production,sqlite,mysql}_wiring/main.go`, `examples/cache_wiring/main.go` (comment), `persistence/durableprovider_test.go`, all `service/*_test.go` |
| private helpers `newEngine`/`newOwnedEngine` (+ their `*Engine` return) | `newProcessEngine`/`newOwnedProcessEngine` (`*ProcessEngine`) | `service/service_test.go:42`, `service/shutdown_test.go:21`, and 30 call sites |
| prose comments naming the type "Engine" in touched `.go` files | "ProcessEngine" | `service/request.go:93`, `service/service.go` comments, test-helper comments (`cancel_instance_test.go:36`, `shutdown_test.go:17-18,37`, `service_lifecycle_test.go:43`) |
| `service/README.md` `Service is implemented by \*Engine` (current-state) | `*ProcessEngine` | `service/README.md:35` |

## Out of scope / must NOT touch

- **`engine.`-qualified references** (root `engine` package — `engine.Trigger`,
  `engine.InstanceState`, etc. in `service.go`): unrelated, leave untouched.
- Other `*Engine`-named identifiers that are common English (test names like
  `TestSQLiteDurableProviderPowersEngine`): not the target; leave.
- **Private "engine"-named symbols** — `engineConfig` (`options.go:18`),
  `validateEngineLeaves` (`service.go:261`), `logConstructionSummary`: internal
  config, not the facade; leave.
- **Pre-existing README doc-rot** (both root `README.md` and `service/README.md`
  describe the ADR-0098-superseded positional `service.New(...)` with nonexistent
  `EngineOption` and ADR-0138-superseded `WithEngineClock`): FILED as a separate
  doc-refresh follow-up, NOT fixed here. This bundle touches `service/README.md`
  only for the one line (`:35`) the rename itself breaks; it does not touch the
  stale `service.New` sections in either README.
- No logic/behavior change. No new tests.

## Verification

- Mechanical rename ⇒ **no new tests**; existing `service` + `persistence` +
  `transport` suites are the safety net and must pass unchanged.
- Sweep gates: after the edits, `grep -rn "\bservice\.Engine\b\|\bservice\.NewEngine\b" --include=*.go` returns nothing; within `service/`, no bare `Engine`/`NewEngine` identifier referring to the renamed type remains (distinguish from `engine.`).
- `go build ./...`, `go test ./...` green; `golangci-lint run ./...` clean; `gofmt -l .` clean.
- CHANGELOG breaking-change entry (rename + migration one-liner, ADR-0141).

## Risk

Low — mechanical, single type + constructor, no aliases/DI, compiler catches any
miss. The one real hazard is over-reaching into `engine.`-qualified names or the
common-English `Engine` test names; the sweep + build guard against it.
