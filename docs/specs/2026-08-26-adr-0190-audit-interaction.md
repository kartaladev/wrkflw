# ADR-0190 audit — INTERACTION LENS

**Date:** 2026-08-26 · **Base:** worktree detached at `98382afd` (the bundle commit)
**Bundle:** `docs/specs/2026-08-26-route-group-authorization-posture-design.md` +
`docs/adr/0190-authorization-is-gated-by-policy-not-by-authentication.md` +
`docs/plans/2026-08-26-route-group-authorization-posture.md`

**Mandate:** not "attack the documents" — take the decisions PAIRWISE and derive what each
does to the other's premises. Findings appended as confirmed.

## Decision glosses (rule #13 — expanded on first use)

- **D1** — the library adds no authentication; route groups stay reachable; ADR-0095's
  default-absent posture is preserved.
- **D2** — opt-in service-layer `authz.OperationPolicy`, installed by
  `service.WithOperationPolicy`, evaluated below the transport.
- **D3** — nil-semantics: no policy = no gate; `SpecFor` returning `ok=false` = DENY;
  `ok=true` + empty spec = ALLOW; the zero return tuple denies.
- **D4a** — constructing with an operation policy while the resolved `Authorizer` is
  `authz.AllowAll` is a CONSTRUCTION ERROR.
- **D4b** — a spec constraining only `Privileges`, under a known-permissive authorizer,
  FAILS CLOSED with an error.
- **D5** — the gate runs BEFORE the body read; ordering 401→403→413→400→404; the gate phase
  (pre-load vs post-load) is derived from whether the returned spec carries an `Attribute`;
  an `Attribute` where no instance exists is a configuration error.
- **D6** — redaction as a rendering policy over three read paths, default-closed;
  `WithRedaction()` with no args = opt-out; an UNGATED read redacts the configured set, a
  GATED-and-PASSED read redacts nothing; fed to the consumer's `InstanceMapper` as redacted
  STATE.
- **D7** — admin gating by opt-in decorators (`service.Guarded<X>Admin(inner, policy,
  authorizer)`); `slog` audit; no durable audit table.
- **D8** — three phases: phase 1 redaction only (no gate), phase 2 the gate, phase 3 admin.

## Executed evidence (this lens's own probes, run 2026-08-26, then deleted)

Worktree detached at `98382afd`; probes written into `transport/http/stdlib`, run, removed
(`git status --porcelain` empty afterwards).

| probe | observed |
|---|---|
| **P1** unauthenticated `POST /instances/{id}/signals`, no actor on ctx | **200**, body `{…,"variables":{"salary":145000,"ssn":"111-22-3333"}}` |
| **P2** unauthenticated `POST /admin/instances/{id}/cancel` with `stdlib.Mount(mux, svc)` | **404** — admin routes are default-ABSENT (ADR-0095 confirmed) |
| **P4** same request with `stdlib.AdminRoutes{Svc: svc}.Customize(mux)` (no optional deps) | **200**, body `{…,"variables":{"ssn":"111-22-3333"}}` |
| **P3** `authz.RoleAuthorizer{}.Authorize(ctx, AuthzSpec{}, Actor{ID:"nobody"}, nil)` | **`<nil>` — ALLOW** |
| **P3b** `authz.RoleAuthorizer{}.Authorize(ctx, AuthzSpec{}, Actor{} /*ZERO actor*/, nil)` | **`<nil>` — ALLOW** |

Source-verified, not probed:

- `transport/http/stdlib/groups.go:39-42` — `decodeRequestBody(cfg, w, req, &in)` runs
  **before** `httpcore.StartInstance(req.Context(), c.Svc, in, …)`. The `service` layer is
  reached only after the body is decoded.
- `transport/http/stdlib/groups.go:136-141` — the human-task routes achieve pre-decode
  identity by calling `httpcore.RequestActor(...)` (a **transport**-level function) before
  `decodeOptionalRequestBody`. That is the only mechanism in the repo that puts a decision
  ahead of the decode.
- `service/options.go:77-85` — `WithHumanTasks(taskStore humantask.TaskStore, az
  authz.Authorizer)` is the **only** option that assigns `c.authz`
  (`grep -rn 'c\.authz' service/*.go` ⇒ options.go:83 is the sole writer besides the
  `AllowAll` default at service.go:200).
- Eleven sites render instance state through the transport:
  `endpoints.go:42,52,60,77,94,133,158,182` and `admin_endpoints.go:111,121,514`.
  `httpcore/view.go:29` — `NewInstanceView` sets `Variables: st.Variables`.
- `service/instance.go:20-23` — `ProcessInstance` embeds `json.Marshaler`, so
  `json.Marshal(pi)` is part of the public contract for in-process callers.

---

## The full pairwise grid

Nine decisions ⇒ 36 unordered pairs. Every cell is either a finding ID or "no interaction"
with a one-clause reason. Survivor-vs-survivor pairs (D1×D6, D6×D8, D3×D6 …) are included —
on the B3 bundle that is where three Criticals lived.

