# Triage — ADR-derived backlog slice (3b, 3d–3f, 4–13, 15, 17–20, 24, 26–41)

Read-only triage run 2026-08-20 against `main` @ `70a631e9`. Every entry below records the
file/symbol actually located; `VERIFIED` means the code was read and it says what the item says.

⚠ Sections are in the order they were triaged, not numeric order: **3b, 3d, 3e, 4, 5, 6, 3f, 7, 8,
9, 10, 11, 12, 13, 15, 17, 18, 19, 20, 41, 24, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38,
39, 40.**

## Summary

| item | package | tier | fix sketch | status |
|---|---|---|---|---|
| 3b | `engine` | S | nil out `s.Timers` when the cancel empties it | VERIFIED |
| 3d | `docs-only` (code in `service`) | S | CHANGELOG note for `incidents[].kind` + `compensating` | VERIFIED |
| 3e | `docs-only` (code in `service`) | S | breaking-change note for `InstanceOps.ResolveCompensationStall` | VERIFIED |
| 3f | `engine` | A | note/trap: empty `ScopeID` is load-bearing; read `NodeID` | VERIFIED |
| 4 | `definition/model`, `engine`, `runtime` | D | per-node stall/retry tier over the engine-wide fallback | VERIFIED |
| 5 | `engine` | D | cap operator `retry`; it resets `RetryAttempts` today | VERIFIED |
| 6 | `engine`/`runtime` | A | owner decision: default stall detection ON? | VERIFIED |
| 7 | `engine` | D | migrate/pin pre-ADR-0171 unpinned cursors | VERIFIED |
| 8 | n/a | A | ADR-0174 rejected it with a measurement; not fixable | VERIFIED |
| 9 | `engine`, `docs/adr/0164` | S | count is **10**, not 8 — state the property instead | VERIFIED |
| 10 | `engine` | S | return `(records, ok)`; closed scope ≠ no records | VERIFIED (reachability unproven) |
| 11 | `engine`, `definition/model` | D | missing-node park → incident, not a keyless wedge | VERIFIED |
| 12 | `engine` | D | discharge `PendingCancel` on a `walkPartial`/abandon finish | VERIFIED |
| 13 | `engine` | D | snapshot signal waiters after Micro's pending parks | VERIFIED |
| 15 | `engine` | S | test `exitNestedEventSubprocessScope`'s arm retirement + conjunct | VERIFIED |
| 17 | `engine` | D | reverse-walk arms still swallowed; fix `rearmRootESP` | PARTLY CONTRADICTED |
| 18 | n/a | A | both bounds closed at ADR-0171's own gate | CONTRADICTED |
| 19 | `processtest` | D | append `ReasonCompensation` to the `Reason` enum | VERIFIED |
| 20 | `service` | A | 53.9 % is raw; filtered it is **93.5 %** | CONTRADICTED |
| 24 | `runtime`, `engine` | D | feed a refused arm back so the phantom record dies | VERIFIED |
| 26 | `scheduler` | S | arithmetic grid jump; `Monthly(120000,{31})` costs **404 ms** | VERIFIED (executed) |
| 27 | `internal/persistence/store` | D | validate definitions on write (or ship a checker) | VERIFIED |
| 28 | `docs-only` (code in `scheduler`) | S | release note; do not diverge from gocron | VERIFIED (executed) |
| 29 | `runtime`, `scheduler` | D | arm the pinned instant instead of re-deriving | VERIFIED |
| 30 | `scheduler` | S | clamp `interval`; `MaxUint64` yields a PAST next-run, `ok=true` | VERIFIED (executed) |
| 31 | `scheduler`, `engine` | S | three `§N` citations resolve to no heading | VERIFIED |
| 32 | `internal/persistence/store`, `engine` | D | version the snapshot; no `DisallowUnknownFields` | VERIFIED |
| 33 | `runtime` | D | driver pre-flight; trigger policy is `rejectSilently` | VERIFIED |
| 34 | `persistence` | S | test the advisory `Lock`/`Unlock` (both **0.0 %**) | VERIFIED (executed) |
| 35 | `definition/model`, `definition/schedule` | A | ADR-0182 records it as an accepted bound | VERIFIED |
| 36 | `definition/schedule` | A | owner decision, already recorded | VERIFIED |
| 37 | `runtime`, `internal/persistence/store` | A | subsumed by backlog 66; escape verbs exist | VERIFIED |
| 38 | `examples/scenarios/admin_monitoring` | S | select by `Kind`, not by index 0 | VERIFIED |
| 39 | `internal/persistence/store` | D | third sweep predicate for orphaned retry rows | VERIFIED |
| 40 | `engine` | S | zero `JitterFraction` must mean full backoff, not zero delay | VERIFIED |

---

## 3b — cancel path flips `s.Timers` nil → empty non-nil slice

- **Package(s):** `engine`
- **Symbol/file:** `engine/state_timers.go` — `removeTimer` (L57-68) and `cancelTimersWhere`
  (L74-89). Both do `out := make([]timerRecord, 0, len(s.Timers))` then `s.Timers = out`
  unconditionally. With `s.Timers == nil` and a non-empty key, `make([]T,0,0)` is non-nil, so the
  snapshot's `Timers` goes `null` → `[]` on the wire.
  ⚠ The sibling `cancelCompensationWalkTimers` (`engine/step_compensation.go:474-483`) **already
  carries the fix** with an explicit comment: *"nil, not an empty slice, when a walk-scoped record
  was the only one — matching cancelAllTimers … Otherwise every RESUME finish … would persist
  `timers: []` where it used to persist null."* So the desired invariant is established in-repo;
  two of the three cancel helpers just don't honour it.
- **Tier:** `S`
- **Fix sketch:** In `removeTimer` and `cancelTimersWhere`, mirror `cancelCompensationWalkTimers`:
  `if len(out) == 0 { s.Timers = nil; return … }`.
- **Falsifiable test:** `engine` black-box: build an `InstanceState` with exactly one timer record,
  call the exported test seam for cancel-by-task-id (see `engine/export_test.go`), assert
  `state.Timers == nil` **and** `json.Marshal` of the snapshot contains `"Timers":null`. It fails
  today because the loop assigns the freshly `make`d empty slice → `Timers != nil` and the JSON
  reads `[]`. Not vacuity-risk: the sibling helper's own comment records the same observation.
- **Dependencies:** none. Touches the same JSON wire as **32** and **61**, but does not conflict.
- **Status:** `VERIFIED` (source; the invariant is stated verbatim in the sibling helper).

---

## 3d — the instance document gains `incidents[].kind` and a `compensating` object (additive)

- **Package(s):** `docs-only` (`CHANGELOG.md`); the code lives in `service`
- **Symbol/file:** `service/instance.go` — `instanceJSON.Incidents []incidentJSON` (L129),
  `instanceJSON.Compensating *compensatingJSON` (L136, `json:"compensating,omitempty"`),
  `incidentJSON.Kind` (`json:"kind"`), `compensatingJSON{ActiveCommandID, Since, ScopeID}`.
  ⚠ **Not** `transport/http/httpcore.InstanceView` — that DTO (`view.go`) projects only
  id/def/status/times/variables and has no incident or compensation fields at all.
- **Tier:** `S` (docs)
- **Fix sketch:** Add an **Added** entry to `CHANGELOG.md [Unreleased]` describing the two new
  `ProcessInstance` JSON members and the "do not route on `incidents[0]`" caveat already written in
  `incidentJSON.Kind`'s godoc.
- **Falsifiable test:** ⚠ **vacuity-risk** — a CHANGELOG edit is not testable. The nearest
  falsifiable artefact is a golden-JSON test in `service` pinning the two new keys; it would fail
  today only if the keys were removed, i.e. it pins rather than reproduces. State that plainly
  rather than pretending a RED exists.
- **Dependencies:** pairs naturally with **3e** (same release-note pass) and **38** (the positional
  `incidents[0]` read the godoc warns about).
- **Status:** `VERIFIED` — fields exist as described; `grep` over `CHANGELOG.md` for
  `compensating`/`active_command_id` returns **0 hits**, so the note is genuinely missing.

---

## 3e — `service.Service` gained a method (ADR-0175): breaking for a decorator

- **Package(s):** `docs-only` (`CHANGELOG.md` / `STABILITY.md`); code in `service`
- **Symbol/file:** `service/service.go` — `InstanceOps.ResolveCompensationStall(ctx,
  ResolveCompensationStallRequest) (ProcessInstance, error)` (L85-97), embedded into
  `Service` (L115-121). Any consumer type implementing `service.Service` (a decorator, a mock, a
  test double) stops compiling on upgrade.
- **Tier:** `S` (docs)
- **Fix sketch:** Add a **Breaking changes (pre-v0.1.0)** entry naming `InstanceOps` /
  `Service` and the new method, with the "embed `service.Service` in your decorator" migration line.
- **Falsifiable test:** ⚠ **vacuity-risk** for the doc half. The code half is already pinned by the
  compiler. A `var _ Service = (*ProcessEngine)(nil)` assertion exists implicitly via the concrete
  type; adding one would not fail today.
- **Dependencies:** same release-note pass as **3d**. No code dependency.
- **Status:** `VERIFIED` — method present; `grep -n "service.Service\|InstanceOps" CHANGELOG.md
  STABILITY.md` finds only one unrelated line (`CHANGELOG.md:953`), so no breaking-change note
  exists.

---

## 4 — per-node `CompensationStallAfter` tier and per-node compensation retry tier

- **Package(s):** `definition/model` (node wire + accessors), `engine` (`step.go`,
  `step_compensation.go`), `runtime` (option plumbing)
