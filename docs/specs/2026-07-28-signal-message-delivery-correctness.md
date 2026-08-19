# Signal & message delivery correctness

**Status:** ⚠ AUDIT FAILED — DO NOT IMPLEMENT. All 3 auditors complete; owner decisions D-A..D-D RESOLVED and folded. Remaining: audit C2/C3/C4, the A-series design fixes, 6 missing tasks, ~30 citation fixes, then freeze + re-audit. See `docs/plans/2026-07-29-audit-findings.md`.
**Date:** 2026-07-28
**Baseline:** `main` @ `9656799`
**ADRs produced by this bundle:** ADR-0155 (durable waiter projection), ADR-0156 (unified delivery bus + message semantics), ADR-0157 (undelivered-wakeup channel), ADR-0158 (signal fires every matching arm)

---

## 1. Problem

Three defects and one structural asymmetry, all in the path between "a consumer
publishes a signal/message" and "the instances that should wake, wake".

### 1.1 Waiter caches do not survive a restart (signal *and* message)

Both waiter registries are per-process in-memory maps, written **only** as a
side-effect of the deliverLoop:

| | storage | sole writer | call site |
|---|---|---|---|
| signal | `SignalBus.waiters map[string]map[string]struct{}` (`runtime/signal/signalbus.go:50`) | `syncSignalBus` (`runtime/processdriver_waiters.go:32`) | `syncWaiters`, `runtime/processdriver.go:784` |
| message | `ProcessDriver.msgWaiters map[msgKey]string` (`runtime/processdriver.go:105`) | `syncMsgWaiters` (`runtime/processdriver_waiters.go:55`) | same |

`ProcessDriver.Start` (`runtime/processdriver.go:258`) starts the owned scheduler
and nothing else. `grep 'func (driver \*ProcessDriver) Rehydrate'` returns exactly
two methods, both timers (`runtime/timerops.go:308`, `:459`). There is no
`RehydrateWaiters`, and the limitation is undocumented.

**Effect after any restart:** both registries are empty. A parked instance is deaf
to `BroadcastSignal` and uncorrelatable by `DeliverMessage` until something else
steps it through the deliverLoop.

The message failure mode is worse than the signal one. `DeliverMessage` does not
stop at "no waiter found" — it **falls through to a message-start create**
(`runtime/processdriver_message.go:65` → `:76`):

| Situation after restart | Outcome | Class |
|---|---|---|
| no message-start matches the name (`matches == 0`, `:79`) | `return nil` — silently dropped, instance parks forever | liveness |
| message-start matches, **keyless default** (`id == ""`, `:91`) | fresh instance minted **per delivery** while the real one stays parked | **correctness** |
| message-start matches, **keyed/singleton** | deterministic id; dedups only if that id already exists. A parked instance started any other way has a different id, so the create succeeds → duplicate | **correctness** |

`uniqueMessageStartDef` scans *all* registered definitions, so definition A's
message-start firing while definition B's parked instance should have caught the
message is enough to trigger this.

### 1.2 A signal fires only the FIRST matching arm per family

`handleSignalReceived` (`engine/step_triggers.go:655`) dispatches through four
tiers. Tiers 1–3 use **singular** lookups — `armedEventBySignal` (`:690`),
`boundaryArmBySignal` (`:703`), `eventTriggeredSubprocessArmBySignal` (`:716`),
each returning the first match via `armBySignal` (`engine/state_arms.go:219`).
Only tier 4 loops (`:731`).

First-match is correct for **messages** (point-to-point) and wrong for **signals**
(broadcast). Reproduction: a parallel fork with two `UserTask`s, each carrying an
interrupting signal boundary on `"escalate"` — a broadcast interrupts one host and
leaves the other parked. BPMN fires both.

Pre-existing, but ADR-0154 promoted it from near-unreachable (those arms were
never subscribed at all) to a routine production path.

### 1.3 Ambiguous message correlation is resolved by silently destroying a waiter

`syncMsgWaiters` writes `driver.msgWaiters[k] = st.InstanceID`
(`runtime/processdriver_waiters.go:93`) into a `map[msgKey]string`. The map
**cannot represent** two instances awaiting one `(name, correlationKey)`, so the
second overwrites the first — the losing waiter is destroyed *at registration
time*, before delivery is ever attempted. ADR-0125 documents this as a WARN plus
last-writer-wins.

No comparable engine does this. Camunda 7's `correlate()` throws
`MismatchingMessageCorrelationException` on multiple matches and offers
`correlateAll()` as the explicit alternative; Camunda 8/Zeebe treats
`(name, correlationKey)` as effectively identifying, buffering messages with a
TTL; Flowable requires the caller to name the target execution. The shared
invariant across all of them is that **ambiguity is never silently resolved**.

### 1.4 Structural asymmetry: a signal bus, but no message bus

`signal.SignalBus` is a package and an exported type with an injected
`DeliverFunc`. Message handling is inlined into `ProcessDriver` as private fields
and methods (`msgMu`, `msgWaiters`, `syncMsgWaiters`, `findMessageWaiter`).

The asymmetry is historical, not principled. The bus exists because `ThrowSignal`
is a command the **engine emits** (`runtime/processdriver_action.go:487`) that must
reach *other* instances, which requires an injectable collaborator holding a
closure over `ApplyTrigger`. `DeliverMessage` is only ever called from outside, so
it never forced the same factoring.

Its costs are load-bearing for the defects above:

- `SignalBus.Subscribe` / `Unsubscribe` / `Sync` are exported but may legitimately
  be called only by the driver — a leaky abstraction with no invariant guarding it.
- `WithSignalBus` is opt-in: forget it and signal delivery silently degrades.
  Message correlation is always-on. Two failure postures for one concept.
- Two hand-maintained reconciliation paths that must be kept in step — exactly how
  ADR-0154's gap survived four ADRs.
- Consumers must construct the bus with a closure capturing a driver that does not
  exist yet (`processtest/harness.go:190`, `examples/scenarios/signal_broadcast/main.go:78-85`
  both use a declare-then-assign dance).

