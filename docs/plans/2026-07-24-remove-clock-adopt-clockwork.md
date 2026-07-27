# Remove `clock`, Adopt `clockwork.Clock` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete the in-repo `clock` package and depend on `clockwork.Clock` directly, routing all five production waiting-primitive sites through the injected clock so tests are deterministic.

**Architecture:** Two phases. **Phase A (Task 1)** is an atomic, behavior-preserving type migration: `clock.Clock`→`clockwork.Clock`, `clock.System()`→`clockwork.NewRealClock()`, delete `clock/`, keep every `time.*` call as-is — existing tests are the safety net (refactor, no new tests). **Phase B (Tasks 2–6)** adds new behavior — each of the five sites routes its ticker/timer/after through the clock, gated by a new red-first deterministic test. **Task 7** is governance (ADR already written; CLAUDE.md + CHANGELOG).

**Tech Stack:** Go 1.25, `github.com/jonboulle/clockwork v0.5.0` (pinned), existing test suites.

## Global Constraints

- Module path: `github.com/kartaladev/wrkflw`. Go 1.25.
- `clockwork` is pinned at `v0.5.0` — do not change `go.mod` version.
- **Engine-core purity is retained.** `engine/purity_test.go` keeps `"clockwork"` in `deniedEngineImports`; `engine/` and `definition/` non-test files must not *directly* import clockwork and must not call wall-clock `time.*`. Both guards must stay green. (expreval → clockwork is a *transitive* import via `internal/expreval`, not a direct engine import — allowed.)
- clockwork `Ticker`/`Timer` are **interfaces**: channel access is `.Chan()`, not the stdlib `.C` field. `Stop()` is unchanged. `Reset` exists on both.
- Real default everywhere time is injected: `clockwork.NewRealClock()`.
- Error sentinels keep the `workflow-<pkg>: …` prefix. Pair each `foo.go` with `foo_test.go`.
- TDD: Phase-B steps are red-first (show the failing run). Phase-A is a pure refactor — no new test, existing tests green before and after.
- Verification per touched package: `go test -race`, ≥85% (hot paths first), `golangci-lint` clean.

---

### Task 1: Atomic type migration — delete `clock`, flip to `clockwork.Clock`

**Files (production — flip `clock.Clock`→`clockwork.Clock`, `clock.System()`→`clockwork.NewRealClock()`, drop the `wrkflw/clock` import, add `jonboulle/clockwork`):**
- Delete: `clock/clock.go`, `clock/clock_test.go`
- Modify: `runtime/processdriver_options.go` (`WithClock` :203), `runtime/processdriver.go`
- Modify: `service/options.go` (:26,:84), `service/service.go`
- Modify: `runtime/chain/chainer.go` (:69,:79,:93)
- Modify: `runtime/calllink/notifier.go` (field :41, `WithClock` :80)
- Modify: `runtime/signal/signalbus.go` (field :47, `WithClock` :59, doc :44/:69)
- Modify: `runtime/task/service.go` (fields :32,:38, `WithClock` :68)
- Modify: `runtime/kernel/caching_definition_registry.go`, `runtime/kernel/mem_calllink.go`
- Modify: `persistence/persistence.go` (`WithRelayClock` :291, `WithCallLinkClock` :387), `persistence/mysql.go`
- Modify: `internal/persistence/store/relay.go` (clock field), `internal/persistence/store/call_links.go` (clock field)
- Modify (**public helper — breaking**): `processtest/memscheduler.go` — field `clk clock.Clock` (:52), `WithMemSchedulerClock(clock.Clock)` option (:64), default `clock.System()` (:77) → `clockwork.Clock` / `clockwork.NewRealClock()`.
- Modify (test-double helper): `runtime/internal/runtimetest/doubles.go` — `Clock clock.Clock` field (:29), default `clock.System()` (:38).
- Modify: `doc.go` (:9 "no clock" comment fine; :93–95 rewrite `clock.Clock`/`clock.System()` → `clockwork.Clock`/`clockwork.NewRealClock()`).
- Modify: examples importing `wrkflw/clock`: `examples/scenarios/{compensation_saga,compensation_throw,completion_action,input_validation,reverse_rollback,usertask_approval}/main.go`
- **Narrow-stub fix (compile break):** `runtime/kernel/caching_definition_registry_test.go` — the hand-rolled `fakeClock`/`newFakeClock` (:41–48) implements only `Now()`; once the registry's field widens to `clockwork.Clock` it no longer satisfies it. Replace those uses with `clockwork.NewFakeClockAt(t)` (delete the local `fakeClock` type).
- **Test-double wrapper:** `processtest/clock.go`, `processtest/clock_test.go`
- **Purity comments only:** `engine/purity_test.go` (comment text lines ~95/103; do NOT remove the `"clockwork"` denylist entry)
- **Sweep + narrow-stub hunt (mandatory):** after the edits, run `grep -rl "kartaladev/wrkflw/clock" --include=*.go` (must be empty) AND `grep -rn "func.*Now() time.Time" --include=*.go | grep -v clockwork@` to catch any remaining narrow `Now()`-only stub that a widened field would reject. Fix each. Note: ~8 additional `*_test.go` files import `wrkflw/clock` for trivial `clock.System()`/`clock.Clock` swaps (`runtime/{observability,subprocess_example,async_callactivity_e2e,human_example}_test.go`, `runtime/calllink/{notifier,notifier_options}_test.go`, `runtime/chain/chainer_options_test.go`, `persistence/persistence_test.go`); they carry **no** narrow stub and are handled by this sweep gate + `go build`/`go test`, not enumerated individually. The only `Now()`-only stub needing manual replacement is `runtime/kernel/caching_definition_registry_test.go`.

