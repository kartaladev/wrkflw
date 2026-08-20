# `/code-review` finding 6 (LOW) — `compensationRecordsForScope`'s advisory `ok`

Agent: engine review-fix. Working tree `main` checkout on `feat/backlog-sweep-small-tier`,
no branch, no commit (controller folds via `--amend`). Date 2026-08-20.

Verification command — **exit code, never a pipeline** (Common Pitfall #4):

```
go test -count=1 ./engine/... > /tmp/eng.log 2>&1; echo "EXIT=$?"
```

---

## The finding

`engine/step_compensation.go:23` — `compensationRecordsForScope` gained a second `ok`
return in this commit, and its doc justified it by a hazard ("a caller keying on
`len(records)` treats a vanished scope as an empty one") that **no caller is exposed
to**, because all four production sites discard the flag with `_`. The doc reads as if
a defect were closed; the change is behaviourally inert.

## Decision — **doc-only. `ok` is advisory and the comment now says so.**

No call site was found where ignoring `ok` is wrong, so nothing was wired up. Using the
flag anywhere would have been manufacturing a use: at every site the two answers the
flag distinguishes lead to the **same, correct** outcome, and at one of them the flag's
`false` case is unreachable entirely. An untestable or outcome-neutral branch with a
test that cannot fail is strictly worse than an honest doc comment — the same trade the
item-10 agent already made once, correctly.

`engine/step_compensation.go` is the only file changed (comments only). Engine suite
`EXIT=0`; `golangci-lint run ./engine/...` → `0 issues`; engine coverage **93.1 %**,
unchanged (no statements added or removed).

---

## Re-derivations (nothing below is inherited — every line was executed or re-grepped)

### Call sites — re-counted, not inherited

```
grep -rn "compensationRecordsForScope" engine/
```

**4 production sites** (+2 mentions in `step_compensation_closed_scope_test.go`):

| site | scope argument | gated by `defForScope`? |
|---|---|---|
| `step_compensation.go:52` `cursorRecords` | cursor's `ScopeID` | **no** |
| `step_compensation.go:345` `beginCompensation` | `const scopeID = ""` (line 343) | n/a — root |
| `step_compensation.go:912` `retainedRecordPrefix` | cursor's `ScopeID` (via caller) | **no** |
| `step_nodes.go:1230` compensation throw | token's `ScopeID` | **yes** |

The count matches the review's. `beginCompensation`'s `scopeID` is verified to be the
const throughout the function — `grep -n "scopeID" engine/step_compensation.go` inside
lines 317–440 yields exactly three uses: the `const`, the helper call, and
`cur.ScopeID = scopeID` at 413. So a `beginCompensation` cursor's `ScopeID` is always
`""`.

### ⚠ Enumeration rot found IN the doc comment being fixed

The old comment said `s.Scopes` entries vanish because "closeScope prunes the entry and
endInstance nils the slice" — **two** sites. Re-derived:

```
grep -n "s.Scopes = " engine/*.go | grep -v _test
→ state_compensation.go:353 (openScope, append)
  state_compensation.go:670 (closeScope)
  state_compensation.go:688 (closeScopeDescendants)   ← omitted by the old comment
  state.go:635              (endInstance, = nil)
  step_state.go:438         (rehydration)
```

**Three** removal sites, not two. Corrected in the new comment by naming the closed set.

### Probe (throwaway `engine/zz_probe_ok_test.go`, `package engine`, run then deleted)

`go test -count=1 -v -run 'TestZZProbe' ./engine/` → `EXIT=0`, output verbatim:

```
P1 root-with-no-scopes: records=[] ok=true
P1 open-empty-scope:   records=[] ok=true
P1 closed-scope:       records=[] ok=false
P2 cursorRecords(open-empty)=[] (nil=true)
P2 cursorRecords(closed)   =[] (nil=true)
P5 retainedRecordPrefix(open-empty,n=1)=[] (nil=true)
P5 retainedRecordPrefix(closed,n=1)   =[] (nil=true)
P5b setScopeRecords(closed,"s1",1 rec) -> Scopes=[{ID:other NodeID: ParentID: Compensations:[]}] RootCompensations=[]
P3 scope OPEN and empty   ok=true  err=<nil> status=completed activeCmd="" cursorScope="" cmds=1
P3 scope OPEN and empty   RETRY err=<nil> status=completed activeCmd="" cmds=1
P3 scope ABSENT (closed)  ok=false err=<nil> status=completed activeCmd="" cursorScope="" cmds=1
P3 scope ABSENT (closed)  RETRY err=<nil> status=completed activeCmd="" cmds=1
P3 DEEP-EQUAL advance: state=true cmds=true
P3 DEEP-EQUAL retry:   state=true cmds=true
P3 advance cmds: []engine.Command{engine.CompleteInstance{Result:map[string]interface {}(nil)}}
P4 throw-with-closed-scope: err=workflow-engine: defForScope: unknown scope "gone" cmds=0 tokenNode="boom"
P4 control open scope:      err=<nil> cmds=0 tokens=1
P4   token[0] node="after" state=1
```

What each line establishes:

- **P1** — the helper really does answer differently for the two states, and `""` is
  resolvable even with `s.Scopes` empty (so site 345 is safe by construction).
- **P2** — `cursorRecords` hands both callers the *same* `nil` either way. The flag's
  information is destroyed at the helper's boundary, by design.
- **P3 — the load-bearing measurement.** Two `InstanceState`s identical except that one
  cursor's scope is **open-and-empty** and the other's is **absent**, both driven
  through the two `cursorRecords` consumers that index records
  (`stepCompensationAdvance` via `ActionCompleted`, `retryStalledCompensation` via
  `RetryStalledCompensation`). Results are `reflect.DeepEqual` in **both** the returned
  `InstanceState` (with the deliberately-differing `Scopes` field zeroed) **and** the
  command slice. Both route to the walk's finish and emit `CompleteInstance` — which is
  precisely what ADR-0171's third disjunct prescribes for a vanished source, and is
  trivially right for an open empty one. **Honouring `ok` here cannot change an
  outcome.**
- **P4 — the gate, re-derived independently of the existing pin.** A token with
  `ScopeID: "gone"` at a compensation throw fails the Step with
  `workflow-engine: defForScope: unknown scope "gone"` before any strategy runs; the
  token stays on `boom`. The **control** — the same definition and token in the root
  scope, which is always resolvable — reaches the strategy and auto-advances the token
  to `after`, proving the fixture really drives the throw and the error above is the
  gate, not a broken fixture. (My first control used an open scope whose `NodeID` was
  the throw node; it failed with `node "boom" has no Subprocess definition`, i.e. it was
  a broken fixture. Recorded because a control that fails for the wrong reason proves
  nothing.)
- **P5 / P5b** — `retainedRecordPrefix` retains nothing for either answer, and the
  write-back path (`setScopeRecords`) is itself a no-op for a scope that is gone: after
  `setScopeRecords(closed, "s1", …)`, `Scopes` is untouched. So there is no lost-work
  hazard behind the discarded flag there either.

### The four consumers of an empty `cursorRecords` result — enumerated, not assumed

`grep -rn "cursorRecords" engine/ | grep -v _test`, keeping only the **calls** (the
other hits are the definition at `step_compensation.go:43` and six doc comments):

```
step_compensation.go:667   stepCompensationAdvance
step_compensation.go:1435  retryStalledCompensation
step_compensation.go:1545  retryFailedCompensation
step_triggers.go:394       failedNodeID lookup (ADR-0179 Decision 1)
```

Three index-and-bounds consumers, all routing an out-of-range index to
`stepCompensationFinish`, plus `step_triggers.go`'s lookup which leaves `failedNodeID`
empty. None of the four can distinguish "open and empty" from "gone" *after* the helper
returns, and none would want to: all four want "there is nothing here".

### Why a live build almost certainly cannot even produce `ok == false` here

Writers of a cursor with a **non-empty** `ScopeID` (source-derived, `grep -rn
"compensationCursor{" engine/*.go | grep -v _test` plus the two field assignments):

- `beginCompensation` (`step_compensation.go:413`) — `""`.
- `step_nodes.go:1188` — `{ArchiveKey: ref}`, `ScopeID` empty.
- `step_nodes.go:1245` — `{ScopeID: tokScope, …}`, handed to `startCompensationWalk`,
  which at `step_nodes.go:1134` sets `cursor.Records = cloneCompensationRecords(records)`
  from a slice the caller has already established is non-empty. Pinned ⇒ `cursorRecords`
  short-circuits on `cur.Records != nil` and never reaches the live read.

So in this build an unpinned cursor with a non-empty `ScopeID` arises only from a row
**deserialized from before ADR-0171** — the case
`step_compensation_cursor_migration_test.go` builds by hand, stating in its own header
that the state "cannot be produced by driving the engine any more". This is a
source-derived enumeration, not an exhaustive execution:
`ASSUMPTION (partially verified): no live path produces an unpinned non-empty-ScopeID
cursor.` It is **not load-bearing** for the decision — P3 shows the outcome is identical
whether or not the state is reachable.

---

## Backlog 133 — **SHARPENED, not closed**

Filed as: *"`cursorRecords` / `retainedRecordPrefix` are not gated by `defForScope`, so
the closed-scope conflation refuted for item 10 may be genuinely reachable there.
Unproven."*

What is now settled:

- ✅ **The "not gated" half is CONFIRMED** — both take their scope id from the cursor,
  not from a token, so `drive()`'s `defForScope` never sees it. (The gate itself is
  re-confirmed for the *throw* site by P4.)
- ✅ **The "genuinely reachable" half no longer matters for correctness.** P3 measures
  the conflation as **outcome-neutral**: the closed-scope and open-empty states produce
  DeepEqual `StepResult`s through both entry points, and the outcome is the one ADR-0171
  designs for. There is no correctness defect to reach.
- ⚠ **Reachability itself remains unproven** in the strict sense (see the ASSUMPTION
  above) — the state is constructible only from a pre-ADR-0171 persisted row on this
  build.

What 133 should be re-filed as, if kept: **a VISIBILITY question, not a correctness
one.** *"A compensation walk whose record source has vanished finishes silently, leaving
its remaining records uncompensated with no operator signal — should `ok == false` at
`cursorRecords` raise a WARN or an incident, in the ADR-0179 'make the silent skip
visible' style?"* That is a deliberate design question with a real cost on both sides
(it fires only for rolling-upgrade cursors), it is reachable and testable via a
hand-built state — unlike item 10's refuted WARN — and it is **out of scope for a LOW
doc finding folded by `--amend`**. Recommend re-titling 133 to that and leaving it open;
the new doc comment points at it by number so the question does not get lost.

---

## Verification actually run

| check | result |
|---|---|
| `go test -count=1 ./engine/...` | `EXIT=0`, `ok github.com/kartaladev/wrkflw/engine 0.637s` |
| `golangci-lint run ./engine/...` (probed on PATH) | `EXIT=0`, `0 issues` — ⚠ package-scoped, **not** the repo-wide gate |
| engine coverage (`go tool cover -func`) | **93.1 %**, unchanged — the change adds no statements |
| working tree | `M engine/step_compensation.go` only; the probe file was `rm`-ed, not left behind |

No Docker was started (engine is container-free). No mutation was performed, so no `cp`
backup was needed — the change is comments only.
