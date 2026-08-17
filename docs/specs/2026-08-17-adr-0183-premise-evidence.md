# Blocker 3 — executed evidence (2026-08-17)

Probe: throwaway `TestZZProbeBlocker3` in `internal/persistence/store`, SQLite via
`dbtest.RunTestSQLite` (pure-Go, no container). EXIT=0, three cases, then deleted.

## Measured — what `HumanTaskStore.Upsert` accepts and reads back

| seeded | Upsert err | read back | inbox queries |
|---|---|---|---|
| `State: Claimed, Claim: nil` | `<nil>` | `state=claimed claim=<nil>` | AssignedTo=0, ClaimableBy=0 |
| `State: Completed, Completion: nil, Claim: nil` | `<nil>` | `state=completed completion=<nil>` | AssignedTo=0, ClaimableBy=0 |
| `State: Unclaimed` **with** `Claim{alice}` | `<nil>` | `state=unclaimed claim=&{alice…}` | **AssignedTo(alice)=1**, ClaimableBy=0 |

Conclusions:
1. Blocker 3 CONFIRMED — the violation round-trips intact.
2. The **completion axis** has the identical gap (same class, second field).
3. **The inverse direction is unguarded and has a worse consequence**: an
   `Unclaimed` row carrying a claim is returned by `AssignedTo` (alice's "my
   tasks") *and* excluded from `ClaimableBy`, so it is simultaneously
   "nobody's" and "in alice's inbox, unclaimable by anyone". The handover
   frames only the `Claimed`/`nil` direction.

## Why the read path cannot repair direction 1

`scanTask` (`humantask_store.go` ~line 415) rebuilds a `Claim` only when
`claimed_at` is **non-NULL** (parseable or not — non-NULL is the discriminator).
Direction 1 writes `claimed_at = NULL`, which is the legitimate "never claimed"
encoding, so the read path has nothing to key on. Enforcement must be on write.

## Writer enumeration (re-derived, not inherited)

- `State = Claimed`: **2** sites — `engine/step_triggers.go:578` (claim), `:634`
  (reassign). **Both set `task.Claim` on the immediately preceding line.**
- `State = Completed`: **2** sites — `engine/step_triggers.go:928` (sets
  `Completion`), `engine/step_nodes.go:755` (**sets NEITHER Claim NOR
  Completion** — the `Manual && ManualImmediate` path).
- `State = Unclaimed`: **2** sites — `runtime/processdriver_action.go:439`,
  `engine/step_nodes.go:733`; both at creation, no Claim.
- `State = Cancelled`: **4** sites — `engine/step_timers.go:89`,
  `step_cancel.go:39`, `state.go:649`, `step_stale_commands.go:170`.

⇒ **No live engine path sends an inconsistent shape to a store.** Blocker 3 is a
**public-API contract gap**, not a live engine bug. Severity rests on
`humantask.TaskStore` being a public interface consumers both call and implement.

⚠ `engine/step_nodes.go:755` mints `State: Completed` with nil Claim *and* nil
Completion in **instance state**, and its comment claims it "mirrors the state
handleHumanCompleted sets" — it does not. It emits no `UpdateTask` and no
`AwaitHuman`, so it appears never to reach the task store. NOT yet executed —
`ASSUMPTION (unverified)`.

## Blast radius — TaskStore implementations

Three in production, **none** validates:
- `humantask.MemTaskStore.Upsert` (`humantask/memory.go:33`) — `copyTask`, store, nil.
- `store.HumanTaskStore.Upsert` (`internal/persistence/store/humantask_store.go:131`).
- `persistence.CachingTaskStore.Upsert` (`persistence/caching_task_store.go:98`) —
  delegates, then **caches the bad value** via `codec.Set`.
Plus a test double in `runtime/processdriver_action_test.go`, plus any consumer's own.

`internal/persistence/store/humantask_store_conformance_test.go` is a
**dialect** conformance suite over `HumanTaskStore` only — there is no
cross-implementation suite today.

## Convention notes

- `humantask` has exactly one sentinel: `ErrTaskNotFound`.
- No `Validate() error` **method** convention in root packages; the precedent is a
  **package-level function**, `model.Validate(d *ProcessDefinition) error`
  (`definition/model/validate.go:276`), called from `builder.go:133`.
- Both `Upsert` call sites live in `runtime/processdriver_action.go:468` (create)
  and `:483` (`performUpdateTask`).

## Fixture-churn measurement (2026-08-17) — the strict rule breaks ZERO tests

Method: throwaway strict guard patched into `MemTaskStore.Upsert` and
`HumanTaskStore.Upsert` (both claim directions), `cp` backups taken, then
restored (`git diff --stat` empty afterwards).

- `go build ./...` → EXIT=0.
- `go test -count=1 ./engine/... ./runtime/... ./service/... ./processtest/...
  ./transport/http/... ./humantask/... ./persistence/...` → **EXIT=0, 0 FAIL,
  26 packages ok**.
- `go test -count=1 -v -run '.*/sqlite' ./internal/persistence/store/` →
  **EXIT=0, 280 subtests ran, 0 FAIL, 0 probe hits**.

⚠ POSITIVE CONTROL (this zero would otherwise prove nothing): a deliberate
`Upsert(State: Claimed, Claim: nil)` in that same package returned
`PROBE: claimed requires claim: ctl-1`. **The guard demonstrably fires**, so the
zero is a measurement and not a dead assertion.

Coverage of the claim: the packages whose tests call `.Upsert(` are exactly
**seven** — `humantask`, `internal/persistence/store`, `persistence`,
`processtest`, `runtime`, `runtime/task`, `service` — and **all seven ran**.

Scope limit, stated honestly: the store package's `postgres` and `mysql`
subtests did NOT run (no Docker probe). They execute the *same Go fixture
values* as the `sqlite` subtest, and the guard is pre-SQL, so a fixture-shape
violation would surface identically on sqlite. Dialect-specific behaviour is
irrelevant to a validation guard that runs before any SQL is built.

⇒ The 35 `State: Claimed` test fixtures are not churn: the ones reaching a store
all set `Claim`. E.g. `humantask_store_conformance_test.go:539` `base()` is bare
`Claimed`, but every case assigns `task.Claim` before upserting.

## ⚠ CORRECTION during spec self-review — the first ClaimableBy zero was confounded

Probe 1 reported `ClaimableBy(alice)=0` for the `Unclaimed`+claim row. That zero
did **not** measure the claim: the fixture declared no `Candidates` and no
`Eligibility.Roles`, so nothing could match regardless of state. Re-probed with
an eligible actor (`Eligibility.Roles: ["mgr"]`, `Candidates: ["alice"]`, actor
`alice` holding `mgr`):

```
PROBE2 eligible actor: AssignedTo(alice)=1 ClaimableBy(alice)=1
PROBE2 claimable row: id=t-x state=unclaimed claim=&{{alice [] map[]} 2026-08-17 10:00:00 +0000 UTC}
```

⇒ The row is **double-listed**: alice both holds it and may still claim it. The
defect is a double-listing, not an omission. The `Claimed`+nil direction's
`ClaimableBy=0` remains correctly explained — `ClaimableBy` restricts to
`state = 'unclaimed'`.

**Lesson for the prescribed regression test**: a fixture that declares no
eligibility cannot fail on the `ClaimableBy` axis. This is the repo's recurring
vacuous-fixture failure — check the fixture, not the assertion line.

## RE-MEASUREMENT (2026-08-18) — churn for the THREE-RULE guard, after the re-audit

Re-audit lens E measured that the rewrite's "zero fixture churn" was **FALSE for the
revised guard**: the number had been measured against the *two-rule* guard, then
restated for a guard that had since gained R1's empty-claimant clause and R3.
Confirmed — `humantask/memory_test.go`'s `claimedByNobody` fixture seeds
`Claimed` + `Claim{Actor{ID: ""}}` through `Upsert` and would fail.

That clause was then **dropped** (see the adjudication addendum: it reverses
ADR-0148 amendment 1 §4's deliberately-blessed kiosk claimant, and would delete a
data-disclosure regression test). Churn re-measured for the guard as it now
stands — R1 (nil claim only), R2, R3:

- `go build ./...` → EXIT=0
- `./humantask/... ./processtest/... ./service/... ./runtime/task/... ./persistence/...`
  → **EXIT=0, 0 FAIL**, 11 packages ok
- `./internal/persistence/store/ ./runtime/...` → **EXIT=0, 0 FAIL** — and this time
  **`docker info` was probed and reported `up`**, so the Postgres and MySQL legs
  genuinely ran (store package 49.4s). This repairs the earlier mislabelled
  "no Docker probe" claim (audit A4/C4/C5).

⇒ All **seven** `Upsert`-calling packages ran, unfiltered, with containers live.
**Churn is genuinely zero for the three-rule guard.**

⚠ POSITIVE CONTROL — four axes, one of them the new one:

```
c1 Claimed + nil claim        -> rejected  ✓
c2 State(99) out of range     -> rejected  ✓   (R3, the bypass C1 found)
c3 Unclaimed + claim          -> rejected  ✓
c4 Claimed + EMPTY claimant   -> ACCEPTED  ✓   (ADR-0148 kiosk shape stays legal)
```

Row c4 is the discriminating one: it proves dropping the empty-claimant clause
preserves the blessed shape while c1–c3 still close blocker 3.

⚠ Still unmeasured: the **pre-commit hook's** own churn and `Step`'s reassignment
guard. Lens E re-derived `Step`'s independently as zero — `NewHumanReassigned` has
16 call sites, none passing an empty target — but the pre-commit hook was not
patched in for this run. `UNVERIFIED`.
