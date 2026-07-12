# Implementation Plan — Persistence (PostgreSQL 17)

**Goal.** Replace the three in-memory `runtime` persistence ports (`StateStore`,
`Journal`, `OutboxWriter`) with a single transactional `Store` port backed by
PostgreSQL 17, making snapshot + journal + outbox atomic per applied trigger
(transactional outbox), with optimistic-version concurrency, a broker-agnostic
outbox relay, a Postgres definition store + read-through definition cache, and a
consumer-facing `persistence/` façade over an `internal/persistence/postgres/`
implementation. Implements `docs/specs/2026-06-21-persistence-postgres-design.md`
exactly; honours ADR-0006 (snapshot-as-JSONB + projected columns), ADR-0007
(per-step atomic `Store` + optimistic concurrency, `NewRunner` port collapse),
ADR-0008 (façade over internal impl).

**Architecture.**

- `runtime/` — the `Store` / `JournalReader` ports + `MemStore` fake + the
  `deliverLoop` / `outboxEventsFor` refactor + `CachingDefinitionRegistry`
  decorator. Depends on interfaces only; no SQL, no pgx.
- `internal/persistence/postgres/` — concrete impl: the `DBTX` querier seam, the
  sealed-`Trigger` JSON codec, row mapping, embedded goose migrations, the
  Postgres `Store`, the Postgres definition store, the outbox `Relay`. Consumers
  never import it.
- `persistence/` (module root) — the consumer-facing façade: `OpenPostgres`,
  `Migrate`, definition-store / cache / `Relay` constructors, the `Publisher`
  interface, re-exported sentinels. Delegates inward (ADR-0008).
- `database/` — the testcontainers `RunTestDatabase(t, opts...)` helper
  (`use-testcontainers` skill), shared by all DB tests.

The atomic unit is **one `Step`** (spec §2): `Step` (pure) → `outboxEventsFor`
(pure) → `store.Commit` (one short tx: snapshot CAS + journal insert + outbox
inserts) → `perform` the non-outbox commands (external I/O, outside any tx).
Outbox-event derivation moves out of `perform` into the pure helper
`outboxEventsFor`.

**Tech Stack.** Go 1.25; PostgreSQL 17; `pgx` v5 + `pgxpool` (no `database/sql`);
`pressly/goose` embedded migrations via `SetBaseFS` + `embed.FS`;
testcontainers-go; module path `github.com/kartaladev/wrkflw`.

**Global Constraints.**

- Go 1.25; PostgreSQL 17; pgx v5 + pgxpool (no database/sql); goose embedded
  migrations (consumer-run, never auto); testcontainers-go via
  `database.RunTestDatabase(t, opts...)`.
- NEVER import watermill/casbin/gocron/clockwork from these packages.
- TDD strict with VISIBLE red→green per symbol (run `go test`, observe the
  failure, then implement). Never batch test+impl in one write; never write impl
  before its failing test.
- Black-box tests (`package x_test`).
- Table tests use the project `table-test` skill's `assert` closure form (NOT
  `want` / `wantErr` fields); use `t.Context()` over `context.Background()`.
- Pair each `foo.go` with `foo_test.go` (reserve `*_example_test.go` for genuine
  e2e/testable examples).
- mockgen via the `use-mockgen` skill (mocks live with the interface, `--typed`).
- ≥85% line coverage on touched packages.
- Conventional commits scoped to the area, each ending with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

**Pre-flight (run once before Task 1).**

- [ ] Add deps:
  ```bash
  go get github.com/jackc/pgx/v5@latest
  go get github.com/pressly/goose/v3@latest
  go get github.com/testcontainers/testcontainers-go@latest
  go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
  go mod tidy
  ```
- [ ] Confirm Docker daemon is running (testcontainers tasks need it):
  `docker info >/dev/null && echo OK`.

---

## Task 1 — Transactional `Store` ports + `MemStore` fake (`runtime`)

Implements spec §3 ports and the `MemStore` requirement. **Keep `OutboxEvent`**
(already declared in `runtime/memory.go`) — move/reconcile it into the ports file
and delete the old declaration as part of this task.

**Files**

- Modify: `runtime/ports.go` (add `Token`, `OutboxEvent`, `AppliedStep`,
  `ErrConcurrentUpdate`, `Store`, `JournalReader`; the old `StateStore` /
  `Journal` / `OutboxWriter` are deleted in Task 3, not here — leave them for
  now so the package keeps compiling).
- Create: `runtime/memstore.go` (the `MemStore` fake).
- Test: `runtime/ports_test.go` (value-type sanity), `runtime/memstore_test.go`.

**Interfaces**

- Produces:
  ```go
  type Token int64

  type OutboxEvent struct {
      Topic   string
      Payload map[string]any
  }

  type AppliedStep struct {
      State   engine.InstanceState
      Trigger engine.Trigger
      Events  []OutboxEvent
  }

  var ErrConcurrentUpdate = errors.New("runtime: concurrent update")

  type Store interface {
      Create(ctx context.Context, step AppliedStep) (Token, error)
      Load(ctx context.Context, id string) (engine.InstanceState, Token, error)
      Commit(ctx context.Context, expected Token, step AppliedStep) (Token, error)
  }

  type JournalReader interface {
      Entries(ctx context.Context, id string) ([]engine.Trigger, error)
  }
  ```
- `MemStore` satisfies `Store` + `JournalReader`; exposes `Events() []OutboxEvent`
  and (via `JournalReader`) `Entries`.

**Steps**

- [ ] 1.1 Write failing test `runtime/memstore_test.go` (`package runtime_test`)
  covering the round-trip + CAS contract. Use the `table-test` `assert` closure
  form:
  ```go
  package runtime_test

  import (
      "errors"
      "testing"
      "time"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/engine"
      "github.com/kartaladev/wrkflw/runtime"
  )

  func step(id, topic string) runtime.AppliedStep {
      return runtime.AppliedStep{
          State:   engine.InstanceState{InstanceID: id, Status: engine.StatusRunning},
          Trigger: engine.NewStartInstance(time.Unix(0, 0), map[string]any{"k": "v"}),
          Events:  []runtime.OutboxEvent{{Topic: topic, Payload: map[string]any{"x": 1}}},
      }
  }

  func TestMemStoreCreateLoadRoundTrip(t *testing.T) {
      ms := runtime.NewMemStore()
      tok, err := ms.Create(t.Context(), step("i1", "instance.completed"))
      require.NoError(t, err)

      st, loaded, err := ms.Load(t.Context(), "i1")
      require.NoError(t, err)
      require.Equal(t, "i1", st.InstanceID)
      require.Equal(t, tok, loaded)
  }

  func TestMemStoreLoadMissing(t *testing.T) {
      ms := runtime.NewMemStore()
      _, _, err := ms.Load(t.Context(), "nope")
      require.ErrorIs(t, err, runtime.ErrInstanceNotFound)
  }

  func TestMemStoreCommit(t *testing.T) {
      tests := map[string]struct {
          assert func(t *testing.T, ms *runtime.MemStore)
      }{
          "advances token": {
              assert: func(t *testing.T, ms *runtime.MemStore) {
                  tok, err := ms.Create(t.Context(), step("i1", "a"))
                  require.NoError(t, err)
                  next, err := ms.Commit(t.Context(), tok, step("i1", "b"))
                  require.NoError(t, err)
                  require.NotEqual(t, tok, next)
              },
          },
          "stale token conflicts": {
              assert: func(t *testing.T, ms *runtime.MemStore) {
                  tok, err := ms.Create(t.Context(), step("i1", "a"))
                  require.NoError(t, err)
                  _, err = ms.Commit(t.Context(), tok, step("i1", "b")) // advances past tok
                  require.NoError(t, err)
                  _, err = ms.Commit(t.Context(), tok, step("i1", "c")) // stale
                  require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)
              },
          },
          "captures outbox events": {
              assert: func(t *testing.T, ms *runtime.MemStore) {
                  tok, err := ms.Create(t.Context(), step("i1", "instance.completed"))
                  require.NoError(t, err)
                  _, err = ms.Commit(t.Context(), tok, step("i1", "instance.failed"))
                  require.NoError(t, err)
                  topics := make([]string, 0)
                  for _, e := range ms.Events() {
                      topics = append(topics, e.Topic)
                  }
                  require.Equal(t, []string{"instance.completed", "instance.failed"}, topics)
              },
          },
          "records journal entries": {
              assert: func(t *testing.T, ms *runtime.MemStore) {
                  tok, err := ms.Create(t.Context(), step("i1", "a"))
                  require.NoError(t, err)
                  _, err = ms.Commit(t.Context(), tok, step("i1", "b"))
                  require.NoError(t, err)
                  entries, err := ms.Entries(t.Context(), "i1")
                  require.NoError(t, err)
                  require.Len(t, entries, 2)
              },
          },
      }
      for name, tc := range tests {
          t.Run(name, func(t *testing.T) {
              tc.assert(t, runtime.NewMemStore())
          })
      }
  }

  var _ = errors.Is // keep errors import if unused after edits
  ```
- [ ] 1.2 Run it, expect FAIL (compile error: `undefined: runtime.Token`,
  `runtime.OutboxEvent`, `runtime.AppliedStep`, `runtime.NewMemStore`,
  `runtime.ErrConcurrentUpdate`):
  ```bash
  go test ./runtime/...
  ```
  Expected: `runtime/memstore_test.go: undefined: runtime.NewMemStore` (build fails).
- [ ] 1.3 Add the port types to `runtime/ports.go`. Append (do NOT remove the
  existing `StateStore`/`Journal`/`OutboxWriter`/`ErrInstanceNotFound` yet — they
  are deleted in Task 3), and add the `context` import:
  ```go
  // Token is an opaque optimistic-concurrency token (Postgres: a bigint version).
  type Token int64

  // OutboxEvent is one domain event to relay.
  type OutboxEvent struct {
      Topic   string
      Payload map[string]any
  }

  // AppliedStep is the atomic persistence unit for exactly one applied trigger:
  // the new snapshot, the trigger that produced it, and the outbox events derived
  // from the resulting commands.
  type AppliedStep struct {
      State   engine.InstanceState
      Trigger engine.Trigger
      Events  []OutboxEvent
  }

  // ErrConcurrentUpdate is returned by Store.Commit when the expected token is
  // stale (a concurrent writer advanced the instance first).
  var ErrConcurrentUpdate = errors.New("runtime: concurrent update")

  // Store is the transactional persistence port the Runner depends on. Commit
  // persists snapshot + journal + outbox atomically per applied trigger.
  type Store interface {
      Create(ctx context.Context, step AppliedStep) (Token, error)
      Load(ctx context.Context, id string) (engine.InstanceState, Token, error)
      Commit(ctx context.Context, expected Token, step AppliedStep) (Token, error)
  }
  ```
  Then delete the old `OutboxEvent` declaration from `runtime/memory.go` (lines
  43–47) to avoid a duplicate-type compile error, and change the `JournalReader`
  in `ports.go` to the new ctx-taking signature:
  ```go
  // JournalReader exposes the recorded trigger history for replay/audit.
  type JournalReader interface {
      Entries(ctx context.Context, id string) ([]engine.Trigger, error)
  }
  ```
  (The old `MemJournal.Entries(id string)` in `memory.go` no longer satisfies it;
  that is fine — `memory.go`'s `_ JournalReader = (*MemJournal)(nil)` assertion is
  removed in Task 3. To keep the package compiling **now**, temporarily delete the
  `_ JournalReader = (*MemJournal)(nil)` line in `memory.go`.)
