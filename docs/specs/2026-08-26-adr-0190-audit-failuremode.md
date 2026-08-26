# ADR-0190 adversarial audit — FAILURE-MODE lens

**Date:** 2026-08-26 · **Bundle commit:** `98382afd` · **Lens:** missing failure modes, edge
cases, migration gaps. Findings are appended as they are confirmed; probes were run in a
detached worktree and deleted.

Legend: **Critical** = ships a security hole or data corruption; **Major** = a stated
guarantee is false, or a documented consumer breaks with no migration; **Minor** = doc /
coherence defect.

---

## F1 — Critical — D6 (redaction) — `POST /instances/{id}/signals` is an unredacted fourth render path, byte-identical to the "render path 3" the bundle closes

**Decision attacked:** D6's central claim, *"There are three render paths, not two"* (spec
D6, ADR Decision 6), and the plan's Task 6, which wires exactly `StartInstance`,
`GetInstance` and `GetActionableView`.

**Failure scenario.** `httpcore.DeliverSignal` (`transport/http/httpcore/endpoints.go:82-94`)
ends with `return http.StatusOK, mapInstance(mapper, pi.State()), nil` — *the same
`mapInstance` call, on the same `pi.State()`, producing the same body* as
`GET /instances/{id}`. It is mounted on `InstanceRoutes` at
`POST /instances/{id}/signals` (`transport/http/stdlib/groups.go:80-91`), which under
Decision 1 is **explicitly unauthenticated**. `ProcessEngine.DeliverSignal`
(`service/service.go:371`) treats *a non-empty signal name matching no awaiting token* as
"a clean no-op, never a wrong-state error" (its own comment, citing ADR-0026), so an
arbitrary signal name on any non-terminal instance returns **200 with the full body**.

After phase 1 ships, an attacker who is denied `variables` on `GET /instances/{id}` simply
issues `POST /instances/{id}/signals {"signal":"x"}` and gets the identical document.
The delivery's headline security fix is bypassed by a one-word change to the request.

**Evidence (executed, worktree `98382afd`, probe deleted).** Harness instance seeded with
`{"ssn":"111-22-3333","salary":145000}`, `InstanceRoutes` mounted, **no actor on the
context**:

```
POST /instances/{id}/signals {"signal":"totally-bogus"}  -> 200
BODY={"instance_id":"da7aol…","def_id":"approval","def_version":1,"status":"running",
      "started_at":"…","variables":{"salary":145000,"ssn":"111-22-3333"}}

GET  /instances/{id}                                     -> 200
BODY={"instance_id":"da7aol…","def_id":"approval","def_version":1,"status":"running",
      "started_at":"…","variables":{"salary":145000,"ssn":"111-22-3333"}}
```

The two bodies are identical apart from nothing. The bundle redacts the second and not the
first.

⚠ All three adapters route through the same function — `gin/groups.go:94` and
`fiber/groups.go:86` both call `httpcore.DeliverSignal` — so the hole is present in every
mounted transport, and equally, a single fix in `httpcore` closes all three.

**Concrete fix.** Route `DeliverSignal`'s response through the same redaction as path 3:
`mapInstance(mapper, view.RedactState(pi.State(), service.RedactionOf(pi)))`. Add
`DeliverSignal` to plan Task 6's file list and to the T12 guard's enumeration, and correct
every "three render paths" sentence in the ADR, spec and plan — the guard in Task 7 must be
seeded from the *call sites of `mapInstance`* (`grep -n 'mapInstance(' endpoints.go`), not
from a prose list.

---

## F2 — Critical — D6 + D2 — the three human-task verbs render the same body, are outside Task 6, and can NEVER take D6's "gated ⇒ redact nothing" arm

**Decision attacked:** D6's effective-policy rule (*"a read that is **not** gated redacts the
configured set … a read that is gated and **passes** redacts nothing"*) combined with D2's
`Operation` constant list, which deliberately excludes the four `TaskManager` verbs
(spec §2.4: *"The 4 on `TaskManager` are already gated by the task's `Eligibility` spec"*).