- **Symbol/file:** `engine/step.go` — `StepOptions.CompensationStallAfter` (L52-68) and
  `StepOptions.CompensationRetryPolicy` (L69-91); both reduced to `stepPolicy.stallAfter` /
  `stepPolicy.compensationRetry` in `resolvePolicy` (L120-127). Consumed at
  `engine/step_compensation.go` `armCompensationStallTimer` (L501, `pol.stallAfter`) and
  `armCompensationRetryTimer` (L543, `pol.compensationRetry`). Wired engine-wide from
  `runtime/processdriver.go:638` (`CompensationStallAfter: driver.compensationStallAfter`) and
  `runtime/processdriver_options.go:344` `WithCompensationRetryPolicy`.
  Both godocs already name this item: *"One engine-wide window is a deliberate v1 simplification…
  A per-node tier is deliberate backlog, not scope."* (L62-67) and *"One engine-wide policy is a
  deliberate v1 simplification… A per-node tier is backlog."* (L89-90).
- **Tier:** `D`
- **Fix sketch:** Add per-node `CompensateStallAfter` / `CompensateRetryPolicy` to the activity node
  wire in `definition/model`, resolve them in `armCompensationStallTimer` /
  `armCompensationRetryTimer` with the `StepOptions` value as the fallback tier — mirroring
  `effectiveRetryPolicy`'s existing node→definition→default chain.
- **Falsifiable test:** `engine` test: two compensating activities with different node-level stall
  windows; assert the two `ScheduleTimer` commands carry different `Trigger` durations. Fails today
  because `armCompensationStallTimer` reads `pol.stallAfter` only — one value for the whole step —
  so both timers carry the identical duration. Not vacuity-risk: the two durations differ by
  construction only if the node tier exists.
- **Dependencies:** grows the definition wire → interacts with **27** (invalid definitions
  round-trip) and ADR-0167 strict decoding. Should land after **5**/**6** are adjudicated, since
  all three shape the same policy surface.
- **Status:** `VERIFIED` — source-verified engine-wide, and the code comments name the gap.

---

## 5 — a bound on repeated operator `retry`

- **Package(s):** `engine` (`step_compensation.go`), plus `service`/`transport` if the refusal
  needs its own sentinel surfaced
- **Symbol/file:** `engine/step_compensation.go` `retryStalledCompensation` (L1410 ff.). Its own
  comment states the gap: *"Since ADR-0175's verb has no cap, retiring only the stall kind grows the
  count without bound"* (L1440-1446). The function then does `cur.RetryAttempts = 0` (L1476) with
  the comment *"The operator's retry is a fresh attempt at this record, not a continuation of the
  budget the superseded dispatch spent"* — so the ADR-0179 per-record budget is **explicitly reset**
  by every operator retry. `handleResolveCompensationStall` (L1366-1407) has exactly three guards
  (no walk / command mismatch / unknown incident) and **no attempt counter**.
- **Tier:** `D`
- **Fix sketch:** Add an `OperatorRetries int` to `compensationCursor`, incremented in
  `retryStalledCompensation`, and refuse past a policy bound with a new
  `ErrCompensationRetryBudgetExhausted` sentinel — a new snapshot field and a new refusal on an
  operator escape hatch, so it needs an ADR (refusing an escape hatch is itself a hazard).
- **Falsifiable test:** `engine` test issuing N+1 `ResolveCompensationStall{Disposition:
  CompensationRetry}` against the same walk, asserting the last one returns the new sentinel. Fails
  today with `err=<nil>` and a fresh `InvokeAction` on every call — `retryStalledCompensation`
  re-dispatches unconditionally once the three guards pass.
- **Dependencies:** grows the unversioned snapshot → collides with **32** and **61**. Related to
  **4** (same policy surface) and **69** (operator escape-hatch contract).
- **Status:** `VERIFIED` — the absence of a cap is asserted by the production comment and confirmed
  by reading all three guards.

---

## 6 — should stall detection default ON?

- **Package(s):** `engine` (`step.go`) + `runtime` if the default moves; effectively `docs-only`
  until adjudicated
- **Symbol/file:** `engine/step.go:52-68` — *"ZERO DISABLES detection, and zero is the default: with
  it unset no stall timer is scheduled and no command stream changes shape."* Confirmed at
  `engine/step_compensation.go:503` (`if pol.stallAfter <= 0 { return cmds }`).
- **Tier:** `A` — a **pure owner decision**. There is nothing to find; the question is whether the
  silent-walk hazard outweighs changing every existing command stream's shape.
- **Fix sketch:** If answered *yes*: pick a default duration, set it in
  `runtime.NewProcessDriver`'s defaults (not in `StepOptions`, so the pure core stays
  zero-means-off), and add a `CHANGELOG` breaking note. Roughly `S`-sized once decided.
- **Falsifiable test:** N/A until decided. If implemented: a `runtime` test asserting a driver built
  with no options arms a `TimerCompensationStall` on a compensation dispatch — fails today because
  `compensationStallAfter` is the zero value.
- **Dependencies:** would change every command-stream golden in `engine` and `processtest`.
  Adjudicate together with **4**.
- **Status:** `VERIFIED` (the default is zero/off, as stated).

---

## 3f — the stall incident's `ScopeID` is empty for a TARGETED compensation throw

- **Package(s):** `engine`
- **Symbol/file:** `engine/step_timers.go` `handleCompensationStallFired` (L133-151) copies
  `ScopeID: rec.ScopeID`; the record is built in `engine/step_compensation.go`
  `armCompensationStallTimer` (L501-528) as `ScopeID: cur.ScopeID`. The code comment at L514-519
  **states this item verbatim**: *"⚠ It is empty for a TARGETED compensation throw: that cursor is
  built as `{ArchiveKey: ref}` and its emptiness is load-bearing for `walkMode()` … Read `NodeID` …
  rather than inferring location from an empty `ScopeID`, which is ambiguous with the root scope."*
  `armCompensationRetryTimer` (L594-608) repeats the same convention for
  `TimerCompensationRetry`.
- **Tier:** `A` — a **note/trap**, not a defect. The emptiness is load-bearing for `walkMode()`;
  "fixing" it by stuffing the archive key into `ScopeID` would break walk-mode detection. Any real
  disambiguation (e.g. adding `Incident.ArchiveKey` or an `IncidentScopeKind`) is a **public wire
  change** to `engine.Incident` + `service.incidentJSON` and would then be `D`, not `S`.
- **Fix sketch:** No code change. If the owner wants disambiguation: add `ArchiveKey string` to
  `Incident` and `incidentJSON`, populated from `cur.ArchiveKey` at both arm sites — that is an
  ADR-sized decision because it grows the unversioned snapshot (backlog **32**/**61**).
- **Falsifiable test:** If treated as a fix, an `engine` test driving a targeted throw
  (`CompensateActivity`-style ref) to stall and asserting `inc.ArchiveKey == ref` fails today with
  "no such field". As a *note* there is nothing to make fail — ⚠ vacuity-risk if filed as a fix.
- **Dependencies:** would conflict with **32** and **61** (snapshot wire growth).
- **Status:** `VERIFIED` — behaviour is exactly as stated and is documented as intentional.

---

## 7 — a pre-ADR-0171 unpinned cursor keeps ADR-0173's double-run at the `endInstance` harvest

- **Package(s):** `engine`
- **Symbol/file:** `engine/state_compensation.go` — `scopeWideWalkDraining` (L520-523):
  `return cur.ActiveCmdID != "" && len(cur.Records) > 0 && cur.ScopeID == scopeID`. Because
  `partitionForLiveWalk` (L472-474) early-returns the WHOLE record list when that predicate is
  false, a cursor deserialized from a row written before ADR-0171 (`Records == nil`) bypasses the
  partition and its already-dispatched prefix is **re-archived** by
  `harvestOpenScopeCompensations` (L437-448), called from `endInstance`
  (`engine/state.go:603`). The production comment states it: *"a cursor persisted before ADR-0171
  … bypasses the partition entirely and its already-dispatched prefix IS re-archived. That row then
  inherits the double-run ADR-0173 deliberately left it … ADR-0174 §5.3's bound."*
- **Tier:** `D` — needs a cursor-migration ADR (how does a running deployment upgrade rows whose
  cursor carries no pinned `Records`?).
- **Fix sketch:** Either (a) a one-shot snapshot migration that pins `Records` onto any live
  `Compensating` cursor at load, or (b) a `CursorVersion` discriminator on `compensationCursor` so
  `scopeWideWalkDraining` can distinguish "unpinned legacy" from "not a scope-wide walk". Both grow
  the unversioned snapshot.
- **Falsifiable test:** `engine` test: hand-build an `InstanceState` with
  `Compensating{ActiveCmdID: "c1", ScopeID: "s1", NextIndex: 1, StartRecordCount: 3, Records: nil}`
  and three records on scope `s1`, force-terminate, then assert
  `ArchivedCompensations["s1"]` holds only the **undispatched prefix**. It fails today because
  `scopeWideWalkDraining` returns false on `len(Records)==0` and all three records are archived.
  Not vacuity-risk — the `Records: nil` fixture is exactly what disarms the guard.
- **Dependencies:** same migration surface as **8**, **32** and **61**; do them as one bundle.
- **Status:** `VERIFIED` — predicate read, call chain traced, and the behaviour is documented as a
  deliberate bound in ADR-0174 §5.3.

---

## 8 — records stranded on pre-ADR-0174 rows stay unreachable