- [ ] 1.4 Create `runtime/memstore.go`:
  ```go
  package runtime

  import (
      "context"

      "github.com/kartaladev/wrkflw/engine"
  )

  // Compile-time checks: MemStore satisfies both ports.
  var (
      _ Store         = (*MemStore)(nil)
      _ JournalReader = (*MemStore)(nil)
  )

  // memInstance is the in-memory record for one instance.
  type memInstance struct {
      state   engine.InstanceState
      version Token
  }

  // MemStore is an in-memory transactional Store + JournalReader for tests and
  // reference wiring. Its Commit performs an in-memory CAS on a per-instance
  // version and BUFFERS all writes so a failed step never half-applies.
  type MemStore struct {
      instances map[string]*memInstance
      journal   map[string][]engine.Trigger
      events    []OutboxEvent
  }

  // NewMemStore constructs an empty MemStore.
  func NewMemStore() *MemStore {
      return &MemStore{
          instances: map[string]*memInstance{},
          journal:   map[string][]engine.Trigger{},
      }
  }

  // Create inserts a brand-new instance from its first applied step and returns
  // its initial token.
  func (m *MemStore) Create(_ context.Context, step AppliedStep) (Token, error) {
      const initial Token = 1
      m.instances[step.State.InstanceID] = &memInstance{state: step.State.Clone(), version: initial}
      m.journal[step.State.InstanceID] = append(m.journal[step.State.InstanceID], step.Trigger)
      m.events = append(m.events, step.Events...)
      return initial, nil
  }

  // Load returns the current snapshot and its concurrency token.
  func (m *MemStore) Load(_ context.Context, id string) (engine.InstanceState, Token, error) {
      inst, ok := m.instances[id]
      if !ok {
          return engine.InstanceState{}, 0, ErrInstanceNotFound
      }
      return inst.state.Clone(), inst.version, nil
  }

  // Commit atomically applies one step under an optimistic CAS on expected.
  // It buffers the snapshot, journal append, and outbox events, applying them
  // only after the CAS succeeds, so a stale token leaves the store untouched.
  func (m *MemStore) Commit(_ context.Context, expected Token, step AppliedStep) (Token, error) {
      inst, ok := m.instances[step.State.InstanceID]
      if !ok {
          return 0, ErrInstanceNotFound
      }
      if inst.version != expected {
          return 0, ErrConcurrentUpdate
      }
      next := inst.version + 1
      inst.state = step.State.Clone()
      inst.version = next
      m.journal[step.State.InstanceID] = append(m.journal[step.State.InstanceID], step.Trigger)
      m.events = append(m.events, step.Events...)
      return next, nil
  }

  // Entries returns the recorded trigger history for id (JournalReader).
  func (m *MemStore) Entries(_ context.Context, id string) ([]engine.Trigger, error) {
      return m.journal[id], nil
  }

  // Events returns all buffered outbox events, in append order (test accessor).
  func (m *MemStore) Events() []OutboxEvent { return m.events }
  ```
- [ ] 1.5 Run the tests, expect PASS:
  ```bash
  go test ./runtime/...
  ```
  Expected: `ok  github.com/kartaladev/wrkflw/runtime`. (Existing runner tests
  still pass because the old ports remain until Task 3.)
- [ ] 1.6 Commit:
  ```
  feat(runtime): add transactional Store ports and MemStore fake
  ```

---

## Task 2 — `outboxEventsFor` pure helper (`runtime`)

Implements spec §2/§4 (outbox derivation moved out of `perform`) and ADR-0007's
exhaustiveness guard.

**Files**

- Create: `runtime/outbox.go`.
- Test: `runtime/outbox_test.go`.

**Interfaces**

- Produces: `func outboxEventsFor(cmds []engine.Command) []OutboxEvent`
  (unexported; consumed only by `deliverLoop` in Task 3).

**Steps**

- [ ] 2.1 Write failing test `runtime/outbox_test.go`. It must live in the
  internal test package (`package runtime`) to call the unexported helper, and
  include an exhaustiveness-style assertion that only `CompleteInstance` /
  `FailInstance` produce events:
  ```go
  package runtime

  import (
      "testing"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/engine"
  )

  func TestOutboxEventsFor(t *testing.T) {
      tests := map[string]struct {
          cmds   []engine.Command
          assert func(t *testing.T, got []OutboxEvent)
      }{
          "complete instance maps to instance.completed": {
              cmds: []engine.Command{engine.CompleteInstance{Result: map[string]any{"ok": true}}},
              assert: func(t *testing.T, got []OutboxEvent) {
                  require.Equal(t, []OutboxEvent{{Topic: "instance.completed", Payload: map[string]any{"ok": true}}}, got)
              },
          },
          "fail instance maps to instance.failed": {
              cmds: []engine.Command{engine.FailInstance{Err: "boom"}},
              assert: func(t *testing.T, got []OutboxEvent) {
                  require.Equal(t, []OutboxEvent{{Topic: "instance.failed", Payload: map[string]any{"error": "boom"}}}, got)
              },
          },
          "preserves order across multiple terminal commands": {
              cmds: []engine.Command{
                  engine.CompleteInstance{Result: nil},
                  engine.FailInstance{Err: "x"},
              },
              assert: func(t *testing.T, got []OutboxEvent) {
                  require.Equal(t, "instance.completed", got[0].Topic)
                  require.Equal(t, "instance.failed", got[1].Topic)
              },
          },
          "non-terminal commands contribute nothing": {
              cmds: []engine.Command{
                  engine.InvokeAction{CommandID: "c1", Name: "n"},
                  engine.AwaitHuman{TaskToken: "t1"},
                  engine.UpdateTask{},
                  engine.ScheduleTimer{TimerID: "tm"},
                  engine.CancelTimer{TimerID: "tm"},
                  engine.ThrowSignal{Name: "s"},
                  engine.StartSubInstance{CommandID: "c2", DefRef: "d"},
                  engine.Compensate{},
              },
              assert: func(t *testing.T, got []OutboxEvent) {
                  require.Empty(t, got)
              },
          },
      }
      for name, tc := range tests {
          t.Run(name, func(t *testing.T) {
              tc.assert(t, outboxEventsFor(tc.cmds))
          })
      }
  }
  ```
- [ ] 2.2 Run it, expect FAIL (`undefined: outboxEventsFor`):
  ```bash
  go test -run TestOutboxEventsFor ./runtime/...
  ```
- [ ] 2.3 Create `runtime/outbox.go`:
  ```go
  package runtime

  import "github.com/kartaladev/wrkflw/engine"

  // outboxEventsFor derives the domain events to relay from the commands a Step
  // produced. Only terminal commands produce events: CompleteInstance →
  // "instance.completed", FailInstance → "instance.failed". Every other command
  // contributes nothing (it is performed as external I/O, not relayed). This is
  // the logic that previously lived inline in perform; an exhaustiveness test
  // guards the mapping (ADR-0007).
  func outboxEventsFor(cmds []engine.Command) []OutboxEvent {
      var events []OutboxEvent
      for _, c := range cmds {
          switch cmd := c.(type) {
          case engine.CompleteInstance:
              events = append(events, OutboxEvent{Topic: "instance.completed", Payload: cmd.Result})
          case engine.FailInstance:
              events = append(events, OutboxEvent{Topic: "instance.failed", Payload: map[string]any{"error": cmd.Err}})
          }
      }
      return events
  }
  ```
- [ ] 2.4 Run it, expect PASS:
  ```bash
  go test -run TestOutboxEventsFor ./runtime/...
  ```
- [ ] 2.5 Commit:
  ```
  feat(runtime): add outboxEventsFor pure helper for transactional outbox derivation
  ```

---

## Task 3 — Refactor `deliverLoop` + `NewRunner` to the `Store` port (`runtime`)

Implements spec §2/§3 and ADR-0007's `NewRunner` port collapse. **Large
mechanical task — keep steps tight.** Behavior must be preserved: `go test ./...`
green at the end.

**Files**

- Modify: `runtime/runner.go` (`Runner` fields, `NewRunner`, `Run`, `Deliver`,
  `deliverLoop`, `perform`).
- Modify: `runtime/ports.go` (delete `StateStore`, `Journal`, `OutboxWriter`).
- Delete: `runtime/memory.go` (and `runtime/memory_test.go`) — replaced by
  `MemStore`.
- Modify (migrate call sites): `runtime/runner_test.go`,
  `runtime/taskservice_test.go`, and all `*_example_test.go` that wire the old
  fakes.
- Test: existing `runtime/runner_test.go` (extend with an `ErrConcurrentUpdate`
  propagation case).

**Call sites to migrate** (grep before starting):
```bash
grep -rln "NewMemStateStore\|NewMemJournal\|NewMemOutbox\|StateStore\|Journal\b\|OutboxWriter" runtime/ examples/ 2>/dev/null
```
Known: `runtime/runner.go`, `runtime/memory.go`, `runtime/runner_test.go` (uses
`errJournal`, `errStateStore`, `errOutbox` fakes — replace with an `errStore`
fake), `runtime/taskservice_test.go`, `runtime/example_test.go`,
`runtime/subprocess_example_test.go`, `runtime/timer_example_test.go`,
`runtime/events_example_test.go`, `runtime/errors_example_test.go`,
`runtime/human_example_test.go`. No `examples/` package exists yet (confirm with
the grep).

**Interfaces**

- Consumes: `runtime.Store` (Task 1), `outboxEventsFor` (Task 2).
- Produces (changed signature):
  ```go
  func NewRunner(cat action.Catalog, clk clock.Clock, store Store, opts ...Option) *Runner
  ```

**Steps**

- [ ] 3.1 Write the failing behavior-change test first. Extend
  `runtime/runner_test.go` with a case proving `deliverLoop` propagates
  `ErrConcurrentUpdate` from `Commit`, and a case proving outbox events are
  recorded via the new `Store` (replacing the old `MemOutbox` assertions). Add an
  `errStore` fake whose `Commit` returns `ErrConcurrentUpdate`:
  ```go
  // errStore is a Store whose Commit always reports a concurrency conflict.
  type errStore struct{ *runtime.MemStore }

  func (errStore) Commit(context.Context, runtime.Token, runtime.AppliedStep) (runtime.Token, error) {
      return 0, runtime.ErrConcurrentUpdate
  }

  func TestDeliverLoopPropagatesConcurrentUpdate(t *testing.T) {
      // a single-node process that completes in one Step; Commit conflicts.
      def := /* minimal one-task definition reused from existing tests */ nil
      _ = def
      r := runtime.NewRunner(nil, clock.System(), errStore{runtime.NewMemStore()})
      _, err := r.Run(t.Context(), def, "i1", nil)
      require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)
  }
  ```
  (Ground `def` against the minimal definition already used elsewhere in
  `runner_test.go`; **trust the test, not this listing** — observe the red.)
- [ ] 3.2 Run it, expect FAIL — initially a compile error because `NewRunner`
  still takes 5 positional ports:
  ```bash
  go test -run TestDeliverLoopPropagatesConcurrentUpdate ./runtime/...
  ```
  Expected: `too many arguments in call to runtime.NewRunner` OR
  `not enough arguments` once you start editing — either way, RED.
- [ ] 3.3 Edit `runtime/runner.go` `Runner` struct: replace
  `store StateStore; jnl Journal; out OutboxWriter` with `store Store`.
