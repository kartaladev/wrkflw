# Route-group authorization posture and the read-disclosure fix (ADR-0190)

**Status:** design — **revision 2**, after the round-1 audit. Pre-second-audit.
**Date:** 2026-08-26, revised 2026-08-27
**Bundle:** this spec + ADR-0190 + `docs/plans/2026-08-26-route-group-authorization-posture.md`
**Base:** `main` at merge `7be335fb` (ADR-0189)
**Round-1 audit evidence, in-repo:** `docs/specs/2026-08-26-adr-0190-audit-{execution,counting,failuremode,interaction}.md`
— four lenses, ~71 findings, **17 Critical**.

---

## 0. What the round-1 audit changed, and why revision 2 is a different design

Revision 1 failed. Not on details — on three structural errors. This section exists so no
later reader re-derives the rejected design from its remains.

**0.1 Every hand-derived enumeration was wrong, including the corrections of earlier errors.**
Variables sites in `engine.InstanceState` went **2 → 3 → 4 → 7** across four independent
derivations. Render paths went **3 → 11 entry points across 4 mechanisms**. Revision 1
corrected the render count from two to three, wrote a guard test *because* the enumeration
had already rotted once, and was still wrong — because it re-checked the paths already known
rather than deriving the set from `mapInstance`'s call sites.

⇒ **Revision 2 contains no hand-maintained list of sensitive fields.** The enumeration is
derived mechanically, and the guard fails on an *unclassified* field rather than on a
remembered one.

**0.2 A deny-list over a growing struct fails open on every field anyone adds.** That is
precisely how `Tasks[].Vars`, `RootCompensations[].Input`, `Scopes[].Compensations[].Input`
and `ArchivedCompensations[k][].Input` all survived a redactor written specifically to catch
process variables.

⇒ **Revision 2 inverts the model to an allow-list.** A new field is withheld by default.

**0.3 The seam could not carry the decision.** Revision 1 stamped a redaction set onto
`processInstance`, but the fidelity signal is per-request and `/snapshot` decides inside
`MarshalJSON`, which sees neither context nor request. Worse, `ProcessInstance` embeds
`json.Marshaler`, so the stamp would have silently redacted `json.Marshal(pi)` for the
**embedded** consumer — the library's flagship use case.

⇒ **Revision 2 moves disclosure control entirely into the transport** and touches `service`
not at all. `Redactable`, `RedactionOf`, `service.WithRedaction`, the `processInstance`
field and the `newInstanceJSON` signature change are all **deleted from the design**.

**0.4 Keying fidelity on the gate outcome re-opened the hole the bundle closes.** Measured:
an empty `AuthzSpec` allows the **zero actor**. A consumer writing the natural read policy
`case OpGetInstance: return AuthzSpec{}, true` would make every unauthenticated read *gated
and passed*, hence unredacted.

⇒ **Revision 2 keys fidelity on ACTOR PRESENCE**, using ADR-0189's existing per-request
resolver, and never on the gate outcome.

### Findings adjudicated as NOT defects

- ⛔ **"Tree not green at the bundle commit" (round-1 execution F18) — REJECTED, THE FINDING
  IS FALSE.** It claimed `internal/database` and `internal/dbtest` fail pre-existing on
  MySQL/testcontainers, baseline "59 ok / 2 FAIL". **MEASURED 2026-08-27 with Docker up:
  `go test ./internal/database/... ./internal/dbtest/...` → EXIT=0**, all three packages `ok`
  (33.1 s, 4.6 s, 26.1 s). The whole tree is **65 ok / 0 FAIL**.
  ⚠⚠ Revision 2 accepted this finding and restated it as fact in **three** places without
  running it, which would have told an implementer to ignore genuine regressions in two
  packages. **A review finding is a claim needing execution — including one from an audit
  this bundle commissioned.** Restating stripped the hedge, exactly as CLAUDE.md's Premise
  Discipline warns.
