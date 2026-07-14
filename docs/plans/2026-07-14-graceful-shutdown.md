# Graceful Shutdown for `runtime.ProcessDriver` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `ProcessDriver.Shutdown` real admission control (reject new external work) and in-flight drain (wait for running instances), honour the `ctx` deadline across the whole drain, and fix two audit bugs — without touching the engine core.

**Architecture:** A `draining atomic.Bool` gates the exported entry points via a tiny `admit()` helper; an `inflight sync.WaitGroup` counts admitted work; `Shutdown` sets draining → closes the scheduler (deadline-raced) → waits the WaitGroup (deadline-bounded). `ApplyTrigger` is split into a gated public method and an ungated internal `applyTrigger` that every in-driver continuation calls. Timer-start fires admit (drop on drain); timer continuations reserve internally.

**Tech Stack:** Go 1.25, `sync/atomic`, `sync.WaitGroup`, `context`, `errors.Join`; test with `stretchr/testify`, package goleak `TestMain`.

**Spec:** `docs/specs/2026-07-14-graceful-shutdown-design.md` (read it first).

## Global Constraints

- Go 1.25; `go test ./...` and `golangci-lint run ./...` must be clean before done.
- TDD strict (CLAUDE.md "TDD Operational Discipline"): every new symbol / behavioural change is preceded by an **observable failing test** (a `Bash` run showing red) before implementation. No batching test+impl in one edit.
- Error sentinels use the `workflow-runtime: ` prefix (project convention).
- Prefer **black-box** tests (`package runtime_test`); use white-box (`package runtime`) only for unexported primitives that have no black-box surface.
- Table tests follow the project `table-test` skill (assert-closure form, `t.Context()` not `context.Background()` where a test context is wanted). Load `cc-skills-golang:golang-how-to` + `table-test`/`use-mockgen`/`use-testcontainers` at the start.
- The runtime package has a goleak `TestMain`: no test may leak a goroutine. Any `Shutdown`-timeout test MUST release its barrier before returning so the `waitInflight` helper goroutine and the scheduler-close goroutine finish.
- Coverage ≥ 85% on touched packages.
- No engine-core changes: `engine.Step` and `deliverLoop`'s signature are untouched.
- Commit per task with Conventional Commits scoped `feat(runtime)` / `fix(runtime)` / `feat(service)` / `docs`. End each commit body with the `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` trailer.

## File Structure

- **Create** `runtime/driver_shutdown.go` — new sentinels (`ErrDriverShuttingDown`, `ErrDrainTimeout`), gate primitives (`admit`, `reserveInternal`, `IsShuttingDown`, `waitInflight`, `effectiveShutdownCtx`). One responsibility: the driver-level shutdown mechanism.
- **Create** `runtime/driver_shutdown_internal_test.go` (`package runtime`) — white-box tests for `admit`/`reserveInternal`/`effectiveShutdownCtx`.
- **Create** `runtime/driver_shutdown_test.go` (`package runtime_test`) — black-box behaviour: gate rejection, drain wait, drain timeout, `WithShutdownTimeout`.
- **Modify** `runtime/processdriver.go` — add struct fields; gate `Drive`; split `ApplyTrigger`→`applyTrigger`; rewrite `Shutdown` body; change scheduler registration to the deadline-raced closer.
- **Modify** `runtime/processdriver_options.go` — add `WithShutdownTimeout`.
- **Modify** `runtime/processdriver_message.go`, `_signal.go`, `_cancel.go`, `_incident.go`, `_reverse.go` — gate the public methods; redirect internal `ApplyTrigger` calls to `applyTrigger`.
- **Modify** `runtime/timerops.go` — gate `startTimerFireFunc`; reserve-internal in `timerFireFunc`; Finding-4 WARN clarification in `armTimer`.
- **Modify** `runtime/processdriver_shutdown_test.go` — add Finding-3 deadline test.
- **Modify** `service/service.go` — early `draining` reject in `ClaimTask`/`CompleteTask`/`ReassignTask`.
- **Modify** `service/service_test.go` (or a new `service/shutdown_test.go`) — Engine propagation + human-task early-reject tests.
- **Create** `docs/adr/0133-graceful-shutdown.md`; **modify** `CHANGELOG.md`.

---

### Task 1: Gate state + primitives

**Files:**
- Create: `runtime/driver_shutdown.go`
- Modify: `runtime/processdriver.go` (struct fields only)
- Test: `runtime/driver_shutdown_internal_test.go`

**Interfaces:**
- Produces: `ErrDriverShuttingDown`, `ErrDrainTimeout error`; `(driver *ProcessDriver) admit() (release func(), ok bool)`; `(driver *ProcessDriver) reserveInternal() (release func())`; `(driver *ProcessDriver) IsShuttingDown() bool`. Fields `draining atomic.Bool`, `inflight sync.WaitGroup`, `shutdownTimeout time.Duration` on `ProcessDriver`.

- [ ] **Step 1: Write the failing test** — `runtime/driver_shutdown_internal_test.go`:

```go
package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmitGate(t *testing.T) {
	driver, err := NewProcessDriver()
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

	release, ok := driver.admit()
	require.True(t, ok, "admit must succeed before draining")
	release()

	assert.False(t, driver.IsShuttingDown(), "not draining yet")
	driver.draining.Store(true)
	assert.True(t, driver.IsShuttingDown(), "draining now")

	_, ok = driver.admit()
	assert.False(t, ok, "admit must fail once draining")

	// reserveInternal ignores the draining flag (continuations must proceed).
	rel := driver.reserveInternal()
	rel()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestAdmitGate$' ./runtime/...`
Expected: FAIL — `driver.admit`, `driver.reserveInternal`, `driver.IsShuttingDown`, `driver.draining` undefined.

- [ ] **Step 3: Add struct fields** in `runtime/processdriver.go` inside `type ProcessDriver struct` (next to the `shutdown ShutdownGroup` field):

```go
	// draining is set true at the start of Shutdown; once set, admit() refuses new
	// externally-initiated work so it is rejected with ErrDriverShuttingDown.
	draining atomic.Bool
	// inflight counts admitted, currently-executing units of work (each deliverLoop-
	// driving call and each in-flight timer continuation). Shutdown waits on it to drain.
	inflight sync.WaitGroup
	// shutdownTimeout is the fallback drain deadline applied by Shutdown ONLY when the
	// ctx passed to Shutdown carries no deadline of its own. Zero = no fallback. Set via
	// WithShutdownTimeout.
	shutdownTimeout time.Duration
```

Add `"sync/atomic"` to the import block (`sync` and `time` are already imported).

- [ ] **Step 4: Create** `runtime/driver_shutdown.go`:

```go
package runtime

import (
	"context"
	"errors"
)

// ErrDriverShuttingDown is returned by every externally-initiated ProcessDriver entry
// point once Shutdown has begun draining. In-flight work already admitted still runs to
// completion; only new work is refused.
var ErrDriverShuttingDown = errors.New("workflow-runtime: driver is shutting down")

// ErrDrainTimeout is returned (joined) by Shutdown when the drain deadline expires before
// every in-flight unit of work has finished. In-flight work is NOT force-cancelled; it
// keeps running to completion on its own goroutine.
var ErrDrainTimeout = errors.New("workflow-runtime: shutdown drain timed out")

// admit reserves an in-flight slot for a new externally-initiated unit of work. It returns
// a release func and true when work may proceed; it returns nil, false once Shutdown has
// begun draining, so the caller rejects with ErrDriverShuttingDown. Call release (via
// defer) exactly once when the unit of work returns.
func (driver *ProcessDriver) admit() (release func(), ok bool) {
	if driver.draining.Load() {
		return nil, false
	}
	driver.inflight.Add(1)
	return driver.inflight.Done, true
}

// reserveInternal joins an in-flight continuation to the drain WaitGroup WITHOUT the
// draining check, so a continuation of already-scheduled work (a timer fire) completes even
// while draining. Safe only because the sole post-draining source of such reservations —
// in-flight timer fires — is drained by the scheduler close before Shutdown waits the
// WaitGroup (see Shutdown's ordering invariant).
func (driver *ProcessDriver) reserveInternal() (release func()) {
	driver.inflight.Add(1)
	return driver.inflight.Done
}

// IsShuttingDown reports whether Shutdown has begun draining. It lets a higher layer
// (e.g. service.Engine's human-task handlers) reject before performing side effects.
func (driver *ProcessDriver) IsShuttingDown() bool {
	return driver.draining.Load()
}

// effectiveShutdownCtx applies the WithShutdownTimeout fallback: a ctx deadline always wins;
// otherwise, if a positive shutdownTimeout is configured, derive now+timeout; otherwise
// return ctx unchanged (unbounded). The returned cancel is always safe to defer.
func (driver *ProcessDriver) effectiveShutdownCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	if driver.shutdownTimeout > 0 {
		return context.WithTimeout(ctx, driver.shutdownTimeout)
	}
	return ctx, func() {}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -run '^TestAdmitGate$' ./runtime/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add runtime/driver_shutdown.go runtime/driver_shutdown_internal_test.go runtime/processdriver.go
git commit -m "feat(runtime): add ProcessDriver shutdown gate primitives

admit/reserveInternal/IsShuttingDown + draining/inflight state + sentinels.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `WithShutdownTimeout` option + `effectiveShutdownCtx` behaviour

**Files:**
- Modify: `runtime/processdriver_options.go`
- Test: `runtime/driver_shutdown_internal_test.go`

**Interfaces:**
- Produces: `func WithShutdownTimeout(d time.Duration) Option`.

- [ ] **Step 1: Write the failing test** — append to `runtime/driver_shutdown_internal_test.go`:

```go
func TestEffectiveShutdownCtx(t *testing.T) {
	t.Run("ctx deadline wins over option", func(t *testing.T) {
		driver, err := NewProcessDriver(WithShutdownTimeout(time.Hour))
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

		parent, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		got, done := driver.effectiveShutdownCtx(parent)
		defer done()
		dl, ok := got.Deadline()
		require.True(t, ok)
		assert.WithinDuration(t, time.Now().Add(5*time.Second), dl, time.Second)
	})

	t.Run("option applies when ctx has no deadline", func(t *testing.T) {
		driver, err := NewProcessDriver(WithShutdownTimeout(2 * time.Second))
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

		got, done := driver.effectiveShutdownCtx(context.Background())
		defer done()
		_, ok := got.Deadline()
		assert.True(t, ok, "fallback must impose a deadline")
	})

	t.Run("unbounded when neither set", func(t *testing.T) {
		driver, err := NewProcessDriver()
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

		got, done := driver.effectiveShutdownCtx(context.Background())
		defer done()
		_, ok := got.Deadline()
		assert.False(t, ok, "no deadline, no fallback => unbounded")
	})
}
```

Add `"context"` and `"time"` imports to the internal test file if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestEffectiveShutdownCtx$' ./runtime/...`
Expected: FAIL — `WithShutdownTimeout` undefined.