- [ ] 3.4 Edit `NewRunner` to the new signature and update its godoc (note it
  amends ADR-0005's persistence positionals → one `Store`):
  ```go
  // NewRunner constructs a Runner with the three required core ports (cat, clk,
  // store) and any optional capability bundles supplied as functional options.
  //
  // Required ports:
  //   - cat: the service-action catalog (may be nil for processes with no service tasks).
  //   - clk: the time source. Pass a fake clock in tests.
  //   - store: the transactional persistence port (snapshot + journal + outbox).
  //     See [Store]; the in-memory [MemStore] is the reference fake.
  //
  // ADR-0007 amends ADR-0005: the former store/jnl/out positionals collapse to
  // one transactional Store, so snapshot, journal, and outbox commit atomically
  // per applied trigger.
  func NewRunner(cat action.Catalog, clk clock.Clock, store Store, opts ...Option) *Runner {
      r := &Runner{
          cat:        cat,
          clk:        clk,
          store:      store,
          msgWaiters: make(map[msgKey]string),
      }
      for _, o := range opts {
          o(r)
      }
      return r
  }
  ```
- [ ] 3.5 Edit `Run` to create the instance via `store.Create` for the first
  step. Because `Create` is the first-step path and `Commit` the subsequent path,
  restructure `Run` to seed `(st, token)` and let `deliverLoop` distinguish the
  initial create. Simplest faithful shape — have `deliverLoop` accept the loaded
  `(st, token)` and a `create bool` flag for the very first applied step:
  ```go
  func (r *Runner) Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
      st := engine.InstanceState{InstanceID: instanceID}
      return r.deliverLoop(ctx, def, st, 0, true, engine.NewStartInstance(r.clk.Now(), vars))
  }
  ```
- [ ] 3.6 Edit `Deliver` to load `(st, token)`:
  ```go
  func (r *Runner) Deliver(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error) {
      st, token, err := r.store.Load(ctx, instanceID)
      if err != nil {
          return engine.InstanceState{}, fmt.Errorf("runtime: deliver: load: %w", err)
      }
      return r.deliverLoop(ctx, def, st, token, false, trg)
  }
  ```
- [ ] 3.7 Rewrite `deliverLoop` to the spec §2 order (Step → outboxEventsFor →
  Create/Commit holding `(st, token)` across the loop → perform non-outbox
  commands):
  ```go
  func (r *Runner) deliverLoop(
      ctx context.Context,
      def *model.ProcessDefinition,
      st engine.InstanceState,
      token Token,
      create bool,
      trg engine.Trigger,
  ) (engine.InstanceState, error) {
      queue := []engine.Trigger{trg}

      for len(queue) > 0 {
          t := queue[0]
          queue = queue[1:]

          res, err := engine.Step(def, st, t, engine.StepOptions{})
          if err != nil {
              return st, fmt.Errorf("runtime: step: %w", err)
          }
          st = res.State

          events := outboxEventsFor(res.Commands)
          appliedStep := AppliedStep{State: st, Trigger: t, Events: events}

          if create {
              token, err = r.store.Create(ctx, appliedStep)
              create = false
          } else {
              token, err = r.store.Commit(ctx, token, appliedStep)
          }
          if err != nil {
              return st, fmt.Errorf("runtime: commit: %w", err)
          }

          // Reconcile signal-bus and message waiters after each committed save.
          r.syncWaiters(st)

          for _, c := range res.Commands {
              next, err := r.perform(ctx, def, st, c)
              if err != nil {
                  return st, err
              }
              if next != nil {
                  queue = append(queue, next)
              }
          }
      }
      return st, nil
  }
  ```
  Note: `fmt.Errorf("runtime: commit: %w", err)` wraps `ErrConcurrentUpdate`, so
  `errors.Is` still matches (the test in 3.1 uses `ErrorIs`).
- [ ] 3.8 Remove the outbox writes from `perform`: `CompleteInstance` and
  `FailInstance` cases become no-ops (return `nil, nil`) — the events are now
  written inside the tx:
  ```go
  case engine.CompleteInstance:
      // Outbox event ("instance.completed") is derived by outboxEventsFor and
      // written inside the Commit tx; nothing to perform here.
      return nil, nil

  case engine.FailInstance:
      // Outbox event ("instance.failed") is derived by outboxEventsFor and
      // written inside the Commit tx; nothing to perform here.
      return nil, nil
  ```
- [ ] 3.9 Delete `StateStore`, `Journal`, `OutboxWriter` from `runtime/ports.go`
  (keep `ErrInstanceNotFound`, `Token`, `OutboxEvent`, `AppliedStep`,
  `ErrConcurrentUpdate`, `Store`, `JournalReader`). Delete `runtime/memory.go` and
  `runtime/memory_test.go`.
- [ ] 3.10 Migrate every call site found by the grep in 3.0. Pattern:
  `NewRunner(cat, clk, NewMemStateStore(), NewMemJournal(), out)` →
  `NewRunner(cat, clk, NewMemStore())`. Replace `errJournal`/`errStateStore`/
  `errOutbox` in `runner_test.go` with the single `errStore` fake (or a
  `MemStore` wrapper whose `Create`/`Commit` errors). Replace any
  `out.Events()` outbox assertion with `store.Events()` on the `MemStore`.
- [ ] 3.11 Run the full suite, expect PASS:
  ```bash
  go test ./...
  ```
  Expected: all packages `ok`. If a test relied on journal-before-Step ordering
  (it cannot — journal now writes inside Commit after Step), adjust the assertion
  to the new atomic ordering.
- [ ] 3.12 Run race + coverage on `runtime`:
  ```bash
  go test -race -coverprofile=cover.out ./runtime/... && go tool cover -func=cover.out | tail -1
  ```
  Expected: `total:` ≥ 85%.
- [ ] 3.13 Commit:
  ```
  refactor(runtime): collapse persistence ports into transactional Store (ADR-0007)
  ```

---

## Task 4 — Sealed-`Trigger` JSON codec (`internal/persistence/postgres`)

Implements spec §3 "Trigger (de)serialization" and §8 risk (exhaustiveness over
the sealed set). Every exported `engine.Trigger` variant must round-trip,
including `authz.Actor` and payload maps.

**Files**

- Create: `internal/persistence/postgres/trigger_codec.go`.
- Test: `internal/persistence/postgres/trigger_codec_test.go`.

**Sealed `Trigger` variants** (enumerate from `engine/trigger.go`):
`StartInstance`, `ActionCompleted`, `ActionFailed`, `HumanCompleted`,
`HumanClaimed`, `HumanReassigned`, `TimerFired`, `SignalReceived`,
`MessageReceived`, `SubInstanceCompleted`, `SubInstanceFailed`,
`CompensateRequested`, `CancelRequested`.

**Interfaces**

- Produces:
  ```go
  func MarshalTrigger(t engine.Trigger) (data []byte, kind string, err error)
  func UnmarshalTrigger(kind string, data []byte) (engine.Trigger, error)
  ```
  Reconstruct each variant via its `engine.NewXxx` constructor, carrying
  `OccurredAt()` through a serialized `at` field.

**Steps**

- [ ] 4.1 Write failing test `internal/persistence/postgres/trigger_codec_test.go`
  (`package postgres_test`) that round-trips ONE instance of EVERY variant via a
  table, failing if a variant is unhandled. Use the `assert` closure form:
  ```go
  package postgres_test

  import (
      "testing"
      "time"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/authz"
      "github.com/kartaladev/wrkflw/engine"
      pg "github.com/kartaladev/wrkflw/internal/persistence/postgres"
  )

  func TestTriggerCodecRoundTrip(t *testing.T) {
      at := time.Unix(1700000000, 0).UTC()
      actor := authz.Actor{ID: "u1", Roles: []string{"r"}, Attributes: map[string]any{"k": "v"}}
      payload := map[string]any{"k": "v"}

      tests := map[string]struct {
          in     engine.Trigger
          assert func(t *testing.T, got engine.Trigger)
      }{
          "StartInstance": {
              in: engine.NewStartInstance(at, payload),
              assert: func(t *testing.T, got engine.Trigger) {
                  require.IsType(t, engine.StartInstance{}, got)
                  require.Equal(t, payload, got.(engine.StartInstance).Vars)
              },
          },
          "ActionCompleted": {in: engine.NewActionCompleted(at, "c1", payload), assert: func(t *testing.T, got engine.Trigger) { require.IsType(t, engine.ActionCompleted{}, got) }},
          "ActionFailed":    {in: engine.NewActionFailed(at, "c1", "boom", true), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, "boom", got.(engine.ActionFailed).Err) }},
          "HumanCompleted":  {in: engine.NewHumanCompleted(at, "t1", payload, actor), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, actor, got.(engine.HumanCompleted).Actor) }},
          "HumanClaimed":    {in: engine.NewHumanClaimed(at, "t1", actor), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, actor, got.(engine.HumanClaimed).Actor) }},
          "HumanReassigned": {in: engine.NewHumanReassigned(at, "t1", "a", "b", actor), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, "b", got.(engine.HumanReassigned).To) }},
          "TimerFired":      {in: engine.NewTimerFired(at, "tm1"), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, "tm1", got.(engine.TimerFired).TimerID) }},
          "SignalReceived":  {in: engine.NewSignalReceived(at, "sig", payload), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, "sig", got.(engine.SignalReceived).Name) }},
          "MessageReceived": {in: engine.NewMessageReceived(at, "msg", "key", payload), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, "key", got.(engine.MessageReceived).CorrelationKey) }},
          "SubInstanceCompleted": {in: engine.NewSubInstanceCompleted(at, "c1", payload), assert: func(t *testing.T, got engine.Trigger) { require.IsType(t, engine.SubInstanceCompleted{}, got) }},
          "SubInstanceFailed":    {in: engine.NewSubInstanceFailed(at, "c1", "err"), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, "err", got.(engine.SubInstanceFailed).Err) }},
          "CompensateRequested":  {in: engine.NewCompensateRequested(at, "n1"), assert: func(t *testing.T, got engine.Trigger) { require.Equal(t, "n1", got.(engine.CompensateRequested).ToNode) }},
          "CancelRequested":      {in: engine.NewCancelRequested(at), assert: func(t *testing.T, got engine.Trigger) { require.IsType(t, engine.CancelRequested{}, got) }},
      }
      for name, tc := range tests {
          t.Run(name, func(t *testing.T) {
              data, kind, err := pg.MarshalTrigger(tc.in)
              require.NoError(t, err)
              require.NotEmpty(t, kind)

              got, err := pg.UnmarshalTrigger(kind, data)
              require.NoError(t, err)
              require.True(t, tc.in.OccurredAt().Equal(got.OccurredAt()))
              tc.assert(t, got)
          })
      }
  }

  func TestUnmarshalTriggerUnknownKind(t *testing.T) {
      _, err := pg.UnmarshalTrigger("does.not.exist", []byte(`{}`))
      require.Error(t, err)
  }
  ```
- [ ] 4.2 Run it, expect FAIL (`undefined: pg.MarshalTrigger` — build fails):
  ```bash
  go test ./internal/persistence/postgres/...
  ```
- [ ] 4.3 Create `internal/persistence/postgres/trigger_codec.go`. Marshal each
  variant to a flat JSON struct carrying its fields + `at`; switch on `kind` to
  reconstruct via constructors:
  ```go
  // Package postgres is the internal Postgres-backed persistence implementation.
  // Consumers must not import it; use the persistence/ façade (ADR-0008).
  package postgres

  import (
      "encoding/json"
      "fmt"
      "time"

      "github.com/kartaladev/wrkflw/authz"
      "github.com/kartaladev/wrkflw/engine"
  )

  // Trigger kind discriminators stored in wrkflw_journal.kind.
  const (
      kindStartInstance        = "start_instance"
      kindActionCompleted      = "action_completed"
      kindActionFailed         = "action_failed"
      kindHumanCompleted       = "human_completed"
      kindHumanClaimed         = "human_claimed"
      kindHumanReassigned      = "human_reassigned"
      kindTimerFired           = "timer_fired"
      kindSignalReceived       = "signal_received"
      kindMessageReceived      = "message_received"
      kindSubInstanceCompleted = "sub_instance_completed"
      kindSubInstanceFailed    = "sub_instance_failed"
      kindCompensateRequested  = "compensate_requested"
      kindCancelRequested      = "cancel_requested"
  )

  type triggerEnvelope struct {
      At             time.Time      `json:"at"`
      Vars           map[string]any `json:"vars,omitempty"`
      Output         map[string]any `json:"output,omitempty"`
      Payload        map[string]any `json:"payload,omitempty"`
      CommandID      string         `json:"command_id,omitempty"`
      Err            string         `json:"err,omitempty"`
      Retryable      bool           `json:"retryable,omitempty"`
      TaskToken      string         `json:"task_token,omitempty"`
      Actor          authz.Actor    `json:"actor,omitempty"`
      From           string         `json:"from,omitempty"`
      To             string         `json:"to,omitempty"`
      By             authz.Actor    `json:"by,omitempty"`
      TimerID        string         `json:"timer_id,omitempty"`
      Name           string         `json:"name,omitempty"`
      CorrelationKey string         `json:"correlation_key,omitempty"`
      ToNode         string         `json:"to_node,omitempty"`
  }

  // MarshalTrigger serialises a sealed Trigger to JSON plus a kind discriminator.
  func MarshalTrigger(t engine.Trigger) ([]byte, string, error) {
      env := triggerEnvelope{At: t.OccurredAt()}
      var kind string
      switch v := t.(type) {
      case engine.StartInstance:
          kind, env.Vars = kindStartInstance, v.Vars
      case engine.ActionCompleted:
          kind, env.CommandID, env.Output = kindActionCompleted, v.CommandID, v.Output
      case engine.ActionFailed:
          kind, env.CommandID, env.Err, env.Retryable = kindActionFailed, v.CommandID, v.Err, v.Retryable
      case engine.HumanCompleted:
          kind, env.TaskToken, env.Output, env.Actor = kindHumanCompleted, v.TaskToken, v.Output, v.Actor
      case engine.HumanClaimed:
          kind, env.TaskToken, env.Actor = kindHumanClaimed, v.TaskToken, v.Actor
      case engine.HumanReassigned:
          kind, env.TaskToken, env.From, env.To, env.By = kindHumanReassigned, v.TaskToken, v.From, v.To, v.By
      case engine.TimerFired:
          kind, env.TimerID = kindTimerFired, v.TimerID
      case engine.SignalReceived:
          kind, env.Name, env.Payload = kindSignalReceived, v.Name, v.Payload
      case engine.MessageReceived:
          kind, env.Name, env.CorrelationKey, env.Payload = kindMessageReceived, v.Name, v.CorrelationKey, v.Payload
      case engine.SubInstanceCompleted:
          kind, env.CommandID, env.Output = kindSubInstanceCompleted, v.CommandID, v.Output
      case engine.SubInstanceFailed:
          kind, env.CommandID, env.Err = kindSubInstanceFailed, v.CommandID, v.Err
      case engine.CompensateRequested:
          kind, env.ToNode = kindCompensateRequested, v.ToNode
      case engine.CancelRequested:
          kind = kindCancelRequested
      default:
          return nil, "", fmt.Errorf("postgres: marshal trigger: unhandled variant %T", t)
      }
      data, err := json.Marshal(env)
      if err != nil {
          return nil, "", fmt.Errorf("postgres: marshal trigger: %w", err)
      }
      return data, kind, nil
  }

  // UnmarshalTrigger reconstructs a sealed Trigger from its kind + JSON payload.
  func UnmarshalTrigger(kind string, data []byte) (engine.Trigger, error) {
      var env triggerEnvelope
      if err := json.Unmarshal(data, &env); err != nil {
          return nil, fmt.Errorf("postgres: unmarshal trigger %q: %w", kind, err)
      }
      switch kind {
      case kindStartInstance:
          return engine.NewStartInstance(env.At, env.Vars), nil
      case kindActionCompleted:
          return engine.NewActionCompleted(env.At, env.CommandID, env.Output), nil
      case kindActionFailed:
          return engine.NewActionFailed(env.At, env.CommandID, env.Err, env.Retryable), nil
      case kindHumanCompleted:
          return engine.NewHumanCompleted(env.At, env.TaskToken, env.Output, env.Actor), nil
      case kindHumanClaimed:
          return engine.NewHumanClaimed(env.At, env.TaskToken, env.Actor), nil
      case kindHumanReassigned:
          return engine.NewHumanReassigned(env.At, env.TaskToken, env.From, env.To, env.By), nil
      case kindTimerFired:
          return engine.NewTimerFired(env.At, env.TimerID), nil
      case kindSignalReceived:
          return engine.NewSignalReceived(env.At, env.Name, env.Payload), nil
      case kindMessageReceived:
          return engine.NewMessageReceived(env.At, env.Name, env.CorrelationKey, env.Payload), nil
      case kindSubInstanceCompleted:
          return engine.NewSubInstanceCompleted(env.At, env.CommandID, env.Output), nil
      case kindSubInstanceFailed:
          return engine.NewSubInstanceFailed(env.At, env.CommandID, env.Err), nil
      case kindCompensateRequested:
          return engine.NewCompensateRequested(env.At, env.ToNode), nil
      case kindCancelRequested:
          return engine.NewCancelRequested(env.At), nil
      default:
          return nil, fmt.Errorf("postgres: unmarshal trigger: unknown kind %q", kind)
      }
  }
  ```
- [ ] 4.4 Run the tests, expect PASS:
  ```bash
  go test ./internal/persistence/postgres/...
  ```
- [ ] 4.5 Commit:
  ```
  feat(persistence): add sealed Trigger JSON codec with exhaustiveness test
  ```

---

## Task 5 — `database.RunTestDatabase` helper + schema migrations

Implements spec §4 / §4b (schema) and §7 (testcontainers helper, goose `SetBaseFS`
+ `embed.FS`). Per the `use-testcontainers` skill, the helper is the single shared
entry point for all Postgres tests.

**Files**

- Create: `database/testutils.go` (the `RunTestDatabase` helper + `TestOption`).
- Create: `internal/persistence/postgres/migrations/0001_init.sql`.
- Create: `internal/persistence/postgres/migrate.go` (`embed.FS` + `Migrate`).
- Test: `internal/persistence/postgres/migrate_test.go`.

**Interfaces**

- Produces:
  ```go
  // database
  func RunTestDatabase(t *testing.T, opts ...TestOption) *pgxpool.Pool
  type TestOption func(*testConfig)

  // internal/persistence/postgres
  func Migrate(ctx context.Context, pool *pgxpool.Pool) error
  ```

**Steps**

- [ ] 5.1 Write the failing migration test first
  `internal/persistence/postgres/migrate_test.go` (`package postgres_test`). It
  spins a fresh testcontainer via `database.RunTestDatabase`, runs `Migrate`, and
  asserts all four tables exist:
  ```go
  package postgres_test

  import (
      "testing"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/database"
      pg "github.com/kartaladev/wrkflw/internal/persistence/postgres"
  )

  func TestMigrateCreatesTables(t *testing.T) {
      t.Parallel()
      pool := database.RunTestDatabase(t)

      require.NoError(t, pg.Migrate(t.Context(), pool))

      tables := []string{"wrkflw_instances", "wrkflw_journal", "wrkflw_outbox", "wrkflw_definitions"}
      for _, tbl := range tables {
          var exists bool
          err := pool.QueryRow(t.Context(),
              `SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = $1)`, tbl,
          ).Scan(&exists)
          require.NoError(t, err)
          require.True(t, exists, "table %s should exist", tbl)
      }
  }

  func TestMigrateIsIdempotent(t *testing.T) {
      t.Parallel()
      pool := database.RunTestDatabase(t)
      require.NoError(t, pg.Migrate(t.Context(), pool))
      require.NoError(t, pg.Migrate(t.Context(), pool)) // second run is a no-op
  }
  ```
