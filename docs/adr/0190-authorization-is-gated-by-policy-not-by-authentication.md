# 190. Disclosure is closed by an allow-list keyed on actor presence; the library never authenticates

- Status: **Proposed — revision 2, round-2 fixes folded.** Audited **twice**: round 1
  (~72 findings, 17 Critical) produced this different design; round 2 (~50 findings, 16
  Critical) found its code did not compile and its presence signal was never set. Both
  evidence sets are in-repo. Owner decision 2026-08-27: fold and implement rather than run a
  third round — round 2's Criticals are implementation-level where round 1's were
  design-level, and rule #11 budgets for implementation correcting design. **Phase 1 IMPLEMENTED** on `design/route-group-authz-posture`; phases 2 and 3 deferred.
- Date: 2026-08-26, revised 2026-08-27
- **⚠ REVISION 1 FAILED ITS AUDIT.** Four lenses, ~71 findings, **17 Critical**. Evidence
  in-repo: `docs/specs/2026-08-26-adr-0190-audit-{execution,counting,failuremode,interaction}.md`.
  Revision 2 is a **different design**, not a patched one. Do not reconstruct revision 1's
  `service.WithRedaction` / `Redactable` / `RedactionOf` scheme from anything below — it is
  deleted, and spec §0 records why.
- **Owner decision that frames this record:** *"all authentication should be done before
  reaching code in this library; all we need is a mechanism for translating or mapping an
  external authentication representation (mostly carried over `context.Context`) into the
  internal workflow actor representation."*
- **Answering ADR-0189's instruction, rather than declaring it inapplicable.** ADR-0189's
  header requires 0190 to *"argue against ADR-0095 §Admin-by-composition (default-absent)"*.
  Both quotes are verbatim (audit-verified): ADR-0095:159-165 states default-absent
  *"replaces the old default-deny (403)… This is safer"*. **The argument, in one paragraph:**
  ADR-0095 is right, and this record does not overturn it. Admin routes remain *absent* unless
  mounted — measured, an unauthenticated admin cancel under `stdlib.Mount` returns **404**, not
  403. Revision 1 additionally claimed the posture was *"preserved exactly"*; that is **true of
  route mounting and false of the delivered posture**, because the phase-2 operation policy
  introduces a deny-by-default *within* a configured policy. That is a different granularity
  from the route-level default-deny ADR-0095 removed, it is opt-in, and it is confined to
  deployments that install a policy. ADR-0095's decision stands; this record narrows the claim
  rather than the decision.
- **Relates to:** ADR-0189 (the ctx→actor translation this record *keys on*), ADR-0095
  (admin-by-composition — **unchanged**), ADR-0094 (mountable transports), ADR-0147 (actor
  passthrough), ADR-0144 / `WithoutEmbeddedDefinition`, ADR-0186 (the body cap).
- **Supersedes:** nothing.
- Backlog: closes the unauthenticated read disclosure filed by ADR-0189. Does **not** close
  **52**, **53**, **103**.
- Spec: `docs/specs/2026-08-26-route-group-authorization-posture-design.md`.
  Plan: `docs/plans/2026-08-26-route-group-authorization-posture.md`.

## Context

ADR-0189 closed the self-asserted actor for three human-task verbs. The rest of the surface
was untouched: `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` authenticate nothing, and
instance-derived data is rendered to unauthenticated callers.

**Measured** (probes run and deleted; ⓐ = independently reproduced by an audit lens):

```
GET  /instances/{id}            -> 200  "variables":{…,"ssn":"111-22-3333"}
GET  /instances/{id}/snapshot   -> 200  variables, claim.actor.attributes, candidates, policy
GET  /instances/{id}/actionable -> 200  claim.actor.attributes, candidates, allowed_actions
POST /instances/{id}/signals    -> 200  body BYTE-IDENTICAL to the GET            ⓐ
POST /admin/instances/{id}/cancel (AdminRoutes mounted) -> 200  full variables    ⓐ
```

`/signals` is decisive: a caller refused `variables` on `GET` obtains the identical document
by changing the verb. Any per-endpoint fix is therefore wrong by construction.

**The render surface, derived mechanically rather than counted: 4 mechanisms / 11 entry
points** — `mapInstance` (`endpoints.go:42,52,94,133,158,182`), `NewInstanceView` called
directly (`admin_endpoints.go:111,121,514`), the self-marshalling `ProcessInstance`
(`endpoints.go:65`), and `NewActionableView` (`endpoints.go:77`). All three transports dispatch
to an identical 29-member set of `httpcore` functions, so this generalises to gin and fiber.