| pair | verdict |
|---|---|
| D1 × D2 | **I-10** — a gate with no identity concept; "0095 preserved exactly" over-generalises |
| D1 × D3 | **I-1** — the ZERO actor passes an explicitly-empty spec (P3b) |
| D1 × D4a | no interaction — D4a fires at construction, independent of per-request identity |
| D1 × D4b | no interaction — D4b inspects spec shape and authorizer type, never the actor |
| D1 × D5 | **I-17** — D5's ordering opens with 401, an arm that does not exist on the routes D5 governs |
| D1 × D6 | **I-3** — D1 keeps open the routes that render the very document D6 redacts (P1, P4) |
| D1 × D7 | **I-9** — the audit record attributes to the zero actor in the deployment D1 permits |
| D1 × D8 | no separate interaction — phase 1's exposure to D1 is I-3; phase 2's is I-10 |
| D2 × D3 | no separate interaction — D3 *is* D2's return contract; its live consequence is I-6 |
| D2 × D4a | **I-5** — `WithHumanTasks` is the only authorizer setter, so D4a routes every policy consumer through a human-task option |
| D2 × D4b | no interaction — D4b is a pure downstream check on the spec D2 returns |
| D2 × D5 | **I-2** — a service-layer gate structurally cannot precede the transport's decode |
| D2 × D6 | **I-7** — one `OpGetInstance` gates three render paths with different disclosure profiles |
| D2 × D7 | **I-4** — D7 re-implements D2's gate outside every guard D2 acquired |
| D2 × D8 | **I-8** — phase 1 ships D6's default with D2 absent, so the fidelity control has no counterpart |
| D3 × D4a | **I-16** — D4a refuses a configuration D3's deny arm makes safe |
| D3 × D4b | **I-15** — the obvious remedy for D4b's error is an empty spec, which ALLOWS (P3) |
| D3 × D5 | **I-6** — `Subject.DefID` is unknowable at D5's phase, so a DefID-keyed switch falls through and D3 denies everything |
| D3 × D6 | **I-1** — CRITICAL: the two arms compose into full re-disclosure |
| D3 × D7 | folded into **I-4** — the fallthrough-denies arm composes correctly; the authorizer does not |
| D3 × D8 | **I-8** — the phase-1 opt-out permanently disables the phase-2 fidelity arm |
| D4a × D4b | **I-14** — D4a makes D4b's `AllowAll` arm unreachable where D4b runs, and reachable where it does not |
| D4a × D5 | no interaction — construction-time vs request-time, disjoint inputs |
| D4a × D6 | no interaction — D4a cannot fire in phase 1 (no policy type exists) and never selects a redaction set |
| D4a × D7 | **I-4** — `Guarded<X>Admin` takes the authorizer directly, out of `NewProcessEngine`'s sight |
| D4a × D8 | no interaction — nothing today calls `WithOperationPolicy`, so the new construction error breaks no existing caller |
| D4b × D5 | no interaction — a `Privileges`-only spec carries no `Attribute`, so it is pre-load; the two checks compose |
| D4b × D6 | no interaction — D4b refuses before `Authorize`, never reaching a render |
| D4b × D7 | **I-4** — admin is where privilege phrasing is most natural, and D4b does not run there |
| D4b × D8 | no interaction — D4b lands whole in phase 2 |
| D5 × D6 | **I-18** — the gate outcome is computed where D6's redaction stamp is not, and nothing crosses the seam |
| D5 × D7 | **I-4** — D5's "an `Attribute` for an instanceless op is a configuration error" names the 12 admin ops, which D7 puts in another component |
| D5 × D8 | no interaction — D5 is wholly inside phase 2 |
| D6 × D7 | folded into **I-3** — `AdminListInstances` is genuinely exempt, but three other admin renders are not, and neither decision claims them |
| D6 × D8 | **I-8**, **I-11**, **I-13** — phase 1 in isolation is the most hazardous configuration of D6 |
| D7 × D8 | no interaction — phase 3 consumes phase 2's policy type and adds nothing phase 2 depends on |

---

## Findings

### I-1 — CRITICAL — D3 × D6: declaring a read "open" re-opens the entire disclosure

**What D3 does to D6's premises.** D6 (redaction as a rendering policy) selects fidelity from
one bit: *was this read gated and did it pass?* It justifies making the gated arm
non-configurable with *"if a reader should not see it, do not authorize them"* — which
presumes that passing a gate implies being identified. D3 (the nil-semantics) supplies the
third row of its own table: `ok=true` with an **empty** spec means **allow**. D1 supplies the
rest: an unauthenticated request carries the **zero** `authz.Actor`.

**Executed:** `RoleAuthorizer{}.Authorize(ctx, AuthzSpec{}, Actor{}, nil)` returns **`<nil>`
— ALLOW** (P3b). An empty spec passes for a caller with no identity at all.

**Broken scenario.** A consumer adopts phase 2 and writes the most natural read policy:

```go
func (p myPolicy) SpecFor(_ context.Context, op authz.Operation, _ authz.Subject) (authz.AuthzSpec, bool) {
    switch op {
    case authz.OpGetInstance, authz.OpListInstances:
        return authz.AuthzSpec{}, true          // "reads are public"
    case authz.OpCancelInstance:
        return authz.AuthzSpec{Roles: []string{"ops"}}, true
    }
    return authz.AuthzSpec{}, false             // D3's fail-closed fallthrough
}
```

`GET /instances/{id}` is now **gated and passed** for every caller including the
unauthenticated one ⇒ D6 redacts **nothing** ⇒ the response carries
`variables:{"ssn":"111-22-3333"}` again. The consumer has, by adopting the gate, undone the
security fix the gate was shipped alongside — and nothing warns them. The pre-phase-2
deployment was *safer* than the post-phase-2 one.