- [ ] **Step 3: Add the option** at the end of `runtime/processdriver_options.go`:

```go
// WithShutdownTimeout sets a FALLBACK drain deadline applied by [ProcessDriver.Shutdown]
// only when the ctx passed to Shutdown carries no deadline of its own. A ctx deadline
// always wins (the caller was explicit). Zero or unset means no fallback — Shutdown then
// respects ctx as-is, waiting unbounded if ctx has no deadline. A non-positive d is ignored
// (treated as unset), consistent with [WithActionTimeout].
func WithShutdownTimeout(d time.Duration) Option {
	return func(driver *ProcessDriver) {
		if d > 0 {
			driver.shutdownTimeout = d
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run '^TestEffectiveShutdownCtx$' ./runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/processdriver_options.go runtime/driver_shutdown_internal_test.go
git commit -m "feat(runtime): add WithShutdownTimeout fallback option

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Rewrite `Shutdown` — drain + deadline-honouring scheduler close

**Files:**
- Modify: `runtime/processdriver.go` (`Shutdown` body; scheduler registration in `NewProcessDriver`)
- Create/Modify: `runtime/driver_shutdown.go` (`waitInflight`)
- Test: `runtime/driver_shutdown_test.go` (new, black-box)

**Interfaces:**
- Consumes: `admit`, `reserveInternal`, `effectiveShutdownCtx` (Task 1/2).
- Produces: new `Shutdown` semantics; `(driver *ProcessDriver) waitInflight(ctx) error`.

- [ ] **Step 1: Write the failing test** — `runtime/driver_shutdown_test.go`:

```go
package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
)

// barrierDef builds a one-service-task definition whose action blocks on `enter`
// (signals it started) then `release` (unblocks), so a test can hold a Drive call
// in-flight while it calls Shutdown.
func barrierDef(t *testing.T, cat action.Catalog, enter, release chan struct{}) *model.ProcessDefinition {
	// Register a blocking action "barrier" into cat, then build:
	//   start -> service("barrier") -> end
	// (Use the project's DefinitionBuilder AddServiceTask + AddStartEvent/AddEndEvent
	//  fluent methods; see runtime/example_test.go for the builder pattern.)
	// ...construct and return the definition...
}

func TestShutdownDrainsInFlight(t *testing.T) {
	cat := action.NewCatalog()
	enter, release := make(chan struct{}), make(chan struct{})
	cat.Register("barrier", action.ActionFunc(func(ctx context.Context, in action.Input) (action.Output, error) {
		close(enter)
		<-release
		return action.Output{}, nil
	}))
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat))
	require.NoError(t, err)

	def := barrierDef(t, cat, enter, release)

	driveDone := make(chan struct{})
	go func() {
		_, _ = driver.Drive(context.Background(), def, "i-barrier", nil)
		close(driveDone)
	}()
	<-enter // Drive is now parked inside the blocking action

	shutdownReturned := make(chan error, 1)
	go func() { shutdownReturned <- driver.Shutdown(context.Background()) }()

	select {
	case <-shutdownReturned:
		t.Fatal("Shutdown returned before in-flight Drive finished")
	case <-time.After(100 * time.Millisecond):
		// good: Shutdown is blocked on the drain
	}

	close(release) // let the action finish
	<-driveDone
	require.NoError(t, <-shutdownReturned)
}

func TestShutdownDrainTimeout(t *testing.T) {
	cat := action.NewCatalog()
	enter, release := make(chan struct{}), make(chan struct{})
	cat.Register("barrier", action.ActionFunc(func(ctx context.Context, in action.Input) (action.Output, error) {
		close(enter)
		<-release
		return action.Output{}, nil
	}))
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat))
	require.NoError(t, err)
	def := barrierDef(t, cat, enter, release)

	driveDone := make(chan struct{})
	go func() { _, _ = driver.Drive(context.Background(), def, "i-timeout", nil); close(driveDone) }()
	<-enter

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = driver.Shutdown(ctx)
	assert.ErrorIs(t, err, runtime.ErrDrainTimeout)

	// goleak: release the barrier so the Drive goroutine + waitInflight goroutine exit
	// before the test (and TestMain's leak check) finishes.
	close(release)
	<-driveDone
}
```

Note: replace the `barrierDef` body and `model` import with the actual builder calls — see `runtime/example_test.go` / `runtime/retry_test.go` for how definitions and `action.ActionFunc` are constructed in this repo. Confirm the real `action.Catalog` constructor/register + `action.ActionFunc`/`Input`/`Output` names against `action/` before writing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestShutdownDrain' ./runtime/...`
Expected: FAIL — `Shutdown` returns immediately (no drain) / `ErrDrainTimeout` not returned.