- **gin/fiber parity** (spec §2.6 in revision 1, counting C7, execution "confirmed sound").
  Resolved **favourably**: five route groups per adapter, and all three dispatch to an
  identical 29-member set of `httpcore` functions. No longer an assumption. ⚠ It also
  *generalises* every disclosure finding to all three transports.
- **`AdminListInstances` is exempt** — executed by two lenses independently. It projects
  `instanceSummaryView`: IDs, status, timestamps, incident count. Kept as an asserted
  exemption, not an assumption.

---

## 1. Problem

ADR-0189 established that the HTTP transport reads the actor from `context.Context` and from
nowhere else — but only for the three human-task verbs. Everything else was left as it was:

- `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` authenticate nothing, and
  `POST /instances`, `/signals` and `/messages` are state-changing and open.
- **Eleven** entry points render instance-derived data to an unauthenticated caller.
- The 12 admin operations have no authorization and no audit record; six of them mutate
  authorization policy or process state.

**The owner's constraint, which frames every decision:** this library must not implement
authentication. Authentication happens upstream. The library *translates* an external
authenticated identity — carried on `context.Context` — into `authz.Actor`, and acts on it.

## 2. Executed premises

Every claim here was **run**. Probes were throwaway tests, executed 2026-08-26/27 and deleted.
Claims marked ⓐ were additionally confirmed by an independent audit lens.

### 2.1 The disclosure, measured ⓐ

Unauthenticated, `stdlib.Mount(mux, svc)`, no actor on the context:

```
GET  /instances/{id}            -> 200  "variables":{…,"ssn":"111-22-3333"}
GET  /instances/{id}/snapshot   -> 200  variables, claim.actor.attributes, candidates,
                                        definition.nodes[].eligible_roles
GET  /instances/{id}/actionable -> 200  claim.actor.attributes, candidates, allowed_actions
POST /instances/{id}/signals    -> 200  body BYTE-IDENTICAL to the GET   ⚠ ⓐ
POST /admin/instances/{id}/cancel (AdminRoutes mounted directly) -> 200  full variables ⓐ
```

⚠ **`/signals` is the finding that invalidates a per-endpoint fix.** A caller refused
`variables` on `GET` obtains the identical document by changing the verb. Two lenses executed
this independently.

### 2.2 The render surface — derived mechanically, not counted

```
$ grep -rn "mapInstance(\|NewInstanceView(\|NewActionableView(\|pi.State()" \
      transport/http/httpcore/*.go | grep -v _test
```

**4 mechanisms / 11 entry points:**

| mechanism | sites |
|---|---|
| `mapInstance(mapper, pi.State())` | `endpoints.go:42, 52, 94, 133, 158, 182` (6) |
| `NewInstanceView(pi.State())` **direct** | `admin_endpoints.go:111, 121, 514` (3) |
| `pi` self-marshalling | `endpoints.go:65` (1) |
| `view.NewActionableView(pi.State(), …)` | `endpoints.go:77` (1) |

⚠ `admin_endpoints.go` bypasses `mapInstance` entirely and was absent from revision 1's plan.
⚠ `endpoints.go:133,158,182` are `ClaimTask`/`CompleteTask`/`ReassignTask` — the three verbs
ADR-0189 authenticates. Under actor-presence keying (§3 D6) they need no special case: their
callers are authenticated by construction, so they render full fidelity.

### 2.3 Three fail-opens in the authorization primitives ⓐ

`authz.RoleAuthorizer{}` — the built-in non-permissive authorizer:

| probe | result |
|---|---|
| `Authorize(AuthzSpec{}, Actor{ID:"nobody"}, nil)` | **`nil` ALLOW** — backlog 53 |
| `Authorize(AuthzSpec{}, Actor{} /* ZERO actor */, nil)` | **`nil` ALLOW** ⓐ |
| `Authorize(AuthzSpec{Attribute:"vars.status != \"closed\""}, …, nil)` | **`nil` ALLOW** — backlog 103 |
| `Authorize(AuthzSpec{Privileges:["admin do"]}, …, nil)` | **`nil` ALLOW** |
| `Authorize(AuthzSpec{Roles:["employee"], Privileges:["admin do"]}, employee, nil)` | **`nil` ALLOW** ⓐ |

⚠ The last row refutes revision 1's D4b as written: the hole is not "a spec constraining
**only** `Privileges`" — a spec mixing a weak `Roles` with a strong `Privileges` also allows,
and looks *more* restrictive than it is.

### 2.4 The operation set (member list pasted, verified by an audit lens)

8 ungated `Service` operations: `StartInstance`, `GetInstance`, `ListInstances`,
`DeliverSignal`, `DeliverMessage`, `ResolveIncident`, `ResolveCompensationStall`,
`CancelInstance`. The four `TaskManager` verbs are already gated by task `Eligibility`
(`Authorize` is called at exactly four sites, `runtime/task/service.go:199,234,255,306`).

12 admin operations across 5 interfaces: `DeadLetterAdmin` (`ListDeadLettered`, `Redrive`),
`LineageAdmin` (`Lineage`), `RelayStatsAdmin` (`OutboxStats`), `TimerAdmin` (`Stats`,
`ListArmedPage`), `PolicyAdmin` (`AddPolicy`, `RemovePolicy`, `ListPolicies`, `AddRole`,
`RemoveRole`, `ListRoles`).

### 2.5 The field surface — 31 exported fields on `engine.InstanceState`

Derived by `sed`+`grep` over the struct, not by reading prose. The seven variables-bearing
sites confirmed by execution: `Variables`, `StartVariables`, `Tokens[].Payload`,
`Tasks[].Vars`, `RootCompensations[].Input`, `Scopes[].Compensations[].Input`,
`ArchivedCompensations[k][].Input`. Source-verified: `HumanTask.Vars` is documented as *"a
snapshot of the process Variables at task-creation time"*; `CompensationRecord.Input` as *"a
snapshot of the instance variables at invocation time"*.

⚠ This list is recorded as **evidence that hand-enumeration failed**, not as the design's
input. D6 does not consume it.

### 2.6 A fresh cross-package literal zeroes unlisted fields — the allow-list is viable

`engine.InstanceState` carries unexported fields (`ids idSource`, six sequence counters), so
revision 1 assumed it could only be copied wholesale. **Executed** from `runtime/view`:

```go
st := engine.InstanceState{InstanceID: "i1", DefID: "d1", DefVersion: 1}
// compiles; st.Variables == nil
```

Go forbids *specifying* another package's unexported fields, not *omitting* them. A fresh
keyed literal therefore zeroes everything not listed — which is exactly the fail-closed
primitive D6 needs.

### 2.7 `authz` has no purity test

`engine/purity_test.go` is the repo's only one. `go list -deps ./authz` returns exactly one
in-repo dependency, `internal/expreval`. The package is pure **in fact** and **unguarded**.

⚠ Revision 1 prescribed an ablation importing `engine` from `authz` — that is an **import
cycle**, so the package never compiles and the assertion never runs. A cyclic ablation is not
a RED. Use a non-cyclic forbidden import (`definition/model`), which produces a real failure.

## 3. Decisions

### D1 — The library adds no authentication mechanism

Unchanged from revision 1. The actor arrives already authenticated via
`authz.ContextWithActor`. No credential parsing, no token validation, no session concept, no
default-deny on unauthenticated requests. A route group mounted without the consumer's
middleware stays reachable.