**Interfaces:**
- Consumes: `clockwork.Clock` (superset of the old `clock.Clock`), `clockwork.NewRealClock()`.
- Produces: every `With…Clock` option now takes `clockwork.Clock`. `processtest.NewFakeClock(start) *processtest.FakeClock` (embeds `*clockwork.FakeClock`, adds `Set`).

- [ ] **Step 1: Rewrite the processtest fake as a clockwork-backed wrapper**

Replace `processtest/clock.go` entirely with:

```go
package processtest

import (
	"time"

	"github.com/jonboulle/clockwork"
)

// FakeClock is a manually-advanced clock for deterministic tests. It embeds a
// clockwork.FakeClock (so it satisfies clockwork.Clock and drives fake tickers,
// timers, and after-channels) and adds Set for absolute jumps. A Harness shares
// one FakeClock between its driver and scheduler.
type FakeClock struct {
	*clockwork.FakeClock
}

// Compile-time assertion.
var _ clockwork.Clock = (*FakeClock)(nil)

// NewFakeClock returns a FakeClock positioned at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{clockwork.NewFakeClockAt(start)}
}

// Set jumps Now to t (forward or backward) by advancing the embedded fake by the
// delta. Forward jumps fire any timers/tickers scheduled in the interval.
func (c *FakeClock) Set(t time.Time) {
	c.Advance(t.Sub(c.Now()))
}
```

- [ ] **Step 2: Update `processtest/clock_test.go`**

Change the interface assertion from `clock.Clock` to `clockwork.Clock`, drop the `wrkflw/clock` import. Keep the `Now`/`Advance`/`Set` assertions (they still hold). Expected final assertion line:

```go
var _ clockwork.Clock = fc
```

- [ ] **Step 3: Flip every production consumer**

For each production file above, apply the mechanical transform:
- import: remove `"github.com/kartaladev/wrkflw/clock"`, add `"github.com/jonboulle/clockwork"`.
- type: `clock.Clock` → `clockwork.Clock`.
- default: `clock.System()` → `clockwork.NewRealClock()`.
- doc comments naming `clock.Clock (ADR-0003)` → `clockwork.Clock (ADR-0138)`.
- **Leave all `time.NewTicker` / `time.NewTimer` / `time.After` calls unchanged** — they are converted in Phase B.

- [ ] **Step 4: Fix purity-test comments (not behavior)**

In `engine/purity_test.go`, update the stale `clock.Clock` mentions in the comments on `TestCorePurityNoWallClock` to `clockwork.Clock`. Leave `deniedEngineImports` (incl. `"clockwork"`) and all assertions untouched.

- [ ] **Step 5: Build and run the full suite (safety net)**

Run: `go build ./... && go test ./...`
Expected: PASS. `grep -rl "kartaladev/wrkflw/clock" --include=*.go` returns nothing.
Fix any remaining reference until both hold.

- [ ] **Step 6: Purity + race + lint**

