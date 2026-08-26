# ADR-0190 bundle — adversarial audit, EXECUTION lens

**Date:** 2026-08-26 · **Auditor:** execution lens (rule #9) · **Bundle commit:** `98382afd`
**Worktree:** detached at `98382afd`; every probe below was compiled and run there.

Documents attacked:
- spec `docs/specs/2026-08-26-route-group-authorization-posture-design.md`
- ADR `docs/adr/0190-authorization-is-gated-by-policy-not-by-authentication.md`
- plan `docs/plans/2026-08-26-route-group-authorization-posture.md`

Every finding below is marked **CONFIRMED BY EXECUTION** or **SUSPECTED (not executed)**.

---

## Summary table

| ID | Severity | One-clause statement | Status |
|---|---|---|---|
| F1 | **Critical** | `POST /instances/{id}/signals` discloses every process variable unauthenticated — a fourth path the bundle names nowhere, and Task 6 does not wire it | executed |
| F1b | **Critical** | the phase split forces a choice between shipping F1 open and redacting the three ADR-0189-authenticated task verbs for a whole release | executed (mechanism) |
| F2 | **Critical** | `ResolveIncident` / `CancelInstance` / `ResolveCompensationStall` render `NewInstanceView(pi.State())` directly — a fourth render mechanism; `admin_endpoints.go` is absent from the plan | executed |
| F3 | — | the `AdminListInstances` exemption holds (executed, not merely read) | executed |
| F4 | **Critical** | `RedactVariables` has **seven** sites in `engine.InstanceState`, not three; the plan's own `RedactState` leaks four, and test T3b would pass anyway | executed |
| F5 | Major | `RedactPolicy` misses `InstanceState.Tasks[].Eligibility` — the policy field D6 exists to protect | executed |
| F6 | Major | `Incidents[].Error` (free text derived from failures) belongs to no category and is neither redacted nor declared structural | executed |
| F7 | Minor | plan Task 5's code does not compile — `model` import missing | executed |
| F8 | Major | the value-copy redaction approach needs a two-level clone for the compensation collections; the no-mutation test's fixture cannot detect the miss | executed |
| F9 | Major | Decision 4b's "only `Privileges`" narrowing misses `Privileges` **plus** anything — the built-in `RoleAuthorizer` still silently allows | executed |
| F10 | Major | Decisions 4b and 5 interact to leave `Roles` as the only expressible dimension for all 13 instance-less operations | executed (premises) |
| F11 | Major | the purity guard's prescribed ablation is an **import cycle** — `[setup failed]`, the test never runs; a valid ablation was found and executed | executed |
| F12 | — | no in-repo type outside `service/instance.go` implements `service.ProcessInstance` (interface-ablation + `go vet ./...`) | executed |
| F13 | Major | `NewProcessInstance` opting out of redaction is a fail-**open** path through a public constructor, already reachable via the gin transport | executed |
| F14 | Major | Task 7 (the T12 guard) is prose with no code, while the plan's self-review asserts the placeholder scan is clean | read |
| F15 | Minor | Task 2 cites `ResolveConfig`, which lives in `httpcore` not `service`, then prescribes the post-loop guard it forbids | executed |
| F16 | Minor | `Completion.Actor` is a second non-`omitempty` exception; D6 claims there is one | executed |
| F17 | — | the gin/fiber parity assumption holds at the delegation level — and therefore generalises F1 and F2 to all three transports | executed |
| F18 | Info | the tree is not green at `98382afd`: `internal/database` and `internal/dbtest` fail pre-existing | executed |
| F19 | Major | Phase 1 offers no configuration where an authorized HTTP reader sees variables and an unauthorized one does not | read (mechanism executed) |
| F20 | Minor | §2.1 and §5 still say "both"/"both read endpoints" in the document whose headline correction is that count | read |
| F21 | Minor | the "Measured" `go list -deps ./authz` sentence names the wrong command for the number it claims | executed |
| F22 | Minor | the bare citation `view.go:31` is ambiguous between `runtime/view/` and `transport/http/httpcore/view.go` | executed |
| F23 | — | §2.4's 12+12=20 operation member set is exact | executed |
| F24 | Major | the §9 breakage net is wrong both ways: 6 of 8 predicted files do not break; `stdlib/maxbody_test.go` does and is unnamed | executed |
| F25 | **Critical** | three prescribed assertions (`alice@corp.example`, `claim.actor.attributes`, `allowed_actions[].condition`) are **vacuous** against the standard harness | executed |

**Criticals: 5 (F1, F1b, F2, F4, F25). Majors: 10. Minors: 6. Confirmed-sound: 4.**
Every finding except F14, F19 and F20 rests on an executed probe.

---
## F1 — CRITICAL — `POST /instances/{id}/signals` is a FOURTH unauthenticated disclosure path, named nowhere in the bundle

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Spec §2.1 / ADR Context: *"**Three** read paths disclose … `GET /instances/{id}`,
`/snapshot` and `/actionable`"*; spec D6 *"Reaching the renderers — there are THREE, not two"*;
plan Task 6 step 3 wires *"`GetInstance` … and the equivalent in `StartInstance` (path 3's second
entry point) and `GetActionableView`"*.

**Probe.** `transport/http/stdlib/zzz_audit_probe_test.go` — `stdlib.Mount(mux, svc)` with **no**
actor on the context, start a `SignalProcess` instance with `vars={ssn, salary}`, then
`POST /instances/{id}/signals` with `payload={card}`, all unauthenticated.

**Observed (verbatim):**
```
PROBE POST /instances/{id}/signals -> 200
  {"instance_id":"da7apcp83g3llnp7rdcg","def_id":"signal-catch-go","def_version":1,
   "status":"completed","started_at":"…","ended_at":"…",
   "variables":{"card":"4111111111111111","salary":145000,"ssn":"111-22-3333"}}
```

**Mechanism.** `httpcore.DeliverSignal` ends with
`return http.StatusOK, mapInstance(mapper, pi.State()), nil` (`transport/http/httpcore/endpoints.go`).
`mapInstance` is called at **six** sites in `endpoints.go`, not the two the plan names:
`StartInstance`, `GetInstance`, **`DeliverSignal`**, `ClaimTask`, `CompleteTask`, `ReassignTask`.
The spec's §1 does note that `/signals` is "state-changing and open", but never that its
*response* discloses; §2.1 counts disclosure paths and stops at three.

**Consequence.** Implementing the plan literally (Task 6 modifies `GetInstance` + `StartInstance` +
`GetActionableView`) ships the fix with `POST /signals` still disclosing every process variable to
an unauthenticated caller — the exact defect the delivery exists to close, on a route the spec
itself flags as open.

