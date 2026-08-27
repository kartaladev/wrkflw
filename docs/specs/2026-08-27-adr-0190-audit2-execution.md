# ADR-0190 revision 2 — round-2 adversarial audit, EXECUTION lens

**Worktree:** detached at `a161f347`. **Method:** the plan's code was transcribed verbatim into
the tree, compiled, and run against real fixtures; every probe deleted afterwards.
**Baseline at the bundle commit, measured on a clean tree with Docker up: 65 ok, 0 FAIL, `go test -count=1 ./...` EXIT=0** (see E12 — the bundle claims 59 ok / 2 FAIL).

Legend: **CONFIRMED BY EXECUTION** = a probe was run and its output is pasted below.

## Findings summary

| ID | Sev | One line | Status |
|---|---|---|---|
| E1 | **Critical** | `/snapshot`'s `service.NewProcessInstance` reconstruction re-embeds a definition `WithoutEmbeddedDefinition` suppressed — 781 → 1068 bytes, on **identified** callers too | CONFIRMED BY EXECUTION |
| E2 | **Critical** | "actor present" is true for the ZERO actor; `renderState` drops ADR-0189's own `isZeroActor` guard, so the repo answers the same question two ways | CONFIRMED BY EXECUTION |
| E3 | **Critical** | `renderState(ctx, cfg CustomizeConfig[any], pi)` does not compile against any adapter — `CustomizeConfig[*http.ServeMux]` is not `CustomizeConfig[any]` | CONFIRMED BY EXECUTION |
| E4 | **Critical** | Task 4 is scoped to 2 files but needs 11 exported signature changes and ~70 call sites in 4 packages; it also breaks Task 5's packages and is an undeclared public-API break | CONFIRMED BY EXECUTION |
| E5 | **Critical** | `DiscloseAll` restores nothing for **20** withheld fields; `/snapshot` loses `incidents` and ADR-0175's `compensating` with no opt-out. The ADR states the opposite for `incidents`. T8 passes vacuously on the standard fixture | CONFIRMED BY EXECUTION |
| E6 | **Critical** | `/actionable` still emits `allowed_actions[].condition` (a routing expression) under the closed default — the plan gates `def` only on `/snapshot` | CONFIRMED BY EXECUTION |
| E7 | Major | `DiscloseActors` alone leaks `Completion.Note`; the code never blanks it and `DiscloseNotes` is read nowhere in the design | CONFIRMED BY EXECUTION |
| E8 | Major | "withheld by construction" is false for `NodeVisit` (`slices.Clone`), and four more copied-wholesale types are outside the guard entirely | CONFIRMED BY EXECUTION |
| E9 | Major | `httpcore.WithDisclosure(…)` cannot infer `R`; the repo's documented "alias is REQUIRED" convention is not followed and no adapter aliases are planned | CONFIRMED BY SOURCE (mechanism executed in E3) |
| E10 | Minor | `DiscloseAll` is spelled two incompatible ways (`WithDisclosure(DiscloseAll)` vs `WithDisclosure(DiscloseAll...)`) and no task declares it | CONFIRMED BY SOURCE |
| E11 | **Critical** | A consumer using `WithRequestActor` — the documented header/token path — gets the public projection for every **authenticated** caller on all 11 endpoints; the resolved actor never reaches the context | CONFIRMED BY EXECUTION |
| E12 | Major | The claimed baseline "59 ok / 2 FAIL" is false: **65 ok / 0 FAIL, EXIT=0** at `a161f347`. Both named packages pass. The plan tells the implementer to ignore regressions there | CONFIRMED BY EXECUTION |
| E13 | Minor | `PublicState` turns nil `Tokens`/`Tasks` into `[]`; and there are **eight** variables-bearing sites, not seven (`Compensating.Records[].Input`) | CONFIRMED BY EXECUTION |

**7 Critical, 4 Major, 2 Minor.** Every finding below carries its probe and verbatim output.
The worktree was returned to a clean `a161f347`; every probe file was deleted.

---
## E1 — CRITICAL — `/snapshot`'s reconstruction re-embeds a definition `WithoutEmbeddedDefinition` deliberately suppressed — **CONFIRMED BY EXECUTION**

**Claim attacked.** Plan Task 4 step 3 and ADR-0190 Decision 7: *"`/snapshot` renders a projected
instance — `service.NewProcessInstance(def, view.PublicState(pi.State()))` … needing no
`MarshalJSON` change, no interface method and no stamp."*

**Why it is wrong.** `service.NewProcessInstance` is documented at `service/instance.go:44-51` as
*"The marshalled document embeds a non-nil definition (ADR-0144); the engine-level opt-out
[WithoutEmbeddedDefinition] applies to instances the ProcessEngine hands out, not to one
fabricated here"* — it hardcodes `newProcessInstance(def, st, false)`. The engine's own
`ProcessEngine.instance` passes `e.omitDefinition`. Reconstructing therefore **discards the
engine's marshalling policy**. It bites the IDENTIFIED-caller branch too, where `renderState`
returns full state and `def` is never nil'd — i.e. **every** `/snapshot` request on such a
deployment, not only unauthenticated ones.

