# 21. Open-visit-preserving history cap at the persistence boundary

- Status: Accepted
- Date: 2026-06-22

## Context

The instance snapshot is stored as one JSONB document (ADR-0006). It includes
`InstanceState.History` (`[]NodeVisit`), which grows on every node entry
(`openVisit`). A long-running or looping instance accumulates unbounded inline
history, bloating its row and amplifying the per-transition TOAST rewrite (spec §4,
Persistence follow-up #2). The `wrkflw_journal` table is the unbounded audit source;
the inline history is a convenience copy that can be bounded — but only if doing so
does not change engine behavior.

The hazard is specific: `engine.Step` **reads** `History` in two places,
`setVisitActor` (`engine/step.go:1390`) and `closeVisit` (`engine/step.go:1427`),
and **both match only _open_ visits** (`NodeVisit.LeftAt == nil`). A visit closes
(`LeftAt` set) when its token leaves the node; **closed visits are never read again**
— they are pure audit. A naive "keep the last N entries" cap is therefore unsafe: it
can drop an old-but-open visit (e.g. a human-task token parked for days whose open
visit sits behind many short closed visits), turning the eventual `closeVisit` into a
no-op (dangling-open visit, lost `LeftAt`) and losing the `ActorID` record.

## Decision

Cap inline history with an **open-visit-preserving** projection applied at the
**persistence marshal boundary**, opt-in and unbounded by default.

- `capHistory(st engine.InstanceState, n int) engine.InstanceState` returns a copy
  whose `History` retains **every open visit** (`LeftAt == nil`) plus at most the
  **most recent N closed visits**, preserving relative order. `n <= 0` means "no
  cap" and returns the state unchanged.
- It is applied in `internal/persistence/postgres/store.go` to a copy of the state
  immediately before `json.Marshal(step.State)` in both `Create` and `Commit`. The
  pure `engine`/`runtime` layers are **untouched** — the core never learns about
  storage caps.
- Opt-in via `persistence.OpenPostgres(..., WithHistoryCap(n))`; unset ⇒ `n <= 0` ⇒
  exact current behavior, no surprise audit-data loss for existing consumers.

Because the engine only ever matches open visits — which are never dropped — the
reloaded, capped state drives identical decisions to the uncapped state. The cap is
provably **behavior-preserving**; the journal remains the complete audit record, so
nothing recoverable is lost.

## Consequences

**Easier:** snapshot growth from accumulated closed visits is bounded, shrinking the
hot per-transition row rewrite and TOAST amplification for long/looping instances —
the largest single contributor to snapshot bloat. The mechanism is one pure helper at
the storage edge; no engine, runtime, or schema change. Correctness is argued from the
engine's actual read sites, not assumed.

**Harder / trade-offs:** an external consumer reading inline history *off the snapshot*
(rather than the journal) sees only retained visits when a cap is set — documented in
the `WithHistoryCap` godoc; the journal is the complete source for full audit. The
in-memory `st` held across the `deliverLoop` keeps full history while only the persisted
snapshot is capped; the two converge on the capped form after the next reload (still
correct, since only closed visits were dropped). The cap is a global per-Store setting
in v1, not per-definition.