**Failure scenario.** `ClaimTask`, `CompleteTask` and `ReassignTask`
(`transport/http/httpcore/endpoints.go:133,158,182`) each end with
`mapInstance(mapper, pi.State())` — the identical render used by `GET /instances/{id}`.
Executed today, authenticated as `alice/manager`:

```
POST /tasks/{taskID}/claim -> 200
BODY={"instance_id":"da7arep…","def_id":"approval","def_version":1,"status":"running",
      "started_at":"…","variables":{"salary":145000,"ssn":"111-22-3333"}}
```

Now apply the bundle. Two outcomes, both wrong:

- **If Task 6 leaves them alone** (which it does — its file list is `StartInstance`,
  `GetInstance`, `GetActionableView` only): D6's stated rule is violated by four endpoints,
  and the T12 guard *"every exported render path is redaction-aware"* must either fail on
  day one or be quietly narrowed to match the implementation — the "guard blind to the
  category of claim it polices" failure this repo already recorded against ADR-0187.
- **If they are redacted** to satisfy the rule: an authenticated, *eligible* manager who
  claims their task receives a body with `variables` stripped, and — because there is no
  `authz.Operation` constant for a task verb — `SpecFor` is never consulted for them, so
  they can **never** reach the "gated and passed ⇒ redact nothing" arm. The regression is
  **permanent and unfixable by configuration**, except by the global `WithRedaction()`
  opt-out, which simultaneously re-opens every anonymous read path. That is exactly the
  interaction hole CLAUDE.md rule #9's interaction-pass warning describes: D2's operation
  set and D6's gated-arm were each written correctly and are jointly unsatisfiable.

**Evidence.** The claim body above, executed at `98382afd`. `mapInstance` has **six** call
sites (`endpoints.go:42,52,94,133,158,182`); adding `GetInstanceSnapshot` and
`GetActionableView`, **eight** `httpcore` functions render an instance body. The bundle names
three, in the very section that congratulates itself for having corrected two to three.

**Concrete fix.** Either (a) add `OpClaimTask`/`OpCompleteTask`/`OpReassignTask`/
`OpRefreshTaskCandidates` to `authz.Operation` so the eligibility check can register as a
pass and the gated-arm becomes reachable; or (b) state explicitly in D6 that a **response to
a successfully authorized mutation** is never redacted, and carve the four verbs (plus
`DeliverSignal` once F1 is fixed) out of the rule with that reason — then encode the carve-out
in the T12 guard as asserted-exempt entries with their justification, alongside
`AdminListInstances`. Either way the "three render paths" enumeration must become eight.

---

## F3 — Critical — D6 / plan Task 5 — `RedactState` implements only 3 of the 4 categories and misses a FOURTH variables site; path 3 is NOT closed for a custom mapper

**Decision attacked:** D6's load-bearing claim that feeding `mapInstance` already-redacted
state *"closes path 3 even for a custom mapper, because the mapper never receives the
redacted fields in the first place"*, and the spec's redaction table asserting
`RedactVariables` has **3 sites** in state (`Variables`, `StartVariables`,
`Tokens[].Payload`) — an assertion the spec twice warns must not be "harmonised".

**Failure scenario.** `humantask.HumanTask` carries **`Vars map[string]any`** — its own doc
comment: *"a snapshot of the process Variables at task-creation time"* — and
**`Eligibility authz.AuthzSpec`**, the roles/privileges/attribute predicate that is precisely
what `RedactPolicy` exists to withhold. `engine.InstanceState.Tasks` is part of the struct a
custom `InstanceMapper` receives. The plan's `RedactState` touches neither. It also contains
**no `RedactPolicy` branch at all** — the category is inert in that function.

A consumer with `InstanceMapper: func(st) any { return st.Tasks }` — a wholly reasonable
mapper for a task inbox — discloses the full process variables and the full eligibility
policy to an unauthenticated caller, with the default `DefaultRedaction()` set applied.

**Evidence (executed).** The plan's Task-5 `RedactState` copied **verbatim** into
`runtime/view`, driven with `authz.NewRedactionSet(authz.DefaultRedaction()...)` — the
maximal set:

```
out.Variables        = map[]                                          (redacted OK)
out.Tasks[0].Vars    = map[salary:145000 ssn:111-22-3333]             <-- LEAK (4th variables site)
out.Tasks[0].Elig    = {Roles:[manager cfo] Privileges:[approve:high]
                        Attribute:actor.clearance == "secret" && vars.salary > 100000}
                                                                      <-- LEAK (RedactPolicy is a no-op)
out.Tasks[0].Claim   = &{Actor:{ID: Roles:[] Attributes:map[]} …}     (redacted OK)
```

**Concrete fix.** In `RedactState`: under `RedactVariables` also clear `out.Tasks[i].Vars`
(and take the task copy — note `needTaskCopy` is currently computed from Actors/Notes only,
so clearing `Vars` under Variables would today write through into the caller's live state;
see F4). Add a `RedactPolicy` branch clearing `out.Tasks[i].Eligibility`. Correct the spec's
site table to **four** state sites and re-derive the whole table from
`go doc engine.InstanceState` and `go doc humantask.HumanTask` rather than from the DTO.

---

## F4 — Major — plan Task 5 — `needTaskCopy` is derived from two categories, so any later per-task redaction silently corrupts the caller's live state

**Decision attacked:** the plan's own load-bearing comment, *"The `Claim`/`Completion`
pointer copies (`cc := *c`) are load-bearing … without them the zeroing would write through
into the caller's task records and corrupt `State()`."*

**Failure scenario.** `needTaskCopy := red.Has(RedactActors) || red.Has(RedactNotes)` guards
the `slices.Clone(st.Tasks)`. It is a hand-maintained enumeration of *which categories touch
a task*, sitting one line above the code it protects. The moment F3's fix adds a task-scoped
mutation under `RedactVariables` (`Tasks[i].Vars`) or `RedactPolicy` (`Tasks[i].Eligibility`)
without also amending `needTaskCopy`, a consumer configuring
`WithRedaction(authz.RedactVariables)` alone gets `out.Tasks` **aliasing** `st.Tasks`, and
`out.Tasks[i].Vars = nil` writes through into the engine's live task record. That is a
**data-corruption bug, not a disclosure one**: `Vars` is the snapshot the attribute-based
eligibility predicate evaluates against, so the next `ClaimTask` on that instance evaluates
`vars.salary > 100000` against an empty map — the deny-list-over-absent-vars fail-open the
bundle cites as backlog 103, manufactured by the redactor itself.

`TestRedactState_DoesNotMutateItsInput` (plan Task 5 step 1) **cannot catch this**: its
fixture uses `authz.NewRedactionSet(authz.RedactActors)`, which takes the `needTaskCopy`
branch. A guard tested with a fixture from the half that works.

**Concrete fix.** Delete `needTaskCopy` and clone `st.Tasks` unconditionally whenever `red`
is non-empty (the allocation is one slice per render), or better, call the existing
`humantask.HumanTask.Clone()` per task — it already deep-copies `Candidates`, `Eligibility.Roles`,
`Eligibility.Privileges`, `Claim`, `Completion`, `DueAt` and `Vars`, so the whole hand-rolled
copy block disappears and cannot rot. Add a table case to
`TestRedactState_DoesNotMutateItsInput` for **every single category in isolation**, not just
`RedactActors`.

---

## F5 — Major — D6 — `incidents[].error` is uncategorised free text that reaches the wire on the default `/snapshot` path

**Decision attacked:** D6's four-category enumeration, and its rationale for keeping
`Completion.Outcome` (*"a controlled vocabulary … not free text"*) while withholding
`Completion.Note` (*"the actor's free-text remark"*). By that exact test,
`incidents[].error` is free text and is not withheld.

**Failure scenario.** `runtime/processdriver_action.go:414` builds the failure trigger with
**`err.Error()`** — the raw, verbatim string returned by the consumer's `action.Action.Do`.
`engine/step_errors.go:225` stores it as `Incident.Error`, and
`service/instance.go`'s `newInstanceJSON` renders it as `incidents[].error`, unconditionally
and with no `omitempty` gate on the field. A consumer whose payment action returns
`fmt.Errorf("charge failed for card %s (customer %s): %w", card, ssn, err)` — the ordinary
way to write a debuggable error — publishes it to any unauthenticated reader of
`/snapshot`, after this bundle ships and with every category enabled.

