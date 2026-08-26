# ADR-0190 revision 2 — round-2 adversarial audit, FAILURE-MODE lens

**Date:** 2026-08-27 · **Worktree:** detached at `a161f347` (bundle commit)
**Bundle audited:** `docs/specs/2026-08-26-route-group-authorization-posture-design.md`,
`docs/adr/0190-authorization-is-gated-by-policy-not-by-authentication.md`,
`docs/plans/2026-08-26-route-group-authorization-posture.md`
**Mandate:** missing failure modes, edge cases, migration gaps, operational hazards.
Step 0 (bundle present in worktree) PASSED.

Severity: **CRITICAL** = ships a broken or unsafe outcome / the decision is unimplementable
as written · **MAJOR** = a real failure mode with no answer in the bundle · **MINOR** = a
correctness or honesty gap in the documents.

Probes were throwaway Go tests run in the worktree and deleted; their output is pasted.

---

## FM-1 — CRITICAL — attacks **D6** (`DiscloseAll` restores the prior wire shape) and plan test **T8**

**Claim under attack.** ADR Decision 6: *"`httpcore.DiscloseAll` restores the exact
pre-ADR-0190 wire shape in one call."* Plan T8: *"`WithDisclosure(DiscloseAll)` reproduces
the pre-ADR-0190 body byte-for-byte."*

**It cannot.** There are exactly four categories — `DiscloseVariables`, `DiscloseActors`,
`DiscloseNotes`, `DisclosePolicy` — and the plan's `PublicState` body restores fields under
only three of them. Three exported `InstanceState` fields that the CURRENT `/snapshot`
document renders are restored by **no category at all**:

| withheld field | wire key on `/snapshot` | restored by |
|---|---|---|
| `Incidents` | `incidents[]` (`service/instance.go:129`, populated at `instance.go:305-318`) | **nothing** |
| `Compensating` | `compensating{active_command_id, since, scope_id}` (`instance.go:136`, `instance.go:321-330`) | **nothing** |
| `PendingFinalErr` | (not on the wire — harmless) | nothing |

So `DiscloseAll` produces a `/snapshot` body that is **missing `incidents` and
`compensating`** relative to today's. T8 as prescribed is unsatisfiable; an implementer will
either weaken it to "not byte-for-byte" (silently retiring the opt-out guarantee) or invent
categories the ADR does not authorise.

**Scenario.** A consumer with no authentication middleware upgrades and sets
`WithDisclosure(DiscloseAll...)` on the strength of the ADR's "one-call opt-out". Their
incident dashboard, which polls `/snapshot` and reads `incidents[]`, goes permanently blank.
Nothing in the release notes predicts it, because the ADR says the shape was restored.

**Evidence (executed).** `service/instance.go:264` `newInstanceJSON` reads `st.Incidents` and
`st.Compensating.ActiveCmdID` directly; the plan's classification table
(`docs/plans/…:"engine.InstanceState — 31 exported fields"`) places both in the withheld
column, and the plan's `PublicState` body restores only
`Variables, StartVariables, RootCompensations, Scopes, ArchivedCompensations` (under
`DiscloseVariables`) plus per-task fields. Field enumeration re-derived by reflection, not
read: `InstanceState` has **31 exported / 1 unexported** fields — see FM-15 for the output.

**Fix.** Either (a) add a fifth category `DiscloseDiagnostics` covering `Incidents`,
`Compensating` and `PendingFinalErr`, and include it in `DiscloseAll`; or (b) make
`DiscloseAll` a distinct sentinel that short-circuits `PublicState` entirely
(`if d.Has(DiscloseAll) { return st }`), which is the only construction that can honestly
claim byte-identity. (b) is smaller and provably total. Then restate T8 against whichever is
chosen, and mutation-verify it by removing one restored field.

---

## FM-2 — CRITICAL — attacks **D3** (fidelity keys on actor presence) and **D7** (the three verbs need no special case)

**Claim under attack.** ADR Decision 7: *"The three human-task verbs need no special case:
ADR-0189 authenticates them, so an actor is present and they render full fidelity."* Spec §2.2
repeats it. The word "present" is doing two different jobs: present as a **local variable**,
and present on the **`context.Context`**. `renderState` reads the second.

**They are not the same, and the divergence is a first-class supported configuration.**
`httpcore.RequestActorFunc`'s own godoc (`transport/http/httpcore/seam.go:18-24`) says:
*"Override it with [WithRequestActor] when the identity lives **somewhere the context does not
reach**."* Every adapter exports the alias (`stdlib/options.go:64`, `gin/options.go:89`,
`fiber/options.go:72`). The adapters pass `req.Context()` **unmodified** to the endpoint
(`stdlib/groups.go:138,152` — resolve, then `httpcore.ClaimTask(req.Context(), …, actor)`);
nothing ever calls `authz.ContextWithActor` with the resolved actor.