### 1.5 Signals are already "deliver AND start"; messages are not

`BroadcastSignal` (`runtime/processdriver_signal.go:53-64`) publishes to the bus
**and** creates one instance per signal-start hit, unconditionally, joining errors.
`DeliverMessage` `return`s after the first waiter and never reaches the start.
Messages are the asymmetric path.

---

## 2. Requirements

Set by the project owner, and stricter than "fix the restart bug":

- **R1 — restart-safe.** A published signal/message must reach every instance that
  should receive it, across a process restart.
- **R2 — multi-instance-deployment-safe.** Same guarantee when the engine library
  is embedded in N replicas sharing one database, including when the replica that
  parked an instance is down.
- **R3 — nothing silently dropped.** No delivery path may discard a match without
  either delivering it or reporting an error.
- **R4 — one authority.** Exactly one mapping from instance state to "what can wake
  this instance", extended by future constructs for free (the ADR-0123/0154
  property).

---

## 3. Design

### 3.1 Why the waiter set must be durable and read at delivery time

The replica handling `BroadcastSignal` / `DeliverMessage` must find instances **it
has never stepped**, including instances parked by a replica that is currently
down. Instances are not replica-owned — `store.Commit` is CAS-guarded on
`expected Version` (`runtime/processdriver.go:716`), so any replica may drive any
instance. Multi-replica safety therefore needs no distributed routing; it needs the
**waiter lookup to read shared durable state at delivery time**.

Rejected alternatives, with the fact that rejects each:

| Alternative | Rejected because |
|---|---|
| Boot-time rehydrate into the in-memory maps | Stale by construction for every instance parked by another replica *after* this replica booted. Satisfies R1, fails R2. |
| In-memory cache + cross-replica invalidation via `Notifier` | `internal/persistence/dialect/dialect.go:170`: MySQL and SQLite do not implement `Notifier`; none is injected for them. Postgres-only; MySQL is a supported production backend. |
| Distributed fan-out over the eventing abstraction | Workable for signal fan-out, but message correlation becomes a scatter-gather with no clean "no waiter exists" answer, and a down replica silently loses its instances' messages. Would also make `BroadcastSignal` newly depend on eventing being wired. |
| Derive at delivery time by scanning instances | O(all non-terminal instances) full-snapshot read per delivery. Pushing the predicate into SQL would reimplement `SignalWaiters()`/`MessageWaiters()` as a JSON expression across three dialects with three different JSON capabilities — an R4 violation. |

Two objections raised against the durable projection during design do not survive:

- **Hot-path cost.** The delivery path is already DB-bound: `DeliverMessage` does
  `resolveInstanceDef` + `applyTrigger` (a `Load` *and* a `Commit`);
  `BroadcastSignal` does N × `ApplyTrigger`, each a `Load` + `Commit`. One indexed
  read is noise against that.
- **Drift.** A projection of the committed snapshot, written *in the same
  transaction*, derived by the *same single authority*, is not a second source of
  truth maintained by hand. The precedent is the ADR-0134 timer direct-save
  (`jobStore.Save` inside `commitFn`, `runtime/processdriver.go:726`). It is
  explicitly **not** the human-task table, which `perform()` writes *after* the
  commit (`:793`, and the comment at `:627`) and which is therefore deliberately
  not crash-safe in the same way.

### 3.2 The `kernel.WaiterStore` port (ADR-0155)

```go
// runtime/kernel/waiterstore.go (new)

type WaiterKind int

const (
	WaiterSignal  WaiterKind = iota // broadcast-by-name, never correlated
	WaiterMessage                   // correlated, optionally keyed
)

// Waiter is one durable "this instance can be woken by this named event" row.
// CorrelationKey is always empty for WaiterSignal and may be empty for a keyless
// message await.
type Waiter struct {
	Kind           WaiterKind
	Name           string
	CorrelationKey string
}

// WaiterStore is the authoritative lookup used by signal broadcast and message
// delivery. Implementations must be safe for concurrent use.
type WaiterStore interface {
	// SignalWaiters returns the instance IDs awaiting signal name, ascending, so
	// fan-out order is deterministic. Empty name returns no waiters (ADR-0152).
	SignalWaiters(ctx context.Context, name string, f WaiterFilter) (WaiterPage, error)

	// MessageWaiters returns the instance IDs awaiting (name, correlationKey),
	// ascending. Empty name returns no waiters (ADR-0152); an empty
	// correlationKey is a legitimate keyless await and matches rows whose key is
	// also empty.
	MessageWaiters(ctx context.Context, name, correlationKey string, f WaiterFilter) (WaiterPage, error)
}

// WaiterWriter is the write-side capability. Writes join the ambient
// ctx-transaction (JoinOrBegin) so the projection commits atomically with the
// snapshot it derives from — mirroring kernel.TimerWriter (ADR-0134).
// WaiterFilter pages a lookup (Limit ≤0 → 50, >200 → 200 via kernel.NormalizeLimit).
// Paging exists so one Publish never materialises an unbounded recipient set.
type WaiterFilter struct {
	Limit  int
	Cursor string
}

// WaiterPage is one page of instance IDs, ascending for deterministic fan-out.
type WaiterPage struct {
	InstanceIDs []string
	NextCursor  string
	HasMore     bool
}

// WaiterProjection is what WithWaiterStore requires: BOTH halves. A read-only
// implementation would leave the projection unwritten and silently disable ALL
// delivery, so construction fails rather than nil-guarding it into a no-op.
type WaiterProjection interface {
	WaiterStore
	WaiterWriter
}

type WaiterWriter interface {
	// ReplaceWaiters makes ws the COMPLETE waiter set for instanceID.
	// An empty ws deletes every row for the instance.
	ReplaceWaiters(ctx context.Context, instanceID string, ws []Waiter) error
}
```

`MessageWaiters` returns a page of ids, not a single id: the fan-out default (§3.5)
needs the whole set, and the strict mode needs to *detect* multiplicity rather
than have it hidden by the signature.