- [ ] 5.2 Run it, expect FAIL (`undefined: database.RunTestDatabase`,
  `undefined: pg.Migrate`):
  ```bash
  go test ./internal/persistence/postgres/...
  ```
- [ ] 5.3 Create `database/testutils.go` per the `use-testcontainers` skill. Use
  the postgres:17 module, Snapshot/Restore, a DB name that is **never**
  `postgres`, and register cleanup via `t.Cleanup`:
  ```go
  // Package database provides the shared testcontainers helper for Postgres tests.
  package database

  import (
      "context"
      "testing"
      "time"

      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/stretchr/testify/require"
      "github.com/testcontainers/testcontainers-go"
      tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
      "github.com/testcontainers/testcontainers-go/wait"
  )

  type testConfig struct {
      dbName   string
      user     string
      password string
  }

  // TestOption customises the test database.
  type TestOption func(*testConfig)

  // WithDBName overrides the database name (never "postgres").
  func WithDBName(name string) TestOption { return func(c *testConfig) { c.dbName = name } }

  // RunTestDatabase starts a PostgreSQL 17 container, applies a clean snapshot,
  // and returns a connected pgxpool.Pool. The container and pool are torn down
  // via t.Cleanup. The DB is never named "postgres" (Restore drops the connected
  // DB). Requires a running Docker daemon.
  func RunTestDatabase(t *testing.T, opts ...TestOption) *pgxpool.Pool {
      t.Helper()

      cfg := testConfig{dbName: "wrkflw_test", user: "wrkflw", password: "wrkflw"}
      for _, o := range opts {
          o(&cfg)
      }

      ctx := context.Background()
      container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
          tcpostgres.WithDatabase(cfg.dbName),
          tcpostgres.WithUsername(cfg.user),
          tcpostgres.WithPassword(cfg.password),
          testcontainers.WithWaitStrategy(
              wait.ForLog("database system is ready to accept connections").
                  WithOccurrence(2).WithStartupTimeout(60*time.Second),
          ),
      )
      require.NoError(t, err)
      t.Cleanup(func() { _ = container.Terminate(context.Background()) })

      dsn, err := container.ConnectionString(ctx, "sslmode=disable")
      require.NoError(t, err)

      pool, err := pgxpool.New(ctx, dsn)
      require.NoError(t, err)
      t.Cleanup(pool.Close)

      require.NoError(t, pool.Ping(ctx))
      return pool
  }
  ```
  (If the `tcpostgres.Run` API signature differs in the pinned version, ground
  against the installed module — trust the build, not this listing.)
- [ ] 5.4 Create `internal/persistence/postgres/migrations/0001_init.sql` (goose
  format) with the four tables from spec §4 / §4b verbatim:
  ```sql
  -- +goose Up
  CREATE TABLE wrkflw_instances (
      instance_id  TEXT PRIMARY KEY,
      def_id       TEXT        NOT NULL,
      def_version  INT         NOT NULL,
      status       SMALLINT    NOT NULL,
      snapshot     JSONB       NOT NULL,
      version      BIGINT      NOT NULL,
      started_at   TIMESTAMPTZ NOT NULL,
      ended_at     TIMESTAMPTZ,
      updated_at   TIMESTAMPTZ NOT NULL
  );
  CREATE INDEX wrkflw_instances_status_idx ON wrkflw_instances (status) WHERE ended_at IS NULL;

  CREATE TABLE wrkflw_journal (
      instance_id TEXT        NOT NULL REFERENCES wrkflw_instances(instance_id),
      seq         BIGINT      NOT NULL,
      kind        TEXT        NOT NULL,
      trigger     JSONB       NOT NULL,
      occurred_at TIMESTAMPTZ NOT NULL,
      applied_at  TIMESTAMPTZ NOT NULL,
      PRIMARY KEY (instance_id, seq)
  );

  CREATE TABLE wrkflw_outbox (
      id           BIGSERIAL PRIMARY KEY,
      instance_id  TEXT        NOT NULL,
      topic        TEXT        NOT NULL,
      payload      JSONB       NOT NULL,
      dedup_key    TEXT        NOT NULL UNIQUE,
      created_at   TIMESTAMPTZ NOT NULL,
      published_at TIMESTAMPTZ
  );
  CREATE INDEX wrkflw_outbox_unpublished_idx ON wrkflw_outbox (id) WHERE published_at IS NULL;

  CREATE TABLE wrkflw_definitions (
      def_id      TEXT        NOT NULL,
      version     INT         NOT NULL,
      definition  JSONB       NOT NULL,
      created_at  TIMESTAMPTZ NOT NULL,
      PRIMARY KEY (def_id, version)
  );

  -- +goose Down
  DROP TABLE wrkflw_definitions;
  DROP TABLE wrkflw_outbox;
  DROP TABLE wrkflw_journal;
  DROP TABLE wrkflw_instances;
  ```