**Fix.** Redact at `mapInstance` itself (one site, all six callers) rather than at individual
endpoints — and then resolve F1b below, which that fix creates. Update spec §2.1, ADR Context and
D6's render-path enumeration to name `DeliverSignal`. Add the `POST /signals` case to plan Task 6's
table-driven test.

---

## F1b — CRITICAL — the phase split has no answer for the three ADR-0189-authenticated verbs

**CONFIRMED BY EXECUTION** (that the three verbs render through the same `mapInstance` call).

**Claim attacked.** Spec §10 / ADR Decision 8: *"Phase 1 … No gate concept: the effective set is
simply the configured one"*, and D6's effective policy *"read **not** gated → the configured set,
defaulting to all four"*.

**Probe.** `grep -n "mapInstance" transport/http/httpcore/endpoints.go` → six call sites; three of
them are `ClaimTask`, `CompleteTask`, `ReassignTask`, which ADR-0189 already gates on a resolved
`authz.Actor` (they refuse the zero actor with `ErrUnauthenticated`).

**The dilemma the bundle never states.** Phase 1 has no gate, so "ungated ⇒ redact everything"
applies to *every* `mapInstance` caller:

- Redact at `mapInstance` (the only place that closes F1) ⇒ an **authenticated** manager who
  claims or completes a task gets back an instance document with `variables` stripped, for a whole
  release, until Phase 2 restores it. That is a functional regression on the one surface ADR-0189
  just authenticated.
- Redact per-endpoint (what the plan actually prescribes) ⇒ F1 ships open.

**Fix.** Either (a) pull the "gated ⇒ redact nothing" arm of D6 forward into Phase 1 for the three
human-task verbs — they already resolve an actor, so the arm is `if actorPresent { empty set }` —
or (b) state explicitly in the ADR and plan that the three task verbs' responses are redacted in
Phase 1 and restored in Phase 2, and put that in the breaking-change notice of Task 8. Option (a)
is preferable: option (b) ships a documented regression. Either way, spec §10's claim that
"Each phase is independently shippable" needs the qualification.

---

## F2 — CRITICAL — three admin endpoints render `NewInstanceView(pi.State())` directly; `admin_endpoints.go` is not in the plan at all

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Spec D6: *"Reaching the renderers — there are THREE, not two"*, and
*"**Not affected:** `AdminListInstances` …"* — presented as the only admin render path needing a
verdict. Plan File Structure lists `transport/http/httpcore/endpoints.go` and never
`admin_endpoints.go`.

**Probe.** `transport/http/stdlib/zzz_audit_probe2_test.go` — mount `stdlib.Mount` plus
`stdlib.AdminRoutes{Svc: svc}.Customize(mux)` with **no** middleware (ADR-0095's default-absent
posture permits exactly this, and D1 keeps it reachable), start an instance with
`vars={ssn, salary}`, then `POST /admin/instances/{id}/cancel` unauthenticated.

**Observed (verbatim):**
```
PROBE POST /admin/instances/{id}/cancel -> 200
  {"instance_id":"da7apj983g3ln9b1tsug","def_id":"signal-catch-go","def_version":1,
   "status":"terminated","started_at":"…","ended_at":"…",
   "variables":{"salary":145000,"ssn":"111-22-3333"}}
```

**Mechanism.** `transport/http/httpcore/admin_endpoints.go` returns
`NewInstanceView(pi.State())` at **three** sites — `ResolveIncident` (line 111),
`CancelInstance` (line 121), `ResolveCompensationStall` (line 514). This is a **fourth render
mechanism**: it bypasses `mapInstance` entirely, so feeding `mapInstance` redacted state (D6's
chosen fix for "path 3") does **not** touch it.

**Consequence.** D6's uniformity claim — *"closes all three paths uniformly"* — is false as
written, and the T12 guard, if it enumerates "the three render paths plus `AdminListInstances`",
will be built around an enumeration that is already wrong by three entries. The guard would then
pass while three admin endpoints disclose, which is the precise failure mode T12 exists to prevent.

**Fix.** Add `admin_endpoints.go` to the plan's File Structure and to Task 6; redact the state fed
to `NewInstanceView` at all three sites. Restate D6's enumeration as *four mechanisms*
(`newInstanceJSON` self-marshal, `NewActionableView`, `mapInstance`, direct `NewInstanceView`) over
**eleven** exported httpcore functions. Make T12 derive its list from the package rather than from
a hand-written literal, or the guard inherits the same wrong count.

---

## F3 — NOT A DEFECT — the `AdminListInstances` exemption holds under execution

**CONFIRMED BY EXECUTION.** Spec D6 says the exemption was *"Verified by reading the projection"*
— Premise Discipline asks for execution. Executed:
```
PROBE GET /admin/instances -> 200
  {"items":[{"instance_id":"…","def_id":"signal-catch-go","def_version":1,"status":"terminated",
    "started_at":"…","ended_at":"…","incident_count":0}],
   "next_cursor":"","has_more":false,"total_count":0}
```
No variables, no actors, no notes, no policy. The exemption is correct. (Unrelated observation,
out of scope: `total_count` reports 0 while one item is returned.)

---
## F4 — CRITICAL — `RedactVariables` has SEVEN sites in `engine.InstanceState`, not the THREE the plan re-derives; and the plan's own `RedactState` leaks four of them

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Plan Task 5 step 3: *"⚠⚠ **`RedactVariables` has THREE sites in
`engine.InstanceState`, not two.** Beyond `Variables` and `Tokens[].Payload`, the struct carries
**`StartVariables`** … A `RedactState` that clears two of three leaks through path 3."* Also spec
D6's table (*"**state:** `InstanceState.Variables` + `StartVariables` + `Tokens[].Payload`
(**3 sites**)"*) and ADR Decision 6 (*"`engine.InstanceState` has **three**"*).

**Probe.** `runtime/view/zzz_audit_redact.go` — the plan's `RedactState` transcribed **verbatim**
(only the symbol renamed `RedactState_Probe`); `runtime/view/zzz_audit_redact_test.go` builds an
`engine.InstanceState` with every variable-bearing field populated and calls it with
**all four categories**.

**Observed (verbatim):**
```
PROBE after RedactState(ALL FOUR CATEGORIES):
  out.Variables                      = map[]
  out.StartVariables                 = map[]
  out.Tokens[0].Payload              = map[]
  out.Tasks[0].Vars                  = map[ssn:111-22-3333]   <-- LEAK
  out.Tasks[0].Eligibility           = {Roles:[manager] Privileges:[] Attribute:vars.department == "finance"}   <-- LEAK (policy)
  out.RootCompensations[0].Input     = map[ssn:111-22-3333]   <-- LEAK
  out.Scopes[0].Compensations[0].Inp = map[ssn:111-22-3333]   <-- LEAK
  out.ArchivedCompensations[sub][0]  = map[ssn:111-22-3333]   <-- LEAK
  out.Incidents[0].Error             = "action \"charge\" failed for ssn 111-22-3333"   <-- LEAK (free text)
```