⚠ **Scoping correction from the audit (interaction I-10).** Revision 1 claimed *"ADR-0095's
posture is preserved exactly"* and used that to decline ADR-0189's instruction to argue
against it. That claim is true of **route mounting** — measured: `stdlib.Mount` does not
register admin routes, and an unauthenticated admin cancel returns **404**, not 403 — and
**false as a statement about the delivered posture**, because D3 introduces a deny-by-default
*within* a configured policy. ADR-0190 now answers 0189's instruction in a paragraph rather
than declaring it inapplicable.

### D6 — Disclosure control: an allow-list, keyed on actor presence, owned by the transport

**This replaces revision 1's D6 entirely.** Revision 1's `service.WithRedaction`,
`Redactable`, `RedactionOf`, the `processInstance` field and the `newInstanceJSON` signature
change are **deleted**.

**Where it lives: the transport, and nowhere else.** "An unidentified caller" is a *transport*
concept — an embedded consumer calling `svc.GetInstance` in-process is the trusted
application and has no such notion. Putting disclosure control in `service` is what caused
revision 1 to silently redact `json.Marshal(pi)` for embedded consumers (interaction I-13).
**`service` is not modified by this decision at all.**

⚠ This is a deliberate split from D2, and the two are not inconsistent: *authorization*
(D2) is a service concern because it must bind embedded callers too; *disclosure* (D6) is a
transport concern because it is about what crosses the wire to someone we cannot identify.

**The signal: actor presence.** The transport already resolves the actor per request via
ADR-0189's `httpcore.RequestActor`. An actor present ⇒ full fidelity. No actor ⇒ the public
projection. Never the gate outcome (§0.4).

**The mechanism: a fresh, allow-listed projection.**

```go
// runtime/view
func PublicState(st engine.InstanceState) engine.InstanceState
```

It builds a **fresh** `engine.InstanceState` (§2.6) carrying only allow-listed structural
fields, recursively rebuilding `Tokens`, `Tasks` and `History` from their own allow-lists.
Everything else is absent **by construction**, so a field added to any of those structs
tomorrow is withheld without anyone remembering to withhold it.

⚠ `PublicState` is a **render-only projection** and must never be fed back into the engine:
it drops the unexported id source and sequence counters. Its godoc must say so.

**The guard: classification, not enumeration.** A test reflects over the exported fields of
`engine.InstanceState`, `engine.Token`, `humantask.HumanTask` and `engine.NodeVisit`, and
asserts every field appears in exactly one of two declared sets — `public` or `withheld`. **A
new field belongs to neither and fails the test**, naming the field and demanding
classification. This is the invariant revision 1's T12 tried and failed to be: it fails on
what nobody thought about, rather than on what someone forgot to add to a list.

**Configuration.** `httpcore.WithDisclosure(cats ...authz.DisclosureCategory)`, a
`CustomizeOption` on the mount — not a service `Option`. Categories are **additive to the
structural baseline**: `DiscloseVariables`, `DiscloseActors`, `DiscloseNotes`,
`DisclosePolicy`. The default is none of them. `httpcore.DiscloseAll` restores the exact
pre-ADR-0190 wire shape as a one-call opt-out.

⚠ Polarity is inverted from revision 1 deliberately: the consumer *widens* disclosure from a
closed baseline rather than *narrowing* it from an open one. Adding a category is an explicit
act; forgetting one is safe.

**Application: one helper, all eleven entry points.** A single
`httpcore.renderState(ctx, cfg, pi) engine.InstanceState` decides once and is called by every
render site, including the three in `admin_endpoints.go`. `/snapshot` (the self-marshalling
path) is handled by rendering a *projected* instance —
`service.NewProcessInstance(def, view.PublicState(pi.State()))` with `def` nil unless
`DisclosePolicy` is set — which needs no change to `MarshalJSON`, no interface method, and no
stamp.

⚠ The three human-task verbs (`endpoints.go:133,158,182`) need no special case: ADR-0189
authenticates them, so an actor is present and they render full fidelity. This is what
dissolves execution-lens F1b, which had no answer under revision 1's gate-keyed design.