**Scenario.** A consumer authenticates from an mTLS peer certificate / a fasthttp header /
a gRPC-gateway metadata bag — none reachable from `context.Context` — and wires
`stdlib.WithRequestActor(fromClientCert)`. After ADR-0190 **every one of the eleven entry
points renders the public projection to every caller, including fully authenticated
administrators**, because `authz.ActorFromContext` never returns ok. There is no error, no
log, no 401 — responses just silently lose their variables.

**Evidence (executed).** Probe mounted `stdlib.Mount(mux, svc, stdlib.WithRequestActor(…))`
with a resolver that authenticates alice as a manager and records whether the context carries
an actor:

```
status=200
authz.ActorFromContext(req.Context()) ok = false
=> renderState(ctx,...) would take the PUBLIC-PROJECTION branch
```

The claim succeeded (200) — the caller *is* authenticated — and the disclosure signal still
reads "unidentified".

**This also refutes the bundle's own argument for revision 2 over revision 1.** Spec §D8:
*"Revision 1's phase 1 had only two reachable configurations — everyone blind, or everyone
sees everything."* For every `WithRequestActor` consumer, revision 2 has exactly the same two.

**Fix.** Make the transport the single source of the presence signal instead of reading the
context twice. Have the adapters (or a shared `httpcore` helper) call the configured
`RequestActor` **once per request for every route**, treat `ErrUnauthenticated` as "no actor"
rather than 401 on the read routes, and carry the outcome forward explicitly — either by
re-injecting `authz.ContextWithActor(ctx, actor)` before calling the endpoint, or by passing a
`bool identified` alongside. Re-injection is the smaller change and additionally fixes FM-13.
Whichever is chosen, the ADR must stop asserting "ADR-0189 authenticates them, so an actor is
present"; that sentence is false for a documented configuration.

---

## FM-3 — CRITICAL — attacks **D7** (one helper at all eleven entry points), plan Task 3 + Task 4

**`renderState(ctx, cfg, pi)` cannot be called from any of the eleven sites as the code
stands, and the plan never says what it costs to make it callable.**

Two independent blockers:

1. **No render site has a `cfg`.** Derived mechanically (`grep -n "^func " ` over both files),
   every exported endpoint takes only what it needs:
   `GetInstance(ctx, svc, id, mapper)`, `GetInstanceSnapshot(ctx, svc, id)`,
   `GetActionableView(ctx, svc, id)`, `StartInstance(ctx, svc, in, mapper)`,
   `DeliverSignal(ctx, svc, id, in, mapper)`, `Claim/Complete/ReassignTask(…, mapper, actor)`,
   `ResolveIncident(ctx, svc, instanceID, incidentID, in)`,
   `CancelInstance(ctx, svc, instanceID)`,
   `ResolveCompensationStall(ctx, svc, instanceID, in)`. **Not one receives a
   `CustomizeConfig`.** The `InstanceMapper` is threaded as a bare function precisely so the
   config stays in the adapter.
2. **The signature the plan writes cannot type-check.** `renderState(… cfg CustomizeConfig[any] …)`
   — `CustomizeConfig[any]`, `CustomizeConfig[*http.ServeMux]`, `CustomizeConfig[ginlib.IRouter]`
   and `CustomizeConfig[fiberlib.Router]` are four **distinct, unrelated** types; Go generics
   are not covariant and there is no conversion. The adapters hold the last three.

**Consequence the bundle does not state.** Making `renderState` reachable means adding a
parameter to **eleven exported functions in the public `httpcore` API** and updating all
three adapters' call sites. That is a compile-breaking change for every consumer who calls
`httpcore.GetInstance` directly (a supported shape — the package is exported and documented as
"pure per-endpoint logic"). The ADR's **Negative** consequences list only response-shape
changes and the `InstanceMapper` behaviour change. A source-incompatible public API break is a
larger headline than either, and it is missing.

**Fix.** Do not thread the config. Thread the **value**: `authz.DisclosureSet` is non-generic.
Add one parameter `d authz.DisclosureSet` (or, better, fold it into a small non-generic
`RenderPolicy{Disclosure authz.DisclosureSet; Identified bool}` which also carries FM-2's
fix) to the eleven functions, and record the API break explicitly in the ADR's Consequences
and in `SECURITY.md`. Re-derive the "18 failures in 3 packages" breakage net afterwards — it
was measured against a design that did not change these signatures.