**Probe.** Real `service.ProcessEngine` built with `service.WithoutEmbeddedDefinition()`,
approval definition, one started instance; marshal `pi` (today) vs
`service.NewProcessInstance(pi.Definition(), pi.State())` (ADR-0190's identified-caller branch).

**Observed output (verbatim):**
```
=== RUN   TestProbeA_SnapshotReconstructionReEmbedsDefinition
    probe_test.go:79: engine built with WithoutEmbeddedDefinition()
    probe_test.go:80:   today   /snapshot has "definition" key: false  (len=781 bytes)
    probe_test.go:81:   ADR-0190 /snapshot has "definition" key: true  (len=1068 bytes)
    probe_test.go:83: REGRESSION: the reconstruction re-embeds the definition that WithoutEmbeddedDefinition suppressed
--- FAIL: TestProbeA_SnapshotReconstructionReEmbedsDefinition (0.00s)
```
The re-embedded `definition` is precisely the payload spec §2.1 lists as a disclosure
(`definition.nodes[].eligible_roles`). So the *disclosure fix* **widens** disclosure on this
path for the one consumer who had opted out.

**Concrete fix.** Do not fabricate. Add an exported constructor that carries the policy, e.g.
`service.NewProcessInstanceOmittingDefinition(def, st)` — or better, an exported
`service.ProjectInstance(pi ProcessInstance, st engine.InstanceState) ProcessInstance` that
preserves `def` **and** `omitDefinition` and only swaps the state. Whichever is chosen, ADR
Decision 7's "no interface method, no `service` change" claim must be retracted: `service`
**is** modified by this decision, which also falsifies the plan header's *"`service` is **not**
modified"*.

---

## E2 — CRITICAL — the "actor present" predicate is satisfied by the ZERO actor, contradicting ADR-0189's own invariant in the same package — **CONFIRMED BY EXECUTION**

**Claim attacked.** ADR-0190 Decision 3 / plan Task 3 step 3:
`if _, ok := authz.ActorFromContext(ctx); ok { return pi.State() }`. Spec §7 q2 raises this and
leaves it open.

**Probe.** `authz.ActorFromContext` run against four contexts.

**Observed output (verbatim):**
```
no actor at all                                  -> ActorFromContext ok=false actor={ID: Roles:[] Attributes:map[]}  => FIDELITY=public
deliberate ZERO actor                            -> ActorFromContext ok=true  actor={ID: Roles:[] Attributes:map[]}  => FIDELITY=FULL
ADR-0189 kiosk claimant {ID:"", Roles:[kiosk]}   -> ActorFromContext ok=true  actor={ID: Roles:[kiosk] Attributes:map[]}  => FIDELITY=FULL
real actor                                       -> ActorFromContext ok=true  actor={ID:alice Roles:[] Attributes:map[]}  => FIDELITY=FULL
```

`ActorFromContext` keys on the *type assertion succeeding*, not on the actor being meaningful.
Any middleware that unconditionally calls `authz.ContextWithActor(ctx, actorFromHeader(r))` —
the obvious shape — hands full fidelity to a caller with no header at all.

**The design contradicts a decision already shipped in the same package.** ADR-0189 put an
explicit `isZeroActor(actor)` guard in `ClaimTask`/`CompleteTask`/`ReassignTask`
(`endpoints.go:122,144,169`) whose comment reads *"a zero actor can never mean
'authenticated'"*. `renderState` as prescribed drops exactly that guard, so the same repo
answers "is the zero actor authenticated?" **no** on three endpoints and **yes** on eleven.

**Concrete fix.** Predicate must be `a, ok := authz.ActorFromContext(ctx); ok && !isZeroActor(a)`
— reusing the existing `httpcore.isZeroActor`, so there is one answer. The kiosk case
(`{ID:"", Roles:["kiosk"]}`) is then still "present" (non-zero), which is the correct
outcome for a deliberately-configured kiosk and must be **stated** in the ADR rather than left
as a flagged residual.

---
## E3 — CRITICAL — `renderState(ctx, cfg CustomizeConfig[any], pi)` cannot be called by ANY adapter; it does not compile — **CONFIRMED BY EXECUTION**

**Claim attacked.** Plan Task 3 step 3, the helper's literal signature:
`func renderState(ctx context.Context, cfg CustomizeConfig[any], pi service.ProcessInstance) engine.InstanceState`,
and Task 3's own prescribed tests, which call `httpcore.RenderStateForTest(t.Context(), cfgClosed(t), …)`.

**Why it is wrong.** `CustomizeConfig[R]` is generic over the ROUTER type. Every adapter holds a
concrete instantiation — `CustomizeConfig[*http.ServeMux]` (`stdlib/groups.go:33`),
`CustomizeConfig[gin.IRouter]`, `CustomizeConfig[fiber.Router]`. Go generic instantiations are
distinct, non-assignable types; there is no conversion to `CustomizeConfig[any]`.

**Probe.** Transcribed `renderState` verbatim into `httpcore`, added the `Disclosure
authz.DisclosureSet` field Task 3 requires, exported the plan's `RenderStateForTest` hook, then
called it from `stdlib` the way Task 4 must.

**Observed output (verbatim):**
```
# github.com/kartaladev/wrkflw/transport/http/stdlib
transport/http/stdlib/zzprobe_call.go:16:42: cannot use cfg (variable of struct type
  httpcore.CustomizeConfig[*http.ServeMux]) as httpcore.CustomizeConfig[any] value in
  argument to httpcore.RenderStateForTest
```

**Concrete fix.** Two options; the second matches the repo's existing convention.
1. `func renderState[R any](ctx context.Context, cfg CustomizeConfig[R], pi service.ProcessInstance) engine.InstanceState`
   — compiles, but forces every endpoint function to become generic over R as well, which
   propagates R into eleven exported signatures for no benefit.
2. **Preferred:** pass only what is needed —
   `func renderState(ctx context.Context, d authz.DisclosureSet, pi service.ProcessInstance) engine.InstanceState`.
   This is exactly how `cfg.InstanceMapper` is already threaded (the endpoints take the func,
   never the config), so it needs no new convention.

---

## E4 — CRITICAL — Task 4 is scoped to two files but requires changing **eleven exported function signatures** and ~70 call sites across four packages — **CONFIRMED BY EXECUTION**

**Claim attacked.** Plan Task 4: *"**Files:** modify `transport/http/httpcore/endpoints.go`,
`admin_endpoints.go`"*, and *"**Step 3: implement.** Replace `pi.State()` with
`renderState(ctx, cfg, pi)` at all ten state-passing sites."* Plus the plan's
*"Fan out BY GO PACKAGE. Tasks below are grouped so no two concurrent agents share one."*

**Why it is wrong.** None of the eleven render functions has `cfg` — or anything carrying a
disclosure signal — in scope. They receive at most `mapper func(engine.InstanceState) any`
(`endpoints.go:25,47,82,116,138,163`) and three receive nothing at all
(`GetInstanceSnapshot:60`, `GetActionableView:72`, `CancelInstance` admin:116). Every one needs a
new parameter, so every caller must change.

**Probe.** Enumerated call sites mechanically.

**Observed output (verbatim):**
```
StartInstance:            prod-adapter-callsites=3
GetInstance:              prod-adapter-callsites=3
GetInstanceSnapshot:      prod-adapter-callsites=3
GetActionableView:        prod-adapter-callsites=3
DeliverSignal:            prod-adapter-callsites=3
ClaimTask:                prod-adapter-callsites=3
CompleteTask:             prod-adapter-callsites=3
ReassignTask:             prod-adapter-callsites=3
ResolveIncident:          prod-adapter-callsites=3
CancelInstance:           prod-adapter-callsites=3
ResolveCompensationStall: prod-adapter-callsites=3
=== total prod === 33
adapter _test call sites: 13
httpcore  _test call sites: 24  (in compensation_stall_endpoint_test.go, admin_endpoints_test.go,
                                 defref_validation_test.go, endpoints_test.go)
```

**Consequences the bundle does not state.**
- Task 4 **breaks the compile of `transport/http/{stdlib,gin,fiber}`**. Those are Task 5's
  packages, so Task 4 and Task 5 are strictly serial and Task 4 leaves the tree red — which
  contradicts the plan's fan-out claim and its per-task `git commit` steps.
- These eleven functions are **exported from a module-root package**, and `httpcore.MountGroups`
  is documented as *"the consumer extension seam: any `RouteCustomizer[R]` — including a
  consumer's own"*. Changing them is an **API break for every consumer-written adapter**.
  ADR-0190's Consequences names exactly one breaking change (the wire shape) and is silent on
  the API break.
- Spec §6 item 2 tells the implementer to *"re-derive the breakage net"* but the plan's own
  Task-4 scope makes the net ~70 call sites, not the 18 failures in 3 packages it quotes from
  revision 1's measurement.

**Concrete fix.** Either (a) add a trailing parameter to all eleven and record the API break as a
second headline in the ADR's Consequences plus a `SECURITY.md`/CHANGELOG note, or (b) keep the
signatures and make the projection happen **inside the mapper**: `ResolveConfig` wraps
`cfg.InstanceMapper` into a ctx-aware `func(context.Context, engine.InstanceState) any`. Option (b)
still changes the three functions that take no mapper. Either way Task 4 must own
`transport/http/{stdlib,gin,fiber}` as well, and the plan's task grouping must be redrawn.

---

## E5 — CRITICAL — `DiscloseAll` does NOT restore the pre-ADR-0190 shape: **20 withheld fields have no restoring category**, and the ADR states the opposite for one of them — **CONFIRMED BY EXECUTION**

**Claims attacked.**
- ADR-0190 Decision 6 / spec §3 D6: *"`httpcore.DiscloseAll` restores the exact pre-ADR-0190 wire
  shape in one call."*
- ADR-0190 **Residuals**: *"`incidents[].error` … Withheld by default (`Incidents` is not
  structural), **but a consumer setting `DiscloseAll` gets it**."*