### 3.3 The single projection (R4)

Replaces both `syncSignalBus` and `syncMsgWaiters`:

```go
// waitersOf is the ONLY mapping from state to waiter rows. A future construct
// extends engine.InstanceState.{Signal,Message}Waiters and is picked up here for
// free — the property ADR-0123/0154 established and this projection must not break.
//
// A terminal instance awaits nothing: a repeatable non-interrupting root
// event-sub arm can survive into a terminal snapshot (ADR-0124), and leaving its
// row would misroute a later delivery to a dead instance.
func waitersOf(st engine.InstanceState) []kernel.Waiter {
	if st.Status.IsTerminal() {
		return nil
	}
	var out []kernel.Waiter
	for _, n := range st.SignalWaiters() {
		out = append(out, kernel.Waiter{Kind: kernel.WaiterSignal, Name: n})
	}
	for _, w := range st.MessageWaiters() {
		out = append(out, kernel.Waiter{
			Kind: kernel.WaiterMessage, Name: w.Name, CorrelationKey: w.CorrelationKey,
		})
	}
	return out
}
```

Duplicate entries (two constructs in one instance awaiting the same name) collapse
on the table's primary key — no dedup is needed here, matching the existing
contract on `SignalWaiters` that a set-based sink collapses duplicates.

### 3.4 Write path — inside the existing transaction

```go
commitFn := func(txCtx context.Context) error {
	// ... store.Create / store.Commit (unchanged) ...
	// Non-nil by construction: WithWaiterStore requires kernel.WaiterProjection
	// and NewProcessDriver fails with ErrNilDependency otherwise.
	{
		if werr := driver.waiterWriter.ReplaceWaiters(txCtx, st.InstanceID, waitersOf(st)); werr != nil {
			return werr
		}
	}
	// ... jobStore.Save / deleteTimer (unchanged) ...
}
```

The post-commit `driver.syncWaiters(st)` (`runtime/processdriver.go:784`) retires.
The degraded-atomicity caveat for a store without `kernel.TxRunner`
(`runtime/processdriver.go:741`) applies identically to timers and is documented,
not fixed here.

### 3.5 Message delivery semantics (ADR-0156)

**Fan-out is across instances. Within one instance, message dispatch stays
first-match-wins.** A message is one item delivered to a participant; it lands once
inside a given process instance. `handleMessageReceived`'s four-tier
first-match cascade (`engine/step_triggers.go:844`) is unchanged.

| mode | behaviour | default |
|---|---|---|
| fan-out | deliver to **every** instance whose `(name, correlationKey)` matches | ✅ |
| strict | deliver to exactly one; return `ErrAmbiguousMessageCorrelation` when several match — never a silent pick | opt-in |

The mode is a **driver-level** setting (`runtime.WithMessageDeliveryMode`), passed
down to the bus at construction — not a per-call argument. Rationale for fan-out as
the default: with the durable table the ambiguity is representable for the first
time, and the legitimate case is real — a correlation key naming a *scope* (key
`order-42` awaited by both an order process and a shipping sub-process) rather
than a single actor. Strict mode exists because for a genuinely 1:1 business
message, two matching waiters is a modelling bug that fan-out would mask as a
silent double-execution.

ADR-0125's last-writer-wins **overwrite** is superseded; its **WARN is re-sited,
not removed**. Its only implementation lives in `syncMsgWaiters`, which this bundle
deletes, so it moves to `Bus.Publish` and fires when `len(ids) > 1` under
`Selective` — carrying the message name and the recipient count. That is a
frequency change (per ambiguous *delivery*, not per *park*) and is stated in
ADR-0156. It is the only diagnostic distinguishing "two instances share a scope
key" from "this key matches everything"; in `Exclusive` mode multiplicity is an
error rather than a warning.

Both semantics **override standing decisions** — ADR-0121's correlate-then-create
and ADR-0125's 1:1 point-to-point were deliberate, BPMN-grounded choices, not
accidents. ADR-0156 carries the full Alternatives-rejected argument; ADR-0121 is
partially superseded and ADR-0125 superseded.

**Deliver AND start.** `DeliverMessage` delivers to all matching waiters **and**
attempts the message-start create, joining errors — structurally identical to
`BroadcastSignal` (§1.5). ADR-0121's dedup bounds the consequence:

| message-start config | instance id | repeat delivery |
|---|---|---|
| keyed (`correlationKey != ""`) | `messageStartInstanceID(name, key)` | `ErrInstanceExists` → clean no-op. At most one start instance per `(name, key)`, ever |
| keyless + `MessageStartSingleton` | `messageStartInstanceID(name, "")` | same — at most one ever |
| keyless, default | `""` → fresh idgen | **fresh instance per delivery** |

Only the third row amplifies, and it is already documented as "BPMN message
fan-in: each message mints a fresh instance, no dedup" — a consumer selecting it
has already opted into that. The amplification must nevertheless be called out
explicitly in ADR-0156's Consequences, since it now also fires when a waiter
matched (previously the start was unreachable while any instance was parked).

`ErrAmbiguousMessageStart` (`matches > 1`) no longer aborts before delivery: waiter
delivery happens first, and the start ambiguity is joined into the returned error.

### 3.6 One delivery bus with a policy, not two buses (ADR-0156)

Signal and message are not two mechanisms. They are one durable-subscriber channel
with different *delivery policies* — the EIP reading in §3.10. `signal.SignalBus`
and the originally-proposed `message.Bus` collapse into a single
`runtime/delivery.Bus`:

```go
// Policy decides how many subscribers one publish reaches.
type Policy int

const (
	// Broadcast reaches every waiter on the name; selector is ignored. (signal)
	Broadcast Policy = iota
	// Selective reaches every waiter whose selector matches. (message, default)
	Selective
	// Exclusive reaches exactly one; ErrAmbiguousMessageCorrelation when
	// several match. (message, opt-in strict mode)
	Exclusive
)

// Bus resolves waiters for (kind, name, selector) from the WaiterStore under
// policy p and delivers the trigger built by mk to each, joining errors.
// mk receives the publish instant so the trigger is stamped once and every
// recipient sees the SAME OccurredAt — load-bearing for replay (§3.9).
func (b *Bus) Publish(ctx context.Context, k kernel.WaiterKind, name, selector string,
	payload map[string]any, p Policy,
	mk func(at time.Time, payload map[string]any) engine.Trigger) error
```

