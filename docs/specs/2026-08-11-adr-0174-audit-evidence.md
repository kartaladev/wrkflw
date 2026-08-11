# ADR-0174 — rule-#9 audit evidence

Companion to
[`2026-08-11-dying-instance-harvests-open-scopes.md`](2026-08-11-dying-instance-harvests-open-scopes.md)
(§9 carries the adjudication) and
[ADR-0174](../adr/0174-a-dying-instance-harvests-its-open-scopes.md).

Three Opus auditors, each in its own `git worktree` at the pre-implementation bundle
commit `be9d714`, each briefed to **attack and to EXECUTE** rather than read. Lenses:
**A** scope lifetime / token-scope referential integrity, **B** compensation record
ownership and the ADR-0173 interaction, **C** premise truth / enumeration rot /
cross-document consistency / test falsifiability.

**33 findings: 1 Critical, 8 High, 1 Medium-High, 6 Medium, the rest Low.** The
design changed in **four** ways, one of them a whole decision deleted.

⚠ Verbatim tool output below is reproduced from the auditors' reports; the
surrounding prose is a summary, not a transcript. This file exists in-repo because
the previous delivery's auditor write-ups lived only in their worktrees and **died
at merge**, leaving dangling citations in a shipped spec (repaired in `02b72be`).

---

## Findings that CHANGED the design

### 1. CRITICAL (B3) — legacy-row recovery re-runs already-compensated actions

Two lenses reached the same place from opposite directions (B3 by serialization,
A4/B4 by the resuming-walk path). The bundle's Decision 4 harvested inside
`beginCompensation` so that instances **already** terminated with a zombie scope
became recoverable. Measured: a `main`-written row whose open scope belonged to an
**abandoned** walk re-dispatches everything that walk had already run.

Fixture: sub-process `sub` running a 3-record saga `[undoA undoB undoC]`; a
scope-wide throw walk dispatches `undoC` then `undoB`; a sibling force-terminates.

```
mode = "none"  (main writes the row, new build reads it)
LENSB after force-termination: status=terminated tokens=0 scopes=1 root=0 archived=map[]
LENSB   scope id="lb1-s1" node="sub" comps=[undoA undoB undoC]
LENSB rollback err=<nil>
LENSB rollback re-dispatched=[undoC undoB undoA]      ← undoB + undoC DOUBLE-RUN
```

Only `[undoA]` was owed. `main` refuses the rollback outright, so this is a
**regression against `main`** for that shape. The mechanism is not fixable: `main`'s
`endInstance` zeroed the cursor, so the row carries **no record of what was
dispatched** and is indistinguishable from a never-walked row.