- Spec §5 T8 / plan Task 5 step 2: *"`WithDisclosure(DiscloseAll)` reproduces the pre-ADR-0190
  body byte-for-byte."*

**Why it is wrong.** The plan's `PublicState` restores exactly five `InstanceState` fields and one
`Token` field under any category. Nothing restores the other twenty.

**Probe 1** — `/snapshot` document on a state carrying one incident, pre vs `DiscloseAll`.

**Observed output (verbatim):**
```
    probe2_test.go:34: pre-ADR-0190  has "incidents": true
    probe2_test.go:35: DiscloseAll   has "incidents": false
    probe2_test.go:37: DiscloseAll does NOT restore incidents — no category maps to InstanceState.Incidents.
```

**Probe 2** — the orphan set.
```
    InstanceState withheld fields with NO restoring category (15 of 20):
      [Incidents PendingFinalErr Compensating Timers ArmedEvents Boundaries
       EventTriggeredSubprocesses DeferredCompensationThrows RecentCompensationCmdIDs
       CmdSeq TokenSeq TaskSeq TimerSeq ScopeSeq IncidentSeq]
    Token withheld fields with NO restoring category (5 of 6):
      [AwaitCommand AwaitSignal AwaitMessage AwaitMessageKey AwaitTimer]
    TOTAL unrestorable: 20 fields
```

