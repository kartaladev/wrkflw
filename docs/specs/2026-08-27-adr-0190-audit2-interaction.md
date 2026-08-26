# ADR-0190 revision 2 — round-2 adversarial audit, INTERACTION lens

**Date:** 2026-08-27 · **Bundle commit:** `a161f347` · **Worktree:** `wt-inter2`
**Mandate:** take the nine revision-1→revision-2 changes PAIRWISE and derive what each
does to the others' premises. Findings below are appended as confirmed; probes were
executed and deleted.

Change labels used throughout (the explicit list handed to this lens):

| # | change |
|---|---|
| **C1** | disclosure control MOVED from `service` to the transport entirely; `service` untouched |
| **C2** | fidelity signal CHANGED to "IS AN ACTOR PRESENT ON THE CONTEXT" (`authz.ActorFromContext`) |
| **C3** | model INVERTED deny-list → allow-list (`DisclosureCategory`); zero value flipped meaning |
| **C4** | mechanism CHANGED to "build a FRESH struct naming only allow-listed fields" |
| **C5** | guard CHANGED to asserting CLASSIFICATION TOTALITY over four structs' exported fields |
| **C6** | wiring went from 2 sites to ALL 11 via one shared `renderState` helper |
| **C7** | `/snapshot` now renders a reconstructed `service.NewProcessInstance(def, projectedState)` |
| **C8** | the operation gate (old D2–D5) and admin (old D7) DEFERRED to phases 2 and 3 |
| **C9** | phase 1's scope shrank to transport-only |

---
## I2-C1 — CRITICAL — (C1 × C2) and (C6 × C2): the transport has TWO identity reads, and they disagree. A consumer using `WithRequestActor` is authenticated by ADR-0189 and simultaneously invisible to `renderState`.

**Pair.** C1 put disclosure in the transport; C2 keyed it on `authz.ActorFromContext`;
C6 routes all eleven render sites through one `renderState` that reads only that.

**What C2 does to C1's premises.** Spec §D6 and ADR Decision 3 both assert *"The transport
already resolves the actor per request via ADR-0189's `httpcore.RequestActor`"* and then have
`renderState` read `authz.ActorFromContext`. **These are not the same source.** ADR-0189's
`RequestActor` calls `cfg.RequestActor`, a `RequestActorFunc` the consumer may replace with
`stdlib.WithRequestActor` / `gin.WithRequestActor` / `fiber.WithRequestActor`. Only the
DEFAULT (`httpcore.defaultRequestActor`, `seam.go`) reads the context. **No code anywhere in
`transport/http` calls `authz.ContextWithActor`** — verified: `grep -rn ContextWithActor
transport/http/*/*.go | grep -v _test` returns only doc-comment examples. The resolved actor
travels to the endpoint as a **function argument** (`endpoints.go:110,131,155` —
`ClaimTask`/`CompleteTask`/`ReassignTask` each take `actor authz.Actor`), and every adapter
passes the *unmodified* `req.Context()` alongside it (`stdlib/groups.go:138,146` etc.).

**MEASURED** (probe run against the bundle commit, then deleted):

```
stdlib.Mount(mux, svc, stdlib.WithRequestActor(resolver))   // resolver returns alice/manager
POST /tasks/{id}/claim
  HTTP status = 200                      <- ADR-0189 authenticated the caller
  authz.ActorFromContext(ctx) ok = false <- renderState would take the PUBLIC branch
```

`WithRequestActor` is not an exotic shape. `transport/http/stdlib/options.go:52-55` documents
it as the supported answer for *"when the identity is not on the context — e.g. it must be
derived from a header or a token store per request"*, and `identity_test.go`'s own
`staticActor` helper is exactly this form.