---

## FM-4 — CRITICAL — attacks **D7** (`/snapshot` renders a reconstructed `service.NewProcessInstance`)

**The reconstruction silently defeats `service.WithoutEmbeddedDefinition()` (ADR-0144), and it
does so in the DISCLOSING direction.**

`service.NewProcessInstance` hardcodes `omitDefinition=false` (`service/instance.go:49-50`);
the engine's own path passes `e.omitDefinition` (`service/service.go:561-562`). The option's
godoc says so outright: *"[NewProcessInstance] — which fabricates an instance outside the
engine — **always embeds**"* (`service/options.go:143-145`).

**Scenario.** A consumer runs `WithoutEmbeddedDefinition()` because the template is the larger
half of every document and their UI already holds it. After ADR-0190, `/snapshot` re-embeds
the whole template on every read — for authenticated callers and for `DisclosePolicy` mounts
alike. Payload regression, and a disclosure regression: the embedded definition carries every
node's eligibility spec, the exact `DisclosePolicy` content this record exists to withhold.

**Evidence (executed).**

```
engine built WithoutEmbeddedDefinition: /snapshot keys TODAY =
  [def_id def_version ended_at history instance_id started_at status tokens]
plan's reconstruction keys                                   =
  [def_id def_version definition ended_at history instance_id started_at status tokens]
Definition() non-nil? true
```

**Fix.** `/snapshot` must not reconstruct through the exported constructor. Either
(a) export a `service.NewProcessInstanceWithout(def, st)` / an option carrying the
marshalling policy, and have the transport read the instance's current policy; or
(b) leave `/snapshot` passing `pi` through and give `ProcessInstance` a
`WithState(engine.InstanceState) ProcessInstance` method that clones the receiver with a new
state, preserving `omitDefinition`. (b) keeps the policy where it already lives and is one
method. Note (b) touches `service`, which contradicts D2's "service is not modified" — that
contradiction is real and must be adjudicated, not papered over: the alternative is a
silent ADR-0144 regression.

---

## FM-5 — MAJOR — attacks **D6**'s classification and **D8**'s deferral, together

**The disclosure change removes the only source of the arguments that the still-ungated
operator verbs REQUIRE, and no category restores them.**

`httpcore.ResolveCompensationStall` refuses a missing argument with a message that names its
source verbatim (`transport/http/httpcore/admin_endpoints.go:497-499`):

```go
return 0, nil, fmt.Errorf("%w: command_id is required — read it from the instance's compensating.active_command_id", ErrBadInput)
```

`compensating.active_command_id` is rendered **only** by `/snapshot`, from
`InstanceState.Compensating` — withheld, and restored by no category (FM-1). The same holds
for `ResolveIncident`, whose `incidentID` path segment is readable only from `/snapshot`'s
`incidents[]`.

**Scenario.** ADR-0175 shipped `compensating` specifically because *"a stalled walk never
dispatches again … and would otherwise be invisible"* (`service/instance.go:169-176`). After
ADR-0190, in any deployment without `authz.ContextWithActor` middleware — which D1 explicitly
permits and D8 explicitly leaves ungated — the wedged instance becomes invisible again, the
escape verbs stay callable, and their required argument is unobtainable. The operation is
reachable; its input is not. `DiscloseAll` does not help (FM-1).

**The bundle is not honest about this.** The ADR's **Negative** consequences say only that
"all eleven entry points change shape by default" and that phases 2–3 leave 20 operations
ungated. Neither sentence says *the phase-1 change breaks a shipped operator workflow whose
own error message documents the broken path*.

**Fix.** Classify `Incidents` and `Compensating` as **public** (they carry no actor identity;
`Incident.Error` is the one variable-bearing member — see the residual in spec §4 — and can be
blanked unless `DiscloseVariables` is set), or gate them behind the new
`DiscloseDiagnostics` category from FM-1 and say in `SECURITY.md` that operator tooling needs
it. Add a test pinning that `compensating.active_command_id` survives whatever posture the
escape verbs are documented against.

---

## FM-6 — MAJOR — attacks **D7** (`def` nil'd only at `endpoints.go:65`)

**`/actionable` keeps disclosing routing expressions under the closed baseline.**

Task 1's godoc defines `DisclosePolicy` as *"authorization policy and routing expressions: the
embedded definition **and flow conditions**"*. `GetActionableView` (`endpoints.go:70-79`)
calls `view.NewActionableView(pi.State(), pi.Definition())`, and `NewActionableView` copies
`f.Condition` into `NextAction.Condition` for every outgoing flow
(`runtime/view/instance_actionable.go:76-84`), rendered as `allowed_actions[].condition`.