The repo already treats raw error text as sensitive on the *error* path: the stdlib test
suite pins that a 5xx must not leak `"db connection refused: internal secret dsn info"`
(`transport/http/stdlib/stdlib_test.go`). The instance document has no equivalent guard.

**Evidence (executed).** `service.NewProcessInstance(nil, st)` with one incident, marshalled:

```
{"instance_id":"i1","def_id":"d","def_version":1,"status":"running",
 "variables":{"ssn":"111-22-3333"},
 "incidents":[{"id":"i1-inc1","kind":"IncidentAction","token_id":"t1","node_id":"charge",
   "error":"charge failed for card 4111111111111111 (ssn 111-22-3333): 402",
   "attempts":3,"created_at":"…"}],
 "started_at":"…"}
```

**Concrete fix.** Add a fifth category — `RedactDiagnostics` — covering `incidents[].error`
(and, for the state redactor, `InstanceState.Incidents[].Error`), included in
`DefaultRedaction()`. Keep `incidents[].id`, `kind`, `token_id`, `node_id`, `attempts` and
`created_at`: they are the structural fields an operator UI needs, and `kind` is a controlled
vocabulary by the same test that keeps `Outcome`. Document in the `Action` interface's godoc
that `Do`'s error string is durable and reader-visible.

---

## F6 — Critical — D5 vs D2 — a SERVICE-layer gate cannot run before the transport reads the body; the claimed 401→403→413→400→404 ordering is unachievable for every body-carrying operation