**Adjudicated: ACCEPTED — Decision 4 DELETED** (owner's call, offered three ways).
Both halves go together: counting open scopes in `hasCompensationRecordsToWalk`
without harvesting would admit a walk that then finds nothing and re-stamps the
terminal transition for zero benefit — exactly what ADR-0165's guard exists to
prevent. Deleting it also moots findings A4/B4 and B5 entirely.

The bundle's sentence *"no data migration, in either direction — and unlike ADR-0173
that holds **both** ways"* was its most confident claim and was **wrong**. Deleted.

### 2. HIGH (B1 + A1, independent) — `endInstance` IS reachable with a live cursor

The bundle's M5 instrumented `endInstance` only `whenever len(s.Scopes) > 0`. That
filter is what made the claim look true.

```
unconditional instrumentation: 226 entries (M5 saw 4), FIVE with a live cursor,
all from step_nodes.go:634 (forceTerminate):

PROBE_ENDINSTANCE caller=step_nodes.go:634 status=compensating scopes=0 \
  activeCmd="ab-c6" curScope="ab-s1" curRecords=3 startCount=3 nextIdx=1 root=0 archived=1
```

`forceTerminate` clears no cursor and closes no scope. So *"`activeCmd=""` at every
entry"* is true of the **suite** and false of the **engine** — and M5(b) already
conceded the suite has zero coverage of the record-holding shape.

**Adjudicated: ACCEPTED.** M5(a) restated as scoped to `len(s.Scopes) > 0`; the five
`forceTerminate` lines added as M5(c).

### 3. HIGH (B2 + A2, independent) — the two harvest orderings are DISTINGUISHABLE today

Decision 3 (harvest **before** the cursor clear) is **right**, but the bundle's
justification — *"the two orderings are indistinguishable today; this is the
defensive order"* — was false. Both orderings measured on the fixture above:

```
"before" (the proposal):
  archived=map[sub:[undoA]]                    rollback re-dispatched=[undoA]         ✅
"after"  (the rejected ordering):
  archived=map[sub:[undoA undoB undoC]]        rollback re-dispatched=[undoC undoB undoA]  ❌
```

The live-cursor caller is not hypothetical and not future: it is `forceTerminate`,
today, on the route ADR-0173's own abandonment tests use.

**Adjudicated: ACCEPTED.** Decision 3 kept, its "indistinguishable today"
justification deleted and replaced with these numbers.

### 4. MEDIUM-HIGH (A5) — the harvest retires incidents, changing an emitted event payload

Found by one lens only, and by nobody else. On the shape ADR-0164 Decision 3
deliberately preserves (an unhandled error whose token survives, carrying an
incident), the harvest makes the predicate true, so `beginCompensation` runs and its
prologue cancels every token — after which `removeOrphanedIncidents` retires the
incident.

```
main:    status=failed tokens=2 incidents=1
patched: status=failed tokens=0 incidents=0
```

`runtime/outbox.go`'s `terminalEventErr` prefers `Incidents[0].Error`, so the
`instance.failed` event payload and `incident_count` both change.

**Adjudicated: ACCEPTED as an intended consequence, previously UNDOCUMENTED.** It is
inherent to the fix — "compensate before terminating" necessarily cancels the tokens
— but it crosses a package boundary into an emitted event, so it belongs in §6 and in
the ADR's Consequences, with these numbers.

---

## The structural claim: refuted as worded, confirmed in conclusion (A3)

Lens C proposed, and the controller asked lens A to verify **independently rather
than accept**, this claim:

> On a terminal instance exactly one trigger reaches a handler, it walks the root
> scope, and therefore no route can resolve a token's scope def — so clearing
> `s.Scopes` cannot wedge a terminal instance.

Lens A re-derived all 15 `terminalPolicy()` values (**exactly one** admitted trigger
— confirmed), traced the admitted path to `applyTerminate` with no `drive`
(confirmed), then **mutated `defForScope` to panic** and swept 20 triggers over three
cleared-`Scopes` terminal fixtures carrying surviving in-scope tokens: **zero
reaches**.

But with exported readers enabled it panics through **`engine.FailingActionName`**
(`runtime/processdriver_action.go:201`), and **`engine.TargetNode`**
(`runtime/processdriver.go:814`) is a second — the latter running *before* `Step`.
Both fail soft to `ok=false`, so **there is no wedge**.

**Adjudicated: the conclusion is used, the absolute sentence is NOT.** The ADR says
no route *wedges*, names the two soft readers, and does not claim no route resolves a
scope. An in-`Step` enumeration structurally cannot see that class of entry point —
which is the point worth carrying forward.

⚠ This is why the controller sent the claim to a second lens instead of folding it
in. A sibling's structural argument is a lead, not a verdict.

---

## Prescribed tests that COULD NOT FAIL

Four of eleven. This repo shipped six such tests in one delivery and caught three
more in one audit; this is the recurring defect class.

| Test | Why it cannot fail | Fix |
|---|---|---|
| **T10** (B2 + A7, independent) | Its own path is `stepCompensationFinish → applyTerminate`, and the cursor is zeroed at `step_compensation.go:709` one call up. Both orderings compute an **identical** result — verbatim ADR-0173's "recomputes identically" defect | Re-fixture on `forceTerminate` (finding 3's fixture), advanced ≥1 step so the **offset** moves |
| **T11** (C7) | On a terminal instance only one trigger reaches a handler and it never resolves a token scope, so "stays non-wedging" cannot fail | Replaced with the *structural* assertion: the only `allowOnTerminal` trigger is a plain full rollback — breaks if a future trigger is flipped |
| **T9** (A7) | Its stated falsifier is not one: both in-flight guards read only the cursor, so harvest placement above or below them changes nothing they observe | Re-state the falsifier as the *record set* the deferral leaves behind |
| **T5** (C3) | Unsatisfiable as prescribed. Holds on **one** of the three routes; on the other two the post-fix instance is `compensating`, not terminal — mid-walk the rollback is swallowed by the in-flight guard (0 commands), post-walk it is refused because records were consumed | Restrict to the `endInstance` routes and say so |