- [ ] **Step 3: Add `waitInflight`** to `runtime/driver_shutdown.go`:

```go
// waitInflight blocks until every admitted in-flight unit of work has released its slot,
// or ctx is done. On ctx expiry it returns ErrDrainTimeout wrapping ctx.Err(); the in-flight
// work is NOT cancelled and keeps running to completion.
func (driver *ProcessDriver) waitInflight(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		driver.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrDrainTimeout, ctx.Err())
	}
}
```

Add `"fmt"` to `driver_shutdown.go` imports.

- [ ] **Step 4: Rewrite `Shutdown`** in `runtime/processdriver.go` (replace the current one-line body):

```go
func (driver *ProcessDriver) Shutdown(ctx context.Context) error {
	// 1. Stop admitting new external work. Set before anything else so a command
	//    racing Shutdown is rejected rather than admitted mid-teardown.
	driver.draining.Store(true)

	// Apply the WithShutdownTimeout fallback iff ctx carries no deadline (ADR-0133).
	ctx, cancel := driver.effectiveShutdownCtx(ctx)
	defer cancel()

	// 2. Close the owned scheduler: gocron stops dispatching and waits for in-flight
	//    timer fires (which hold reserveInternal slots) to finish. Bounded by ctx via
	//    the deadline-raced closer registered in NewProcessDriver. A consumer-injected
	//    scheduler is not registered, so this is a no-op for it.
	schedErr := driver.shutdown.Shutdown(ctx)

	// 3. Wait for consumer-initiated deliverLoops still running. By now no new inflight
	//    Add can occur: draining rejects external work, and the only internal source
	//    (timer fires) drained in step 2. This ordering rules out Add-after-Wait.
	drainErr := driver.waitInflight(ctx)

	return errors.Join(schedErr, drainErr)
}
```

- [ ] **Step 5: Make the scheduler close honour ctx (Finding 3)** — in `NewProcessDriver`, replace `driver.shutdown.AddCloser(sched)` with:

```go
		// Register a deadline-raced closer instead of AddCloser so Shutdown's ctx actually
		// bounds the scheduler drain (audit Finding 3). sched.Close() blocks on gocron's
		// own stop timeout; racing it against ctx.Done() lets a caller-supplied deadline win.
		// If ctx wins, Close keeps running in its goroutine and finishes shortly after
		// (bounded by gocron's stop timeout) — not leaked indefinitely.
		driver.shutdown.Add(func(ctx context.Context) error {
			done := make(chan error, 1)
			go func() { done <- sched.Close() }()
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		})
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -run '^TestShutdownDrain' ./runtime/...`
Expected: PASS.
Also run the pre-existing shutdown suite to confirm no regression:
Run: `go test -run '^TestProcessDriverShutdown$|^TestProcessDriverStart$' ./runtime/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add runtime/processdriver.go runtime/driver_shutdown.go runtime/driver_shutdown_test.go
git commit -m "feat(runtime): drain in-flight work on Shutdown, honour ctx deadline

Shutdown now sets draining, closes the scheduler (deadline-raced, fixes audit
Finding 3), then waits inflight bounded by ctx (ErrDrainTimeout on expiry).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Split `applyTrigger` + gate public `ApplyTrigger` + redirect internal callers

**Files:**
- Modify: `runtime/processdriver.go` (`ApplyTrigger`)
- Modify: `runtime/processdriver_cancel.go`, `_incident.go`, `_reverse.go`, `_message.go`
- Test: `runtime/driver_shutdown_test.go`

**Interfaces:**
- Produces: unexported `(driver *ProcessDriver) applyTrigger(ctx, def, instanceID, trg) (engine.InstanceState, error)` — the current `ApplyTrigger` body, ungated. Public `ApplyTrigger` becomes a gated wrapper.
- Consumes: `admit` (Task 1).

- [ ] **Step 1: Write the failing test** — append to `runtime/driver_shutdown_test.go`:

```go
func TestApplyTriggerRejectedWhenDraining(t *testing.T) {
	driver, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	require.NoError(t, driver.Shutdown(context.Background()))

	// Any def/id: the gate rejects before touching the store.
	_, err = driver.ApplyTrigger(context.Background(), twoStepDef(t), "i-x",
		engine.NewCancelRequested(time.Now()))
	assert.ErrorIs(t, err, runtime.ErrDriverShuttingDown)
}
```

Use an existing helper def (e.g. `twoStepDef`/`timerOnlyDef` already defined in the package's test files) rather than adding a new one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestApplyTriggerRejectedWhenDraining$' ./runtime/...`
Expected: FAIL — `ApplyTrigger` currently loads the store and returns a load error, not `ErrDriverShuttingDown`.