**Concrete fix.** Do not derive fidelity from the gate outcome. Derive it from **actor
presence**, which ADR-0189 already resolves per request and which D1 explicitly endorses as
the library's one identity input: full fidelity iff `authz.ActorFromContext(ctx)` reports
`ok` (equivalently, iff `httpcore.RequestActor` succeeded); redact otherwise. This makes the
two arms per-*request* rather than per-*deployment*, removes the dependence on D3's allow
semantics entirely, and lets phase 1 ship the useful posture (public callers redacted,
authenticated callers full) instead of I-8's all-or-nothing. If the gate outcome must stay
part of the rule, then at minimum require a **non-empty** spec to earn full fidelity, and
document that `(AuthzSpec{}, true)` keeps the redacted posture.

---

### I-2 — CRITICAL — D2 × D5: a service-layer gate cannot run before the transport's decode

**What D2 does to D5's premises.** D5 requires the gate to run **before the request body is
read**, and fixes the ordering **401 → 403 → 413 → 400 → 404** — where 413 and 400 are
produced *by* the decode. D2 places the gate at the **service layer** and rejects a
transport-level gate by name: *"A transport-level gate would have been smaller and would have
failed library-first."*

**Source-verified.** `transport/http/stdlib/groups.go:39-42`:

```go
var in httpcore.StartInput
if !decodeRequestBody(cfg, w, req, &in) {   // 413 / 400 decided HERE
    return
}
status, body, err := httpcore.StartInstance(req.Context(), c.Svc, in, cfg.InstanceMapper)
```

The `service` is not reached until after the decode. The only mechanism in this repo that
puts a decision ahead of the decode is ADR-0189's, and it works precisely because
`httpcore.RequestActor` is a **transport** function called from the handler
(`groups.go:136-141`). A gate reachable only through `svc.StartInstance(...)` cannot occupy
that slot, so D5's ordering is unimplementable as specified and the ADR-0189 F6
resource-consumption primitive D5 claims to close stays open for the unauthorized caller.

**Concrete fix.** Split the decision from the enforcement, and say so in both D2 and D5:

- the **policy evaluation** stays in `service` (library-first, embedded consumers gated) —
  but it must be reachable *without* performing the operation. Export it as a method the
  transport can call ahead of the decode, e.g. `service.OperationGate` (an optional interface
  on `Service`, mirroring the `Redactable` shape Task 4 already adopts):
  `Authorize(ctx, op authz.Operation, subj authz.Subject) error`;
- `httpcore` calls it from the handler **before** `decodeRequestBody`, exactly where
  `RequestActor` is called today, and only for the operations whose subject is fully known
  pre-decode (see I-6);
- the service methods keep calling the same gate internally, so an embedded caller is gated
  identically and the double call is idempotent by construction.

Whatever shape is chosen, D2's sentence rejecting a transport-level gate and D5's ordering
must be reconciled in the ADR — today they contradict each other outright.

---

### I-3 — CRITICAL — D1 × D6: D1 keeps open the routes that render the document D6 redacts

**What D1 does to D6's premises.** D6 enumerates **three** render paths and machine-checks
that enumeration with T12 *because the count was already wrong once*. D1 independently
guarantees that state-changing routes stay reachable without middleware. The two together
mean the uncounted render sites are not a hypothetical: they are the reachable ones.

**Executed (P1):** unauthenticated `POST /instances/{id}/signals`, no actor on the context:

```
-> 200  {"instance_id":"…","def_id":"signal-catch-approved","def_version":1,
         "status":"completed","started_at":"…","ended_at":"…",
         "variables":{"salary":145000,"ssn":"111-22-3333"}}
```

**Executed (P4):** `stdlib.AdminRoutes{Svc: svc}.Customize(mux)` with no optional deps —
unauthenticated `POST /admin/instances/{id}/cancel` ⇒ **200** with
`"variables":{"ssn":"111-22-3333"}`.
(P2 confirms `stdlib.Mount(mux, svc)` alone answers **404** there — ADR-0095's default-absent
posture is real, so this arm needs the consumer to have mounted `AdminRoutes`.)

**Source-verified enumeration.** Eleven transport sites render instance state:
`endpoints.go:42` (StartInstance), `:52` (GetInstance), `:60` (snapshot), `:77` (actionable),
`:94` (**DeliverSignal**), `:133` (**ClaimTask**), `:158` (**CompleteTask**), `:182`
(**ReassignTask**); `admin_endpoints.go:111` (**ResolveIncident**), `:121`
(**CancelInstance**), `:514` (**ResolveCompensationStall**). The bundle names three. The
**seven bolded sites are uncounted**, and `DeliverSignal` and the three admin ones render
through the same `mapInstance` / `NewInstanceView(pi.State())` D6 is fixing elsewhere —
`httpcore/view.go:29` sets `Variables: st.Variables`.

D6's own rule already covers them: *"a read that is not gated redacts the configured set."*
Phase 1 implements that rule at 3 of 11 sites. The bundle is not wrong about the rule; its
enumeration is wrong, in exactly the way it created T12 to prevent, and D1 is what makes the
gap exploitable rather than academic.

**Concrete fix.** Re-derive the render enumeration from
`grep -n 'mapInstance\|NewInstanceView\|pi\.State()\|view\.NewActionableView'
transport/http/httpcore/*.go` (excluding `_test.go`) and paste the member list — not a count
— into D6. Redact all eleven, or state per site why it is exempt. `DeliverSignal` is the
urgent one: it is unauthenticated, state-changing, and returns the full variable map. Then
widen T12 to enumerate every site rather than every "path", and see I-11 for why the
exemption list needs an admission criterion.

---

### I-4 — CRITICAL — (D4a, D4b, D5) × D7: none of the three refusals reaches the admin surface

