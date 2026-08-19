# Adversarial audit findings — signal & message delivery correctness

**Audited:** bundle at `846c7c7` (spec + ADR-0155/0156/0157/0158 + plan).
**Auditors:** 2 of 3 Opus agents completed (correctness/races; plan executability).
Brief 1 (citation sweep) not yet run — see `2026-07-28-audit-briefs.md`.
**Verdict from both, independently: DO NOT IMPLEMENT AS WRITTEN.**
21 Critical findings between them.

**Status of this document:** findings recorded and adjudicated. The unambiguous
corrections are listed with their fix; the **four semantic decisions in §1 belong to
the project owner** and are NOT folded, because three of them reverse or qualify a
choice the owner made on the strength of analysis that turned out to be wrong.

---

## 0. Three claims I made that the source contradicts

Recorded first because each one influenced a decision.

### 0.1 "ADR-0121's dedup bounds deliver-and-start" — WRONG for the first delivery

I told the owner that keyed and singleton message-starts make deliver-AND-start
well-behaved: *"at most one start instance per (name, key), ever."* That sentence is
true and **irrelevant**. The bound is one *extra complete process execution*, not
one harmless row.

Concrete (auditor's, source-verified): order process `P` started via
`StartInstance` awaits `("approve","ORD-42")`; the same definition also declares a
message-start on `approve`. `DeliverMessage("approve","ORD-42", …)` resumes `P`
**and** creates `msgstart-<sha256(name\x00key)>`
(`runtime/processdriver_message.go:94`, `:128-131`), which runs the definition from
its start event with the same payload — executing `ChargeCard` a second time. Today
this is unreachable: `DeliverMessage` returns at `:72` before the start block.
`uniqueMessageStartDef` scans all registered definitions
(`runtime/event_start.go:142`), so the start and the catch need not even share a
definition.

ADR-0156's Consequences paragraph is source-falsifiable as written.

### 0.2 "Replay must not restamp, or downstream timers shift" (D12) — FACTUALLY WRONG

**No downstream timer is anchored to `Trigger.OccurredAt()`.** `timerJobsFor`
computes `now := driver.clk.Now()...` and derives `nextRun` from
`strig.Next(now)` (`runtime/timerops.go:131`, `:149-152`); `ScheduleTimer.Trigger`
is a definition-derived `TriggerSpec`, not an absolute instant. My ADR-0134 analogy
does not transfer — that path re-arms `schedule.At(a.NextRun)`, a *stored absolute
time* (`timerops.go:484-489`), which is a different mechanism.

What `OccurredAt` actually drives is `at` inside `Step`: `Token.EnteredAt`,
`openVisit`/`closeVisit` timestamps, and `s.EndedAt` (`engine/step_boundaries.go:150,162`;
`engine/step_triggers.go:831-832`). So replaying with a stale instant **backdates
`NodeVisit` records and can set `EndedAt` before the preceding visit closes** —
corrupting the ADR-0144–0151 audit view. And ADR-0157 itself says replay is manual
with no sweeper, which *guarantees* staleness.

**The decision should probably invert:** stamp the replayed trigger with
`clk.Now()`, keep the original `OccurredAt` on the record as provenance and surface
it in `ListUndelivered`.

### 0.3 "The CAS retry mirrors `timerFireFunc`" — a false precedent

A fired one-shot timer **is consumed**, so `timerFireFunc`'s retry is genuinely a
no-op. A signal is the opposite: a non-interrupting arm is deliberately **never**
consumed (`engine/step_boundaries.go:151-163`, `engine/step_eventsubprocess.go:217-233`,
ADR-0124). Retrying a `SignalReceived` against an already-advanced state fires the
arm **again** — a second spawned token and a second reminder/escalation per retry,
up to 5.

---

## 1. OWNER DECISIONS — RESOLVED 2026-07-29

| # | Decision | Chosen |
|---|---|---|
| **D-A** | deliver-AND-start precedence | **Keep 'both' unconditionally, document loudly.** Reaffirmed by the owner after the §0.1 duplicate-execution analysis was presented explicitly. Not a fix — a deliberate acceptance. |
| **D-B** | message fan-out default | **Keep `Selective`**, restore ADR-0125's ambiguous-correlation WARN, add a `wrkflw_delivery_recipients` histogram. |
| **D-C** | CAS retry | **Retry only when nothing has committed yet.** `deliverLoop` surfaces a committed-step count; a CAS failure on the first commit is retryable, anything mid-loop is recorded as undelivered. |
| **D-D** | service/transport reachability | **Expose fully** — `service.WithWaiterStore`/`WithUndeliveredStore`/`WithMessageDeliveryMode`, `BroadcastSignal` on the service facade, `POST {bp}/signals`, `GET/POST {bp}/admin/undelivered[/replay]`. |

### Consequences of D-A + D-B taken together

The owner accepted **both** unconditional deliver-and-start **and** `Selective`
fan-out.

⚠ **CORRECTION (controller re-analysis, 2026-07-29): auditor finding C2's
"quadratic" claim is WRONG.** Resuming a message catch clears
`AwaitMessage`/`AwaitMessageKey` (`engine/step_triggers.go:889-890`), and
`MessageWaiters()` reports only tokens with a non-empty `AwaitMessage`
(`engine/state_waiters.go:105-117`) — so a resumed instance is no longer a waiter.
In the auditor's own straight-line scenario exactly ONE instance is parked at any
moment, so N publishes cost N instances and ~N deliveries: **linear**. The
N(N−1)/2 table assumed every previously-created instance stays parked, which only
holds if the flow loops back to the catch. The real cost is still real — N
instances where one is wanted — but the "~500k round-trips from 1000 POSTs" figure
was wrong and has been withdrawn from ADR-0156.

Mitigation is therefore bounding and observability, not precedence:

1. **`WithMaxFanout(n)`** (finding A8) — a hard bound on recipients per `Publish`;
   exceeding it errors rather than proceeding. This is the blast-radius cap.
2. **`wrkflw_delivery_recipients` histogram** (D-B) — makes fan-out width visible
   before it becomes a latency incident.
3. **ADR-0125 WARN restored** (D-B) — the only diagnostic for a degenerate or
   accidentally-empty correlation key.
4. **ADR-0156 Consequences must be rewritten** to state the §0.1 truth plainly:
   the first keyed delivery to a parked instance creates a full duplicate process
   execution when a message-start on that name exists anywhere in the registry, and
   the fan-out/start interaction is quadratic in the keyless case. The current
   "ADR-0121's dedup bounds this" paragraph is source-falsifiable and must go.

### Correction applied without a decision: replay restamping (§0.2)

D12's rationale was factually wrong, so the decision resting on it does not stand
on its own. **Applied fix:** `ReplayUndelivered` stamps the rebuilt trigger with
`clk.Now()`; the original `OccurredAt` is retained on the record as provenance and
surfaced in `ListUndelivered`. This protects the ADR-0144–0151 audit trail from
backdated `NodeVisit`/`EndedAt` values. Flagged here because it reverses D12 as
written — raise it if the audit-trail reasoning is not wanted.

---

## 1b. Original framing of the decisions (retained for context)

### D-A. Deliver-AND-start precedence (reverses part of the owner's earlier choice)

§0.1 shows the first keyed delivery duplicates a business process. Compounded with
fan-out it is **quadratic**: definition with a keyless non-singleton message-start
plus a keyless intermediate catch on the same name → after N publishes, **N
instances and N(N−1)/2 deliveries** (auditor's table). At N=1000 that is ~500k
synchronous `Load`+`Step`+`Commit` round-trips from 1000 HTTP POSTs, versus 1000
deliveries and 1 instance today.

Options: **(a)** attempt the start only when the fan-out delivered to nobody —
keeps today's precedence but runs the fan-out first, collapsing the quadratic to
linear; **(b)** keep deliver-AND-start but gate it behind
`WithMessageStartOnDelivery(true)`, default off; **(c)** keep as decided and accept
the duplicate execution, documenting it loudly.

### D-B. Fan-out as the message default

The destructive direction was analysed only for the benign scope-key case. An
intermediate `ReceiveTask` with no correlation key gives **every** instance of that
definition `AwaitMessageKey == ""` (`engine/state_arms.go:236-247`), so one
`DeliverMessage("approve","")` resumes all of them — 10,000 parked instances from
one POST, synchronously, in one request. ADR-0125's WARN was the only diagnostic
for a degenerate key, and §3.5 explicitly removes it.

Options: **(a)** make `Exclusive` the default and `Selective` opt-in; **(b)** keep
`Selective` but retain the ADR-0125 WARN plus a recipients histogram; **(c)** keep
as decided.

### D-C. Whether a blind CAS retry ships at all

§0.3 plus two further holes:

- **Duplicate side effects.** `deliverLoop` commits once per iteration and performs
  side effects after each commit (`runtime/processdriver.go:711-748`, `:786-800`).
  A retry after a partial success re-fires non-interrupting arms.
- **Silent success over a stuck instance.** Iteration 1 commits, `perform` runs
  `InvokeAction`, iteration 2 CAS-fails → the queued `ActionCompleted` is **dropped**
  (`processdriver.go:746-748`) and the token is parked forever. The bus then retries,
  finds the arm gone, returns **nil**. Nothing recorded, nothing logged — the exact
  R3 failure shape, introduced by this bundle, absent from spec §5.

Options: **(a)** retry only when the CAS failed on the *first* commit (requires
`deliverLoop` to surface a committed-step count); **(b)** stamp each publish with a
delivery id and dedup in the engine (EIP Idempotent Receiver, already named in
§3.10); **(c)** drop the retry, record undelivered on the first
`ErrConcurrentUpdate`, use replay as recovery.

### D-D. Service/transport reachability (both auditors, independently)

`grep` confirms **zero** `BroadcastSignal` call sites outside `runtime/`.
`service.DeliverSignal` is instance-targeted (`service/service.go:354-374`), not a
broadcast. `service.NewProcessEngine` (`service/service.go:206-226`) has no way to
inject a `WaiterStore`, so the flagship constructor lands permanently in the
degraded configuration. `ListUndelivered`/`ReplayUndelivered` land on
`*ProcessDriver` only, while the counterpart ADR-0157 cites — `DeadLetterAdmin` — is
a service port with mounted admin endpoints.

Options: **(a)** add `service.WithWaiterStore`/`WithUndeliveredStore`/
`WithMessageDeliveryMode`, `Messaging.BroadcastSignal`, `POST {bp}/signals`, and
`GET/POST {bp}/admin/undelivered[/replay]`; **(b)** record in ADR-0156 that this is
deliberately driver-only and say why, given the ADR's motivation is a multi-replica
production deployment that necessarily goes through a transport.

**Also unanswered: authorization on replay.** `ReplayUndelivered` re-applies a
trigger and bypasses every authz seam by construction
(cf. `processdriver.go:511-516`).

---

## 2. ACCEPTED — unambiguous corrections to fold

### Design-breaking (feature is silently wrong as specified)

| # | Finding | Fix |
|---|---|---|
| A1 | **`Policy` zero value is `Broadcast`** (`iota`=0) and `msgMode` is never initialised → `DeliverMessage` queries **signal** waiters on every zero-config driver | default `driver.msgMode = delivery.Selective` in `NewProcessDriver`; give the option a two-valued type so `Broadcast` is unrepresentable; RED test with `SignalWaiters` `.Times(0)` |
| A2 | **The PK rejects duplicates, it does not collapse them.** `SignalWaiters()` explicitly documents it returns duplicates (`engine/state_waiters.go:131-134`), so an instance with a signal boundary + signal catch on one name **fails its commit** | dedup in `waitersOf` via `slices.Sort`+`slices.Compact` (fixes Mem and SQL at once); spec §3.3's "collapse on the PK" claim is wrong and must be deleted |
| A3 | **`Bus.Publish` has no payload parameter**, so `UndeliveredWakeup.Payload` cannot be populated — and replay reconstructs the trigger from exactly that field | add `payload map[string]any` to `Publish`; `mk func(at time.Time, payload map[string]any) engine.Trigger` |
| A4 | **`driver.waiterWriter` is never assigned**; `WithWaiterStore` takes the read interface and the commit hook is `if != nil` → a read-only store (cache decorator, metrics wrapper, mock) **silently disables all delivery** | require both halves: `WithWaiterStore(interface{ kernel.WaiterStore; kernel.WaiterWriter })`, or fail construction with `ErrNilDependency`. Never nil-guard a load-bearing capability into a no-op |
| A5 | **`name`/`correlation_key` are expression-derived and unbounded** (`eval.EvalString`, `engine/step_boundaries.go:63-68`) with **no length validation anywhere**, yet sit in a PK → an over-long key fails the INSERT **inside `commitFn`**, rolling back the whole state commit. MySQL-only, permanent | **hash the natural key**: `PRIMARY KEY (instance_id, kind, key_hash)`, `key_hash = sha256(kind‖0x00‖name‖0x00‖correlation_key)` as fixed 64 chars; keep `name`/`correlation_key` as payload columns; index `(kind, key_hash)`. Identical on all 3 dialects, no ceiling, no divergence. In-repo precedent: `mysqlHashKey` (`internal/persistence/store/ownership.go:283`). **This dissolves the plan's own blocking VARCHAR(191) question.** |
| A6 | **Compile errors** in plan Task 8a/8b: `attempts` used outside the `for attempt :=` scope; `NewBus` returns `(*Bus, error)` but is assigned to one variable | hoist `attempts := 0`; two-value assign with error wrap |

### Robustness / correctness

| # | Finding | Fix |
|---|---|---|
| A7 | **Context cancellation is recorded as undelivered, using the cancelled ctx to write it.** `store.Load` maps only `sql.ErrNoRows` to `ErrInstanceNotFound` (`store_core.go:146-161`), so `context.Canceled` falls to ladder step 3. A client disconnect mid-fan-out → one failed `Record` + one ERROR line per remaining recipient | check `ctx.Err()` at the top of each ladder iteration and abort recording nothing; use `context.WithoutCancel` + a bound for a genuine late `Record` |
| A8 | **Unbounded synchronous fan-out**: `SignalWaiters(ctx, name)` has no `Limit`/`Cursor`, unlike every other read port. One publish loads the whole matching set and does N sequential round-trips holding an `admit()` slot, blocking `Shutdown`'s drain | add `Limit`/`Cursor`; page the fan-out; `WithMaxFanout(n)` |
| A9 | **New unguarded recursion.** `performThrowSignal` calls `Publish` from **inside** a running `deliverLoop` (`processdriver_action.go:486-494` via `processdriver.go:793`). Under ADR-0155 that is synchronous unbounded cross-instance recursion with no depth guard — call activities have `CallLink.Depth` for exactly this | ctx-carried throw-depth guard mirroring `CallLink.Depth` |
| A10 | **`ErrAmbiguousMessageStart` flips previously-successful deliveries into errors.** Under deliver-and-start it is joined into *every* call, so two definitions sharing a message-start name make every successful `DeliverMessage` return non-2xx → broker retries → re-delivery with A3/D-C non-idempotency | when ≥1 waiter was delivered, downgrade to WARN + metric; reserve the error for the zero-waiter case. Decide the HTTP status mapping here (409 for `ErrAmbiguousMessageCorrelation`, currently unmapped → 500) |
| A11 | **`RehydrateWaiters` races the deliverLoop.** It Loads then `ReplaceWaiters` outside any tx; a concurrent step between the two makes the stale wholesale write win and the instance goes deaf. `Start` does not gate entry points | document the ordering requirement; make `ReplaceWaiters` version-aware or route it through the commit path |
| A12 | **`PruneWaiters`' anti-join is an unbounded correlated DELETE on the hot table**, in no tx, with no LIMIT, taking row locks — a MySQL deadlock source that `mapConflict` turns into `ErrConcurrentUpdate`, failing the step. Spec §5.4 also argues orphans are near-impossible, i.e. an argument for not adding it | drop it, or add a `created_at` column + cutoff + LIMIT batching |
| A13 | **ADR-0158 ordering is outcome-affecting and undecided.** Interrupting vs non-interrupting arms on one name give different results depending on `def.Nodes` scan order (two worked examples in the report). "Deterministic" ≠ "correct" | sort each family's snapshot so interrupting arms fire first (BPMN-defensible), and test both worked cases |
| A14 | **ADR-0158 emits commands for tokens a later arm cancels in the same step** — `InvokeAction`/`AwaitHuman` runs for a token that no longer exists. Pre-existing cross-family; ADR-0158 multiplies it within families | filter `signalCmds` against the final token set, or accept explicitly and test |
| A15 | **`MemWaiterStore` lookup is O(instances×waiters)** per delivery, replacing an O(1) map, on the default backend for every test and example | add `byWaiter map[Waiter]map[string]struct{}` as a secondary index |

### Documentation / citation errors

| # | Finding |
|---|---|
| A16 | **`Notifier` citation is wrong.** No dialect implements it — `postgres{}`, `mysql{}`, `sqliteDialect{}` are empty structs; the only impl is `pgxNotifier` (`notifier_pgx.go:15`) injected via `store.WithNotifier`. Conclusion stands, citation misleads. Cite `notifier_pgx.go` + `store_core.go:332-344` |
| A17 | **`runtime/doc.go` does not exist** — the hit is root `doc.go:34`. Task 9's rewire surface is ~⅓ of the real one: 16 Go files + `INTERACTIONS.md` (46 KB, §3 is entirely about the deleted machinery), root `README.md`, `runtime/README.md` (8 sites), `processtest/README.md`, `runtimetest/constructors.go` (`MustSignalBus`), `processdriver_action.go`, `processdriver.go` construction log |
| A18 | **`transaction.RunInTx` does not exist.** Package is `internal/database/transaction` (`JoinOrBegin` at `begin.go:44`); `RunInTx` is a **method on `*store.Store`** (`txrunner.go:22`) |
| A19 | **`dbtest.RunTestDatabase` returns an UNMIGRATED pool** (Postgres only; MySQL/SQLite self-migrate). Task 10's Postgres case fails on a missing relation. Follow `conformance_test.go:37-40` |
| A20 | **Task 1's replacement range is wrong** — "replace `:689-726`" but the block re-declares `snapshotIDs`/`signalCmds`/`matched` from `:684-687`. Should be `684-726` |
| A21 | **Zero-config has no real transaction.** `MemInstanceStore.RunInTx` is `return fn(ctx)` with a doc warning that rollback parity is SQL-only (`memstore.go:145-154`), so D3's headline property is vacuous in the configuration `processtest` and every example use. Add to ADR-0155 Consequences |
| A22 | Migration edit needs an upgrade note: existing dev databases must be dropped and re-migrated; there is no upgrade path |
| A23 | ADR status hygiene: ADR-0125 superseded by 0156; ADR-0154's open consequence closed by 0158. Repo convention exists (0138↔0003, 0127↔0119) |

### Process / plan

| # | Finding |
|---|---|
| A24 | **Task 11 is a TDD Forbidden Pattern** — Task 8b implements message semantics, Task 11 tests them afterwards ("implement only if a gap is found"). Move Task 11's case table into Task 8b Step 1 |
| A25 | **8 hot paths uncovered**, 5 Critical: in-tx `ReplaceWaiters`; commit-rollback atomicity at driver level; the no-`TxRunner` degraded path; `handleSignalReceived` **tiers 1 and 3** (Task 1 tests only tier 2 while changing all three); cross-family "gateway+boundary+event-sub+token all fire"; interrupting-arm-removes-snapshotted-sibling; merge-once-on-no-match; message/timer stays first-match regression guard |
| A26 | **No observability task at all.** `driverObs` has 11 instruments and an established recipe; this bundle adds fan-out, retries, self-heals, records and replays with zero. Also: `BroadcastSignal`/`DeliverMessage`/`syncWaiters` are uninstrumented **today** |
| A27 | **No testable Examples** for a whole new public package + ~15 new public symbols (CLAUDE.md Golang rule #6) |
| A28 | **`persistence.Pruner` is a public interface** with a compile-time assertion (`persistence/pruner.go:19,52`); adding methods is breaking and is absent from spec §4 |
| A29 | Corrections F1–F7 live in an appendix 1,300 lines below the tasks they correct — a subagent dispatched "implement Task 1" reads the broken version |
| A30 | All verification commands write to the same `/tmp/red.txt`; parallel tasks clobber each other and Task 1 Step 5/6 overwrites its own RED evidence. Use the scratchpad dir, per-task filenames |
| A31 | Two existing tests assert now-unreachable errors (`events_example_test.go:144`, `processdriver_signal_test.go:90`); `BroadcastSignal` changes from "error when nothing matched" to nil — a public behaviour change absent from spec §4 |
| A32 | `ctx` modifier field missing from the testCase struct in Tasks 1/2/3/4/11 despite Global Constraints requiring it; forces an undecided question — should `Mem*` stores honour `ctx.Err()`? (recommend yes; the SQL sibling does) |
| A33 | Task 2's concurrency test justification for `context.Background()` is false — it calls `wg.Wait()` before returning, so the goroutines do not outlive the test |

---

## 3. ATTACKED AND FOUND SOUND — do not re-litigate

- **Arm-identity reuse (ADR-0158) is safe.** Token/scope ids come from the injected
  `IDGenerator` (xid) or `"<instanceID>-t<seq>"` with monotonic never-reset counters
  (`engine/idgen.go:46-49`, `engine/state.go:265-270`). A consumed token's id can
  never be reissued. `resolveGatewayWin` and `fireBoundaryArm` additionally carry
  their own staleness no-ops.
- **Half-written waiter sets are not observable.** No isolation level is set
  anywhere (zero hits for `TxOptions`/`IsoLevel`), so all three backends are MVCC and
  the pool-read `SignalWaiters` cannot see the DELETE without the INSERT. The
  degraded path yields a *stale*, not *torn*, projection. §5.3 is correct.
- **`wrkflw_processed_message` is a red herring** — `Deduper.Seen` has no caller
  outside tests; it is a consumer-facing building block, not on the delivery path.
- **Call-activity and chaining are untouched** by message fan-out — `CallLink`
  correlates on `ParentCommandID`, `ChainLink` on `(PredecessorID, Outcome)`.
- **The `InstanceLister` claim is correct** — `*store.Store` has no `List`; a bare
  type-assert would silently no-op on every SQL backend. `WithInstanceLister` is right.
- **`ErrInstanceNotFound` is reliably distinguishable at the `Load` call site.**
- **`gomock.Cond` generic form** is correct for mock v0.6.0; `kernel.NormalizeLimit`
  clamps `[1,200]` default 50 as assumed.

---

## 4. Recommended order of work

1. Owner decides **D-A, D-B, D-C, D-D** (§1). Three of these change ADR-0156/0157
   materially; nothing downstream is worth writing until they are settled.
2. Fold **A1–A6** into spec §3.2/§3.3/§3.6/§3.7 and the ADRs — these make the
   feature silently wrong, not merely uncompilable. A5's hash key also dissolves the
   plan's own blocking open question.
3. Correct **§0.2 (D12), §0.3, A16** — claims the source contradicts, load-bearing in
   their ADR's argument.
4. Fold **A7–A15**, then the doc/process items **A16–A33**.
5. Add the missing tasks: observability (A26), service/transport (D-D), Examples
   (A27), `INTERACTIONS.md`/README (A17).
6. Re-run the audit on the revised bundle, plus Brief 1 (never run).

**The core insight — one durable projection with a single producer — survived the
audit intact and is well-argued. What did not survive is most of what was layered on
top of it.**

---

# ADDENDUM — citation sweep (Brief 1), run at `b61772a`, reported against `cb20497`

The third auditor completed. **The audit gate is now fully exercised** (3 of 3
briefs). This sweep verified every `file:line` citation and every stated-as-fact
behavioural claim in all six documents. A long list of citations and behavioural
claims verified CORRECT is in the raw report; only defects are recorded here.

## ⚠ THE BUNDLE MUTATED MID-AUDIT

The auditor was briefed on `b61772a` and finished after `cb20497` landed. **Freeze
the tree before the next audit round** — tag it, or pin the SHA in every document
header — and re-dispatch all three briefs against the frozen revision.

---

## C1 (Critical) — my partial fold is now the largest defect in the bundle

`cb20497` rewrote ADR-0156 and ADR-0157 only. The **spec, the plan, ADR-0155 and
ADR-0158 are byte-identical to the pre-decision state**, so they now instruct an
implementer to build the *withdrawn* design. Nine divergences:

| | revised ADR says | spec/plan still says |
|---|---|---|
| a | replay restamps with `clk.Now()` | spec §3.9/§4/§5.8/§6/§8 D12 and the `occurred_at` DDL comment all say "never `clk.Now()`"; **plan Task 3's RED test is literally named "OccurredAt round-trips verbatim — replay must not restamp"** |
| b | retry only if nothing committed; `timerFireFunc` precedent withdrawn | spec §3.9 step 1 "mirroring `timerFireFunc`"; plan Task 8a implements the blind loop and tests it |
| c | `Publish` takes `payload` | spec §3.6 + plan Shared Interfaces have the 6-param form |
| d | waiter reads carry `Limit`/`Cursor`; `WithMaxFanout` | spec §3.2 **and ADR-0155, which owns the port** define the unpaged 2-arg form; Tasks 2 and 5 build it |
| e | `WithWaiterStore` requires both halves | spec §4 takes `kernel.WaiterStore`; plan Task 8b keeps the nil-guard (reintroduces A4) |
| f | two-valued `MessageDeliveryMode`, `Broadcast` unrepresentable | spec §3.5/§4 and plan Task 8b pass `delivery.Policy` (reintroduces A1) |
| g | ADR-0125's WARN retained | spec §1.3/§3.5 say it is superseded and "not warned"; §8 D4 unchanged |
| h | ladder step 0: `ctx.Err()` aborts | spec §3.9 has 3 steps; plan Task 8a has no step 0 |
| i | service + transport fully exposed | spec §4 lists none of it; **no plan task touches `service/` or `transport/`** |

**The fold is not complete until all six documents carry one text.** Until then the
plan is not an implementation input.

---

## Decision-relevant: two decisions were argued from misrepresented prior ADRs

### C5 — "the two entry points diverge in a way nobody chose" is false

ADR-0156's Context says the message/signal asymmetry was an accident.
**ADR-0121 (Accepted) chose it deliberately, three times, and grounded it in BPMN:**

- Context: *"**Message start is addressed and correlation-controlled** … a running
  instance for the same key correlates the message to itself; otherwise a new
  instance is created (**correlate-to-running-first, then create**)."*
- Decision: *"It **first** tries to correlate to a running waiter … ; **on a miss**
  it looks for a unique message-start definition."*
- Deliberately dropped as YAGNI: *"**correlate-then-create only**."*
- And the BPMN grounding: *"**Signal start is a broadcast, 1:N fan-out** … no
  correlation is performed — it is a 'signal flare'"* versus message being
  *"addressed and correlation-controlled."*

So "structurally identical to `BroadcastSignal`" (D5) is **precisely the
equivalence ADR-0121 rejected on BPMN grounds**. D5 supersedes ADR-0121's
`DeliverMessage` decision and neither ADR says so.

### C6 — "point-to-point was an artifact of the data structure" is false

ADR-0156's Context says the `map[msgKey]string` *caused* point-to-point.
**ADR-0125 evaluated and rejected the exact fan-out proposal now being adopted:**

> *"A `map[msgKey][]string` **with fan-out was rejected**: it rewrites the
> documented 1:1 point-to-point contract and **invents non-BPMN message
> semantics**. Fan-out is the signal model and already exists … a consumer wanting
> many receivers models a signal, not a message."*

The destroyed waiter was the **cost ADR-0125 knowingly accepted** to preserve
point-to-point — not the *cause* of it. D4's stated rationale ("under the old
behaviour that second waiter was silently destroyed") therefore rests on a false
premise.

### C7 — the prior art cited for D4 unanimously supports the opposite default

Spec §1.3 cites Camunda 7, Zeebe and Flowable for "ambiguity is never silently
resolved", then chooses `Selective` as the default. **All three make
single-recipient the default and fan-out the explicit opt-in.** Also: Camunda 7's
`correlate()` throws on 0 *or* >1 match (the spec omits the 0 half, which this
bundle keeps as a clean no-op); Zeebe is overstated — it *does* correlate one
publish to matching subscriptions across definitions, i.e. it already fans out at
the `Selective` level, so it is evidence *for* D4, not for the invariant it is
cited under; Flowable resolves ambiguity in the **caller**, not the engine, so it
does not evidence the claimed invariant at all.

Using three engines as authority for "don't silently resolve ambiguity" and then
choosing a default that silently *multiplies* it is not a defensible argument.

**→ These three findings are why D4 and D5 may warrant revisiting. Both were
decided on framing that misrepresents what this repo already decided.**

---

## C2, C3, C4 (Critical) — folded prose with no implementation

- **C2** ADR-0156 says ADR-0125's WARN is "retained". Its **only** site is
  `syncMsgWaiters` (`runtime/processdriver_waiters.go:84-92`), which the plan
  deletes. It must be **re-sited** to `Bus.Publish` (fire when `len(ids) > 1` under
  `Selective`) — a different trigger point with different frequency (per delivery,
  not per park; ADR-0125 relied on it being low-frequency). Its regression test
  `runtime/message_collision_warn_test.go` is named nowhere in the plan.
- **C3** D-D produced zero plan tasks and zero §4 rows. Needs Task 12 (service
  facade) + Task 13 (four transport adapters + error→status mapping). ⚠ **ADR-0154
  recorded that `BroadcastSignal` being absent from every transport was a
  security-relevant property** — exposing it now requires that argument restated,
  not silently inherited.
- **C4** ADR-0157 says `ReplayUndelivered` "persists the arm identity", but the
  `UndeliveredWakeup` struct **twelve lines above** has no such field, the DDL has
  no column, and the signature has no override parameter. "Arm identity" is also
  not one shape — ADR-0158 gives three, plus tier-4 token ids. A `Waiter` row
  identifies a *name*, not an arm, so waiter-set equality cannot detect the
  loop-back/re-arm case the ADR cites as its motivation.

---

## Important (I1–I9) and Minor (M1–M12) — condensed

**I1** spec §3.9's code block is headed `runtime/kernel/deadletter.go`, inside the
section establishing D15 that it must never be called that. · **I2**
`ErrAmbiguousMessageCorrelation` has two home packages across the bundle. · **I3**
spec §4 claims completeness but omits ≥10 added symbols (`delivery.DeliverFunc`,
`Option`, `WithClock`, `WithUndeliveredStore`, `WithIDGenerator`, `WithMaxAttempts`,
`WithMaxFanout`, `kernel.NewMemWaiterStore`, `NewMemUndeliveredStore`,
`runtime.MessageDeliveryMode`) and, on the removed side, `signal.SignalBusOption`,
`processtest.WithSignalBus`, `processtest.Harness.Bus`. It also lists the
**internal** `store.Pruner` as public API. · **I4** "forget `WithSignalBus` and
signals silently degrade" is wrong: two of three nil-bus paths error loudly naming
the option and are regression-tested; only the broadcast-with-a-signal-start path
is silent. · **I5** `service/service.go:190` is cited as precedent for auto-detecting
the lister, but that code is **gated on `!c.durable`** and deliberately fails
validation in the durable path — the spec's ungated version produces exactly the
degraded configuration its own WARN row is about. · **I6** `AwaitMessageKey` is a
`Token` field at `engine/state.go:98-101`, not in `state_arms.go`. · **I7** "`Limit`/
`Cursor` like every other read port" — only `InstanceLister` has that shape;
`ListArmed`, `ListDefinitions` and `ListDeadLettered` do not. · **I8** two more
white-box tests break with `processdriver_waiters.go`:
`runtime/message_collision_warn_test.go` and `runtime/terminal_waiter_test.go` —
the latter is the **only** end-to-end proof of the ADR-0124 terminal-waiter guard,
which a pure-function `waitersOf` test cannot replace. · **I9** §8 omits ADR-0158
entirely; D8 and D13 have no ADR; D13 is self-falsifying (says EIP lives in ADR
prose, but §3.10's table is in no ADR); ADR-0156 has **no Alternatives-rejected
section at all**, despite carrying the two most contested decisions.

**M1** spec §3.7 says ADR-0151 applies to "both new tables" — `wrkflw_waiters` has
no timestamp; the plan correctly says the opposite. · **M2** ADR-0132 "mandates" is
overstated: it mandates a one-time pre-1.0 squash and explicitly plans numbered
files after release. · **M3** `BroadcastSignal` does have call sites outside
`runtime/` (three examples); the intended claim is "not in `service/` or
`transport/`". · **M4** the real dead-letter routes are `/admin/dead-letters` and
`/admin/dead-letters/redrive`, so a true mirror is `/admin/undelivered-wakeups`. ·
**M5–M9** five off-by-a-few citations (`processdriver.go:741`→`:739-745`;
`processdriver_action.go:487`→`:486-494`; `processdriver_message.go:79`→`:77-82`;
`lister.go:26`→`:17-21`; "unconditionally" in §1.5 overreaches). · **M10** the
plan's own self-review coverage map is wrong in three places, so it does not prove
coverage. · **M11** `engine/state_waiters.go` and `engine/README.md` also reference
`SignalBus` (25 Go files repo-wide). · **M12** the spec header still says "4 open
owner decisions" and both revised ADRs still say "revision required" — stale as of
`cb20497`.

---

## Revised order of work

1. **Complete the fold across all six documents (C1).** Nothing else matters while
   the plan instructs building the withdrawn design.
2. **Surface C5/C6/C7 to the owner** — D4 and D5 were decided on framing that
   misrepresents ADR-0121 and ADR-0125. Confirm or revise before folding them
   further.
3. **C4** — decide what `ReplayUndelivered` actually persists (changes struct,
   interface, DDL, signature).
4. **C2, C3** — re-sited WARN task; service + transport tasks; restate ADR-0154's
   security argument.
5. **I1–I9**, then **M1–M12**.
6. **Freeze the tree**, then re-run all three briefs against the frozen SHA.
