# Triage — backlog items 70–103 (`AUDIT.md` tiers 1–3 verification, 2026-08-20)

Slice: items **70–103** of `docs/plans/HANDOVER.md` §"🆕 from the 2026-08-20 verification of
`AUDIT.md` tiers 1–3" (Engine core, Persistence, Runtime, Public API, Security).

Method: every attribution below was re-derived from source in the working tree (`main`, clean).
Behavioural measurements quoted in the backlog statements were **not** re-executed (they were
produced by the executed probes on `docs/architecture-audit@9769a8e5`); where I re-derived the
mechanism from source I say `VERIFIED`, where I could only confirm the code shape but not the
runtime number I say so explicitly. No Docker was started; no repo file outside this one was
modified.

Tier legend: `S` small (≲100 lines, no new public API, no architectural call) · `D` design
(spec/ADR needed) · `A` adjudication (not a defect / closed / duplicate / trap).

---

## Engine core (audit §4.1)

### 70 — `DeferredCompensationThrows` has no liveness maintenance

- **Package(s):** `engine`
- **Symbols/files:**
  - `engine/state.go:394-403` — `InstanceState.DeferredCompensationThrows []string`
  - `engine/step_nodes.go:1102-1106` — `deferCompensationThrow(s, tok)` (the only enqueue site;
    sets `tok.State = TokenWaiting` and appends `tok.ID`)
  - `engine/step_compensation.go:934-943` — `popOneDeferredThrow(s)` (the only dequeue site)
  - `engine/step_state.go:455` — cloneState copies the queue
- **Tier:** `D`
- **Fix sketch:** give the queue a liveness contract — `popOneDeferredThrow` must *skip* entries
  whose `tokenByID` is nil (loop, not `[0]`), and token-consuming paths
  (`consumeToken`/`consumeTokenAs`, ESP interruption) must prune the ID from
  `DeferredCompensationThrows`.
- **Falsifiable test:** `engine` black-box — build a state with
  `DeferredCompensationThrows = [deadID, liveID]` where `deadID` names no token, finish a walk with
  `popDeferred`, assert the live throw token is `TokenActive`. **Fails today** because
  `popOneDeferredThrow` unconditionally pops index `0`, `s.tokenByID(deferredTok)` returns `nil`,
  nothing is re-activated, and the live entry is now at index 0 with no further finish to pop it.
- **Dependencies:** touches the same finish path as **71** and **72**
  (`stepCompensationFinish`/`applyFinish`); serialize behind whichever lands first. Adjacent to
  ADR-0071 (one-walk-at-a-time serialization) — the ADR is the contract to amend.
- **Status:** `VERIFIED` (source: pop is unconditional index-0 with a nil-tolerant
  `if tok := s.tokenByID(deferredTok); tok != nil`, so a dead head silently discards the pop).
  The measured "2 of 2 queue entries dead / sibling `TokenJoining`" scenario is inherited from the
  probe run, not re-executed here. ⚠ Respect the backlog's bound: proven to strand *the next*
  throw, **not** "every throw behind it".

### 71 — Partial rollback into a sub-process-internal node resumes in the wrong scope

- **Package(s):** `engine`
- **Symbols/files:**
  - `engine/step_compensation.go:1061-1078` — `case walkPartial:` builds
    `plan = finishPlan{resume: true, resumeAt: toNode}` — **no `resumeScope`** (contrast
    `walkThrowTargeted`/`walkThrowScopeWide` at :1029-1036, which set `resumeScope: resumeScope`)
  - `engine/step_compensation.go:1269` —
    `resumeDropped := plan.resumeScope != "" && s.scopeByID(plan.resumeScope) == nil` — the safety
    net is gated on the field `walkPartial` never sets, so it structurally cannot fire
  - `engine/step.go:321` — `slog.WarnContext(ctx, "token routed to a missing node", …)`
  - Entry point: `CompensateRequested` trigger (`engine/step_triggers.go:123` →
    `stepCompensationAdvance`/`stepCompensationFinish`), reachable from the admin API.
- **Tier:** `D`
- **Fix sketch:** resolve `toNode`'s owning scope and set `finishPlan.resumeScope` (mirroring the
  throw branches), **or** reject a `toNode` outside the reachable scope with a typed error from
  `stepCompensationFinish` — the choice is a contract decision on the public admin verb, hence ADR.
- **Falsifiable test:** `engine` black-box — sub-process definition; drive a `CompensateRequested`
  with `ToNode` = a node *inside* the sub-process; assert `res.State.Status` and that the resulting
  token sits in the sub-process scope (or that a typed error is returned). **Fails today**: the
  token is placed at root scope, `drive` logs `token routed to a missing node`, the token parks with
  every await key empty, `status=running`, zero commands, and `Step` returns `err=nil`.
- **Dependencies:** same `finishPlan` switch as **70**/**72**. If **71** is fixed by *rejecting*,
  it becomes an input to the general escape-hatch item **69** (out of slice).
- **Status:** `VERIFIED` (both halves read directly from source: missing `resumeScope` on the
  `walkPartial` plan, and the `plan.resumeScope != ""` guard on `resumeDropped`).

### 72 — Reverse-compensation order ties on `CompletedAt` and then flips on node NAME

- **Package(s):** `engine`
- **Symbols/files:**
  - `engine/state_compensation.go:532-545` — `(*InstanceState).consolidateArchiveIntoRoot`;
    line **543**: `return cmp.Or(a.CompletedAt.Compare(b.CompletedAt), cmp.Compare(a.NodeID, b.NodeID))`
  - The two record writers that can stamp the **same** `CompletedAt` from **one** trigger:
    `engine/step_triggers.go:158` `s.recordCompensation(tok.ScopeID, node.ID(), compAction, t.OccurredAt(), …)`
    and `engine/step_nodes.go:582`
    `c.s.recordCompensation(parentScopeID, enclosingNodeID, sp.CompensateAction, c.at, …)` — and
    `c.at` is the same `t.OccurredAt()` threaded through `drive`.
  - `engine/state_compensation.go:332` — `CompletedAt: completedAt` on the record.
- **Tier:** `D`
- **Fix sketch:** add a monotonic per-instance `Seq` to `CompensationRecord` (minted at
  `recordCompensation`) and sort by `(CompletedAt, Seq)` — dropping `NodeID` as the tiebreak.
  Persisted-struct change ⇒ ADR + migration note for stored snapshots.
- **Falsifiable test:** `engine` black-box — sub-process whose own `CompensateAction` is recorded by
  the same trigger that records the inner activity's; run the pair once with inner id `z-inner` /
  root id `a-root` and once with `a-inner` / `z-root`; assert the compensation dispatch order is the
  **same** in both. **Fails today** because `CompletedAt` is byte-identical (one trigger, one
  `OccurredAt`), so the comparator falls through to `cmp.Compare(a.NodeID, b.NodeID)` and the two
  runs produce opposite orders.
- **Dependencies:** shares the `finishPlan`/consolidation path with **70**/**71**. Interacts with
  ADR-0173 (record ownership) — the `Seq` must survive `dropArchiveRecordAt` and the teardown
  window.
- **Status:** `VERIFIED` for the mechanism and for the backlog's **widening** of the audit: the tie
  is *by construction* (one trigger → two records → identical `CompletedAt`), not a
  coarse/fake-clock artefact, and only `consolidateArchiveIntoRoot` sorts, so flat root-only
  processes are unaffected. ⚠ I did **not** re-execute which of the two orders is the *correct*
  one; the backlog's `[undo-inner undo-root]` labelling is inherited — `ASSUMPTION (unverified)` on
  that detail only.

### 73 — Every `Step` is O(entire state)  ⛓ **PAIRED WITH 114**

- **Package(s):** `engine`
- **Symbols/files:** `engine/step_state.go:361` `func cloneState(st InstanceState) InstanceState`;
  **the exact line is `engine/step_state.go:374`**:
  ```go
  s.History = append([]NodeVisit(nil), st.History...)
  ```
  Sibling O(n) copies in the same function: `s.Tokens` (:370), `s.Timers` (:392),
  `s.ArmedEvents` (:395), `s.Boundaries` (:398), `s.EventTriggeredSubprocesses` (:402),
  `s.Tasks` (:387-391, `HumanTask.Clone` per task), `s.Scopes` (:424+).
- **Tier:** `D`
- **Fix sketch:** do **not** simply delete line 374. Change the copy strategy — e.g. copy-on-write /
  append-only `History` with an explicit "never mutate an existing `NodeVisit` in place" invariant,
  or a full-capacity `slices.Clip`-style copy so a shared backing array cannot be appended into by
  two clones. Public `Clone` contract ("independently allocated") is at stake ⇒ ADR.
- **Falsifiable test:** two tests, both required.
  (a) a `testing.B` over `Step` with `History` at 0 / 10k / 100k asserting the per-op cost is not
  superlinear — fails today (556 ns → 1,045,401 ns, 1,880×, inherited measurement);
  (b) **item 114's** aliasing test (see below) as the *guard* on any change to line 374.
- **Dependencies:** ⛓ **HARD PAIR WITH 114.** 114 proves line 374 is load-bearing *and* completely
  untested, and that the obvious regression test (`len == cap` fixture) **cannot reproduce** the
  corruption. **Anyone who fixes 73 without landing 114's `cap > len` test first ships state
  corruption under a green suite.** Sequence: 114 (RED test) → then 73.
  Also interacts with the `Clone` deep-copy tests in `engine/scope_test.go:216-320` and
  `engine/state_test.go:77-192`.
- **Status:** `VERIFIED` — line 374 confirmed verbatim at `engine/step_state.go:374`; the
  `History` copy is unconditional and per-`Step`. Benchmark numbers inherited from the probe run
  (not re-executed; no ablation performed here because this triage is read-only).
  Cross-check for 114: the only test named as the deep-copy guard,
  `engine/step_test.go:94 TestStepDoesNotMutateInput` (cited by the `cloneState` comment at
  `engine/step_state.go:383`), builds its fixture with **no `History` field at all** — it asserts
  `in.Tokens`, `in.Variables`, `in.Scopes` only. **Confirms 114's "completely untested".**

