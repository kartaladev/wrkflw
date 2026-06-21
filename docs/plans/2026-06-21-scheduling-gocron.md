# Implementation plan — Scheduling (gocron)

Spec: `docs/specs/2026-06-21-scheduling-gocron-design.md`
ADR: `docs/adr/0009-gocron-scheduler.md`

## Goal

Ship a production `runtime.Scheduler` backed by gocron v2.21.2, sharing the
engine's clockwork time source so a single fake-clock `Advance` deterministically
drives both engine timestamps and timer firing. Concrete impl lives in
`internal/scheduling/gocron/`; a consumer façade lives in `scheduling/` (ADR-0008
template). `MemScheduler` is kept. Rehydration-on-restart is deferred.

## Architecture

- `internal/scheduling/gocron/scheduler.go` — `*GocronScheduler` wrapping
  `gocron.Scheduler`; `sync.Mutex`-guarded `map[string]uuid.UUID` (timerID → job
  ID); `Schedule`/`Cancel`/`Close`; `AfterJobRuns` listener prunes the map.
- `scheduling/scheduler.go` — `Scheduler` façade + `NewScheduler(clk
  clockwork.Clock) (*Scheduler, error)`; satisfies `runtime.Scheduler` +
  `io.Closer`; delegates to the internal impl.
- Capstone e2e wires `scheduling.NewScheduler(fakeClock)` into a real
  `runtime.NewRunner(..., runtime.WithScheduler(sched))` and proves a
  timer-intermediate process resumes to Completed under one shared fake clock.

The port (`runtime.Scheduler`) and runtime are **unchanged**; only new packages
are added.

## Tech Stack

- Go 1.25.7 (per `go.mod`).
- `github.com/go-co-op/gocron/v2@v2.21.2` (hard pin).
- `github.com/jonboulle/clockwork v0.5.0` (already pinned; has
  `BlockUntilContext`).
- `github.com/google/uuid` (transitive via gocron; job IDs).
- Existing: `github.com/zakyalvan/krtlwrkflw/{runtime,engine,action,model,clock}`.

## Global Constraints