Plan Task 4 Step 3 nils `def` **only** for `endpoints.go:65` (`/snapshot`). `endpoints.go:77`
is listed as a wired site but gets `renderState` applied to the **state** argument only; the
`def` argument is untouched. Result: an unidentified caller still receives every gateway
condition on the task's outgoing flows, e.g.
`{"flow_id":"f_reject","condition":"vars.amount > 10000","target":"manual-review"}`.

Spec §2.1's own measurement lists `allowed_actions` among what `/actionable` discloses today,
and T1 asserts "no policy" at all eleven sites — so T1 will fail at this site and the
implementer will have to invent the fix mid-implementation.

**Fix.** Apply the same `def`-nilling rule at `endpoints.go:77`: build the `def` argument
through one helper (`renderDefinition(ctx, d, pi) *model.ProcessDefinition`) shared by
`/snapshot` and `/actionable`, so the policy decision is made once rather than at two sites
one of which was forgotten. Add the site to the plan's Task 4 table with an explicit
"def **and** state" column, because the table's current "sites" column hides that two sites
carry two arguments.

---

## FM-7 — MAJOR — attacks **D4/D6**: the "structural baseline" is an oracle over the withheld variables

**Nothing in the bundle asks whether the fields it keeps let a caller RECONSTRUCT the fields
it drops.** They do.

The public set keeps `Tokens[].NodeID`, `History[].NodeID`, `History[].EnteredAt/LeftAt`,
`Status` and `Tasks[].NodeID`. In a BPMN engine, an exclusive gateway's chosen branch is a
**pure function of the process variables** (CLAUDE.md: *"Gateways … read token variables (via
`expr`) to decide routing"*). So the node trajectory *is* the evaluated predicate.

**Scenario.** A loan process routes on `vars.amount > 10000`. An unauthenticated caller
`POST /instances` with a chosen amount and reads back `history[].node_id`: `auto-approve` or
`manual-review` answers the predicate. Iterating the amount binary-searches the threshold.
Now combine with FM-6: `/actionable` hands over the *expression itself*, so the caller does
not even need to guess which predicate it is solving — it reads
`condition: "vars.amount > 10000"` and then reads which branch a target instance took,
recovering a bound on that instance's withheld variable. `POST /signals` (still open by D1)
lets it drive other people's instances through the same gateways and observe the result.

The bundle's threat model stops at "does this field contain a variable?". It never asks
"does this field *reveal* one?".

**Fix.** State it. This is a residual, not a bug to solve in phase 1 — a node trajectory is
the minimum an instance view can be — but it must be written down, because it bounds what
phase 1 can claim. Concretely: (a) add a **Residual** to the ADR: *"the public projection
discloses the executed path, and an exclusive gateway's path is a function of the variables it
routes on; a caller who can start instances can use it as an oracle"*; (b) let FM-6's fix
withhold `allowed_actions[].condition`, which is the half that turns the oracle from a guess
into a solve; (c) note that closing the oracle properly requires the **phase-2 operation
gate** on `OpStartInstance`/`OpDeliverSignal` — which is exactly the argument for not
claiming phase 1 "closes" anything but disclosure.

---

## FM-8 — MAJOR — the design governs SUCCESS bodies only; the ERROR envelope is outside it

**`ClassifyError`'s five 4xx arms set `Message: err.Error()` (`transport/http/httpcore/errors.go`),
and at least one of them ships `DisclosePolicy` content verbatim to the client.** No document
in the bundle mentions the error path; spec §4's "Error text" residual is about
`incidents[].error` in a **200** body.

**Evidence (executed)** — `ClassifyError` fed errors built at their real production wrapping
sites:

```
RoleAuthorizer attribute-predicate failure    -> 403  error="forbidden"
      message="workflow-authz: not authorized: attribute predicate: workflow-expreval:
      run \"vars.salary > 100000 && actor.attributes.dept == \\\"payroll\\\"\":
      cannot fetch salary from <nil> (1:6)\n | vars.salary > 100000 && actor.attributes.dept == \"payroll\"\n | .....^"
invalid outcome (engine/step_triggers.go:934) -> 400  message="… node \"approve-salary-raise\" outcome \"escalate-to-ceo\""
invalid task (humantask/validate.go:51)       -> 422  message="… task \"inst-7-t1\": state claimed requires a claim"
```