**Why revision 1's approach was abandoned.** It redacted a *deny-list* of sensitive categories
from a wholesale copy of `engine.InstanceState`. The struct has 31 exported fields; four
independent derivations of "where process variables live" produced **2, 3, 4 and 7**. A
deny-list over a growing struct fails open on every field anyone adds, and it did: `Tasks[].Vars`,
`RootCompensations[].Input`, `Scopes[].Compensations[].Input` and `ArchivedCompensations[k][].Input`
all survived a redactor written specifically to catch process variables.

## Decision

### 1. The library adds no authentication mechanism

The actor arrives already authenticated via `authz.ContextWithActor`. No credential parsing, no
token validation, no session concept, **no default-deny on unauthenticated requests**. A route
group mounted without the consumer's middleware stays reachable; what changes is what its
response *discloses*.

### 2. Disclosure control is a TRANSPORT concern; `service` gains exactly one function

"An unidentified caller" is a transport concept. An embedded consumer calling `svc.GetInstance`
in-process is the trusted application and has no such notion.

⚠ **CORRECTED BY IMPLEMENTATION.** This decision originally read *"`service` is not
modified"*. That proved impossible: `/snapshot` returns the self-marshalling
`ProcessInstance`, and only `service` can construct one. The transport's alternative —
rebuilding via `service.NewProcessInstance` — **re-embeds the definition**, because that
constructor always embeds; it would have silently undone `WithoutEmbeddedDefinition` for
every consumer using it, on the one route this record narrows.

So `service` gains **one** function, [service.ProjectFor], which renders a projected
instance and may only ADD an omission, never remove one. The disclosure *decision* still
lives entirely in the transport; `service` only carries it into the document. Nothing else
in `service` changed, and the embedded consumer's `json.Marshal(pi)` is untouched.

Revision 1 put this in `service` and the audit showed the consequence: `ProcessInstance` embeds
`json.Marshaler`, so a redaction stamp would have silently redacted `json.Marshal(pi)` for the
**embedded** consumer — the library's flagship use case.

⚠ This deliberately splits from the phase-2 operation gate, which *is* a service concern
because authorization must bind embedded callers too. Disclosure is about what crosses the wire
to someone we cannot identify; authorization is about what anyone may do.

### 3. The signal is ACTOR PRESENCE, never the gate outcome

The transport already resolves the actor per request via ADR-0189's `httpcore.RequestActor`.
**An actor with a non-empty ID ⇒ full fidelity. Anything else ⇒ the public projection.**

⚠ **CORRECTED BY IMPLEMENTATION.** This originally read *"actor present"*, and "present"
was implemented with ADR-0189's `isZeroActor`. The test failed, correctly: that guard
deliberately **admits** the kiosk claimant `{ID:"", Roles:["kiosk"]}` blessed at
`humantask/validate.go:24`, so an anonymous caller received full process variables.

The two guards answer different questions. ADR-0189's asks *may this actor ACT*; this one
asks *may this caller SEE EVERYTHING*, and an actor with no ID is unattributable — there is
nobody to hold responsible for the disclosure. A kiosk may therefore complete a task and
still receive the projection.

⚠ It also keys on the configured `RequestActorFunc`, **not** `authz.ActorFromContext`.
Nothing in `transport/http` ever calls `authz.ContextWithActor` — ADR-0189 passes the actor
to the endpoints as an argument — so a context-keyed check is always false and would have
projected for authenticated callers too.

⚠ Revision 1 keyed fidelity on "was this read gated and did it pass". **Measured: an empty
`AuthzSpec` allows the ZERO actor.** A consumer writing the natural read policy
`case OpGetInstance: return AuthzSpec{}, true` would have made every unauthenticated read
*gated and passed*, hence unredacted — the phase-2 gate would have re-opened the hole phase 1
closes. Keying on actor presence also makes phase 1 coherent alone, which revision 1's was not.

### 4. The mechanism is an ALLOW-LIST built as a fresh projection

```go
// runtime/view
func PublicState(st engine.InstanceState) engine.InstanceState
```

It builds a **fresh** `engine.InstanceState` carrying only allow-listed structural fields, and
recursively rebuilds `Tokens`, `Tasks` and `History` from their own allow-lists. Everything
else is absent **by construction**.

This is viable because of a measured language property: `engine.InstanceState` has unexported
fields, and Go forbids *specifying* another package's unexported fields but not *omitting*
them. **Executed** from `runtime/view`: `engine.InstanceState{InstanceID:"i1"}` compiles and
leaves `Variables` nil. Revision 1 assumed the struct could only be copied wholesale, which is
what forced the deny-list.

⚠ `PublicState` is a **render-only projection**: it drops the unexported id source and sequence
counters and must never be fed back into the engine.

### 5. The guard asserts CLASSIFICATION, not enumeration

A test reflects over the exported fields of `engine.InstanceState`, `engine.Token`,
`humantask.HumanTask` and `engine.NodeVisit` and asserts each appears in exactly one declared
set — `public` or `withheld`. **A field added tomorrow belongs to neither and fails the test**,
naming itself and demanding classification.