### 74 — A consumer `IDGenerator` emitting an `evtgw:`-prefixed id cross-wires tokens

- **Package(s):** `engine`
- **Symbols/files:**
  - `engine/step_nodes.go:975` — `sentinel := "evtgw:" + tok.ID`; `tok.AwaitCommand = sentinel`
  - `engine/step_nodes.go:988` — `ae := armedEvent{GatewayToken: tok.ID, …}` (so
    `ae.GatewayToken == <gateway token ID>`)
  - `engine/step_gateways.go:216` — `tok := s.tokenAwaiting("evtgw:" + ae.GatewayToken)` — **the
    exact-equality lookup**, `engine/step_state.go:79-89`
    (`if s.Tokens[i].AwaitCommand == cmdID { return &s.Tokens[i] }`, first match wins)
  - Benign prefix site (the one the audit blamed): `engine/step_cancel.go:26`
    `strings.HasPrefix(tok.AwaitCommand, "evtgw:")`
  - Id mint: `engine/idgen.go:46 (*InstanceState).nextID` — delegates verbatim to the consumer
    `IDGenerator`, no validation of the returned string.
- **Tier:** `S`
- **Fix sketch:** in `resolveGatewayWin`, stop trusting the string: resolve by identity —
  `tok := s.tokenByID(ae.GatewayToken)` and require `tok.AwaitCommand == "evtgw:"+ae.GatewayToken`
  (or keep `tokenAwaiting` and add `tok.ID == ae.GatewayToken`). ~4 lines, no API change.
  (A larger `D` variant — a dedicated `Token.AwaitGateway` field instead of overloading
  `AwaitCommand` — is the structural cure but changes persisted state.)
- **Falsifiable test:** `engine` black-box — event-based gateway plus a user task whose action
  `CommandID` is minted by a stub `IDGenerator` returning `"evtgw:"+<gateway token id>`; fire the
  gateway's timer; assert the *gateway* token moved and the task token did not. **Fails today**:
  `tokenAwaiting` returns the task token (whichever comes first in `s.Tokens`), the task token is
  driven into the gateway branch, and the real gateway token stays parked `running` forever.
- **Dependencies:** none in this slice. Note the backlog's correction: damage is at the
  **exact-equality** lookup, *not* the prefix match — do not "fix" `step_cancel.go:26`.
- **Status:** `VERIFIED` (both the collision site and the benign prefix site read from source).

### 75 — The wall-clock purity guard is evadable by import alias

- **Package(s):** `engine` (test-only: `engine/purity_test.go`)
- **Symbols/files:** `engine/purity_test.go`
  - `wallClockCalls(f *ast.File)` — matches only when
    `pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time"`. The AST identifier for
    `import chrono "time"` is **`chrono`**, so `pkg.Name != "time"` ⇒ early `return true` ⇒ the call
    is never recorded. `parser.ParseFile` is used with **no type information**, so there is nothing
    to resolve the alias back to the `"time"` package.
  - `TestCorePurityNoWallClock` consumes `wallClockCalls` ⇒ silently green.
  - `deniedEngineImports` = `{"/transport/", "/internal/persistence", "/runtime/", "watermill",
    "gocron", "clockwork", "casbin", "go.opentelemetry.io"}` — **`"time"` is not, and never was, a
    denied path**, so `TestCorePurityImportDenylist` cannot compensate. The backlog's ⚠ correction
    of the audit ("over-credits the denylist") is **CONFIRMED from source**.
  - `TestCorePurityNoOTel` only greps for `go.opentelemetry.io` ⇒ also green.
  - `TestPurity_ASTDetectsWallClock` is the detector's own liveness test — it uses an **unaliased**
    `import "time"` fixture, so it does not cover the alias case either.
- **Tier:** `S`
- **Fix sketch:** in `wallClockCalls`, resolve the local name of the `"time"` import per file
  (walk `f.Imports`, honour `imp.Name`) and match `sel.X` against **that** identifier — plus dot-
  and blank-import handling. Add an aliased fixture to `TestPurity_ASTDetectsWallClock`.
- **Falsifiable test:** extend `TestPurity_ASTDetectsWallClock` with a second table case whose
  source is `import chrono "time"` … `chrono.Now()`, asserting `wallClockCalls` reports it.
  **Fails today** for the reason above (`pkg.Name == "chrono"`).
- **Dependencies:** none. Guards ADR-0138's core-purity invariant; a fix here may surface latent
  violations elsewhere in `engine/` (unlikely — the unaliased form is already RED, so no existing
  file can be reading the clock unaliased).
- **Status:** `VERIFIED` from source (the aliasing evasion is structural in the matcher; the
  denylist genuinely never contained `"time"`).

---

## Persistence (audit §4.2)

### 76 — Every replica arms every timer; exclusion is opt-in and per-fire

- **Package(s):** `runtime` (+ `internal/persistence/store`, `scheduler`)
- **Symbols/files:**
  - `runtime/jobstore.go:71` `(*jobStore).Load` → `j.driver.timerStore.ListArmed(ctx)` at **:77** —
    **no ownership/replica predicate anywhere in the loop** (:83-105); every row becomes a
    `scheduler.ScheduledJob`.
  - `runtime/timerops.go:385 (*ProcessDriver).RehydrateTimers` → `NewJobStore(driver).Load(ctx)` →
    `driver.sched.Activate(ctx, j)` for **every** job (`runtime/timerops.go:394`).
  - `internal/persistence/store/timerstore.go` `ListArmed` — no owner column in the predicate.
  - Exclusion today is per-*fire* only (the CAS on commit), not per-*arm*.
- **Tier:** `D`
- **Fix sketch:** add an ownership/fencing dimension to the arm path — either filter `ListArmed`
  by an owner/lease column, or fence each occurrence (claim-on-fire with a per-occurrence token)
  so N replicas produce one fire. Cross-package (`runtime` + `store` schema + `scheduler`) ⇒ ADR.