The 403 body carries the **complete ABAC predicate source, twice, with a caret diagram** —
i.e. the authorization policy and the names of the process variables it reads. That is the
`DisclosePolicy` and `DiscloseVariables` vocabulary leaving through a door the allow-list does
not cover. It is produced by `authz.RoleAuthorizer` (`authz/authz.go:137`) wrapping the
`expreval` error, and reachable by any consumer whose `Authorizer` wraps its reason with `%w`.

**Fix.** Bring the error envelope inside the decision. Minimum: add a Residual to the ADR
naming `ClassifyError`'s `err.Error()` arms and stating that a policy predicate can reach a
client through the 403 arm. Better, and cheap: make the 403 arm static
(`Message: "not authorized"`) the way the 413 arm already is — that arm's comment
(`errors.go:"⚠ The body is STATIC — no err.Error(), unlike every other 4xx arm"`) already
establishes the convention and the reason. Add a test that a 403 body does not contain the
predicate source; it fails today.

---

## FM-9 — MAJOR — attacks plan Task 2's "blank the note after the assignment"

**The one instruction the plan flags as its own known asymmetry prescribes writing through a
pointer the projection does not own.**

Plan Task 2, and again in Self-review notes: *"`DiscloseActors` restores `Completion`
**including its `Note`**. If `DiscloseNotes` is not also set, **blank the note after the
assignment**."* The assignment is `out.Tasks[i].Completion = tk.Completion` — a
`*humantask.Completion`. Blanking `out.Tasks[i].Completion.Note` writes into the struct
`tk.Completion` points at, which is the caller's `engine.InstanceState`.

**Evidence (executed).** Two `pi.State()` calls hand back the *same* `*Completion`:

```
two pi.State() calls share the *Completion pointer: true (0x36c16a7d5490 vs 0x36c16a7d5490)
Variables map shared: true
after blanking through the projection, the STORED note is now "SENSITIVE-NOTE"
```

The in-memory store re-materialises on read, so the durable record survived **in this
configuration** — that is the store's clone discipline saving the design, not the design being
safe. The repo's own two sibling projections both clone first *for this exact reason*:
`runtime/view.NewActionableView` — *"Clone before exposing: Claim is a pointer and Candidates
a slice, both reachable into the caller's live InstanceState"* — and
`service.ProcessInstance.ActiveTasks`. `PublicState` would be the only projection in the repo
that does not, while being the only one that **mutates**.

**And its own test cannot catch it.** `TestPublicState_DoesNotMutateInput` checks
`st.Variables["ssn"]` and `st.Tasks[0].Vars != nil` — neither is on the mutated path. Per
Premise Discipline that assertion is vacuous for the defect it is nearest to.

**Fix.** Never mutate through a restored pointer. Under `DiscloseActors`-without-
`DiscloseNotes`, allocate: `c := *tk.Completion; c.Note = ""; out.Tasks[i].Completion = &c`.
Extend `TestPublicState_DoesNotMutateInput` to assert `st.Tasks[0].Completion.Note` is
unchanged **with `DiscloseActors` set and `DiscloseNotes` unset** — the fixture must set both
a `Completion` with a non-empty `Note` and that disclosure combination, or the assertion
cannot fail.

---

## FM-10 — MAJOR — withheld is byte-for-byte indistinguishable from absent

**No wire signal separates "you may not see this" from "there is nothing here", and every
sensitive key is `omitempty`.**

`InstanceView.Variables` is `json:"variables,omitempty"` (`transport/http/httpcore/view.go:20`);
`instanceJSON.Variables/Tokens/History/Tasks/Incidents/Compensating` are all `omitempty`
(`service/instance.go:122-138`); `ActionableTask.Claim/Candidates/AllowedActions` are all
`omitempty`. A withheld field does not render as `null` or `{}` — the **key disappears**.

**Evidence (executed).** An instance that genuinely has no variables marshals to

```
[def_id def_version definition ended_at history instance_id started_at status tokens]
```

— which is exactly the key set a *withheld* one will produce. A client cannot tell them apart,
and neither can a support engineer reading a captured body.

**Scenario.** A UI polls `/instances/{id}`, sees no `variables` key, and renders "This process
has no data." A caller retries a failing integration because it reads the empty document as
"the instance was created without inputs". The ADR's Negative consequences say a custom
`InstanceMapper` "silently renders fewer" fields — the word *silently* is accurate and is
treated as acceptable without argument.

**Fix.** Emit the signal. Cheapest and adapter-local: a response header on any request that
took the projection branch, e.g. `X-Wrkflw-Disclosure: structural` vs the disclosed category
list. It costs one `w.Header().Set` in the three adapters, changes no body shape (so T8's
byte-comparison is unaffected), and gives both clients and support a deterministic answer. If
a body-level marker is preferred it must be argued against the `DiscloseAll` byte-identity
promise, which a new key would break. Whichever is chosen, add it to the ADR's Decision 6 and
to `SECURITY.md`; "indistinguishable from empty" must not be an undocumented property.