### D2 — An opt-in, service-layer operation policy · **PHASE 2, and its design is NOT settled here**

The round-1 audit found the gate's *placement* contradictory (interaction I-2: a service-layer
gate cannot precede the body decode that D5's ordering requires) and its *subject derivation*
under-specified (I-6: `Subject.DefID` is unknown pre-load, and `OpStartInstance`'s `DefRef`
lives only in the body).

**This spec therefore records the constraints and the direction, and defers the design to
phase 2's own spec, ADR and audit.** Writing it out here is what produced revision 1's
Criticals.

Constraints phase 2 must satisfy, all audit-derived:

1. The gate must bind **embedded** callers, so its authoritative evaluation is at the service
   layer.
2. D5's pre-decode ordering cannot be met by a service-layer call alone. The direction is to
   **split decision from enforcement**: export the evaluation so the transport can call it
   pre-decode, exactly where `httpcore.RequestActor` is called today, with the service
   methods calling the same evaluator internally.
3. `SpecFor`'s calling contract must state that the subject may be **partial**, and let a
   policy answer conservatively rather than fall through to a deny.
4. `ok=false` denying is right, but a **migration story is missing**: an operation constant
   added by a later release is unknown to an existing policy, which then denies it. `Operation`
   being a string means no compile error warns the consumer.
5. D4a's `AllowAll` type check is defeated by wrapping (`casbinauthz` shows wrapping is the
   normal idiom) and does not fire for `processtest.SpyAuthorizer`, which allows by default
   without being of that type. Replace the type check with a **capability declaration** on the
   authorizer.
6. D4b's narrowing is **wrong as written**: `{Roles:["employee"], Privileges:["admin do"]}`
   also allows (§2.3). The rule must key on "the spec's effective constraint under this
   authorizer", not on "only `Privileges`".
7. The no-instance operation set was **wrong in both directions** (counting C1): it omitted
   `OpListInstances` and `OpDeliverMessage` — `DeliverMessageRequest` has no `InstanceID`
   field — and wrongly included `OpAdminLineage`, which is instance-scoped. Phase 2 must
   derive this set **from the request types**, mechanically.
8. `service` has **no `WithAuthorizer`**; `c.authz` is written in exactly one place, inside
   `WithHumanTasks`. Any construction-time rule about the authorizer needs that option first.

### D7 — Admin gating and audit · **PHASE 3, deferred**

Direction unchanged: opt-in decorators, `slog` audit, no durable audit table. Two audit
constraints recorded for phase 3:

- All of D4a/D4b/D5's refusals live in the service; free-function decorators bypass every one
  of them (interaction I-4). Phase 3 needs **one shared gate helper** used by both paths,
  which requires the decorator constructors to return an error.
- *"Admin actions become attributable"* is **false where D1 permits no actor**: records would
  read `actor=""`, including for `RemovePolicy`. Either hedge the claim or offer an opt-in
  `RequireIdentifiedActor`.

### D8 — Phasing

**Phase 1 (this plan): disclosure control only** — D6, entirely within
`transport/http/{httpcore,stdlib,gin,fiber}` and `runtime/view`. No `service` changes, no gate
concept, no `authz` changes beyond the `DisclosureCategory` type.

Phase 1 is now **coherent on its own**, which revision 1's was not: keyed on actor presence, a
deployment that already authenticates gets full fidelity for its users and a closed door for
everyone else. Revision 1's phase 1 had only two reachable configurations — everyone blind, or
everyone sees everything (execution F19, interaction I-8).

Phase 2 (the gate) and phase 3 (admin) each get **their own spec, ADR and rule-#9 audit**,
written against the tree as it exists after the previous phase lands.

## 4. What this record does not close

- **Backlog 52, 53, 103** and the deny-list actor-attribute fail-open. Untouched. Each needs
  its own ADR.