```
C3, patched tree:
M1 (force-term):      rollback err=<nil> cmds=1 invoked=[undo-inner]   ← WALKS
M2 (unhandled error): status=COMPENSATING (not terminal)
   rollback err=<nil> cmds=0 invoked=[]                                 ← accepted, dispatches NOTHING
   driven to finish → post-walk rollback: REFUSED "nothing left to compensate"
```

Also **T7** (C5) was unobservable on the route the plan implied — `beginCompensation`
harvests then immediately consolidates, so `ArchivedCompensations` is `map[]` and the
`NodeID` keys can never be asserted; they are visible only on the `endInstance`
route. And **plan 1.1's stated RED** (C8) was impossible: `harvestOpenScopeCompensations`
is unexported, so a black-box `engine_test` test cannot produce
`undefined: harvestOpenScopeCompensations`. Both fixed.

---

## False claims and premise defects

| # | Claim | Verdict |
|---|---|---|
| C1 | M4's `rollback-after-clear … undo-inner runs` | **FALSE as labelled.** The label says "harvest excluded"; the probe silently seeded `RootCompensations` by hand. Run literally: **REFUSED**. A recorded measurement whose stated conditions differ from what was executed |
| C2 | "clearing `s.Scopes` leaves **M2's** surviving token naming a scope that no longer exists" | **FALSE (mis-attributed).** Post-fix, M2 routes into `beginCompensation`, which cancels every token (`tokens=0`). The hazard exists only in the **no-record** shape, which the bundle never built |
| C4 | `ADR-0034 §"Terminal unhandled error: run compensation walk before terminating"` | **FABRICATED citation.** No such section (`grep` EXIT=1; ADR-0034 has only Context/Decision/Consequences/Post-acceptance fix). The string is a **code comment** in `step_errors.go`. Worse, ADR-0034 conditions the walk on `RootCompensations` non-empty and the measured shape has `root=0` — so `main` **conforms**. It is a gap in the ADR-0034 × ADR-0039 interaction, not a violated contract |
| C10, B7 | *"**Every** … reader … **never** at an open scope"* | **FALSE.** `compensationRecordsForScope` reads the open scope's live list as a records-exist decision at `step_nodes.go:1204`; `step_nodes.go:1160` is a fourth such reader. The bundle's own narrow grep structurally cannot see either. The three-site enumeration remains correct *for dying-instance readers* |
| C11 | *"the only path into a root walk's record set"* | Over-broad — root records append directly via `recordCompensation`. Intended claim ("the only path **from the archive**") is true |
| C6, B10, A12 | *"an **empty** `Scopes`, which it already tolerates"* | **Imprecise + unstated change.** `closeScope` leaves a **non-nil** empty slice, so today every terminal instance persists `"Scopes":[]`; `s.Scopes = nil` yields `"Scopes":null` on **every** terminal transition, ordinary completions included. Functionally inert (no reader outside `engine/` touches the field — verified) but far wider than §6 stated, and precisely the wart T8 invokes ADR-0173 to prevent, applied to the map and missed on the slice |
| C9.2, A10 | "`engine/state.go:380-391`, ten statements" | **Nine** statements; ten *lines*. The load-bearing half (none mentions `Scopes`) is TRUE |
| C9.1, B8 | in-flight guard at `step_triggers.go:162` | **:163** — `:162` is a comment line |
| C9.3 | M3's `cmds=1` | **Fixture-dependent.** With a human-task park: `cmds=2 [UpdateTask FailInstance]`. The spec never said what M3 parks on. Robust criterion: absence of the compensation `InvokeAction` |
| B6 | *"a live walk's window still applies"* | **Half true.** What protects the records is `partitionForLiveWalk`'s **drop**; the window *stamp* is written onto the cursor and destroyed by the clear on the next line. Correct today only because the one live-cursor site is a terminal transition where the walk never advances again |
| A6 | — | **New reachable outcome, absent from the bundle:** a rollback on a `failed` instance now flips it to `terminated`, drops surviving tokens and moves `EndedAt` (via `handleSubInstanceFailed`'s tail — the only route yielding terminal + `Scopes==nil` + in-scope tokens + non-empty archive) |

---

## Verified TRUE — enumerations that survived independent re-derivation

Re-derived by **all three** lenses, line-for-line:

- **three** records-exist predicate sites (`step_errors.go:253`, `step_triggers.go:213`,
  `step_compensation.go:91`), and the two dismissed hits really are internal guards
- **ten** `endInstance` call sites, all ten lines exact
- **four** `beginCompensation` callers (`step_compensation.go:219`, `:915`,
  `step_errors.go:255`, `step_triggers.go:221`); `const scopeID = ""` at `:316`
- ADR-0162's stale sentence is **verbatim** and genuinely false; `endInstance` really
  never touches `s.Scopes`
- §3.5 holds: the two `Scopes`-non-empty assertions are on **non-terminal** instances
  with real fixtures, neither vacuous
- M1/M2/M3 reproduce **exactly**, including scope IDs and all three refusal strings
- `Scopes` has **no reader outside `engine/`**
- Two lenses independently implemented the full proposal → `go test ./engine/`
  **EXIT=0**, confirming §3.5's "the fix does not have to fight the suite"

## Obligations answered, and one that stayed open then closed

- **§8.1** (readers of `Token.ScopeID` on a terminal instance) — answered by A3 above.
- **§8.4** (event-sub-process double-harvest) — lens C could **not** execute it and
  correctly marked it `ASSUMPTION (unverified)`. **A8 and B9 then closed it
  independently:** `cancelScopeSubtree` archives the enclosing scope too, so both
  scopes are already emptied and the harvest's `len == 0` early return fires.
  Compensations `[undoDComp undoNComp]`, once each. **No double-harvest.**
- **§8.5** (re-derive the counts) — answered, all three correct.
- **§8.6** (attack the migration claim) — answered by B3; the claim is gone.

## Methodology lessons worth carrying forward

1. ⚠ **A `go test` run reported `EXIT=0` from a CACHE HIT** while the code under it
   panicked (Go caches on observed `os.Getenv`, and the probe was env-switched). The
   `-v` re-run panicked. It nearly cost lens A its central finding. **A green exit
   code from a cacheable run is not evidence the code ran.**
2. ⚠ **An instrumentation FILTER can manufacture the premise it is measuring.** M5's
   `if len(s.Scopes) > 0` produced 4 entries and a false universal; unconditional
   instrumentation produced 226 and five counterexamples.
3. ⚠ **Two audit worktrees were created at the base commit `02b72be`, without the
   bundle.** Lens C reset, lens A checked the docs out of the branch, lens B had
   branched from the feature branch and was unaffected. Both recovered *and said so*
   — but an auditor that silently audited a paraphrase would have produced
   confident, worthless findings. **Verify the audit tree contains the bundle, in the
   brief.**
4. **A sibling lens's structural argument is a lead, not a verdict** — sending C7 to
   lens A for independent verification is what caught the two exported soft readers.
