# Optional `clock.Clock` via Option — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `clock.Clock` an optional dependency on every constructor that takes it — supplied via a `With<Component>Clock` functional option defaulting to `clock.System()`, instead of a required positional parameter.

**Architecture:** Convert each positional `clk clock.Clock` parameter to a per-component option following the repo's existing `With<Component>Clock` naming (avoids same-package `WithClock` collisions). Constructors initialise the clock field to `clock.System()` before applying options; the option setter ignores a nil clock. The engine core (`engine/`, `model/`) is untouched — it never constructs a clock.

**Tech Stack:** Go 1.25, `github.com/kartaladev/wrkflw/clock` (in-repo `clock.Clock` + `clock.System()`), functional-options pattern.

## Global Constraints

- Go 1.25; module path `github.com/kartaladev/wrkflw`.
- TDD strict (CLAUDE.md): every new symbol/behaviour change gets a failing test (visible red) before implementation.
- Black-box tests preferred (`<package>_test`). Table tests use the project `table-test` skill's `assert` closure form when 2+ cases share one SUT call.
- Never import `clockwork` from `runtime`/`engine`/workflow code — depend on `clock.Clock` (ADR-0003).
- Touched packages: keep `go build ./...` green and all `examples/` compiling after every task.
- Conventional Commits scoped to the area; commit per task. End commit messages with the `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` trailer.
- Spec: `docs/specs/optional-clock-via-option.md`.
- Naming: `With<Component>Clock` (e.g. `WithClock`, `WithClock`). Default when absent or nil: `clock.System()`.
- Out of scope: the `clockwork.Clock` gocron constructors (`scheduling.NewScheduler`, `internal/scheduling/gocron.NewGocronScheduler`, `WithElectorClock`).

---

### Task 1: Record the ADR

**Files:**
- Create: `docs/adr/NNNN-optional-clock-via-option.md` (use the next free ADR number; check `ls docs/adr/ | tail -1` — currently the next free is **0066**, but the SendTask-outbox track may also claim 0066; take whichever is free at execution time).

**Interfaces:**
- Produces: the decision record referenced by every later commit.

- [ ] **Step 1: Write the ADR (Nygard template)**

Create the file with this content (substitute the real number for `NNNN`):

```markdown
# NNNN. Make clock.Clock optional via a With<Component>Clock option

- Status: Accepted
- Date: 2026-06-26

## Context

Per ADR-0003 stateful components depend on the in-repo `clock.Clock` interface. Many
constructors took the clock as a required positional parameter. The consumer wants the
clock to be optional — when not provided, default to the system clock (`clock.System()`).
Several components already expose the clock as a `With<Component>Clock` option defaulting
to `clock.System()`; this decision makes that pattern uniform across the codebase.

## Decision

Move every positional `clock.Clock` parameter to a `With<Component>Clock(clk clock.Clock)`
functional option. The constructor initialises the clock field to `clock.System()` before
applying options; the option setter ignores a nil clock (an explicit nil falls back to the
system clock). Naming follows `With<Component>Clock` because Go forbids two `WithClock`
functions in one package and several packages host multiple clock-bearing constructors.

The `clockwork.Clock` gocron-adapter constructors are excluded — they take a different type
at the deliberately-confined vendor seam.

## Consequences

- The clock argument becomes optional and self-documenting at the call site; production
  wiring can omit it.
- This relaxes ADR-0003's compile-time guarantee that every caller passes a clock. A test
  that forgets to inject its fake clock will no longer fail to compile; it will run against
  wall time and may be non-deterministic. Mitigation: determinism-sensitive tests continue
  to inject a fake via `With<Component>Clock(fake)`; reviewers must ensure new deterministic
  tests do the same.
- The engine core (`engine/`, `model/`) is unaffected — it receives time via triggers/
  commands and never constructs a clock, so its wall-clock-free determinism is preserved.
- Amends the injection convention of ADR-0003 (does not supersede it).
```

- [ ] **Step 2: Commit**

```bash
git add docs/adr/NNNN-optional-clock-via-option.md
git commit -m "docs(adr): make clock.Clock optional via With<Component>Clock option

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: `NewMemScheduler` → `WithMemSchedulerClock`

**Files:**
- Modify: `runtime/scheduler.go` (add option type + option; change `NewMemScheduler` signature)
- Test: `runtime/scheduler_test.go`

**Interfaces:**
- Produces: `func NewMemScheduler(opts ...MemSchedulerOption) *MemScheduler`; `type MemSchedulerOption func(*MemScheduler)`; `func WithMemSchedulerClock(clk clock.Clock) MemSchedulerOption`.

- [ ] **Step 1: Write the failing tests**

Add to `runtime/scheduler_test.go` (package `runtime_test`):

```go
func TestNewMemSchedulerDefaultUsesSystemClock(t *testing.T) {
	// No clock option → uses clock.System(); a past-due timer fires on Tick.
	s := runtime.NewMemScheduler()
	fired := false
	s.Schedule("t1", time.Now().Add(-time.Second), func() { fired = true })
	require.NoError(t, s.Tick(t.Context()))
	assert.True(t, fired, "past-due timer should fire under the system clock")
}

func TestNewMemSchedulerWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	s := runtime.NewMemScheduler(runtime.WithMemSchedulerClock(fake))
	fired := false
	s.Schedule("t1", time.Unix(999, 0), func() { fired = true }) // fireAt <= fake now
	require.NoError(t, s.Tick(t.Context()))
	assert.True(t, fired, "timer due at the fake clock's now should fire")
}
```

Ensure imports include `time`, `github.com/jonboulle/clockwork`, testify `assert`/`require`, and `github.com/kartaladev/wrkflw/runtime`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./runtime/ -run 'TestNewMemScheduler' -v`
Expected: FAIL — compile error `not enough arguments` / `undefined: runtime.WithMemSchedulerClock`.

- [ ] **Step 3: Implement the option and new signature**

In `runtime/scheduler.go`, replace the constructor and add the option:

```go
// MemSchedulerOption configures a MemScheduler.
type MemSchedulerOption func(*MemScheduler)

// WithMemSchedulerClock sets the time source used to evaluate timer due-ness.
// Default: clock.System(). A nil clock is ignored. Inject a fake clock in tests.
func WithMemSchedulerClock(clk clock.Clock) MemSchedulerOption {
	return func(s *MemScheduler) {
		if clk != nil {
			s.clk = clk
		}
	}
}

// NewMemScheduler constructs a MemScheduler. The time source defaults to
// clock.System(); override it with WithMemSchedulerClock (e.g. a fake clock in tests).
func NewMemScheduler(opts ...MemSchedulerOption) *MemScheduler {
	s := &MemScheduler{
		clk:     clock.System(),
		pending: make(map[string]pendingTimer),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}
```

- [ ] **Step 4: Update all callers**

Run: `go build ./...`
For each `NewMemScheduler(<clk>)` the compiler flags: if `<clk>` was a fake/real clock, rewrite as `NewMemScheduler(runtime.WithMemSchedulerClock(<clk>))` (or `WithMemSchedulerClock` inside package `runtime`); if it was `clock.System()`, drop the argument entirely. Repeat until `go build ./...` is clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestNewMemScheduler' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add runtime/scheduler.go runtime/scheduler_test.go
git commit -m "refactor(runtime): make NewMemScheduler clock optional via WithMemSchedulerClock

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: `NewSignalBus` → `WithClock`

**Files:**
- Modify: `runtime/broadcast.go` (add option type + option; change `NewSignalBus` signature, `deliver` stays positional)
- Test: `runtime/broadcast_test.go`

**Interfaces:**
- Produces: `func NewSignalBus(deliver DeliverFunc, opts ...SignalBusOption) *SignalBus`; `type SignalBusOption func(*SignalBus)`; `func WithClock(clk clock.Clock) SignalBusOption`.

- [ ] **Step 1: Write the failing tests**

Add to `runtime/broadcast_test.go` (package `runtime_test`):

```go
func TestNewSignalBusDefaultUsesSystemClock(t *testing.T) {
	var got time.Time
	deliver := func(_ context.Context, _ string, trg engine.Trigger) error {
		got = trg.OccurredAt()
		return nil
	}
	bus := runtime.NewSignalBus(deliver)
	bus.Subscribe("inst-1", "sig")
	before := time.Now()
	require.NoError(t, bus.Publish(t.Context(), "sig", nil))
	after := time.Now()
	assert.False(t, got.Before(before) || got.After(after), "SignalReceived should be stamped from the system clock")
}

func TestNewSignalBusWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	var got time.Time
	deliver := func(_ context.Context, _ string, trg engine.Trigger) error {
		got = trg.OccurredAt()
		return nil
	}
	bus := runtime.NewSignalBus(deliver, runtime.WithClock(fake))
	bus.Subscribe("inst-1", "sig")
	require.NoError(t, bus.Publish(t.Context(), "sig", nil))
	assert.Equal(t, time.Unix(1000, 0).UTC(), got.UTC())
}
```

Ensure imports include `context`, `time`, `clockwork`, the `engine` package, testify, and `runtime`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./runtime/ -run 'TestNewSignalBus' -v`
Expected: FAIL — `undefined: runtime.WithClock` / signature mismatch.

- [ ] **Step 3: Implement the option and new signature**

In `runtime/broadcast.go`, replace the constructor and add the option:

```go
// SignalBusOption configures a SignalBus.
type SignalBusOption func(*SignalBus)