**These fields really do carry process variables — verified in a live run, not reasoned:**
```
PROBE st.Variables            = map[salary:145000 ssn:111-22-3333]
PROBE st.StartVariables       = map[salary:145000 ssn:111-22-3333]
PROBE st.Tasks[0].Vars        = map[salary:145000 ssn:111-22-3333]      <-- 4TH VARIABLES SITE
PROBE st.Tasks[0].Eligibility = {Roles:[manager] Privileges:[] Attribute:}   <-- POLICY SITE outside `definition`
```
(`transport/http/stdlib/zzz_audit_probe3_test.go`, a real `POST /instances` on `ApprovalProcess`
followed by `svc.GetInstance(...).State()`.)

**Source of each site** (so the correction is checkable rather than another counted claim):
- `engine/step_nodes.go:743` — `Vars: copyVars(c.s.Variables)` mints every `HumanTask` with a
  **copy of the whole instance variable map** ⇒ `InstanceState.Tasks[].Vars`.
- `engine/step_triggers.go:158` and `engine/step_nodes.go:582` — `recordCompensation(..., copyVars(s.Variables))`
  ⇒ `CompensationRecord.Input` is a full variables copy, landing in
  `InstanceState.RootCompensations[]`, `InstanceState.Scopes[].Compensations[]` and
  `InstanceState.ArchivedCompensations[key][]` (`engine/state_compensation.go:328-343`).

**The corrected enumeration is SEVEN variable-bearing sites** in `engine.InstanceState`:
`Variables`, `StartVariables`, `Tokens[].Payload`, `Tasks[].Vars`, `RootCompensations[].Input`,
`Scopes[].Compensations[].Input`, `ArchivedCompensations[k][].Input`. An eighth is *suspected*
(not executed): `Compensating.Records` — the ADR-0171 pinned-records slice, which marshals as
`"Records":null` in the probe's JSON dump and is `[]CompensationRecord`, hence also `Input`-bearing
once a walk is in flight.

**Why this is Critical, not Major.** Plan test T3b asserts only
`if _, ok := seen.Variables["ssn"]; ok`. With the plan's `RedactState` as written that assertion
**passes** while four other fields still carry the same `ssn`. This is verbatim the failure the
spec itself names — *"a guard tested with a fixture from the half that works"* — reproduced inside
the very task written to avoid it.