- [ ] 5.5 Create `internal/persistence/postgres/migrate.go` using goose
  `SetBaseFS` over the embedded FS, driving goose through a `database/sql` handle
  derived from the pool's DSN via `pgx`'s `stdlib` adapter (goose needs a
  `*sql.DB`; the runtime store still uses pgxpool):
  ```go
  package postgres

  import (
      "context"
      "database/sql"
      "embed"
      "fmt"

      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/jackc/pgx/v5/stdlib"
      "github.com/pressly/goose/v3"
  )

  //go:embed migrations/*.sql
  var migrationsFS embed.FS

  // Migrate applies all embedded migrations to the database behind pool. It is
  // idempotent (goose_db_version tracks applied versions) and is run by the
  // consumer, never auto-run on import.
  func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
      db := stdlib.OpenDBFromPool(pool)
      defer func() { _ = db.Close() }()

      goose.SetBaseFS(migrationsFS)
      if err := goose.SetDialect("postgres"); err != nil {
          return fmt.Errorf("postgres: migrate: set dialect: %w", err)
      }
      if err := goose.UpContext(ctx, db, "migrations"); err != nil {
          return fmt.Errorf("postgres: migrate: up: %w", err)
      }
      return nil
  }

  var _ = (*sql.DB)(nil) // stdlib import anchor
  ```
  (If `stdlib.OpenDBFromPool` is unavailable in the pinned pgx version, open a
  separate `*sql.DB` from the pool's `Config().ConnString()` via
  `sql.Open("pgx", dsn)` — trust the build.)
- [ ] 5.6 `go mod tidy`, then run the tests (Docker required), expect PASS:
  ```bash
  go test ./internal/persistence/postgres/... ./database/...
  ```
- [ ] 5.7 Commit:
  ```
  feat(persistence): add testcontainers helper and goose schema migrations
  ```

---

## Task 6 — Postgres `Store` impl (`internal/persistence/postgres`)

Implements spec §2/§3/§4 and ADR-0007's `Commit` (snapshot CAS + journal + outbox
in one pgx.Tx), with SQLSTATE `40001` mapped to `ErrConcurrentUpdate`.

**Files**

- Create: `internal/persistence/postgres/dbtx.go` (the `DBTX` querier interface).
- Create: `internal/persistence/postgres/store.go`.
- Test: `internal/persistence/postgres/store_test.go` (testcontainers).

**Interfaces**

- Produces:
  ```go
  type DBTX interface {
      Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
      Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
      QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
      Begin(ctx context.Context) (pgx.Tx, error)
      SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
  }

  type Store struct { /* pool *pgxpool.Pool */ }
  func NewStore(pool *pgxpool.Pool) *Store
  // Store satisfies runtime.Store + runtime.JournalReader:
  func (s *Store) Create(ctx, step runtime.AppliedStep) (runtime.Token, error)
  func (s *Store) Load(ctx, id string) (engine.InstanceState, runtime.Token, error)
  func (s *Store) Commit(ctx, expected runtime.Token, step runtime.AppliedStep) (runtime.Token, error)
  func (s *Store) Entries(ctx, id string) ([]engine.Trigger, error)
  ```

**Steps**

- [ ] 6.1 Write failing test `internal/persistence/postgres/store_test.go`
  (`package postgres_test`) over a migrated testcontainer. Cover create→load,
  commit advances version + persists journal & outbox atomically, stale-version
  conflict, and load-missing. Use the `assert` closure form:
  ```go
  package postgres_test

  import (
      "testing"
      "time"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/database"
      "github.com/kartaladev/wrkflw/engine"
      pg "github.com/kartaladev/wrkflw/internal/persistence/postgres"
      "github.com/kartaladev/wrkflw/runtime"
  )

  func newStore(t *testing.T) *pg.Store {
      t.Helper()
      pool := database.RunTestDatabase(t)
      require.NoError(t, pg.Migrate(t.Context(), pool))
      return pg.NewStore(pool)
  }

  func appliedStep(id, topic string) runtime.AppliedStep {
      now := time.Unix(1700000000, 0).UTC()
      return runtime.AppliedStep{
          State:   engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
          Trigger: engine.NewStartInstance(now, map[string]any{"k": "v"}),
          Events:  []runtime.OutboxEvent{{Topic: topic, Payload: map[string]any{"x": float64(1)}}},
      }
  }

  func TestStore(t *testing.T) {
      tests := map[string]struct {
          assert func(t *testing.T, s *pg.Store)
      }{
          "create then load round-trips": {
              assert: func(t *testing.T, s *pg.Store) {
                  tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
                  require.NoError(t, err)
                  st, loaded, err := s.Load(t.Context(), "i1")
                  require.NoError(t, err)
                  require.Equal(t, "i1", st.InstanceID)
                  require.Equal(t, tok, loaded)
              },
          },
          "commit advances version and persists journal+outbox": {
              assert: func(t *testing.T, s *pg.Store) {
                  tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
                  require.NoError(t, err)
                  next, err := s.Commit(t.Context(), tok, appliedStep("i1", "b"))
                  require.NoError(t, err)
                  require.Greater(t, int64(next), int64(tok))
                  entries, err := s.Entries(t.Context(), "i1")
                  require.NoError(t, err)
                  require.Len(t, entries, 2)
              },
          },
          "stale version conflicts": {
              assert: func(t *testing.T, s *pg.Store) {
                  tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
                  require.NoError(t, err)
                  _, err = s.Commit(t.Context(), tok, appliedStep("i1", "b"))
                  require.NoError(t, err)
                  _, err = s.Commit(t.Context(), tok, appliedStep("i1", "c"))
                  require.ErrorIs(t, err, runtime.ErrConcurrentUpdate)
              },
          },
          "load missing": {
              assert: func(t *testing.T, s *pg.Store) {
                  _, _, err := s.Load(t.Context(), "nope")
                  require.ErrorIs(t, err, runtime.ErrInstanceNotFound)
              },
          },
      }
      for name, tc := range tests {
          t.Run(name, func(t *testing.T) {
              t.Parallel()
              tc.assert(t, newStore(t))
          })
      }
  }
  ```
- [ ] 6.2 Run it, expect FAIL (`undefined: pg.NewStore`):
  ```bash
  go test -run TestStore ./internal/persistence/postgres/...
  ```
- [ ] 6.3 Create `internal/persistence/postgres/dbtx.go`:
  ```go
  package postgres

  import (
      "context"

      "github.com/jackc/pgx/v5"
      "github.com/jackc/pgx/v5/pgconn"
  )

  // DBTX is the minimal querier seam satisfied by *pgxpool.Pool, *pgx.Conn, and
  // pgx.Tx, so the same repo code runs against a pool or an in-flight transaction.
  type DBTX interface {
      Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
      Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
      QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
      Begin(ctx context.Context) (pgx.Tx, error)
      SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
  }
  ```