// WithClock sets the time source used to stamp SignalReceived triggers.
// Default: clock.System(). A nil clock is ignored. Pass the Runner's fake clock in
// tests so downstream timers anchored to the signal timestamp stay deterministic.
func WithClock(clk clock.Clock) SignalBusOption {
	return func(b *SignalBus) {
		if clk != nil {
			b.clk = clk
		}
	}
}

// NewSignalBus constructs a SignalBus backed by the given delivery function.
// deliver is called once per registered waiter for each Publish. The time source
// defaults to clock.System(); override it with WithClock (ADR-0003).
func NewSignalBus(deliver DeliverFunc, opts ...SignalBusOption) *SignalBus {
	b := &SignalBus{
		clk:     clock.System(),
		waiters: make(map[string]map[string]struct{}),
		deliver: deliver,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}
```

Also update the doc-comment example in the `SignalBus` type comment from
`runtime.NewSignalBus(clk, func(...){...})` to `runtime.NewSignalBus(func(...){...}, runtime.WithClock(clk))`.

- [ ] **Step 4: Update all callers**

Run: `go build ./...`
For each `NewSignalBus(<clk>, <deliver>)`: rewrite as `NewSignalBus(<deliver>, runtime.WithClock(<clk>))` when a fake/explicit clock was passed; drop the clock and reorder to `NewSignalBus(<deliver>)` when it was `clock.System()`. Note the **argument order swap** (deliver is now first). Repeat until clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestNewSignalBus' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add runtime/broadcast.go runtime/broadcast_test.go
git commit -m "refactor(runtime): make NewSignalBus clock optional via WithClock

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: `NewCachingDefinitionRegistry` (runtime) → `WithCachingDefinitionRegistryClock`

**Files:**
- Modify: `runtime/caching_definition_registry.go`
- Test: `runtime/caching_definition_registry_test.go`

**Interfaces:**
- Produces: `func NewCachingDefinitionRegistry(backing DefinitionRegistry, ttl time.Duration, opts ...CachingDefinitionRegistryOption) *CachingDefinitionRegistry`; `type CachingDefinitionRegistryOption func(*CachingDefinitionRegistry)`; `func WithCachingDefinitionRegistryClock(clk clock.Clock) CachingDefinitionRegistryOption`.

- [ ] **Step 1: Write the failing tests**

Add to `runtime/caching_definition_registry_test.go` (package `runtime_test`). Use the package's existing fake `DefinitionRegistry` test double (reuse whatever stub the existing tests use; if none, define a tiny inline stub that returns a fixed `*model.ProcessDefinition` and counts calls):

```go
func TestNewCachingDefinitionRegistryDefaultUsesSystemClock(t *testing.T) {
	backing := &countingRegistry{def: &model.ProcessDefinition{ID: "d", Version: 1}}
	c := runtime.NewCachingDefinitionRegistry(backing, time.Minute) // no clock option
	_, err := c.Lookup(t.Context(), "d:1")
	require.NoError(t, err)
	_, err = c.Lookup(t.Context(), "d:1") // within TTL → cache hit, no second backing call
	require.NoError(t, err)
	assert.Equal(t, 1, backing.calls, "second lookup within TTL should hit cache under the system clock")
}

func TestNewCachingDefinitionRegistryWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	backing := &countingRegistry{def: &model.ProcessDefinition{ID: "d", Version: 1}}
	c := runtime.NewCachingDefinitionRegistry(backing, time.Minute, runtime.WithCachingDefinitionRegistryClock(fake))
	_, err := c.Lookup(t.Context(), "d:1")
	require.NoError(t, err)
	fake.Advance(2 * time.Minute) // past TTL → next lookup re-hits backing
	_, err = c.Lookup(t.Context(), "d:1")
	require.NoError(t, err)
	assert.Equal(t, 2, backing.calls, "lookup after TTL expiry on the fake clock should re-call backing")
}
```

If `countingRegistry` does not already exist in the test file, add it:

```go
type countingRegistry struct {
	def   *model.ProcessDefinition
	calls int
}