Go 1.25; gocron pinned `v2.21.2`; clockwork v0.5.0; NEVER import gocron from
engine/runtime/model/workflow code (only `internal/scheduling/`+`scheduling/`);
never import clockwork outside the scheduling adapter + `_test.go` files (engine
depends on `clock.Clock`); TDD strict with VISIBLE red→green; black-box tests
(`package x_test`); table tests use the `assert` closure form (not want/wantErr);
`t.Context()`; pair each foo.go with foo_test.go; ≥85% coverage on touched
packages; conventional commits ending with
`Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## Task 1 — `GocronScheduler` (`internal/scheduling/gocron`)

The bulk. Add the gocron dependency and implement the wrapper with the
timerID→uuid map + `AfterJobRuns` cleanup. TDD against the deterministic
shared-fake-clock pattern.

**Files:**

- `internal/scheduling/gocron/scheduler.go` (impl)
- `internal/scheduling/gocron/scheduler_test.go` (black-box, `package gocron_test`)

**Interfaces / signatures:**

```go
func NewGocronScheduler(clk clockwork.Clock) (*GocronScheduler, error)
func (s *GocronScheduler) Schedule(timerID string, fireAt time.Time, fire func())
func (s *GocronScheduler) Cancel(timerID string)
func (s *GocronScheduler) Close() error
```

### Steps

- [ ] **Add the gocron dependency (hard pin).**

  ```bash
  go get github.com/go-co-op/gocron/v2@v2.21.2
  go mod tidy
  ```

  Confirm `go.mod` shows `github.com/go-co-op/gocron/v2 v2.21.2` and that
  `github.com/jonboulle/clockwork` is still `v0.5.0` (gocron must not bump it).

- [ ] **RED — fires-at-time test.** Create
  `internal/scheduling/gocron/scheduler_test.go`. This will not compile
  (`undefined: NewGocronScheduler`) — that is the red state. Run
  `go test ./internal/scheduling/gocron/...` and observe the failure.

  ```go
  package gocron_test

  import (
      "sync"
      "testing"
      "time"

      "github.com/jonboulle/clockwork"
      "github.com/stretchr/testify/require"

      sched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
  )

  func TestGocronScheduler_FiresAtTime(t *testing.T) {
      fakeClock := clockwork.NewFakeClock()
      s, err := sched.NewGocronScheduler(fakeClock)
      require.NoError(t, err)
      t.Cleanup(func() { _ = s.Close() })

      var wg sync.WaitGroup
      wg.Add(1)
      s.Schedule("t1", fakeClock.Now().Add(5*time.Second), func() { wg.Done() })

      // MANDATORY barrier: wait until gocron armed its timer (1 waiter) before
      // advancing, else Advance can outrun the arm and the timer never fires.
      require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
      fakeClock.Advance(5 * time.Second)
      wg.Wait() // executor goroutine actually ran the task
  }
  ```

- [ ] **GREEN — implement `NewGocronScheduler`/`Schedule`/`Close`.** Create
  `internal/scheduling/gocron/scheduler.go`. Run
  `go test ./internal/scheduling/gocron/...` → green.

  ```go
  // Package gocron is the concrete gocron v2-backed Scheduler implementation
  // (ADR-0009). It is internal: consumers reach it only through the module-root
  // scheduling façade. gocron and clockwork are imported here only — never from
  // engine/runtime/model code.
  package gocron

  import (
      "errors"
      "log/slog"
      "sync"
      "time"

      "github.com/go-co-op/gocron/v2"
      "github.com/google/uuid"
      "github.com/jonboulle/clockwork"
  )

  // GocronScheduler is a production runtime.Scheduler backed by gocron v2. It
  // shares the engine's clockwork time source so one fake-clock advance drives
  // both engine timestamps and timer firing (ADR-0003, ADR-0009).
  type GocronScheduler struct {
      sched gocron.Scheduler

      mu   sync.Mutex
      jobs map[string]uuid.UUID // timerID -> gocron job ID
  }

  // NewGocronScheduler constructs and starts a gocron-backed scheduler driven by
  // clk. The caller must Close it to avoid leaking gocron's executor goroutine.
  func NewGocronScheduler(clk clockwork.Clock) (*GocronScheduler, error) {
      s, err := gocron.NewScheduler(gocron.WithClock(clk))
      if err != nil {
          return nil, err
      }
      s.Start() // non-blocking
      return &GocronScheduler{
          sched: s,
          jobs:  make(map[string]uuid.UUID),
      }, nil
  }

  // Schedule registers a one-time timer that calls fire at or after fireAt. If a
  // timer with the same timerID already exists it is replaced. Best-effort: a
  // gocron job-creation error is logged and the timer is not armed.
  func (s *GocronScheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
      s.mu.Lock()
      defer s.mu.Unlock()

      if existing, ok := s.jobs[timerID]; ok {
          _ = s.sched.RemoveJob(existing) // ignore ErrJobNotFound: already fired/pruned
          delete(s.jobs, timerID)
      }

      job, err := s.sched.NewJob(
          gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(fireAt)),
          gocron.NewTask(fire),
          gocron.WithEventListeners(gocron.AfterJobRuns(func(uuid.UUID, string) {
              s.mu.Lock()
              delete(s.jobs, timerID)
              s.mu.Unlock()
          })),
      )
      if err != nil {
          slog.Error("gocron: schedule timer failed", "timerID", timerID, "error", err)
          return
      }
      s.jobs[timerID] = job.ID()
  }

  // Cancel removes a pending timer. No-op if the timer is unknown or already fired.
  func (s *GocronScheduler) Cancel(timerID string) {
      s.mu.Lock()
      defer s.mu.Unlock()

      id, ok := s.jobs[timerID]
      if !ok {
          return // unknown id: safe no-op
      }
      delete(s.jobs, timerID)
      if err := s.sched.RemoveJob(id); err != nil && !errors.Is(err, gocron.ErrJobNotFound) {
          slog.Error("gocron: cancel timer failed", "timerID", timerID, "error", err)
      }
  }

  // Close shuts gocron down gracefully. The scheduler cannot be reused afterward.
  func (s *GocronScheduler) Close() error {
      return s.sched.Shutdown()
  }
  ```

- [ ] **RED — cancel-prevents-fire + replace + cancel-unknown + runs-once table.**
  Extend `scheduler_test.go`. Fold the behavioural cases into one table-driven
  test using the project `assert` closure form (each case asserts on a shared
  `*GocronScheduler` it sets up). Run → RED for the new cases
  (`fired` helper / counter not yet asserted as the impl is present, so prove RED
  by first writing an assertion that must fail, e.g. assert `replace` fires twice,
  watch it fail, then correct to once). Keep `t.Context()`.

  ```go
  func TestGocronScheduler_Behaviour(t *testing.T) {
      type tc struct {
          name   string
          assert func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock)
      }

      // counter returns an atomically-incrementing fire callback and a reader.
      counter := func() (func(), func() int64) {
          var n atomic.Int64
          return func() { n.Add(1) }, func() int64 { return n.Load() }
      }

      cases := []tc{
          {
              name: "cancel prevents fire",
              assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
                  fire, count := counter()
                  s.Schedule("c1", clk.Now().Add(5*time.Second), fire)
                  require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
                  s.Cancel("c1")
                  clk.Advance(10 * time.Second)
                  // No barrier to wait on; assert it never fires within a short window.
                  require.Never(t, func() bool { return count() > 0 },
                      200*time.Millisecond, 10*time.Millisecond)
              },
          },
          {
              name: "replace reschedules and fires once",
              assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
                  var wg sync.WaitGroup
                  wg.Add(1)
                  var n atomic.Int64
                  fire := func() { n.Add(1); wg.Done() }

                  s.Schedule("r1", clk.Now().Add(5*time.Second), func() { t.Error("stale timer fired") })
                  require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
                  s.Schedule("r1", clk.Now().Add(10*time.Second), fire) // replace
                  require.NoError(t, clk.BlockUntilContext(t.Context(), 1))

                  clk.Advance(5 * time.Second)
                  require.Never(t, func() bool { return n.Load() > 0 },
                      150*time.Millisecond, 10*time.Millisecond) // old T+5 must not fire
                  clk.Advance(5 * time.Second)                    // now at T+10
                  wg.Wait()
                  require.Equal(t, int64(1), n.Load())
              },
          },
          {
              name: "cancel unknown is a no-op",
              assert: func(t *testing.T, s *sched.GocronScheduler, _ *clockwork.FakeClock) {
                  require.NotPanics(t, func() { s.Cancel("does-not-exist") })
              },
          },
          {
              name: "callback runs exactly once",
              assert: func(t *testing.T, s *sched.GocronScheduler, clk *clockwork.FakeClock) {
                  var wg sync.WaitGroup
                  wg.Add(1)
                  var n atomic.Int64
                  s.Schedule("o1", clk.Now().Add(time.Second), func() { n.Add(1); wg.Done() })
                  require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
                  clk.Advance(time.Second)
                  wg.Wait()
                  require.Never(t, func() bool { return n.Load() > 1 },
                      150*time.Millisecond, 10*time.Millisecond)
              },
          },
      }

      for _, c := range cases {
          t.Run(c.name, func(t *testing.T) {
              clk := clockwork.NewFakeClock()
              s, err := sched.NewGocronScheduler(clk)
              require.NoError(t, err)
              t.Cleanup(func() { _ = s.Close() })
              c.assert(t, s, clk)
          })
      }
  }
  ```

  Add the needed imports to the test file: `sync/atomic`, `sync`, `time`,
  `testing`, `clockwork`, `require`, `sched`.

- [ ] **GREEN — confirm all cases pass.** The impl from the prior step already
  satisfies replace/cancel/once semantics. Run
  `go test -race ./internal/scheduling/gocron/...` → green.

- [ ] **Coverage gate.**
  `go test -race -coverprofile=cover.out ./internal/scheduling/gocron/... && go tool cover -func=cover.out | tail -1`
  ≥ 85%. The `slog.Error` best-effort branches are unlikely to be hit; if coverage
  dips below 85%, add a case that forces a `NewJob` error (e.g. a `fireAt` in the
  past combined with gocron's start-date validation) or accept the documented
  uncovered error-log lines if still ≥85%.

**Deliverable:** a working, race-clean gocron `Scheduler` impl with ≥85% coverage.

---

## Task 2 — `scheduling` root façade (`scheduling/`)

Consumer-facing façade returning a type implementing `runtime.Scheduler` +
`io.Closer`, delegating to the internal `GocronScheduler`.

**Files:**

- `scheduling/scheduler.go` (façade)
- `scheduling/scheduler_test.go` (black-box, `package scheduling_test`)

**Interfaces / signatures:**

```go
func NewScheduler(clk clockwork.Clock) (*Scheduler, error)
func (s *Scheduler) Schedule(timerID string, fireAt time.Time, fire func())
func (s *Scheduler) Cancel(timerID string)
func (s *Scheduler) Close() error
```

No `...Option` variadic in v1 (no real knob; avoids the unusable-option mistake).

### Steps

- [ ] **RED — façade construct + fire-through test.** Create
  `scheduling/scheduler_test.go`. Will not compile (`undefined: NewScheduler`) —
  red. Run `go test ./scheduling/...` and observe.

  ```go
  package scheduling_test

  import (
      "io"
      "sync"
      "testing"
      "time"

      "github.com/jonboulle/clockwork"
      "github.com/stretchr/testify/require"

      "github.com/zakyalvan/krtlwrkflw/runtime"
      "github.com/zakyalvan/krtlwrkflw/scheduling"
  )

  func TestNewScheduler_SatisfiesPortAndFires(t *testing.T) {
      fakeClock := clockwork.NewFakeClock()
      s, err := scheduling.NewScheduler(fakeClock)
      require.NoError(t, err)
      t.Cleanup(func() { _ = s.Close() })

      // Compile-time-style runtime checks that the façade satisfies the contracts.
      var _ runtime.Scheduler = s
      var _ io.Closer = s

      var wg sync.WaitGroup
      wg.Add(1)
      s.Schedule("t1", fakeClock.Now().Add(3*time.Second), func() { wg.Done() })
      require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
      fakeClock.Advance(3 * time.Second)
      wg.Wait()
  }
  ```

- [ ] **GREEN — implement the façade.** Create `scheduling/scheduler.go`. Run
  `go test -race ./scheduling/...` → green.

  ```go
  // Package scheduling is the consumer-facing façade over the internal gocron
  // scheduler (ADR-0008, ADR-0009). It is the only scheduling package consumers
  // import; the concrete gocron impl stays in internal/scheduling/gocron.
  package scheduling

  import (
      "io"
      "time"

      "github.com/jonboulle/clockwork"

      gocronsched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
      "github.com/zakyalvan/krtlwrkflw/runtime"
  )

  // Scheduler is the production, gocron-backed runtime.Scheduler. Construct it
  // with NewScheduler, passing the SAME clockwork.Clock instance used to build
  // the runtime (clock.Clock), so one fake-clock advance drives engine + scheduler
  // together under test (ADR-0003). Close it to release gocron's goroutine.
  type Scheduler struct {
      impl *gocronsched.GocronScheduler
  }

  // Compile-time contract checks.
  var (
      _ runtime.Scheduler = (*Scheduler)(nil)
      _ io.Closer         = (*Scheduler)(nil)
  )

  // NewScheduler constructs a started gocron-backed scheduler driven by clk.
  func NewScheduler(clk clockwork.Clock) (*Scheduler, error) {
      impl, err := gocronsched.NewGocronScheduler(clk)
      if err != nil {
          return nil, err
      }
      return &Scheduler{impl: impl}, nil
  }

  // Schedule registers a timer; replaces any existing timer with the same id.
  func (s *Scheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
      s.impl.Schedule(timerID, fireAt, fire)
  }

  // Cancel removes a pending timer; no-op if unknown or already fired.
  func (s *Scheduler) Cancel(timerID string) { s.impl.Cancel(timerID) }

  // Close shuts the scheduler down gracefully.
  func (s *Scheduler) Close() error { return s.impl.Close() }
  ```

- [ ] **Coverage gate.**
  `go test -race -coverprofile=cover.out ./scheduling/... && go tool cover -func=cover.out | tail -1`
  ≥ 85% (thin delegation; the construct + fire test exercises all four methods —
  `Cancel` is covered indirectly; if not, add a one-line `s.Cancel("x")` no-op
  assertion to the test).

**Deliverable:** a root-package `scheduling.Scheduler` that drop-in satisfies the
runtime port and closes cleanly.

---

## Task 3 — Runner integration e2e (capstone proof)

Prove the gocron scheduler drives the engine identically to `MemScheduler`: wire
`scheduling.NewScheduler(fakeClock)` into a real `Runner` with a tiny
timer-intermediate process, run to park, advance the SHARED fake clock (with the
barrier), and assert the instance resumes to Completed. No testcontainers — pure
in-memory store + fake clock.

**Files:**

- `scheduling/runner_e2e_test.go` (black-box, `package scheduling_test`)

Reuse the timer-intermediate definition shape from `runtime/timer_example_test.go`
(`start → timer-catch → service → end`).

### Steps

- [ ] **RED — e2e resume-under-shared-fake-clock test.** Create
  `scheduling/runner_e2e_test.go`. It compiles against existing runtime symbols
  but RED first by asserting the WRONG terminal status (e.g.
  `engine.StatusRunning`) to observe a real failing assertion, then flip to
  `engine.StatusCompleted`. Run `go test ./scheduling/...` and observe RED.

  ```go
  package scheduling_test

  import (
      "context"
      "testing"
      "time"

      "github.com/jonboulle/clockwork"
      "github.com/stretchr/testify/assert"
      "github.com/stretchr/testify/require"

      "github.com/zakyalvan/krtlwrkflw/action"
      "github.com/zakyalvan/krtlwrkflw/engine"
      "github.com/zakyalvan/krtlwrkflw/model"
      "github.com/zakyalvan/krtlwrkflw/runtime"
      "github.com/zakyalvan/krtlwrkflw/scheduling"
  )

  func timerIntermediateDef() *model.ProcessDefinition {
      return &model.ProcessDefinition{
          ID:      "timer-intermediate",
          Version: 1,
          Nodes: []model.Node{
              {ID: "start", Kind: model.KindStartEvent},
              {ID: "wait1h", Kind: model.KindIntermediateCatchEvent, TimerDuration: `"1h"`},
              {ID: "greet", Kind: model.KindServiceTask, Action: "greet"},
              {ID: "end", Kind: model.KindEndEvent},
          },
          Flows: []model.SequenceFlow{
              {ID: "f1", Source: "start", Target: "wait1h"},
              {ID: "f2", Source: "wait1h", Target: "greet"},
              {ID: "f3", Source: "greet", Target: "end"},
          },
      }
  }

  // TestGocronSchedulerDrivesRunnerToCompletion proves the gocron-backed scheduler
  // drives a real Runner identically to MemScheduler: ONE shared fake clock is the
  // runner's clock.Clock AND the scheduler's clockwork.Clock.
  func TestGocronSchedulerDrivesRunnerToCompletion(t *testing.T) {
      ctx := t.Context()

      startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
      fc := clockwork.NewFakeClockAt(startAt) // ONE shared instance

      serviceRan := make(chan struct{})
      cat := action.NewMapCatalog(map[string]action.ServiceAction{
          "greet": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
              close(serviceRan)
              return map[string]any{"greeted": true}, nil
          }),
      })

      sched, err := scheduling.NewScheduler(fc) // same fc, as clockwork.Clock
      require.NoError(t, err)
      t.Cleanup(func() { _ = sched.Close() })

      store := runtime.NewMemStore()
      r := runtime.NewRunner(cat, fc, store, runtime.WithScheduler(sched)) // same fc, as clock.Clock

      def := timerIntermediateDef()
      const instanceID = "gocron-e2e-1"

      // Run → parks at the intermediate timer node.
      parked, err := r.Run(ctx, def, instanceID, nil)
      require.NoError(t, err)
      assert.Equal(t, engine.StatusRunning, parked.Status)
      require.Len(t, parked.Tokens, 1)
      assert.Equal(t, "wait1h", parked.Tokens[0].NodeID)

      // Barrier: wait until gocron armed the 1h timer, then advance past FireAt.
      require.NoError(t, fc.BlockUntilContext(ctx, 1))
      fc.Advance(1*time.Hour + time.Second)

      // The scheduler fires the timer on its executor goroutine, which Delivers
      // TimerFired and runs the service action.
      select {
      case <-serviceRan:
      case <-time.After(2 * time.Second):
          t.Fatal("service action did not run after timer fired")
      }

      // The instance must reach Completed (assert eventually — Deliver runs async
      // on the executor goroutine).
      require.Eventually(t, func() bool {
          final, _, err := store.Load(ctx, instanceID)
          return err == nil && final.Status == engine.StatusCompleted
      }, 2*time.Second, 10*time.Millisecond, "instance must complete after gocron fires the timer")

      final, _, err := store.Load(ctx, instanceID)
      require.NoError(t, err)
      assert.Equal(t, true, final.Variables["greeted"])
      assert.Empty(t, final.Tokens)
  }
  ```

- [ ] **GREEN — make it pass.** No new production code is expected; this test
  exercises Task 1 + Task 2 through the real runtime. Run
  `go test -race ./scheduling/...` → green. If the fire callback's `Deliver`
  surfaces a wiring issue (e.g. the runner's fire callback path), debug per
  `superpowers:systematic-debugging` — do NOT change the engine/runtime contract;
  the bug, if any, is in the scheduler adapter.

  Note on async vs the `MemScheduler` analogue: `MemScheduler` fires
  synchronously inside `Tick`; gocron fires on its executor goroutine, so the test
  uses `BlockUntilContext` + `require.Eventually`/a channel rather than a
  synchronous post-`Tick` assertion. This is the only e2e difference — the engine
  path is identical.

**Deliverable:** a green capstone e2e proving gocron drives the real engine to
completion under one shared fake clock.

---

## Task 4 — Verification + HANDOVER

Run the full gate, enforce the import boundary, and update the handover.

**Files:**

- `docs/plans/HANDOVER.md` (update the Scheduling entry)

### Steps

- [ ] **Full race + coverage gate.**
  ```bash
  go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
  ```
  All green; ≥85% on `internal/scheduling/gocron` and `scheduling`. No regressions
  elsewhere (`MemScheduler` tests still pass — it was not touched).

- [ ] **Lint clean.**
  ```bash
  golangci-lint run ./...
  ```
  Zero issues.

- [ ] **Import-boundary check — gocron only under the scheduling packages.**
  ```bash
  go list -deps -f '{{.ImportPath}} {{join .Imports " "}}' \
    ./engine/... ./model/... ./runtime/... \
    | grep -E 'go-co-op/gocron' && echo "FORBIDDEN gocron import" && exit 1 || echo "ok: no gocron in engine/runtime/model"
  ```
  Must print `ok`. Also spot-check that clockwork is imported only by
  `scheduling`/`internal/scheduling` and `_test.go` files (consistent with
  ADR-0003), not by `engine`/`model`/`runtime` production code:
  ```bash
  grep -rl 'jonboulle/clockwork' engine model runtime --include='*.go' | grep -v '_test.go' || echo "ok: no clockwork in engine/model/runtime prod"
  ```

- [ ] **Update `docs/plans/HANDOVER.md`.** Replace the "Scheduling — gocron
  `Scheduler` (replace `MemScheduler`...)" bullet under "What's next" with a
  completed-sub-project section modeled on the Persistence one: what shipped
  (`internal/scheduling/gocron` impl, `scheduling` façade, capstone e2e), the
  ADR-0009 reference, the shared-clock mechanism, that `MemScheduler` is retained,
  the gate numbers, and the **deferred rehydration follow-up** (needs a persistence
  `ListPendingTimers` enumeration query; v1 ships firing + lifecycle only). Note
  the v1 caveat that a restart loses in-memory timer arming until rehydration
  lands.

- [ ] **Commit per logical change** (Conventional Commits, scope `scheduling`),
  each ending with the
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` trailer.