**Fix.**
1. Correct the count in spec D6, ADR Decision 6 and plan Task 5 to **seven** (or, better, follow
   the repo's own advice and name the closed set rather than count it).
2. Extend `RedactState` to clear `Tasks[].Vars`, and to clone-and-clear `Input` on all three
   compensation-record collections (`slices.Clone` + per-element copy; `ArchivedCompensations`
   needs `maps.Clone` plus a per-key `slices.Clone`, which the plan's value-copy approach does
   **not** give you — the map header is shared).
3. Make T3b's fixture populate **all seven** and assert on all seven, or the test is vacuous.
4. Decide `Compensating.Records` explicitly rather than leaving it unenumerated.

---

## F5 — MAJOR — `RedactPolicy` misses `InstanceState.Tasks[].Eligibility`, the exact field D6 says it is protecting

**CONFIRMED BY EXECUTION** (see F4's output line `out.Tasks[0].Eligibility = {Roles:[manager] …}`).

**Claim attacked.** Spec D6's category table: *"`RedactPolicy` | the embedded `Definition`
(carries every node's `Eligibility`); `ActionableView.OpenTasks[].AllowedActions[].Condition`"*.

**Observed.** After `RedactState` with all four categories, `out.Tasks[0].Eligibility` still
carries `{Roles:[manager] Attribute:vars.department == "finance"}` — the node's authorization
policy **and** an attribute predicate that itself names a process variable. A custom
`InstanceMapper` rendering `st.Tasks` therefore publishes the eligibility policy in full even
though `RedactPolicy` is active and the embedded `Definition` was dropped.

**Fix.** Add `Tasks[].Eligibility` to `RedactPolicy`'s site list in spec D6 and ADR Decision 6, and
zero it in `RedactState` under `RedactPolicy`. Note that this changes `needTaskCopy` — today it is
`RedactActors || RedactNotes`; it must also include `RedactPolicy`, or the clone is skipped and the
zeroing writes through into the caller's `State()`.

---

## F6 — MAJOR — the four categories have no home for `Incidents[].Error`, which is free text derived from action failures

**CONFIRMED BY EXECUTION** (F4 output: `out.Incidents[0].Error = "action \"charge\" failed for ssn 111-22-3333"`).

**Claim attacked.** Spec D6: *"Four named categories"*, and *"Structural fields — instance/def/task/node
IDs, statuses, timestamps, retry counts — are always kept"*. `Incidents` is neither enumerated as a
redaction site nor declared structural.

**Mechanism.** `instanceJSON.Incidents[].Error` (`service/instance.go`) is rendered on `/snapshot`
today, and `InstanceState.Incidents[].Error` reaches a custom mapper. The string is whatever a
service action's error carried — in this repo's own action-failure paths it is the wrapped error
text, which routinely embeds inputs.

**Fix.** Adjudicate explicitly: either add `Incidents[].Error` to `RedactNotes` (it is the other
free-text field on the document), give it its own category, or record in D6 that error text is
deliberately kept and say why. Silence is the defect — an unenumerated field on a *closed-by-default*
posture is a hole in the posture's own claim.

---

## F7 — MINOR (compile) — plan Task 5's `NewActionableViewRedacted` does not compile as written

**CONFIRMED BY EXECUTION.**

**Probe.** Task 5 step 3's code transcribed verbatim (import block exactly as printed) into
`runtime/view/zzz_audit_redact.go`, then `go build ./runtime/view/`.

**Observed (verbatim):**
```
# github.com/kartaladev/wrkflw/runtime/view
runtime/view/zzz_audit_redact.go:57:7: undefined: model
```

**Fix.** The plan's import block lists only `slices`, `authz` and `engine`; the signature uses
`*model.ProcessDefinition`. Add `"github.com/kartaladev/wrkflw/definition/model"`. (Adding it made
the file build: `BUILD_OK`.)

---

## F8 — MAJOR — `RedactState`'s "copy the struct by value" approach does not protect map- or nested-slice-valued fields, and the plan asserts it does

**CONFIRMED BY EXECUTION** (F4's `out.ArchivedCompensations[sub][0].Input` still populated after a
value copy; the map header is shared with the caller).

**Claim attacked.** Plan Task 5 step 3: *"Copy the struct by value … then overwrite only the
exported fields that need it. Copying by value shares the underlying maps and slices, so any field
whose *elements* are mutated must be cloned first."* The rule is stated correctly and then applied
to only two of the fields that need it (`Tokens`, `Tasks`).

**Consequence once F4/F5 are fixed.** `ArchivedCompensations` is a `map[string][]CompensationRecord`.
`slices.Clone` is not enough and neither is `maps.Clone` alone — the values are slices whose
elements carry the `Input` map. Clearing `Input` needs `maps.Clone` **plus** a per-key
`slices.Clone` **plus** a per-element field write. `Scopes[].Compensations` needs the same two-level
clone. Writing this without the two-level clone would corrupt what `pi.State()` returns for an
in-process consumer — the exact invariant `TestRedactState_DoesNotMutateItsInput` exists to protect,
and that test's fixture (a claimed task only) cannot detect it.

**Fix.** Spell out the two-level clone in Task 5's code, and extend
`TestRedactState_DoesNotMutateItsInput`'s fixture to populate `ArchivedCompensations`,
`Scopes[].Compensations`, `RootCompensations` and `Tasks[].Vars` — otherwise the no-mutation test
is as vacuous as T3b.

---

## F9 — MAJOR — Decision 4b's "only Privileges" narrowing leaves the same fail-open wide open for `Privileges` combined with anything

**CONFIRMED BY EXECUTION.**

**Claim attacked.** ADR Decision 4b / spec D4b: *"A resolved spec that constrains **only**
`Privileges` (no `Roles`, no `Attribute`) … fails closed with an error"*, with the residual stated
as *"a third-party authorizer that silently ignores `Privileges` is outside what this check can
see."*

**Probe.** `authz/zzz_audit_probe_test.go`, `authz.RoleAuthorizer{}`.

**Observed (verbatim):**
```
PROBE §2.2 r1 empty spec                             -> ALLOW
PROBE §2.2 r2 deny-list attribute over absent vars   -> ALLOW
PROBE §2.2 r3 privileges-only                        -> ALLOW
PROBE privileges PLUS roles (D4b does not fire)      -> ALLOW
PROBE privileges PLUS attribute (D4b does not fire)  -> ALLOW
PROBE empty non-nil Roles slice                      -> ALLOW
PROBE attribute negation over absent actor attribute -> ALLOW
PROBE attribute over absent vars, positive form      -> deny: workflow-authz: not authorized
PROBE roles present, actor lacks                     -> deny: workflow-authz: not authorized
PROBE attribute is a bare truthy literal             -> ALLOW
PROBE AllowAll with Roles:[manager], actor nobody    -> <nil>
```
Row 1–3: **all three §2.2 premises reproduce exactly as the spec states them.** That part of §2.2
is sound.

Rows 4–5 are new. `AuthzSpec{Roles:["employee"], Privileges:["admin do"]}` with an actor holding
only `employee` **ALLOWS** — the privilege is silently dropped by `RoleAuthorizer` (`authz/authz.go:124-146`
never reads `spec.Privileges`), and D4b does not fire because `Roles` is non-empty. The natural
authoring of "an employee who also holds the admin privilege" therefore degrades to "any employee",
under the **built-in** authorizer, not a third-party one.

**Fix.** Widen 4b: refuse **any** non-empty `Privileges` under a known-non-privilege authorizer,
not only a `Privileges`-only spec. There is no case where passing a `Privileges` entry to
`RoleAuthorizer`/`AllowAll` is meaningful, so nothing is lost. Correct the residual sentence in
Decision 4b and spec D4b, which currently mis-describes the gap as third-party-only.

---

## F10 — MAJOR — Decisions 4b and 5 INTERACT to make every admin operation role-only, contradicting the project's ABAC mandate

**CONFIRMED BY EXECUTION** (the two constraints are established by F9's probe and by reading D5).

**Claim attacked.** ADR Decision 5: *"For operations with no instance at all (`StartInstance` and
all 12 admin operations), a spec carrying a non-empty `Attribute` is a **configuration error**"*;
Decision 4b refuses `Privileges`. `AuthzSpec` has exactly three dimensions — `Roles`, `Privileges`,
`Attribute`.

**Consequence.** For the 12 admin operations plus `StartInstance`, D5 forbids `Attribute` and D4b
forbids `Privileges` (widened per F9, or already for the `Privileges`-only form), leaving **`Roles`
as the only expressible policy dimension**. CLAUDE.md's Architecture section requires authorization
that supports *"role-based, resource-privilege-based, **and attribute-based**"* rules; the admin
surface this record introduces supports one of the three. Note that an *actor*-attribute predicate
— `actor.Attributes["clearance"] == "secret"`, the natural admin gate, and one that needs **no**
instance at all — is refused by D5 purely because D5's rule keys on `Attribute` being non-empty
rather than on whether the predicate references `vars`.

**Fix.** Narrow D5's refusal to predicates that actually reference `vars`. The repo already has the
machinery: `expr` can be compiled and its identifiers inspected, and `internal/expreval` is already
an `authz` dependency. Failing that, state the limitation explicitly in the ADR's *What this record
does not close* — right now it is an unstated interaction between two decisions written apart.

---
## F11 — MAJOR — the purity guard's prescribed ablation CANNOT produce a RED; it produces an import cycle

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Plan Task 1 step 5: *"Ablate it: add `_ "github.com/kartaladev/wrkflw/engine"`
to `authz/redaction.go`, run `go test -count=1 ./authz/...`, **observe RED**"*; ADR Decision 2 and
spec D2 both rest the guard's falsifiability on that ablation (spec T14: *"its falsifiability is
established by ablation: add a forbidden import and confirm RED"*).

**Probe.** Transcribed the plan's `authz/purity_test.go` verbatim (renamed `TestAUDITAuthzPurity`),
ran a control, then the prescribed ablation, then a non-cyclic ablation, restoring from a `cp`
backup each time (`diff` reported `RESTORED_EXACT`).

**Observed (verbatim):**
```
--- CONTROL (unablated) ---
EXIT=0
--- PASS: TestAUDITAuthzPurity (0.02s)

--- ABLATION A: the plan's prescribed one (import engine) ---
EXIT=1
# github.com/kartaladev/wrkflw/authz
package github.com/kartaladev/wrkflw/authz
	imports github.com/kartaladev/wrkflw/engine from zzz_audit_redaction.go
	imports github.com/kartaladev/wrkflw/authz from command.go: import cycle not allowed
FAIL	github.com/kartaladev/wrkflw/authz [setup failed]

--- ABLATION B: a NON-CYCLIC forbidden import (definition/model) ---
EXIT=1
    zzz_audit_purity_test.go:25: authz must not import "github.com/kartaladev/wrkflw/definition/model" — only "…/internal/expreval" is permitted
--- FAIL: TestAUDITAuthzPurity (0.02s)
```

**Why it matters.** `engine` imports `authz` (the plan's own Import-graph section says so, three
lines above), so importing `engine` from `authz` is refused by the **compiler**, not by the guard.
`[setup failed]` means the test never ran. This repo's standing lesson — *"a mutation that fails to
compile is not a RED"* — is being violated by the plan's own prescription, and the resulting
transcript would show `EXIT=1` and be mistaken for a passing ablation.

**Fix.** Change the prescribed ablation to a **non-cyclic** in-repo import, e.g.
`_ "github.com/kartaladev/wrkflw/definition/model"` (executed above; produces a legible per-import
failure). Also state in the plan that a `[setup failed]` line is **not** an acceptable RED.

**Second, smaller defect in the same test.** The plan says the guard *"mirrors
`engine/purity_test.go`"*. It does not: `engine/purity_test.go` parses the package's files with
`go/parser` in-process; the plan's version shells out to `go list` via `exec.Command(...).Output()`,
which (a) requires a Go toolchain on `PATH` at test time, (b) discards stderr, so the `t.Fatalf("go
list: %v", err)` path reports only `exit status 1` with no diagnostic, and (c) depends on the test
binary's working directory being the package source dir. Prefer the AST form the repo already uses,
or at minimum capture `CombinedOutput`.

---

## F12 — CONFIRMED, NOT A DEFECT — no in-repo type outside `service/instance.go` implements `service.ProcessInstance`

**CONFIRMED BY EXECUTION.** Plan Task 4's claim.

**Probe.** Added `AUDITProbeMethod() int` to the `ProcessInstance` interface, implemented it **only**
on `processInstance`, then ran `go vet ./...` — which type-checks Docker-only test packages too, so
it sees every in-repo implementer including test doubles.

**Observed:** `EXIT=0`, no output. (The intermediate run, before implementing the method, produced
exactly one error, naming only `processInstance`.) Restored from a `cp` backup; `diff` reported
`RESTORED_EXACT`.

Relatedly: `newInstanceJSON` has exactly **one** call site (`service/instance.go:99`). Plan Task 3's
*"`newProcessInstance` and every call site must pass the set"* over-states the blast radius — it is
one line — but nothing depends on that, so it is informational.

---

## F13 — MAJOR — `NewProcessInstance` opting OUT of redaction is a fail-OPEN path through a public constructor, reachable from the HTTP transport

**CONFIRMED BY EXECUTION** (the reachability; the redaction behaviour is prospective).

**Claim attacked.** Plan Task 4: *"`NewProcessInstance` (the public fabricator) passes
`authz.NewRedactionSet()` — **nothing redacted** — because a consumer fabricating an instance
in-process is the trusted application"*, and *"⚠ Note the deliberate asymmetry: `NewProcessInstance`
fabricates a *known* type that opts out; `RedactionOf` fails closed for an *unknown* type. Both are
safe."*

**Probe.** `grep -rn "NewProcessInstance(" --include='*.go' .` over the worktree.

**Observed (the load-bearing hit):**
```
transport/http/gin/gin_bodycap_test.go:228:
    return service.NewProcessInstance(nil, engine.InstanceState{InstanceID: "inst-1"}), nil
```
That is a consumer-shaped `service.Service` implementation returning a `NewProcessInstance`, served
**through the gin transport**. `service.Service` and `service.ProcessInstance` are both public, so
this is a supported consumer wiring, not a test-only artefact.

**Consequence.** Any consumer who implements `service.Service` themselves — or wraps ours and
re-fabricates — and returns `NewProcessInstance` gets **zero redaction** on all three (four, per F1)
render paths, silently. "Both are safe" is false: the *known* type is the fail-open one and it is
the one reachable over HTTP; the fail-closed branch protects the case that, per F12, does not exist
in-repo.

**Fix.** Either make `NewProcessInstance` default to the closed posture and add an explicit
`NewProcessInstanceUnredacted` / functional option for the trusted in-process case, or state this
bypass in the ADR's *What this record does not close* and in `SECURITY.md` (Task 8). Note the
in-process argument does not apply: `pi.State()` and `pi.Definition()` already give the trusted
caller full fidelity regardless of the marshalling policy, so defaulting the *marshalled* document
closed costs the in-process consumer nothing.

---

## F14 — MAJOR — Task 7 (the T12 guard) is prose with no code, while the plan's self-review asserts the placeholder scan is clean

**CONFIRMED BY READING** (documentary; not executable).

**Claim attacked.** Plan *Self-review notes*: *"**Placeholder scan.** Clean — no "TBD", no "handle
edge cases", no "similar to Task N". Task 5's body was described rather than written in the first
draft; that is a plan failure by this skill's own rules"*.

**Observed.** Task 7 step 1 is one paragraph of description — *"It enumerates every exported
function in `httpcore` returning a rendered instance body and asserts each is redaction-aware…"* —
with **no code block**, no stated mechanism (reflection cannot decide "redaction-aware"; an AST scan
is implied but never named), and no RED step. This is the same failure the self-review diagnoses one
paragraph later for Task 5 — and Task 5's write-out is what surfaced the `StartVariables` site, so
the plan's own evidence says writing Task 7 out would surface more.

**Why it matters here specifically.** T12 is the invariant D6 substitutes for the *rejected*
structural option (redact-at-source). Per F1 and F2 the enumeration it is meant to freeze is already
wrong by four entries, so a hand-written literal list inside the guard would freeze the wrong set
and pass.

**Fix.** Write Task 7's code out before implementation starts, and derive the enumeration from the
package (AST scan of `httpcore` for functions whose returned expression reaches `pi.State()`,
`NewInstanceView`, `mapInstance` or a `service.ProcessInstance`), with the exemption list asserted
non-stale in both directions.

---

## F15 — MINOR — plan Task 2's ⚠ box cites `ResolveConfig`, which does not exist in `service`, and then prescribes the very post-loop guard it forbids

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Plan Task 2: *"⚠ **Placement matters and has bitten this repo twice.** The
default must live in `ResolveConfig`'s **struct literal**, not a post-loop nil-guard, for the same
reason `MaxBodyBytes` and `BodyReadTimeout` do"* — in a task whose Files are `service/options.go`
and `service/service.go`.

**Probe / observed:**
```
$ grep -rn "func ResolveConfig|MaxBodyBytes:|BodyReadTimeout:" --include='*.go' . | grep -v _test
transport/http/httpcore/seam.go:159:func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R] {
transport/http/httpcore/seam.go:168:		MaxBodyBytes:        defaultMaxBodyBytes,
transport/http/httpcore/seam.go:169:		BodyReadTimeout:     defaultBodyReadTimeout,
```
`ResolveConfig` lives in `transport/http/httpcore`, not `service`. `NewProcessEngine`
(`service/service.go:167`) builds `c := &engineConfig{}` and applies **post-loop nil guards** — the
`AllowAll` default at line 200 is one. Task 2's own Step 3 then prescribes
`if !c.redactionSet { c.redaction = … }` — a post-loop guard, i.e. the opposite of what its warning
says.

**Fix.** Drop the `ResolveConfig` sentence or rewrite it to say what it actually means: *use an
explicit `redactionSet bool` rather than a nil-map test, because a post-loop nil guard cannot
distinguish "never called" from `WithRedaction()`.* The `redactionSet bool` design is correct; only
the justification's citation is wrong. Both premises (`service/service.go:200` and `:316`) were
re-derived and are **exact**.

---
## F16 — MINOR — D6's wire-shape claim has TWO exceptions, not one: `Completion.Actor` is also non-`omitempty`

**CONFIRMED BY EXECUTION** (source read + the marshalled output in F4).

**Claim attacked.** Spec D6 *Wire shape*: *"All four categories sit on `omitempty` fields **except**
`Claim.Actor` (`json:"actor"`, no `omitempty`), so a redacted claimed task renders
`"actor":{"id":""}` rather than dropping the key."*

**Observed.** `humantask/humantask.go:59-78`:
```go
type Claim struct {
	Actor authz.Actor `json:"actor"`
	At    time.Time   `json:"timestamp"`
}
type Completion struct {
	Actor   authz.Actor `json:"actor"`
	At      time.Time   `json:"timestamp"`
	Outcome string      `json:"outcome,omitempty"`
	Note    string      `json:"note,omitempty"`
}
```
`Completion.Actor` carries `json:"actor"` with no `omitempty` exactly as `Claim.Actor` does, and the
F4 probe's marshalled state shows the resulting shape: `"Claim":{"actor":{"id":""},"timestamp":…}`.

**Consequence.** The ADR-0152 discriminator argument is stated for `Claim` only; `Completion` gets
the same behaviour by accident. That is almost certainly the right behaviour — but it is unstated,
so a later implementer "tidying" one of the two has no record of why the key survives.

**Fix.** One sentence in D6: `Completion.Actor` is the second non-`omitempty` site and keeps its key
for the same reason.

---

## F17 — CONFIRMED, ASSUMPTION RESOLVED — the gin/fiber parity assumption (§2.6) is cheaply verifiable and holds at the delegation level

**CONFIRMED BY EXECUTION.** Spec §2.6 marks as `ASSUMPTION (unverified)`: *"the gin and fiber
adapters expose the same five route groups with the same handler semantics as stdlib."*

**Probe.** `grep -oE "httpcore\.[A-Z][A-Za-z]*\(" transport/http/{stdlib,gin,fiber}/groups.go | sort | uniq -c`

**Observed.** All three adapters call an **identical multiset** of 29 `httpcore.*` entry points —
same functions, same multiplicities, including `httpcore.ResolveConfig` ×5, `NewInstrumentation` ×5,
`RequestActor` ×3, and one call each to `StartInstance`, `GetInstance`, `GetInstanceSnapshot`,
`GetActionableView`, `DeliverSignal`, `ResolveIncident`, `CancelInstance`,
`ResolveCompensationStall`, `AdminListInstances` and the rest. Every body-shaping decision lives in
`httpcore`, so a redaction implemented there covers all three adapters.

**Consequence for the bundle.** Two, in opposite directions:
- Good: spec §2.6's assumption can be promoted from `ASSUMPTION (unverified)` to a measured premise,
  and T13 (the gin/fiber parity suite) becomes a regression pin rather than a discovery exercise.
- Bad: it **generalises F1 and F2 to all three transports**. `DeliverSignal` and the three admin
  instance-ops disclose identically under gin and fiber.

---

## F18 — INFORMATIONAL — the tree at the bundle commit is not green; two packages fail before any change

**CONFIRMED BY EXECUTION.** `go test -count=1 ./...` at `98382afd` with Docker up:
```
BASELINE_EXIT=1
59 packages ok
FAIL	github.com/kartaladev/wrkflw/internal/database	101.031s
FAIL	github.com/kartaladev/wrkflw/internal/dbtest	75.882s
--- FAIL: TestSQLQuerierRoundTrip (68.50s)   --- FAIL: TestQuerierTransparentPoolVsTx_MySQL
--- FAIL: TestProbeUTCPassesOnMySQL          --- FAIL: TestBeginTxCommitRollback  (…and others)
```
These are MySQL/testcontainers failures unrelated to the bundle (neither package is touched by any
task). Recording them so the implementer does not mistake them for a regression, and so the plan's
verification checklist item *"`go test ./...` from the repo root — no regressions"* is read against
a **known-red baseline** rather than an assumed-green one. Per Common Pitfall #1 they should be
queued as follow-up rather than excused.

**Fix.** Add the known-red baseline to the plan's Phase-1 verification checklist.

---
## F19 — MAJOR — Phase 1 has no configuration in which an authorized HTTP reader sees variables and an unauthorized one does not

**CONFIRMED BY READING the two documents together** (the mechanism is established by F1's and F4's
executed probes).

**Claim attacked.** Spec §10: *"**Phase 1 — the disclosure fix.** … No gate concept: the effective
set is simply the configured one. Ships the live security fix without waiting for the gate."*
ADR Decision 8: *"Each is independently shippable and verifiable."*

**Mechanism.** D6's effective policy has two arms — *not gated ⇒ configured set*, *gated and passed
⇒ nothing*. Phase 1 implements only the first, because it has no gate. So in Phase 1 the redaction
set is **global and unconditional**. The only two reachable configurations over HTTP are:

| configuration | unauthenticated reader | authenticated reader |
|---|---|---|
| default (all four) | sees no variables ✅ | sees no variables ❌ |
| `WithRedaction()` (documented opt-out) | sees everything ❌ | sees everything ✅ |

There is no third. A deployment that actually needs process variables on `GET /instances/{id}` — the
common case, and the reason the endpoint renders them — must take the second row, which is exactly
today's disclosure. Phase 1 therefore ships a fix that a large class of real deployments must
immediately disable.

**Fix.** Pull the "gated ⇒ redact nothing" arm forward far enough to be usable in Phase 1. The
cheapest version needs no `OperationPolicy` at all: **if the request carries an authenticated actor
(`authz.ActorFromContext`, already built by ADR-0189), redact nothing.** That is one condition, it
reuses machinery that exists, it makes the default posture deployable, and it simultaneously fixes
F1b's regression on the three human-task verbs. If the owner prefers to keep Phase 1 gate-free,
the ADR must say plainly that Phase 1's default is unusable for consumers who need variables over
HTTP, rather than describing it as *"the live security fix"*.

---

## F20 — MINOR — spec §2.1 and §5 still say "both" where the corrected count is three, inside the document whose headline correction is that count

**CONFIRMED BY READING.**

**Observed.**
- §2.1: *"`stdlib.Mount(mux, svc)` with no actor on the context, plain `GET`, **status 200 on
  both**."* — then lists three paths.
- §5 Consequences: *"The disclosure on **both read endpoints** is closed in the default deployment"*
  — while ADR Consequences says *"closed on all three read paths"*.

**Why it is worth a finding rather than a typo report.** This is the repo's recorded
*"verify recap sentences too"* failure, verbatim: the detailed reasoning was corrected and the
summary sentences that compressed it were not, in the one document that makes the two-vs-three
correction its centrepiece. Per F1 the correct number is four (or eleven exported functions), so
these sentences need rewriting rather than a `both`→`three` substitution.

**Fix.** Rewrite both sentences against the corrected enumeration, and re-grep the bundle for
`both`, `two` and `three` after the F1/F2 fixes land — the repo's own lesson is that a corrected
value must be `grep`ed for in its OLD form afterwards.

---

## F21 — MINOR — D2's "Measured" sentence about `go list -deps ./authz` is imprecise

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Spec D2 and ADR Decision 2: *"**Measured:** `go list -deps ./authz` returns
exactly one in-repo dependency, `internal/expreval`."*

**Observed (verbatim):**
```
$ go list -deps ./authz | grep kartaladev
github.com/kartaladev/wrkflw/internal/expreval
github.com/kartaladev/wrkflw/authz

$ go list -f '{{join .Imports "\n"}}' ./authz
context
errors
fmt
github.com/kartaladev/wrkflw/internal/expreval
maps
slices
```
`-deps` includes the package itself, so it returns **two** in-repo lines. The intended claim — one
in-repo *import* — is true and is what the purity guard actually checks (`.Imports`).

**Fix.** State the command that produces the claimed number:
`go list -f '{{join .Imports "\n"}}' ./authz`.

---

## F22 — MINOR — the bare citation `view.go:31` is ambiguous in a repo with both `runtime/view/` and `transport/http/httpcore/view.go`

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Spec D6 path 3 and T3a, and ADR Decision 6: *"the default mapper
`NewInstanceView` renders `Variables: st.Variables` (`view.go:31`)"*.

**Observed.** `grep -n "st.Variables" transport/http/httpcore/view.go` → `31:		Variables:  st.Variables,`.
The line number is exact — but the file is `transport/http/httpcore/view.go`, while the plan's own
File Structure creates `runtime/view/redact.go` and modifies `runtime/view/instance_actionable.go`.
A subagent handed Task 5 or Task 6 and told to look at `view.go:31` has a coin-flip.

**Fix.** Qualify every citation of this file with its full path. (Both other cited line numbers were
re-derived and are exact: `service/service.go:200` for the `AllowAll` default, `service/service.go:316`
for the type-check idiom, `transport/http/httpcore/errors.go:87` for the 403 arm,
`runtime/task/service.go:199,234,255,306` for the four already-gated task verbs, and
`transport/http/httpcore/admin_endpoints.go:88-96` for `instanceSummaryView`.)

---

## F23 — CONFIRMED, NOT A DEFECT — spec §2.4's operation member set is exact

**CONFIRMED BY EXECUTION.**

**Probe.** `awk`-extracted the method list of each of the ten interfaces named in §2.4.

**Observed:** `InstanceStarter`=`StartInstance`; `InstanceReader`=`GetInstance`,`ListInstances`;
`TaskManager`=`ClaimTask`,`CompleteTask`,`ReassignTask`,`RefreshTaskCandidates`;
`Messaging`=`DeliverSignal`,`DeliverMessage`;
`InstanceOps`=`ResolveIncident`,`ResolveCompensationStall`,`CancelInstance`
⇒ **12 `Service` methods, 4 gated, 8 ungated**, exactly as §2.4 states.
`DeadLetterAdmin`=`ListDeadLettered`,`Redrive`; `LineageAdmin`=`Lineage`;
`RelayStatsAdmin`=`OutboxStats`; `TimerAdmin`=`Stats`,`ListArmedPage`;
`PolicyAdmin`=`AddPolicy`,`RemovePolicy`,`ListPolicies`,`AddRole`,`RemoveRole`,`ListRoles`
⇒ **12 admin operations**. Total **20**, and D2's constant list has exactly 20 entries matching
one-for-one.

This enumeration did **not** rot. Recording it so the adjudication does not spend effort re-deriving
it, and so a later reader can tell a checked count from an unchecked one.

---
## F24 — MAJOR — the §9 assertion-breakage net is wrong in BOTH directions: 6 of its 8 predicted files do not break, and a file it never names does

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Spec §9 item 3: the *"member set — **listed, not counted**, because two different
nets can agree on a total while being disjoint on members"* of tests that will break when the two
read endpoints change shape. Eight files are listed.

**Probe.** Applied the Phase-1 **default posture** as a real ablation to the three renderers —
`newInstanceJSON` (`Variables=nil`, `Tokens[].Payload=nil`, `Candidates=nil`, `Claim.Actor`/
`Completion.Actor` zeroed, `Completion.Note=""`, `Definition=nil`), `httpcore.NewInstanceView`
(`Variables=nil`), `view.NewActionableView` (`Candidates=nil`, `Claim.Actor` zeroed,
`Condition=""`) — then `go test -count=1` over every package except the two known-red ones (F18).
Files restored from a `cp` backup; `git status --porcelain` reports the worktree clean.

**Observed:** `EXIT=1`, **18 failing test functions/subtests in exactly 3 packages**:
```
FAIL	github.com/kartaladev/wrkflw/runtime/view	1.433s
FAIL	github.com/kartaladev/wrkflw/service	1.313s
FAIL	github.com/kartaladev/wrkflw/transport/http/stdlib	2.107s
```
Resolved to files:

| file | predicted by §9? | broke? |
|---|---|---|
| `runtime/view/instance_actionable_test.go` (`TestActionableViewDoesNotAliasInstanceState`) | yes | **yes** |
| `service/instance_test.go` (7 test funcs incl. `TestProcessInstanceMarshalTasks`, `…MatchesSampleDocument`, `…DefinitionEmbedPolicy`) | yes | **yes** |
| **`transport/http/stdlib/maxbody_test.go`** (`TestUnderCapBehaviourIsUnchanged`) | **no** | **yes** |
| `transport/http/stdlib/stdlib_test.go` | yes | no |
| `transport/http/stdlib/errors_test.go` | yes | no |
| `transport/http/gin/gin_test.go` | yes | no |
| `transport/http/gin/gin_coverage_test.go` | yes | no |
| `transport/http/fiber/fiber_test.go` | yes | no |
| `transport/http/httpcore/observability_test.go` | yes | no |

**The miss that matters.** `transport/http/stdlib/maxbody_test.go` is an **ADR-0186 body-cap** test.
Its `wantTodaysSuccess` helper asserts on the *instance document* as a side effect of asserting the
cap behaves:
```
maxbody_test.go:152:  vars, ok := resp["variables"].(map[string]any)
                      require.True(t, ok, "variables missing: %v", resp)
Messages: variables missing: map[def_id:greeting def_version:1 ended_at:… instance_id:… started_at:… status:completed]
```
§9's net was built from filenames matching `snapshot|actionable|Snapshot|Actionable` and narrowed by
hand; a body-cap test that happens to assert on `variables` is invisible to it. Its two sibling
tests of the same name (`transport/http/fiber/bodylimit_test.go`,
`transport/http/gin/gin_bodycap_test.go`) do **not** assert on `variables` and did not break — so
name-matching across adapters would also have misled.

**Caveat, stated rather than hidden.** My ablation is close to but not identical to the planned
change: I redacted inside `NewInstanceView` rather than feeding `mapInstance` redacted state, so a
test using a **custom** `InstanceMapper` would break under the real change and not under mine. The
member set above is therefore a **lower bound** on breakage, which makes the over-prediction finding
stronger and the under-prediction finding weaker.

**Fix.** Replace §9 item 3's hand-narrowed list with the measured one, and record the ablation
recipe in the plan so Phase 2 re-derives rather than inherits it (spec §10 already demands that).
Note that `transport/http/{gin,fiber}` and `transport/http/httpcore` need **no** test changes for
this ablation — which is worth knowing before an implementer starts "fixing" green tests.

---

## F25 — CRITICAL — three of the prescribed redaction assertions are VACUOUS against the standard test harness: the §2.1 lesson was applied to one field and not to its three siblings

**CONFIRMED BY EXECUTION.**

**Claim attacked.** Spec §7: T1 *"unauthenticated `GET /snapshot` renders no `variables`, no
`claim.actor.attributes`, no `candidates`, no `definition`"*; T2 *"unauthenticated `GET /actionable`
renders no `claim.actor.attributes`, no `candidates`, no `allowed_actions[].condition`"*; plan
Task 6's test asserting `!strings.Contains(body, "alice@corp.example")`. And the spec's own
standing warning: *"the redaction guard MUST use a fixture in which `tokens[].payload` is populated"*
— the *"guard tested with a fixture from the half that works"* lesson.

**Probe.** `stdlib.Mount` over the standard `internal/transporttest` harness with
`ApprovalProcess()`, then `GET /snapshot` and `GET /actionable`, testing for each literal the
prescribed assertions rely on.

**Observed (verbatim):**
```
PROBE snapshot body contains "alice@corp.example"? false
PROBE snapshot body contains "alice"?              true
PROBE snapshot body contains "attributes"?         false
PROBE snapshot body contains "condition"?          false
PROBE actionable contains "condition"?             false
  {"instance_id":"inst-1","status":"running","open_tasks":[{"task_id":"…","node_id":"approve",
   "state":"unclaimed","candidates":[{"id":"alice","roles":["manager"]}],
   "allowed_actions":[{"flow_id":"f2","target":"end"}]}]}
```

**Three vacuous assertions, all in the same class as the one the spec caught:**
1. `internal/transporttest`'s resolver is
   `map[string][]authz.Actor{"manager": {{ID: "alice", Roles: []string{"manager"}}}}`
   — the actor id is **`alice`**, so `strings.Contains(body, "alice@corp.example")` is false before
   *and* after the fix. Plan Task 6's prescribed test cannot fail on the actor arm.
2. That actor carries **no `Attributes`**, so T1's and T2's *"no `claim.actor.attributes`"* arms
   cannot fail.
3. `ApprovalProcess()`'s flow `f2` carries **no `Condition`**, so T2's
   *"no `allowed_actions[].condition`"* arm cannot fail — and the spec's own §2.1 output proves it:
   `"allowed_actions":[{"flow_id":"f2","target":"end"}]`, no `condition` key.

The spec's §2.1 probe bodies show `alice@corp.example`, `attributes:{clearance,department}` and a
condition — so those probes used a **hand-built fixture**, not the harness. The plan then prescribes
tests without saying that the harness must be extended, and Task 6's helper line is elided as
`// … seed an instance with ssn in variables and a claimed task …`.

**Fix.** Make it an explicit, checkable requirement in Task 6 (and in spec §7 alongside the
`tokens[].payload` note) that the fixture must carry, at minimum: populated `variables`, populated
`tokens[].payload`, a **claimed** task whose actor has a distinctive id **and** non-empty
`Attributes`, a completion with a `Note`, a flow with a non-empty `Condition`, and — per F4/F5 —
`Tasks[].Vars`, `Tasks[].Eligibility` and at least one `CompensationRecord.Input`. Either extend
`internal/transporttest` with such a definition/resolver or build the fixture in-test, but say
which. A prescribed test whose fixture cannot exhibit the defect is the failure mode §7 exists to
prevent, and it recurs here three times.

---

## Probes run and deleted

All probe files were created in the audit worktree, executed, and removed;
`git status --porcelain` is empty and `go build ./...` is `BUILD_OK`.

| probe file | what it established |
|---|---|
| `transport/http/stdlib/zzz_audit_probe_test.go` | F1 — the four unauthenticated disclosure paths |
| `transport/http/stdlib/zzz_audit_probe2_test.go` | F2, F3 — admin instance-ops disclose; `AdminListInstances` does not |
| `transport/http/stdlib/zzz_audit_probe3_test.go` | F4, F5 — `Tasks[].Vars` and `Tasks[].Eligibility` in a live run |
| `transport/http/stdlib/zzz_audit_probe4_test.go` | F25 — the harness's actual claim/attribute/condition literals |
| `authz/zzz_audit_probe_test.go` | F9 — the fail-open matrix, including the two shapes D4b misses |
| `authz/zzz_audit_redaction.go`, `authz/zzz_audit_purity_test.go` | F11 — the purity guard's ablation is an import cycle |
| `runtime/view/zzz_audit_redact.go`, `…_test.go` | F4, F7, F8 — the plan's `RedactState` verbatim: does not compile, then leaks four sites |
| interface ablation on `service/instance.go` | F12 — no external `ProcessInstance` implementer |
| default-posture ablation on the three renderers | F24 — the measured breakage member set |

**Baseline for every run:** `98382afd`, Docker up, `go test -count=1 ./...` = 59 ok / 2 pre-existing
FAIL (`internal/database`, `internal/dbtest` — MySQL, unrelated).