Both entry points become the same call plus their asymmetric start half:

| | waiter half | start half (driver-owned) |
|---|---|---|
| `BroadcastSignal` | `Publish(WaiterSignal, name, "", Broadcast, NewSignalReceived)` | `signalStartDefs` → `createAtNode(…, "", payload)` — always a fresh instance, never deduped (`runtime/processdriver_signal.go:61`) |
| `DeliverMessage` | `Publish(WaiterMessage, name, key, mode, NewMessageReceived)` | `uniqueMessageStartDef` → `createAtNode` with the ADR-0121 deterministic id |

Written once in the bus and therefore no longer duplicable: the fan-out loop, the
`errors.Join` accumulation, the `ErrInstanceNotFound` self-heal (§5.4), the
**CAS-retry loop**, and the undelivered-wakeup hand-off (§3.9).

The CAS retry is new, not a move. Today `SignalBus.Publish` calls `deliver` exactly
once and joins the error (`runtime/signal/signalbus.go:179-183`); only
`timerFireFunc` retries `ErrConcurrentUpdate` (`runtime/timerops.go:266`). With R2
in force, two replicas publishing concurrently against one instance makes CAS
conflict a routine outcome rather than a rarity, so the retry must exist on this
path — and a two-bus design would have had to write it twice.

Deleted along the way: `SignalBus.{waiters,Subscribe,Unsubscribe,Sync}`,
`ProcessDriver.{msgWaiters,msgMu}`, `syncWaiters`, `syncSignalBus`,
`syncMsgWaiters`, `findMessageWaiter`. "Who is waiting" stops being two
hand-maintained in-memory mechanisms and becomes one port.

**Package naming.** `runtime/signal` is retired: a package named for one of the two
kinds it now serves would be actively misleading. `runtime/delivery` is chosen over
`runtime/eventbus` because "event" already names a BPMN node category
(`definition/event`), and over EIP's "channel" per §3.10. Open to override — it is
the one purely cosmetic decision in this bundle.

**Wiring.** The driver owns the `WaiterStore` (`WithWaiterStore`) and constructs
the bus internally; `WithSignalBus` is **removed**. This deletes the
declare-then-assign closure dance consumers perform today
(`processtest/harness.go:190`, `examples/scenarios/signal_broadcast/main.go:78-85`,
where the `DeliverFunc` closure captures a `driver` variable that is still nil at
construction) and removes the "forget `WithSignalBus` and signals silently break"
footgun. `delivery.Bus` stays exported with a public constructor so it is
independently testable, but a consumer no longer injects one: `WithWaiterStore` is
the injection point for behaviour and `WithMessageDeliveryMode` for policy. A
consumer-supplied bus would need the driver's `applyTrigger` closure, which is
precisely the cycle that produced the dance.

### 3.7 Data model

ADR-0132 mandates exactly one migration file per dialect, enforced by
`TestMigrations_OneFilePerDialect` (`internal/persistence/store/migrations_count_test.go`).
This therefore **edits `0001_init.sql` in all three dialect directories**.
Legitimate only pre-release; v0.1.0 is untagged, so the window is open and closes
on tag.

```sql
CREATE TABLE wrkflw_waiters (
    instance_id     TEXT     NOT NULL,
    kind            SMALLINT NOT NULL,          -- 0 = signal, 1 = message
    name            TEXT     NOT NULL,
    correlation_key TEXT     NOT NULL DEFAULT '',
    PRIMARY KEY (instance_id, kind, name, correlation_key)
);
-- Serves both lookups: signal fan-out on (kind, name); message correlation on
-- (kind, name, correlation_key) as a left-prefix match.
CREATE INDEX wrkflw_waiters_lookup_idx ON wrkflw_waiters (kind, name, correlation_key);
```

And the undelivered-wakeup channel (§3.9):

```sql
CREATE TABLE wrkflw_undelivered (
    id              TEXT        PRIMARY KEY,
    instance_id     TEXT        NOT NULL,
    kind            SMALLINT    NOT NULL,
    name            TEXT        NOT NULL,
    correlation_key TEXT        NOT NULL DEFAULT '',
    payload         JSONB,                       -- JSON (MySQL), TEXT (SQLite)
    occurred_at     TIMESTAMPTZ NOT NULL,        -- when delivery originally failed; PROVENANCE, not the replay instant
    failed_at       TIMESTAMPTZ NOT NULL,
    attempts        INT         NOT NULL,
    cause           TEXT        NOT NULL,
    waiters         JSONB                        -- C4: waiter-set snapshot replay checks against
);
CREATE INDEX wrkflw_undelivered_list_idx     ON wrkflw_undelivered (failed_at DESC, id DESC);
CREATE INDEX wrkflw_undelivered_instance_idx ON wrkflw_undelivered (instance_id);
```

Per-dialect column types follow the established mapping — `snapshot`/
`trigger_payload` are `JSONB` on Postgres, `JSON` on MySQL
(`migrations/mysql/0001_init.sql:18,100`) and `TEXT` on SQLite
(`migrations/sqlite/0001_init.sql:25,107`).

⚠ **ADR-0151 applies to `wrkflw_undelivered`'s two timestamp columns.** (`wrkflw_waiters` has none.) SQLite stores times as TEXT,
and the keyset index on `(failed_at DESC, id DESC)` sorts lexicographically, so
these columns must go through the existing fixed-width 9-digit-fraction encoder
(`timeArg` / `parseTimeText`, `internal/persistence/store/time_codec.go`). Writing
`RFC3339Nano` directly reintroduces exactly the bug ADR-0151 fixed — trailing zeros
are trimmed, so `…34.1Z` sorts *after* `…34.15Z` and rows are silently skipped.