**Deliverable:** green gate, clean lint, enforced import boundary, updated
HANDOVER with the deferred rehydration follow-up recorded.

---

## Verification checklist

- [ ] gocron pinned exactly to `v2.21.2`; clockwork unchanged at `v0.5.0`.
- [ ] `internal/scheduling/gocron` impl: Schedule (replace), Cancel
      (unknown=no-op, ErrJobNotFound-safe), Close (Shutdown), AfterJobRuns map
      cleanup, mutex-guarded map.
- [ ] Every timer test uses the `BlockUntilContext(ctx, n)` arm barrier before
      `Advance`, and synchronizes on the callback actually running (WaitGroup /
      channel / `Eventually`), not on `Advance` returning.
- [ ] `scheduling.Scheduler` satisfies `runtime.Scheduler` + `io.Closer`
      (compile-time `var _` assertions); no unusable `...Option`.
- [ ] Capstone e2e: one shared fake clock is BOTH the runner's `clock.Clock` and
      the scheduler's `clockwork.Clock`; instance resumes to `StatusCompleted`.
- [ ] `MemScheduler` retained; its tests still pass.
- [ ] `go test -race ./...` green; ≥85% on touched packages; lint clean.
- [ ] gocron not imported from engine/runtime/model; clockwork not in their prod
      code.
- [ ] HANDOVER updated; rehydration deferred follow-up recorded.