- **Falsifiable test:** conformance test over `dbtest.RunTestSQLite` — arm one timer, build three
  `ProcessDriver`s over the same store, `RehydrateTimers` on each, assert exactly one fire callback
  runs. **Fails today**: `Load` returns the same row to all three and `Activate` arms it three times
  (backlog's measured 3 Loads → 9 fires).
- **Dependencies:** hard interaction with **80** (multi-replica default) and **86** (contention
  drops fires); overlaps backlog **63** (non-leader-armed timer) and **66** (post-commit
  projections have no reconciler) — both outside this slice.
- **Status:** `VERIFIED` for the mechanism (no ownership filter on `Load`, unconditional
  `Activate`). The 3→9 fire count is inherited from the probe run.

### 77 — A post-commit `Activate` failure loses the timer until reboot

- **Package(s):** `runtime`
- **Symbols/files:** the **two** driver-side `Activate` sites, both WARN-and-continue:
  - `runtime/processdriver.go:836` — post-commit arm:
    `"runtime: timer arm: post-commit activate failed, skipping (durable arm rehydrates on next boot)"`
  - `runtime/timerops.go:394` — the boot-recovery path inside `RehydrateTimers`:
    `"runtime: rehydrate: failed to re-arm timer, skipping"`
  - (`scheduler/scheduler.go:610` is a third `Activate` call but is the scheduler's own
    `ActivationAuto` path; timer jobs are `ActivationManual` — `runtime/timerjob.go:47` — so it is
    **not** a timer-arm site. The backlog's "two sites" is correct as scoped to the driver.)
- **Tier:** `D`
- **Fix sketch:** a timer **reconciler**: a periodic (or `Start()`-triggered) re-scan comparing
  `ListArmed` against the scheduler's live registrations and re-arming the difference — new public
  verb + lifecycle ⇒ ADR.
- **Falsifiable test:** `runtime` + `dbtest.RunTestSQLite` — inject a scheduler whose `Activate`
  fails once, commit a step that arms a 200 ms timer, then `Start()` again and wait past due;
  assert the fire callback ran. **Fails today**: the failure is logged and dropped, `Start()` does
  not re-scan, and the durable-but-unarmed row is only picked up by an explicit `RehydrateTimers`
  at boot (0 fires in 1.6 s in the probe).
- **Dependencies:** same seam as **76** (both want an arm-side reconciliation); one ADR could
  cover both. Instance of the class in backlog **66**.
- **Status:** `VERIFIED` (both sites read from source, both WARN-only; `RehydrateTimers` is the
  only re-scan and is not called by `Start()`).

### 78 — A crash mid-`RunInTx` makes a shared cache fabricate an instance that never existed

- **Package(s):** `persistence`
- **Symbols/files:**
  - `persistence/caching_instance_store.go:351 (*CachingInstanceStore).RunInTx` — the rollback
    compensation is an **in-process `defer`** (`:359-366`) that evicts touched ids only when
    `succeeded == false`. A process crash (`os.Exit`, SIGKILL, panic-with-exit) never runs it.
  - `persistence/caching_instance_store.go:243 (*CachingInstanceStore).Create` — write-throughs
    `c.put(ctx, id, step.State, tok)` **immediately** after `c.backing.Create` returns, i.e. while
    the enclosing tx is still uncommitted.
  - `persistence/caching_instance_store.go:287 Commit` — same eager `c.put` on success.
  - The cache substrate is consumer-chosen (`persistence/durableprovider_cache.go:41
    WithCacheProvider`); a *shared* provider (Redis/memcache) makes the residue visible to peers.
- **Tier:** `D`
- **Fix sketch:** make cache writes **commit-ordered** — buffer the `put` on the `txTouched` set and
  flush only after `runner.RunInTx` returns nil (never inside the tx), and/or write a
  crash-surviving invalidation (short TTL + version fence read-through). Cache-coherence contract
  change ⇒ ADR.
- **Falsifiable test:** `persistence` + `dbtest.RunTestSQLite` + an in-memory *shared* provider
  simulating a second node — call `RunInTx` with a `Create` inside, return an error **and skip the
  defer** (exercise via a helper that mimics crash by dropping the eviction), then read through a
  second `CachingInstanceStore` over the same provider; assert `Load` reports not-found.
  **Fails today**: node B sees the phantom `version=1` entry. ⚠ Faithfully reproducing the *crash*
  needs a subprocess (`os.Exit`), as the probe did — a pure in-process test proves the write-inside-
  tx half, not the crash half.
- **Dependencies:** ⚠ backlog's own note — **fixing 80 by scaling out properly ARMS this one**
  (a real distributed cache is precisely what makes the residue cross-node). Sequence: 78 before 80.
- **Status:** `VERIFIED` for the mechanism (eager in-tx `put`, defer-only compensation). The
  Redis + `os.Exit(9)` observation and the 5-minute TTL window are inherited (default instance TTL
  is indeed `5 * time.Minute`, `persistence/durableprovider_cache.go:34`).

### 79 — `HumanTaskStore.Upsert`'s completion axis is still unvalidated

- **Package(s):** `humantask` (the guard), `internal/persistence/store` (the caller)
- **Symbols/files:**
  - `humantask/validate.go:40 func Validate(t HumanTask) error` — enumerates `t.State`, then
    guards **only** the claim axis: `State == Claimed && t.Claim == nil` and
    `State == Unclaimed && t.Claim != nil`. **`t.Completion` is never inspected.**
  - `internal/persistence/store/humantask_store.go:148 (*HumanTaskStore).Upsert` calls
    `humantask.Validate(t)` at `:153` and rejects with `humantask.ErrInvalidTask`.
  - The Upsert godoc itself states the completion axis is unconstrained "deliberately, since the
    completion axis is unconstrained (ADR-0183)".
- **Tier:** `D`
- **Fix sketch:** extend `humantask.Validate` with the completion rules
  (`Completed && Completion == nil` ⇒ error; `Unclaimed|Claimed && Completion != nil` ⇒ error) and
  amend ADR-0183, which **deliberately** left this axis open. Implementation is ~10 lines; the
  decision to reverse a shipped ADR and reject previously-accepted shapes is the ADR-sized part.
- **Falsifiable test:** table case in `humantask` — `HumanTask{State: Unclaimed, Completion: &…}`
  ⇒ expect `ErrInvalidTask`. **Fails today**: `Validate` returns `nil` for that shape (no branch
  reads `Completion`).
- **Dependencies:** ADR-0183 (must be amended, not merely cited). Interacts with **90** — both are
  human-task invariants but at different layers (**79** is the persistence write guard, **90** is
  the runtime precondition); fixing one does not fix the other.
- **Status:** `VERIFIED`, including the backlog's ⚠ correction: the audit's "the write path
  validates **nothing**" is **CONTRADICTED** — `Upsert` does call `humantask.Validate` and the
  claim axis is enforced. Re-headline on the completion axis, as the backlog says.

### 80 — Caching + `AlwaysOwn` is the DEFAULT `DurableProvider`

- **Package(s):** `persistence` (+ `runtime/kernel`)
- **Symbols/files:**
  - `persistence/durableprovider_cache.go:29 defaultDurableConfig()` —
    `cacheEnabled: true`, `instanceOwnership: kernel.AlwaysOwn{}`, `instanceTTL: 5m`.
    Applied by `applyDurableOptions` to **all three** constructors
    (`persistence/durableprovider.go:65, :107, :150` all call `cfg.wrapCaching`).
  - `runtime/kernel/ownership.go:42 AlwaysOwn` — `Acquire` unconditionally `true`.
  - The single construction warning: `persistence/caching_instance_store.go:190-191`
    `if _, ok := owner.(kernel.AlwaysOwn); ok { c.logger.Warn("persistence: CachingInstanceStore paired with AlwaysOwn is single-replica only; …") }`.
  - `persistence/unsafe_config.go:30 WarnUnsafeConfig` — its three rules are `MultiReplica &&
    CallLinksEnabled && !CallLinkLeaseWired`, `!HistoryCapSet`, `!PruningScheduled`. **None of them
    reads caching or ownership**, so a `MultiReplica: true` profile gets **no** warning about the
    caching/`AlwaysOwn` pairing.
- **Tier:** `D`
- **Fix sketch:** either flip the default (`cacheEnabled: false` unless an ownership is supplied,
  or default to a fail-loud ownership) **or** add a `CacheEnabled`/`InstanceOwnershipWired` field to
  `DeploymentProfile` and a fourth `WarnUnsafeConfig` rule. Default-behaviour change on a public
  constructor ⇒ ADR (+ CHANGELOG breaking note).
- **Falsifiable test:** `persistence` table test — `WarnUnsafeConfig(logger, DeploymentProfile{
  MultiReplica: true, HistoryCapSet: true, PruningScheduled: true})` must emit a warning naming the
  caching/ownership hazard. **Fails today**: zero records for that profile.
- **Dependencies:** ⚠ **78 first** — properly scaling out (a shared cache) arms 78's crash residue.
- **Status:** `VERIFIED` on substance; ⚠ **one sub-claim CONTRADICTED as literally worded.** The
  backlog says "`WarnUnsafeConfig(MultiReplica:true)` emits `\"\"`". Read against source, a bare
  `DeploymentProfile{MultiReplica: true}` has `HistoryCapSet == false` and `PruningScheduled ==
  false`, so it emits **two** warnings (`WarnMsgHistoryCap`, `WarnMsgPruning`) — just not one about
  caching/ownership. The probe presumably set those two fields true. Use the corrected wording:
  *"no `WarnUnsafeConfig` rule reads caching or ownership at all"*, which is what the source shows.

### 81 — The relay holds row locks and an open tx across a network `Publish`

- **Package(s):** `internal/persistence/store` (+ `persistence` for the public relay constructors)
- **Symbols/files:** `internal/persistence/store/relay.go`
  - `:220 (*Relay).DrainOnce` — the documented invariant at `:229-238` is explicit:
    *"claim + Publish + mark all run inside ONE transaction that is committed only at the end. The
    SELECT … FOR UPDATE SKIP LOCKED lock is held for the whole drain."*
  - `:265` claim (locks held until commit) → `:288 if pubErr := r.pub.Publish(ctx, c.event)` inside
    the loop → `:328 q.Commit(txCtx)`.
  - Shipped default batch: `:197 batch: 100`; override `:96 WithRelayBatchSize`.
- **Tier:** `D`
- **Fix sketch:** split the drain into claim-tx → publish (outside any tx) → mark-tx, with a
  claim/lease column so an abandoned claim is reclaimable. This weakens the current "whole batch
  commits atomically" invariant into "at-least-once with visible in-flight claims" ⇒ ADR
  (the doc comment above is the contract being changed).
- **Falsifiable test:** `internal/persistence/store` conformance over SQLite — a publisher that
  blocks 200 ms per event, batch 10, concurrently issue an engine commit; assert the commit's
  latency stays below a bound (or does not return `SQLITE_BUSY`). **Fails today** because the
  relay's write tx is open for `10 × 200 ms` and SQLite is single-writer.
- **Dependencies:** ⚠ interacts with **117** (out of slice): the documented `busy_timeout` DSN is
  inert on `modernc.org/sqlite`, which is what turns the block into a hard `SQLITE_BUSY` failure
  instead of a wait. **Fix 117 first** or the measured severity is misattributed. Also relates to
  **109** (pool sizing).
- **Status:** `VERIFIED` from source (single-tx drain across `Publish`, default batch 100). The
  641 µs → 1.55 s (2418×) figure is inherited.

### 82 — No retention path for the three fastest-growing tables

- **Package(s):** `internal/persistence/store` (+ `persistence` façade, + migrations), `docs-only`
  for the retention doc
- **Symbols/files:** `internal/persistence/store/pruner.go` — the **five** pruners are
  `PruneOutbox` (:74), `PruneCallLinks` (:109), `PruneChainLinks` (:141),
  `PruneProcessedMessages` (:166), `PruneTimers` (:212) (plus `ReclaimNeverDueTimers` :284, not a
  pruner). **None targets `wrkflw_instances`, `wrkflw_journal` or `wrkflw_human_task`.**
  FK confirmed: `wrkflw_journal.instance_id … REFERENCES wrkflw_instances(instance_id)` with **no
  `ON DELETE CASCADE`** in all three dialects
  (`migrations/postgres/0001_init.sql:27`, `migrations/sqlite/0001_init.sql:37`,
  `migrations/mysql/0001_init.sql:35 fk_journal_instance`).
- **Tier:** `D`
- **Fix sketch:** add `PruneInstances`/`PruneJournal`/`PruneHumanTasks` with a terminal-status +
  cutoff predicate, **plus a migration adding `ON DELETE CASCADE`** (or an ordered child-first
  delete) — schema migration + retention policy ⇒ ADR, and `docs/retention.md` must be corrected.
- **Falsifiable test:** `internal/persistence/store` conformance over SQLite — insert a terminated
  instance with journal rows, call the new `PruneInstances` at a far-future cutoff, assert both
  tables are empty. **Fails today**: no such method exists (compile error = valid RED), and a raw
  `DELETE FROM wrkflw_instances` errors on the journal FK.
- **Dependencies:** none in slice; `WarnMsgPruning` in `persistence/unsafe_config.go:12` currently
  enumerates only "outbox, call-link, chain-link, dedup, and timer tables" and must be updated too.
- **Status:** `VERIFIED`, including the backlog's ⚠ correction: **`docs/retention.md` already
  exists** (the audit proposes creating it) — so this is a *correction* task, not a creation task.

### 83 — Schema-skew protection is prose; there is no `CheckSchema`

- **Package(s):** `persistence` (+ `internal/persistence/store`)
- **Symbols/files:** repo-wide grep for `CheckSchema` / `SchemaVersion` over non-test `.go` files
  returns **zero** hits. The only readiness surface is `persistence/health.go:32 PingCheck`, whose
  `Check` issues a bare `Ping` (`pinger` interface, `:14`) and knows nothing about schema.
  `persistence/migrator.go` has the migration machinery but no verification verb exposed as a
  health check.
- **Tier:** `D`
- **Fix sketch:** add `persistence.NewSchemaCheck(conn, dialect)` satisfying the `httpcore
  .HealthCheck` shape (`Name` + `Check`), comparing the applied-migration version against the
  library's embedded expectation; mount it beside `PingCheck`. New public API ⇒ ADR.
- **Falsifiable test:** `persistence` + `dbtest.RunTestSQLite` — migrate, then `DROP` a column the
  library writes; assert `SchemaCheck.Check` returns an error while `PingCheck.Check` returns nil.
  **Fails today**: `NewSchemaCheck` is undefined (compile error = valid RED).
- **Dependencies:** relates to **106** (readiness cannot see scheduler/elector/notifier) — same
  `HealthCheck` registration seam; consider one ADR covering the readiness surface.
- **Status:** `VERIFIED` (absence established by symbol search over non-test files, not by a
  name-filtered test run).

### 84 — The store layer reads the wall clock directly, against ADR-0138

- **Package(s):** `internal/persistence/store`
- **Symbols/files:** re-counted from source. `time.Now()` appears **8×** in non-test files; **2**
  are latency stopwatches (`store_core.go:136`, `store_core.go:191` — `start := time.Now()`, never
  persisted), leaving **5 persisted wall-clock sites** — matching the backlog exactly:
  - `internal/persistence/store/store_core.go:83` (`now := time.Now().UTC()`)
  - `internal/persistence/store/store_core.go:223` (`now := time.Now().UTC()`)
  - `internal/persistence/store/definitions.go:98` (`createdAt := timeArg(ds.dialect, time.Now().UTC())`)
  - `internal/persistence/store/dedup.go:86` (`timeArg(d.dialect, time.Now().UTC())`)
  - `internal/persistence/store/chainlink.go:81` (`at = time.Now().UTC()`)

  And **3 clockwork-compliant types**, also matching: `PgxNotifier`
  (`notifier_pgx.go:33 clk clockwork.Clock`, `:55 WithPgxNotifierClock`), `Relay`
  (`relay.go:49`, `:101 WithRelayClock`), `CallLinkStore`
  (`call_links.go:75`, `:50 WithCallLinkClock`).
- **Tier:** `S`
- **Fix sketch:** give `Store`, `DefinitionStore`, `DedupStore` and `ChainLinkStore` a
  `clk clockwork.Clock` field defaulting to `clockwork.NewRealClock()` with a `WithXClock` option,
  mirroring the three types that already comply; replace the 5 sites with `s.clk.Now().UTC()`.
  All inside `internal/` ⇒ **no public API change**, and ADR-0138 already decided the policy.
- **Falsifiable test:** `internal/persistence/store` conformance over SQLite — inject a
  `clockwork.FakeClock` pinned to a fixed instant, `Create` an instance / register a definition,
  read the row's timestamp back and assert it equals the fake instant. **Fails today**: the fields
  are stamped from the real wall clock, so the assertion sees "now", not the fake instant.
  (Today the option does not exist, so the RED is a compile error — equally valid.)
- **Dependencies:** none. ⚠ Respect the backlog's correction: the audit's enumeration was too small
  on **both** sides (it missed `ChainLinkStore.Record`/`DefinitionStore` among the offenders **and**
  `CallLinkStore`/`PgxNotifier` among the compliant). Both re-counts are confirmed above.
- **Status:** `VERIFIED` — enumerations re-derived here, 5 and 3 exactly.

---

## Runtime (audit §4.3)

### 85 — `BroadcastSignal` is non-idempotent under retry

- **Package(s):** `runtime`
- **Symbols/files:** `runtime/processdriver_signal.go:32 (*ProcessDriver).BroadcastSignal`
  - `:50` `hits := signalStartDefs(driver.listDefinitions(ctx), name)`
  - `:56-60` `if err := driver.sigbus.Publish(...); err != nil { errs = append(errs, err) }` —
    **records the error and continues**, it does not return
  - `:61-67` `for _, h := range hits { driver.createAtNode(ctx, h.Def, h.NodeID, "", payload) }` —
    the fan-out is a **plain synchronous loop on the caller's goroutine**, and the empty
    `instanceID` argument is commented in-place: *"signal-start create is not deduped — each
    broadcast mints a fresh instance via the driver's id generator"*
  - `:68` `return errors.Join(errs...)` — so the caller sees a non-nil error **after** the creates
    already succeeded, and a retry re-creates them.
- **Tier:** `D`
- **Fix sketch:** give signal-start creation a deduplication key (broadcast id / correlation key
  threaded into `createAtNode`), or make the fan-out transactional/outbox-driven so a retry is a
  no-op. Also decide whether `Publish` failure should short-circuit the creates. New key on a public
  verb + at-least-once semantics ⇒ ADR.
- **Falsifiable test:** `runtime` — a `SignalBus` whose `Publish` always errors, two registered
  signal-start definitions; call `BroadcastSignal` twice with the same name/payload; assert the
  instance count is 2, not 4. **Fails today**: each call runs the create loop unconditionally.
- **Dependencies:** ⚠ **ADR-0158 does NOT close it** — 0158 changed waiter-*arm* dispatch in
  `engine/`, this is signal-*start* creation in `runtime/`. ⚠ This is a **second** undeduplicated
  creator, so **88**'s "the only undeduplicated creator" is **false**; a single idempotency-key ADR
  should cover **85 and 88** together.
- **Status:** `VERIFIED` from source, including the "synchronous on the caller goroutine" and
  "not deduped" halves (the latter is stated verbatim in the code's own comment).

### 86 — Nothing bounds concurrent step execution, and contention silently drops timer fires

- **Package(s):** `runtime` (+ `scheduler` for the unused gocron limiter)
- **Symbols/files:**
  - `runtime/processdriver.go:145-147` — `// inflight counts admitted, currently-executing units of
    work` / `inflight sync.WaitGroup`. A `WaitGroup` **has no capacity**; it counts, it never
    blocks an `Add`.
  - `runtime/driver_shutdown.go:30 (*ProcessDriver).admit()` — the only gate is
    `if driver.draining.Load() { return nil, false }`, then `driver.inflight.Add(1)`. Nothing
    refuses on load.
  - `runtime/timerops.go:330 (*ProcessDriver).timerFireFunc` — `const maxAttempts = 5` (`:341`);
    on exhaustion it logs
    `"runtime: timer fire: ApplyTrigger permanently dropped after CAS conflicts"` (`:356`) and
    **returns** — the fire is gone, nothing re-arms it.
  - `LimitConcurrentJobs`: **zero** hits repo-wide (non-test and test alike).
- **Tier:** `D`
- **Fix sketch:** two separable decisions — (a) a real admission bound (buffered semaphore behind
  `admit`, with a `WithMaxConcurrentSteps` option and a documented rejection error), and (b) timer-
  fire durability after CAS exhaustion (re-arm with backoff / hand to the reconciler of **77**
  rather than dropping). Both are new public options + failure contracts ⇒ ADR.
- **Falsifiable test:** (a) `runtime` — set the (new) limit to 1 and issue 2 concurrent
  `BroadcastSignal`s, assert the second is refused/queued. **Fails today**: the option does not
  exist and `admit` accepts unboundedly (probe: 500 admitted, 0 refused).
  (b) a store whose `Commit` always returns `kernel.ErrConcurrentUpdate`; fire a timer; assert the
  fire is eventually retried/re-armed. **Fails today**: 5 attempts then a permanent drop.
- **Dependencies:** (b) shares the reconciler seam with **76**/**77**. Do not fix (b) by raising
  `maxAttempts` — that moves the cliff, it does not remove it.
- **Status:** `VERIFIED` from source for all three legs (`sync.WaitGroup` has no capacity by
  construction; `maxAttempts = 5` then drop; zero `LimitConcurrentJobs` references). The 53.9 µs /
  500-admitted numbers are inherited.

### 87 — Cancel propagation orphans the whole descendant subtree

- **Package(s):** `runtime`
- **Symbols/files:** `runtime/processdriver_cancel.go`
  - `:24 CancelInstance` → `:32 driver.applyTrigger(...)` → `:43 driver.propagateCancel(ctx, instanceID, visited)`
  - `:53 propagateCancel` → `:55 driver.callLinks.ListRunningChildren(ctx, parentID)`
  - `:97` the child cancel: `driver.applyTrigger(ctx, childDef, child.ChildInstanceID, engine.NewCancelRequested(...))`
    — and on any error **other than** `engine.ErrCancelNotApplicable` it logs and `continue`s
    (`:113`), skipping the recursion into that child's own children.
  - **No CAS retry:** `runtime/processdriver.go:563 applyTrigger` is a single `store.Load` +
    `deliverLoop`; `kernel.ErrConcurrentUpdate` propagates straight out. The **only** CAS-retry loop
    in `runtime/` is `runtime/timerops.go:341-352` inside `timerFireFunc`.
  - `ListRunningChildren` has exactly **one** non-test caller — `processdriver_cancel.go:55`
    (other hits are the interface decl `runtime/kernel/calllink.go:50`, the in-memory impl
    `runtime/kernel/mem_calllink.go:196`, the SQL impl `internal/persistence/store/call_links.go:499`,
    and two doc comments in `persistence/{sqlite,mysql}.go`). **Re-counted; the backlog is right.**
- **Tier:** `S`
- **Fix sketch:** extract `timerFireFunc`'s `maxAttempts` CAS-retry loop into a shared
  `(*ProcessDriver).applyTriggerRetryingCAS` and use it for both the parent cancel (`:32`) and the
  child cancel (`:97`). ~40 lines, no public API. (Whether to recurse anyway on a *non*-CAS error is
  a **separate `D`** — the current `continue` is a documented deliberate choice in the code comment
  at `:100-110`, so reversing it needs an ADR, not a patch.)
- **Falsifiable test:** `runtime` — parent → child → grandchild call-link chain; a store wrapper
  that returns `kernel.ErrConcurrentUpdate` for the child's *first* commit only; `CancelInstance` the
  parent; assert child **and** grandchild reach a terminal status. **Fails today**: the single-shot
  `applyTrigger` surfaces the conflict, `propagateCancel` logs + `continue`s, and both descendants
  stay `running` while `CancelInstance` returns `err=nil`.
- **Dependencies:** none blocking. Related to **86**(b) — same "one CAS conflict = permanent loss"
  shape, same shared-helper fix.
- **Status:** `VERIFIED` from source (no retry in `applyTrigger`; `continue` on non-`ErrCancelNot
  Applicable` errors; single non-test `ListRunningChildren` caller). ⚠ The code has been revised
  since `AUDIT.md` was written — `ErrCancelNotApplicable` **does** now recurse (ADR-0180) — so the
  surviving gap is narrower than the audit's wording and matches the backlog's.

### 88 — `StartInstance` has no idempotency key

- **Package(s):** `service`, `transport/http/httpcore` (+ `runtime`)
- **Symbols/files:**
  - `service/request.go:14 StartInstanceRequest` — the struct is **exactly** `{DefRef
    model.Qualifier; Vars map[string]any}`. Re-counted from source; the backlog is right.
  - `service/service.go:26` (interface) and `service/service.go:339 (*ProcessEngine).StartInstance`
  - `transport/http/httpcore/endpoints.go:35` — builds the request from the body, nothing else
  - Case-insensitive grep for `idempotenc` across `transport/` returns **zero** hits.
  - Existing plan: `docs/plans/2026-07-13-start-instance-idempotency-key.md`, header reads
    `Status: OPEN — needs design (ADR + brainstorming before implementing)`.
- **Tier:** `D`
- **Fix sketch:** add `StartInstanceRequest.IdempotencyKey` (+ an `Idempotency-Key` header binding
  in the three HTTP adapters) backed by a unique constraint / the existing dedup table, returning
  the already-created `ProcessInstance` on a repeat. Public API + wire contract ⇒ ADR (the plan
  itself says so).
- **Falsifiable test:** `service` — two `StartInstance` calls with the same key; assert one
  instance and identical returned `ProcessInstance.ID`. **Fails today**: the field does not exist
  (compile error = valid RED); without it, two independent instances are created.
- **Dependencies:** ⚠ the statement's own correction — **"the only undeduplicated creator" is
  FALSE**; **85** is a second one. Design one idempotency mechanism covering **85 + 88**.
  ADR-0018 is about *action* keys and does **not** cover this.
- **Status:** `VERIFIED` (struct shape, zero transport hits, plan status all read from source).

### 89 — A foreign scheduler that omits `Location()` silently persists wrong `NextRun`s

- **Package(s):** `runtime` (+ `scheduler` for the capability)
- **Symbols/files:**
  - `runtime/timerops.go:23-25` — `type locatedScheduler interface { Location() *time.Location }`,
    **unexported**, opt-in
  - `runtime/timerops.go:30-38 (*ProcessDriver).schedulingLocation()` — the silent fallback:
    ```go
    if ls, ok := driver.sched.(locatedScheduler); ok { if loc := ls.Location(); loc != nil { return loc } }
    return time.UTC
    ```
    **no log statement of any level in the function.**
  - Consumers of the fallback: `runtime/timerops.go:162`, `:291`, `:514`,
    `runtime/processdriver.go:779` — each computes the persisted `NextRun` in that location.
- **Tier:** `S`
- **Fix sketch:** emit a one-time WARN (at driver construction, not per call, so it cannot spam) when
  `driver.sched` does not satisfy `locatedScheduler` **and** a calendar/at-time trigger is in use;
  optionally export the capability so consumers can discover it. ~20 lines, no contract change.
  (A `D` alternative — putting `Location()` on the `scheduler.Scheduler` port itself — is breaking.)
- **Falsifiable test:** `runtime` — a foreign scheduler double with **no** `Location` method + a
  `slogtest`-style handler; construct the driver and arm a calendar timer; assert a WARN naming the
  missing capability was recorded. **Fails today**: `schedulingLocation` returns `time.UTC` with
  zero log output.
- **Dependencies:** ADR-0137 (location-aware `Trigger.Next`) is the decision this weakens.
- **Status:** `VERIFIED`, and the backlog's ⚠ correction of the audit is **confirmed on both
  seams**: (a) `Location()` **is exported** — `scheduler/scheduler.go:343
  func (s *NativeScheduler) Location() *time.Location`; (b) the jobstore-save assertion is **not** a
  silent fallback — `runtime/jobstore.go:168-171` returns
  `fmt.Errorf("workflow-runtime: job store: unexpected job implementation %T", sj)`. So **2 of the
  audit's 3 seams are wrong**; only the UTC fallback is real. Do not aim a fix at `jobStore.Save`.

### 90 — ⭐ Silent claim theft: any eligible actor can take a task another actor holds

- **Package(s):** `runtime/task` (primary), `engine` (the authoritative gate)
- **Symbols/files:**
  - `runtime/task/service.go:194 (*TaskService).Claim` — the whole body is:
    ```go
    task, err := s.store.Get(ctx, taskID)          // :195
    if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil { … }  // :199
    s.humanTasks.Add(ctx, 1, …"claimed")           // :202
    return engine.NewHumanClaimed(s.clk.Now(), taskID, actor), nil  // :203
    ```
    **`task.Claim` and `task.State` are never read.** The missing precondition is exactly
    *"the task must not already be held by a different actor"*.
  - `runtime/task/service.go:219 (*TaskService).Reassign` — **25 lines below** — reads the current
    holder and refuses a mismatch:
    ```go
    var claimant string
    if task.Claim != nil { claimant = task.Claim.Actor.ID }   // :227-230
    if from != claimant { return nil, fmt.Errorf("workflow-runtime: reassign: from %q is not the current claimant %q", from, claimant) }  // :231
    ```
    So the *same* transfer of ownership is guarded on the `Reassign` verb and unguarded on `Claim`
    — `Claim` bypasses `Reassign`'s guard entirely.
  - The engine does **not** compensate: `engine/step_triggers.go:567 handleHumanClaimed` checks only
    `task == nil` (`ErrTokenNotFound`) and `!task.IsOpen()` (`ErrTaskNotOpen`). `IsOpen()` is
    `State == Unclaimed || State == Claimed` (`humantask/humantask.go:167`), so an **already-Claimed**
    task passes, and `:577 task.Claim = &humantask.Claim{Actor: t.Actor, At: t.OccurredAt()}`
    overwrites the holder. This is why the probe saw `Claim.Actor` flip alice→bob with `err=<nil>`
    on both the trigger and the delivery.
- **Tier:** `D`
- **Fix sketch:** add the precondition at **both** layers — a new `engine.ErrTaskAlreadyClaimed`
  sentinel refused in `handleHumanClaimed` when `task.Claim != nil && task.Claim.Actor.ID !=
  t.Actor.ID` (the authoritative gate), plus the same fast-fail in `TaskService.Claim` so the
  metric and round-trip are not spent. `D`, not `S`, because: a new exported sentinel, a
  previously-succeeding public call now failing, **and** a real trade-off with no existing answer —
  ⚠ **there is no unclaim/release verb anywhere** (grep for `Unclaim`/`HumanUnclaimed` returns
  nothing; the only exit is engine-side cancellation), so a strict precondition creates a new stuck
  state when a claimant disappears. Decide re-claim-by-same-actor idempotency and the release path
  in the same ADR.
- **Falsifiable test:** `runtime/task` — Alice claims; Bob (also eligible) calls `Claim`; assert
  `errors.Is(err, engine.ErrTaskAlreadyClaimed)` and no trigger. Plus an `engine` test that
  `handleHumanClaimed` refuses the second claim. **Fails today**: both return `err=nil` and the
  claim silently transfers.
- **Dependencies:** ⚠ **ADR-0183 does not close it** (0183 constrained the task *shape* — `Claimed`
  requires a claim, `Unclaimed` forbids one — not a claim *precondition*). Sibling of **79**, which
  is the persistence-layer completion-axis gap: **fixing either does not fix the other**. The
  missing release verb makes this an input to backlog **69** (operator escape hatches).
- **Status:** `VERIFIED` end-to-end from source at both layers. ⚠ The backlog's "twelve lines below"
  is off — `Reassign` starts **25 lines** after `Claim` (`:194` vs `:219`); the guard itself is at
  `:231`. Substance unaffected.

### 91 — Eventing consumers get no schema/version envelope

- **Package(s):** `internal/eventing/watermill` (the emitter), `eventing` (the public consumer
  helpers)
- **Symbols/files:** `internal/eventing/watermill/publisher.go:91 (*Publisher).publish`
  - `:92` `payload, err := json.Marshal(ev.Payload)` — `ev.Payload` is written to the wire **at top
    level**, with no wrapper object
  - `:104-106` — the entire metadata set: `msg.Metadata.Set("topic", …)`,
    `Set("instance_id", …)`, `Set("definition_ref", …)`. **No schema name, no version, no
    event-type discriminator beyond the topic.**
  - `:99-103` — `id := ev.DedupKey` (falls back to a random UUID), used as the watermill message id.
  - Consumer side reads exactly those three keys: `eventing/chaining.go:55, :64, :71, :73`.
- **Tier:** `D`
- **Fix sketch:** wrap the payload in a versioned envelope
  (`{"schema":"wrkflw.instance.completed","v":1,"data":{…}}`) or, if the flat shape must be kept for
  compatibility, add `schema`/`schema_version` metadata keys and a consumer-side reader. Wire
  contract for every existing consumer ⇒ ADR (amending **ADR-0012**, which deferred this as YAGNI)
  + a migration/compat window.
- **Falsifiable test:** `internal/eventing/watermill` — publish an event whose `Payload` contains a
  user variable named like an engine field (e.g. `"schema"`, `"v"`, `"outcome"`); assert the
  consumer can still distinguish engine metadata from user data. **Fails today**: user keys sit in
  the same top-level object as anything the engine would add, so a collision is unresolvable.
- **Dependencies:** none blocking. Note the backlog's sharpening: the risk is a **key collision**
  between an engine-added field and a user variable, not merely "consumers must guess the shape" —
  aim the ADR at that.
- **Status:** `VERIFIED` from source (top-level `json.Marshal(ev.Payload)`; exactly three metadata
  keys, enumerated above).

---

## Public API (audit §4.4)

### 92 — Generated gomock doubles are compiled into the public `service` package  ⚠ BREAKING, IN SCOPE

- **Package(s):** `service` (source of the leak), `transport/http/{httpcore,stdlib,gin,fiber}`
  (every consumer)
- **The 4 mock files — the complete repo-wide set** (`grep -rl "Code generated by MockGen"`):
  1. `service/opsadmin_mock.go` — `MockRelayStatsAdmin`, `MockTimerAdmin` (+ recorders/call types)
  2. `service/lineage_mock.go` — `MockLineageAdmin`
  3. `service/deadletter_mock.go` — `MockDeadLetterAdmin`
  4. `service/policyadmin_mock.go` — `MockPolicyAdmin`

  Each carries `// Package service is a generated GoMock package.` and a directive of the form
  `mockgen -source=<x>.go -package=service -destination=<x>_mock.go -typed`. **`-package=service`
  is the defect**: the doubles land in the public package, not a `_test` package.
- **Measured here (executed, not inherited):**
  - `go doc service | grep -c "generated GoMock package"` → **4**
  - `go doc -all service | grep -cE '^type [A-Z]'` → **49**; of those, `^type Mock` → **22**
    ⇒ 27 real, ratio **1.81×**. All three numbers match the backlog exactly.
- **Every consumer, enumerated** (`grep -rn 'service\.NewMock'`):
  - **Non-test consumers in-repo: ZERO.**
  - Test consumers — **7 files, all in OTHER packages**, which is why the mocks must currently be
    exported: `transport/http/httpcore/admin_endpoints_test.go`,
    `transport/http/fiber/fiber_test.go`, `transport/http/stdlib/stdlib_test.go`,
    `transport/http/stdlib/errors_test.go`, `transport/http/stdlib/coverage_test.go`,
    `transport/http/gin/gin_admin_test.go`, `transport/http/gin/gin_admin_errors_test.go`.
  - The backlog's "a *non-test* external file calling `service.NewMockPolicyAdmin` builds `EXIT=0`"
    is the **hazard demonstration**, not an in-repo consumer — do not go looking for one.
- **Tier:** `D`
- **Fix sketch:** move the four generations into a dedicated package —
  `mockgen … -package=servicemock -destination=servicemock/<x>_mock.go` under
  `service/servicemock/` (public sub-package, keeps the doubles usable by the 7 cross-package test
  files and by consumers who want them, while removing 22 types from `go doc service`). Update the
  7 import sites. **Removes exported symbols from a public package ⇒ BREAKING ⇒ ADR + CHANGELOG**;
  ⚠ window-limited — cheap before v0.1.0, a major-version event after.
  (`internal/servicemock` is the alternative if consumers must *not* get them; it still satisfies
  all 7 in-repo test files.)
- **Falsifiable test:** an API-surface guard in `service` — parse the package's exported type set
  and assert no name matches `^Mock`. **Fails today**: 22 matches.
- **Dependencies:** none blocking; sequence with **95** (STABILITY.md must describe the moved
  surface). Same ADR-0004 "what belongs at module root" question as **116** (out of slice).
- **Status:** `VERIFIED` — file list, `-package=service` directives, the three counts, and the full
  consumer enumeration all derived here by execution/grep.

### 93 — `persistence` is an N×M constructor lattice — `54` constructors

- **Package(s):** `persistence`
- **Counts re-derived here by execution** (`go doc -all persistence`):
  - `^func (New|Open|Migrate)` → **54**. Confirms the backlog; the audit's "~35" is wrong.
  - `^func MySQLWith` → **14** (exactly the list: `MySQLWithCallLinkClock`,
    `MySQLWithCallLinkLease`, `MySQLWithHistoryCap`, `MySQLWithStoreLogger`,
    `MySQLWithStoreMeterProvider`, `MySQLWithStoreTracerProvider`, `MySQLWithBatchSize`,
    `MySQLWithMaxDeliveryAttempts`, `MySQLWithPollInterval`, `MySQLWithRelayBackoff`,
    `MySQLWithRelayClock`, `MySQLWithRelayLogger`, `MySQLWithRelayMeterProvider`,
    `MySQLWithRelayTracerProvider`).
  - `^func WithDurable` → **3** (`WithDurableHumanTaskCacheTTL`,
    `WithDurableInstanceCacheOwnership`, `WithDurableInstanceCacheTTL`) — legitimate, as the
    backlog says. **The audit blamed the wrong prefix.**
- **The "identical types" proof, from source:** `type MySQLOption = store.Option` and
  `type Option = store.Option` — the **same** alias. Likewise
  `type SQLiteRelayOption = RelayOption`. So:
  - `OpenMySQL(ctx, db, WithOutboxNotify())` **compiles**, and `WithOutboxNotify`'s own godoc says
    *"Only Postgres emits a NOTIFY; MySQL silently skips it."* ⇒ silent no-op, confirmed.
  - `NewSQLiteRelay(db, pub, WithListenNotify())` **compiles**, and `WithListenNotify`'s godoc says
    *"Postgres-only"* ⇒ silent no-op, confirmed.
- **Tier:** `D`
- **Fix sketch:** collapse the 14 `MySQLWith*` aliases into the single shared option set (they are
  already the same type — the aliases carry no information) and make dialect-inapplicable options
  **fail loudly**: give `store.Option` a dialect predicate so `OpenMySQL(…, WithOutboxNotify())`
  returns an error instead of quietly doing nothing. Deleting 14 exported functions is **breaking**
  ⇒ ADR + CHANGELOG.
- **Falsifiable test:** `persistence` — `OpenMySQL(ctx, db, WithOutboxNotify())` must return a
  non-nil error naming the unsupported option. **Fails today**: it returns a working store and
  `err == nil`, and the NOTIFY simply never happens.
- **Dependencies:** window-limited alongside **92** (both are pre-v0.1.0 breaking cleanups; bundle
  them). Also touches **95** (STABILITY.md enumerates the surface).
- **Status:** `VERIFIED` — all four counts executed here (54 / 14 / 3), both silent-no-op paths
  proven from the type aliases and the options' own godoc.

### 94 — YAML semantic errors carry no source positions

- **Package(s):** `definition/model`
- **Symbols/files, with the backlog's correction confirmed:**
  - **Already positioned (free from yaml.v3), so NOT the gap:** `definition/model/yaml.go:209`
    `dec.KnownFields(true)` — strict-field and type errors arrive as a `*yaml.TypeError`, whose
    per-error strings are `line N: …`. `boundFieldErrors` (`:185-193`) deliberately preserves the
    concrete `*yaml.TypeError` rather than flattening it.
  - **Not from `Build()`, so the audit's attribution is wrong:** unknown node kind is raised at
    `definition/model/yaml.go:88` `fmt.Errorf("workflow-definition: unknown node kind %q", ny.Kind)`
    inside `fromNodeYAML`, reached via `coreFromYAML` at `yaml.go:243` — **inside `ParseYAML`**.
    `(*definitionLoader).Build` is `definition/model/builder.go:227`.
  - **The genuine gap:** the semantic validators in `definition/model/validate.go` run over the
    **built model**, which carries no positions — e.g. `ErrDanglingFlow`
    (`validate.go:58`, raised at `:346` and `:349`) names `flow %q source %q` / `target %q` but no
    line; likewise the start-event rules around `validate.go:444`.
- **Tier:** `D`
- **Fix sketch:** decode into `yaml.Node` (or keep a parallel `map[nodeID]line` side table built
  during `coreFromYAML`) and thread it into `Validate`'s error construction so semantic errors read
  `line 42: workflow-definition: flow "f3" target "pay" …`. Adding position provenance to the model
  (or a parallel channel for it) is a cross-package shape decision ⇒ ADR.
- **Falsifiable test:** `definition/model` — a YAML doc whose flow targets a nonexistent node,
  asserting the error string contains the flow's line number. **Fails today**: `ErrDanglingFlow`'s
  message contains IDs only; there is no line information anywhere on the path.
- **Dependencies:** ADR-0167 (strict decoding) is the neighbouring decision. ⚠ Do **not** aim the
  fix at `Build()` or at unknown-kind handling — both are the audit's false sub-claims.
- **Status:** `VERIFIED`, and the backlog's ⚠ sub-claim correction is **confirmed on both halves**
  (`KnownFields` line numbers exist; unknown-kind is a `ParseYAML`-time error).

### 95 — `STABILITY.md` contains stale facts

- **Package(s):** `docs-only` (`STABILITY.md`, `README.md`)
- **Findings re-derived here:**
  - `STABILITY.md:81` — "…`gocron` pinned to **v2.21.2**…" vs `go.mod:11`
    `github.com/go-co-op/gocron/v2 **v2.22.0**` (ADR-0135's pin). **Stale, confirmed.**
  - `STABILITY.md:35` — lists "`engine/`, **`model/`**, `runtime/`, `action/`, `authz/`" as
    module-root packages. There is **no root `model/`** (`ls` of the repo root confirms); it is
    `definition/model`. **Confirmed.**
  - `STABILITY.md:81` also lists `samber/do` v2 as a locked dependency; `grep -c "samber/do"
    go.mod` → **0**. (This is backlog **120**, out of slice — recorded here because it lives in the
    same sentence as the gocron pin and should be fixed in the same edit.)
  - `README.md:633` — "There are **19** node kinds". ⚠ **Re-count differs from the backlog's.**
    `definition/model/definition.go:17-35` declares **18 `NodeKind` constants**, of which
    `KindUnspecified` (iota 0) is the zero sentinel ⇒ **17 real kinds**.
    `engine/step_dispatch.go:36 nodeStrategies` maps **16** (`KindBoundaryEvent` is arm-driven, not
    strategy-dispatched). So README is stale, but **the replacement number must be re-derived
    against whatever README's own grouping counts** — do not copy "18" or "19" from either
    document. `ASSUMPTION (unverified)`: which of 16/17/18 README intends.
- **Tier:** `S` (docs-only)
- **Fix sketch:** correct the gocron version, drop the phantom root `model/`, resolve the
  `samber/do` line against `go.mod`, and re-derive the node-kind count from
  `definition/model/definition.go` before writing it into `README.md:633`.
- **Falsifiable test:** a docs-drift guard test that parses the gocron version out of `go.mod` and
  asserts it appears in `STABILITY.md`. **Fails today**: `STABILITY.md` says `v2.21.2`, `go.mod`
  says `v2.22.0`. (The node-kind count is harder to guard mechanically — `⚠ vacuity-risk` for that
  half unless the test counts `NodeKind` constants and asserts the README number.)
- **Dependencies:** **92** and **93** both change the documented public surface — do this **after**
  them, or it will be stale again immediately. Overlaps backlog **120** (out of slice).
- **Status:** `VERIFIED` for the gocron pin, the phantom `model/`, and the `samber/do` absence
  (all executed). ⚠ **PARTIALLY CONTRADICTED** on the node-kind count: the backlog's "the repo has
  18" counts constants including `KindUnspecified`; the real-kind count is 17 and the dispatchable
  count is 16.

### 96 — The parity suite is blind to routes nobody listed

- **Package(s):** `transport/http/parity` (test-only), `transport/http/{stdlib,gin,fiber}`
- **Symbols/files, with both audit sub-claims re-checked:**
  - `transport/http/parity/parity_test.go:22` — **`package parity_test`**, and it is the **only**
    file in the directory. ⇒ the package is **not importable**, so the audit's "make it internal"
    proposal is moot. **Backlog correction CONFIRMED.**
  - `transport/http/` contains **four** directories — `httpcore`, `stdlib`, `gin`, `fiber` — but
    `httpcore` is the shared **core**, not an adapter; the parity doc comment itself names "the
    three HTTP transport adapters — transport/http/stdlib, transport/http/gin,
    transport/http/fiber". ⇒ **three** frameworks. **Backlog correction CONFIRMED.**
  - **Route surface re-counted here: 26 × 3 = 78.**
    stdlib: `handle(` appears 27× in non-test files, one of which is its own definition at
    `transport/http/stdlib/groups.go:16` ⇒ **26 call sites**.
    gin: `\.(GET|POST|PUT|DELETE|PATCH)\(` in non-test files ⇒ **26**.
    fiber: `\.(Get|Post|Put|Delete|Patch)\(` in non-test files ⇒ **26**.
    **Exactly matches the backlog.**
  - The suite is alive but unenforcing: it iterates a hand-written table, so a route added to one
    adapter and listed nowhere is invisible.
- **Tier:** `D`
- **Fix sketch:** derive the three adapters' registrations from **one** shared route table
  (a `[]httpcore.RouteSpec` that each `Mount` ranges over), then make parity assert
  *table == each adapter's registered set* rather than *table ⊆ everything*. Restructures how all
  three adapters mount ⇒ ADR. (A cheaper `S` mitigation: a completeness test that reflects each
  adapter's registered patterns and asserts the three sets are equal — catches "added to one
  adapter only" without restructuring, but not "added to all three and listed nowhere".)
- **Falsifiable test:** register an extra route in **stdlib only** and run
  `go test ./transport/...`. **Fails today** in the wrong direction: it stays `EXIT=0`. The
  positive control (diverging an existing listed route) already goes RED, so the mechanism is
  alive — the assertion just never sees unlisted routes.
- **Dependencies:** touches the same `Mount`/`CustomizeOption` seam as **98** (`MaxBytesReader`
  would want the same shared table) and **104** (error-body shaping). Consider one transport ADR.
- **Status:** `VERIFIED` — all three re-counts (26/26/26), the `package parity_test` line, and the
  three-adapters framing executed/read here.

### 97 — Authoring fans a definition author across packages

- **Package(s):** `definition` (+ `definition/{activity,event,gateway,flow,schedule,model,build}`)
- **Symbols/files:** `README.md:55-70` — the Quickstart's first code block is
  *"Define a process (Go builder)"* and imports exactly **two** packages:
  `github.com/kartaladev/wrkflw/definition` and `github.com/kartaladev/wrkflw/definition/activity`.
  The audit's own proposed remedy (lead with the fluent builder) is therefore **already shipped**.
- **Tier:** `A`
- **Adjudication:** **not actionable as filed.** The backlog itself ranks it lowest and records that
  the fix the audit proposed has landed. The residual — a *full-fidelity* author still reaches into
  7 (minimal) / 14 (`examples/production_wiring`) packages — is a real ergonomics fact, but it is
  the direct consequence of ADR-0004's no-`pkg/` module-root layout and the BPMN-family package
  split, i.e. a **deliberate trade-off**, not a defect. If the owner wants it reopened, the shape is
  a `D`: a re-export facade in `definition` covering the common node kinds, weighed against the
  duplicate-symbol cost.
- **Falsifiable test:** none proposed — `⚠ vacuity-risk`. An "import count ≤ N" test would assert a
  taste preference, not a behaviour.
- **Dependencies:** none.
- **Status:** `VERIFIED` (README's lead block and its two imports read from source).

---

## Security (audit §4.5)

### 98 — No HTTP body or process-variable size limit

- **Package(s):** `transport/http/{stdlib,gin,fiber}` (the decode sites), `service`/`engine` (the
  variable-size half)
- **Decode-site enumeration re-counted here** (non-test files, per adapter):
  - `transport/http/stdlib` → **13** (`json.NewDecoder` idiom)
  - `transport/http/gin` → **13** (`ShouldBindJSON`/`c.Bind` idiom)
  - `transport/http/fiber` → **13** (`BodyParser` idiom)
  - `transport/http/httpcore` → **0**
  ⇒ **39 sites across three different idioms.** Exactly matches the backlog; the audit's
  "every decode is a bare `json.NewDecoder`" is **CONTRADICTED** — two thirds of the sites are
  framework binders that `http.MaxBytesReader` does not reach the same way.
  - `grep -rn "MaxBytesReader|BodyLimit" transport/` (non-test) → **zero hits**. Fiber's 4 MiB
    rejection is `fiber.DefaultBodyLimit`, i.e. the framework's, not wrkflw's.
- **Tier:** `D`
- **Fix sketch:** two coupled decisions. (a) A transport-level body cap applied uniformly — a
  `httpcore.CustomizeOption` carrying `MaxBodyBytes`, honoured by all three adapters through their
  own idiom (`http.MaxBytesReader` for stdlib, `BodyLimit` config for fiber, a limiting
  `io.LimitReader` wrapper for gin). (b) A **process-variable** size limit enforced in
  `service`/`engine` so the 64 MiB variable is refused before it is persisted and echoed. New
  public option + a new rejection status on every route ⇒ ADR.
- **Falsifiable test:** per-adapter table test posting a body one byte over the configured cap;
  assert `413`. **Fails today**: stdlib and gin return **201 Created** for a 256 MiB body
  (~3.2× heap amplification measured); fiber returns 413 only by its own default.
- **Dependencies:** the fix wants the shared route table from **96**; a per-adapter patch will
  otherwise drift again. Overlaps **99** (both are unbounded-input DoS) and **104** (the
  rejection body must not echo internals).
- **Status:** `VERIFIED` for the 13/13/13/0 enumeration and the zero `MaxBytesReader`/`BodyLimit`
  hits (executed here). The 256 MiB / 770–834 MiB heap figures are inherited.

### 99 — A pathological expression pins a core and the request deadline is ignored

- **Package(s):** `engine` (the interface), `internal/expreval`, `runtime` (the timeout option)
- **Symbols/files, with the backlog's ⚠ INVERSION correction verified:**
  - `engine/conditions.go:20-27` — `type ConditionEvaluator interface` — **all three methods
    (`EvalBool`, `EvalDuration`, `EvalString`) take `(code string, env map[string]any)` and NO
    `context.Context`.** This is why a request's 1 s deadline cannot reach evaluation. **VERIFIED.**
  - `internal/expreval/expreval.go:108` — `p, err := expr.Compile(code, expr.AllowUndefinedVariables())`.
    **`expr.MaxNodes` is never called.** In expr-lang, `expr.MaxNodes(0)` is the call that
    *disables* the node-count check, so **not calling it leaves `DefaultMaxNodes` (1e4) ACTIVE**.
    ⇒ **the audit's finding is inverted, exactly as the backlog says**, and its proposed
    `MaxNodes` fix would not have stopped the measured stall (that condition is ~11 AST nodes).
  - The unmetered surface is therefore **caller-supplied arrays** in `env` — `vm.memory` counts only
    VM-allocated data, so a clean O(n²) over a large input array is unbounded (60 ms → 242 ms →
    1.10 s → 4.00 s in the probe).
  - The only existing guard is opt-in and wall-clock based: `engine/conditions.go:43`
    `var conditions = expreval.New(expreval.WithTimeout(0))` (guard **disabled** by default, for
    core purity), overridable via `runtime/processdriver_options.go:200`
    `expreval.New(expreval.WithTimeout(d))`.
- **Tier:** `D`
- **Fix sketch:** add `context.Context` to the three `ConditionEvaluator` methods and thread the
  request context through `Step`/`StepOptions.Evaluator` so a deadline aborts evaluation; pair it
  with an **input-size** bound on the env (cap variable/array sizes — the same knob as **98**(b)),
  since a node-count cap demonstrably does not cover this. Changing a public interface's method
  set is **BREAKING** ⇒ ADR (and it re-opens ADR-0003/ADR-0049's core-purity trade-off, since a
  `ctx` in the core is not the same as a wall clock in the core).
- **Falsifiable test:** `engine` — a gateway condition doing O(n²) over a caller-supplied 10k-element
  array, stepped with a `ctx` cancelled after 100 ms; assert `Step` returns a
  deadline/cancellation error. **Fails today**: the evaluator never sees the context, so it runs to
  completion (37.66 s in the probe) and the request returns 201.
- **Dependencies:** ⚠ **Do NOT implement the audit's `MaxNodes` fix** — it is inverted and would be
  a no-op that looks like a mitigation. Pairs with **98**(b). ⚠ The backlog's de-risking claim is
  **verified true**: 26 routes, **no definition-deploy route**, so today the expression source is
  not attacker-supplied over HTTP — which is what keeps this Medium rather than High.
- **Status:** `VERIFIED` — the missing `ctx`, the absent `MaxNodes` call, and the disabled-by-default
  timeout all read from source; the inversion is confirmed against expr-lang's own semantics.

### 100 — No data-at-rest posture or codec for PII in variables

- **Package(s):** `internal/persistence/store`, `persistence` (the option surface)
- **Symbols/files:** case-insensitive grep for `encrypt`/`redact`/PII codec across
  `persistence/`, `internal/persistence/`, `engine/` (non-test) returns **zero hits** — there is no
  encryption or redaction seam at all. The two plaintext copies are:
  - `wrkflw_instances.snapshot TEXT NOT NULL`
    (`internal/persistence/store/migrations/sqlite/0001_init.sql:25`) — the whole
    `engine.InstanceState`, variables included, as JSON.
  - `wrkflw_journal.trigger TEXT NOT NULL` (`…/0001_init.sql:40`) — the applied trigger, payload
    included.
- **Tier:** `D`
- **Fix sketch:** introduce a `persistence.VariableCodec` seam (encrypt/decrypt or
  tokenize/detokenize) applied symmetrically at the snapshot and journal write/read boundaries,
  plus a documented "what wrkflw does and does not protect" statement in `SECURITY.md`. New public
  extension point + a key-management contract the library must **not** own ⇒ ADR.
- **Falsifiable test:** `internal/persistence/store` + SQLite — register a codec that reverses
  strings, save an instance with `vars["ssn"]="123"`, then read the raw `snapshot` column with a
  direct query and assert the plaintext is absent. **Fails today**: the seam does not exist
  (compile error = valid RED), and the raw column contains the value verbatim.
- **Dependencies:** interacts with **101** (a tamper-evident journal and an encrypted journal are
  the same column) and with backlog **54** (unredacted instance read, out of slice) — one
  data-protection ADR should cover the read path, the at-rest path and the audit path together.
- **Status:** `VERIFIED` (absence of any codec established by symbol search; both column
  definitions read from the migration).

### 101 — No tamper-evident audit trail

- **Package(s):** `internal/persistence/store` (schema + writes), `engine` (what the record carries)
- **Symbols/files, with the backlog's ⚠ "wrong on both nouns" correction verified:**
  - `engine/state.go:248-262 type NodeVisit` — fields are exactly `NodeID`, `TokenID`, `EnteredAt`,
    `LeftAt`, `TaskID`, `CloseKind`. **No actor field** — and the godoc says so explicitly:
    *"a rendered history can resolve who claimed/completed it … from the task record instead of
    duplicating that audit on the visit. See ADR-0145."* ⇒ **the audit's first noun is
    CONTRADICTED**, confirming the backlog.
  - `wrkflw_journal` is **6 columns** — `instance_id, seq, kind, trigger, occurred_at, applied_at`
    (`internal/persistence/store/migrations/sqlite/0001_init.sql:36-43`) — **no hash, no previous-
    hash chain, no signature**. Re-counted; the backlog is right.
  - The actor's two real homes: the **task record** (`wrkflw_human_task.claim_actor` /
    `completion_actor`, `internal/persistence/store/humantask_store.go:118-120`) and the
    **journal**'s `trigger` payload.
- **Tier:** `D`
- **Fix sketch:** add a hash-chain column to `wrkflw_journal` (`prev_hash`, `row_hash` over the
  canonicalized row) written inside the same tx, plus a `VerifyJournal(instanceID)` verb; and
  document that `UPDATE`/`DELETE` are detectable rather than prevented (the library cannot revoke
  the consumer's own DB grants). Schema migration + a new integrity contract ⇒ ADR.
- **Falsifiable test:** `internal/persistence/store` + SQLite — write three journal rows, mutate
  row 2 with a direct `UPDATE`, assert `VerifyJournal` reports the break at seq 2. **Fails today**:
  the verb does not exist (compile error = valid RED) and the mutation is undetectable.
- **Dependencies:** ⚠ **Do NOT aim a fix at `engine.NodeVisit` or at the outbox** — the audit's
  premises there are false (no actor field by ADR-0145 design; the outbox emitted **zero**
  actor-bearing events). Aim at the journal and the task record. Shares the journal column with
  **100**.
- **Status:** `VERIFIED`, including both of the audit's refuted nouns.

### 102 — A casbin cross-node policy-reload failure is silently swallowed

- **Package(s):** `internal/authz/casbin` (the defect), `casbinauthz` (the public surface)
- **Symbols/files:** `internal/authz/casbin/db.go:97` — **the audit's citation is exact**:
  ```go
  if err := w.SetUpdateCallback(func(string) { _ = enforcer.LoadPolicy() }); err != nil {
  ```
  The `_ =` discards the reload error inside the watcher callback. Nothing logs it, nothing marks
  the enforcer stale, and `Enforce` keeps answering from the last successfully loaded policy — so a
  **revoked** permission still returns `true, err=nil`.
  Startup load does fail closed (the `SetWatcher`/`SetUpdateCallback` errors above **are**
  returned). The public wrapper `casbinauthz/casbinauthz.go:168 (*Authorizer).ReloadPolicy` does
  propagate its error — it is only the watcher-driven path that swallows.
- **Tier:** `S`
- **Fix sketch:** in the callback, log the error at `Error` with the channel/node id and increment a
  `wrkflw_authz_policy_reload_failures_total` counter; optionally set a staleness flag the health
  check can read. ~20 lines, `internal/`-only, no public API change. (Making `Enforce` **fail
  closed** on a stale policy would be `D` — that is a real availability/security trade-off and
  should be a separate decision.)
- **Falsifiable test:** `internal/authz/casbin` — an adapter whose `LoadPolicy` returns an error,
  drive the watcher callback, assert an ERROR log line naming the failure was recorded. **Fails
  today**: the error is discarded by `_ =` and the function has no logger call at all.
- **Dependencies:** the "fail closed" variant would interact with **106** (readiness surface) —
  a stale-policy flag is a natural `HealthCheck`.
- **Status:** `VERIFIED` — `db.go:97` read verbatim; the startup-fails-closed half confirmed from
  the surrounding returns.

### 103 — ⭐ Negative/deny-list ABAC predicates fail OPEN

- **Package(s):** `authz` (the evaluation), `internal/expreval` (the cause),
  `transport/http/httpcore` (the `Attributes` drop)
- **Symbols/files:**
  - `authz/authz.go:123 (RoleAuthorizer).Authorize` — step 2 builds
    `env := map[string]any{"actor": actor, "vars": vars}` (`:131-134`) and calls
    `attrEval.EvalBool(spec.Attribute, env)` (`:136`).
  - **The cause:** `internal/expreval/expreval.go:108`
    `expr.Compile(code, expr.AllowUndefinedVariables())`. An **absent** variable evaluates to `nil`,
    so a deny-list predicate over a missing key is *true*:
    `x != "blocked"` → `nil != "blocked"` → **true** → **allow**;
    `x != true` → **true** → allow; `!(x == true)` → `!(false)` → **true** → allow;
    `x == nil or …` → **true** → allow; `… or "manager" in actor.Roles` short-circuits to allow.
    A *positive* predicate (`x == "eu"`) correctly denies on absence, which is why the class was
    never noticed.
  - `authz/authz.go:136-141` — an evaluation **error** is wrapped with `ErrNotAuthorized`, i.e.
    denies everyone. This is why the audit's own exemplar (`actor.attributes.…`) **errors and 403s
    everyone** rather than failing open — **the audit's "silently" is false as written**, and the
    backlog's correction is confirmed.
  - **`Attributes` is dropped at the transport boundary — 3 sites, all in
    `transport/http/httpcore/endpoints.go`:** `:119` `authz.Actor{ID: in.Actor.ID, Roles:
    in.Actor.Roles}`, `:132` same, `:150` `authz.Actor{ID: in.By.ID, Roles: in.By.Roles}`.
    `authz.Actor.Attributes` exists (`authz/authz.go:38`) but never arrives over HTTP.
- **Tier:** `D`
- **Fix sketch:** two parts. (a) Compile ABAC predicates **without**
  `expr.AllowUndefinedVariables()` (or pre-declare the env schema) so a reference to an absent
  variable is a **compile/eval error** ⇒ denies, restoring fail-closed for deny-lists — this is a
  behaviour change for every existing predicate, hence ADR, and it must not regress the engine's
  *gateway* evaluation, which legitimately relies on undefined variables. (b) Carry `Attributes`
  through the three `httpcore` actor constructions (that half alone is `S`).
- **Falsifiable test:** `authz` table test — `AuthzSpec{Attribute: `vars.status != "blocked"`}`
  evaluated with `vars = map[string]any{}`; assert `errors.Is(err, ErrNotAuthorized)`.
  **Fails today**: `nil != "blocked"` is `true`, `EvalBool` returns `(true, nil)`, and `Authorize`
  returns `nil` — everybody is allowed. Second case: post a task claim with
  `actor.attributes.clearance` set and assert the predicate can read it — fails today because
  `httpcore` drops the map.
- **Dependencies:** (a) shares the compile path with **99** (`internal/expreval` is one evaluator
  for both gateways and ABAC — splitting the two compile configurations may be the actual fix).
  ⚠ Severity is **Low → Medium bypass** per the backlog, not the audit's framing.
- **Status:** `VERIFIED` — the fail-open mechanism derived from `AllowUndefinedVariables()` +
  `RoleAuthorizer`'s `!ok ⇒ deny / ok ⇒ allow` branch; the error-path-denies-everyone behaviour and
  the 3 `Attributes`-dropping sites read from source. The specific five predicate forms are
  inherited from the probe run (the mechanism covers all five).