Naming note: `wrkflw_undelivered` is unrelated to `wrkflw_processed_message`, which
is inbound-subscriber dedup keyed by `(subscriber, message_id)`
(`internal/persistence/store/dedup.go`).

`instance_id` deliberately carries **no** foreign key to `wrkflw_instances`,
matching `wrkflw_timers` — the projection is deleted by `ReplaceWaiters(id, nil)`
on the terminal commit, and an FK would add a lock-ordering dependency inside the
hot commit transaction for no benefit.

### 3.8 In-memory backend and `RehydrateWaiters`

`MemWaiterStore` in `kernel`, mirroring `MemTimerStore`, stays the zero-config
default so `processtest.Harness` and every `examples/` scenario keep working.

`RehydrateWaiters(ctx)` exists **for that backend**: it pages non-terminal
instances (`StatusRunning`, `StatusCompensating`) through a
`kernel.InstanceLister`, loads each, and rebuilds the in-memory waiter set from
`waitersOf`. It restores single-replica restart safety for a consumer who wires a
durable `InstanceStore` but not a durable `WaiterStore`.

This needs a `WithInstanceLister` option: the SQL `store.Store` does **not**
implement `kernel.InstanceLister` — `store.Lister` is a separate type
(`persistence/persistence.go:181`, `internal/persistence/store/lister.go:26`) — so
a bare type-assert would silently no-op on every SQL backend while passing all
`MemInstanceStore` tests. `MemInstanceStore` does implement it
(`runtime/kernel/memstore.go:17`), so it is auto-detected after the option loop,
mirroring `service/service.go:190`.

Resulting guarantees:

| configuration | R1 restart-safe | R2 multi-replica-safe |
|---|---|---|
| durable `WaiterStore` (SQL) | ✅ by construction | ✅ |
| in-memory + `RehydrateWaiters` at boot | ✅ | ❌ — WARN at construction |
| in-memory, no rehydrate | ❌ (today) | ❌ |

A construction WARN fires when a durable `InstanceStore` is wired without a
durable `WaiterStore`, mirroring the `kernel.DefinitionLister` WARN at
`runtime/timerops.go:467`.

### 3.9 Undelivered-wakeup channel (ADR-0157)

⚠ **Name collision, resolved.** `DeadLetter` is already taken in this repo:
`monitor.DeadLetter` (`runtime/monitor/dlq.go`) plus `service.DeadLetterAdmin` and
its generated mock describe **outbound** failures — outbox rows the relay could
not publish, quarantined as `status = 'dead'` in `wrkflw_outbox`, recovered with
`Redrive`. This section is the opposite direction: an **inbound** wake-up the
engine could not apply. Same EIP pattern, opposite way, sharing nothing
operationally — different identity (outbox row id vs instance + waiter), different
recovery verb (`Redrive` re-queues a publish; `Replay` re-applies a trigger),
different owner (relay vs driver). The concept is therefore named **undelivered
wakeup** throughout so `DeadLetter` keeps exactly one meaning in the codebase.

R3 says nothing is silently dropped. Closing the *drop* path (§3.1–3.5) leaves the
*permanent failure* path open: a caller who ignores the joined error loses the
wake-up with no trace, and the instance stays parked forever — the exact failure
shape this bundle exists to eliminate. Under R2, CAS exhaustion is a realistic
terminal outcome rather than a rarity, so this is not hypothetical.

**Escalation ladder inside `Bus.Publish`, per recipient:**

0. `ctx.Err() != nil` → abort the fan-out, record nothing. `store.Load` maps only
   `sql.ErrNoRows` to `ErrInstanceNotFound`, so a cancelled caller would otherwise
   be recorded once per remaining recipient, each write using the dead context.
1. `ErrConcurrentUpdate` → retry **only when zero steps committed**
   (`runtime/timerops.go:266`).
2. `ErrInstanceNotFound` → **self-heal, not recorded.** An orphan waiter row
   means the projection is inconsistent, not that a delivery failed: there is no
   instance to wake. Delete the row, WARN, count a metric, continue.
3. Anything else — retries exhausted, store error, engine `Step` error → **record a
   undelivered wakeup**, then continue with the remaining recipients.

The joined error return is unchanged. The record is defence in depth for a
caller who ignores it, never a replacement for it.

```go
// runtime/kernel/deadletter.go (new)

// UndeliveredWakeup is one wake-up that could not be applied to its waiter.
type UndeliveredWakeup struct {
	ID             string
	InstanceID     string
	Kind           WaiterKind
	Name           string
	CorrelationKey string
	Payload        map[string]any
	// OccurredAt is the ORIGINAL publish instant, preserved verbatim.
	OccurredAt time.Time
	FailedAt   time.Time
	Attempts   int
	Cause      string
}

// UndeliveredStore is the opt-in capability enabling recording and replay.
// Absent, step 3 above degrades to ERROR log + metric only.
type UndeliveredStore interface {
	Record(ctx context.Context, u UndeliveredWakeup) error
	List(ctx context.Context, f UndeliveredFilter) (UndeliveredPage, error)
	Delete(ctx context.Context, id string) error
}
```

**`Record` is deliberately NOT in the delivery transaction** — by definition that
transaction failed. It is a separate best-effort write after the failure; if it
also fails, ERROR-log it, because there is nowhere left to escalate.

**Replay restamps; the recorded instant is provenance.**
`(*ProcessDriver).ReplayUndelivered(ctx, id)` rebuilds the trigger with
`clk.Now()`. An earlier draft decided the opposite on the reasoning that
restamping would shift downstream timers — **that is false and is withdrawn**: no
timer is anchored to `Trigger.OccurredAt()` (`timerJobsFor` derives `nextRun` from
`clk.Now()`). What `OccurredAt` drives is `at` inside `Step` — `Token.EnteredAt`,
`openVisit`/`closeVisit`, `s.EndedAt` — so replaying at a stale instant backdates
`NodeVisit` records and can set `EndedAt` before the preceding visit closes,
corrupting the ADR-0144–0151 audit view. Since replay is manual with no sweeper,
staleness is the normal case.