**Concrete broken scenario.** A deployment that authenticates by header via
`WithRequestActor` gets, for **every one of the eleven entry points**, the closed public
projection — for callers it just authenticated. The three ADR-0189 verbs return 200 with a
redacted body. `GET /instances/{id}` is redacted for the CEO. This is precisely the
"everyone blind" configuration spec §D8 claims revision 2 eliminated ("Revision 1's phase 1
had only two reachable configurations — everyone blind, or everyone sees everything").
Revision 2 reintroduces it for the entire class of header-authenticating consumers, and the
only escape is `DiscloseAll`, i.e. "everyone sees everything".

**Three bundle claims are false as a consequence, all of them load-bearing:**
- ADR Decision 7 ⚠ and spec §D6 ⚠: *"The three human-task verbs need no special case:
  ADR-0189 authenticates them, so an actor is present and they render full fidelity."*
  False whenever the resolver is custom — the actor is an argument, never on the ctx.
- Spec §2.2 ⚠ (same sentence) and plan test **T5** (*"an authenticated request renders full
  fidelity"*): T5 passes only because it will be written with the default ctx-middleware
  fixture. It is **vacuous for the custom-resolver half**, which is the half that breaks —
  the same "fixture from the half that works" failure ADR-0189 hit twice.
- ADR Decision 3: *"The transport already resolves the actor per request via …
  `RequestActor`."* `RequestActor` is called at **9 adapter sites only** (3 task verbs ×
  3 adapters). It is called on **none** of the other eight render entry points —
  `GET /instances`, `/snapshot`, `/actionable`, `POST /instances`, `/signals`, and the three
  `admin_endpoints.go` sites resolve no actor at all. So even the default-resolver claim is
  a claim about 3 of 11 sites restated as one about all 11.

**CONCRETE FIX (two parts, both required).**

1. **Make `renderState` read the same source ADR-0189 does.** Resolve through
   `cfg.RequestActor` — not `authz.ActorFromContext` — and treat *any* error as "no actor":

   ```go
   func renderState(ctx context.Context, cfg CustomizeConfig[R], pi service.ProcessInstance) engine.InstanceState {
       if _, err := RequestActor(ctx, cfg.RequestActor); err == nil {
           return pi.State()
       }
       return view.PublicState(pi.State(), cfg.Disclosure)
   }
   ```
   This satisfies the plan's own three constraints on the call (*"must not return an error,
   must not turn an unidentified read into a 401"*) because the error is swallowed, and it
   removes the second identity source. ⚠ It does **not** satisfy the plan's third constraint
   *"must not invoke a consumer resolver a second time"* on the three task verbs, which is
   why part 2 is required.

2. **Resolve once per request and carry it.** In the three task handlers, after
   `RequestActor` succeeds, derive the request context:
   `ctx := authz.ContextWithActor(req.Context(), actor)` and pass that ctx onward — then
   `renderState` may keep using `ActorFromContext` for free on those three, and part 1's
   resolver call covers the other eight. Either half alone leaves a hole: part 1 alone
   double-invokes the resolver (and double-charges `RequestActorTimeout`) on the task verbs;
   part 2 alone leaves the eight non-task sites reading a context nothing ever populates
   under a custom resolver.

   ⚠ If the authors prefer to keep `ActorFromContext` as the sole signal, then the design
   must instead say so honestly and **document that `WithRequestActor` deployments must ALSO
   install ctx middleware** — but that contradicts the very reason `WithRequestActor` exists
   ("when the identity is not on the context") and should be rejected.

**Also invalidated:** the ADR's Residual *"A permissive `RequestActorFunc` … yields 'actor
present' and therefore full fidelity"* is **backwards for the shape it names**. A permissive
`RequestActorFunc` yields actor-ABSENT under `ActorFromContext`, i.e. the projection. The
residual describes a design (resolver-keyed) the bundle did not adopt. Fixing I2-C1 via part 1
makes the residual true again; leaving it makes the residual a false claim in the record.

## I2-C2 — CRITICAL — (C6 × C7): C7 invented a definition-withholding rule for `/snapshot` only; C6's one-helper wiring carries STATE, so `/actionable` keeps disclosing the `DisclosePolicy` category by default.

**Pair.** C7 gave `/snapshot` a bespoke rule (`def = nil` unless `DisclosePolicy`). C6
asserts one helper covers all eleven sites. `renderState(ctx,cfg,pi)` returns
`engine.InstanceState` — **it carries no definition**.

**What C7 does to C6's premises.** There are exactly **two** render sites that consume
`pi.Definition()`: `endpoints.go:65` (`/snapshot`, via the self-marshalling `pi`) and
`endpoints.go:77` (`GetActionableView` → `view.NewActionableView(pi.State(), pi.Definition())`).
C7 handles the first. Plan Task 4 Step 3 says only *"Replace `pi.State()` with
`renderState(ctx, cfg, pi)` at all ten state-passing sites"* — so site 77 becomes
`view.NewActionableView(renderState(ctx,cfg,pi), pi.Definition())` with the definition
**unchanged**. `runtime/view/instance_actionable.go:70-83` reads `def.Outgoing(t.NodeID)` and
emits `NextAction.Condition`, *"the routing expression guarding this flow"* — verbatim the
thing `authz.DisclosePolicy` is defined to cover (*"authorization policy and routing
expressions: the embedded definition and flow conditions"*, plan Task 1).

**MEASURED** (probe: plan's `PublicState` transcribed verbatim, closed set, definition passed
through as Task 4 prescribes; then deleted):

```
unauthenticated /actionable =>
{"instance_id":"i1","status":"running","open_tasks":[{"task_id":"t1","node_id":"approve",
 "state":"unclaimed","allowed_actions":[{"flow_id":"f1","target":"paid",
 "condition":"vars.amount > 10000 && vars.tier == \"platinum\""}]}]}
```

`candidates` and task `vars` ARE closed by `renderState` — so **the leak is invisible to any
test that greps for the secret string**. Spec §2.1 measured `/actionable` as leaking three
things: `claim.actor.attributes`, `candidates`, `allowed_actions`. The bundle closes two of
three and describes the site as closed.

**Compounding: plan test T1 is vacuous on exactly this field.** T1 asserts *"no variables, no
actors, no notes, **no policy**"* across the eleven. The plan's own "Fixture traps" box records
that `ApprovalProcess`'s flow `f2` has **no `Condition`** — so a T1 written on the standard
harness asserts the absence of a value the fixture never produces. The plan warns about this
trap for the actor fields and walks into it for the policy field.

**CONCRETE FIX.** Make the definition part of the one decision, not a per-site special case.
Add a sibling helper next to `renderState` and call it at BOTH sites:

```go
// renderDefinition withholds the process template from a caller the transport could not
// identify, unless DisclosePolicy widens it. The template carries every node's eligibility
// spec and every flow's routing expression.
func renderDefinition[R any](ctx context.Context, cfg CustomizeConfig[R], pi service.ProcessInstance) *model.ProcessDefinition {
    if identified(ctx, cfg) || cfg.Disclosure.Has(authz.DisclosePolicy) {
        return pi.Definition()
    }
    return nil
}
```
then `endpoints.go:77` becomes `view.NewActionableView(renderState(...), renderDefinition(...))`
and Task 4's `/snapshot` block collapses into the same call. `NewActionableView`'s godoc
already documents `def == nil ⇒ AllowedActions nil`, so no change is needed there.
⚠ Add to T1 a fixture whose flow **has** a `Condition` — the standard harness cannot falsify
this assertion.

---

## I2-C3 — CRITICAL — (C4 × C7) and (C1 × C7): the `/snapshot` reconstruction silently REVERSES `service.WithoutEmbeddedDefinition` for every identified caller, and C1 left the transport no way to see that setting.

**Pair.** C1 declared `service` untouched. C7 rebuilds the `/snapshot` document with
`service.NewProcessInstance(def, projected)`.

**What C1 does to C7's premises.** `service.NewProcessInstance` is a thin wrapper:
`service/instance.go:49-51` calls `newProcessInstance(def, st, **false**)`. The third
argument is `omitDefinition`, the stored `service.WithoutEmbeddedDefinition()` setting
(`service/options.go:128-146`, `service/service.go:153`). It is **unexported and has no
accessor** — `ProcessInstance.Definition()` returns `def` *"regardless"* (`instance.go:64-69`).
Because C1 forbids touching `service`, the transport cannot read it, and the only exported
constructor hard-codes the opposite value.

**MEASURED** (probe, then deleted):

```
service.NewProcessInstance(def, st) =>
{"instance_id":"i1",...,"definition":{"id":"d1","version":1,"nodes":[],"flows":null}}
```

**Concrete broken scenario.** A consumer sets `WithoutEmbeddedDefinition()` — today
`/snapshot` omits `definition`. After ADR-0190, every **identified** caller (and any caller
under `DisclosePolicy`) gets the template re-embedded, because C7 rebuilds through the
constructor that ignores the setting. The template is exactly what spec §2.1 measured as
sensitive (`definition.nodes[].eligible_roles`). **A disclosure ADR would ship a disclosure
regression on the one route it rewrites**, and it would fire for the population that
deliberately opted out.

**CONCRETE FIX — pick one, and record which:**
1. **Preferred, and it does not violate C1's spirit:** add a marshalling-policy-preserving
   exported constructor or option in `service`, e.g.
   `service.NewProcessInstanceLike(pi ProcessInstance, def *model.ProcessDefinition, st engine.InstanceState) ProcessInstance`
   that copies `omitDefinition` off the original. C1's real content is *"do not put the
   disclosure decision in `service`"*; adding a policy-preserving rebuild primitive keeps the
   decision in the transport. ⚠ This must then be stated in the ADR, because plan/ADR/spec all
   assert `service` is *"not modified at all"* — that quantifier becomes false and must be
   corrected rather than silently broken.
2. Do not rebuild at all: leave `/snapshot` marshalling `pi` and give `ProcessInstance` a
   projection method. Rejected in revision 1 for good reasons (§0.3) — but note revision 1's
   objection was to a *stamp* read inside `MarshalJSON`, not to a pure `WithState` rebuild.
3. Accept the regression and **document it as a second headline breaking change**. Weakest
   option; state it explicitly rather than discovering it in `/code-review`.

---

## I2-C4 — MAJOR — (C3 × C6): `DiscloseAll` is NOT a complete opt-out. Twenty `InstanceState` fields and five `Token` fields are classified *withheld* with **no category that restores them**.

**Pair.** C3 inverted the model to an allow-list of four categories; C6 routes all eleven
sites (including a consumer's custom `InstanceMapper`) through the projection.

**Derivation, from the plan's own table.** `InstanceState` withheld = 20. Restored by
`DiscloseVariables`: `Variables`, `StartVariables`, `RootCompensations`, `Scopes`,
`ArchivedCompensations` = 5. **Unreachable by any category = 15**: `Incidents`,
`PendingFinalErr`, `Compensating`, `Timers`, `ArmedEvents`, `Boundaries`,
`EventTriggeredSubprocesses`, `DeferredCompensationThrows`, `RecentCompensationCmdIDs`,
`CmdSeq`, `TokenSeq`, `TaskSeq`, `TimerSeq`, `ScopeSeq`, `IncidentSeq`.
`Token` withheld = 6; `Payload` restored under `DiscloseVariables`; **5 unreachable**:
`AwaitCommand`, `AwaitSignal`, `AwaitMessage`, `AwaitMessageKey`, `AwaitTimer`.
(`HumanTask`'s 5 withheld fields are all reachable — that half is fine.)

**MEASURED** — the plan's `PublicState` transcribed verbatim, `DiscloseAll`, marshalled
through `service.NewProcessInstance` (i.e. the `/snapshot` document):

```
BEFORE: {...,"variables":{"ssn":"111-22-3333"},
         "incidents":[{"id":"inc-1","kind":"IncidentAction","token_id":"t1","node_id":"n1",
                       "error":"boom: ssn=111-22-3333","attempts":3,...}],"started_at":...}
AFTER : {...,"variables":{"ssn":"111-22-3333"},"started_at":...}
```

`incidents` is gone under `DiscloseAll`. This is spec **open question 4** answered: **NO.**

**Three bundle claims falsified:**
- ADR Decision 6: *"`httpcore.DiscloseAll` restores the exact pre-ADR-0190 wire shape in one
  call."* False.
- Plan **T8** / Task 5 Step 2: *"a byte-comparison test that `WithDisclosure(DiscloseAll...)`
  reproduces the pre-change body exactly, on `/snapshot` especially"*. **This prescribed test
  cannot pass as written** — the implementer will be forced either to weaken it (and a
  weakened T8 pins nothing) or to discover the design gap during implementation.
- ADR **Residual "Error text"**: *"Withheld by default … but a consumer setting `DiscloseAll`
  gets it and no category isolates it."* Measured: `DiscloseAll` does **not** get it. The
  residual is wrong in the *safe* direction, but it is still a false statement of current
  design, and it hides the real problem — `incidents` is unrecoverable, so the operator who
  needs the failure message has no configuration that returns it.

**Interaction with C8 makes this worse, not neutral.** C8 defers admin gating to phase 3, so
there is no privileged path either. Under a deployment without ctx middleware,
`compensating.active_command_id` — which `service/instance.go:170-183` documents as *"what
makes a WEDGED instance findable"* and *"every escape verb requires naming it"* (ADR-0175) —
becomes unreachable through **every** configuration phase 1 offers.

**CONCRETE FIX.** Two changes, both small:
1. Add the missing categories so the allow-list is **surjective onto the withheld set**:
   `DiscloseIncidents` (covers `Incidents`, `PendingFinalErr`), `DiscloseOperational`
   (covers `Compensating`, `Timers`, `ArmedEvents`, `Boundaries`,
   `EventTriggeredSubprocesses`, `DeferredCompensationThrows`, `RecentCompensationCmdIDs`,
   the six `*Seq` counters), `DiscloseCorrelation` (covers `Token.Await*`). Then
   `DiscloseAll` is genuinely total.
2. **Machine-check surjectivity in the same guard as C5** (see I2-C5): assert that for every
   field in a `withheld` set there exists a category set under which `PublicState` returns it
   non-zero. Without this the next added category will drift the same way.

---

## I2-C5 — MAJOR — (C4 × C5): the totality guard and the projection are **independent transcriptions with nothing tying them together**, and the guard covers 4 structs while the projection copies 6 more struct types wholesale.

**Pair.** C4 is a hand-written literal in `runtime/view/public.go`. C5 is two hand-written
string slices in `runtime/view/classification_test.go`.

**What C5 does to C4's premises — and vice versa.** The guard asserts *field names* partition
into `publicX`/`withheldX`. It never calls `PublicState`. Consequences:

- A field **classified `public` but absent from the literal** ⇒ guard GREEN, field silently
  dropped from every response. The guard's own error text (*"add it to publicX or withheldX
  in this file AND to PublicState if public"*) is a **comment asking a human to remember** —
  the exact class of instruction §0.1 says revision 2 exists to eliminate.
- A field **classified `withheld` but present in the literal** ⇒ guard GREEN, field leaks.
- **Coverage gap.** The guard reflects over `InstanceState`, `Token`, `HumanTask`,
  `NodeVisit` — 4 types. The projection copies **six further struct types wholesale**:
  `engine.Scope` (`[]engine.Scope`, verified), `engine.CompensationRecord`
  (`[]engine.CompensationRecord`, verified) via `RootCompensations`/`Scopes`/
  `ArchivedCompensations`; `humantask.Claim`, `humantask.Completion`, `authz.Actor` (via
  `Candidates`/`Claim.Actor`) and `authz.AuthzSpec` (via `Eligibility`). A field added to any
  of those is disclosed under its category and **the guard stays green** — the precise
  fail-open C5 was built to remove. ADR Consequences states the property unqualified:
  *"A field added to `InstanceState` tomorrow is withheld by default and fails a guard until
  classified — the failure mode … is structurally removed."* It is removed for four types.

**Concrete broken scenario.** Someone adds `CompensationRecord.ActorAttributes` next quarter.
`DiscloseVariables` now also discloses actor attributes; `TestClassification_IsTotal` passes;
`TestPublicState_WithholdsEverySnapshotOfVariables` passes (closed set unaffected). Nobody
learns. This is structurally identical to how `Tasks[].Vars` and the three
`CompensationRecord.Input` sites survived revision 1's redactor.

**CONCRETE FIX — make the guard test the PROJECTION, not a parallel list.** Replace/augment
`TestClassification_IsTotal` with a behavioural totality test:

```go
// For each struct, build a value with EVERY exported field set non-zero (reflect +
// a non-zero filler), run PublicState under the closed set, and assert:
//   field in publicX  => non-zero in the output
//   field in withheldX => ZERO in the output
// Then, per category c, assert every withheld field is non-zero under some category set
// (the surjectivity check I2-C4 needs).
```
This ties the classification to the code by *value round-trip*, not by field-name agreement —
which is the ADR-0188 lesson already recorded in this repo: *guard VALUE round-trips, not
field sets*. Extend the reflected type list to the six nested types, or (better) drive it
recursively from `InstanceState` so no type list is maintained at all.
⚠ Plan Task 2 Step 5's ablation (*add `Scratch map[string]any` to `engine.InstanceState`*)
only exercises the top-level type. Add a second ablation that adds a field to
`engine.CompensationRecord` and observe the guard **stay green** — that is the finding, and it
must be recorded rather than skipped.

## I2-C6 — MAJOR — (C1 × C7) and (C7 × C9): the `/snapshot` reconstruction DISCARDS a consumer-supplied `ProcessInstance`'s own `MarshalJSON`.

**Pair.** C1/C9 keep `service` untouched and scope phase 1 to the transport, which the spec
uses to answer open question 3 (*a consumer-supplied `Service` is a supported shape*). C7
replaces `return pi` with `return service.NewProcessInstance(def, projected)`.

**What C7 does to C1's premise.** `service.ProcessInstance` is an **exported interface that
embeds `json.Marshaler`** (`service/instance.go:20-23`), and its godoc sells that on purpose:
*"the serialized shape is an internal detail (no exported DTO fields), so a consumer can embed
it in its own domain/DTO type and marshal with no transformation."* A consumer-supplied
`Service` returning its own `ProcessInstance` implementation therefore owns `/snapshot`'s wire
format today. After C7 the transport throws that implementation away and re-marshals through
the library's `instanceJSON`.

**Concrete broken scenario.** A consumer whose `Service` wraps instances in a type adding
`tenant_id` and `sla_deadline` to the snapshot document loses both keys on every request —
authenticated or not, `DiscloseAll` or not. There is no configuration that restores them,
because the substitution is unconditional. Spec open question 3 is therefore answered
*"no path where full state renders"* — correct — while missing the reciprocal defect the same
change introduces: **a path where the consumer's own rendering stops happening.**

**CONCRETE FIX.** Only reconstruct when the projection actually differs, and preserve the
consumer's marshaller otherwise:

```go
st := renderState(ctx, cfg, pi)
if identified(ctx, cfg) && cfg.Disclosure.Has(authz.DisclosePolicy) == false { /* unchanged path */ }
if !projectionApplied { return http.StatusOK, pi, nil }   // consumer's MarshalJSON survives
```
i.e. keep `return pi` verbatim on the full-fidelity branch, and reconstruct **only** on the
projected branch — where a consumer's custom marshaller would leak anyway. Document in
`SECURITY.md` that a custom `ProcessInstance` marshaller is bypassed on the projected branch,
since that residual cannot be removed. ⚠ This also shrinks I2-C3's blast radius to the
projected branch, but does **not** remove I2-C3 (the `DisclosePolicy` branch still rebuilds).

---

## I2-C7 — MAJOR — (C6 × C8): C6 applies an identity-keyed projection to the three admin render sites, while C8 defers every admin identity concept to phase 3 — and I2-C4 leaves no category to recover what the operator needs.

**Pair.** C6 wires `renderState` into `admin_endpoints.go:111` (`ResolveIncident`), `:121`
(`CancelInstance`) and `:514` (`ResolveCompensationStall`) — verified, all three call
`NewInstanceView(pi.State())` directly. C8 defers admin gating and audit to phase 3, so
nothing in phase 1 gives an admin route an actor.

**What C8 does to C6's premise.** ADR-0095's admin-by-composition — which ADR-0190 Decision 0
explicitly reaffirms as *unchanged* — means the consumer protects admin routes by
**composition**: their own router group, their own middleware, a reverse proxy, a network
boundary. **None of those necessarily calls `authz.ContextWithActor`.** The stdlib group
comment says so in as many words (`stdlib/groups.go:206-208`: *"Mount AdminRoutes only onto a
router group already protected by your auth middleware"* — protected, not *identified*). So
the ADR-0095-blessed deployment shape yields, at every admin render site, **actor absent ⇒
public projection**.

**Concrete broken scenario, and it is the sharp one.** ADR-0175's operator escape from a
stalled compensation walk requires naming `active_command_id`. That value reaches the operator
through exactly one document: `/snapshot`'s `compensating` block, which
`service/instance.go:170-183` documents as *"what makes a WEDGED instance findable"* and
*"every escape verb requires naming it"*. `InstanceState.Compensating` is classified
**withheld with no category** (I2-C4), so after phase 1 a proxy-authenticated operator:
(a) cannot read the command id from `/snapshot`, and (b) has no `WithDisclosure` setting that
returns it. **ADR-0190 phase 1 breaks ADR-0175's escape workflow** for that deployment shape.
The same applies to `incidents[]`, which `ResolveIncident` exists to act on.

**CONCRETE FIX.** Three parts, and the first two are already required by other findings:
1. Fix I2-C1 so a `WithRequestActor` deployment is seen as identified.
2. Fix I2-C4 so `Compensating`, `Incidents` and the operational fields are reachable
   (`DiscloseIncidents` / `DiscloseOperational`).
3. **Give admin routes a distinct default.** ADR-0095 makes admin routes default-*absent*;
   a consumer who mounted them has already made the trust decision. Have `AdminRoutes`
   seed `cfg.Disclosure` to the full set unless the consumer overrides it — i.e. mounting
   admin routes IS the disclosure decision, exactly as it is already the reachability
   decision. State this in the ADR as a deliberate asymmetry between `InstanceRoutes` and
   `AdminRoutes`; without it, phase 1 makes admin routes strictly less useful than the
   unauthenticated instance routes they exist to supersede.

---

## I2-C8 — MAJOR — (C2 × C8): the delivered guarantee is "SOME identity is present", not "this caller may read this instance" — and because the gate is deferred, ADDING authentication middleware in preparation for phase 2 INCREASES disclosure at phase 1.

**Pair.** C2 keys fidelity on actor presence. C8 defers the operation gate to phase 2.

**What C8 does to C2's premise.** C2 is defended in §0.4 on the ground that keying on the gate
outcome would let an empty `AuthzSpec` re-open the hole — correct, and measured. But the
alternative it chose delivers a *different* property than the ADR's Positive section claims.
*"All eleven render entry points close at once"* is true only against **anonymous** callers.
With C8 there is no per-instance authorization anywhere in phase 1, so any authenticated
principal — the lowest-privileged employee, the ADR-0189-blessed kiosk claimant
`{ID:"", Roles:["kiosk"]}` (`humantask/validate.go:24`), any actor a permissive middleware
attaches — reads full variables of **every instance by id** across all eleven endpoints.

**The perverse incentive is the finding.** Today (pre-0190) a deployment with no middleware
and a deployment with middleware disclose the same thing: everything. After phase 1 they
diverge — the middleware-less one closes, and the middleware-ful one stays fully open with the
*appearance* of having been secured. A consumer who does the right thing in preparation for
phase 2 (installs authentication) gets **no disclosure benefit at all** until phase 2 lands,
and may reasonably read the ADR's Positive section as saying otherwise.

**This is not an argument to revert C2** — §0.4's measurement stands. It is an argument that
the ADR's **Consequences must state the delivered property in one sentence** and that phase 2
is not optional follow-up work.

**CONCRETE FIX.**
1. ADR Consequences, replacing *"All eleven render entry points close at once"*:
   *"All eleven render entry points close **against callers the transport cannot identify**.
   Phase 1 draws no distinction between identified principals: any authenticated caller reads
   any instance in full. The per-caller distinction is phase 2's operation gate, and phase 1
   is not a substitute for it."*
2. `SECURITY.md` must carry the same sentence, next to the `DiscloseAll` note.
3. ADR **Residual "A permissive `RequestActorFunc`"** is currently false as written (see
   I2-C1) — restate it as *"any authenticated principal, however weak, receives full
   fidelity; ADR-0189 deliberately blesses `{ID:"", Roles:["kiosk"]}`"*, which is the true and
   larger version of the same hazard.

---

## I2-C9 — MAJOR — (C4 × C6): the projection is handed to a consumer's `InstanceMapper`, where `InstanceState`'s 17 exported METHODS now return silently WRONG answers, not merely fewer fields.

**Pair.** C4 made the projection a real `engine.InstanceState` missing 20 of 31 fields; C6
routes it into `cfg.InstanceMapper`, arbitrary consumer code, at six of the eleven sites.

**What C6 does to C4's premise.** ADR Decision 4 ⚠ and the plan both frame the hazard as
*"must never be fed back into the engine"* — an engine-internal concern. But `InstanceState`
carries **17 exported methods** (verified: `HasArmedTimers`, `TimerWaiters`,
`TimerRecordWaiters`, `TimerBoundaryWaiters`, `TimerArmedEventWaiters`,
`TimerEventSubprocessWaiters`, `TimerTokenWaiters`, `MessageWaiters`,
`MessageBoundaryWaiters`, `MessageArmedEventWaiters`, `MessageEventSubprocessWaiters`,
`SignalWaiters`, `SignalBoundaryNames`, `SignalArmedEventNames`,
`SignalEventSubprocessNames`, `TaskByID`, `Clone`), and they read exactly the fields the
projection drops — `Timers`, `ArmedEvents`, `Boundaries`, `EventTriggeredSubprocesses`.

**Concrete broken scenario.** A consumer's mapper renders `"waiting_on": st.SignalWaiters()`
or `"has_timers": st.HasArmedTimers()`. On an unidentified read these return `nil` / `false` —
**a false statement about the instance**, not an omitted field. The ADR's Consequences
(*"renders fewer on an unidentified read"*) describes only the field case. `TaskByID` is worse:
it returns a task whose `Vars`, `Claim`, `Completion` and `Candidates` are nil, which reads as
"unclaimed and empty" rather than "withheld".

**CONCRETE FIX.**
1. Make the deficit **detectable**. Since `PublicState` may not add a field to
   `engine.InstanceState` (that would itself need classifying), carry the signal in the
   mapper's contract instead: change `InstanceMapper` to
   `func(engine.InstanceState, authz.DisclosureSet) any` — or add a sibling
   `WithDisclosureAwareInstanceMapper` — so a mapper can branch. ⚠ Changing the existing
   signature is a breaking change to a documented public option and must be listed with the
   other headline breaks, not slipped in.
2. Whichever is chosen, `CustomizeConfig.InstanceMapper`'s godoc and `SECURITY.md` must say:
   *"On an unidentified read the mapper receives a render-only projection. Methods on it —
   `HasArmedTimers`, `SignalWaiters`, `TaskByID`, … — answer from the projection, not from the
   instance."*
3. Add a test: a mapper that calls `st.HasArmedTimers()` on a state that HAS armed timers,
   asserting the documented behaviour explicitly rather than leaving it to be discovered.

---

## I2-C10 — MINOR — (C1 × C3): C1's whole argument is that disclosure is a TRANSPORT concept, and C3 then puts the disclosure vocabulary in `authz` — a package the engine core depends on, in the same task that adds a purity guard to it.

**Pair.** C1: *"'An unidentified caller' is a **transport** concept … `service` is not modified
by this decision at all."* C3/plan Task 1: `authz.DisclosureCategory`, `authz.DisclosureSet`,
in `authz/disclosure.go` — plus `authz/purity_test.go` in the same commit, whose stated reason
is *"`authz` is imported by `engine`, so anything added here propagates into the engine core."*

**The incoherence.** The task simultaneously argues that `authz` must stay minimal and enlarges
its responsibility with a concept D6 says belongs to the transport. No import constraint forces
the placement: `runtime/view` and `transport/http/httpcore` both already import `authz`, and
`view.PublicState(st, d)` would type-check identically with `d` declared in `runtime/view`.

**Consequence if left.** `authz`'s exported surface grows a transport vocabulary that the
engine, the service and every embedded consumer see and must ignore — the same "server concern
leaking into the core" CLAUDE.md forbids, one package removed.

**CONCRETE FIX.** Declare `DisclosureCategory` / `DisclosureSet` in **`runtime/view`**
(alongside `PublicState`, which is the only consumer that interprets them) and re-export the
four constants from `httpcore` for ergonomics, exactly as the adapters already re-export
`WithRequestActor`. Keep `authz/purity_test.go` — it is worth having on its own merits, and
with the type moved it no longer sits beside the change it argues against.
⚠ If the authors keep the type in `authz`, the ADR must say *why* in one sentence, because
D6's stated rationale currently argues the opposite.

## I2-C11 — MAJOR — (C5 × C6): the eleven wiring SITES are still a hand-maintained enumeration. C5 made the FIELD list machine-checked and left the SITE list exactly as fragile as the one that killed revision 1.

**Pair.** C5 replaced enumeration with classification totality. C6 depends on a list of eleven
sites, written out in spec §2.2, ADR Context, plan "The classification" preamble and plan
Task 4 — four copies, all by hand, several with line numbers.

**What C5 does NOT do for C6.** Spec §0.1 concludes: *"**Revision 2 contains no hand-maintained
list of sensitive fields.**"* True, and precisely scoped — but ADR Decision 5's summary and the
Consequences generalise it (*"the failure mode that produced four different answers to one
question is structurally removed"*). It is removed for **fields** on four types. It is **not**
removed for **render sites**, and the render-site enumeration is the one that actually failed
in revision 1: *"Render paths went 3 → 11 entry points across 4 mechanisms … revision 1 wrote a
guard test **because** the enumeration had already rotted once, and was still wrong."*

**Concrete broken scenario.** A twelfth render site lands next quarter — a new admin verb, a
new instance sub-resource — written as `NewInstanceView(pi.State())` by copy-paste from
`admin_endpoints.go:121`. Every test in the bundle passes: T1–T3 iterate the eleven the author
wrote down; T7 checks fields, not sites; T10 checks gin/fiber match stdlib *on the eleven*. The
new site discloses full variables to anonymous callers and nothing goes red. That is revision
1's failure, reproduced one layer up.

**CONCRETE FIX — machine-check the sites, in the spirit C5 established for fields.** A test in
`transport/http/httpcore` that parses the package's own non-test sources (`go/ast`, no new
dependency — the repo already runs `go list` from a test in plan Task 1) and asserts:

```
every call to pi.State() / NewInstanceView( / NewActionableView( / a bare `return …, pi,`
inside transport/http/httpcore appears in an ALLOW-LISTED set of enclosing functions
```
with the error text naming the offending function and telling the author to route it through
`renderState`. Ablate it by adding a twelfth raw `pi.State()` site and observing RED — that
ablation is cheap and is the one that would have caught revision 1.
⚠ Plan Task 4 Step 5's prescribed mutation (revert `endpoints.go:94`, confirm only its subtest
reds) proves the **table discriminates**; it proves nothing about a site that is not in the
table. Both are needed.

---

## I2-C12 — MINOR — (C1 × C8): the phase-2 constraint *"exactly where `httpcore.RequestActor` is called today"* names a location that exists on 3 routes, not on the surface phase 2 must gate.

**Pair.** C8 deferred the gate but recorded constraints for phase 2. C1's transport placement
makes the transport the natural pre-decode gate site.

**The false premise.** Spec §3 D2 constraint 2: *"export the evaluation so the transport can
call it pre-decode, **exactly where `httpcore.RequestActor` is called today**"*. Verified:
`httpcore.RequestActor(` is called at **9 sites total** — `stdlib/groups.go:138,170,191`,
`gin/groups.go:164,191,218`, `fiber/groups.go:146,168,191` — i.e. the three human-task verbs in
each of three adapters. It is called on **none** of the 8 ungated `Service` operations or the
12 admin operations the gate must cover. Phase 2's author reading that sentence will look for a
seam that is not there.

**CONCRETE FIX.** Restate as: *"…call it pre-decode, in the same position the three task
handlers already call `httpcore.RequestActor` — **a position phase 2 must first create at the
other route handlers**, since `RequestActor` is called at 9 sites covering 3 routes today."*
⚠ This is also an argument for adopting **I2-C1 fix part 1 now**: routing `renderState` through
`cfg.RequestActor` establishes exactly the per-request resolution phase 2 needs on the other
routes, instead of leaving phase 2 to add it and then reconcile two identity sources.

---

# The full pairwise grid

36 unordered pairs over C1…C9. Every cell is either a finding ID or "no interaction" with its
reason. Survivor-vs-survivor pairs (C3–C7 against each other, and C8/C9 against everything) are
included — the brief warns that is where an earlier author's grid lost three Criticals.

| pair | verdict |
|---|---|
| C1×C2 | **I2-C1 (CRITICAL)** — two identity reads that disagree |
| C1×C3 | **I2-C10 (MINOR)** — transport vocabulary placed in `authz` |
| C1×C4 | no interaction — the fresh literal lives in `runtime/view`, which C1's "`service` untouched" rule does not constrain; cross-package construction re-verified to compile in this worktree |
| C1×C5 | no interaction — the guard is a `runtime/view` test over engine/humantask types C1 does not move |
| C1×C6 | no interaction beyond C1×C2 — a transport-local helper is exactly what C1 asked for |
| C1×C7 | **I2-C3 (CRITICAL)** re-embeds the definition despite `WithoutEmbeddedDefinition`; **I2-C6 (MAJOR)** discards a consumer's `MarshalJSON` |
| C1×C8 | **I2-C12 (MINOR)** — the phase-2 seam is claimed to exist where it does not |
| C1×C9 | **I2-C6** — open question 3 is answered favourably for the mapper paths, and the reciprocal defect it introduces on `/snapshot` was missed |
| C2×C3 | folded into **I2-C4** — presence-keying makes categories inert for identified callers, so no "closed for everyone" configuration exists; the config axis is anonymous-only |
| C2×C4 | no interaction — `PublicState` takes no actor; the keying happens one level up in `renderState` |
| C2×C5 | no interaction — the classification guard is actor-free |
| C2×C6 | **I2-C1** — the shared helper is where the wrong identity source is read |
| C2×C7 | folded into **I2-C1** — plan Task 4's `/snapshot` block calls `ActorFromContext` a *second* time; unify behind one `identified(ctx,cfg)` or the two can diverge after the fix |
| C2×C8 | **I2-C8 (MAJOR)** — presence ≠ permission; adding auth increases disclosure until phase 2 |
| C2×C9 | **I2-C1** — transport-only scope forces the signal to be transport-visible, and `ActorFromContext` is not, under a custom resolver |
| C3×C4 | no interaction — allow-list polarity and fresh-literal construction agree; the literal names only public fields and widens per category |
| C3×C5 | **I2-C5 (MAJOR)** — classification and projection are independent transcriptions |
| C3×C6 | **I2-C4 (MAJOR)** — `DiscloseAll` is not surjective onto the withheld set |
| C3×C7 | no NEW interaction — `/snapshot`'s definition gate consumes `DisclosePolicy` consistently; its `incidents`/`compensating` gap is I2-C4 |
| C3×C8 | no interaction — no deferred decision consumes a `DisclosureCategory` |
| C3×C9 | no interaction — categories are transport-scoped by construction |
| C4×C5 | **I2-C5** — the guard covers 4 types; the projection copies 6 more wholesale |
| C4×C6 | **I2-C9 (MAJOR)** — 17 exported `InstanceState` methods return wrong answers to a consumer's mapper |
| C4×C7 | **I2-C3** — the reconstruction cannot preserve marshalling policy |
| C4×C8 | no interaction — the deferred gate consumes the actor, never the projection |
| C4×C9 | no interaction — the mechanism is inert outside the render path |
| C5×C6 | **I2-C11 (MAJOR)** — fields are machine-checked, SITES are still a hand-maintained list of eleven |
| C5×C7 | folded into **I2-C5** — the guard reflects over engine types while `/snapshot` marshals through `service`'s unexported `instanceJSON`/`tokenJSON`/`taskJSON`, a wire surface no guard sees |
| C5×C8 | no interaction — nothing deferred is classified |
| C5×C9 | no interaction |
| C6×C7 | **I2-C2 (CRITICAL)** — the one helper carries state, so C7's definition rule never reaches `/actionable` |
| C6×C8 | **I2-C7 (MAJOR)** — admin sites projected, no admin identity concept until phase 3 |
| C6×C9 | no interaction — all eleven sites are inside the transport, which is the shrunken scope |
| C7×C8 | folded into **I2-C7 / I2-C4** — `/snapshot` is the only route carrying `compensating`, and phase 3 provides no alternative |
| C7×C9 | **I2-C3 / I2-C6** — the reconstruction is the one place transport-only scope forces the transport to re-do `service`'s job |
| C8×C9 | see below — the "phase 1 is independently coherent and independently shippable" claim |

**C8×C9, stated rather than folded.** ADR Consequences and spec D8 both assert *"Phase 1 is
independently coherent and independently shippable."* Against the findings above that holds
only for the deployment shape where (a) authentication middleware calls
`authz.ContextWithActor`, (b) admin routes are mounted behind that same middleware, and (c) no
consumer-supplied `Service`, custom `ProcessInstance`, custom `InstanceMapper` or
`WithoutEmbeddedDefinition` is in use. That is one shape out of several the library documents
as supported. Phase 1 is shippable — after I2-C1, I2-C2, I2-C3 and I2-C4 are folded in; it is
not shippable as written.

---

# Summary

| ID | sev | pair | one line |
|---|---|---|---|
| I2-C1 | **CRITICAL** | C1×C2, C6×C2 | `renderState` reads `ActorFromContext` while ADR-0189 reads `cfg.RequestActor`; a `WithRequestActor` deployment is authenticated (200, MEASURED) and simultaneously invisible ⇒ all eleven sites redacted for authenticated users |
| I2-C2 | **CRITICAL** | C6×C7 | the definition rule reaches `/snapshot` only; `/actionable` still emits flow `condition` (MEASURED) — a `DisclosePolicy` value — and plan T1's fixture cannot falsify it |
| I2-C3 | **CRITICAL** | C1×C7, C4×C7 | `service.NewProcessInstance` hard-codes `omitDefinition=false` (MEASURED), so `/snapshot` re-embeds the template for consumers who set `WithoutEmbeddedDefinition` |
| I2-C4 | MAJOR | C3×C6 | 15 `InstanceState` + 5 `Token` withheld fields have **no category**; `DiscloseAll` drops `incidents` (MEASURED) ⇒ ADR D6, plan T8 and the "Error text" residual are all false |
| I2-C5 | MAJOR | C3×C5, C4×C5 | guard and projection are independent transcriptions; guard covers 4 types, projection copies 6 more wholesale ⇒ the revision-1 fail-open survives one level down |
| I2-C6 | MAJOR | C1×C7, C7×C9 | the `/snapshot` rebuild discards a consumer-supplied `ProcessInstance.MarshalJSON` unconditionally |
| I2-C7 | MAJOR | C6×C8 | admin render sites get the projection while phase 3 owns admin identity ⇒ ADR-0175's `active_command_id` escape becomes unreachable in ADR-0095's blessed shape |
| I2-C8 | MAJOR | C2×C8 | delivered property is "some identity", not "may read"; installing auth *increases* disclosure until phase 2 — must be stated in Consequences |
| I2-C9 | MAJOR | C4×C6 | 17 exported `InstanceState` methods answer from the projection ⇒ a consumer mapper gets false statements, not fewer fields |
| I2-C10 | MINOR | C1×C3 | a transport concept declared in `authz`, in the same task that guards `authz` against growth |
| I2-C11 | MAJOR | C5×C6 | fields machine-checked, **sites not** — a twelfth render site is silent, which is revision 1's exact failure one layer up |
| I2-C12 | MINOR | C1×C8 | *"exactly where `RequestActor` is called today"* = 9 sites / 3 routes, not the surface phase 2 gates |

**Three CRITICALs, and all three are holes the revision's own fixes opened in each other** —
C2's new signal against C1's new placement (I2-C1), C7's new reconstruction against C6's new
single helper (I2-C2), and C7's new reconstruction against C1's untouched `service` (I2-C3).
Each change is defensible alone; none of the three defects exists in revision 1.

**Probes.** Four executed in worktree `wt-inter2` at `a161f347`, all deleted, tree restored:
custom-resolver context visibility (`transport/http/stdlib`), `DiscloseAll` byte-identity and
definition re-embed (`runtime/view/zzprobe`, plan's `PublicState` transcribed verbatim),
`/actionable` policy disclosure, and an exported-field/type census of the four classified
structs (31/13/11/6 — the plan's counts confirmed).