func (r *countingRegistry) Lookup(_ context.Context, _ string) (*model.ProcessDefinition, error) {
	r.calls++
	return r.def, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./runtime/ -run 'TestNewCachingDefinitionRegistry' -v`
Expected: FAIL — `undefined: runtime.WithCachingDefinitionRegistryClock` / signature mismatch.

- [ ] **Step 3: Implement the option and new signature**

In `runtime/caching_definition_registry.go`, replace the constructor and add the option:

```go
// CachingDefinitionRegistryOption configures a CachingDefinitionRegistry.
type CachingDefinitionRegistryOption func(*CachingDefinitionRegistry)

// WithCachingDefinitionRegistryClock sets the time source used to evaluate TTL.
// Default: clock.System(). A nil clock is ignored. Inject a fake clock in tests.
func WithCachingDefinitionRegistryClock(clk clock.Clock) CachingDefinitionRegistryOption {
	return func(c *CachingDefinitionRegistry) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// NewCachingDefinitionRegistry wraps backing with a TTL-bounded, single-flight
// read-through cache. ttl is the maximum age of a cached definition. The time
// source used to evaluate TTL defaults to clock.System(); override it with
// WithCachingDefinitionRegistryClock (a fake clock in tests).
func NewCachingDefinitionRegistry(backing DefinitionRegistry, ttl time.Duration, opts ...CachingDefinitionRegistryOption) *CachingDefinitionRegistry {
	c := &CachingDefinitionRegistry{
		backing: backing,
		ttl:     ttl,
		clk:     clock.System(),
		entries: make(map[string]cacheEntry),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}
```

- [ ] **Step 4: Update all callers**

Run: `go build ./...`
Rewrite `NewCachingDefinitionRegistry(b, ttl, <clk>)` → drop `<clk>` if it was `clock.System()`, else `NewCachingDefinitionRegistry(b, ttl, runtime.WithCachingDefinitionRegistryClock(<clk>))`. (The `persistence` facade caller is handled in Task 9.) Repeat until clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestNewCachingDefinitionRegistry' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add runtime/caching_definition_registry.go runtime/caching_definition_registry_test.go
git commit -m "refactor(runtime): make NewCachingDefinitionRegistry clock optional

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: `NewCachingStore` → `WithCachingStoreClock`

**Files:**
- Modify: `runtime/caching_store.go` (drop positional `clk`; add option; default `clock.System()` before the `for _, o := range opts` loop)
- Test: `runtime/caching_store_test.go`

**Interfaces:**
- Produces: `func NewCachingStore(backing Store, owner Ownership, opts ...CachingStoreOption) *CachingStore`; `func WithCachingStoreClock(clk clock.Clock) CachingStoreOption`. (`CachingStoreOption` already exists.)

- [ ] **Step 1: Write the failing test**

Add to `runtime/caching_store_test.go` (package `runtime_test`). Mirror an existing CachingStore test's construction. Assert the injected fake clock drives TTL, and that omitting the option does not panic:

```go
func TestNewCachingStoreDefaultClockNoPanic(t *testing.T) {
	// No clock option → clock.System(); construction + a basic op must not panic.
	s := runtime.NewCachingStore(<existing test backing store>, <existing test ownership>)
	assert.NotNil(t, s)
}

func TestNewCachingStoreWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	s := runtime.NewCachingStore(<backing>, <ownership>, runtime.WithCacheTTL(time.Minute), runtime.WithCachingStoreClock(fake))
	assert.NotNil(t, s)
	// Reuse the file's existing TTL/eviction assertion style to confirm the fake
	// clock drives expiry (advance fake past TTL → cached snapshot is re-read).
}
```

Replace the `<...>` placeholders with the exact backing-store / ownership doubles already used by the surrounding tests in this file (read the file's existing `NewCachingStore(...)` call to copy the constructor arguments and the TTL-assertion idiom).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run 'TestNewCachingStore.*Clock' -v`
Expected: FAIL — `undefined: runtime.WithCachingStoreClock` / too many args.

- [ ] **Step 3: Implement the option and new signature**

In `runtime/caching_store.go`, add the option near the other `With*` options:

```go
// WithCachingStoreClock sets the time source used to evaluate cache TTL.
// Default: clock.System(). A nil clock is ignored. Inject a fake clock in tests.
func WithCachingStoreClock(clk clock.Clock) CachingStoreOption {
	return func(c *CachingStore) {
		if clk != nil {
			c.clk = clk
		}
	}
}
```

Change the constructor to drop the positional `clk` and default it:

```go
func NewCachingStore(backing Store, owner Ownership, opts ...CachingStoreOption) *CachingStore {
	c := &CachingStore{
		backing: backing,
		owner:   owner,
		clk:     clock.System(),
		// ...keep all other existing field initialisers exactly as they were...
	}
	for _, o := range opts {
		o(c)
	}
	return c
}
```

Read the current struct-literal initialiser in the file and preserve every existing field (ttl default, entries map, logger, etc.); only remove `clk: clk` and add `clk: clock.System()`.

- [ ] **Step 4: Update all callers**

Run: `go build ./...`
Rewrite `NewCachingStore(b, o, <clk>, <opts...>)` → drop `<clk>` if `clock.System()`, else add `runtime.WithCachingStoreClock(<clk>)` to the option list. Repeat until clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestNewCachingStore' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add runtime/caching_store.go runtime/caching_store_test.go
git commit -m "refactor(runtime): make NewCachingStore clock optional via WithCachingStoreClock

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: `NewCallNotifier` → `WithClock`

**Files:**
- Modify: `runtime/call_notifier.go` (drop positional `clk`; add option; default `clock.System()`)
- Test: `runtime/call_notifier_test.go`

**Interfaces:**
- Produces: `func NewCallNotifier(cl CallLinkStore, deliver CallDeliverFunc, reg DefinitionRegistry, opts ...CallNotifierOption) *CallNotifier`; `func WithClock(clk clock.Clock) CallNotifierOption`. (`CallNotifierOption` already exists.)

- [ ] **Step 1: Write the failing test**

Add to `runtime/call_notifier_test.go` (package `runtime_test`), copying the construction idiom (CallLinkStore / CallDeliverFunc / DefinitionRegistry doubles) from the surrounding tests:

```go
func TestNewCallNotifierDefaultClockNoPanic(t *testing.T) {
	n := runtime.NewCallNotifier(<cl>, <deliver>, <reg>)
	assert.NotNil(t, n)
}

func TestNewCallNotifierWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	n := runtime.NewCallNotifier(<cl>, <deliver>, <reg>, runtime.WithClock(fake))
	assert.NotNil(t, n)
	// If the file already drives a notify cycle and inspects the stamped trigger
	// time, extend it to assert time.Unix(1000,0) flows into the delivered trigger.
}
```

Replace `<cl>`, `<deliver>`, `<reg>` with the exact doubles the existing tests pass to `NewCallNotifier`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run 'TestNewCallNotifier.*Clock' -v`
Expected: FAIL — `undefined: runtime.WithClock` / too many args.

- [ ] **Step 3: Implement the option and new signature**

In `runtime/call_notifier.go`, add the option beside the other `WithCallNotifier*` options:

```go
// WithClock sets the time source for trigger timestamps (ADR-0003).
// Default: clock.System(). A nil clock is ignored. Inject a fake clock in tests.
func WithClock(clk clock.Clock) CallNotifierOption {
	return func(n *CallNotifier) {
		if clk != nil {
			n.clk = clk
		}
	}
}
```

Change the constructor to drop the positional `clk` and default it (preserve all other existing field initialisers and the options loop):

```go
func NewCallNotifier(cl CallLinkStore, deliver CallDeliverFunc, reg DefinitionRegistry, opts ...CallNotifierOption) *CallNotifier {
	n := &CallNotifier{
		// ...keep existing field initialisers (cl, deliver, reg, batch, poll, etc.)...
		clk: clock.System(),
	}
	for _, o := range opts {
		o(n)
	}
	return n
}
```

- [ ] **Step 4: Update all callers**

Run: `go build ./...`
Rewrite `NewCallNotifier(cl, d, reg, <clk>, <opts...>)` → drop `<clk>` if `clock.System()`, else add `runtime.WithClock(<clk>)`. (The `persistence` facade caller is handled in Task 9.) Repeat until clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestNewCallNotifier' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add runtime/call_notifier.go runtime/call_notifier_test.go
git commit -m "refactor(runtime): make NewCallNotifier clock optional via WithClock

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: `NewTaskService` → `WithClock`

**Files:**
- Modify: `runtime/taskservice.go` (option flows through the existing `taskServiceConfig`; drop positional `clk`)
- Test: `runtime/taskservice_test.go`

**Interfaces:**
- Produces: `func NewTaskService(store humantask.TaskStore, az authz.Authorizer, opts ...TaskServiceOption) *TaskService`; `func WithClock(clk clock.Clock) TaskServiceOption`. (`TaskServiceOption func(*taskServiceConfig)` already exists.)

**Note:** This constructor's options mutate a `taskServiceConfig`, not the `*TaskService` directly. Read `runtime/taskservice.go` to confirm `taskServiceConfig` has (or add) a `clk clock.Clock` field, default it to `clock.System()` when building the config, and copy `cfg.clk` into the `TaskService` struct.

- [ ] **Step 1: Write the failing test**

Add to `runtime/taskservice_test.go` (package `runtime_test`), copying the TaskStore/Authorizer doubles from existing tests:

```go
func TestNewTaskServiceDefaultClockNoPanic(t *testing.T) {
	svc := runtime.NewTaskService(<store>, <authorizer>)
	assert.NotNil(t, svc)
}

func TestNewTaskServiceWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	svc := runtime.NewTaskService(<store>, <authorizer>, runtime.WithClock(fake))
	assert.NotNil(t, svc)
	// If the file exercises a task-lifecycle op that stamps a time, assert the
	// fake time flows through.
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run 'TestNewTaskService.*Clock' -v`
Expected: FAIL — `undefined: runtime.WithClock` / too many args.

- [ ] **Step 3: Implement the option and new signature**

In `runtime/taskservice.go`:

1. Ensure `taskServiceConfig` has a `clk clock.Clock` field.
2. Add the option:

```go
// WithClock sets the time source used to stamp task-lifecycle triggers.
// Default: clock.System(). A nil clock is ignored. Inject a fake clock in tests.
func WithClock(clk clock.Clock) TaskServiceOption {
	return func(c *taskServiceConfig) {
		if clk != nil {
			c.clk = clk
		}
	}
}
```

3. Change the constructor to drop the positional `clk`, seed the config clock to `clock.System()`, apply options, then copy into the struct:

```go
func NewTaskService(store humantask.TaskStore, az authz.Authorizer, opts ...TaskServiceOption) *TaskService {
	cfg := taskServiceConfig{clk: clock.System()}
	for _, o := range opts {
		o(&cfg)
	}
	return &TaskService{
		// ...keep existing field initialisers...
		clk: cfg.clk,
	}
}
```

Read the file first to preserve the existing config defaults and the exact `TaskService` field set.

- [ ] **Step 4: Update all callers**

Run: `go build ./...`
Rewrite `NewTaskService(s, az, <clk>, <opts...>)` → drop `<clk>` if `clock.System()`, else add `runtime.WithClock(<clk>)`. Repeat until clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestNewTaskService' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add runtime/taskservice.go runtime/taskservice_test.go
git commit -m "refactor(runtime): make NewTaskService clock optional via WithClock

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: `NewRunner` → `WithClock` (highest call-site churn)

**Files:**
- Modify: `runtime/runner.go` (drop positional `clk`; add `WithClock` using the existing `Option` type; default `clock.System()`)
- Test: `runtime/runner_test.go` (add the two focused tests; migrate existing callers)

**Interfaces:**
- Produces: `func NewRunner(cat action.Catalog, store Store, opts ...Option) *Runner`; `func WithClock(clk clock.Clock) Option`. (`Option func(*Runner)` already exists.)

**Note:** `NewRunner` is called by many tests/examples and by `service`/`persistence` examples. This is the bulk of the churn — budget time for it.

- [ ] **Step 1: Write the failing tests**

Add to `runtime/runner_test.go` (package `runtime_test`). Use the package's existing minimal definition + in-memory store helpers (copy from an existing `NewRunner` test):

```go
func TestNewRunnerDefaultUsesSystemClock(t *testing.T) {
	r := runtime.NewRunner(<catalog>, <store>) // no clock option
	before := time.Now()
	st, err := r.Run(t.Context(), <oneStepDef>, "i-1", nil)
	after := time.Now()
	require.NoError(t, err)
	// The start trigger / first journal entry is stamped from the system clock.
	ts := <read the stamped time from st or the journal>
	assert.False(t, ts.Before(before) || ts.After(after))
}

func TestNewRunnerWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	r := runtime.NewRunner(<catalog>, <store>, runtime.WithClock(fake))
	_, err := r.Run(t.Context(), <oneStepDef>, "i-1", nil)
	require.NoError(t, err)
	// Assert time.Unix(1000,0) flows into a stamped trigger/journal entry.
}
```

Fill `<catalog>`, `<store>`, `<oneStepDef>`, and the timestamp read using the idioms already in `runner_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./runtime/ -run 'TestNewRunner.*Clock' -v`
Expected: FAIL — `undefined: runtime.WithClock` / not enough arguments.

- [ ] **Step 3: Implement the option and new signature**

In `runtime/runner.go`, add the option beside the other `Option`-returning `With*` functions:

```go
// WithClock sets the time source the Runner uses to stamp triggers,
// step-duration metrics, and armed-timer times. Default: clock.System().
// A nil clock is ignored. Inject a fake clock in tests for determinism (ADR-0003).
func WithClock(clk clock.Clock) Option {
	return func(r *Runner) {
		if clk != nil {
			r.clk = clk
		}
	}
}
```

Change the constructor to drop the positional `clk` and default it:

```go
func NewRunner(cat action.Catalog, store Store, opts ...Option) *Runner {
	r := &Runner{
		cat:        cat,
		clk:        clock.System(),
		store:      store,
		jitter:     NewJitterSource(),
		msgWaiters: make(map[msgKey]string),
	}
	for _, o := range opts {
		o(r)
	}
	r.obs = newRunnerObs(r.logOpt, r.tpOpt, r.mpOpt)
	return r
}
```

Update the `NewRunner` doc comment to drop the `clk` parameter line and mention `WithClock` defaulting to `clock.System()`.

- [ ] **Step 4: Update all callers (repo-wide)**

Run: `go build ./...`
For every `NewRunner(cat, <clk>, store, <opts...>)`: drop `<clk>` and reorder to `NewRunner(cat, store, <opts...>)` when it was `clock.System()`; otherwise `NewRunner(cat, store, runtime.WithClock(<clk>), <opts...>)`. Note the **positional removal** changes argument order (store moves to second). Sweep `runtime/`, `service/`, `persistence/`, and `examples/`. Repeat `go build ./...` until clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./runtime/ -run 'TestNewRunner' -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(runtime): make NewRunner clock optional via WithClock

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: `service.New` and the `persistence` facades

**Files:**
- Modify: `service/service.go` (add option type + `WithEngineClock`; drop positional `clk`)
- Modify: `persistence/persistence.go` (`NewCachingDefinitionRegistry`, `NewCallNotifier` facade wrappers drop positional `clk`, forward the runtime options)
- Test: `service/service_test.go`

**Interfaces:**
- Produces: `func New(runner *runtime.Runner, tasks *runtime.TaskService, reg runtime.DefinitionRegistry, store runtime.Store, lister runtime.InstanceLister, taskStore humantask.TaskStore, opts ...EngineOption) *Engine`; `type EngineOption func(*Engine)`; `func WithEngineClock(clk clock.Clock) EngineOption`.
- Consumes: `runtime.WithCachingDefinitionRegistryClock` (Task 4), `runtime.WithClock` (Task 6).

- [ ] **Step 1: Write the failing test**

Add to `service/service_test.go` (package `service_test`), copying the existing `service.New(...)` construction doubles:

```go
func TestNewEngineDefaultClockNoPanic(t *testing.T) {
	e := service.New(<runner>, <tasks>, <reg>, <store>, <lister>, <taskStore>)
	assert.NotNil(t, e)
}

func TestNewEngineWithClockOption(t *testing.T) {
	fake := clockwork.NewFakeClockAt(time.Unix(1000, 0))
	e := service.New(<runner>, <tasks>, <reg>, <store>, <lister>, <taskStore>, service.WithEngineClock(fake))
	assert.NotNil(t, e)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./service/ -run 'TestNewEngine.*Clock' -v`
Expected: FAIL — `undefined: service.WithEngineClock` / too many args.

- [ ] **Step 3: Implement**

In `service/service.go`:

```go
// EngineOption configures an Engine.
type EngineOption func(*Engine)

// WithEngineClock sets the time source used to stamp signal triggers.
// Default: clock.System(). A nil clock is ignored.
func WithEngineClock(clk clock.Clock) EngineOption {
	return func(e *Engine) {
		if clk != nil {
			e.clk = clk
		}
	}
}

func New(
	runner *runtime.Runner,
	tasks *runtime.TaskService,
	reg runtime.DefinitionRegistry,
	store runtime.Store,
	lister runtime.InstanceLister,
	taskStore humantask.TaskStore,
	opts ...EngineOption,
) *Engine {
	e := &Engine{
		runner:    runner,
		tasks:     tasks,
		reg:       reg,
		store:     store,
		lister:    lister,
		taskStore: taskStore,
		clk:       clock.System(),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}
```

In `persistence/persistence.go`, update the two facade wrappers to drop positional `clk` and forward options:

```go
func NewCachingDefinitionRegistry(backing runtime.DefinitionRegistry, ttl time.Duration, opts ...runtime.CachingDefinitionRegistryOption) *runtime.CachingDefinitionRegistry {
	return runtime.NewCachingDefinitionRegistry(backing, ttl, opts...)
}

func NewCallNotifier(pool *pgxpool.Pool, deliver runtime.CallDeliverFunc, reg runtime.DefinitionRegistry, opts ...runtime.CallNotifierOption) *runtime.CallNotifier {
	return runtime.NewCallNotifier(postgres.NewCallLinkStore(pool), deliver, reg, opts...)
}
```

- [ ] **Step 4: Update all callers**

Run: `go build ./...`
Drop positional `clk` from every `service.New(...)`, `persistence.NewCachingDefinitionRegistry(...)`, `persistence.NewCallNotifier(...)` call; where a fake clock was passed, add the matching `With*Clock` option (`service.WithEngineClock`, `runtime.WithCachingDefinitionRegistryClock`, `runtime.WithClock`). Sweep `service/`, `persistence/`, `examples/`. Repeat until clean.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./service/ ./persistence/ -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(service,persistence): make clock optional via options

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Harden the already-option-based clocks (nil-guard)

**Files:**
- Modify: `runtime/chainer.go` (`WithClock`), `runtime/mem_calllink.go` (`WithMemCallLinkClock`), `internal/persistence/postgres/relay.go` (`WithClock`), `internal/persistence/postgres/call_links.go` (`WithCallLinkClock`)
- Test: extend each package's existing test file with a nil-guard test.

**Interfaces:**
- These already default to `clock.System()` when the option is absent; this task only adds the nil-guard so an explicit `With*Clock(nil)` does not overwrite the default with nil.

- [ ] **Step 1: Write the failing tests**

For each of the four options, add a focused test asserting that passing `nil` leaves a working (non-nil) clock. Example for `WithClock` in `runtime/chainer_test.go`:

```go
func TestWithChainClockNilFallsBackToSystem(t *testing.T) {
	// Building a chainer with an explicit nil clock must not nil out the default.
	// Construct via the package's existing chainer test helper, passing
	// runtime.WithClock(nil), then exercise an operation that calls clk.Now()
	// and assert it does not panic.
}
```

Write the analogous nil-guard test in `internal/persistence/postgres/relay_test.go` (`WithClock(nil)`), `internal/persistence/postgres/call_links_test.go` (`WithCallLinkClock(nil)`), and `runtime/mem_calllink_test.go` (`WithMemCallLinkClock(nil)`), each constructing via the existing helper and exercising a `clk.Now()` path without panic.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./runtime/ -run 'NilFallsBackToSystem' -v`
Expected: FAIL — a `nil`-clock construction panics on `clk.Now()` (nil interface call) before the guard is added.

- [ ] **Step 3: Add the nil-guard to each setter**

`runtime/chainer.go`:
```go
func WithClock(clk clock.Clock) ChainerOption {
	return func(c *chainerConfig) {
		if clk != nil {
			c.clk = clk
		}
	}
}
```
`runtime/mem_calllink.go`:
```go
func WithMemCallLinkClock(clk clock.Clock) MemCallLinkOption {
	return func(s *memCallLinkStore) {
		if clk != nil {
			s.clk = clk
		}
	}
}
```
`internal/persistence/postgres/relay.go`:
```go
func WithClock(clk clock.Clock) RelayOption {
	return func(r *Relay) {
		if clk != nil {
			r.clk = clk
		}
	}
}
```
`internal/persistence/postgres/call_links.go`:
```go
func WithCallLinkClock(clk clock.Clock) CallLinkOption {
	return func(s *CallLinkStore) {
		if clk != nil {
			s.clk = clk
		}
	}
}
```
Match each setter's actual receiver type/field name to the file (the bodies above mirror the current ones; only the `if clk != nil` guard is added).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./runtime/ ./internal/persistence/postgres/ -run 'NilFallsBackToSystem' -v`
Expected: PASS (postgres tests require Docker/testcontainers).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(runtime,postgres): nil-guard existing With*Clock options

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Full verification sweep

**Files:** none (verification only).

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Full race test suite**

Run: `go test -race ./...`
Expected: all green (Docker daemon up for testcontainers-backed packages).

- [ ] **Step 3: Coverage on touched packages**

Run: `go test -race -coverprofile=cover.out ./runtime/... ./service/... ./persistence/... && go tool cover -func=cover.out | tail -1`
Expected: ≥ 85% line coverage.

- [ ] **Step 4: Lint + format**

Run: `golangci-lint run ./... && gofmt -l runtime service persistence internal`
Expected: lint clean; `gofmt -l` prints nothing.

- [ ] **Step 5: Confirm engine/model untouched**

Run: `git diff --name-only edf3c1c..HEAD -- engine/ model/`
Expected: no output (engine/ and model/ unchanged by this refactor).

- [ ] **Step 6: Final commit (only if Step 4 reformatted anything)**

```bash
git add -A
git commit -m "chore(runtime): gofmt after clock-option refactor

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-review notes

- **Spec coverage:** every In-scope constructor in the spec maps to a task (MemScheduler→T2, SignalBus→T3, runtime CachingDefinitionRegistry→T4, CachingStore→T5, CallNotifier→T6, TaskService→T7, Runner→T8, service.New + persistence facades→T9); already-option-based hardening→T10; ADR→T1; verification→T11. The `clockwork.Clock` seam is explicitly out of scope (spec + Global Constraints).
- **Naming consistency:** option names are `WithMemSchedulerClock`, `WithClock`, `WithCachingDefinitionRegistryClock`, `WithCachingStoreClock`, `WithClock`, `WithClock`, `WithClock`, `WithEngineClock` — all `With<Component>Clock`, all returning the component's existing/added option type, all defaulting to `clock.System()` with a nil-guard.
- **Determinism footgun:** recorded in the ADR (T1) and every migrated test passes its fake via the new option rather than relying on the default.