**Replay is at-least-once with side effects, not idempotent.** A non-interrupting
arm is never consumed (ADR-0124), so replaying twice sends two escalations, and an
instance that looped back and re-armed the same name will accept a stale replay as
fresh. **RESOLVED (C4):** the record carries a `Waiters []Waiter` snapshot taken when
delivery failed, and `ReplayUndelivered(ctx, id, opts ...ReplayOption)` refuses with
`ErrWaiterSetChanged` unless the instance still awaits a matching entry or the
caller passes `WithForce()`. A waiter set rather than an arm identity: an arm
identity is not one shape and is meaningless to an operator. It catches "instance
completed" and "advanced past the wait"; it does not catch a loop-back that
re-armed the same name, which is why replay stays at-least-once.

`ID` comes from the driver's existing `idgen.Generator` (`runtime/processdriver.go:37`,
ADR-0149) — no new id concept.

Hygiene: `PruneUndelivered(cutoff)`, sibling to the existing `PruneTimers`
(`internal/persistence/store/pruner.go:192`).

### 3.10 EIP as prior art, not as vocabulary

The design is a subset of Hohpe & Woolf's Enterprise Integration Patterns, and the
ADRs cite them as prior art because it makes the decisions defensible against a
known catalogue:

| this design | EIP pattern |
|---|---|
| signal delivery (`Broadcast`) | Publish-Subscribe Channel |
| message delivery (`Selective`) | Publish-Subscribe + Selective Consumer |
| message strict mode (`Exclusive`) | Point-to-Point Channel |
| `correlation_key` | Correlation Identifier |
| `wrkflw_waiters` row | Durable Subscriber |
| `wrkflw_undelivered` (§3.9) | Dead Letter Channel (inbound; see ADR-0157 on the name) |
| `ErrInstanceExists`, `wrkflw_processed_message` | Idempotent Receiver |
| outbox relay | Guaranteed Delivery |
| journal | Message Store |

**The vocabulary stays BPMN, deliberately.** No identifier is renamed to
`Channel` / `Selector` / `Subscription` / `Endpoint`. This repo names one concept
identically across Go, JSON and SQL, and `signal name` / `message name` /
`correlation key` is the vocabulary a consumer *authors definitions in*. Importing
EIP jargon would insert a translation layer between the YAML a user writes and the
code that serves it. EIP belongs in ADR Context sections; it must not reach
`state_waiters.go`.

No EIP framework or routing DSL is adopted — see §7.

### 3.11 Signal fires every matching arm per family (ADR-0158)

Engine-pure, independent of everything above. `handleSignalReceived` tiers 1–3
change from singular lookup to **snapshot-then-fire-each**:

- Snapshot matching arm identities **before** any dispatch, per family:
  `armedEvent` → `(GatewayToken, CatchNode)`; `boundaryArm` → `(HostToken, BoundaryNode)`;
  `eventTriggeredSubprocessArm` → `(EnclosingScopeID, EventSubprocessNode)`.
- Before firing each, re-resolve it by identity and skip if gone — an interrupting
  boundary or event-sub-process fire cancels sibling arms and scope tokens, so a
  snapshotted arm may legitimately no longer exist. This mirrors tier 4's existing
  `tok == nil || tok.AwaitSignal != t.Name` guard (`engine/step_triggers.go:735`).

The snapshot is **mandatory, not an optimisation**: a non-interrupting boundary arm
deliberately stays armed after firing (`engine/step_boundaries.go:152-158`,
ADR-0124), so a re-scanning loop would never terminate.

`mergeVars` keeps its merge-once-before-first-match semantics (`:691-694`), so a
no-match delivery still mutates nothing.

Timer and message dispatch keep first-match-wins: a `TimerID` is unique per arm,
and message delivery is point-to-point within an instance (§3.5).

---

## 4. Public API changes (all BREAKING, pre-1.0)

| change | kind |
|---|---|
| `runtime.WithWaiterStore(kernel.WaiterProjection)` | added — **both halves required** |
| `kernel.WaiterFilter`, `WaiterPage`, `WaiterProjection` | added |
| `runtime.MessageDeliveryMode` (`ModeSelective` / `ModeExclusive`) | added — distinct from `delivery.Policy` |
| `delivery.DeliverFunc`, `Option`, `WithClock`, `WithUndeliveredStore`, `WithIDGenerator`, `WithMaxAttempts`, `WithMaxFanout`, `WithLogger`, `WithWaiterWriter` | added |
| `kernel.NewMemWaiterStore`, `kernel.NewMemUndeliveredStore` | added |
| `service.WithWaiterStore` / `WithUndeliveredStore` / `WithMessageDeliveryMode` | added (D-D) |
| `service.Service.BroadcastSignal`, `ListUndelivered`, `ReplayUndelivered`, `DeleteUndelivered` | added (D-D) |
| *(no HTTP route added)* | **deliberate** — ADR-0154's security property preserved; broadcast and undelivered recovery are service-facade/Go-API only, mounted by the consumer under their own policy |
| `kernel.UndeliveredWakeup.Waiters []Waiter` | added (C4) — the waiter-set snapshot replay checks against |
| `runtime.ReplayOption`, `runtime.WithForce()`, `runtime.ErrWaiterSetChanged` | added (C4) |
| `signal.SignalBusOption` | **removed** |
| `processtest.WithSignalBus`, `processtest.Harness.Bus` | **removed** |
| `persistence.Pruner` gains `PruneWaiters`/`PruneUndelivered` | **BREAKING** — it is a public interface with a compile-time assertion |
| `BroadcastSignal` no longer errors when nothing matched (returns nil) | **behaviour** |
| replay stamps `clk.Now()`, not the recorded instant | **behaviour** (D12 reversed) |
| `runtime.WithInstanceLister(kernel.InstanceLister)` | added |
| `runtime.WithMessageDeliveryMode(...)` (fan-out \| strict) | added |
| `(*ProcessDriver).RehydrateWaiters(ctx) error` | added |
| `kernel.Waiter`, `WaiterKind`, `WaiterStore`, `WaiterWriter`, `MemWaiterStore` | added |
| `kernel.UndeliveredWakeup`, `UndeliveredStore`, `UndeliveredFilter`, `UndeliveredPage`, `MemUndeliveredStore` | added |
| `runtime.WithUndeliveredStore(kernel.UndeliveredStore)` | added |
| `(*ProcessDriver).ListUndelivered` / `ReplayUndelivered` / `DeleteUndelivered` | added |
| `runtime/delivery` package: `Bus`, `NewBus`, `Policy` (`Broadcast`/`Selective`/`Exclusive`) | added |
| `runtime.ErrAmbiguousMessageCorrelation` | added |
| `runtime.WithSignalBus` | **removed** |
| `runtime/signal` package (`SignalBus`, `NewSignalBus`, `DeliverFunc`, `WithClock`) | **removed** — superseded by `runtime/delivery` |
| `persistence.NewWaiterStore` / `NewMySQLWaiterStore` / `NewSQLiteWaiterStore` | added |
| `persistence.NewUndeliveredStore` / `NewMySQLUndeliveredStore` / `NewSQLiteUndeliveredStore` | added |
| `store.Pruner.PruneWaiters` / `PruneUndelivered` | added |
| `DeliverMessage` now also fires a matching message-start | **behaviour** |
| ambiguous correlation: last-writer-wins → fan-out (or error in strict mode) | **behaviour** |
| broadcast signal fires every matching arm per family, not the first | **behaviour** |