- [ ] **Step 3: Split and gate** in `runtime/processdriver.go`. Rename the existing `func (driver *ProcessDriver) ApplyTrigger(...)` body to `applyTrigger` (unexported, same signature, same body), then add the gated public wrapper immediately above it:

```go
// ApplyTrigger applies one external trigger to an instance. It is the gated public entry
// point: once Shutdown has begun draining it rejects with ErrDriverShuttingDown. Internal
// continuations (timer fires, cancel cascades) call the ungated applyTrigger instead.
func (driver *ProcessDriver) ApplyTrigger(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error) {
	release, ok := driver.admit()
	if !ok {
		return engine.InstanceState{}, ErrDriverShuttingDown
	}
	defer release()
	return driver.applyTrigger(ctx, def, instanceID, trg)
}

// applyTrigger is the ungated worker: load → deliverLoop → save. Callers that are already
// inside a gated method (or a counted continuation) call this to avoid a nested re-admit.
func (driver *ProcessDriver) applyTrigger(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error) {
	// ...exact current body of ApplyTrigger, unchanged (span, store.Load, deliverLoop)...
}
```

- [ ] **Step 4: Redirect internal callers** from `driver.ApplyTrigger(` to `driver.applyTrigger(` at these exact sites (they are all already inside a gated method, so they must not re-admit):
  - `runtime/processdriver_cancel.go:20` (CancelInstance top-level trigger)
  - `runtime/processdriver_cancel.go:77` (propagateCancel child cascade)
  - `runtime/processdriver_incident.go:18` (ResolveIncident)
  - `runtime/processdriver_reverse.go:98` and `:105` (ReverseInstance)
  - `runtime/processdriver_message.go:63` (DeliverMessage correlate)

Leave the signal-bus `DeliverFunc` (consumer-wired to public `ApplyTrigger`) as-is — that nesting is intentional and documented (§ 5 of the spec).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -run '^TestApplyTriggerRejectedWhenDraining$' ./runtime/...`
Expected: PASS.
Regression: `go test ./runtime/...`
Expected: PASS (cancel cascade / reverse / incident / message paths unaffected).

- [ ] **Step 6: Commit**

```bash
git add runtime/processdriver.go runtime/processdriver_cancel.go runtime/processdriver_incident.go runtime/processdriver_reverse.go runtime/processdriver_message.go runtime/driver_shutdown_test.go
git commit -m "feat(runtime): gate public ApplyTrigger, route continuations to ungated applyTrigger

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Gate the remaining public entry points

**Files:**
- Modify: `runtime/processdriver.go` (`Drive`), `_message.go` (`DeliverMessage`), `_signal.go` (`BroadcastSignal`), `_cancel.go` (`CancelInstance`), `_incident.go` (`ResolveIncident`), `_reverse.go` (`ReverseInstance`)
- Test: `runtime/driver_shutdown_test.go`

**Interfaces:**
- Consumes: `admit` (Task 1). Each method gains the same 4-line prologue.

- [ ] **Step 1: Write the failing test** — append a table test (per the `table-test` skill: assert-closure form) to `runtime/driver_shutdown_test.go`:

```go
func TestExternalEntryPointsRejectedWhenDraining(t *testing.T) {
	tests := map[string]func(d *runtime.ProcessDriver) error{
		"Drive": func(d *runtime.ProcessDriver) error {
			_, err := d.Drive(context.Background(), twoStepDef(t), "i-1", nil)
			return err
		},
		"DeliverMessage": func(d *runtime.ProcessDriver) error {
			return d.DeliverMessage(context.Background(), "m", "k", nil)
		},
		"BroadcastSignal": func(d *runtime.ProcessDriver) error {
			return d.BroadcastSignal(context.Background(), "s", nil)
		},
		"CancelInstance": func(d *runtime.ProcessDriver) error {
			_, err := d.CancelInstance(context.Background(), twoStepDef(t), "i-1")
			return err
		},
		"ResolveIncident": func(d *runtime.ProcessDriver) error {
			_, err := d.ResolveIncident(context.Background(), twoStepDef(t), "i-1", "inc", 1)
			return err
		},
		"ReverseInstance": func(d *runtime.ProcessDriver) error {
			_, err := d.ReverseInstance(context.Background(), twoStepDef(t), "i-1")
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			driver, err := runtime.NewProcessDriver()
			require.NoError(t, err)
			require.NoError(t, driver.Shutdown(context.Background()))
			assert.ErrorIs(t, call(driver), runtime.ErrDriverShuttingDown)
		})
	}
}
```

Note: `DeliverMessage`/`BroadcastSignal` on an empty-match return `nil` today; the gate must run BEFORE the empty-name/empty-match no-op returns, so a drained driver rejects even a would-be no-op. Confirm the gate is the first statement in each method.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestExternalEntryPointsRejectedWhenDraining$' ./runtime/...`
Expected: FAIL — methods currently proceed / return nil instead of `ErrDriverShuttingDown`.

- [ ] **Step 3: Add the gate prologue** as the FIRST statements of each method body. For the `(T, error)` returners (`Drive`, `CancelInstance`, `ResolveIncident`, `ReverseInstance`):

```go
	release, ok := driver.admit()
	if !ok {
		return engine.InstanceState{}, ErrDriverShuttingDown
	}
	defer release()
```