- **Package(s):** `engine` (would-be), but see status
- **Symbol/file:** `docs/adr/0174-a-dying-instance-harvests-its-open-scopes.md:173-186`
  (**Rejected: recovering records already stranded on pre-ADR-0174 rows**) and L254-256
  (Consequences: *"Deliberate (see the rejected option): recovering them safely needs information
  the row does not carry. Strictly no worse than `main`, and logged as backlog."*).
- **Tier:** `A` — **already adjudicated and rejected with a measurement.** The ADR records that the
  earlier draft which recovered them *"re-dispatched `[undoC undoB undoA]` where only `[undoA]` was
  owed"*, and states it is not fixable: pre-0174 `endInstance` zeroed the cursor, so such a row is
  indistinguishable from a never-walked one.
- **Fix sketch:** None that is safe. If ever revisited it is an operator-tooling question (an
  offline reconciler that a human adjudicates per instance), not an engine change — which puts it
  under **69**'s escape-hatch contract rather than here.
- **Falsifiable test:** ⚠ **vacuity-risk.** There is no assertion that can distinguish a stranded
  row from a never-walked one — that indistinguishability *is* the finding.
- **Dependencies:** subsumed by any decision taken for **7**.
- **Status:** `VERIFIED` — the item restates an ADR consequence that is already an accepted
  trade-off, not an open defect.

---

## 9 — ADR-0164's "eight terminal sites" is stale

- **Package(s):** `engine` (comment), `docs/adr/0164` (prose)
- **Re-counted independently** (`grep -rn "endInstance(" engine/*.go | grep -v _test`), excluding
  the declaration at `engine/state.go:590`:

  | # | site |
  |---|---|
  | 1 | `engine/step_compensation.go:1292` |
  | 2 | `engine/step_compensation.go:1346` |
  | 3 | `engine/step_errors.go:280` |
  | 4 | `engine/step_nodes.go:223` |
  | 5 | `engine/step_nodes.go:352` |
  | 6 | `engine/step_nodes.go:501` |
  | 7 | `engine/step_nodes.go:631` |
  | 8 | `engine/step_nodes.go:633` |
  | 9 | `engine/step_triggers.go:342` |
  | 10 | `engine/step_triggers.go:1271` |

  **Real number today: 10.** The handover's "ten today" is correct; ADR-0164's "eight" is stale.
  Cross-check: `endInstance` is the ONLY writer of a terminal status — `grep "s.Status = "` over
  non-test `engine/` returns `state.go:591` (inside `endInstance`) plus writes of
  `StatusCompensating`/`StatusRunning` only, so the call-site count *is* the terminal-site count.
- **Stale text:** `engine/state_arms.go:174` — *"Since ADR-0164 all eight terminal sites route
  through endInstance"*; and `docs/adr/0164-…:25,58,250` (*"All eight sites route through it."*).
- **Tier:** `S` (comment/doc correction)
- **Fix sketch:** Replace the count with the property — *"every terminal site routes through
  `endInstance`, which is the only writer of a terminal `Status`"* — per Premise Discipline's
  "prefer naming a closed set over counting it". Leave ADR-0164's historical prose alone but add a
  dated correction note.
- **Falsifiable test:** ⚠ **vacuity-risk** for the comment edit. A genuinely falsifiable guard is a
  `go/ast`-based test in `engine` asserting no function other than `endInstance` assigns a terminal
  `Status` — it fails today only if someone adds one, so it is a *pin*, not a RED. Say so rather
  than claiming a red state.
- **Dependencies:** none.
- **Status:** `VERIFIED` — re-counted from source, not inherited: **10**, not 8.

---

## 10 — `compensationRecordsForScope` reads an open scope as a records-exist decision

- **Package(s):** `engine`
- **Symbol/file:** `engine/step_compensation.go:15-23`:

  ```go
  func compensationRecordsForScope(s *InstanceState, scopeID string) []CompensationRecord {
      if scopeID == "" { return s.RootCompensations }
      sc := s.scopeByID(scopeID)
      if sc == nil { return nil }        // ← "scope gone" is indistinguishable from "no records"
      return sc.Compensations
  }
  ```

  `s.Scopes` holds **open scopes only** — `closeScope` (L653-665) removes the entry and
  `endInstance` sets `s.Scopes = nil` (`engine/state.go:635`) — and `harvestOpenScopeCompensations`'s
  own doc says *"no records-exist predicate consults an open scope"* (L403-409). The load-bearing
  consumer is `engine/step_nodes.go:1221-1224`: `records := compensationRecordsForScope(c.s,
  tokScope); if len(records) == 0 { c.s.moveAlongSingleFlow(...) }` — a compensation throw whose
  scope has already been closed **auto-advances silently**, indistinguishable from a scope that
  genuinely had nothing to compensate. Same conflation at `retainedRecordPrefix` (L890) and
  `beginCompensation` (L333, where `scopeID` is the const `""` so it is benign).
- **Tier:** `S`
- **Fix sketch:** Return `([]CompensationRecord, bool)` (or add
  `compensationRecordsForScopeOK`), and at `step_nodes.go:1221` route the `!ok` case to a
  `slog.WarnContext("compensation throw names a closed scope")` plus the `ArchivedCompensations`
  lookup, rather than to the silent `moveAlongSingleFlow`.
- **Falsifiable test:** `engine` test: build a state whose throw token carries `ScopeID: "s1"` with
  **no** `Scope{ID:"s1"}` entry but `ArchivedCompensations["s1"]` holding one record; drive the
  compensation throw. Today it takes the `len(records)==0` branch and emits **zero**
  `InvokeAction` commands with `err=<nil>`; the test asserts one is emitted (or that a warning
  fires). ⚠ **Reachability caveat, honestly stated:** I did not prove a *live* route that parks a
  throw token in a closed scope — ADR-0162 treats a token naming a closed scope as a wedge in its
  own right. So this may be latent-defensive; the fixture above is hand-built.
- **Dependencies:** touches the same helper as **7**; sequence after it.
- **Status:** `VERIFIED` for the conflation (source). `ASSUMPTION (unverified)` for
  end-to-end reachability of the closed-scope throw.

---

## 11 — a flow targeting a NON-EXISTENT node parks a permanent wedge

- **Package(s):** `engine` (primary), `definition/model` (authoring-time half)
- **Symbol/file:** `engine/step.go` `drive`, L318-327:

  ```go
  node, ok := tdef.Node(tok.NodeID)
  if !ok {
      slog.WarnContext(ctx, "token routed to a missing node", …)
      tok.State = TokenWaiting          // ← no AwaitCommand/Signal/Message/Timer set
      continue
  }
  ```

  The token becomes `TokenWaiting` with **every await key empty**, so no trigger kind can reach it
  and the instance stays `running` forever. Provenance and evidence id: `docs/plans/
  2026-08-10-signal-fanout-and-esp-status.md:428-430` (*"evidence D8-0: `WARN token routed to a
  missing node`, then a `TokenWaiting` token with `AwaitCommand == ""` that nothing can resume,
  instance `running` forever"*).
  ⚠ **Authoring-time nuance the item does not state:** `model.Validate` **does** reject dangling
  sequence flows — `definition/model/validate.go:58` `ErrDanglingFlow` + L344-350 check both
  `f.Source` and `f.Target`. So this is reachable via (a) a definition never passed through
  `Validate` (the engine's `Step` takes a `*model.ProcessDefinition` directly — nothing forces
  validation on the library API), and (b) cross-scope/non-flow routing that `Validate` does not
  cover. Backlog **71** (partial rollback resumes into the wrong scope) reaches this exact branch
  from a *validated* definition.
- **Tier:** `D` — "what does the engine do when a token lands on a node that does not exist" is a
  behavioural contract with real trade-offs (park vs incident vs hard error) and it must be decided
  jointly with **71** and **55**.
- **Fix sketch:** Raise an incident (`Incident{Kind: IncidentAction, TokenID: tok.ID, NodeID:
  tok.NodeID, Error: "node not found"}`) and set `TokenIncident` instead of the keyless
  `TokenWaiting` park, so the instance is visible and `ResolveIncident`-able; alternatively return
  `ErrNodeNotFound` from `drive`. The minimal incident variant is roughly `S`-sized once the
  decision is taken.
- **Falsifiable test:** `engine` test: definition whose flow target names `"ghost"`, driven without
  `Validate`. Assert the resulting state carries an incident (or that `Step` errors). Fails today
  because the state carries **zero incidents**, `status=running`, and one `TokenWaiting` token with
  all four await keys empty. Not vacuity-risk — the WARN line proves the branch is reached.
- **Dependencies:** decide with **71** and **55** (the `drive` loop's other unbounded/no-progress
  hazards).
- **Status:** `VERIFIED` (source + recorded measurement D8-0).

---

## 12 — `PendingCancel=true` survives onto a `Running` instance and terminates the NEXT walk

- **Package(s):** `engine`
- **Symbol/file:** `engine/state_compensation.go` `walkTerminates` L282-310 — the mode table is
  explicit: *"walkPartial → resume, consumePendingCancel **NOT SET** → resumes"* and *"a partial
  rollback does NOT consume a deferred cancel, so it resumes with `PendingCancel` still true"*.
  The consumption site is `engine/step_compensation.go` `applyFinish` L1172-1173:
  `if plan.resume && plan.consumePendingCancel && s.PendingCancel { s.PendingCancel = false … }` —
  gated on a flag `walkPartial` never sets. A second leak: `abandonCompensationWalk` L1605-1610
  states *"A `PendingCancel` is therefore left set on the terminated instance. ADR-0175 claimed
  abandon discharges the deferred-cancel deadlock; it cannot."*
  Consequence: the instance returns to `Running` with the flag set, so the operator's cancel is
  silently lost **and** the next `walkThrowTargeted` / `walkThrowScopeWide` / `walkReverse` walk —
  all of which DO set `consumePendingCancel` — terminates the instance instead of resuming.
  Provenance: `docs/plans/2026-08-10-…:455-458` (*"Measured during the audit… Pre-existing… Own
  ADR."*).
- **Tier:** `D` — the plan that opened it says "Own ADR", and the choice (discharge at the partial
  finish / refuse the cancel up front / keep it armed deliberately) is a real trade-off.
- **Fix sketch:** Either set `consumePendingCancel` on the `walkPartial` finish plan (terminating
  the instance the operator asked to cancel), or clear `PendingCancel` + `PendingFinalStatus` with a
  WARN when a `walkPartial` resumes. Both change an operator-visible outcome.
- **Falsifiable test:** `engine` test: `CompensateRequested{ToNode, ReverseNode}` (a partial), a
  mid-walk `CancelRequested`, let the partial finish, then throw a compensation. Assert the
  instance is still `running` after the throw. Fails today: the throw walk's finish consumes the
  stale `PendingCancel` and terminates (`status=terminated`, err `"cancelled"`).
- **Dependencies:** interacts with **69** (escape-hatch contract) and with abandon's leak above —
  fix both in one ADR.
- **Status:** `VERIFIED` — the mode table, the gated consumption and abandon's leak are all stated
  in production comments and confirmed at the code.

---

## 13 — micro mode loses a signal delivery

- **Package(s):** `engine`
- **Symbol/file:** `engine/step_triggers.go:1006` — `snapshotIDs := s.tokenIDsAwaitingSignal(t.Name)`
  taken **before** steps 1-3, and `engine/step_state.go:117-128`, which matches only
  `s.Tokens[i].AwaitSignal == name`. In `Micro` mode (`engine/step.go:367`, `if pol.mode == Micro &&
  stopped { break }`) a token that has not yet reached its intermediate signal catch is still
  `TokenActive` with `AwaitSignal == ""`, so it is **absent from the snapshot**; step 4
  (`step_triggers.go:1146-1153`) iterates the snapshot only, while `markMatched` has already merged
  the payload and consumed the delivery. Evidence id D9,
  `docs/plans/2026-08-10-…:431-434` (*"silently missed while the signal is still consumed and the
  catch is **not** re-armed. Pre-existing."*).
  ⚠ Blast radius, source-verified: **no non-test production caller selects `Micro`** — `grep` for
  `engine.Micro` outside `_test.go` returns only `engine/step.go`'s own definition and comments. It
  is a public `StepOptions.Mode` a library consumer can select, not a path the shipped runtime takes.
- **Tier:** `D` — the fix is a semantics decision for `Micro` (drive-to-quiescence before the
  snapshot, defer the delivery, or document the loss), and any of them changes the documented
  meaning of a public mode.
- **Fix sketch:** Take the snapshot **after** driving pending active tokens to their parks, or make
  `handleSignal` refuse/queue when `pol.mode == Micro` and any token is still `TokenActive`.
- **Falsifiable test:** `engine` table test running the same signal fixture in `Macro` and `Micro`:
  a token upstream of an intermediate signal catch, one `Step(Micro)` short of parking, then
  `SignalReceived`. Assert both modes resume the catch. Fails today only in the `Micro` row — the
  token stays put, `Variables` carry the merged payload, and the catch is not re-armed. Not
  vacuity-risk: the Macro row is the control that proves the fixture works.
- **Dependencies:** none blocking. Same file/function as ADR-0158's fan-out, so it will conflict
  textually with any further `handleSignal` work.
- **Status:** `VERIFIED` (source + recorded measurement D9).

---

## 15 — `engine/step_nodes.go`'s nested arm retirement is uncovered

- **Package(s):** `engine`
- **Symbol/file:** `engine/step_nodes.go` `exitNestedEventSubprocessScope` (L417-511). **Two**
  uncovered things, both named in the production comment at L486-501:
  1. `cmds = appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(parentScopeID))`
     — the non-completing tail (L508);
  2. the `&& c.s.Compensating.ActiveCmdID == ""` conjunct in the root-completion branch (L502).

  The comment records both mutations and their results: *"deleting `&& c.s.Compensating.ActiveCmdID
  == ""` leaves `go test ./engine/...` at EXIT=0 with that test RUNNING and PASSING, and replacing
  the arm retirement below with a no-op likewise leaves the suite green."* ⚠ Note the item's
  citation `step_nodes.go:501` has **rotted** — L501 today is the tail of that comment; the code it
  names is at L508 and L502. Use symbol names.
- **Tier:** `S`
- **Fix sketch:** Two `engine` tests: (a) a nested ESP whose enclosing scope drains into a
  **non-root** grandparent with a sibling ESP arm still armed — assert a `CancelTimer` for that arm
  appears in `Commands`; (b) a nested ESP exiting at root with `Compensating.ActiveCmdID != ""` —
  assert the instance does **not** complete.
- **Falsifiable test:** The mutation is already known and recorded: replace the arm retirement with
  `_ = parentScopeID` (test (a)) and delete the `ActiveCmdID == ""` conjunct (test (b)). Today both
  mutations leave `./engine` at EXIT=0 — that is precisely the RED the new tests must produce.
  Not vacuity-risk: the mutation is prescribed and its current (green) outcome is measured.
- **Dependencies:** none. Pure coverage; conflicts with nothing.
- **Status:** `VERIFIED` — the uncovered lines and both ablations are documented in the function's
  own comment and confirmed at source. ⚠ The line-number citation in the backlog is stale.

---

## 17 — the event-sub-process hole's remaining direction

- **Package(s):** `engine` (`state_compensation.go`, `step_compensation.go`)
- **What the two directions were** (`docs/adr/0168-…:341-350`): (1) arms firing when they should
  not; (2) arms not firing when they should (the root-only `s.Status != StatusRunning` predicate).
- **What shipped:** ADR-0172 closed **both at the fire site**. `engine/step_eventsubprocess.go`
  `fireEventTriggeredSubprocessArm` (L156-183) now reads `if !s.spawnsNewWork() { return nil, nil }`
  with a comment naming both directions explicitly, followed by a scope-liveness check for non-root
  arms. So the item as literally worded is **largely closed**.
- **What is genuinely left — direction (2) for REVERSE walks.**
  `engine/state_compensation.go` `walkTerminates` L300-306: *"⚠ `walkReverse` **RESUMES**, yet is
  reported as terminating — deliberately, per ADR-0172 Decision 1a. A reverse resume sets ResetVars
  and re-arms every root event sub-process (`finishPlan.rearmRootESP`); letting an arm fire into it
  was measured producing two concurrent tokens, the event sub-process body's variables wiped
  underneath it, and an INTERRUPTING one-shot arm resurrected while its body still runs. **Widening
  this is `rearmRootESP`'s problem, not the arm-fire path's.**"* A signal aimed at a root ESP during
  a reverse walk is therefore still **swallowed** — one-shot broadcast, nothing redelivers — which
  is exactly direction (2), narrowed from "every walk" to "reverse walks".
  A second remnant is recorded in ADR-0172's own Consequences (L364-371): *"A second counterexample
  to 'resume ⇒ does not terminate', not closed here"* — an arm firing during a dropped-resume walk
  places a token and suppresses the recovery completion.
- **Tier:** `D` — closing it means redesigning `rearmRootESP` so a reverse resume can tolerate an
  arm firing into it, which is precisely what ADR-0172 declined to scope.
- **Fix sketch:** Make `finishPlan.rearmRootESP` idempotent w.r.t. an arm that fired mid-walk
  (re-arm only arms that are absent, and refuse to resurrect an interrupting one-shot whose body is
  live), then narrow `walkTerminates` to report `walkReverse` as **resuming**.
- **Falsifiable test:** `engine` test: reverse walk in flight, root ESP armed on signal `"esc"`,
  deliver `SignalReceived{Name:"esc"}` mid-walk. Assert the arm fires (a child scope + token
  appears). Fails today: `walkTerminates` reports the reverse walk as terminating,
  `spawnsNewWork()` is false, the arm does not fire, and the delivery is consumed.
- **Dependencies:** conflicts with **12** (both touch `walkTerminates` / `applyFinish`).
- **Status:** `VERIFIED` for the remnant; ⚠ **PARTLY CONTRADICTED** as worded — the general
  "remaining direction" was closed by ADR-0172; what survives is the *reverse-walk* case, and the
  backlog line should be re-worded to say so.

---

## 18 — ADR-0171's two open bounds

- **Package(s):** n/a
- **The two bounds** (recovered from `git show adf39777:docs/plans/HANDOVER.md:261-264`):
  (a) the **error-boundary teardown** route — the record is compensated but the walk's finish still
  wedges on the destroyed resume scope; (b) `exitNestedEventSubprocessScope`'s sibling close is not
  held.
- **They are CLOSED.** `docs/adr/0171-…:252-262` now reads: *"✅ **CLOSED AT THE GATE (2026-08-10)
  — these two bullets used to read 'an error-boundary teardown over a live walk still strands the
  walk's resume' (pinned as a `KNOWN LIMITATION` assertion) and '`exitNestedEventSubprocessScope`'s
  close of the ENCLOSING scope is not held … neither fixed nor demonstrated'. Both are closed by
  Decision part 4."* Source-verified: the two named tests exist and no longer pin the limitation —
  `engine/step_compensation_scope_drain_test.go:334`
  (`TestCompensationWalkKeepsItsRecordsWhenAnErrorBoundaryTearsDownItsScope`, whose L378 comment
  says it *"replaces the 'KNOWN LIMITATION' this test used to pin"*) and L479
  (`TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope`).
- **Tier:** `A` — **already closed; remove from the backlog.**
- **Fix sketch:** Delete backlog item 18 and record the closure. ⚠ What is *still* open from the
  same paragraph is **ADR-0168's conjunct 3**, which is backlog **15** — do not fold it back in
  here under 18's number.
- **Falsifiable test:** N/A — the closing tests already exist and are RED-verified per the ADR.
- **Dependencies:** none.
- **Status:** `CONTRADICTED` — both bounds were closed at ADR-0171's own delivery gate.

---

## 19 — `processtest.Classify` has no reason for a compensation-walk park

- **Package(s):** `processtest`
- **Symbol/file:** `processtest/park.go:14-73` — the `Reason` iota block is exactly
  `ReasonTerminal, ReasonHumanTask, ReasonIncident, ReasonSignal, ReasonMessage, ReasonTimer,
  ReasonAsyncChild, ReasonUnknown`. **Eight constants, no `ReasonCompensation`** (re-counted from
  source, not inherited). What bundle C added instead is the *split rung* documented at L19-53:
  a walk-scoped `IncidentCompensationStall`/`IncidentCompensationFailed` fires `ReasonIncident`
  from the LAST rung, below every actionable reason, so a compensation-failure park is now
  *drivable* — but it reports `ReasonIncident`, which is the reason whose contract says
  `ResolveIncident` frees it, and for these it does not (`engine.ErrIncidentNotResolvable`).
- **Tier:** `D` — a new exported enum constant is public API, and it must be **appended** to the
  iota block (inserting renumbers every existing value in any consumer that persisted one).
- **Fix sketch:** Append `ReasonCompensation` after `ReasonUnknown`, raise it from `Classify`'s last
  rung when `st.Status == engine.StatusCompensating` (or a walk-scoped incident is the only
  incident), and teach `Harness`/`Chain` recipes to drive it through
  `ResolveCompensationStall`.
- **Falsifiable test:** `processtest` test: drive an instance into a stalled compensation walk and
  assert `Classify(...).Reason == ReasonCompensation`. Fails today with `undefined:
  ReasonCompensation` (a compile-error RED), and after the constant exists it fails on the value —
  today the same state classifies as `ReasonIncident` (walk-scoped rung) or `ReasonUnknown` for a
  zero-token `Compensating` instance (`docs/adr/0171-…:307-309`).
- **Dependencies:** **41** is the same item. Interacts with **38** (`Park.Incidents[0]`).
- **Status:** `VERIFIED` — enum re-counted at source; the partial closure by bundle C is real and
  the missing constant is real.

---

## 20 — repo-wide coverage (`service` "53.9 %")

- **Package(s):** `service` (and the measurement method itself)
- **⚠ The number is measured the WRONG WAY, and the repo's own script says so.**
  Executed just now:

  | measurement | command | result |
  |---|---|---|
  | raw, per-package | `go test -count=1 -cover ./service/...` | **53.9 %** |
  | generated-file-excluded (the CLAUDE.md floor, ADR-0143) | `scripts/coverage.sh /tmp/svc.cover` | **93.5 %** |

  `service/` contains four generated mockgen doubles — `deadletter_mock.go`, `lineage_mock.go`,
  `opsadmin_mock.go`, `policyadmin_mock.go` — and `scripts/coverage.sh`'s own header comment names
  this exact package as the motivating example: *"e.g. the `service` package reads 49.9 % raw vs
  89.3 % excluding the four mock files."* The 85 % floor is defined over hand-written code only.
  **`service` is at 93.5 %, comfortably over the floor.**
  Control, so this is not a blanket "the numbers are wrong" claim: `definition` measures **33.3 %
  both ways** (`go tool cover -func` and `scripts/coverage.sh` agree) — it has no generated files,
  so its number is real.
- **Tier:** `A` — the `service` half is a **measurement artefact**, not a defect. What survives is
  the genuine drag: `definition` 33.3 % (2 files, only `definition.go` is production) and
  `internal/dbtest` 39.8 %.
- **Fix sketch:** Correct the backlog line to quote `scripts/coverage.sh` numbers, drop `service`
  from the drag list, and re-file the remaining drag as a `definition` coverage task.
- **Falsifiable test:** N/A (a measurement correction). The falsifier is the table above and it has
  been run.
- **Dependencies:** same class as **34** (`persistence` 84.1 %) — ⚠ re-measure that one the
  filtered way before acting on it too.
- **Status:** `CONTRADICTED` (executed) — `service` is 93.5 % under the repo's own floor
  definition, not 53.9 %.

---

## 41 — no `ReasonCompensation` in `processtest`

- **Package(s):** `processtest`
- **Tier:** `A` — **exact duplicate of 19**, and the backlog line says so itself (*"see backlog
  19"*). The 2026-08-20 audit-verification note at `docs/plans/HANDOVER.md:539` also treats them as
  one (*"**19/41** … is confirmed still open and is **not** renumbered"*).
- **Fix sketch:** Close 41 as a duplicate; do the work under 19.
- **Falsifiable test:** see 19.
- **Dependencies:** 19.
- **Status:** `VERIFIED` (duplicate).

---

## 24 — a refused arm leaves an in-memory `timerRecord` with no durable row

- **Package(s):** `runtime` (refusal site), `engine` (the phantom record)
- **Symbol/file:** `runtime/timerops.go` `timerJobsFor` L179-198 — `if neverDueNextRun(next, ok) {
  driver.obs.timerArmsRefused.Add(ctx, 1); …WARN…; continue }`. The `continue` skips
  `driver.newTimerJob(...)`, so **no** `ScheduledJob` is built, nothing is saved in-tx and nothing
  is activated. But the engine already appended the `timerRecord` to `s.Timers` when it emitted
  `ScheduleTimer` (`engine/step_nodes.go:705,783`, `engine/step_triggers.go:482`,
  `engine/step_compensation.go:508,594`), and that state **is** what gets committed. Result: a token
  parked awaiting a timer with no durable row and no gocron job; `InstanceState.TimerWaiters()` /
  `Park.HasArmedTimers` report an arm that does not exist. The comment itself chooses this: *"Log-
  and-skip rather than error: the StepResult is already computed by now, and a step-time failure was
  measured to wedge the running instance instead."*
- **Tier:** `D` — the engine cannot currently learn that the runtime refused its command. Any real
  fix adds a feedback trigger (`TimerArmRefused`) or an incident kind — new public API on a sealed
  trigger set.
- **Fix sketch:** Emit `engine.NewTimerArmRefused(timerID, at)` back into the driver's own step loop
  so the engine drops the phantom `timerRecord` and raises an incident, rather than only
  incrementing `timerArmsRefused`.
- **Falsifiable test:** `runtime` test: arm a definition whose timer trigger is never-due
  (e.g. `Monthly(12, []int{31})` anchored in February — the shape ADR-0176 names), then read back
  the committed `InstanceState`. Assert `len(st.Timers) == 0`. Fails today: the record is present
  (the refusal happens after `Step` produced it) while `jobStore` holds no row. ⚠ Needs Docker only
  if driven through a durable store; an in-memory driver reproduces the state half.
- **Dependencies:** overlaps **35** (ADR-0182's authoring gate is the *other* end of the same
  problem — catch it before it is ever armed) and **39** (unreachable leaked rows).
- **Status:** `VERIFIED` (source: the `continue` skips job construction while the engine record is
  already in `step.State`).

---

## 26 — the calendar scan is still linear in `interval` — **EXECUTED**

- **Package(s):** `scheduler`
- **Symbol/file:** `scheduler/trigger.go` `calendarNext` L382-455. `bound := maxCalendarScanDays *
  int(interval)` (L419) and `for i := 0; i <= bound; i++` (L420). The monthly path got a
  whole-month skip (L430-440, whose comment records the old cost: *"measured at 6.3 s for
  `Monthly(120000, {31})`, on the arm path, inside the commit transaction"*), but the loop is still
  bounded by `1830 × interval`.
- **Measured just now** (throwaway `scheduler` test, since deleted; `anchor = 2026-08-20T12:00Z`):

  | spec | result | elapsed |
  |---|---|---|
  | `Daily(1)` | 2026-08-21 | 10.2 µs |
  | `Daily(1000)` | 2029-05-16 | 14.8 µs |
  | `Daily(120000)` | 2355-03-09 | **1.73 ms** |
  | `Monthly(1,{31})` (anchored 2026-02-01) | 2026-03-31 | 3.3 µs |
  | `Monthly(120000,{31})` (anchored 2026-02-01) | `ok=false` | **404 ms** |

  So: still linear, still on the arm path inside the commit transaction, just 15× cheaper than the
  6.3 s the ADR fixed. 404 ms of CPU inside a commit tx is a real production hazard.
- **Tier:** `S`
- **Fix sketch:** Replace the day-by-day grid test with direct arithmetic — for `triggerDaily`,
  jump to the first grid day ≥ `after` (`i = (interval - offset%interval) % interval`); for
  `triggerMonthly`, iterate **months** on the interval grid rather than days. Alternatively clamp
  `bound` to `maxCalendarScanDays` and cap `interval` at authoring time (see **35**).
- **Falsifiable test:** `scheduler` test asserting `Monthly(120000, []int{31}).Next(feb)` completes
  under a budget (e.g. 20 ms) **and** returns the same `(next, ok)` as `interval=1`'s grid
  semantics. Fails today: measured 404 ms. Not vacuity-risk — the budget is 20× under the observed
  cost. ⚠ Per this repo's own rule, state the mode: measured **without** `-race`.
- **Dependencies:** must not change answers — pin against the existing
  `TestNativeScheduler_ScheduleReturnMatchesLocation` large-interval row. Conflicts with **30**
  (same file).
- **Status:** `VERIFIED` **by execution** (table above), and *sharper* than the backlog line: the
  monthly case is 404 ms, not merely "linear".

---

## 27 — the definition store round-trips semantically invalid definitions

- **Package(s):** `internal/persistence/store` (+ `definition/model`, `service` if the gate moves)
- **Symbol/file:** `internal/persistence/store/definitions.go` — `PutDefinition` (L92-109) is
  `json.Marshal(def)` + one upsert `INSERT`, with **no** `model.Validate(def)` call anywhere;
  `GetDefinition` (L114-133) is `json.Unmarshal` with no validation either. `grep` for `Validate`
  in that file returns nothing. So a definition with a dangling flow, no start event, two manual
  starts or a never-due timer stores and reloads cleanly and only fails at execution time — which
  is how backlog **11** becomes reachable from a stored row.
  ⚠ ADR-0167's strict decoding is **syntactic** (unknown JSON fields); it says nothing about
  semantic validity.
- **Tier:** `D` — validating on write breaks writes that succeed today and interacts with the
  pre-ADR-0144 camelCase migration blocker; validating on read makes `Lookup(latest)` (the
  instance-start hot path) reject stored rows. Which end takes the gate is a real decision.
- **Fix sketch:** Call `model.Validate` in `PutDefinition` behind an opt-in
  `WithDefinitionValidation()` store option, defaulting **on** for new deployments, and add a
  `definition.Check(rows)` offline checker for existing ones (the checker the deployment blocker
  already asks for and does not have).
- **Falsifiable test:** `internal/persistence/store` test (Docker/SQLite): `PutDefinition` a
  definition whose only flow targets `"ghost"`, assert `errors.Is(err, model.ErrDanglingFlow)`.
  Fails today with `err=<nil>` and a readable row. ⚠ Docker needed for Postgres/MySQL rows; the
  SQLite leg is pure-Go.
- **Dependencies:** **11**, **35**, and the pre-ADR-0167 deployment blocker (38 camelCase keys).
- **Status:** `VERIFIED` — read both functions; no validation call exists.

---

## 28 — a weekday set mixing in/out-of-range weekdays changed answer — **EXECUTED**

- **Package(s):** `docs-only` (`CHANGELOG.md`); code in `scheduler`
- **Symbol/file:** `scheduler/trigger.go` `weeklyNext` L459-500 and `weekdayAtTime` L505-527. The
  godoc states the two consequences: *"A weekday ABOVE Saturday is not normalised into the week …
  it always matches on the first pass"* and *"an out-of-range weekday beats an in-range one that the
  anchor has already passed, even when the in-range one would fire sooner."*
- **Measured just now** (anchor Thu 2026-08-20T12:00Z, `interval=1`):

  | weekday set | next | ok |
  |---|---|---|
  | `[Monday]` | 2026-08-24 | true |
  | `[Weekday(9)]` | 2026-08-25 | true |
  | `[Monday, Weekday(9)]` | **2026-08-25** | true |
  | `[Weekday(-1)]` | zero | **false** |
  | `[Monday, Weekday(-1)]` | 2026-08-24 | true |

  Row 3 is the item: adding an out-of-range weekday to `[Monday]` moves the answer **later**
  (8/25 instead of 8/24). Row 4 is the second half — a negative weekday reports never-due for a
  spec gocron would arm and then silently never fire.
- **Tier:** `S` (docs). ⚠ Do **not** "fix" the behaviour: `weeklyNext` deliberately transcribes
  gocron v2.22.0's `weeklyJob.next`, and diverging would re-open the disagreement ADR-0176 closed.
  The correct action is a release note, plus (optionally) an authoring-time rejection under **35**.
- **Fix sketch:** Add a **Changed** entry to `CHANGELOG.md [Unreleased]` with the table above.
  `grep` confirms no existing entry mentions weekday sets — the two `Weekly` hits at
  `CHANGELOG.md:530,548` are the ADR-0136/0137 location note and the ADR-0140 interval-awareness
  note.
- **Falsifiable test:** ⚠ **vacuity-risk** for the CHANGELOG half. The behaviour half is already
  pinned by the existing `scheduler` weekday tests (`trigger_test.go`), which is why the note — not
  a code change — is the deliverable.
- **Dependencies:** **35** (authoring gate could reject out-of-range weekdays outright).
- **Status:** `VERIFIED` **by execution**.

---

## 29 — the arm guard is not atomic with the arm

- **Package(s):** `runtime`, `scheduler` (+ `scheduler/internal/gocron`)
- **Symbol/file:** `runtime/processdriver.go` L809-833, the post-commit `for _, sj := range armed`
  loop. The re-check is there and its own comment concedes the gap verbatim: *"⚠ **This narrows the
  window, it does not close it**: `activateJob` discards this instant and lets gocron re-derive from
  the trigger at its own, later reading. The residual is a few instructions wide instead of a whole
  commit."* The first reading is at `runtime/timerops.go:180` (`strig.Next(now)`, pre-transaction);
  the second is `sj.NextRun()` post-commit; the third is gocron's own, later still.
- **Tier:** `D` — closing it means the scheduler must accept a **pinned** first-fire instant instead
  of re-deriving from the trigger, which changes `scheduler.Scheduler`'s contract and interacts with
  ADR-0184's `ScheduleJob` hardening and backlog **49**/**50**.
- **Fix sketch:** Thread the already-computed `sj.NextRun()` into `activateJob` and have the gocron
  adapter arm a one-shot at that instant plus a native recurrence, instead of handing gocron the raw
  trigger. Alternatively make `Activate` fail closed when its own re-derivation is zero.
- **Falsifiable test:** `runtime` test with a fake clock: arm `Monthly(12, []int{31})` at
  2026-01-31T23:59:59.9, advance the fake clock across the month boundary between the guard and
  `Activate`. Assert either a refusal or a non-zero armed next-run. ⚠ Today the seam is not
  injectable — that is part of why this is `D`, and any test written before the seam exists is
  **vacuity-risk**.
- **Dependencies:** **24** (same refusal machinery), **49**, **50**.
- **Status:** `VERIFIED` — the residual window is stated in the production comment and the three
  separate derivations are visible in source.

---

## 30 — `weeklyNext`'s `int(interval)*7` overflows — **EXECUTED, and worse than filed**

- **Package(s):** `scheduler`
- **Symbol/file:** `scheduler/trigger.go` `weeklyNext` L495-497:

  ```go
  from := time.Date(after.Year(), after.Month(),
      after.Day()-int(after.Weekday())+int(interval)*7, 0, 0, 0, 0, after.Location())
  ```

  `interval` is `uint`; `int(interval)` wraps for values above `MaxInt64` and `*7` overflows well
  below that. The same conversion appears at `calendarNext` L419 (`bound := maxCalendarScanDays *
  int(interval)`).
- **Measured just now** (anchor Thu 2026-08-20T12:00Z, weekday set `[Monday]` so the first pass
  finds nothing and the interval-week pass runs):

  | interval | `int(interval)*7` | next | ok |
  |---|---|---|---|
  | 1 | 7 | 2026-08-24 | true |
  | 2 | 14 | 2026-08-31 | true |
  | `MaxUint32` | 30064771065 | 82316573-12-27 | true |
  | `MaxUint64/7` | **-2** | zero | false |
  | `MaxUint64` | **-7** | **2026-08-10** | **true** |

  ⚠ The last row is the real finding and it is **not** what the backlog says. It is not garbage or a
  crash: `Weekly(MaxUint64, [Monday]).Next(now)` returns a next fire **10 days in the past** with
  `ok=true`. That is a past-due arm reported as valid — precisely the class ADR-0176 exists to
  refuse, and the class backlog **49** shows gocron rejects with a raw un-wrapped error.
  Control: `Daily(MaxUint64)` returns `ok=false` (the `bound` overflow fails closed), so the two
  paths behave differently and only the weekly one produces a false positive.
- **Tier:** `S`
- **Fix sketch:** Clamp before converting, in both places:
  `if interval > maxSchedulableInterval { return time.Time{}, false }` with
  `maxSchedulableInterval` chosen so `int(interval)*7` and `maxCalendarScanDays*int(interval)`
  cannot overflow (e.g. `1 << 20`). Fail closed, matching `neverDueNextRun`'s contract.
- **Falsifiable test:** `scheduler` table test over the interval column above, asserting
  `ok == false` for every interval past the clamp and asserting `!next.Before(after)` for every
  `ok == true` row. Fails today on the `MaxUint64` row (`next` is 10 days **before** `after` with
  `ok=true`) and on the `MaxUint64/7` row if the clamp is expected to be uniform. Not vacuity-risk —
  the failing values are measured above.
- **Dependencies:** same file as **26**; do them together. **35** (an authoring gate could reject
  the interval before it reaches here).
- **Status:** `VERIFIED` **by execution** — and the backlog line understates it: the overflow yields
  a **past** next-run with `ok=true`, not merely a wrong one.

---

## 31 — three dangling ADR-section citations

- **Package(s):** `scheduler`, `engine` — comments only
- **Located, all three still present:**
  1. `scheduler/trigger.go:381` — *"…(ADR-0176 §4)"*
  2. `scheduler/trigger_test.go:554` — *"…(ADR-0176 §4)"*
  3. `engine/state_compensation.go:423` — *"…ADR-0174 §5.3's bound."*

  Verified they dangle: `grep -n "^#" docs/adr/0176-…` yields exactly `## Context`, `## Decision`,
  `## Consequences` — **no numbered sections**; `docs/adr/0174-…` yields `## Context`,
  `### Measured on main @ 02b72be`, `### Why it survived three ADRs`,
  `### ADR-0162's stale sentence`, `## Decision`, `## Consequences` — **no §5.3**.
- **Tier:** `S`
- **Fix sketch:** Replace each `§N` with the ADR's actual heading name — e.g. *"ADR-0176's
  Decision"*, *"ADR-0174's Consequences (the pre-ADR-0171 unpinned-cursor bound)"*. Prefer heading
  names over numbers for the same reason this repo prefers symbol names to line numbers.
- **Falsifiable test:** ⚠ **vacuity-risk** — comment text. A genuinely falsifiable guard is a small
  `scripts/` checker that extracts `ADR-\d{4} §[\d.]+` from `*.go` and asserts each resolves to a
  heading in the named ADR; it goes RED today on exactly these three. That is worth more than the
  edit, since backlog 1 and the audit's rotted citations are the same family.
- **Dependencies:** none.
- **Status:** `VERIFIED` — all three citations located and both ADRs' heading sets enumerated.

---

## 32 — downgrade drops new state fields

- **Package(s):** `internal/persistence/store`, `engine` (the snapshot shape)
- **Symbol/file:** `internal/persistence/store/store_core.go` — `json.Marshal(capHistory(step.State,
  s.historyCap))` at L78 and L216 (whole-struct), and `json.Unmarshal(snap, &stateOut)` at L164.
  `grep -n "DisallowUnknownFields" internal/persistence/store/*.go` → **0 hits**. So an older build
  reading a newer row drops every field it does not know, silently, with `err=<nil>`.
  ⚠ The stakes named in the backlog check out at source: `engine/state.go:224` documents the
  `IncidentCompensationStall` → resolvable-`IncidentAction` degradation explicitly, and
  `compensationCursor.RetryAttempts` is the per-record retry budget
  (`engine/step_compensation.go:565`), so dropping it restarts the budget on every reload.
  ⚠ Constraint on any fix, source-verified: `engine.InstanceState` carries **no json tags** — the
  wire format is Go field names.
- **Tier:** `D` — a snapshot versioning/compat scheme is exactly the kind of decision that needs an
  ADR, and `HANDOVER.md`'s ▶ NEXT WORK already ranks it first.
- **Fix sketch:** Add a `SchemaVersion int` to `InstanceState`, write it on every save, and refuse
  (or quarantine) a row whose version exceeds the build's — fail closed rather than silently
  truncate. Pair with json tags so the wire stops being Go field names.
- **Falsifiable test:** `internal/persistence/store` (SQLite leg, no Docker): write a snapshot
  through a struct carrying an extra field, read it back through `engine.InstanceState`, assert an
  error. Fails today with `err=<nil>` and the field gone — the exact round-trip
  `HANDOVER.md:176-183` records as already executed.
- **Dependencies:** blocks/conflicts with **3b**, **5**, **7**, **61** — every one of them changes
  the same unversioned wire. **Do 32 first.**
- **Status:** `VERIFIED` — marshal/unmarshal sites read, `DisallowUnknownFields` absent, no json
  tags on `InstanceState`.

---

## 33 — `ProcessDriver.CancelInstance` answers `err=<nil> status=terminated` on a terminal instance

- **Package(s):** `runtime` (+ `engine` if the trigger's policy is what changes)
- **Symbol/file:** `engine/trigger.go:833` — `func (CancelRequested) terminalPolicy() terminalPolicy
  { return rejectSilently }`. `rejectSilently` is defined at L31 as *"[Step] returns the state
  unchanged, with no commands and no error"*. `runtime/processdriver_cancel.go`
  `CancelInstance` (L24-47) passes that through: `st, err := driver.applyTrigger(…
  NewCancelRequested…)`, and returns `st, err` — so the caller gets the terminal state and a nil
  error.
  The service layer guards separately at `service/service.go:539-541`:
  `if isTerminal(st.Status) { return nil, fmt.Errorf("%w: instance %q is already terminal",
  ErrConflict, …) }` — a **pre-flight** status check, not a translation of a driver error.
  ⚠ The `rejectSilently` choice is deliberate and reasoned (L821-832): `propagateCancel`'s child
  loop and the signal/call-link relays fan out and must not be failed by one dead target.
- **Tier:** `D` — the fix is to split the two delivery contexts (a synchronous admin cancel vs. an
  async relay), which means either a second trigger constructor or a driver-level pre-flight check
  that duplicates `service`'s. Both are public-API decisions, and the existing policy was set by a
  rule-#9 audit that must not be silently reversed.
- **Fix sketch:** Add a driver-level pre-flight (`if st.Status.IsTerminal() { return st,
  ErrInstanceAlreadyTerminal }`) on `CancelInstance` only, leaving `propagateCancel`'s inner
  deliveries on the silent path — mirroring `service`'s shape one layer down.
- **Falsifiable test:** `runtime` test: cancel an already-terminated instance through
  `ProcessDriver.CancelInstance`, assert a non-nil error. Fails today with `err=<nil>` and
  `st.Status == StatusTerminated`. Control that keeps it honest: `propagateCancel` over a dead child
  must still not surface an error.
- **Dependencies:** must not regress ADR-0165's resurrection guard or ADR-0180's
  `ErrCancelNotApplicable` classification.
- **Status:** `VERIFIED` — the `rejectSilently` policy, the driver pass-through and the
  `service`-only guard are all read at source.

---

## 34 — `persistence` under the 85 % floor — **MEASURED**

- **Package(s):** `persistence`
- **Measured:** `go test -count=1 -coverprofile ./persistence/` → **84.1 %**, and
  `scripts/coverage.sh` on the same profile → **84.1 %** as well (no generated files in this
  package, so unlike **20** the number is real). Under the floor by 0.9 pt.
- **The item's prescription checks out.** `go tool cover -func` rows at 0.0 %:

  | file:line | symbol | coverage |
  |---|---|---|
  | `persistence/scheduler_locker.go:112` | `Lock` | **0.0 %** |
  | `persistence/scheduler_locker.go:137` | `Unlock` | **0.0 %** |
  | `persistence/scheduler_locker.go:52` | `NewPostgresSchedulerLocker` | 0.0 % |
  | `persistence/scheduler_locker.go:68` | `NewMySQLSchedulerLocker` | 0.0 % |
  | `persistence/persistence.go:501` | `NewCallNotifier` | 0.0 % |
  | `persistence/mysql.go:146…233` | eight `MySQLWith…` option setters | 0.0 % |

  The advisory lock — the multi-replica timer-exclusion primitive
  (`ErrSchedulerLockNotObtained`, `NewSchedulerLocker`) — is **entirely untested**, which is exactly
  the hot path Golang rule #8 says must be covered before the percentage is chased. The eight
  `MySQLWith…` setters are the "don't chase these" half.
- **Tier:** `S`
- **Fix sketch:** Add `persistence/scheduler_locker_test.go` covering: `Lock` succeeds on a free
  key; a **second** session's `Lock` returns `ErrSchedulerLockNotObtained`; `Unlock` releases;
  `Unlock` without a held lock. Run it on both the Postgres and MySQL legs via
  `dbtest.RunTestDatabase` / `dbtest.RunTestMySQL`.
- **Falsifiable test:** The two-session contention case is the falsifiable one — it fails today
  with `undefined` coverage (the function is never entered) and, once written, a mutation that
  drops the "not obtained" branch turns it RED. ⚠ Needs **Docker** (advisory locks are a
  Postgres/MySQL feature; SQLite has none — `persistence/sqlite.go:5,47,97` say so).
- **Dependencies:** none. ⚠ Do **not** pair with **20** — that one is a measurement artefact and
  this one is not.
- **Status:** `VERIFIED` **by execution** (84.1 % filtered, uncovered-symbol table above).

---

## 35 — ADR-0182's gate cannot judge the legacy flat trigger strings

- **Package(s):** `definition/model`, `definition/schedule`
- **Symbol/file:** `definition/model/trigger_wire.go` `ReadTrigger` L60-68 — when `w == nil` and
  `flatExpr != ""` it returns `schedule.AfterExpr(flatExpr)` / `EveryExpr(flatExpr)`.
  `definition/schedule/trigger.go` `NeverDue` L184-217 has **no case** for `KindExpr`/`KindEveryExpr`
  — they fall to `default: return false`. The gate site is
  `definition/model/validate.go:701-706` (`ErrTriggerNeverDue`).
  ⚠ It is **documented as an accepted bound**, twice: `NeverDue`'s godoc L161-173 (*"The
  engine-resolved expression forms, AfterExpr and EveryExpr, whose duration is not known until the
  expression is evaluated at run time"*) and `docs/adr/0182-…:97-103` (*"they are **not statically
  judgeable** and the arm guard remains their only layer"*).
- **Tier:** `A` — an ADR-recorded, deliberate incompleteness with a named fallback layer
  (ADR-0176's arm guard), not an open defect.
- **Escalation path if the owner wants it closed (then it is `D`, not `S`):** a flat string IS an
  `expr` expression (`engine/trigger_resolve.go` → `internal/expreval.EvalDuration`, which accepts
  a `time.Duration`, an int as seconds, a float as fractional seconds, or a `time.ParseDuration`
  string). A **constant** expression — one referencing no variables — could be compiled and
  evaluated at authoring time with an empty env and judged. That pulls an evaluator into
  `definition/model`, which today is expr-free; that dependency is the decision.
- **Falsifiable test:** if implemented — `definition/model` test: `Validate` a definition whose flat
  `timerDuration` is `"0"`, assert `errors.Is(err, ErrTriggerNeverDue)`. Fails today with
  `err=<nil>` (the spec is `KindExpr`, `NeverDue()` returns false). Falsifiable today, so if the
  owner accepts the escalation the test is not vacuous.
- **Dependencies:** **24**, **27**, **36** — all four are "where does the never-due gate live".
- **Status:** `VERIFIED` — the fall-through is at source and the bound is stated in both the godoc
  and the ADR.

---

## 36 — Cron is out of scope for the never-due gate

- **Package(s):** `definition/schedule` (would-be)
- **Symbol/file:** `definition/schedule/trigger.go` `NeverDue` — `KindCron` hits `default: return
  false`; the godoc L168-172 gives the reason: *"Both never-due cron causes — an unparseable
  expression, and a parseable one matching no instant, such as `"0 0 30 2 *"` (30 February) — can
  only be detected by parsing the expression, which would pull a cron library into the definition
  layer. **That dependency was declined**, so every KindCron spec reports false."*
- **Tier:** `A` — the backlog line itself says *"(owner decision)"*, and the ADR records the
  decision and its reason. Nothing to find.
- **Fix sketch:** Only if the owner reverses it: add a cron parser to `definition/schedule` (a new
  third-party dependency — locked stack, so it needs an ADR of its own).
- **Falsifiable test:** N/A unless reversed. If reversed: `Cron("0 0 30 2 *").NeverDue()` should be
  `true`; today it is `false`. Falsifiable, so a future test would not be vacuous.
- **Dependencies:** **35** (same gate, same accepted-bound family).
- **Status:** `VERIFIED` — an owner decision, already recorded.

---

## 37 — a compensation retry timer lost at boot still strands the walk

- **Package(s):** `runtime` (rehydration), `internal/persistence/store` (the row)
- **Symbol/file:** `internal/persistence/store/pruner.go` `PruneTimers` L195-200 states the residue
  verbatim: *"⚠ This closes the retention-job route to a stranded walk, **and only that route**. A
  retry row skipped by the runtime's job-store load at boot, or never rehydrated at all, still
  strands its walk; the escape there is ADR-0175's operator verbs. **Do not read this exclusion as
  making a lost retry timer impossible.**"* The only reconcilers in the repo are
  `RehydrateTimers`/`RehydrateStartTimers` — there is no per-walk reconciler.
- **Tier:** `A` — **subsumed, not closed.** `docs/plans/HANDOVER.md:509-510` (backlog **66**) says
  it outright: *"Backlog 37 is one instance of this class; the **class itself** is this item."* The
  class is "post-commit projections have no crash-recovery path". An escape already exists
  (ADR-0175's `retry`/`skip`/`abandon`), so this is not an unescapable wedge.
- **Fix sketch:** Do not fix in isolation. When **66** is designed, its reconciler must enumerate
  `TimerCompensationRetry` rows for instances still `StatusCompensating` and re-arm them; a
  bespoke fix here would be a sixth one-off verb of the kind **69** already criticises.
- **Falsifiable test:** Belongs to 66's bundle. A standalone one would be: durable driver, arm a
  compensation retry, drop the row from the job store, restart the driver, assert the walk still
  advances. Fails today (walk is stranded) — falsifiable, but fixing it alone builds the wrong
  thing.
- **Dependencies:** **66** (blocking), **39** (the same row's other failure mode), **69**.
- **Status:** `VERIFIED` — the residue is documented at source; the subsumption is declared by the
  handover itself.

---

## 38 — `Incidents[0]` read positionally — the remaining site

- **Package(s):** `examples/scenarios/admin_monitoring`
- **Re-counted independently** (`grep -rn "Incidents\[0\]" --include=*.go .`): **one** non-test site
  remains, and it is the one the backlog names.
  `examples/scenarios/admin_monitoring/main.go:248` — `incidentID := parked.Incidents[0].ID`, echoed
  at L250, then fed to `driver.ResolveIncident(ctx, def, instanceID, incidentID, 1)` at L257. Every
  other `Incidents[0]` hit is in a `_test.go` file. The two `runtime` resolvers are de-positionalised
  — `runtime/terminal_cause.go:17` records the change (*"The alternative it replaces was positional
  — `st.Incidents[0].Error`"*).
- **Tier:** `S`
- **Fix sketch:** Select by kind instead of by position:

  ```go
  idx := slices.IndexFunc(parked.Incidents, func(i engine.Incident) bool {
      return i.Kind == engine.IncidentAction
  })
  ```

  and error out when none is found, so the example teaches the pattern
  `service/instance.go`'s `incidentJSON.Kind` godoc already prescribes (*"⚠ Do NOT route on slice
  position"*).
- **Falsifiable test:** ⚠ **Honest caveat:** this example's own scenario raises exactly one
  `IncidentAction` and no compensation walk, so it does **not** fail as written — it is a
  *reference-wiring* defect (it teaches the pattern that breaks) rather than a live one. A
  falsifiable test therefore has to change the fixture: add a walk-scoped incident ahead of the
  action incident and assert the example still resolves. That fails today with
  `engine.ErrIncidentNotResolvable`. If you write the test **without** that fixture change, it is
  vacuous — this repo has shipped that exact mistake.
- **Dependencies:** **19**/**41** (`Park.Incidents` has the same hazard), **3d** (the release note
  that documents the `kind` field).
- **Status:** `VERIFIED` — count re-derived from source: **one** non-test site open, as claimed.

---

## 39 — a leaked `TimerCompensationRetry` row is permanent

- **Package(s):** `internal/persistence/store` (surfaced through `persistence`)
- **Symbol/file:** `internal/persistence/store/pruner.go`. Both halves confirmed at the SQL:
  - `PruneTimers` L219-228: `DELETE … WHERE next_run < ? AND trigger_kind IN (?,?,?) AND kind <> ?`
    with the last bind `int16(engine.TimerCompensationRetry)` — an **unconditional** exclusion at
    any cutoff.
  - `ReclaimNeverDueTimers` L289-300: `DELETE … WHERE next_run < epoch AND trigger_kind IN
    (7 recurring kinds)`. A compensation-retry backoff is armed with `schedule.AfterDuration`, whose
    `schedule.Kind` is `KindOneTime` — a member of `nonRecurringTriggerKinds` (L316-320) and by
    construction **disjoint** from `recurringTriggerKinds` (L323-330). So the reclaim predicate can
    never match one.

  Net: no bulk sweep in the repo can delete such a row. Intended for a live walk; unbounded
  accumulation for a row leaked by an instance that died.
- **Tier:** `D` — the fix needs a *liveness* predicate (delete the row only when its instance is
  terminal or no longer `StatusCompensating`), which means either a join across
  `wrkflw_timers`/`wrkflw_instances` or a new exported `Pruner` verb. Both change the retention
  contract of an exported API, on three dialects.
- **Fix sketch:** Add `Pruner.ReclaimOrphanedCompensationRetryTimers(ctx)` deleting
  `kind = TimerCompensationRetry` rows whose `instance_id` names an instance in a terminal status —
  a **third disjoint predicate**, following the precedent `ReclaimNeverDueTimers` set (its own doc
  explicitly refuses to widen `PruneTimers`' IN-list for the same reason).
- **Falsifiable test:** `internal/persistence/store` (SQLite leg is pure-Go): write a
  `TimerCompensationRetry` row for a terminated instance, run `PruneTimers` with a far-future cutoff
  **and** `ReclaimNeverDueTimers`, assert the row is gone. Fails today: both return 0 and the row
  survives. Not vacuity-risk — the two exclusions above are why.
- **Dependencies:** **37** (same row, other failure mode), **66**.
- **Status:** `VERIFIED` — both SQL predicates read, and the kind-list disjointness confirmed at
  `nonRecurringTriggerKinds` / `recurringTriggerKinds`.

---

## 40 — `engine.NewActionFailed` gives a consumer a ZERO retry backoff

- **Package(s):** `engine`
- **Symbol/file:** `engine/step_triggers.go:472` —
  `delay := time.Duration(t.JitterFraction * float64(eff.Backoff(attempt)))`. With
  `JitterFraction == 0` the product is **0**, so the emitted command is
  `ScheduleTimer{Trigger: schedule.AfterDuration(0), Kind: TimerRetry}` — an immediate retry, no
  backoff at all. `NewActionFailed` (`engine/trigger.go:236`) leaves the field at its zero value;
  `WithJitter` is opt-in.
  The shipped runtime is fine and does it deliberately: `runtime/processdriver_action.go:414` passes
  `engine.WithJitter(driver.jitter.Fraction())` — full-jitter, correct by design.
  ⚠ The doc bug is real and independently confirmable: `engine/trigger.go:165` says *"Zero means no
  jitter (the default when constructed via NewActionFailed)"* — **false**; zero means zero *delay*.
  `engine/step_compensation.go:572-575` already contains the correct reading: *"ActionFailed.
  JitterFraction defaults to ZERO, so that expression yields a zero delay unless the runtime samples
  a fraction — which would make the default compensation backoff instantaneous, i.e. not a backoff
  at all."* — i.e. the compensation path deliberately does NOT use this formula.
- **Tier:** `S`
- **Fix sketch:** Make the zero value mean what the doc says, in ~4 lines:

  ```go
  delay := eff.Backoff(attempt)
  if t.JitterFraction > 0 {
      delay = time.Duration(t.JitterFraction * float64(delay))
  }
  ```

  plus correct the `JitterFraction` godoc at `engine/trigger.go:163-166`. The runtime path is
  unchanged for any sampled fraction > 0.
- **Falsifiable test:** `engine` test: a node with a retry policy whose `InitialInterval` is 1 s,
  deliver `engine.NewActionFailed(at, cmdID, "boom", true)` with **no** `WithJitter`, assert the
  emitted `ScheduleTimer.Trigger` equals `schedule.AfterDuration(time.Second)`. Fails today with
  `AfterDuration(0)`. Control row so the change is not a regression: the same case **with**
  `WithJitter(0.5)` must still yield `500 ms`.
- **Dependencies:** none blocking. ⚠ Behaviour note for the review: a runtime jitter source that
  samples exactly `0.0` would, after this fix, produce a full (unjittered) backoff instead of an
  immediate retry — strictly safer, but it is a behaviour delta and belongs in the commit message.
- **Status:** `VERIFIED` — formula, default and the false godoc sentence all read at source; the
  correct reading already exists one file away.

---

## Method notes

- Everything above was located in source; the tables marked **EXECUTED** (**20**, **26**, **28**,
  **30**, **34**) were produced by running code, not by reading it.
- Two throwaway probes were written into `scheduler/` and **deleted**; `git status --porcelain`
  afterwards showed only this evidence directory as untracked.
- ⚠ Disclosure: the `persistence` coverage run (`go test ./persistence/`) exercised the package's
  testcontainers suite against an **already-running** Docker daemon. No container was started
  deliberately for triage beyond that one command, and nothing was mutated.