Consumers affected: anyone calling `WithSignalBus` (all `examples/scenarios/*`
using signals, `processtest.Harness`) and anyone relying on a message-start being
suppressed while an instance is parked.

---

## 5. Failure modes and edge cases to cover

1. **Terminal instance.** `waitersOf` returns nil so `ReplaceWaiters` deletes every
   row — including a repeatable non-interrupting root event-sub arm surviving into
   a terminal snapshot (ADR-0124).
2. **Empty identity keys.** Empty signal/message name matches no waiter (ADR-0152).
   Empty `correlationKey` is a legitimate keyless await, not a wildcard.
3. **Store without `TxRunner`.** Each write self-commits; a crash between the
   snapshot and the waiter write leaves a stale projection. Documented, matching
   timers.
4. **Orphan waiter row.** `Pruner` has no `PruneInstances` — it prunes outbox,
   call links, chain links, processed messages and timers only
   (`internal/persistence/store/pruner.go`), so instances are never deleted and a
   row is normally cleared by `ReplaceWaiters(id, nil)` on the terminal commit. An
   orphan can therefore arise only from the degraded no-`TxRunner` path or a bug.
   Delivery must still tolerate `ErrInstanceNotFound` as a **skip, not a failure**.
   A `PruneWaiters(cutoff)` sibling to the existing `PruneTimers` is the symmetric
   hygiene addition and is proposed, not required, by this spec.
5. **Fan-out partial failure.** One instance failing `ApplyTrigger` must not abort
   the rest; errors join, mirroring `SignalBus.Publish`'s existing contract
   (`runtime/signal/signalbus.go:177-184`), **and** each permanent failure is
   recorded as undelivered (§3.9 step 3).
6. **CAS conflict during fan-out.** Two replicas broadcasting concurrently both
   `ApplyTrigger` the same instance; the loser sees `ErrConcurrentUpdate`. Bounded
   retry per §3.9 step 1; exhaustion is recorded as undelivered, not silently dropped.
7. **`Record` itself fails.** ERROR log only — there is nowhere left to escalate.
   Must never fail the surrounding publish or abort the remaining recipients.
8. **Replay of an already-advanced instance.** A clean engine no-op: the waiter is
   gone, so the trigger matches nothing. Replay is therefore idempotent without
   extra bookkeeping — but it must reuse the stored `OccurredAt` (§3.9), never
   `clk.Now()`.
9. **No `UndeliveredStore` wired.** Degrades to ERROR log + metric; delivery
   behaviour is otherwise unchanged. Zero-config and `processtest` keep working.
10. **Race: instance parks concurrently with delivery.** Inherent and pre-existing;
    mitigated for keyed starts by the deterministic-id + `ErrInstanceExists` dedup
    (`runtime/processdriver_message.go:100`).
11. **Duplicate waiter rows within one instance.** Collapsed by the primary key.
12. **`MemWaiterStore` + durable `InstanceStore`.** The degraded configuration —
    construction WARN plus `RehydrateWaiters`.

---

## 6. Testing strategy