**Two of these are user-visible regressions with no opt-out.** `instanceJSON` renders
`incidents` (`service/instance.go:127`) and `compensating` (`:136`). ADR-0175 shipped
`compensating` *"to make a WEDGED instance findable: a stalled walk never dispatches again, so an
instance already stalled … raises no incident and would otherwise be invisible"* — an operator
polling `/snapshot` unauthenticated loses exactly that, permanently.

**Why T8 as prescribed would NOT catch this.** I ran the byte-identity comparison first on the
harness's standard fixture (fresh approval instance, no incident, no walk) and it **passed**:
```
=== RUN   TestProbeB_SnapshotDiscloseAllByteIdentity
    probe_test.go:104: byte-identical under DiscloseAll
--- PASS
```
T8 is vacuous unless its fixture carries an incident *and* an in-flight compensation cursor.

**Concrete fix.** Pick one and state it:
- Give `DiscloseAll` a distinct meaning — a `PublicState` bypass (`renderState` returns
  `pi.State()` when the set is "all"), so "one-call opt-out" is literally true; **and** add the
  missing categories (`DiscloseIncidents`, `DiscloseCorrelation` for `Token.Await*`,
  `DiscloseInternals` for the counters/arms) so intermediate postures exist.
- Or retract the byte-identity claim in the ADR, spec §5 T8 and plan Task 5 step 2, and list the
  twenty fields that can never be restored.
Either way T8's fixture must populate `Incidents` and `Compensating`, and the ADR's Residuals
sentence about `DiscloseAll` must be corrected — it is false as written.

---
## E6 — CRITICAL — `/actionable` still discloses flow-condition expressions; the plan gates `def` only on `/snapshot` — **CONFIRMED BY EXECUTION**

**Claim attacked.** Plan Task 4 step 3: *"Replace `pi.State()` with `renderState(ctx, cfg, pi)` at
all ten state-passing sites"*, plus spec §5 T1: *"unauthenticated GET on each of the 11 entry
points renders no variables, no actors, no notes, **no policy**"*. `DisclosePolicy` is defined in
plan Task 1 as *"authorization policy and routing expressions: the embedded definition **and flow
conditions**"*.

**Why it is wrong.** `endpoints.go:77` is
`view.NewActionableView(pi.State(), pi.Definition())`. Replacing only the first argument leaves
`pi.Definition()` untouched, and `NewActionableView` reads `def.Outgoing(t.NodeID)` and emits
`NextAction.Condition` — the raw `expr` routing expression
(`runtime/view/instance_actionable.go:70-83`). The plan's `def = nil` gate exists **only** in the
`/snapshot` branch at `endpoints.go:65`.