Run: `go test ./engine/... -run Purity -race` → PASS (core stays clockwork-free & wall-clock-free).
Run: `golangci-lint run ./...` → clean.

- [ ] **Step 7: Commit (WIP — bundle amended later)**

```bash
git add -A
git commit -m "refactor(clock): remove clock package, adopt clockwork.Clock directly (ADR-0138)"
```

---

### Task 2: expreval — route the timeout timer through the clock

**Files:**
- Modify: `internal/expreval/expreval.go` (struct :34, `New` :52, `run` timer :83)
- Modify caller passing a fake in test: `internal/expreval/expreval_test.go`

**Interfaces:**
- Produces: `expreval.WithClock(clockwork.Clock) Option`; `Evaluator` gains unexported `clk clockwork.Clock` defaulting to `clockwork.NewRealClock()`.
- Consumes: `clockwork.Clock`, `clockwork.NewFakeClock()`.

- [ ] **Step 1: Write the failing deterministic-timeout test**

Add to `internal/expreval/expreval_test.go`:

```go
func TestEvaluator_TimeoutIsClockDriven(t *testing.T) {
	fc := clockwork.NewFakeClock()
	e := expreval.New(expreval.WithTimeout(time.Hour), expreval.WithClock(fc))

	// A releasable blocker: the expr goroutine parks here until the test ends, so
	// it exits cleanly instead of leaking (expr.Run cannot be interrupted, so the
	// timeout only bounds latency — the goroutine must be freed at cleanup).
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	var err error
	go func() {
		_, err = e.EvalBool("block()", map[string]any{
			"block": func() bool { <-release; return true },
		})
		close(done)
	}()

	fc.BlockUntil(1)      // exactly one waiter: run()'s timeout timer
	fc.Advance(time.Hour) // fire the timeout deterministically
	<-done
	require.ErrorIs(t, err, expreval.ErrEvalTimeout)
}
```

Rationale: only `run()`'s timer registers a fake-clock waiter (the expr goroutine
never touches the clock), so `BlockUntil(1)` is exact.

- [ ] **Step 2: Run it — expect FAIL**

Run: `go test ./internal/expreval/ -run TestEvaluator_TimeoutIsClockDriven -race`
Expected: FAIL — `WithClock` undefined (build error), or (once added but timer still `time.NewTimer`) the test hangs/fails because `fc.Advance` does not fire a stdlib timer.

- [ ] **Step 3: Add the clock field/option and route the timer**

In `expreval.go`: add `clk clockwork.Clock` to `Evaluator`; in `New`, default `e.clk = clockwork.NewRealClock()` before applying opts; add:

```go
// WithClock sets the clock backing the evaluation-timeout timer. Defaults to a
// real clock; pass a clockwork fake to drive timeouts deterministically in tests.
func WithClock(c clockwork.Clock) Option {
	return func(e *Evaluator) { e.clk = c }
}
```

In `run`, replace the timer lines:

```go
timer := e.clk.NewTimer(e.timeout)
defer timer.Stop()
select {
case r := <-ch:
	return r.out, r.err
case <-timer.Chan():
	return nil, ErrEvalTimeout
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/expreval/ -run TestEvaluator_TimeoutIsClockDriven -race` → PASS.
Run: `go test ./internal/expreval/... -race` → all PASS.

- [ ] **Step 5: Confirm engine purity unaffected**