Hot paths first (Golang rule #8), each with its failure branches, before anything
else:

- **Projection.** `waitersOf` over all four signal sources × all four message
  sources × terminal/non-terminal, including the ADR-0124 surviving-arm case.
- **Commit-tx atomicity.** A commit that fails after `store.Commit` but inside
  `commitFn` must roll back the waiter rows (real Postgres/MySQL/SQLite, via
  `dbtest`).
- **Restart (R1).** Park an instance on a signal *and* a message, drop the
  in-memory driver, construct a fresh one over the same store, broadcast/deliver,
  assert both wake. Run for both backends — durable (no rehydrate call) and
  in-memory (`RehydrateWaiters` first).
- **Multi-replica (R2).** Two `ProcessDriver`s over one store: replica A parks the
  instance, replica B broadcasts, assert the instance wakes. This is the test that
  fails today and that boot-rehydrate alone would not fix.
- **Fan-out.** N instances on one `(name, key)`; all wake. Strict mode returns
  `ErrAmbiguousMessageCorrelation` and delivers to none.
- **Deliver-and-start.** Waiter present *and* message-start present: both happen;
  repeat delivery dedups for keyed/singleton and amplifies for keyless-default.
- **Arm fan-out (ADR-0158).** Parallel fork, two hosts, interrupting signal
  boundary on one name → both fire. Non-interrupting arm fires exactly once per
  delivery and stays armed. Interrupting event-sub-process removing a snapshotted
  sibling arm → skipped, not an error.
- **Dead letter.** CAS exhaustion records exactly one row with the ORIGINAL
  `OccurredAt`; `ErrInstanceNotFound` self-heals (row deleted, nothing recorded);
  `Record` failing does not abort the remaining recipients; replay reuses the
  stored instant and is a no-op against an already-advanced instance; no
  `UndeliveredStore` wired degrades to log+metric without changing delivery.
- **ADR-0151 encoding.** `failed_at` / `occurred_at` round-trip through the
  fixed-width encoder on SQLite, and a keyset page over `(failed_at DESC, id DESC)`
  returns rows whose fractional seconds have trailing zeros in the correct order —
  the specific regression ADR-0151 fixed.
- **Store conformance.** `WaiterStore`/`WaiterWriter` and `UndeliveredStore`
  conformance suites across all three dialects, matching the package's existing
  `*_conformance_test.go` pattern.
- **Purity.** `engine/purity_test.go` must still pass — ADR-0158 touches the pure
  core and must not introduce a runtime or clock dependency.

Coverage floor 85% per touched package; hot paths and their error branches are the
real target.

---

## 7. Out of scope

- **Distributed pub/sub for signal throw.** `ThrowSignal` emitted by an instance on
  replica A fans out through replica A's bus, which now reads the shared table — so
  it reaches every instance. No message broker is introduced.
- **EIP frameworks / routing DSLs** (Camel-style routers, or routing wake-ups
  through watermill). No new dependency: the eventing abstraction already owns
  *outbound* broker concerns, while this is in-process instance wake-up against a
  shared database — the durable waiter table *is* the channel. EIP enters as prior
  art only (§3.10).
- **Message buffering / TTL** — EIP's **Message Expiration**, and Zeebe's model of
  delivering to subscriptions that open *after* publish. Deliberately not adopted:
  a message with no waiter and no start stays a clean no-op. Note the interaction
  with §3.5's deliver-and-start decision — for a keyless non-singleton start, an
  early message mints an instance rather than being held for the waiter that is
  about to appear. Naming the omitted pattern is deliberate; if buffering is ever
  wanted it belongs in its own ADR, not bolted onto this one.
- **`armedTimerRecurring`'s unfiltered `SELECT`** on every timer fire — ADR-0153's
  declined list, needs its own ADR.
- **Per-step waiter reconciliation cost** — largely dissolved by this change (the
  process-global scan under a global lock is replaced by a PK-scoped write), but no
  reverse-index work is undertaken.
- **Retiring `wrkflw_instances`' JSONB snapshot** in favour of normalised state.

---

## 8. Decision log

| # | Decision | Rationale |
|---|---|---|
| D1 | Durable waiter projection, not boot rehydrate | Only shape satisfying R2; boot rehydrate is stale for post-boot remote instances |
| D2 | One `wrkflw_waiters` table with a `kind` discriminator, not two tables | One projection, one writer, one index; a future waiter kind extends the enum |
| D3 | Written in the commit tx | Crash-safety parity with ADR-0134 timers; makes drift structurally impossible |
| D4 | Fan-out default, strict opt-in | Owner decision, re-confirmed as an explicit **override of ADR-0125**, which evaluated and rejected fan-out as "non-BPMN message semantics". Justified by the scope-key case and by ADR-0155 making multiplicity representable for the first time. The external prior art (Camunda 7 / Zeebe / Flowable) supports the OPPOSITE default and is not authority for this |
| D5 | Deliver AND start | Owner decision, re-confirmed as an explicit **override of ADR-0121**'s deliberate correlate-then-create. The earlier "ADR-0121 dedup bounds it" rationale is **withdrawn**: the first keyed delivery creates a full duplicate process execution, and with D4 the keyless case is quadratic. Accepted, bounded by `WithMaxFanout` |
| D6 | **ONE** `delivery.Bus` with a policy, not two buses | Signal and message are one channel with different policies; a second bus would duplicate the fan-out loop, error join, self-heal and CAS retry. Supersedes the earlier "symmetric `message.Bus`" proposal |
| D7 | `WithSignalBus` removed in favour of `WithWaiterStore` | Kills the declare-then-assign closure dance and the silent-degradation footgun |
| D8 | "Lowest instance ID wins" **withdrawn** | Still a silent drop; determinism is not the property R3 asks for |
| D9 | Message stays first-match-wins *within* an instance | A message is one item delivered to a participant; fan-out is across instances |
| D10 | Durable undelivered-wakeup channel in this bundle | Owner decision; closing the drop path while leaving the permanent-failure path invisible would violate R3 in spirit, and R2 makes CAS exhaustion routine |
| D11 | `ErrInstanceNotFound` self-heals, is not recorded | An orphan row means an inconsistent projection, not a failed delivery — there is no instance to wake |
| D12 | **REVERSED** — replay stamps `clk.Now()`; the stored `OccurredAt` is provenance | The original rationale was false: no timer is anchored to `Trigger.OccurredAt()` (`timerJobsFor` uses `clk.Now()`). It drives `Token.EnteredAt`/`NodeVisit`/`EndedAt`, so replaying at a stale instant backdates the ADR-0144–0151 audit trail |
| D13 | EIP as prior art in ADR prose; BPMN vocabulary in code | The catalogue makes decisions defensible; renaming to Channel/Selector would insert a translation layer between authored YAML and code |
| D14 | New package `runtime/delivery`, retiring `runtime/signal` | A package named for one of the two kinds it serves is misleading; "event" is taken by `definition/event`. Purely cosmetic — flagged as overridable |
| D15 | Named `UndeliveredWakeup`, **not** `DeadLetter` | `monitor.DeadLetter` + `service.DeadLetterAdmin` already mean outbound outbox publish failures with `Redrive`. Reusing the name for an inbound wake-up failure with `Replay` would give one term two unrelated meanings |