- **Unauthenticated state-changing routes remain reachable.** D1's deliberate consequence.
  Phase 1 changes what they *disclose in their response*, not whether they can be called.
- **Error text.** `incidents[].error` is `err.Error()` from the consumer's action verbatim and
  may embed variables (execution F5). Under D6's allow-list it is withheld by default because
  `Incidents` is not in the structural baseline — but a consumer setting `DiscloseAll` gets it,
  and no category isolates it. Stated, not solved.

## 5. Test plan

Every prescribed test states what makes it fail today. ⚠ **Revision 1's fixtures were vacuous
three ways** (execution F25): `transporttest`'s actor is `alice` — **not** `alice@corp.example`
— carries **no** `Attributes`, and `ApprovalProcess`'s flow `f2` has **no** `Condition`. Every
assertion below names a value the standard harness actually produces, or builds its own fixture.

| # | test | fails today because |
|---|---|---|
| T1 | unauthenticated GET on each of the **11** entry points renders no variables, no actors, no notes, no policy | measured 200 with full data on the probed ones (§2.1) |
| T2 | **`POST /instances/{id}/signals` specifically** | §2.1 — byte-identical to the GET |
| T3 | the three `admin_endpoints.go` sites | §2.1 — 200 with full variables |
| T4 | fixture populates `Tokens[].Payload`, `Tasks[].Vars`, `RootCompensations[].Input`, `Scopes[].Compensations[].Input`, `ArchivedCompensations[k][].Input` — none appear | revision 1's redactor leaked all but the first; a fixture lacking them is vacuous |
| T5 | an **authenticated** request renders full fidelity | pins that the fix does not break the three ADR-0189 verbs |
| T6 | a custom `InstanceMapper` receives the public projection when no actor is present | a mapper rendering `st.Tasks[0].Vars` leaks otherwise |
| T7 | **classification guard**: every exported field of `InstanceState`, `Token`, `HumanTask`, `NodeVisit` is in exactly one declared set | add a field to any of them ⇒ RED naming it |
| T8 | `WithDisclosure(DiscloseAll)` reproduces the pre-ADR-0190 body byte-for-byte | pins the opt-out is complete |
| T9 | `AdminListInstances` is unchanged | pins the asserted exemption |
| T10 | parity: gin and fiber match stdlib on T1–T3 | §0.4 — all three share a 29-member dispatch set |
| T11 | `authz/purity_test.go` | no such guard exists; ablate with `definition/model`, **not** `engine` (§2.7) |

**Mutation obligations.** T1, T4, T7 and T8 are load-bearing and must each be
mutation-verified: break the line, observe RED, restore from a `cp` backup, `diff`. T7 must be
ablated by *adding a field*, not by editing the list.

## 6. Verification