---

## FM-11 — MAJOR — the allow-list is not total; it answers spec §7 Q1 with "yes, it does"

**Spec §7 open question 1** asks whether nested types reintroduce a wholesale copy. They do,
in two distinct ways.

1. **Whole-field reference copies under a category.** `out.Variables = st.Variables`,
   `out.RootCompensations = st.RootCompensations`, `out.Scopes = st.Scopes`,
   `out.ArchivedCompensations = st.ArchivedCompensations`, `out.Tasks[i].Vars`,
   `out.Tasks[i].Candidates`, `out.Tokens[i].Payload`. For these, "absent by construction"
   is replaced by one boolean. A field added to `engine.Scope` or `engine.CompensationRecord`
   is disclosed by default under `DiscloseVariables` — *exactly* the deny-list failure mode
   spec §0.2 condemns, reintroduced under a different name.
2. **The D5 guard covers four types; the render graph reaches at least eight.** Guarded:
   `InstanceState`, `Token`, `HumanTask`, `NodeVisit`. **Unguarded and reachable:**
   `engine.Scope` (4 fields), `engine.CompensationRecord` (4, incl. `Input map[string]any`,
   documented as *"a snapshot of the instance variables at invocation time"*),
   `engine.Incident` (9+), `humantask.Claim` (2, incl. `Actor`), `humantask.Completion`
   (4, incl. `Actor` and `Note`), `authz.Actor` (3, incl. `Attributes`),
   `authz.AuthzSpec` (3). Add a field to `humantask.Completion` tomorrow and it is disclosed
   under `DiscloseActors` with **no test going red** — the property ADR Decision 5 claims as
   its headline (*"A field added tomorrow belongs to neither and fails the test"*) does not
   hold for the types that actually carry the sensitive values.

**Scenario.** ADR-0191 adds `Completion.Attachments []string` (uploaded evidence file names).
`DiscloseActors` discloses it. `TestClassification_IsTotal` stays green because
`humantask.Completion` is not one of its four types. That is a silent fail-open, and it is the
precise failure mode the whole revision was written to remove.

**Fix.** (a) Extend the D5 guard's `cases` slice to every struct the projection copies —
`Scope`, `CompensationRecord`, `Incident`, `Claim`, `Completion`, `authz.Actor`,
`authz.AuthzSpec` — with a declared `public`/`withheld` partition for each. (b) For those
reached only by whole-field copy, the honest classification is *"whole field, gated by
category C"*, so give the guard a third declared set (`wholesale`) whose entries must name
their gating category, and assert the projection never copies a `wholesale` field without it.
(c) Answer §7 Q1 in the spec with the executed finding rather than leaving it open.

---

## FM-12 — MAJOR — the same-type projection footgun is reachable through a shipped public API

The ADR calls this out and then mitigates it with prose: *"`PublicState` is a projection that
must never re-enter the engine — a footgun mitigated only by documentation and naming."*
The mitigation is weaker than the ADR assumes, because a **repo-owned public API** consumes
`engine.InstanceState` and silently produces a wrong answer for a projected one.

`processtest.Classify(state engine.InstanceState) Park` (`processtest/park.go:211`) — the
consumer-facing test-harness classifier — decides on
`state.HasArmedTimers()`, `state.Incidents`, `state.SignalWaiters()`,
`state.MessageWaiters()`, `hasIncidentToken(state.Tokens)` and `Token.AwaitCommand`. The
projection withholds **every one of them**: `Timers`, `ArmedEvents`, `Boundaries`,
`EventTriggeredSubprocesses`, `Incidents` and all five `Token.Await*` fields.

**Scenario.** A consumer writes an integration test that fetches an instance through their own
HTTP client, reconstructs the state via a custom `InstanceMapper`, and calls
`processtest.Classify` to assert "this instance is waiting on signal X". It compiles, runs,
and reports `ReasonUnknown` or `ReasonTerminal`. Nothing indicates the value was a projection.
The three sequence counters plus `ids` mean a projected state fed to any future
state-accepting API mints colliding ids — and **six of the seven dropped id fields
(`CmdSeq`, `TokenSeq`, `TaskSeq`, `TimerSeq`, `ScopeSeq`, `IncidentSeq`) are EXPORTED**, so
"the compiler stops you" is false (see FM-15).

**Fix.** Make the type carry the fact. Return a distinct named type from `runtime/view`:

```go
// PublicInstanceState is a RENDER-ONLY projection. It is deliberately NOT an
// engine.InstanceState: it must never re-enter the engine, be persisted, or be
// classified by processtest.
type PublicInstanceState struct{ engine.InstanceState }
```

An embedded struct keeps `json.Marshal` byte-identical (embedding promotes the fields) and
keeps the `InstanceMapper` seam usable via `.InstanceState`, while making
`processtest.Classify(p)` and `service.NewProcessInstance(def, p)` **compile errors** instead
of silent wrong answers. If the mapper seam must keep taking `engine.InstanceState`
unchanged, then at minimum add `processtest.Classify` and `NewProcessInstance` to the ADR's
Residuals by name, so the footgun is enumerated rather than gestured at.

---

## FM-13 — MINOR — admin routes never resolve an actor at all, so ADR-0095 composition degrades them

The three `admin_endpoints.go` render sites are wired by Task 4, but **no admin route calls
`RequestActor`** — only the three task routes do (`stdlib/groups.go:138,170,191` and the gin /
fiber equivalents). ADR-0095's admin-by-composition means the consumer protects the group with
*their own* middleware, which has no reason to construct an `authz.Actor` (an IP allow-list, a
basic-auth wrapper, a service-mesh sidecar, a `WithBasePath` behind an internal ingress).

**Scenario.** An operator's `POST /admin/instances/{id}/cancel` succeeds and returns an
`InstanceView` with `variables` gone, because the protecting middleware never called
`authz.ContextWithActor`. The operator cannot tell whether the cancel worked on the instance
they meant. The bundle asserts the opposite implicitly, by treating "unidentified" as
equivalent to "untrusted".

**Fix.** Covered by FM-2's re-injection fix if the consumer supplies an actor; otherwise the
ADR must state that mounting `AdminRoutes` behind non-`authz` middleware yields projected
admin responses, and `SECURITY.md` must tell operators to add
`authz.ContextWithActor` to their admin middleware. Add a test mounting `AdminRoutes` behind
middleware that authenticates without touching the context, pinning the chosen behaviour.

---

## FM-14 — MINOR — T1's "no actors, no notes, no policy" clauses cannot fail at nine of the eleven sites

T1: *"unauthenticated GET on each of the 11 entry points renders **no variables, no actors, no
notes, no policy**"*, "fails today because measured 200 with full data on the probed ones".

The default `InstanceMapper` is `NewInstanceView`, and `InstanceView` has **seven** fields
(`transport/http/httpcore/view.go:14-21`): `instance_id, def_id, def_version, status,
started_at, ended_at, variables`. No tokens, no tasks, no history, no incidents, no claims,
no candidates. Nine of the eleven sites render it (six `mapInstance` + three
`NewInstanceView` direct). At those nine, *"no actors / no notes / no policy" is already true
today* — three of the four clauses cannot fail, and only `variables` discriminates. Only
`/snapshot` and `/actionable` exercise the rest.

This matters beyond tidiness: it means the elaborate `Token` / `HumanTask` / `NodeVisit`
classification has **no observable effect at nine of the eleven sites** unless a consumer
supplies a custom mapper — which is T6's job, not T1's.

**Fix.** Split T1 into T1a (the nine `InstanceView` sites — assert `variables` absent, and say
that is the only sensitive key the default view carries) and T1b (`/snapshot` and
`/actionable` — assert the full set). State each clause's falsifier. Move the
Token/HumanTask/NodeVisit assertions to T6, where a custom mapper actually renders them, and
give T6 a fixture that reads `st.Tasks[0].Claim`, `st.Tokens[0].Payload` and
`st.History[0].NodeID` so it can fail.

---

## FM-15 — MINOR — two factual errors in the ADR/spec about what the projection drops, plus a signature contradiction

1. **"the unexported id source and sequence counters"** (ADR Decision 4 ⚠, spec §D6 ⚠, plan
   Task 2 godoc). Measured by reflection over `engine.InstanceState`: **31 exported, 1
   unexported.** The single unexported field is `ids engine.idSource`. All six sequence
   counters — `CmdSeq`, `TokenSeq`, `TaskSeq`, `TimerSeq`, `ScopeSeq`, `IncidentSeq` — are
   **exported `int`s**, and the plan's own classification table correctly lists them among the
   20 withheld exported fields. The ADR sentence therefore contradicts the plan's table and,
   worse, implies the compiler protects against reconstructing a full state. It does not
   (FM-12).

   Full measured field lists (executed): `InstanceState` 31 exported / 1 unexported;
   `Token` 13 / 0; `humantask.HumanTask` 11 / 0; `engine.NodeVisit` 6 / 0. The plan's four
   counts (31 / 13 / 11 / 6) are **correct**; the ADR's prose about *which* are unexported is
   not.