**Decisions attacked:** D2 (*"It is evaluated at the **service layer**, not the transport …
A transport-level gate would have been smaller and would have failed library-first"*) and D5
(*"The gate runs **before the request body is read** … Request ordering becomes
401 → 403 → 413 → 400 → 404"*). Both are stated as decided; they contradict.

**Failure scenario.** `transport/http/stdlib/groups.go:37-48` is the shape of every
body-carrying route:

```go
var in httpcore.StartInput
if !decodeRequestBody(cfg, w, req, &in) { return }   // body read → 413/400 decided here
status, body, err := httpcore.StartInstance(req.Context(), c.Svc, in, cfg.InstanceMapper)
```

`svc.StartInstance` cannot be called until `in` exists, and `in` cannot exist until the body
is read. A gate living inside `ProcessEngine.StartInstance` therefore runs **after** 413 and
after 400 — the exact ordering D5 says it prevents, and the exact primitive ADR-0189's F6
identified: an unauthorized caller can still force a full `MaxBodyBytes` (1 MiB) read and
hold the handler for `BodyReadTimeout` (30 s). ADR-0189 solved this for the task verbs by
calling `httpcore.RequestActor` **in the handler, at the transport**, before
`decodeRequestBody` — a transport-level position, which is precisely what D2 rules out.

Affected operations carry a body: `OpStartInstance`, `OpDeliverSignal`, `OpDeliverMessage`,
`OpResolveIncident`, `OpResolveCompensationStall`, `OpAdminRedrive`, `OpAdminAddPolicy`,
`OpAdminRemovePolicy`, `OpAdminAddRole`, `OpAdminRemoveRole` — ten of the twenty. The
body-free ones (the GETs) are exactly the ones for which the ordering claim is vacuous.

**Compounding: `OpStartInstance` cannot know its own subject.** `Subject` carries `DefID` and
`DefVersion`, but for `POST /instances` the definition reference lives **only** in the request
body (`httpcore.StartInput.DefRef`, decoded at `groups.go:38`). A pre-body gate for
`OpStartInstance` is handed `Subject{}` — all three fields empty. The single most natural
policy this feature exists to express, *"only role X may start process Y"*, is **inexpressible**
under D5, and D5 additionally forbids `OpStartInstance` from carrying an `Attribute`, closing
the other escape.

**Concrete fix.** Choose one and say so:
1. Keep the service-layer gate (library-first) and **withdraw D5's ordering claim**, stating
   plainly that a 403 for a body-carrying operation is decided after the decode and that the
   `MaxBodyBytes`/`BodyReadTimeout` primitive remains open to an authenticated-but-unauthorized
   caller — a residual, not a closed hole; or
2. Keep the ordering and add an explicit **transport-level pre-decode gate** for the
   operations whose subject is derivable from method + path alone, with the service-layer gate
   retained as the authoritative one for embedded consumers (defence in depth, mirroring
   `isZeroActor`'s existing double-check at `endpoints.go:120`). Then state which operations
   get the pre-decode gate and which cannot, and why.

Either way, `OpStartInstance` needs its own answer: gate it **post-decode** on a `Subject`
populated from `DefRef`, and record that as a deliberate exception with its ordering
consequence.

---

## F7 — Major — D2/D5 — `Subject.DefID` is empty on the pre-load path, and the policy has no way to distinguish "unknown" from "genuinely absent"

**Decision attacked:** `Subject`'s field comment, `DefID string // empty when unknown at gate
time`, combined with D5's pre-load evaluation and D3's `ok=false ⇒ deny`.

**Failure scenario.** For every instance-scoped operation the transport holds only the
instance id from the path; `DefID`/`DefVersion` live in the instance row, which the pre-load
gate refuses to fetch. So a consumer writing the obvious policy —

```go
switch { case op == authz.OpGetInstance && subj.DefID == "invoice": return financeSpec, true }
```

— sees `subj.DefID == ""` on **every** call and falls through to `return authz.AuthzSpec{}, false`,
which D3 makes **deny**. Result: the consumer wires a policy intended to restrict invoices and
silently loses every instance read in production, with no error, no log and no construction-time
signal. The mirror-image consumer who writes `if subj.DefID == "" { return spec, true }` as a
default arm silently opens every read instead. The design forces a coin flip and names neither
outcome.

`ok=false ⇒ deny` is chosen (D3) precisely so a fall-through is safe; here the fall-through is
*guaranteed*, so the safety property degenerates into "the feature does not work".

**Concrete fix.** Either drop `DefID`/`DefVersion` from `Subject` for the pre-load phase and
document that a definition-keyed policy must use an `Attribute` (accepting the enumeration
oracle D5 already licenses for that case), or add an explicit
`Subject.Resolved bool` / `Phase` discriminator so a policy can tell "not looked up yet" from
"no definition", and require D5 to run a **second** gate pass post-load with the populated
subject when the first pass returns a sentinel meaning "I need the subject resolved". Whichever
is chosen, add a prescribed test with a `DefID`-keyed policy — the bundle has none.

---

## F8 — Major — D4a — there is no way to supply an `Authorizer` without also wiring human tasks, so `WithOperationPolicy` is unusable for a consumer with no human-task nodes

**Decision attacked:** D4a, *"Calling `WithOperationPolicy` while the resolved `Authorizer` is
`authz.AllowAll` is a construction error"*.

**Failure scenario.** Source-verified: `c.authz` is assigned at **exactly one** place in the
package, `service/options.go:83`, inside `WithHumanTasks(taskStore humantask.TaskStore, az
authz.Authorizer)`. The only other write is the `AllowAll` default at `service/service.go:200`.
There is **no `service.WithAuthorizer` option**. Therefore a consumer running a
purely-automated process — service tasks, gateways, timers, no user tasks — who wants to gate
`OpStartInstance` and `OpCancelInstance` has no path: their resolved authorizer is `AllowAll`,
D4a refuses construction, and the only remedy is to wire a human-task store they do not use
just to smuggle an `Authorizer` in through its second parameter. `NewProcessEngine` returns an
error that names the wrong problem.

Migration check (mandate item 4): no in-repo consumer breaks today, because
`WithOperationPolicy` does not yet exist — D4a is not retro-breaking. `casbinauthz` **is**
detected correctly: `if _, ok := c.authz.(authz.AllowAll); ok` is false for any concrete
casbin type, so a casbin-wired consumer passes. The gap is the missing option, not the
type check.

**Concrete fix.** Add `service.WithAuthorizer(az authz.Authorizer) Option` in the same bundle
that adds `WithOperationPolicy` (Phase 2), assigning `c.authz`, and have `WithHumanTasks`
delegate to it so there is one writer. D4a's error text should name both remedies explicitly.

---

## F9 — Major — D3 — `ok=false ⇒ deny` gives a library upgrade a silent production outage, with no migration story and no compile-time signal

**Decision attacked:** D3's stated benefit, *"`ok=false` denying also means a future operation
added to this library cannot silently open itself in an existing deployment."* True — and its
unstated cost is the whole failure mode.

**Failure scenario.** `Operation` is `type Operation string`. Adding
`OpAdminSomethingNew Operation = "admin_something_new"` in a later release is **not** a
breaking API change: the consumer's `switch op { … }` compiles unchanged, falls through,
returns `(AuthzSpec{}, false)`, and the new operation is denied for everyone the moment they
run `go get -u`. Nothing warns them: no compile error (a string-backed enum cannot produce
one), no construction-time check (the policy is an opaque interface), no log line (the design
prescribes none for the deny path), and the deny arrives as a plain 403 indistinguishable from
a policy decision the consumer actually made.

The bundle's own phasing makes this concrete rather than hypothetical: Phase 2 ships eight
`Service` operations, Phase 3 adds twelve admin ones. **A consumer who adopts the gate at
Phase 2 has every admin operation switched off by their next upgrade.**

**Concrete fix.** Ship the migration surface with the decision, in Phase 2:
- export `authz.AllOperations() []Operation`, and prescribe a consumer-runnable conformance
  helper (`authz.CheckPolicyCoverage(p OperationPolicy) []Operation`) returning the operations
  for which `SpecFor` answers `ok=false` — a consumer puts it in their own test and their CI
  breaks on upgrade instead of production;
- emit a **WARN `slog` record**, once per operation (not per request), the first time a
  configured policy answers `ok=false` for a given `Operation`, naming it;
- state the upgrade contract in the ADR's Consequences: *adding an `Operation` constant is a
  behaviourally breaking change for any consumer with a policy configured, and must be called
  out in the changelog.*

---

## F10 — Major — D7 — nothing signals an admin deployment that forgot the decorators, and the surrounding API makes forgetting the default

**Decision attacked:** D7's opt-in decorator model, and the ADR's own Consequence, *"an admin
deployment that forgets them is gated exactly as much as today: not at all"* — recorded, but
recorded as acceptable rather than mitigated. A residual you wrote down is still a defect you
shipped.

**Failure scenario.** `stdlib.AdminRoutes` (`transport/http/stdlib/groups.go`) is a plain
struct of six consumer-supplied interface fields:

```go
type AdminRoutes struct {
    Svc service.Service; DeadLetters service.DeadLetterAdmin; Policies service.PolicyAdmin
    RelayStats service.RelayStatsAdmin; Timers service.TimerAdmin; Lineage service.LineageAdmin
}
```

A decorated and an undecorated `PolicyAdmin` are indistinguishable at that field's type, so a
consumer who mounts `Policies: myCasbinAdmin` instead of
`Policies: service.GuardedPolicyAdmin(myCasbinAdmin, pol, az)` gets an unauthenticated,
unaudited `POST /admin/policies` — the operation that **rewrites the authorization policy
itself** — and no error, no log line and no test will say so. The comparison the mandate asks
for is instructive: `service.WithHumanTasks(taskStore, az)` takes the authorizer as a
**positional parameter**, so the human-task path cannot be wired without deciding about
authorization. The admin path can.

**Concrete fix.** Make the omission visible without breaking ADR-0095's default-absent posture
(which forbids default-deny, not visibility):
- give each decorator an unexported marker method and have `AdminRoutes.Customize` log a
  **WARN once per mount** naming each dependency that is not a `Guarded*` — mirroring
  `logConstructionSummary`'s existing `authz: allow-all` label at `service/service.go:316`,
  which sets the precedent for *reporting* a permissive default rather than refusing it;
- add the same line to `logConstructionSummary`'s `hint` field so it appears in the one place
  a consumer already looks;
- add an `examples/production_wiring` diff showing the decorated form, since that example is
  the bundle's cited proof that fail-closed admin wiring still works.

---

## F11 — Major — plan Task 7 (T12) — the guard's mechanism is unspecified, and the only mechanism satisfying its own prescribed ablation is source parsing, which the plan never mentions

**Decision attacked:** D6's *"machine-checked guard"*, which the spec offers as the reason the
structurally-safe alternative (redact at the source) could be rejected. It is the bundle's
single automated defence against exactly the enumeration error F1 and F2 demonstrate.

**Failure scenario.** Plan Task 7 says the guard *"enumerates every exported function in
`httpcore` returning a rendered instance body and asserts each is redaction-aware"*, and Step 2
prescribes the ablation: *"Add a new unredacted render path returning `pi.State()` directly.
Run the guard. It must go RED."* Go has no runtime reflection over a package's function set, so
the only implementations available are (a) a hand-maintained slice of names — which **cannot**
detect a newly added function and so **cannot** pass its own prescribed ablation; or (b) a
`go/ast` scan of `endpoints.go`/`admin_endpoints.go` for `mapInstance(` and `pi.State()`
call sites. The plan prescribes neither, has no `go/parser` import anywhere, and writes no code
for this task — the only task in the plan whose body is described rather than written, which
the plan's own Self-review notes flag as *"a plan failure by this skill's own rules"* when it
happened to Task 5.

A hand-maintained list here is a prose enumeration wearing a test's clothing, and the bundle's
own history says prose enumerations rot: this one has already been wrong twice (two→three in
the spec, three→eight per F1/F2).

**Concrete fix.** Specify the mechanism in the plan, with code: parse the package's
non-test `.go` files with `go/parser`, collect every top-level exported `func` whose body
contains a call to `mapInstance` or a selector `.State()` or returns a `service.ProcessInstance`,
and assert each name appears in either the redaction-aware set or an `exempt` map whose values
are justification strings. Assert the exempt map is **non-stale** (every key still resolves to
a function in the parsed set). Then run the prescribed ablation and paste the observed RED text
into the plan's `▶ Progress`.

---

## F12 — Major — D8 phasing — Phase 1 offers only two settings, "redact everything for everyone" or "redact nothing for anyone", so no real deployment can keep it on

**Decision attacked:** D8's claim that *"Each phase is independently shippable and
verifiable"* and that Phase 1 *"Ships the live security fix without waiting for the gate"*.

**Failure scenario.** Phase 1 has, by the plan's own words, *"no gate concept. The effective
redaction set is simply the configured one."* So the policy is a single global constant applied
to every render, for every caller, authenticated or not. Concretely, after Phase 1:

- default (no option): `GET /instances/{id}` returns no `variables` **to anyone** —
  including the consumer's own authenticated front-end. Per F2, `POST /tasks/{id}/claim`
  returns none either if Task 6 is extended to satisfy D6's rule.
- the only remedy is `WithRedaction()` with no arguments, which the ADR itself documents as
  restoring the pre-0190 wire shape — i.e. **re-opening the exact anonymous disclosure the
  phase exists to close.**

Any consumer whose UI reads `variables` — the ordinary case, and the reason `NewInstanceView`
renders them at `view.go:31` — is therefore pushed to the global opt-out on day one, and the
security fix is off in precisely the deployments that adopted it. The differentiation that
makes the default tolerable (D6's "gated and passed ⇒ redact nothing") arrives only in Phase 2,
which the plan explicitly does not schedule and which must be separately designed, audited and
delivered.

Relatedly, Phase 1's default silently *creates* a clean existence oracle where a full
disclosure previously stood: a real id returns 200 with structural fields, a fabricated one
returns 404. D5 reasons carefully about not adding an enumeration oracle and never notes that
the no-policy deployment — the default, and the only one Phase 1 can produce — has one wide
open.

**Concrete fix.** Do not ship Phase 1 alone with a default-closed global set. Options, in
descending preference:
1. Ship Phases 1 and 2 as one delivery, so the default is "redact unless the request passed a
   gate" from the first release and the differentiation exists.
2. Ship Phase 1 with the default **inverted** (`WithRedaction()` semantics by default,
   i.e. no behavioural change) plus prominent documentation, and flip the default to closed in
   Phase 2 when it becomes survivable — trading a delayed fix for a fix consumers keep on.
3. Ship Phase 1 default-closed but add the minimum differentiator it needs to be usable: an
   `authz.ActorFromContext(ctx)`-presence check — *an identified caller sees full fidelity,
   an anonymous one does not*. This needs no `OperationPolicy`, uses only ADR-0189's existing
   ctx→actor translation, and does not violate D1 (it authenticates nothing; it reads what the
   consumer's middleware already put there).

If Phase 1 ships as written anyway, the ADR must say plainly that the default is expected to be
switched off by most consumers until Phase 2 lands, rather than presenting it as *the*
disclosure fix.

---

## F13 — Major — plan Task 4 — `RedactionOf` fails closed for a foreign `ProcessInstance`, so the documented complete opt-out is not complete

**Decision attacked:** the ADR's Negative consequence, *"the opt-out is `WithRedaction()` with
an empty set"*, and plan Task 4's deliberate asymmetry note.

**Failure scenario.** `RedactionOf(pi)` returns `authz.DefaultRedaction()` for any
`ProcessInstance` not implementing `Redactable`. `httpcore.GetInstance` receives its `pi` from
`svc.GetInstance`, and `svc` is the **public `service.Service` interface** — a consumer who
wraps the engine (for caching, multi-tenancy, or metrics) and returns their own
`ProcessInstance` type gets *everything redacted*, and `WithRedaction()` on the engine config
never reaches the decision because `RedactionOf` never consults the engine. The documented
opt-out silently does nothing for that consumer, and there is no other knob.

Source-verified that no in-repo type besides `service.processInstance` implements
`ProcessInstance` (`grep -rn 'State() engine.InstanceState'` returns `service/instance.go:22,70`
and two test-helper *constructors*, not implementations) — so the plan's claim is true, and the
exposure is purely to external consumers, who are the product's audience.

For the in-repo case the opt-out **is** byte-exact: `RedactState(st, emptySet)` takes no branch
and returns `st` unchanged; `newInstanceJSON(..., emptySet)` takes no branch;
`NewActionableViewRedacted(..., emptySet)` degenerates to `NewActionableView`. That half of the
claim holds.

**Concrete fix.** Have the transport carry the engine's policy rather than interrogating the
instance: thread the resolved `authz.RedactionSet` through `httpcore.CustomizeConfig` (a
`WithRedaction` `CustomizeOption`, defaulting to the engine's), and use `RedactionOf(pi)` only
as a per-instance **override** when the instance implements `Redactable`. Then a foreign
`ProcessInstance` inherits the consumer's configured posture instead of the maximal one.

---

## F14 — Major — spec §9 item 3 — the breakage member set omits a test that Phase 1 provably breaks, and it is an ADR-0186 wire-shape guard

**Failure scenario.** The spec lists eight test files as the candidate breakage set,
"listed, not counted". `transport/http/stdlib/maxbody_test.go` is **not** among them, and its
`TestUnderCapBehaviourIsUnchanged` — the ADR-0186 guard pinning that an under-cap
`POST /instances` body still produces today's wire — asserts:

```go
vars, ok := resp["variables"].(map[string]any)
require.True(t, ok, "variables missing: %v", resp)
assert.Equal(t, "hi ada", vars["greeting"])
```

Plan Task 6 names `StartInstance` as "path 3's second entry point" and feeds it redacted
state, so `variables` becomes absent and this `require.True` fails. The plan's verification
checklist says *"Expect breakage in the eight files named in spec §9 item 3; each break must be
adjudicated as correct"* — a break outside the named eight will read as an unexpected
regression rather than an adjudicated one, in a file whose entire purpose is to detect wire
changes.

`transport/http/httpcore/view_test.go:42` also builds an `InstanceState` carrying `Variables`
and is likewise absent from the eight, though whether it breaks depends on whether Task 6's
change reaches `NewInstanceView`'s own unit tests (it tests the mapper directly, so probably
not) — flagged as a member-set gap rather than a confirmed break.

**Concrete fix.** Re-derive the member set during implementation from a compile-and-run
ablation (make the change, run `go test ./... 2>&1 | grep -E '^(---|FAIL)'`, list the
failures) rather than from a grep, and paste the resulting file list into the plan's
`▶ Progress`. Per this repo's own lesson, two nets can agree on a total while being disjoint
on members — and here the grep net and the compile net are disjoint by construction, because
a `require.True` on a missing map key is invisible to both a signature ablation and a
literal grep.