1. `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — touched
   packages ≥ 85 %. ⚠ Baseline is **65 ok / 0 FAIL, EXIT=0** — MEASURED 2026-08-27 with Docker up. An earlier
   draft claimed "59 ok / 2 FAIL (`internal/database`, `internal/dbtest`, pre-existing)"; that
   was INHERITED FROM A ROUND-1 AUDIT FINDING AND RESTATED WITHOUT EXECUTION, and it is FALSE —
   both packages pass (33.1 s and 26.1 s). Treat ANY failure as a regression.
2. `go test ./...` — no *new* regressions. ⚠ Revision 1's breakage prediction was wrong both
   ways: measured ablation gives **18 failures in 3 packages**; 6 of the 8 predicted files do
   not break, and `transport/http/stdlib/maxbody_test.go` does and was unnamed. **Re-derive
   this net; do not inherit it.**
3. `golangci-lint run ./...` repo-wide.
4. `/code-review` and `/security-review`, all findings fixed and folded.

## 7. Open questions for the second audit

1. Does `PublicState`'s fresh-literal construction actually withhold everything unlisted for
   **nested** types, or does a nested slice of structs reintroduce a wholesale copy?
2. Is "actor present" the right predicate, or does a consumer with a permissive
   `RequestActorFunc` (one that manufactures an anonymous actor) silently get full fidelity?
   ⚠ ADR-0189 blesses a kiosk claimant `{ID:"", Roles:["kiosk"]}` at `humantask/validate.go:24`.
3. Does moving disclosure to the transport leave any path where `service` hands full state to
   something that renders it — e.g. a consumer-supplied `Service` implementation, which
   interaction I-12 showed is a supported shape?
4. `DiscloseAll` claims byte-identical restoration. Is that true for `/snapshot`, where the
   projected-instance approach reconstructs the document rather than passing it through?

---

## 8. Round-2 audit adjudication (2026-08-27)

Four lenses, **~50 findings, 16 Critical**. Evidence in-repo:
`docs/specs/2026-08-27-adr-0190-audit2-{execution,counting,failuremode,interaction}.md`.

**Round 1 was ~72 findings / 17 Critical. The Critical count barely moved (17 → 16) while the
design changed wholesale.** ADR-0186's lineage recorded the same shape across seven rounds and
concluded the rate is a property of the **process**, not the bundle. That is the context for
the disposition below — it is not an argument that the findings are wrong. They are not: three
of them mean the plan does not compile or does not close what it measured.

### What round 2 CONFIRMED as sound — do not re-audit

- **The classification table is total and exactly counted.** Reflection over the real types:
  `InstanceState` 31 = 11 + 20, `Token` 13 = 7 + 6, `HumanTask` 11 = 6 + 5, `NodeVisit`
  6 = 6 + 0. **Zero** unclassified, misspelled or duplicated fields, and every public field's
  type peels to a scalar, `time.Time`, or one of the three tabled structs.
- **`PublicState` withholds all seven enumerated variables sites** — 14 secret occurrences in
  the raw state, **0** in the projection.
- The fresh-literal primitive; the 11 render sites; the `AdminListInstances` exemption; the
  classification guard's `InstanceState` ablation behaving as designed; the 29-member adapter
  dispatch set, `diff`-identical across stdlib/gin/fiber.

### ACCEPTED — Critical, and each has a concrete mechanical fix

| id | defect | fix |
|---|---|---|
| E3 / FM-3 | `renderState(ctx, cfg CustomizeConfig[any], pi)` **does not compile** — `CustomizeConfig[R]` is generic over the router type and instantiations are non-assignable | thread the non-generic `authz.DisclosureSet`, as `cfg.InstanceMapper` already is |
| E11 / FM-2 / I2-C1 | **no code in `transport/http` ever calls `authz.ContextWithActor`** — the actor is a function argument, so `ActorFromContext` is false at all 11 sites and every authenticated caller is projected | resolve via `cfg.RequestActor` in the helper **and** derive the ctx with `ContextWithActor` in the task handlers; either half alone leaves a hole |
| E2 | "actor present" is true for the **zero actor** — the design drops the `isZeroActor` guard ADR-0189 put three lines away in the same package | reuse `isZeroActor`; presence must mean *identified*, not *non-nil* |
| E1 / FM-4 / I2-C3 | `/snapshot`'s rebuild **re-embeds** a definition `WithoutEmbeddedDefinition` suppressed (`NewProcessInstance` hardcodes `omitDefinition=false`); measured **781 → 1068 bytes**, and it hits *identified* callers | do not reconstruct via `NewProcessInstance`; project without rebuilding, or accept a `service` change and retract "service is not modified" |
| E5 / FM-1 / I2-C4 / F1 | `DiscloseAll` restores **11 of 31** withheld fields; 20 are restorable by nothing, including `incidents` and ADR-0175's `compensating` (the wedged-instance escape hatch). T8 **passes vacuously** on the standard fixture | make `DiscloseAll` a sentinel meaning *no projection*; add a machine-asserted **"restored by"** column to the classification table |
| E6 / I2-C2 | `/actionable` still emits `allowed_actions[].condition` under the closed default — the helper carries state, conditions come from the **definition** | gate the definition at `/actionable` too; the fixture must declare a condition (`ApprovalProcess`'s `f2` has none, so T1 is vacuous here) |
| E4 | Task 4 is scoped to 2 files but needs **11 exported signature changes and ~70 call sites across 4 packages**; it breaks Task 5's packages, so the by-package fan-out claim is wrong and Task 4 commits a red tree | fold Tasks 4–5 into one serial task; declare the public-API break in the ADR's Consequences |

### ACCEPTED — Major

- **E7 / F2:** `DiscloseActors` alone leaks `Completion.Note`; `DiscloseNotes` is read
  **nowhere** in the design. ⚠ And the prescribed blanking **writes through a shared
  `*Completion`** — two `pi.State()` calls return the same pointer. ⚠⚠ **No prescribed test
  touches `DiscloseActors` or `DiscloseNotes` at all**, so the plan's *"a test asserts it"*
  cites a test that does not exist, and `TestPublicState_DoesNotMutateInput` uses the zero set,
  which never enters the mutating branch.
- **E8 / FM-11:** *"withheld by construction"* is **false one level down** — `NodeVisit` is
  `slices.Clone`d, and `Claim`, `Completion`, `authz.Actor` and `Scope` are copied wholesale,
  outside the guard. The prescribed ablation adds a field to `InstanceState`, the level that
  works. Extend the guard to every type the projection copies.
- **FM-8:** the design governs **success bodies only**. `ClassifyError`'s 403 arm ships
  `err.Error()`, which for an ABAC failure is the entire predicate source, twice, with a caret
  diagram. Out of scope as written; must be either scoped in or stated as a residual.
- **FM-7 + FM-6:** kept structural fields are an **oracle** over withheld ones —
  `history[].node_id` reveals the gateway branch, and a branch is a function of the variables;
  `/actionable`'s conditions turn inference into a solve. Unmodelled.
- **F3:** *"`c.authz` is written in exactly one place"* is **false — there are two**; the missed
  writer is `service/service.go:200` (`AllowAll`), which this very spec cites elsewhere. A
  phase-2 gate built on the stated premise is fail-open where nothing was configured.
- **F6:** `admin_endpoints.go` has **four more** body-returning handlers no document mentions;
  `ListDeadLetters` renders `LastError` — the consumer-error-text category the ADR calls a
  residual — on a path `PublicState` never touches.
- **I2-C9:** `InstanceState` has **17 exported methods** that answer from whatever state they
  are on, so a consumer's `InstanceMapper` receiving the projection gets **false statements**,
  not merely fewer fields. Argues the projection should not share a type with the real thing.
- **E9:** `WithDisclosure` cannot infer `R`; the repo documents per-adapter option aliases as
  "REQUIRED, not cosmetic".

### Corrected in this revision, immediately

- ⛔ **E12 — the claimed baseline was FALSE.** See §0's rejected-findings entry. Fixed in all
  three places; the plan now says every failure is a regression.
- **F4** — *"six of them mutate"* is **five** (4 `PolicyAdmin` + `DeadLetterAdmin.Redrive`).
  ⚠ Round 1 found this, it was accepted, and **the fix vanished when the ADR's Context was
  rewritten**. An accepted finding that is not folded is not fixed — ADR-0189's lesson,
  repeated here.
- **F5** — *"seven variables sites"* is a total over an unstated subset; a reflective walk finds
  **nine** exported `map[string]any` sites (the two omitted are actor attributes, safe in
  revision 2 because both sit under withheld fields).

### REJECTED

- **F7–F10 (minor counting)**: the "18 failures in 3 packages" figure is retained explicitly as
  a **floor measured against revision 1's narrower posture**, in a bullet that already says
  "re-derive, do not inherit". No change needed beyond that wording.