**What D7 does to D4a/D4b/D5's premises.** All three refusals are written against a single
enforcement point. D4a is *"detected in `NewProcessEngine`'s validation"*. D4b and D5's
configuration-error rule are checks the service gate performs on the spec it resolved. D7
then places the 12 admin operations in a **different component**: free-function decorators
`service.Guarded<X>Admin(inner, policy, authorizer)`, constructed by the consumer, taking
the authorizer as a direct argument. `NewProcessEngine` never sees that authorizer.

**Three concrete holes, each on the highest-value surface in the bundle:**

1. **D4a does not fire.** `service.GuardedPolicyAdmin(inner, myPolicy, authz.AllowAll{})`
   constructs successfully and installs a gate that passes every check — the exact failure
   D4a exists to refuse. Six of the 12 admin operations mutate authorization policy
   (`AddPolicy`, `RemovePolicy`, `AddRole`, `RemoveRole`, plus `Redrive` and the cancel-class
   ops).
2. **D4b does not fire.** The spec's own words: expressing an admin gate as a privilege is
   *"the most natural phrasing"*, and measured, `AuthzSpec{Privileges:["admin do"]}` under
   `RoleAuthorizer` returns `nil` — ALLOW. D4b was designed for this case and is installed
   in the one component this case does not pass through.
3. **D5's configuration-error rule does not fire.** D5 says *"For operations with no instance
   at all (`StartInstance` and **all 12 admin operations**), a spec carrying a non-empty
   `Attribute` is a configuration error."* All 12 of those operations are gated by D7's
   decorators. The rule names a set that its own enforcement point does not contain.

**Concrete fix.** Make the decorators go through one shared, non-exported gate helper that
carries all three checks, rather than re-implementing enforcement:

- extract the resolve-spec-then-authorize logic (including D4b's `Privileges`-only refusal
  and D5's instanceless-`Attribute` refusal) into a single `service` internal function, and
  have both the service gate and every `Guarded<X>Admin` call it;
- move D4a from a `NewProcessEngine` type-check to a check **inside that helper's
  constructor**, so `Guarded<X>Admin(inner, policy, authz.AllowAll{})` returns
  `(nil, error)` — which requires changing the decorator signature to return an error. State
  that in D7; the current `Guarded<X>Admin(inner, policy, authorizer)` shape has nowhere to
  report the refusal;
- restate D5's sentence so the operation set it names and the component it runs in agree.

---

### I-5 — MAJOR — D4a × D2: D4a makes `WithOperationPolicy` unreachable without a human-task option

**What D4a does to D2's premises.** D2 presents `service.WithOperationPolicy` as a
self-contained opt-in. D4a makes it a **construction error** unless the resolved `Authorizer`
is something other than `authz.AllowAll`.

**Source-verified:** `grep -rn 'c\.authz' service/*.go` — the only writer besides the
`AllowAll` default (`service/service.go:199-200`) is `service/options.go:83`, inside
`WithHumanTasks(taskStore humantask.TaskStore, az authz.Authorizer)`. There is no
`WithAuthorizer`.

**Broken scenario.** A consumer with **no human tasks at all** — a pure orchestration process
of service tasks and gateways — wants to gate `OpStartInstance` and `OpCancelInstance`. They
write `service.NewProcessEngine(service.WithOperationPolicy(p))` and get a construction error
telling them the authorizer is `AllowAll`. The only cure is
`service.WithHumanTasks(nil, myAuthorizer)` — an option whose name, godoc
(*"overrides the human-task store and authorizer used to build the internal task service"*)
and first parameter are all about a feature they do not use. This is precisely the
library-ergonomics-versus-internal-convenience trade CLAUDE.md resolves in favour of
ergonomics.

**Concrete fix.** Add `service.WithAuthorizer(az authz.Authorizer) Option` in the same phase
as `WithOperationPolicy`, assigning `c.authz`; keep `WithHumanTasks`' authorizer parameter
for compatibility and document that the two set the same field (last-writer-wins, or make a
conflicting pair a construction error of its own). D4a's error message must then name
`WithAuthorizer` as the remedy — an error whose remedy is undiscoverable is a support ticket,
not a guard.

---

### I-6 — MAJOR — D3 × D5: the pre-load subject is incomplete, and D3 turns that into deny-everything

**What D5 does to D3's premises.** D3's fail-closed fallthrough (`ok=false` ⇒ deny) assumes
the policy has enough information to recognise the operation. D5 decides *when* the gate runs
and therefore *what the `Subject` contains*. `Subject` is documented as
`DefID string // empty when unknown at gate time`, and D5's pre-load arm is exactly when it
is unknown.

**Two distinct breakages:**

1. **A DefID-keyed policy denies everything.** "Who may cancel instances of process
   `payroll`" is the most natural instance-scoped policy there is. Written as a switch on
   `subject.DefID`, it receives `""` on every pre-load call, falls through, returns
   `(AuthzSpec{}, false)` and — by D3 — **denies every request**, including the authorized
   ones. The consumer sees a uniform 403 with no diagnostic. D3's fail-closed default is
   correct in isolation; combined with D5's deliberately-empty subject it produces a
   deployment-wide outage from a policy that reads as obviously correct.
2. **`OpStartInstance` can never key on the definition.** Combined with I-2's finding that
   the gate must sit before the decode, the `DefRef` — which arrives **in the request body**
   (`httpcore.StartInput.DefRef`) — is not merely unloaded, it is *unparsed*. So the single
   most-wanted start policy, "who may start process X", is inexpressible. Note the spec's
   §6 rejected "policy in the process definition" partly because a transport-and-ops concern
   belongs elsewhere; the chosen alternative cannot express it either.

**A latent circularity worth naming.** D5 derives the gate **phase** from whether the
returned spec carries an `Attribute`; the spec is returned by `SpecFor(ctx, op, subject)`;
the subject's completeness depends on the phase. The bundle never says whether `SpecFor` is
called once with a partial subject, or twice (once partial, once complete) — and if twice,
whether a policy returning different specs across the two calls is a consumer error or
undefined behaviour.

**Concrete fix.** (a) State the calling contract explicitly in D2's godoc: `SpecFor` is
invoked **once**, pre-load, with `DefID`/`DefVersion` populated **only** when the transport
already knows them from the path; a policy must therefore not key on `DefID` for
`OpStartInstance` or for pre-load instance operations. (b) Give `Subject` a `Phase` field (or
`DefIDKnown bool`) so a policy can *tell* it is being asked with partial information and
answer conservatively rather than fall through. (c) For the start case, choose one and record
it: either accept a post-decode gate for `OpStartInstance` only (giving up D5's pre-decode
property on that one route, with the F6 cost stated), or declare `OpStartInstance` policies
def-agnostic. (d) Add the prescribed test: *a policy switching on `Subject.DefID` denies a
legitimate request today* — it fails against any implementation that populates `DefID`, which
is what makes it falsifiable.

---

### I-7 — MAJOR — D2 × D6: one operation gates three renders with three disclosure profiles

**What D2 does to D6's premises.** D6 treats the three read paths as distinct enough to
enumerate, machine-check and reason about individually — `/snapshot` discloses the embedded
definition, claims, candidates and token payloads; `/instances/{id}` discloses only
`Variables`; `/actionable` discloses claims, candidates and flow conditions. D2's operation
vocabulary collapses all three into **`OpGetInstance`**.

**Source-verified:** `endpoints.go:48`, `:61`, `:73` — `GetInstance`, `GetInstanceSnapshot`
and `GetActionableView` all call `svc.GetInstance(ctx, id)`. There is one service call, so
there is one gate decision, so under D6 all three flip to full fidelity together.

**Broken scenario.** A consumer wants the operator UI to read `/actionable` at full fidelity
while `/snapshot` (which embeds the whole definition, i.e. every node's eligibility policy)
stays redacted. There is no vocabulary for it: one `SpecFor` answer governs all three.

**Concrete fix.** Either split the vocabulary — `OpGetInstance`, `OpGetInstanceSnapshot`,
`OpGetActionableView` as three constants over the same underlying service call, passed down
from the transport — or state plainly in D6 that fidelity is per-*instance-read*, not per-
*endpoint*, and that the coarsest-disclosing endpoint (`/snapshot`) sets the effective
sensitivity of the whole group. The first is better; the second is at least honest. Silence
is the one option that leaves an implementer guessing.

---

### I-8 — MAJOR — D8 × D6 × D3: phase 1 offers only "fully redacted" or "fully open", and the escape is permanent

**What D8 does to D6's premises.** D6's design has two arms and its usefulness lives in the
contrast between them. D8 ships phase 1 with **only the ungated arm existing**: *"Phase 1 has
no gate concept. The effective redaction set is simply the configured one."*

**Broken scenario, in order.**

1. A consumer today runs the engine behind their own auth middleware; their operator UI reads
   `/snapshot` and renders variables and the definition. Phase 1 lands. Their UI breaks:
   authenticated readers now get structural fields only, because phase 1 has no way to know
   they are authenticated.
2. The only remedy phase 1 offers is the documented opt-out `service.WithRedaction()` with no
   arguments — which restores **full** disclosure to *everyone*, authenticated or not. There
   is no middle setting.
3. Phase 2 lands. Their configured set is now empty, so D6's ungated arm redacts **nothing**
   and the gated arm redacts nothing. The fidelity control the gate was supposed to give them
   is inert, and their unauthenticated exposure is exactly what it was before phase 1 — except
   the ADR now records the disclosure as closed.

The consumers who are *worst* served are the ones who already do authentication properly.
This is the direct interaction consequence of building the fidelity switch on the gate
(D6/D3) and then shipping the switch a phase before the gate (D8).

**Concrete fix.** Adopt I-1's fix — key fidelity on **actor presence** rather than on the
gate — and phase 1 becomes coherent on its own: unauthenticated readers redacted,
authenticated readers full, no opt-out required, no phase-2 dependency, and nothing for a
consumer to remember to undo later. If the gate-keyed design is kept instead, D8 must
(a) state that phase 1's only migration path for an authenticated deployment is total opt-out,
(b) add a phase-2 migration note instructing those consumers to *remove* `WithRedaction()`,
and (c) accept that between the two phases the fix is off for exactly the deployments with
real users.

---

### I-9 — MAJOR — D1 × D7: "admin actions become attributable" is false where D1 permits no actor

**What D1 does to D7's premises.** D7's decorator *"already holds operation, actor, subject
and decision"* and emits them as an `slog` record; the ADR's Positive consequences claim
*"Admin actions become attributable."* D1 guarantees the library never establishes who the
caller is, and that a route group mounted without middleware stays reachable.

**Broken scenario.** `AdminRoutes` mounted without the consumer's middleware (P2/P4 show this
is a real, reachable configuration once the consumer mounts them). Every admin action —
including `RemovePolicy`, which strips an authorization rule — produces an audit line reading
`actor=""`. The record exists, is queryable in the log pipeline, and attributes nothing. That
is worse than no record: it looks like an audit trail during an incident review.

The decorator also cannot refuse an unidentified actor without becoming a default-deny on
unauthenticated requests, which D1 forbids in its second sentence.

**Concrete fix.** Three parts. (a) Correct the ADR's Positive bullet to
*"admin actions become attributable **when the consumer authenticates them**"* — the
unhedged form is exactly the over-general recap sentence Premise Discipline warns about.
(b) Have the decorator record the **absence** explicitly and loudly: a distinguishable
`actor_present=false` attribute and a WARN-level record, never an empty-string ID rendered as
if it were an identity. (c) Give the decorator a construction option
`GuardedXAdmin(..., RequireIdentifiedActor())` — opt-in, so D1 and ADR-0095's default-absent
posture are untouched, but available to a consumer who wants the fail-closed behaviour
`examples/production_wiring` already implements by hand.

---

### I-10 — MAJOR — D3 × D1 vs the record's ADR-0095 claim: default-deny returns by another route

The brief's explicit question: *does a construction error that fails closed reintroduce the
default-deny posture ADR-0095 removed?*

**The construction error does not.** D4a fires at `NewProcessEngine`, not per request, and
changes no route's default status code. P2 confirms ADR-0095's mechanism is intact:
`stdlib.Mount(mux, svc)` answers **404** on an admin path because admin routes are *absent*,
not *denied*. On that narrow question the bundle is right.

**D3 does, at a different granularity, and the record does not say so.** ADR-0095's principle
is *absent rather than denied*. D3 chooses **denied rather than absent**: once any policy is
installed, every operation the policy does not name returns `ErrNotAuthorized` ⇒ 403 — and
the ADR states the intent plainly: *"a future operation added to this library cannot silently
open itself in an existing deployment."* That is a good default. It is also, precisely, a
default-deny, and it is what an operator experiences: a caller with no identity (D1's
guaranteed case) hits a policy-gated route and gets 403 on everything the policy gates.

The record's sentence — *"ADR-0095's default-absent posture is preserved **exactly**"* — is
true of route mounting and false as a statement about the delivered posture. It is the
load-bearing sentence behind *"the argument is not needed"*, i.e. the reason the bundle
declines the one instruction ADR-0189 and the handover both marked as this delivery's loudest
constraint. A quantifier that carries that much weight has to be scoped.

**Concrete fix.** Keep the decision (declining to add authentication is right, and D3's
fail-closed fallthrough is right). Replace the claim with the scoped version:

> ADR-0095's *route-mounting* posture is preserved exactly: no route becomes deny-by-default
> and no route is refused for lack of a credential. ADR-0190 does introduce a deny-by-default
> **within a configured operation policy** — an operation the policy does not name is refused,
> not permitted. That is a narrower surface than ADR-0095 governs and applies only to a
> consumer who has opted in, but it is a default-deny and is recorded as one.

Then answer ADR-0189's instruction in those terms, in one paragraph, rather than declaring it
inapplicable. Declining an inherited instruction is fine; declining it on a claim that is
true of a narrower surface than the sentence states is the failure mode.

---

### I-11 — MAJOR — D6 × D8: T12's exemption list has no admission criterion, and 7 sites want in

**What D6's enumeration error does to T12's premises.** T12 exists because the render-path
count was wrong once; the plan describes it as enumerating *"every exported function in
`httpcore` returning a rendered instance body"*, asserting each is redaction-aware, with
`AdminListInstances` as an **asserted-exempt** entry, and a new path having to *"either become
redaction-aware or be added to the exemption list deliberately."*

Per I-3 there are **seven** unredacted instance-rendering functions in `httpcore` on day one:
`DeliverSignal`, `ClaimTask`, `CompleteTask`, `ReassignTask`, `ResolveIncident`,
`CancelInstance`, `ResolveCompensationStall`. So on first run T12 either fails — which is the
correct and useful outcome — or the implementer makes it pass. The plan supplies no criterion
for what may be exempted, and the cheapest way to green is a seven-entry exemption list.
T12 then ships **green over the disclosure it was built to police**: precisely the ADR-0187
lesson that a guard can be blind to the category of claim it exists to check.

**Concrete fix.** (a) State in the plan that T12 is expected to be **RED on first run against
seven named functions**, and list them, so an implementer cannot mistake the failure for a
guard bug. (b) Give the exemption list an assertable criterion rather than a name list: an
entry is exempt only if its response type carries none of the four categories — which is
checkable by reflecting over the returned DTO's fields, and is why `AdminListInstances`
(`instanceSummaryView`: IDs, status, timestamps, incident count) genuinely qualifies while
`NewInstanceView` (which sets `Variables`, `view.go:29`) does not. (c) Keep the ablation step,
and add a second one: remove `AdminListInstances`' exemption and confirm the guard reports it
as *stale-exemption*, not silently passing.

---

### I-12 — MAJOR — D6 × plan Task 4: the public fabricator is a supported bypass of the whole posture

**What Task 4's two rules do to each other.** Task 4 states two fallbacks and calls the
asymmetry *"deliberate … Both are safe"*:

- `RedactionOf(pi)` **fails closed** for a `ProcessInstance` that does not implement
  `Redactable`;
- `service.NewProcessInstance(def, st)` — the **public fabricator** — passes
  `authz.NewRedactionSet()`, i.e. **nothing redacted**.

They are not both safe, and the asymmetry inverts the protection. `NewProcessInstance`
returns the concrete `processInstance`, which **does** implement `Redactable` — so
`RedactionOf`'s fail-closed branch never runs for it. The fail-closed rule guards the case
that does not occur; the opt-out rule governs the case that does.

**Broken scenario.** `service.Service` is a public interface and `stdlib.Mount(mux, svc)`
accepts any implementation — this is a supported embedding shape and the repo already uses it
(`transport/http/gin/gin_bodycap_test.go:228` returns
`service.NewProcessInstance(nil, engine.InstanceState{...})` from a stub `Service`). A
consumer who wraps or replaces `ProcessEngine` and fabricates results with the library's own
public constructor gets **zero redaction on all three fixed paths**, with no warning and no
guard. The rationale offered — *"a consumer fabricating an instance in-process is the trusted
application"* — is a claim about the *caller*, but the fabricated object is then rendered to
an untrusted HTTP reader.

**Concrete fix.** Make the fabricator's redaction explicit rather than implicit: keep
`NewProcessInstance(def, st)` returning an unredacted instance **only** for the in-process
`State()`/`Definition()`/`json.Marshal` uses it was built for, and add
`NewProcessInstanceWithRedaction(def, st, red authz.RedactionSet)` for anything that will be
rendered. Then either (a) have `httpcore` apply `view.RedactState` at the render site using
the transport's own effective set rather than trusting `RedactionOf(pi)` — which closes the
bypass structurally regardless of who built the instance — or (b) document in
`NewProcessInstance`' godoc, in the imperative, that an instance fabricated by it must not be
returned from a `Service` mounted on a transport. (a) is much the stronger of the two.

---

### I-13 — MAJOR — D6 × D2: phase 1 silently redacts the embedded consumer's `json.Marshal(pi)`

**What D6 does to D2's library-first premise.** D6 is careful and correct that redaction
*"never changes what in-process accessors return … `pi.State()` and `pi.Definition()` keep
full fidelity for the embedded consumer."* The enumeration is short by one:
`service/instance.go:20-23` shows `ProcessInstance` **embeds `json.Marshaler`**. Marshalling
is part of the public in-process contract, and `newInstanceJSON` is exactly what phase 1
changes.

**Broken scenario.** An embedded consumer who never mounts a transport at all — the flagship
use case, per CLAUDE.md's "library-first, always" — persists or forwards instances with
`json.Marshal(pi)`. Phase 1 lands with its default set (all four categories) and their
serialized instances silently lose `variables`, the embedded `definition`, claim actors and
completion notes. Nothing in D6's "in-process keeps full fidelity" sentence warns them, and
the ADR's Negative consequences discuss only HTTP consumers and custom `InstanceMapper`s.

**Concrete fix.** (a) Correct D6's sentence to name the accessors it actually covers
(`State()`, `Definition()`) and to state that `MarshalJSON` **is** governed by the policy —
it is a rendering path, which is consistent, but it must be said. (b) Add it to the ADR's
Negative bullet and to `SECURITY.md`'s breaking-change notice, which Task 8 currently frames
entirely in terms of read *endpoints*. (c) Add a test pinning the intended behaviour of
`json.Marshal(pi)` for an engine constructed with no options — it is the case every existing
`service/instance_test.go` marshalling test exercises, and per spec §9 those tests are in the
expected-breakage set; each break must be adjudicated as intended rather than patched away.

---

### I-14 — MINOR — D4a × D4b: D4b's `AllowAll` arm is dead where it runs and live where it does not

D4b names two known-permissive authorizers, `authz.RoleAuthorizer` and `authz.AllowAll`. D4a
makes the second unreachable in the component D4b runs in: if the resolved authorizer is
`AllowAll`, construction already failed, so the service gate can never evaluate a
`Privileges`-only spec under `AllowAll`. Meanwhile, per I-4, `AllowAll` **is** reachable in
D7's decorators — where D4b does not run at all. The enumeration is correct for a component
other than the one it is installed in.

**Concrete fix.** Keep both entries (they cost nothing and become live the moment D4a is
relaxed or the decorator path adopts D4b per I-4), but add one clause to D4b:
*"the `AllowAll` arm is unreachable in the service gate while D4a stands; it is retained
because the admin decorators of Decision 7 accept an authorizer D4a never inspects."* An
enumeration whose members have different reachability should say which.

---

### I-15 — MAJOR — D4b × D3: the obvious fix for D4b's error is the fail-open D3 permits

**What D3 does to D4b's premises.** D4b converts a measured silent fail-open — a
`Privileges`-only spec that ALLOWS under `RoleAuthorizer` — into a loud error. Good. D3 then
defines the neighbouring cell of the same table: an **empty** spec returned with `ok=true`
**allows**, measured (P3, P3b: it allows even the zero actor).

**Broken scenario.** A developer writes
`return authz.AuthzSpec{Privileges: []string{"admin:redrive"}}, true`, gets D4b's error at
runtime, and applies the minimal change that makes the error stop: delete the `Privileges`
field. They now return `authz.AuthzSpec{}, true` — which **allows everyone**, silently, which
is the same fail-open D4b was built to prevent, reached in two keystrokes. The error is loud;
its cheapest remedy is not.

**Concrete fix.** The error text must carry the remedy, not just the diagnosis — this repo's
own convention for `workflow-<pkg>:` sentinels. Something with the shape:

> `workflow-service: operation policy for %q resolved a spec constraining only Privileges,
> which authz.RoleAuthorizer does not evaluate (it would permit every caller). Express the
> constraint with Roles or an Attribute, or supply an Authorizer that evaluates privileges
> (e.g. the casbin adapter). Returning an empty AuthzSpec with ok=true ALLOWS every caller
> including unauthenticated ones and is not the fix.`

Add a prescribed test asserting the message names a remedy — it fails today against any
implementation that returns a bare sentinel, which is what makes it falsifiable.

---

### I-16 — MINOR — D4a × D3: D4a refuses a configuration D3's own deny arm makes safe

D4a's rationale is *"otherwise the gate installs and enforces nothing"* — true only of specs
that reach `Authorize`. D3 supplies a whole arm that never does: `ok=false` denies **before**
the authorizer is consulted. A policy of the form "these operations exist, everything else is
refused" — returning `(AuthzSpec{}, true)` for the permitted set and falling through to
`(AuthzSpec{}, false)` otherwise — is fully functional under `AllowAll`, because `AllowAll`
is only ever asked about the operations the consumer already declared open. D4a refuses that
consumer at construction.

It is a defensible conservative choice, but the ADR states it as though the combination were
*always* inert, and that is falsified by D3 one decision earlier.

**Concrete fix.** Keep the refusal — the conservative default is right and the failure mode
it prevents is worse than the ergonomics it costs — but correct the rationale to
*"a spec that reaches `Authorize` under `AllowAll` is not enforced; refusing the combination
outright is chosen over refusing it per-spec, because the per-spec version fails only on the
requests that matter."* And note the escape in the error message: a deny-list-only policy
should use an authorizer that is not `AllowAll` anyway, or the consumer should express the
refusals with `ok=false` and pass `authz.RoleAuthorizer{}` (which denies nothing it is not
asked to).

---

### I-17 — MINOR — D1 × D5: D5's ordering opens with a status code its routes do not produce

D5 fixes the request ordering at **401 → 403 → 413 → 400 → 404**. The 401 arm
(`httpcore.ErrUnauthenticated`, `errors.go:76-80`) is produced only by
`httpcore.RequestActor`, which ADR-0189 wired into the **three human-task routes** and
nowhere else (`groups.go:136-141`). D1 guarantees no other route acquires it. So on every
route D5 actually governs — instance reads, signals, messages, admin — the ordering begins
at 403, and the "401 →" prefix describes a transition that cannot occur there.

Harmless as written, but it is the kind of inherited-and-restated ordering claim that later
gets cited as evidence the surface authenticates. It also hides a real question the bundle
should answer: on a policy-gated route, should a request carrying **no** actor get 401
(honest: nothing identified you) or 403 (D3's deny)? D1's second sentence pushes toward 403;
ADR-0189's precedent pushes toward 401; the bundle never chooses.

**Concrete fix.** Restate as *"on the human-task routes the existing 401 arm continues to
precede everything; on the routes this record gates, the ordering is 403 → 413 → 400 → 404"*,
and add one sentence choosing the no-actor status code for gated routes, with the reason.
Then extend T10 (the `ClassifyError` co-match ordering test the §2.5 standing invariant
already owes) to pin that choice.

---

### I-18 — MAJOR — D5 × D6: the gate outcome is computed where the redaction stamp is not

**What D5 does to D6's plumbing premises.** D6 needs one bit at render time — *was this read
gated and did it pass?* The plan carries that bit on the object: `processInstance.redaction`,
set at construction, read back by `service.RedactionOf(pi)` (Tasks 3, 4, 6). That works while
the set is a **static config value**. D5 makes the gate decision a **per-request, pre-load**
event — computed before any `ProcessInstance` exists, and (per I-2) plausibly in the
transport rather than the service.

**The gap.** Nothing in the bundle carries the outcome from the gate to the object. Three
sub-questions, none answered:

1. If the gate runs pre-load in `httpcore` (I-2's fix), the service never learns the outcome,
   so it cannot stamp the returned `processInstance` — yet `RedactionOf(pi)` is exactly how
   Task 6 selects the set for paths 2 and 3, and path 1 (`/snapshot`) returns `pi` itself and
   is redacted **inside** `MarshalJSON`, where no request context is available at all.
2. If instead the service stamps it, the service must know the gate outcome, which means the
   gate ran in the service, which contradicts D5's pre-decode ordering (I-2).
3. `GetInstanceSnapshot` returns the bare `pi` and the plan says it *"needs **no** change"*.
   In phase 2 it needs the biggest change of the three: its fidelity is decided by a
   `MarshalJSON` method that receives neither the context nor the gate result.

**Concrete fix.** Decide and record the carrier, in phase 2's ADR text rather than leaving it
to the implementer:

- **preferred** — make the effective set a **per-request value the transport owns**. The
  handler computes it once (from actor presence per I-1, and/or the gate outcome), and
  `httpcore` applies `view.RedactState` at all render sites (which also closes I-12
  structurally). `/snapshot` then stops returning the bare `pi` and returns
  `service.NewProcessInstanceWithRedaction(pi.Definition(), redactedState, set)` — or an
  explicit snapshot DTO — so no fidelity decision hides inside `MarshalJSON`;
- or, if the object must carry it, give `ProcessInstance` a
  `WithRedaction(authz.RedactionSet) ProcessInstance` builder on the `Redactable` optional
  interface, so the transport can re-stamp what the service returned — and say in D6 that the
  stamp is advisory and the transport is authoritative.

Either way, phase 1's `RedactionOf(pi)` seam should be introduced already knowing which of
these phase 2 needs; introducing it as a static stamp and discovering in phase 2 that it must
be per-request is a rework the interaction is visible enough to avoid now.

---

## Note on what this lens did NOT audit

Single-decision defects, prose accuracy, test-plan falsifiability outside the interactions
above, and the §2.4 operation member set are other lenses' remit. Where this lens re-derived
a count (the eleven render sites in I-3, the sole `c.authz` writer in I-5) it did so because
a pairwise conclusion depended on it, not as a counting sweep.