This is the invariant revision 1's guard tried and failed to be. That one enumerated known
render paths, so it could only fail on what someone forgot to add; this one fails on what
nobody thought about.

### 6. Configuration widens from a closed baseline

`httpcore.WithDisclosure(cats ...authz.DisclosureCategory)` — a `CustomizeOption` on the mount,
not a service `Option`. Categories are **additive**: `DiscloseVariables`, `DiscloseActors`,
`DiscloseNotes`, `DisclosePolicy`. Default: none. `authz.DiscloseAll` restores the pre-ADR-0190 wire
shape in one call.

⚠ Polarity is inverted from revision 1 on purpose: the consumer *widens* disclosure explicitly
rather than *narrowing* it from an open default. Adding a category is a deliberate act;
forgetting one is safe.

### 7. One helper serves all eleven entry points

A single `httpcore` helper decides once per request and is called by every render site,
including the three in `admin_endpoints.go` that bypass `mapInstance`. `/snapshot` renders a
*projected* instance — `service.NewProcessInstance(def, view.PublicState(pi.State()))`, `def`
nil unless `DisclosePolicy` is set — needing no `MarshalJSON` change, no interface method and
no stamp.

⚠ The three human-task verbs need no special case: ADR-0189 authenticates them, so an actor is
present and they render full fidelity.

### 8. The operation gate and admin audit are DEFERRED, with their constraints recorded

Phase 2 (the gate) and phase 3 (admin) each get their **own spec, ADR and rule-#9 audit**.
Writing them out here is what produced revision 1's Criticals. The audit-derived constraints
they must satisfy are recorded in spec §3 D2 and D7; the load-bearing ones:

- A service-layer gate **cannot** precede the body decode, so the pre-decode ordering revision 1
  promised is unimplementable as designed. Split decision from enforcement.
- The `AllowAll` type check is defeated by wrapping and misses `processtest.SpyAuthorizer`.
  Replace it with a capability declaration.
- The "Privileges-only" narrowing is wrong: `{Roles:["employee"], Privileges:["admin do"]}`
  also allows.
- The no-instance operation set was wrong **in both directions** — omitting `OpListInstances`
  and `OpDeliverMessage`, wrongly including the instance-scoped `OpAdminLineage`.
- `service` has no `WithAuthorizer`; `c.authz` is written only inside `WithHumanTasks`.

## Consequences

**Positive.** All eleven render entry points close at once — verified end-to-end on all three
adapters — including `POST /signals`, which made any per-endpoint fix futile. A field added to `InstanceState` tomorrow is withheld by
default and fails a guard until classified — the failure mode that produced four different
answers to one question is structurally removed. `service` is untouched, so the embedded
consumer's `json.Marshal(pi)` is unaffected. Phase 1 is independently coherent and independently
shippable.

**Negative.** ⚠ **This is a source-incompatible public API change** — but a much smaller one
than the round-2 audit predicted, and the correction is recorded because the prediction is
also on the record. The audit derived "11 exported signatures and ~70 call sites across 4
packages". The measured change is **5 exported signatures and 7 test call sites**:
`GetInstanceSnapshot` and `GetActionableView` each take a projection and a
withhold-definition flag; `ResolveIncident`, `CancelInstance` and `ResolveCompensationStall`
each take an instance mapper.

Six of the eleven render sites needed **no signature change at all**, because they already
accept `cfg.InstanceMapper` — so the adapters pass a per-request WRAPPED mapper instead, and
the endpoints never learn that disclosure exists. A consumer calling those five `httpcore`
functions directly must update their calls.

All eleven entry points change shape by default for unauthenticated callers;
`DiscloseAll` is the documented one-call opt-out and this must be a headline breaking change,
not a footnote. A consumer whose custom `InstanceMapper` renders withheld fields silently
renders fewer on an unidentified read. `PublicState` is a projection that must never re-enter
the engine — a footgun mitigated only by documentation and naming. Deferring phases 2 and 3
means the 20 ungated operations stay ungated after this ships.

**Neutral.** ADR-0095's default-absent posture stands. State-changing routes remain reachable
without middleware, by Decision 1.

### Residuals, stated rather than implied

- **Error text.** `incidents[].error` is `err.Error()` from the consumer's action verbatim and
  may embed variables. Withheld by default (`Incidents` is not structural), but a consumer
  setting `DiscloseAll` gets it and no category isolates it.
- **A permissive `RequestActorFunc`** that manufactures an anonymous actor yields "actor
  present" and therefore full fidelity. ADR-0189 blesses a kiosk claimant
  `{ID:"", Roles:["kiosk"]}` at `humantask/validate.go:24`. Flagged for the second audit.
- **A consumer-supplied `Service`** is a supported shape and bypasses anything `service` would
  have enforced — one more reason disclosure lives in the transport, but not a complete answer.