**Probe.** Own fixture (the harness's `ApprovalProcess` flow `f2` has NO `Condition` — the plan's
own documented vacuity trap), one open task, closed disclosure set.

**Observed output (verbatim):**
```
=== RUN   TestProbeF_ActionableStillLeaksFlowConditions
    probe3_test.go:56: /actionable body for an UNIDENTIFIED caller, CLOSED disclosure:
        {"instance_id":"i1","status":"running","open_tasks":[{"task_id":"t1","node_id":"approve",
         "state":"unclaimed","allowed_actions":[{"flow_id":"f2","target":"end",
         "condition":"vars.salary > 100000 && actor.dept == \"SECRET-DEPT\""}]}]}
    probe3_test.go:58: LEAK: the routing expression survives — DisclosePolicy is never consulted on this path
--- FAIL
```
Spec §2.1 measured exactly this endpoint and the closed default does not close it.

**Concrete fix.** `GetActionableView` must gate `def` on the same predicate as `/snapshot`:
```go
def := pi.Definition()
if !d.Has(authz.DisclosePolicy) && !identified(ctx) { def = nil }
return http.StatusOK, view.NewActionableView(renderState(ctx, d, pi), def), nil
```
⚠ `def = nil` also nils `allowed_actions` **entirely** (`NewActionableView`'s godoc: *"If def is
nil, AllowedActions on each ActionableTask is nil"*), so `/actionable` becomes a task list with no
next steps for unidentified callers. That is a bigger behavioural change than "no policy" and the
ADR must state it — or `NextAction` must gain a condition-suppressing form so targets survive
while expressions do not. Either way the choice must be **written down**; today the bundle
neither closes it nor names it.

---

## E7 — MAJOR — `DiscloseActors` alone leaks the completion **Note**, and `DiscloseNotes` is a category the design never reads — **CONFIRMED BY EXECUTION**

**Claim attacked.** Plan Task 2 step 3, prose after the code: *"⚠ `DiscloseActors` restores
`Completion` **including its `Note`**. If `DiscloseNotes` is not also set, blank the note after the
assignment — categories are independent and a test asserts it."* Plan "Self-review notes" repeats
it. **The code as written does not do it**, and `authz.DiscloseNotes` appears in exactly one place
in the whole design — its own `const` declaration.

**Probe.** Transcribed `PublicState` verbatim, ran with `NewDisclosureSet(DiscloseActors)`.

**Observed output (verbatim):**
```
"Completion":{"actor":{"id":"completer"},"timestamp":"…","note":"note-111-22-3333"}
    zzprobe_public_test.go:76: LEAK: DiscloseActors alone leaked Completion.Note="note-111-22-3333"
      with DiscloseNotes UNSET
```
`service/instance.go:239` puts `*humantask.Completion` on the `/snapshot` wire verbatim, so this
reaches the client.

**Why the prescribed tests do not catch it.** Plan Task 2 step 1's only category test,
`TestPublicState_WidensOnDisclosure`, asserts `DiscloseVariables` does not restore `Claim`. No
prescribed test exercises `DiscloseActors` at all. The "a test asserts it" clause in the plan
names no test.

**Concrete fix.** In `PublicState`, after `out.Tasks[i].Completion = tk.Completion`:
```go
if c := tk.Completion; c != nil {
    cp := *c
    if !d.Has(authz.DiscloseNotes) { cp.Note = "" }
    out.Tasks[i].Completion = &cp
}
```
(note the **copy** — assigning `tk.Completion` shares the pointer with live state, so blanking in
place would mutate the engine's audit record and break
`TestPublicState_DoesNotMutateInput`). Add an explicit case to Task 2's test table:
`DiscloseActors` alone ⇒ `Claim` present, `Completion.Note == ""`.

---

## E8 — MAJOR — "withheld by construction" is FALSE for `engine.NodeVisit`: `slices.Clone(st.History)` copies it wholesale — **CONFIRMED BY EXECUTION**

**Claim attacked.** ADR-0190 Decision 4 and spec §3 D6: *"It builds a **fresh**
`engine.InstanceState` … and recursively rebuilds `Tokens`, `Tasks` **and `History`** from their
own allow-lists. Everything else is absent **by construction**, so a field added to any of those
structs tomorrow is withheld without anyone remembering to withhold it."* Plan Task 2 step 3 in
fact writes `History: slices.Clone(st.History), // all six fields are public`.

**Probe.** Ablation, the way plan Task 2 step 5 prescribes — but on `NodeVisit` instead of
`InstanceState`: added `LeakVar string` to `engine.NodeVisit`, populated it with a secret,
projected with the CLOSED set.

**Observed output (verbatim):**
```
=== RUN   TestProbeG_NodeVisitNewFieldIsCopiedWholesale
    zzprobe_public_test.go:100: NOT withheld by construction: the new NodeVisit field survives slices.Clone.
--- FAIL: TestProbeG_NodeVisitNewFieldIsCopiedWholesale (0.00s)
    zzprobe_classification_test.go:60: NodeVisit.LeakVar is UNCLASSIFIED …
    --- FAIL: TestClassification_IsTotal/NodeVisit
```
The guard **does** fire, so this is fail-loud rather than fail-open — but the ADR's central
structural claim is false for one of the three "recursively rebuilt" types, and the plan's own
comment bakes the assumption in.

**The plan's prescribed ablation cannot find this.** Task 2 step 5 says *"Add `Scratch
map[string]any` to `engine.InstanceState`"* — that path I also ran, and it behaves as designed:
```
    zzprobe_classification_test.go:60: InstanceState.Scratch is UNCLASSIFIED …
--- FAIL: TestClassification_IsTotal
--- PASS: TestProbe_PublicState_AllSevenSites   ← withheld by construction, as claimed
```
So the prescribed ablation passes while the property it certifies is false one level down.

**Same hole, three more types, and there it IS fail-open.** `PublicState` also copies
`*humantask.Claim`, `*humantask.Completion` and `[]authz.Actor` wholesale under `DiscloseActors`,
and `[]engine.Scope` / `[]engine.CompensationRecord` / the `Variables` maps wholesale under
`DiscloseVariables`. **None of those five types is in the classification guard's four-type
table**, so a field added to `humantask.Claim` is disclosed with no test failing at all.

**Concrete fix.**
1. Rebuild `History` element-wise from the `NodeVisit` allow-list, exactly as `Tokens` and `Tasks`
   are — do not `slices.Clone`.
2. Extend `TestClassification_IsTotal`'s table to `humantask.Claim`, `humantask.Completion` and
   `authz.Actor` (and, if the disclosure form for them is ever widened, `engine.Scope` and
   `engine.CompensationRecord`).
3. Change plan Task 2 step 5's ablation to run on **each** type in the table, not only
   `InstanceState` — that is the only version of the ablation that certifies the ADR's claim.

---

## E9 — MAJOR — `httpcore.WithDisclosure` cannot be called: R is uninferrable, and the bundle prescribes no per-adapter aliases — **CONFIRMED BY SOURCE, mechanism executed in E3**

**Claim attacked.** Spec §3 D6 / ADR Decision 6 / plan Task 3 Produces:
*"`httpcore.WithDisclosure(cats ...authz.DisclosureCategory)`, a `CustomizeOption` on the mount"*.

**Why it is wrong.** Every existing `CustomizeOption` factory carries a documented warning that
this exact shape does not compile at a call site. `transport/http/stdlib/options.go:20-27`:
*"⚠ This alias is REQUIRED, not cosmetic. On the generic [httpcore.WithMaxBodyBytes] the type
parameter R appears only in the RESULT type, so Go cannot infer it from the argument —
`httpcore.WithMaxBodyBytes(0)` does not compile ("cannot infer R")."* `WithDisclosure` has the
identical shape, so `httpcore.WithDisclosure(authz.DiscloseVariables)` will not compile and a
consumer must write `httpcore.WithDisclosure[*http.ServeMux](…)`.

The repo answers this with five aliases per adapter (`WithBasePath`, `WithMaxBodyBytes`,
`WithRequestActor`, `WithRequestActorTimeout`, `WithBodyReadTimeout` — ×3 adapters = 15). The
bundle adds a sixth generic option and **no aliases**: Task 5 lists only parity tests, the byte
comparison, `SECURITY.md` and the doc sweep.

**Concrete fix.** Add `stdlib.WithDisclosure`, `gin.WithDisclosure`, `fiber.WithDisclosure` to
Task 5 (or a new task), each a one-line alias, each with the "⚠ REQUIRED, not cosmetic" note the
siblings carry. Same for `DiscloseAll` if it is meant to be reachable without importing
`httpcore`.

---

## E10 — MINOR — `DiscloseAll` has two incompatible spellings across the bundle

**Claim attacked.** Spec §5 T8 writes `WithDisclosure(DiscloseAll)` — one value. Plan Task 5 step 2
writes `WithDisclosure(DiscloseAll...)` — a **variadic spread**, i.e. a slice. Plan Task 3
"Produces" lists `httpcore.DiscloseAll` with no type. It cannot be both an
`authz.DisclosureCategory` and a `[]authz.DisclosureCategory`, and Task 1 — which owns the
disclosure vocabulary — does not produce it at all.

**Concrete fix.** Declare it once, in Task 1 beside the categories, as
`var AllDisclosureCategories = []DisclosureCategory{…}` (spread at the call site) **or** as
`func DiscloseAll[R any]() CustomizeOption[R]`; fix every citation to match. Note that whichever
form is chosen, E5 shows it cannot actually restore the prior shape.

---
## E11 — CRITICAL — a consumer using `WithRequestActor` gets the PUBLIC PROJECTION for every AUTHENTICATED caller, on all eleven endpoints — **CONFIRMED BY EXECUTION**

**Claims attacked.**
- Plan Task 3 step 3: *"⚠ Use `authz.ActorFromContext` directly, **not** `RequestActor`: this must
  not return an error, must not invoke a consumer resolver **a second time**, and must not turn an
  unidentified read into a 401."*
- Spec §3 D6 and ADR Decision 7: *"⚠ The three human-task verbs need no special case: ADR-0189
  authenticates them, so an actor is present and they render full fidelity."*

**Why it is wrong.** `httpcore.RequestActor` **returns** the actor; it never places it on the
context (`resolve_actor.go`, `resolveRequestActor` — the actor is a return value throughout). The
adapters then pass `req.Context()` **unchanged** into the endpoint and hand the actor as a
separate argument (`stdlib/groups.go:138,157` and the two siblings). So `authz.ActorFromContext`
is empty on exactly the routes ADR-0189 authenticates, whenever the identity did not come from the
context in the first place — which is the entire documented purpose of `WithRequestActor`:
*"Pass this option instead when the identity is not on the context — e.g. it must be derived from a
header or a token store per request"* (`stdlib/options.go`).

Two sub-claims in the plan's own justification are also false: for the **eight non-task**
endpoints `cfg.RequestActor` is never invoked at all, so there is no "second time"; and reusing the
already-resolved actor on the three task verbs costs no extra call either.

**Probe.** Real `http.ServeMux` + `stdlib.TaskRoutes` mounted with
`stdlib.WithRequestActor(headerResolver)`; a real claim against the standard harness.

**Observed output (verbatim):**
```
=== RUN   TestProbeH_WithRequestActorNeverReachesTheContext
    probe4_test.go:40: claim status                       = 200
    probe4_test.go:41: actor present on ctx at resolve    = false
    probe4_test.go:46: actor present on ctx at RENDER     = false
    probe4_test.go:48: AUTHENTICATED (200) but renderState's predicate says UNIDENTIFIED =>
      public projection for a fully authenticated caller.
      body={"instance_id":"i1","def_id":"approval","def_version":1,"status":"running",
            "started_at":"…"}
--- FAIL
```

**Concrete fix.** The predicate must consult the **configured resolver**, not the raw seam:
```go
func renderState(ctx context.Context, d authz.DisclosureSet, resolve RequestActorFunc,
	pi service.ProcessInstance) engine.InstanceState {
	if a, err := resolve(ctx); err == nil && !isZeroActor(a) { // errors ⇒ unidentified, never 401
		return pi.State()
	}
	return view.PublicState(pi.State(), d)
}
```
and the three task verbs must pass the actor they already resolved rather than re-resolving.
Alternatively, have the adapters re-inject: `req = req.WithContext(authz.ContextWithActor(req.Context(), actor))`
after `RequestActor` succeeds — a smaller diff that also fixes it, and arguably a fix ADR-0189
should have made. Whichever is chosen, spec §3 D6's "the three human-task verbs need no special
case" must be **retracted**: as written they are the *most* affected routes.

---

## E12 — MAJOR — the claimed baseline "59 ok / 2 FAIL" is FALSE: the tree is **65 ok / 0 FAIL** at the bundle commit — **CONFIRMED BY EXECUTION**

**Claims attacked.** Repeated four times, and always as fact:
- Spec §0 "Findings adjudicated as NOT defects": *"Tree not green at the bundle commit … Baseline
  **59 ok / 2 FAIL**, unrelated to this work. Recorded so the next run is not misread."*
- Spec §6 item 1, plan "Global Constraints" (*"Baseline is not green"*), and plan "Phase 1
  verification checklist" — all naming `internal/database` and `internal/dbtest` as failing on
  MySQL/testcontainers, and all instructing *"Do not report those as regressions."*

**Probe.** `docker info` → up. Clean worktree at the bundle commit `a161f347` (every probe file
moved aside first, `git status --porcelain` empty), then `go test -count=1 ./...`.

**Observed output (verbatim):**
```
$ grep -c "^ok " /tmp/p/baseline.log
65
$ grep "^FAIL" /tmp/p/baseline.log
   (no output)
$ tail -1 /tmp/p/baseline.log
EXIT=0
$ grep -E "internal/database|internal/dbtest" /tmp/p/baseline.log
ok  	github.com/kartaladev/wrkflw/internal/database              49.623s
ok  	github.com/kartaladev/wrkflw/internal/database/transaction  23.083s
ok  	github.com/kartaladev/wrkflw/internal/dbtest                49.096s
```
The two packages the bundle names as pre-existing failures both **pass**, in ~50 s each — i.e. the
original measurement was almost certainly taken with the Docker daemon down, where a container
failure is not a pre-existing defect but a missing prerequisite.

**Why this is dangerous, not cosmetic.** The plan tells the implementer, three times, to treat
failures in those two packages as expected. Both sit on the persistence path. A genuine regression
there would be waved through.

**Concrete fix.** Replace all four occurrences with **"Baseline at `a161f347`: 65 ok, 0 FAIL,
`go test ./...` EXIT=0, Docker up (47 packages have no test files). ⚠ `internal/database`,
`internal/database/transaction` and `internal/dbtest` need a running Docker daemon; if the daemon
is down they fail as MISSING PREREQUISITE, not as a pre-existing defect."** Delete every
"do not report those as regressions" instruction.

---

## E13 — MINOR — `PublicState` turns a nil `Tokens`/`Tasks` into `[]`, and the seven-site enumeration misses an eighth — **CONFIRMED BY EXECUTION**

**(a) nil → empty slice.** `out.Tokens = make([]engine.Token, len(st.Tokens))` runs
unconditionally, so a state with no tokens (any completed instance) projects `[]` where the input
had `null`:
```
    zzprobe_public_test.go:85: IN : …,"Tokens":null,…,"Tasks":null,…
    zzprobe_public_test.go:86: OUT: …,"Tokens":[],  …,"Tasks":[],  …
```
Harmless on `/snapshot` (`instanceJSON` uses `omitempty` and rebuilds with `make(…, 0, …)`
anyway) and on the default `InstanceView` (which carries neither field), but a **custom
`InstanceMapper`** — the T6 case — sees the change. Fix: guard both `make` calls with
`if st.Tokens != nil` / `if st.Tasks != nil`, or use `slices.Grow`-style nil preservation.

**(b) There are eight variables-bearing sites, not seven.** Spec §2.5, ADR "Context" and plan Task
2 all enumerate seven. `engine.InstanceState.Compensating` is an **exported** field of the
unexported `compensationCursor`, whose `Records []CompensationRecord` each carry
`Input map[string]any` — documented at `engine/state_compensation.go:65-68` as *"Records is a slice
whose elements carry an Input map, so cloneState deep-copies it explicitly (ADR-0171)"*. It
marshals: my raw dump shows `"Compensating":{…,"Records":null,…}` present in
`json.Marshal(InstanceState)`.

`PublicState` withholds `Compensating`, so this is **not** a leak — but it makes plan Task 2 step 1's
fixture (*"populates ALL SEVEN measured variables sites … Any site left empty makes the test
vacuous for it"*) vacuous for the eighth, and it is the fifth different answer this bundle has
given to "where do process variables live" (2 → 3 → 4 → 7 → 8). Fix: say **eight**, populate
`Compensating.Records[].Input` in the T4 fixture, and note that `compensatingJSON`
(`service/instance.go:167`) does not render `Records`, so the exposure is confined to the
custom-mapper path.

---

## What the execution lens VERIFIED AS SOUND

Recorded so the next reader does not re-derive them.

1. **The fresh-literal primitive works.** `engine.InstanceState{InstanceID:"i1", …}` compiles from
   `runtime/view` despite the struct's unexported `ids idSource` and six sequence counters. Spec
   §2.6 is correct.
2. **`PublicState` withholds all seven enumerated variables sites.** Fixture populating
   `Variables`, `StartVariables`, `Tokens[].Payload`, `Tasks[].Vars`, `RootCompensations[].Input`,
   `Scopes[].Compensations[].Input`, `ArchivedCompensations[k][].Input` — plus `Incidents[].Error`,
   `PendingFinalErr`, `Tokens[].AwaitMessageKey`, `Claim.Actor.Attributes`, `Candidates`,
   `Eligibility` — projected with the closed set: **14 occurrences of the secret in the raw
   document, 0 in the projection.**
3. **The field counts are right**: `InstanceState` 31 exported (32 fields, one unexported),
   `Token` 13, `HumanTask` 11, `NodeVisit` 6. Every name in the plan's classification tables exists.
4. **The classification guard works and its `InstanceState` ablation behaves as designed** — see E8
   for the observed output, and for why that is not sufficient.
5. **The eleven render sites are right**, re-derived independently:
   `endpoints.go:42,52,65,77,94,133,158,182` and `admin_endpoints.go:111,121,514`. No other
   `.State()` / `ActiveTasks()` render exists in `transport/`.
6. **`AdminListInstances` is genuinely exempt** — it projects `instanceSummaryView`
   (`admin_endpoints.go:35-43`): 7 scalar fields, no variables, no actors.
7. **`instanceJSON`'s `taskJSON` carries no `Vars` and no `Eligibility`** — so those two withheld
   fields were never on the `/snapshot` wire and their withholding costs nothing there.