- [ ] 6.4 Create `internal/persistence/postgres/store.go`. `Create` inserts the
  instance row (version=1) + journal seq=1 + outbox rows in one tx; `Commit` opens
  a tx, runs the CAS `UPDATE`, checks `RowsAffected()==0` ⇒ `ErrConcurrentUpdate`,
  inserts the next journal seq + outbox rows, commits; `40001` is mapped to
  `ErrConcurrentUpdate`. Snapshot is marshalled with `json.Marshal`; projected
  `status`/`ended_at` written explicitly; `dedup_key` is
  `<instance_id>:<seq>:<event_index>`:
  ```go
  package postgres

  import (
      "context"
      "encoding/json"
      "errors"
      "fmt"
      "time"

      "github.com/jackc/pgx/v5"
      "github.com/jackc/pgx/v5/pgconn"
      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/kartaladev/wrkflw/engine"
      "github.com/kartaladev/wrkflw/runtime"
  )

  // Compile-time checks.
  var (
      _ runtime.Store         = (*Store)(nil)
      _ runtime.JournalReader = (*Store)(nil)
  )

  // Store is the Postgres-backed runtime.Store + JournalReader.
  type Store struct {
      pool *pgxpool.Pool
  }

  // NewStore constructs a Store over the given pool.
  func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

  func endedAt(st engine.InstanceState) *time.Time { return st.EndedAt }

  // Create inserts a brand-new instance from its first applied step (version 1).
  func (s *Store) Create(ctx context.Context, step runtime.AppliedStep) (runtime.Token, error) {
      const version int64 = 1
      tx, err := s.pool.Begin(ctx)
      if err != nil {
          return 0, fmt.Errorf("postgres: create: begin: %w", err)
      }
      defer func() { _ = tx.Rollback(ctx) }()

      snap, err := json.Marshal(step.State)
      if err != nil {
          return 0, fmt.Errorf("postgres: create: marshal snapshot: %w", err)
      }
      now := time.Now().UTC()
      if _, err := tx.Exec(ctx,
          `INSERT INTO wrkflw_instances
             (instance_id, def_id, def_version, status, snapshot, version, started_at, ended_at, updated_at)
           VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
          step.State.InstanceID, step.State.DefID, step.State.DefVersion,
          int16(step.State.Status), snap, version, step.State.StartedAt, endedAt(step.State), now,
      ); err != nil {
          return 0, fmt.Errorf("postgres: create: insert instance: %w", err)
      }
      if err := writeJournal(ctx, tx, step, 1, now); err != nil {
          return 0, err
      }
      if err := writeOutbox(ctx, tx, step.State.InstanceID, 1, step.Events, now); err != nil {
          return 0, err
      }
      if err := tx.Commit(ctx); err != nil {
          return 0, fmt.Errorf("postgres: create: commit: %w", err)
      }
      return runtime.Token(version), nil
  }

  // Load returns the snapshot and current version token.
  func (s *Store) Load(ctx context.Context, id string) (engine.InstanceState, runtime.Token, error) {
      var snap []byte
      var version int64
      err := s.pool.QueryRow(ctx,
          `SELECT snapshot, version FROM wrkflw_instances WHERE instance_id = $1`, id,
      ).Scan(&snap, &version)
      if errors.Is(err, pgx.ErrNoRows) {
          return engine.InstanceState{}, 0, runtime.ErrInstanceNotFound
      }
      if err != nil {
          return engine.InstanceState{}, 0, fmt.Errorf("postgres: load %q: %w", id, err)
      }
      var st engine.InstanceState
      if err := json.Unmarshal(snap, &st); err != nil {
          return engine.InstanceState{}, 0, fmt.Errorf("postgres: load %q: unmarshal snapshot: %w", id, err)
      }
      return st, runtime.Token(version), nil
  }

  // Commit atomically applies one step: CAS the snapshot, append journal, insert outbox.
  func (s *Store) Commit(ctx context.Context, expected runtime.Token, step runtime.AppliedStep) (runtime.Token, error) {
      tx, err := s.pool.Begin(ctx)
      if err != nil {
          return 0, mapConflict(fmt.Errorf("postgres: commit: begin: %w", err))
      }
      defer func() { _ = tx.Rollback(ctx) }()

      snap, err := json.Marshal(step.State)
      if err != nil {
          return 0, fmt.Errorf("postgres: commit: marshal snapshot: %w", err)
      }
      now := time.Now().UTC()
      tag, err := tx.Exec(ctx,
          `UPDATE wrkflw_instances
              SET snapshot = $1, version = version + 1, status = $2, ended_at = $3, updated_at = $4
            WHERE instance_id = $5 AND version = $6`,
          snap, int16(step.State.Status), endedAt(step.State), now, step.State.InstanceID, int64(expected),
      )
      if err != nil {
          return 0, mapConflict(fmt.Errorf("postgres: commit: update: %w", err))
      }
      if tag.RowsAffected() == 0 {
          return 0, runtime.ErrConcurrentUpdate
      }
      next := int64(expected) + 1

      // Next journal seq = next version (1:1 with applied steps).
      if err := writeJournal(ctx, tx, step, next, now); err != nil {
          return 0, mapConflict(err)
      }
      if err := writeOutbox(ctx, tx, step.State.InstanceID, next, step.Events, now); err != nil {
          return 0, mapConflict(err)
      }
      if err := tx.Commit(ctx); err != nil {
          return 0, mapConflict(fmt.Errorf("postgres: commit: %w", err))
      }
      return runtime.Token(next), nil
  }

  func writeJournal(ctx context.Context, db DBTX, step runtime.AppliedStep, seq int64, appliedAt time.Time) error {
      data, kind, err := MarshalTrigger(step.Trigger)
      if err != nil {
          return err
      }
      if _, err := db.Exec(ctx,
          `INSERT INTO wrkflw_journal (instance_id, seq, kind, trigger, occurred_at, applied_at)
           VALUES ($1,$2,$3,$4,$5,$6)`,
          step.State.InstanceID, seq, kind, data, step.Trigger.OccurredAt(), appliedAt,
      ); err != nil {
          return fmt.Errorf("postgres: write journal: %w", err)
      }
      return nil
  }

  func writeOutbox(ctx context.Context, db DBTX, instanceID string, seq int64, events []runtime.OutboxEvent, createdAt time.Time) error {
      for i, ev := range events {
          payload, err := json.Marshal(ev.Payload)
          if err != nil {
              return fmt.Errorf("postgres: write outbox: marshal payload: %w", err)
          }
          dedup := fmt.Sprintf("%s:%d:%d", instanceID, seq, i)
          if _, err := db.Exec(ctx,
              `INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
               VALUES ($1,$2,$3,$4,$5)`,
              instanceID, ev.Topic, payload, dedup, createdAt,
          ); err != nil {
              return fmt.Errorf("postgres: write outbox: %w", err)
          }
      }
      return nil
  }

  // Entries returns the recorded trigger history for id, ordered by seq.
  func (s *Store) Entries(ctx context.Context, id string) ([]engine.Trigger, error) {
      rows, err := s.pool.Query(ctx,
          `SELECT kind, trigger FROM wrkflw_journal WHERE instance_id = $1 ORDER BY seq`, id)
      if err != nil {
          return nil, fmt.Errorf("postgres: entries %q: %w", id, err)
      }
      defer rows.Close()

      var triggers []engine.Trigger
      for rows.Next() {
          var kind string
          var data []byte
          if err := rows.Scan(&kind, &data); err != nil {
              return nil, fmt.Errorf("postgres: entries %q: scan: %w", id, err)
          }
          trg, err := UnmarshalTrigger(kind, data)
          if err != nil {
              return nil, err
          }
          triggers = append(triggers, trg)
      }
      return triggers, rows.Err()
  }

  // mapConflict translates a Postgres serialization failure (SQLSTATE 40001) into
  // runtime.ErrConcurrentUpdate; other errors pass through unchanged.
  func mapConflict(err error) error {
      var pgErr *pgconn.PgError
      if errors.As(err, &pgErr) && pgErr.Code == "40001" {
          return runtime.ErrConcurrentUpdate
      }
      return err
  }
  ```
- [ ] 6.5 Run the tests, expect PASS:
  ```bash
  go test -run TestStore ./internal/persistence/postgres/...
  ```
- [ ] 6.6 Commit:
  ```
  feat(persistence): add Postgres transactional Store (snapshot CAS + journal + outbox)
  ```

---

## Task 7 — Postgres definition store + caching decorator (`internal/persistence/postgres` + `runtime`)

Implements spec §4b (durable definition store) and §6 (read-through, TTL,
single-flight definition cache).

**Files**

- Create: `internal/persistence/postgres/definitions.go`.
- Test: `internal/persistence/postgres/definitions_test.go` (testcontainers).
- Create: `runtime/caching_definition_registry.go`.
- Test: `runtime/caching_definition_registry_test.go`.

**Note on the `DefinitionRegistry` port.** The current port is
`Lookup(defRef string) (*model.ProcessDefinition, error)` (DefRef-keyed). The
spec describes the Postgres store as `PutDefinition` / `GetDefinition(defID,
version)`. Keep the existing `DefinitionRegistry.Lookup` port unchanged (the
engine calls it by DefRef); the Postgres store exposes `PutDefinition` /
`GetDefinition` AND satisfies `DefinitionRegistry` by parsing a `"defID:version"`
or `"defID"` DefRef in `Lookup`. The `CachingDefinitionRegistry` wraps any
`runtime.DefinitionRegistry` (read-through on `Lookup`).

**Interfaces**

- Produces (postgres):
  ```go
  type DefinitionStore struct { /* pool */ }
  func NewDefinitionStore(pool *pgxpool.Pool) *DefinitionStore
  func (d *DefinitionStore) PutDefinition(ctx context.Context, def *model.ProcessDefinition) error
  func (d *DefinitionStore) GetDefinition(ctx context.Context, defID string, version int) (*model.ProcessDefinition, error)
  func (d *DefinitionStore) Lookup(defRef string) (*model.ProcessDefinition, error) // satisfies runtime.DefinitionRegistry
  ```
- Produces (runtime):
  ```go
  type CachingDefinitionRegistry struct { /* backing, ttl, clock, singleflight guard */ }
  func NewCachingDefinitionRegistry(backing DefinitionRegistry, ttl time.Duration, clk clock.Clock) *CachingDefinitionRegistry
  func (c *CachingDefinitionRegistry) Lookup(defRef string) (*model.ProcessDefinition, error)
  ```

**Steps**

- [ ] 7.1 Write the failing cache test first
  `runtime/caching_definition_registry_test.go` (`package runtime_test`). Use a
  counting fake backing registry to prove the second lookup is served from cache,
  and that concurrent misses collapse to one backing call. Prefer stdlib
  `golang.org/x/sync/singleflight` (note: adds a dependency — already a common
  transitive dep; if avoiding new deps, hand-roll a per-key mutex guard):
  ```go
  package runtime_test

  import (
      "sync"
      "sync/atomic"
      "testing"
      "time"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/clock"
      "github.com/kartaladev/wrkflw/model"
      "github.com/kartaladev/wrkflw/runtime"
  )

  type countingRegistry struct {
      calls atomic.Int64
      def   *model.ProcessDefinition
  }

  func (c *countingRegistry) Lookup(string) (*model.ProcessDefinition, error) {
      c.calls.Add(1)
      return c.def, nil
  }

  func TestCachingDefinitionRegistry(t *testing.T) {
      tests := map[string]struct {
          assert func(t *testing.T, backing *countingRegistry, c *runtime.CachingDefinitionRegistry)
      }{
          "second lookup served from cache": {
              assert: func(t *testing.T, backing *countingRegistry, c *runtime.CachingDefinitionRegistry) {
                  _, err := c.Lookup("d:1")
                  require.NoError(t, err)
                  _, err = c.Lookup("d:1")
                  require.NoError(t, err)
                  require.Equal(t, int64(1), backing.calls.Load())
              },
          },
          "concurrent misses collapse to one backing call": {
              assert: func(t *testing.T, backing *countingRegistry, c *runtime.CachingDefinitionRegistry) {
                  var wg sync.WaitGroup
                  for range 50 {
                      wg.Add(1)
                      go func() { defer wg.Done(); _, _ = c.Lookup("d:1") }()
                  }
                  wg.Wait()
                  require.Equal(t, int64(1), backing.calls.Load())
              },
          },
      }
      for name, tc := range tests {
          t.Run(name, func(t *testing.T) {
              backing := &countingRegistry{def: &model.ProcessDefinition{ID: "d", Version: 1}}
              c := runtime.NewCachingDefinitionRegistry(backing, time.Minute, clock.System())
              tc.assert(t, backing, c)
          })
      }
  }
  ```
- [ ] 7.2 Run it, expect FAIL (`undefined: runtime.NewCachingDefinitionRegistry`):
  ```bash
  go test -run TestCachingDefinitionRegistry ./runtime/...
  ```
- [ ] 7.3 Create `runtime/caching_definition_registry.go` (single-flight via
  `golang.org/x/sync/singleflight`; TTL keyed by `clock.Clock`). If adding the dep,
  run `go get golang.org/x/sync/singleflight`:
  ```go
  package runtime

  import (
      "sync"
      "time"

      "golang.org/x/sync/singleflight"

      "github.com/kartaladev/wrkflw/clock"
      "github.com/kartaladev/wrkflw/model"
  )

  var _ DefinitionRegistry = (*CachingDefinitionRegistry)(nil)

  type cacheEntry struct {
      def       *model.ProcessDefinition
      expiresAt time.Time
  }

  // CachingDefinitionRegistry is a read-through, TTL'd, single-flight cache in
  // front of any DefinitionRegistry. Definitions are immutable per (defID,
  // version), so caching is safe (spec §6). Concurrent misses for the same DefRef
  // collapse to one backing Lookup via singleflight.
  type CachingDefinitionRegistry struct {
      backing DefinitionRegistry
      ttl     time.Duration
      clk     clock.Clock

      mu      sync.Mutex
      entries map[string]cacheEntry
      group   singleflight.Group
  }

  // NewCachingDefinitionRegistry wraps backing with a TTL'd single-flight cache.
  func NewCachingDefinitionRegistry(backing DefinitionRegistry, ttl time.Duration, clk clock.Clock) *CachingDefinitionRegistry {
      return &CachingDefinitionRegistry{
          backing: backing,
          ttl:     ttl,
          clk:     clk,
          entries: make(map[string]cacheEntry),
      }
  }

  // Lookup returns the cached definition for defRef, loading it through the
  // backing registry on a miss (single-flight, TTL-bounded).
  func (c *CachingDefinitionRegistry) Lookup(defRef string) (*model.ProcessDefinition, error) {
      now := c.clk.Now()

      c.mu.Lock()
      if e, ok := c.entries[defRef]; ok && now.Before(e.expiresAt) {
          c.mu.Unlock()
          return e.def, nil
      }
      c.mu.Unlock()

      v, err, _ := c.group.Do(defRef, func() (any, error) {
          def, err := c.backing.Lookup(defRef)
          if err != nil {
              return nil, err
          }
          c.mu.Lock()
          c.entries[defRef] = cacheEntry{def: def, expiresAt: c.clk.Now().Add(c.ttl)}
          c.mu.Unlock()
          return def, nil
      })
      if err != nil {
          return nil, err
      }
      return v.(*model.ProcessDefinition), nil
  }
  ```
- [ ] 7.4 Run the cache test, expect PASS:
  ```bash
  go test -run TestCachingDefinitionRegistry -race ./runtime/...
  ```
- [ ] 7.5 Write the failing definition-store test
  `internal/persistence/postgres/definitions_test.go` (`package postgres_test`,
  testcontainers): Put then Get round-trips, Get-missing errors, Lookup parses
  `"d:1"`:
  ```go
  package postgres_test

  import (
      "testing"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/database"
      pg "github.com/kartaladev/wrkflw/internal/persistence/postgres"
      "github.com/kartaladev/wrkflw/model"
  )

  func TestDefinitionStore(t *testing.T) {
      t.Parallel()
      pool := database.RunTestDatabase(t)
      require.NoError(t, pg.Migrate(t.Context(), pool))
      ds := pg.NewDefinitionStore(pool)

      def := &model.ProcessDefinition{ID: "d", Version: 1}
      require.NoError(t, ds.PutDefinition(t.Context(), def))

      got, err := ds.GetDefinition(t.Context(), "d", 1)
      require.NoError(t, err)
      require.Equal(t, "d", got.ID)

      viaRef, err := ds.Lookup("d:1")
      require.NoError(t, err)
      require.Equal(t, 1, viaRef.Version)

      _, err = ds.GetDefinition(t.Context(), "missing", 9)
      require.Error(t, err)
  }
  ```
- [ ] 7.6 Run it, expect FAIL (`undefined: pg.NewDefinitionStore`):
  ```bash
  go test -run TestDefinitionStore ./internal/persistence/postgres/...
  ```
- [ ] 7.7 Create `internal/persistence/postgres/definitions.go`:
  ```go
  package postgres

  import (
      "context"
      "encoding/json"
      "errors"
      "fmt"
      "strconv"
      "strings"
      "time"

      "github.com/jackc/pgx/v5"
      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/kartaladev/wrkflw/model"
      "github.com/kartaladev/wrkflw/runtime"
  )

  var _ runtime.DefinitionRegistry = (*DefinitionStore)(nil)

  // DefinitionStore is the Postgres-backed durable process-definition store. It
  // satisfies runtime.DefinitionRegistry by resolving "defID:version" (or "defID"
  // for the latest) DefRefs.
  type DefinitionStore struct {
      pool *pgxpool.Pool
  }

  // NewDefinitionStore constructs a DefinitionStore over pool.
  func NewDefinitionStore(pool *pgxpool.Pool) *DefinitionStore { return &DefinitionStore{pool: pool} }

  // PutDefinition upserts an immutable definition keyed by (def_id, version).
  func (d *DefinitionStore) PutDefinition(ctx context.Context, def *model.ProcessDefinition) error {
      data, err := json.Marshal(def)
      if err != nil {
          return fmt.Errorf("postgres: put definition: marshal: %w", err)
      }
      if _, err := d.pool.Exec(ctx,
          `INSERT INTO wrkflw_definitions (def_id, version, definition, created_at)
           VALUES ($1,$2,$3,$4)
           ON CONFLICT (def_id, version) DO UPDATE SET definition = EXCLUDED.definition`,
          def.ID, def.Version, data, time.Now().UTC(),
      ); err != nil {
          return fmt.Errorf("postgres: put definition %s:%d: %w", def.ID, def.Version, err)
      }
      return nil
  }

  // GetDefinition fetches a definition by (defID, version).
  func (d *DefinitionStore) GetDefinition(ctx context.Context, defID string, version int) (*model.ProcessDefinition, error) {
      var data []byte
      err := d.pool.QueryRow(ctx,
          `SELECT definition FROM wrkflw_definitions WHERE def_id = $1 AND version = $2`, defID, version,
      ).Scan(&data)
      if errors.Is(err, pgx.ErrNoRows) {
          return nil, fmt.Errorf("%w: %s:%d", runtime.ErrDefinitionNotFound, defID, version)
      }
      if err != nil {
          return nil, fmt.Errorf("postgres: get definition %s:%d: %w", defID, version, err)
      }
      var def model.ProcessDefinition
      if err := json.Unmarshal(data, &def); err != nil {
          return nil, fmt.Errorf("postgres: get definition %s:%d: unmarshal: %w", defID, version, err)
      }
      return &def, nil
  }

  // Lookup satisfies runtime.DefinitionRegistry. defRef is "defID:version" or
  // "defID" (latest version).
  func (d *DefinitionStore) Lookup(defRef string) (*model.ProcessDefinition, error) {
      ctx := context.Background()
      if id, ver, ok := strings.Cut(defRef, ":"); ok {
          n, err := strconv.Atoi(ver)
          if err != nil {
              return nil, fmt.Errorf("postgres: lookup %q: bad version: %w", defRef, err)
          }
          return d.GetDefinition(ctx, id, n)
      }
      var data []byte
      err := d.pool.QueryRow(ctx,
          `SELECT definition FROM wrkflw_definitions WHERE def_id = $1 ORDER BY version DESC LIMIT 1`, defRef,
      ).Scan(&data)
      if errors.Is(err, pgx.ErrNoRows) {
          return nil, fmt.Errorf("%w: %s", runtime.ErrDefinitionNotFound, defRef)
      }
      if err != nil {
          return nil, fmt.Errorf("postgres: lookup %q: %w", defRef, err)
      }
      var def model.ProcessDefinition
      if err := json.Unmarshal(data, &def); err != nil {
          return nil, fmt.Errorf("postgres: lookup %q: unmarshal: %w", defRef, err)
      }
      return &def, nil
  }
  ```
- [ ] 7.8 `go mod tidy`, run the tests, expect PASS:
  ```bash
  go test -run 'TestDefinitionStore|TestCachingDefinitionRegistry' ./internal/persistence/postgres/... ./runtime/...
  ```
- [ ] 7.9 Commit:
  ```
  feat(persistence): add Postgres definition store and caching definition registry
  ```

---

## Task 8 — Outbox relay (broker-agnostic) (`internal/persistence/postgres` + `persistence`)

Implements spec §5: `SELECT ... FOR UPDATE SKIP LOCKED`, at-least-once,
configurable poll interval/batch, `Run(ctx)` until cancel. No watermill.

**Files**

- Create: `persistence/publisher.go` (the `Publisher` interface — module-root,
  consumer-facing; re-exported from `runtime` is not needed since it is purely a
  relay concern, but place the interface where the consumer wires it: the
  `persistence` façade).
- Create: `internal/persistence/postgres/relay.go`.
- Test: `internal/persistence/postgres/relay_test.go` (testcontainers).

**Interfaces**

- Produces (persistence):
  ```go
  type Publisher interface {
      Publish(ctx context.Context, ev runtime.OutboxEvent) error
  }
  ```
- Produces (postgres):
  ```go
  type Relay struct { /* pool, pub, pollInterval, batch */ }
  type RelayOption func(*Relay)
  func WithPollInterval(d time.Duration) RelayOption
  func WithBatchSize(n int) RelayOption
  func NewRelay(pool *pgxpool.Pool, pub persistence.Publisher, opts ...RelayOption) *Relay
  func (r *Relay) Run(ctx context.Context) error
  func (r *Relay) drainOnce(ctx context.Context) (int, error) // claim+publish one batch
  ```

> Import-direction note: `internal/persistence/postgres` importing the root
> `persistence` package risks a cycle if `persistence` imports
> `internal/persistence/postgres`. To avoid it, declare the `Publisher`
> interface in `runtime` (alongside `OutboxEvent`) and have `persistence`
> re-export it. **Adopt that:** put `Publisher` in `runtime/publisher.go`;
> `persistence` aliases it. Update the signatures above to `runtime.Publisher`.

**Steps**

- [ ] 8.1 Write failing test `runtime/publisher_test.go` (`package runtime_test`)
  asserting the `Publisher` interface shape via a compile-time satisfaction check
  + a trivial fake:
  ```go
  package runtime_test

  import (
      "context"
      "testing"

      "github.com/kartaladev/wrkflw/runtime"
  )

  type fakePub struct{ got []runtime.OutboxEvent }

  func (f *fakePub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
      f.got = append(f.got, ev)
      return nil
  }

  func TestPublisherInterface(t *testing.T) {
      var _ runtime.Publisher = (*fakePub)(nil)
  }
  ```
- [ ] 8.2 Run it, expect FAIL (`undefined: runtime.Publisher`):
  ```bash
  go test -run TestPublisherInterface ./runtime/...
  ```
- [ ] 8.3 Create `runtime/publisher.go`:
  ```go
  package runtime

  import "context"

  // Publisher relays one outbox event to the eventing backend. Implementations
  // must be idempotent downstream (delivery is at-least-once; the outbox
  // dedup_key supports deduplication). The persistence relay calls Publish for
  // each claimed unpublished row. No broker is imported here — the Eventing
  // sub-project supplies a watermill-backed Publisher.
  type Publisher interface {
      Publish(ctx context.Context, ev OutboxEvent) error
  }
  ```
- [ ] 8.4 Run it, expect PASS:
  ```bash
  go test -run TestPublisherInterface ./runtime/...
  ```
- [ ] 8.5 Write failing relay test `internal/persistence/postgres/relay_test.go`
  (`package postgres_test`, testcontainers). Seed outbox rows directly, then:
  (a) `drainOnce` drains rows to a fake publisher and marks them published;
  (b) two relays via `SKIP LOCKED` cooperate without double-publish;
  (c) a publish error leaves the row unpublished. Use the `assert` closure form:
  ```go
  package postgres_test

  import (
      "context"
      "errors"
      "sync"
      "testing"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/database"
      pg "github.com/kartaladev/wrkflw/internal/persistence/postgres"
      "github.com/kartaladev/wrkflw/runtime"
  )

  type recordingPub struct {
      mu  sync.Mutex
      got []string
  }

  func (p *recordingPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
      p.mu.Lock()
      defer p.mu.Unlock()
      p.got = append(p.got, ev.Topic)
      return nil
  }

  type failingPub struct{}

  func (failingPub) Publish(context.Context, runtime.OutboxEvent) error { return errors.New("down") }

  func seedOutbox(t *testing.T, pool any, n int) { /* INSERT n rows via the pool */ }

  func TestRelayDrainsRows(t *testing.T) {
      t.Parallel()
      pool := database.RunTestDatabase(t)
      require.NoError(t, pg.Migrate(t.Context(), pool))
      // seed 3 unpublished rows (insert directly; instance_id need not FK here).
      // ... INSERT INTO wrkflw_outbox(...) x3 ...
      pub := &recordingPub{}
      relay := pg.NewRelay(pool, pub)
      n, err := relay.DrainOnce(t.Context())
      require.NoError(t, err)
      require.Equal(t, 3, n)
      require.Len(t, pub.got, 3)

      // second drain finds nothing.
      n, err = relay.DrainOnce(t.Context())
      require.NoError(t, err)
      require.Equal(t, 0, n)
  }

  func TestRelaySkipLockedNoDoublePublish(t *testing.T) {
      t.Parallel()
      pool := database.RunTestDatabase(t)
      require.NoError(t, pg.Migrate(t.Context(), pool))
      // seed N rows; run two relays concurrently; assert total published == N, no dupes.
  }

  func TestRelayPublishErrorLeavesRowUnpublished(t *testing.T) {
      t.Parallel()
      pool := database.RunTestDatabase(t)
      require.NoError(t, pg.Migrate(t.Context(), pool))
      // seed 1 row; DrainOnce with failingPub returns an error and leaves published_at NULL.
      relay := pg.NewRelay(pool, failingPub{})
      _, err := relay.DrainOnce(t.Context())
      require.Error(t, err)
      // assert the row is still unpublished via a count query.
  }
  ```
  (Flesh out `seedOutbox` and the count assertions against the real pool; trust
  the build.)
- [ ] 8.6 Run it, expect FAIL (`undefined: pg.NewRelay` / `pg.(*Relay).DrainOnce`):
  ```bash
  go test -run TestRelay ./internal/persistence/postgres/...
  ```
- [ ] 8.7 Create `internal/persistence/postgres/relay.go`. `DrainOnce` opens a tx,
  claims a batch with `FOR UPDATE SKIP LOCKED`, publishes each, marks
  `published_at = now()`, commits; on a publish error it returns the error and
  rolls back (leaving rows unpublished). `Run` loops `DrainOnce` on a ticker until
  ctx cancel:
  ```go
  package postgres

  import (
      "context"
      "errors"
      "fmt"
      "time"

      "github.com/jackc/pgx/v5"
      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/kartaladev/wrkflw/runtime"
  )

  // Relay drains wrkflw_outbox and hands each event to a Publisher (at-least-once).
  type Relay struct {
      pool         *pgxpool.Pool
      pub          runtime.Publisher
      pollInterval time.Duration
      batch        int
  }

  // RelayOption configures a Relay.
  type RelayOption func(*Relay)

  // WithPollInterval sets the poll interval between drain attempts.
  func WithPollInterval(d time.Duration) RelayOption { return func(r *Relay) { r.pollInterval = d } }

  // WithBatchSize sets the max rows claimed per drain.
  func WithBatchSize(n int) RelayOption { return func(r *Relay) { r.batch = n } }

  // NewRelay constructs a Relay over pool publishing to pub.
  func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...RelayOption) *Relay {
      r := &Relay{pool: pool, pub: pub, pollInterval: time.Second, batch: 100}
      for _, o := range opts {
          o(r)
      }
      return r
  }

  // Run drains the outbox on the poll interval until ctx is cancelled.
  func (r *Relay) Run(ctx context.Context) error {
      ticker := time.NewTicker(r.pollInterval)
      defer ticker.Stop()
      for {
          if _, err := r.DrainOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
              return err
          }
          select {
          case <-ctx.Done():
              return ctx.Err()
          case <-ticker.C:
          }
      }
  }

  // DrainOnce claims one batch of unpublished rows (FOR UPDATE SKIP LOCKED),
  // publishes each, and marks them published in the same tx. Returns the number
  // published. A publish error rolls the batch back (rows stay unpublished).
  func (r *Relay) DrainOnce(ctx context.Context) (int, error) {
      tx, err := r.pool.Begin(ctx)
      if err != nil {
          return 0, fmt.Errorf("postgres: relay: begin: %w", err)
      }
      defer func() { _ = tx.Rollback(ctx) }()

      rows, err := tx.Query(ctx,
          `SELECT id, topic, payload FROM wrkflw_outbox
            WHERE published_at IS NULL
            ORDER BY id
            FOR UPDATE SKIP LOCKED
            LIMIT $1`, r.batch)
      if err != nil {
          return 0, fmt.Errorf("postgres: relay: claim: %w", err)
      }

      type claim struct {
          id    int64
          event runtime.OutboxEvent
      }
      var claims []claim
      for rows.Next() {
          var id int64
          var topic string
          var payload []byte
          if err := rows.Scan(&id, &topic, &payload); err != nil {
              rows.Close()
              return 0, fmt.Errorf("postgres: relay: scan: %w", err)
          }
          ev := runtime.OutboxEvent{Topic: topic}
          if err := unmarshalPayload(payload, &ev); err != nil {
              rows.Close()
              return 0, err
          }
          claims = append(claims, claim{id: id, event: ev})
      }
      rows.Close()
      if err := rows.Err(); err != nil {
          return 0, fmt.Errorf("postgres: relay: rows: %w", err)
      }

      for _, c := range claims {
          if err := r.pub.Publish(ctx, c.event); err != nil {
              return 0, fmt.Errorf("postgres: relay: publish id=%d: %w", c.id, err)
          }
          if _, err := tx.Exec(ctx,
              `UPDATE wrkflw_outbox SET published_at = now() WHERE id = $1`, c.id,
          ); err != nil {
              return 0, fmt.Errorf("postgres: relay: mark id=%d: %w", c.id, err)
          }
      }
      if err := tx.Commit(ctx); err != nil {
          return 0, fmt.Errorf("postgres: relay: commit: %w", err)
      }
      return len(claims), nil
  }

  func unmarshalPayload(data []byte, ev *runtime.OutboxEvent) error {
      m := map[string]any{}
      if err := jsonUnmarshal(data, &m); err != nil {
          return fmt.Errorf("postgres: relay: unmarshal payload: %w", err)
      }
      ev.Payload = m
      return nil
  }

  // jsonUnmarshal is a tiny seam so the relay does not import encoding/json twice.
  func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

  var _ = pgx.ErrNoRows
  ```
  (Replace the `jsonUnmarshal` seam with a direct `encoding/json` import; the
  `var _ = pgx.ErrNoRows` anchor is illustrative — remove unused-import anchors
  before committing. Trust the build.)
- [ ] 8.8 Run the relay tests, expect PASS:
  ```bash
  go test -run TestRelay -race ./internal/persistence/postgres/...
  ```
- [ ] 8.9 Commit:
  ```
  feat(persistence): add broker-agnostic outbox relay with SKIP LOCKED claiming
  ```

---

## Task 9 — `persistence` root façade (`persistence`, module root)

Implements spec §7 / ADR-0008: the consumer-facing façade delegating to
`internal/persistence/postgres`, plus an end-to-end test driving a real
`runtime.Runner` against Postgres.

**Files**

- Create: `persistence/persistence.go` (`OpenPostgres`, `Migrate`, definition
  store / cache / relay constructors, `Publisher` alias, re-exported sentinels).
- Test: `persistence/persistence_test.go` (testcontainers, end-to-end).

**Interfaces**

- Produces:
  ```go
  type Store = postgres.Store // or a wrapper exposing only runtime.Store
  type Publisher = runtime.Publisher
  type Relay = postgres.Relay

  func OpenPostgres(ctx context.Context, pool *pgxpool.Pool, opts ...Option) (*postgres.Store, error)
  func Migrate(ctx context.Context, pool *pgxpool.Pool) error
  func NewDefinitionStore(pool *pgxpool.Pool) *postgres.DefinitionStore
  func NewCachingDefinitionRegistry(backing runtime.DefinitionRegistry, ttl time.Duration, clk clock.Clock) *runtime.CachingDefinitionRegistry
  func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...postgres.RelayOption) *postgres.Relay

  var ErrConcurrentUpdate = runtime.ErrConcurrentUpdate
  var ErrInstanceNotFound = runtime.ErrInstanceNotFound

  type Option func(*config)
  ```

**Steps**

- [ ] 9.1 Write the failing e2e test `persistence/persistence_test.go`
  (`package persistence_test`). It wires `OpenPostgres` + `Migrate` and drives a
  tiny start→end process through a real `runtime.Runner`, then asserts the
  instance loads back as completed and an `instance.completed` outbox row exists:
  ```go
  package persistence_test

  import (
      "testing"

      "github.com/stretchr/testify/require"
      "github.com/kartaladev/wrkflw/clock"
      "github.com/kartaladev/wrkflw/database"
      "github.com/kartaladev/wrkflw/engine"
      "github.com/kartaladev/wrkflw/persistence"
      "github.com/kartaladev/wrkflw/runtime"
  )

  func TestOpenPostgresEndToEnd(t *testing.T) {
      t.Parallel()
      pool := database.RunTestDatabase(t)
      require.NoError(t, persistence.Migrate(t.Context(), pool))

      store, err := persistence.OpenPostgres(t.Context(), pool)
      require.NoError(t, err)

      // A minimal start→end definition that completes in one drive. Reuse the
      // helper used by runtime tests; trust the test, not this listing.
      def := minimalStartEndDefinition(t)

      r := runtime.NewRunner(nil, clock.System(), store)
      st, err := r.Run(t.Context(), def, "i-e2e", map[string]any{"k": "v"})
      require.NoError(t, err)
      require.Equal(t, engine.StatusCompleted, st.Status)

      reloaded, _, err := store.Load(t.Context(), "i-e2e")
      require.NoError(t, err)
      require.Equal(t, engine.StatusCompleted, reloaded.Status)

      var n int
      require.NoError(t, pool.QueryRow(t.Context(),
          `SELECT count(*) FROM wrkflw_outbox WHERE topic = 'instance.completed'`).Scan(&n))
      require.Equal(t, 1, n)
  }
  ```
- [ ] 9.2 Run it, expect FAIL (`undefined: persistence.OpenPostgres`):
  ```bash
  go test -run TestOpenPostgresEndToEnd ./persistence/...
  ```
- [ ] 9.3 Create `persistence/persistence.go`:
  ```go
  // Package persistence is the consumer-facing façade over the internal
  // Postgres-backed persistence implementation (ADR-0008). It exposes
  // constructors, options, and re-exported sentinels; the SQL/pgx/relay plumbing
  // lives in internal/persistence/postgres and is never imported by consumers.
  package persistence

  import (
      "context"
      "time"

      "github.com/jackc/pgx/v5/pgxpool"
      "github.com/kartaladev/wrkflw/clock"
      "github.com/kartaladev/wrkflw/internal/persistence/postgres"
      "github.com/kartaladev/wrkflw/runtime"
  )

  // Publisher is the broker-agnostic outbox publisher (alias of runtime.Publisher).
  type Publisher = runtime.Publisher

  // Relay drains the transactional outbox to a Publisher.
  type Relay = postgres.Relay

  // RelayOption configures a Relay.
  type RelayOption = postgres.RelayOption

  // Re-exported sentinels so consumers match on the façade package.
  var (
      ErrConcurrentUpdate = runtime.ErrConcurrentUpdate
      ErrInstanceNotFound = runtime.ErrInstanceNotFound
  )

  type config struct{}

  // Option configures the Postgres store (reserved for future use).
  type Option func(*config)

  // OpenPostgres constructs a Postgres-backed runtime.Store over pool. The
  // returned *postgres.Store satisfies runtime.Store and runtime.JournalReader.
  func OpenPostgres(_ context.Context, pool *pgxpool.Pool, opts ...Option) (*postgres.Store, error) {
      var c config
      for _, o := range opts {
          o(&c)
      }
      return postgres.NewStore(pool), nil
  }

  // Migrate applies the embedded schema migrations to pool (consumer-run, never
  // auto-run on import).
  func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
      return postgres.Migrate(ctx, pool)
  }

  // NewDefinitionStore constructs the durable Postgres definition store.
  func NewDefinitionStore(pool *pgxpool.Pool) *postgres.DefinitionStore {
      return postgres.NewDefinitionStore(pool)
  }

  // NewCachingDefinitionRegistry wraps backing with a TTL'd single-flight cache.
  func NewCachingDefinitionRegistry(backing runtime.DefinitionRegistry, ttl time.Duration, clk clock.Clock) *runtime.CachingDefinitionRegistry {
      return runtime.NewCachingDefinitionRegistry(backing, ttl, clk)
  }

  // NewRelay constructs an outbox relay over pool publishing to pub.
  func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...postgres.RelayOption) *postgres.Relay {
      return postgres.NewRelay(pool, pub, opts...)
  }
  ```
- [ ] 9.4 Provide `minimalStartEndDefinition(t)` in the test from the existing
  runtime test fixtures (grep `runner_test.go` for a one-node start→end def).
  Run it, expect PASS:
  ```bash
  go test -run TestOpenPostgresEndToEnd ./persistence/...
  ```
- [ ] 9.5 Commit:
  ```
  feat(persistence): add module-root façade (OpenPostgres, Migrate, Relay, cache)
  ```

---

## Task 10 — Verification & docs

Implements spec §9. Light task; folds in the doc update.

**Steps**

- [ ] 10.1 Full race + coverage gate over all touched packages:
  ```bash
  go test -race -coverprofile=cover.out ./runtime/... ./database/... ./internal/persistence/... ./persistence/... && go tool cover -func=cover.out | tail -1
  ```
  Expected: all `ok`; `total:` ≥ 85%. If a package is below 85%, add the missing
  red→green cases (e.g. error branches in `store.go` / `relay.go`).
- [ ] 10.2 Whole-module regression:
  ```bash
  go test ./...
  ```
  Expected: every package `ok` (no regressions from the Task 3 port collapse).
- [ ] 10.3 Lint clean:
  ```bash
  golangci-lint run ./...
  ```
  Expected: no findings.
- [ ] 10.4 Assert no forbidden imports under the persistence packages:
  ```bash
  ! grep -rn "watermill\|casbin\|gocron\|clockwork" persistence/ internal/persistence/ && echo "clean"
  ```
  Expected: prints `clean` (grep finds nothing, so `!` succeeds).
- [ ] 10.5 Update `docs/plans/HANDOVER.md` persistence section: mark Persistence
  (PostgreSQL) DONE, note the `Store` port collapse (ADR-0007), the
  façade/internal split (ADR-0008), the snapshot-JSONB schema (ADR-0006), the
  broker-agnostic relay awaiting an Eventing `Publisher`, and the
  deferred follow-ups (owned-instance cache, history cap, LISTEN/NOTIFY,
  per-aggregate relay ordering, TOAST/fillfactor tuning).
- [ ] 10.6 Commit:
  ```
  docs(persistence): record completion and follow-ups in HANDOVER
  ```

---

## Spec → task coverage map (self-review)

- §2 (transaction boundary, per-Step atomic Commit, outbox derived before tx) → Tasks 2, 3, 6.
- §3 (port redesign: `Token`/`OutboxEvent`/`AppliedStep`/`Store`/`JournalReader`/`ErrConcurrentUpdate`, `MemStore`, trigger codec) → Tasks 1, 3, 4.
- §4 (storage shape: snapshot-JSONB + projected columns, schema, migrations) → Tasks 5, 6.
- §4b (definition persistence for recovery) → Task 7.
- §5 (broker-agnostic relay: SKIP LOCKED, at-least-once, dedup, options, `Run`) → Task 8.
- §6 (hot-path cache: definitions + single-flight, NOT instance state) → Task 7.
- §7 (layout: `internal/persistence/postgres`, `persistence` façade, `runtime` ports; pgx+pgxpool, `DBTX`, JSONB, testcontainers helper) → Tasks 5, 6, 8, 9.
- §9 (verification: race+coverage, testcontainers, lint, no forbidden imports, e2e against Postgres) → Tasks 9, 10.

Symbol-name consistency (verified identical across tasks): `Store`, `Token`,
`AppliedStep`, `OutboxEvent`, `outboxEventsFor`, `Commit`, `Create`, `Load`,
`Entries`, `ErrConcurrentUpdate`, `ErrInstanceNotFound`, `JournalReader`,
`MemStore`, `DBTX`, `Relay`, `Publisher`, `DrainOnce`, `DefinitionStore`,
`CachingDefinitionRegistry`, `MarshalTrigger`/`UnmarshalTrigger`, `OpenPostgres`,
`Migrate`.