Run: `go test ./engine/... -run Purity` → PASS (expreval's clockwork import is transitive; the default real clock keeps `expreval.New(WithTimeout(0))` in `engine/conditions.go` allocation-safe and timer-free).

- [ ] **Step 6: Commit**

```bash
git add internal/expreval/
git commit -m "feat(expreval): clock-driven evaluation timeout (ADR-0138)"
```

---

### Task 3: CallNotifier — route the poll ticker through the clock

**Files:**
- Modify: `runtime/calllink/notifier.go` (`Run` ticker :227 and its `.C` read below it)
- Test: `runtime/calllink/notifier_test.go`

**Interfaces:**
- Consumes: `n.clk clockwork.Clock` (already flipped in Task 1); `clockwork.NewFakeClock()`.

- [ ] **Step 1: Write the failing deterministic-tick test**

Add a test that constructs a `CallNotifier` with `WithClock(fc)` and a poll interval, backed by a fake link store that **signals each drain on a buffered channel** `drained chan struct{}` (model on the existing notifier test's store double). Synchronize, do not sleep or bare-count:
1. Start `Run` in a goroutine (cancel via `t.Context()`/`context.WithCancel`, and `defer cancel()` for clean shutdown).
2. Receive the immediate drain: `<-drained`.
3. `fc.BlockUntil(1)` (confirms the ticker waiter is armed — clockwork registers it at `NewTicker` construction, i.e. from `Run` start, not when the select parks), then `fc.Advance(poll)`.
4. Receive the tick-driven drain: `<-drained` — this receive *is* the assertion (a stdlib `time.NewTicker` would never fire under `Advance`, so a bounded `select { case <-drained: case <-time.After(2*time.Second): t.Fatal("no clock-driven tick") }` fails fast on the unrouted implementation).

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./runtime/calllink/ -run TestCallNotifier_TickIsClockDriven -race`
Expected: FAIL — with `time.NewTicker`, `fc.Advance` does not fire the tick, so the second drain never happens (test times out via a bounded `context.WithTimeout` in the test, or the count stays 1).

- [ ] **Step 3: Route the ticker through the clock**

In `Run`, replace `ticker := time.NewTicker(n.poll)` with `ticker := n.clk.NewTicker(n.poll)`; keep `defer ticker.Stop()`; change the select read `case <-ticker.C:` → `case <-ticker.Chan():`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./runtime/calllink/ -run TestCallNotifier_TickIsClockDriven -race` → PASS.
Run: `go test ./runtime/calllink/... -race` → PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/calllink/
git commit -m "feat(calllink): clock-driven poll ticker (ADR-0138)"
```

---

### Task 4: Relay — route the outbox poll ticker through the clock

**Files:**
- Modify: `internal/persistence/store/relay.go` (`Run` ticker :482 and its `.C` read)
- Test: `internal/persistence/store/relay_*_test.go` (add a focused unit test; the store conformance tests already use clockwork)

**Interfaces:**
- Consumes: `r.clk clockwork.Clock` (flipped in Task 1); `clockwork.NewFakeClock()`.

- [ ] **Step 1: Write the failing test**

Add a test constructing a `Relay` in poll-only mode (no notifier) with a fake clock and a poll interval, backed by a fake outbox that **signals each relay pass on a buffered channel** `passed chan struct{}`. Same synchronization discipline as Task 3 (no sleeps, no bare counts): start `Run` under a cancelable ctx (`defer cancel()`), `<-passed` (immediate), `fc.BlockUntil(1); fc.Advance(poll)`, then a bounded `select { case <-passed: case <-time.After(2*time.Second): t.Fatal("no clock-driven relay pass") }`. **goleak in this package is per-test `VerifyNone` (no `TestMain`), so the new test is not auto-checked** — the real guarantee is joining `Run`: `Relay.Run` blocks on `defer wg.Wait()` and, in poll-only mode (`r.notifier == nil`), spawns no `listenLoop`, so it returns cleanly on `ctx.Done()`. Assert `Run` returned; optionally add `defer goleak.VerifyNone(t)`.

Note on `BlockUntil(1)`: clockwork's `NewTicker` registers its waiter at **construction** (start of `Run`), so `BlockUntil(1)` is already satisfied by the time it is called; the test is robust because a fake ticker buffers one tick, so the `Advance(poll)` tick is delivered even if `Run` is mid-drain. `BlockUntil(1)` remains a correct barrier confirming exactly one waiter (the ticker) is armed.

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/persistence/store/ -run TestRelay_TickIsClockDriven -race`
Expected: FAIL (second pass never happens with `time.NewTicker`).

- [ ] **Step 3: Route the ticker**

Replace `ticker := time.NewTicker(r.poll)` → `ticker := r.clk.NewTicker(r.poll)`; keep `defer ticker.Stop()`; change the loop's `case <-ticker.C:` → `case <-ticker.Chan():`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/persistence/store/ -run TestRelay_TickIsClockDriven -race` → PASS.
Run: `go test ./internal/persistence/store/... -race` → PASS (SQLite pure-Go; Postgres/MySQL conformance need Docker — run if available).

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/
git commit -m "feat(store): clock-driven relay poll ticker (ADR-0138)"
```

---

### Task 5: pgx notifier — route reconnect backoff through the clock

**Files:**
- Modify: `internal/persistence/store/notifier_pgx.go` (struct :29, `NewPgxNotifier` :61, `time.After` :155)
- Test: `internal/persistence/store/notifier_pgx_test.go`

**Interfaces:**
- Produces: `store.WithPgxNotifierClock(clockwork.Clock) PgxNotifierOption`; `pgxNotifier` gains `clk clockwork.Clock`, default `clockwork.NewRealClock()` in `NewPgxNotifier`.

- [ ] **Step 1: Write the failing backoff test**

Add a test that exercises the reconnect wait: with a fake clock, assert the backoff select unblocks only after `fc.Advance(pgxNotifierReconnectBackoff)`. If the reconnect path is hard to reach without a live pool, extract the wait into a small `func (n *pgxNotifier) waitBackoff(ctx) error { select { case <-ctx.Done(): return ctx.Err(); case <-n.clk.After(pgxNotifierReconnectBackoff): return nil } }` and test that directly (red-first: the method does not exist yet).

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/persistence/store/ -run TestPgxNotifier_BackoffIsClockDriven -race`
Expected: FAIL — `WithPgxNotifierClock`/`waitBackoff` undefined, or fake advance does not release `time.After`.

- [ ] **Step 3: Add the clock and route `time.After`**

Add `clk clockwork.Clock` to `pgxNotifier`; default in `NewPgxNotifier`:
```go
n := &pgxNotifier{pool: pool, clk: clockwork.NewRealClock()}
```
Add the option:
```go
func WithPgxNotifierClock(c clockwork.Clock) PgxNotifierOption {
	return func(n *pgxNotifier) { n.clk = c }
}
```
Replace `case <-time.After(pgxNotifierReconnectBackoff):` → `case <-n.clk.After(pgxNotifierReconnectBackoff):`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/persistence/store/ -run TestPgxNotifier_BackoffIsClockDriven -race` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/store/
git commit -m "feat(store): clock-driven pgx-notifier reconnect backoff (ADR-0138)"
```

---

### Task 6: casbin pg_watcher — route reconnect backoff through the clock

**Files:**
- Modify: `internal/authz/casbin/pg_watcher.go` (struct :23, `newPGWatcher` :41, `backoff` :130 `time.After` :133)
- Modify: `internal/authz/casbin/db.go` (caller at :84 passes `clockwork.NewRealClock()`)
- Modify: `internal/authz/casbin/export_test.go` (the exported `NewPGWatcher` wrapper at :13–14) — **`newPGWatcher` has TWO callers, not one.**
- Test: `internal/authz/casbin/pg_watcher_test.go`

**Interfaces:**
- Produces: `newPGWatcher(pool *pgxpool.Pool, channel, nodeID string, listenReady chan struct{}, clk clockwork.Clock) *pgWatcher` (clock appended as last param); `pgWatcher` gains `clk clockwork.Clock`.
- **Keep the exported test wrapper backward-compatible:** `NewPGWatcher` in `export_test.go` stays 4-param and passes `clockwork.NewRealClock()` into `newPGWatcher`, so its 5 black-box callers (`db_test.go:86,109,131`, `pg_watcher_test.go:26,28`) keep compiling unchanged. The white-box backoff test constructs `newPGWatcher(..., fc)` directly with the fake.

- [ ] **Step 1: Write the failing backoff test**

Add a white-box test (`package casbin`) constructing a `pgWatcher` via `newPGWatcher(nil, "ch", "node", make(chan struct{}), fc)` and calling `w.backoff(ctx)` in a goroutine; assert it returns/unblocks only after `fc.BlockUntil(1); fc.Advance(watcherReconnectDelay)`, and returns immediately on `ctx` cancel.

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/authz/casbin/ -run TestPgWatcher_BackoffIsClockDriven -race`
Expected: FAIL — `newPGWatcher` arity mismatch / fake advance does not release `time.After`.

- [ ] **Step 3: Add the clock param and route `time.After`**

Add `clk clockwork.Clock` to `pgWatcher`; set it in `newPGWatcher` from the new param. Update **both** callers: `db.go:84` passes `clockwork.NewRealClock()`, and `export_test.go`'s `NewPGWatcher` wrapper passes `clockwork.NewRealClock()` (keeping its own 4-param signature). In `backoff`, replace `case <-time.After(watcherReconnectDelay):` → `case <-w.clk.After(watcherReconnectDelay):`.

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/authz/casbin/ -run TestPgWatcher_BackoffIsClockDriven -race` → PASS.
Run: `go test ./internal/authz/casbin/... -race` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/casbin/
git commit -m "feat(casbin): clock-driven watcher reconnect backoff (ADR-0138)"
```

---

### Task 7: Governance — CLAUDE.md + CHANGELOG (ADR-0138 already written)

**Files:**
- Modify: `CLAUDE.md` (Tech-Stack "Time source" row; Common Pitfall #3)
- Modify: `CHANGELOG.md`
- Modify: `docs/adr/0003-clockwork-as-time-source.md` — Status line → `Superseded by [ADR-0138]` (already applied in-tree; listed here so a from-scratch run performs it).
- (ADR-0138 and this plan/spec are already in the tree.)

- [ ] **Step 1: Edit the Tech-Stack "Time source" row**

Replace the "never import clockwork from engine/workflow code, depend on `clock.Clock`" wording with: outer stateful layers depend on `clockwork.Clock` **directly** (ADR-0138, supersedes ADR-0003); the pure engine core stays clockwork-free (time enters as `Trigger.OccurredAt`); one fake clock still drives engine + scheduler in tests.

- [ ] **Step 2: Edit Common Pitfall #3**

Remove `clockwork` from the "never import … directly from workflow/engine code" list; keep watermill, casbin, gocron. Add a clause: the engine *core* still must not import clockwork (enforced by `engine/purity_test.go`).

- [ ] **Step 3: Add CHANGELOG breaking-change entries**

Under an Unreleased/Breaking section: removed public `clock` package; every
`With…Clock` option now takes `clockwork.Clock` — **including
`processtest.WithMemSchedulerClock`**; consumers passing `clock.Clock` migrate to
`clockwork.Clock`. (No `processtest.FakeClock` break — the wrapper preserves
`Now`/`Advance`/`Set`.) **Behavioral note:** `processtest.FakeClock.Advance` no
longer no-ops on a non-positive duration (the old narrow fake guarded `d>0`); it
now follows clockwork semantics (`Advance(0)` fires already-due waiters; negative
moves time backward). All in-tree callers pass strictly positive durations, so
nothing changes today.

- [ ] **Step 4: Full verification + fold the bundle**

Run: `go test -race ./... && go tool cover` (≥85% touched pkgs); `golangci-lint run ./...` clean.
Then squash Task-1–7 commits into one feature bundle (impl + tests + ADR-0138 + spec + this plan + CLAUDE.md + CHANGELOG) per Git Discipline, e.g. interactive reset to a single:
```
feat(clock): remove clock package, adopt clockwork directly (ADR-0138)
```

- [ ] **Step 5: Delivery Gate**

Run `/code-review` then `/security-review` on the bundle; fold all findings via `git commit --amend`. Then merge `--no-ff` to `main`.

---

## Self-Review

- **Spec coverage:** package removal + API flip → Task 1; all 5 waiting sites → Tasks 2–6; processtest migration → Task 1 Step 1–2; engine-purity retention → Global Constraints + Task 1 Step 4 + Task 2 Step 5; governance (ADR/CLAUDE.md/CHANGELOG) → ADR pre-written + Task 7. No spec section is unmapped.
- **`FakeClock.Set`** open question resolved: single caller (its own test); kept via wrapper — no consumer break.
- **expreval threading** open question resolved: default real clock; only the runtime (`WithTimeout(d>0)`) exercises the timer; engine uses `WithTimeout(0)` (timer path never taken) and clockwork stays a transitive, purity-legal import.
- **Type consistency:** `WithClock`/`WithPgxNotifierClock`/`newPGWatcher` signatures and the `.Chan()` (not `.C`) reads are consistent across tasks.
- **Placeholders:** none — every code step shows the code; Tasks 3/4/5 describe the store double to model on rather than inventing a divergent one, which is intentional (reuse existing test doubles, DRY).
- **`processtest.FakeClock.Set` behavioral note:** the old narrow fake fired nothing on `Set`/`Advance`; the new wrapper's `Set` calls clockwork `Advance`, which fires any fake waiters registered in the jumped interval. This is inert in the harness today (`MemScheduler` reads `Now()` only and registers no clockwork waiters), so existing tests are unaffected — but if a future harness-shared component arms a `clk.NewTicker/NewTimer` on the shared fake, a forward `Set` would now fire it. Documented so it is not a surprise.