2. **`PublicState`'s signature contradicts itself across the bundle.** ADR Decision 4 and spec
   §D6 both declare `func PublicState(st engine.InstanceState) engine.InstanceState` — one
   argument. The plan declares
   `view.PublicState(st engine.InstanceState, d authz.DisclosureSet) engine.InstanceState` and
   calls it that way in Task 2 and Task 3. With the ADR's one-argument form, `WithDisclosure`
   and `DiscloseAll` are unimplementable — the projection has no way to learn the categories.
   The plan's form is the correct one.

3. Consequently `Timers`, `ArmedEvents`, `Boundaries`, `EventTriggeredSubprocesses` and
   `Compensating` are exported fields of **unexported types** (`engine.timerRecord`,
   `armedEvent`, `boundaryArm`, `eventTriggeredSubprocessArm`, `compensationCursor`). Worth
   recording because it is the reason FM-1's fix must be **assignment** (`out.Compensating =
   st.Compensating`, legal — the type need not be named) rather than a composite literal.

**Fix.** Correct the ADR and spec sentences to "the unexported id source **and the six
exported sequence counters**"; correct the ADR's and spec's `PublicState` signature to the
two-argument form; add the unexported-type note beside FM-1's fix so an implementer does not
conclude the field is unrestorable.

---

## Summary

| ID | Sev | Decision attacked | One-line |
|---|---|---|---|
| FM-1 | CRITICAL | D6 / T8 | `DiscloseAll` cannot restore `/snapshot`: `Incidents` and `Compensating` are on the wire and no category restores them |
| FM-2 | CRITICAL | D3 / D7 | a custom `WithRequestActor` (documented, aliased in all 3 adapters) leaves the context actorless ⇒ **every** site projects for **every** caller — executed |
| FM-3 | CRITICAL | D7 / Tasks 3–4 | `renderState(ctx, cfg, pi)` is uncallable: no endpoint takes a cfg, and `CustomizeConfig[any]` ≠ `CustomizeConfig[R]`; the real fix is an undisclosed break of 11 exported functions |
| FM-4 | CRITICAL | D7 | `/snapshot`'s reconstruction re-embeds the definition, defeating `WithoutEmbeddedDefinition` (ADR-0144) — executed |
| FM-5 | MAJOR | D6 + D8 | the escape verbs stay callable while `compensating.active_command_id` / `incidents[].id` become unobtainable — the shipped operator workflow breaks |
| FM-6 | MAJOR | D7 | `/actionable` still emits `allowed_actions[].condition`; only `/snapshot` nils `def` |
| FM-7 | MAJOR | D4 / D6 | the kept structural fields are an oracle over the dropped variables (gateway branch = f(vars)); unmodelled |
| FM-8 | MAJOR | (unscoped) | error bodies are outside the design: a 403 echoes the whole ABAC predicate source — executed |
| FM-9 | MAJOR | Task 2 | the prescribed note-blanking writes through a shared `*Completion`; the anti-mutation test cannot catch it — executed |
| FM-10 | MAJOR | D6 | `omitempty` everywhere ⇒ withheld is byte-identical to empty; no signal, by omission not by decision — executed |
| FM-11 | MAJOR | D5 | the guard covers 4 types; the projection copies ≥ 8 wholesale ⇒ a new `Completion` field fails open, green |
| FM-12 | MAJOR | D4 | the footgun is reachable through `processtest.Classify`, which reads only withheld fields; make the projection a distinct type |
| FM-13 | MINOR | D3 | admin routes resolve no actor at all ⇒ ADR-0095 composition silently projects admin responses |
| FM-14 | MINOR | T1 | 3 of T1's 4 clauses cannot fail at 9 of the 11 sites (the default view has 7 fields) |
| FM-15 | MINOR | D4 / D6 | sequence counters are EXPORTED (31/1 measured), and the ADR's 1-arg `PublicState` signature makes `DiscloseAll` unimplementable |

**Cross-cutting theme.** Revision 2's central promise is *fail-closed by construction*. It
holds for the four guarded structs at the top level and **stops there**: at the category
boundary it becomes a boolean over wholesale copies (FM-11), at the presence signal it becomes
a second, differently-sourced notion of "actor" (FM-2), at the error envelope it does not
apply at all (FM-8), and at the reconstruction it fails **open** in the disclosing direction
(FM-4). The four CRITICALs are all seam defects, not field-classification defects — the
classification work is the part of this revision that is sound.