For the `error`-only returners (`DeliverMessage`, `BroadcastSignal`):

```go
	release, ok := driver.admit()
	if !ok {
		return ErrDriverShuttingDown
	}
	defer release()
```

(`Drive` and `ReverseInstance` open a trace span before other work — put the gate ABOVE the span so a rejected call does no work at all.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run '^TestExternalEntryPointsRejectedWhenDraining$' ./runtime/...`
Expected: PASS.
Regression: `go test ./runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/processdriver.go runtime/processdriver_message.go runtime/processdriver_signal.go runtime/processdriver_cancel.go runtime/processdriver_incident.go runtime/processdriver_reverse.go runtime/driver_shutdown_test.go
git commit -m "feat(runtime): gate Drive/Deliver/Broadcast/Cancel/Resolve/Reverse on shutdown

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Timer callbacks — continuation vs new-instance

**Files:**
- Modify: `runtime/timerops.go` (`timerFireFunc`, `startTimerFireFunc`, `armTimer`)
- Test: `runtime/driver_shutdown_test.go`

**Interfaces:**
- Consumes: `reserveInternal`, `admit`, `applyTrigger` (Tasks 1/4).

- [ ] **Step 1: Write the failing test** — append to `runtime/driver_shutdown_test.go`. This asserts a timer-start fire is dropped once draining (no new instance created):

```go
func TestTimerStartFireDroppedWhenDraining(t *testing.T) {
	driver, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	driver.Shutdown(context.Background()) // sets draining; scheduler closed

	// startTimerFireFunc is unexported; exercise via the exported behaviour: build the
	// fire callback and invoke it, asserting it does NOT create an instance while draining.
	// (Use an export_test.go hook: expose func (d *ProcessDriver) StartTimerFireFuncForTest(...)
	//  returning the callback, OR assert via a metric/log. Prefer the export_test hook.)
	fire := driver.StartTimerFireFuncForTest(twoStartTimerDef(t), "start", "start-timer:...:...")
	fire() // must be a no-op: draining rejects createAtNode's admit
	// Assert no instance exists (store is empty) — list instances and expect zero.
	page, err := driver.ListInstancesForTest(context.Background())
	require.NoError(t, err)
	assert.Empty(t, page, "timer-start must not create an instance during shutdown")
}
```

Add the `export_test.go` hooks in `runtime/export_test.go` (already exists):

```go
func (d *ProcessDriver) StartTimerFireFuncForTest(def *model.ProcessDefinition, nodeID, timerID string) func() {
	return d.startTimerFireFunc(def, nodeID, timerID)
}
```

If a store-listing hook is awkward, instead assert via the `timerFired`/created-instance count using the existing metrics test helpers; keep whichever is simplest against the real API.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestTimerStartFireDroppedWhenDraining$' ./runtime/...`
Expected: FAIL — `startTimerFireFunc` currently calls `createAtNode` unconditionally and creates the instance.

- [ ] **Step 3: Gate `startTimerFireFunc`** in `runtime/timerops.go` — add the admit prologue inside the returned closure, above `fireCtx`:

```go
	return func() {
		release, ok := driver.admit()
		if !ok {
			driver.obs.tel.Logger.LogAttrs(context.Background(), slog.LevelDebug,
				"runtime: timer-start fire skipped: driver shutting down",
				slog.String("timer_id", timerID), slog.String("def_id", def.ID))
			return
		}
		defer release()
		fireCtx := context.Background()
		// ...existing body...
	}
```

- [ ] **Step 4: Reserve-internal + ungated apply in `timerFireFunc`** — in the returned closure, add `release := driver.reserveInternal(); defer release()` above `fireCtx`, and change the `driver.ApplyTrigger(fireCtx, ...)` call at line 165 to `driver.applyTrigger(fireCtx, ...)` (ungated: this is a counted continuation, must complete during drain, never rejected):

```go
	return func() {
		release := driver.reserveInternal()
		defer release()
		fireCtx := context.Background()
		trg := engine.NewTimerFired(driver.clk.Now(), timerID)
		driver.obs.timerFired.Add(fireCtx, 1)
		const maxAttempts = 5
		var err error
		for range maxAttempts {
			if _, err = driver.applyTrigger(fireCtx, def, instanceID, trg); err == nil {
				return
			}
			// ...unchanged CAS-retry / error logging...
		}
		// ...unchanged permanent-drop log...
	}
```

- [ ] **Step 5: Finding-4 WARN clarification in `armTimer`** — extend the existing WARN in `armTimer` (around line 136) to note the shutdown case, so a skip during drain is not mistaken for a lost timer. Add a `slog.Bool("driver_shutting_down", driver.IsShuttingDown())` attribute to the existing WARN log call, and append to the log message context that the durable arm rehydrates on next boot. Do the same in `armStartTimer`'s WARN. No behavioural change.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -run '^TestTimerStartFireDroppedWhenDraining$' ./runtime/...`
Expected: PASS.
Regression (timer suites): `go test -run 'Timer|Rehydrate|Recurring' ./runtime/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add runtime/timerops.go runtime/export_test.go runtime/driver_shutdown_test.go
git commit -m "feat(runtime): timer-start drops on drain; timer continuation counted + ungated

Also clarifies armTimer WARN for the shutdown skip case (audit Finding 4).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Finding-3 deadline regression test

**Files:**
- Modify: `runtime/processdriver_shutdown_test.go`

- [ ] **Step 1: Write the failing test** — append to `runtime/processdriver_shutdown_test.go`. It proves the scheduler-close race honours a tight ctx deadline. Because the default gocron scheduler with nothing scheduled closes fast, simulate a slow close via a consumer-injected scheduler whose `Close` blocks:

```go
type slowCloseScheduler struct{ closeStarted chan struct{} }

func (s *slowCloseScheduler) Schedule(context.Context, string, schedule.TriggerSpec, func()) (time.Time, error) {
	return time.Time{}, nil
}
func (s *slowCloseScheduler) Cancel(context.Context, string)   {}
func (s *slowCloseScheduler) NextRun(string) (time.Time, bool) { return time.Time{}, false }
func (s *slowCloseScheduler) Close() error {
	close(s.closeStarted)
	time.Sleep(2 * time.Second) // longer than the test's ctx deadline
	return nil
}
```

Note: a consumer-injected scheduler is NOT registered in the driver's ShutdownGroup, so its `Close` is never called by `Shutdown` — this scenario cannot use `WithScheduler`. Instead assert Finding-3 on the **owned** default scheduler: give `Shutdown` a already-expired ctx and assert it returns promptly with a ctx-derived error rather than blocking on gocron's full stop timeout:

```go
func TestShutdownHonoursCtxDeadline(t *testing.T) {
	driver, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	require.NoError(t, driver.Start(t.Context())) // start the owned gocron scheduler

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // ensure ctx is already expired

	start := time.Now()
	err = driver.Shutdown(ctx)
	assert.Less(t, time.Since(start), 500*time.Millisecond,
		"Shutdown must return promptly on an expired ctx, not block on gocron's stop timeout")
	// The scheduler close raced against ctx: err carries ctx.DeadlineExceeded (joined).
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestShutdownHonoursCtxDeadline$' ./runtime/...`
Expected: On the PRE-Task-3 code this fails (AddCloser ignored ctx). After Task 3 it should pass — so if Task 3 is already merged, write this test FIRST against a temporary revert, or treat it as a lock-in test: run it and confirm PASS, and confirm it FAILS if the `shutdown.Add` race is reverted to `AddCloser`. Document the manual revert-check in the commit message.

- [ ] **Step 3: Run test to verify it passes**

Run: `go test -run '^TestShutdownHonoursCtxDeadline$' ./runtime/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add runtime/processdriver_shutdown_test.go
git commit -m "test(runtime): lock scheduler close honours Shutdown ctx deadline (Finding 3)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: `service.Engine` human-task early reject + propagation tests

**Files:**
- Modify: `service/service.go` (`ClaimTask`, `CompleteTask`, `ReassignTask`)
- Create: `service/shutdown_test.go`

**Interfaces:**
- Consumes: `runtime.ErrDriverShuttingDown`, `(*runtime.ProcessDriver).IsShuttingDown()`.

- [ ] **Step 1: Write the failing test** — `service/shutdown_test.go` (black-box `service_test`):

```go
func TestEngineHumanTaskRejectedDuringShutdown(t *testing.T) {
	eng, err := service.NewEngine( /* default in-memory wiring, see service/service_test.go */ )
	require.NoError(t, err)
	require.NoError(t, eng.Shutdown(context.Background()))

	_, err = eng.ClaimTask(context.Background(), service.ClaimTaskRequest{TaskToken: "t", Actor: "a"})
	assert.ErrorIs(t, err, runtime.ErrDriverShuttingDown)
}

func TestEngineStartInstanceRejectedDuringShutdown(t *testing.T) {
	eng, err := service.NewEngine( /* ... */ )
	require.NoError(t, err)
	require.NoError(t, eng.Shutdown(context.Background()))

	_, err = eng.StartInstance(context.Background(), service.StartInstanceRequest{ /* ... */ })
	assert.ErrorIs(t, err, runtime.ErrDriverShuttingDown)
}
```

Model the `NewEngine` construction and request structs on the existing `service/service_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run '^TestEngine.*Shutdown$' ./service/...`
Expected: FAIL — `ClaimTask` calls `e.tasks.Claim` (task-store write) before the driver gate, so it returns a task-store error, not `ErrDriverShuttingDown`; `StartInstance` may already pass via the driver gate (confirm — if it already passes, keep it as a lock-in test).

- [ ] **Step 3: Add early reject** at the top of `ClaimTask`, `CompleteTask`, `ReassignTask` in `service/service.go`, before the `e.tasks.*` call:

```go
	if e.driver.IsShuttingDown() {
		return nil, fmt.Errorf("workflow-service: claim task: %w", runtime.ErrDriverShuttingDown)
	}
```

(Adjust the wrapped verb per method: "complete task", "reassign task". Add the `runtime` import to `service/service.go` if not already present.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run '^TestEngine.*Shutdown$' ./service/...`
Expected: PASS.
Regression: `go test ./service/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add service/service.go service/shutdown_test.go
git commit -m "feat(service): reject human-task ops before side effects during shutdown

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: ADR + CHANGELOG + godoc

**Files:**
- Create: `docs/adr/0133-graceful-shutdown.md`
- Modify: `CHANGELOG.md`
- Modify: `runtime/processdriver.go` (godoc on `Shutdown`/`Start` to describe drain + admission)

- [ ] **Step 1: Write the ADR** `docs/adr/0133-graceful-shutdown.md` using the Nygard template (Status/Date, Context, Decision, Consequences), following `docs/adr/0001-record-architecture-decisions.md`. Content: summarise the audit findings, the decisions D1–D3 from the spec, the admit/drain/deadline mechanism, and the two bug fixes. Reference the spec path.

- [ ] **Step 2: Update `CHANGELOG.md`** under the unreleased/v0.1.0 section:

```markdown
### Added
- `ProcessDriver.Shutdown` now performs graceful shutdown: it rejects new work with
  `ErrDriverShuttingDown` and drains in-flight instances before returning, bounded by the
  `ctx` deadline (or `WithShutdownTimeout` fallback). Returns `ErrDrainTimeout` on expiry.
- `runtime.WithShutdownTimeout(d)` option; `ProcessDriver.IsShuttingDown()`.

### Fixed
- `ProcessDriver.Shutdown(ctx)` now honours the `ctx` deadline when closing the owned
  scheduler (previously ignored — the close used gocron's internal stop timeout).
```

- [ ] **Step 3: Update godoc** on `Shutdown` and `Start` in `runtime/processdriver.go` to document the new admission + drain semantics, `ErrDriverShuttingDown`, `ErrDrainTimeout`, and the ordering invariant (brief).

- [ ] **Step 4: Verify build + docs**

Run: `go build ./... && go vet ./runtime/... ./service/...`
Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0133-graceful-shutdown.md CHANGELOG.md runtime/processdriver.go
git commit -m "docs(runtime): ADR-0133 + CHANGELOG + godoc for graceful shutdown

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Full verification, review, merge

- [ ] **Step 1: Full test + race + coverage**

Run: `go test -race -coverprofile=cover.out ./runtime/... ./service/... && go tool cover -func=cover.out | tail -1`
Expected: PASS, no races, coverage ≥ 85% on touched packages.

- [ ] **Step 2: Workspace regression**

Run: `go test ./...`
Expected: PASS (examples, transport, persistence unaffected).

- [ ] **Step 3: Lint**

Run: `golangci-lint run ./...`
Expected: clean.

- [ ] **Step 4: Whole-branch code review + security review**

Run `/code-review` over the branch diff and `/security-review`; adjudicate and fix every finding (the working cadence: fix ALL findings before landing).

- [ ] **Step 5: Merge to main (`--no-ff`) and push**

```bash
git fetch origin && git merge origin/main   # per the mid-work advance GOTCHA
git checkout main && git merge --no-ff feat/graceful-shutdown
git push origin main
```

Ask the user before merging/pushing (Git Discipline).

## Self-Review (author checklist — completed)

**Spec coverage:**
- § 4 core mechanism → Task 1. § 7 `WithShutdownTimeout` → Task 2. § 6 shutdown sequence + § 8 Finding 3 → Task 3 + Task 7. § 5 admission gate (public ApplyTrigger split + internal redirects) → Task 4; remaining entry points → Task 5. § 5 timer continuation vs new-instance + § 8 Finding 4 → Task 6. § 9 Engine propagation → Task 8. ADR/CHANGELOG → Task 9. Verification → Task 10. **No gaps.**
- D1 strict quiescence → Tasks 4/5/6 (all external entries + timer-start reject; timer continuation completes). D2 wait-not-cancel → Task 3 (`waitInflight` returns `ErrDrainTimeout`, no cancel of in-flight). D3 opt-in fallback → Task 2 + Task 3.

**Placeholder scan:** the two `/* ... */` markers (barrier def body in Task 3, NewEngine wiring in Task 8) are explicit "mirror the existing repo helper" instructions with the named source file, not vague TODOs — each names the exact existing test to copy from. All other steps carry complete code.

**Type consistency:** `admit() (func(), bool)`, `reserveInternal() func()`, `IsShuttingDown() bool`, `waitInflight(ctx) error`, `effectiveShutdownCtx(ctx) (ctx, cancel)`, `applyTrigger(ctx, def, id, trg) (InstanceState, error)`, `WithShutdownTimeout(d) Option`, sentinels `ErrDriverShuttingDown`/`ErrDrainTimeout` — used identically across Tasks 1–8.

**Open confirmations for the implementer (verify against source before coding, do not assume):**
- `action` package constructor/register + `ActionFunc`/`Input`/`Output` names (Task 3 barrier). 
- The `DefinitionBuilder` fluent method names for building `barrierDef`/`twoStepDef` (reuse an existing package test helper if one exists).
- `ReverseInstance`'s zero-option call form and `ResolveIncident`'s parameter order (Task 5 table).
- `service.NewEngine` default wiring + request struct fields (Task 8) — copy from `service/service_test.go`.
