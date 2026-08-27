# ADR-0190 bundle — adversarial audit, RE-COUNTING lens

**Date:** 2026-08-26
**Lens:** re-derive every enumeration, quantifier, explicit count and inherited citation.
**Worktree:** detached at `98382afd` (the bundle commit).
**Bundle audited:**
- `docs/specs/2026-08-26-route-group-authorization-posture-design.md` (spec)
- `docs/adr/0190-authorization-is-gated-by-policy-not-by-authentication.md` (ADR)
- `docs/plans/2026-08-26-route-group-authorization-posture.md` (plan)

Findings are appended as they are confirmed. Every finding pastes the MEMBER SET, not a total.

---

## Enumerations that RE-DERIVED CLEAN (recorded so the next reader need not redo them)

- **`Service` = 12 methods across 5 sub-interfaces** (`service/service.go:22,30,41,61,75,115`).
  Member set: `InstanceStarter{StartInstance}`, `InstanceReader{GetInstance, ListInstances}`,
  `TaskManager{ClaimTask, CompleteTask, ReassignTask, RefreshTaskCandidates}`,
  `Messaging{DeliverSignal, DeliverMessage}`,
  `InstanceOps{ResolveIncident, ResolveCompensationStall, CancelInstance}`. 1+2+4+2+3 = 12. ✓
- **8 ungated `Service` operations** — the 12 minus the 4 `TaskManager` verbs. ✓
- **`Authorize` at exactly 4 sites in `runtime/task/service.go`: 199, 234, 255, 306.** All four
  line numbers are exact, and they map 1:1 onto the only four exported `TaskService` methods
  (`Claim`:194, `Reassign`:219, `Complete`:250, `RefreshCandidates`:294), so "the 4 `TaskManager`
  verbs are already gated" holds. ⚠ Minor caveat: a 5th `.Authorize(` call exists repo-wide at
  `casbinauthz/casbinauthz.go:163`, but it is a decorator delegating inward, not a gate site.
- **12 admin operations across 5 consumer-supplied interfaces.** Member set re-derived:
  `DeadLetterAdmin{ListDeadLettered, Redrive}` (`service/deadletter.go:20`),
  `LineageAdmin{Lineage}` (`service/lineage.go:15`),
  `RelayStatsAdmin{OutboxStats}` (`service/opsadmin.go:18`),
  `TimerAdmin{Stats, ListArmedPage}` (`service/opsadmin.go:25`),
  `PolicyAdmin{AddPolicy, RemovePolicy, ListPolicies, AddRole, RemoveRole, ListRoles}`
  (`service/policyadmin.go:26`). 2+1+1+2+6 = 12. ✓ No sixth `*Admin` interface exists
  (`grep -rn "Admin interface" --include='*.go' .` returns only these five plus their mockgen
  doubles). The `AdminRoutes` struct's optional dep fields are exactly these five
  (`transport/http/stdlib/groups.go:216-224`). ✓
- **5 route groups in stdlib**: `InstanceRoutes`(27), `MessageRoutes`(98), `TaskRoutes`(124),
  `AdminRoutes`(216), `HealthRoutes`(484) in `transport/http/stdlib/groups.go`. ✓

---

## Findings

### C1 — CRITICAL — D5's "operations with no instance at all" enumerates 13 members; the real set is 15, and one of its named members does not belong

**Quantifier attacked:** ADR-0190 Decision 5 and spec §D5, identical wording in both:
> "For operations with no instance at all (`OpStartInstance` and **all 12 admin operations**),
> a spec carrying a non-empty `Attribute` is a **configuration error**, never evaluated over
> nil `vars`."

That sentence is the ONLY thing standing between this feature and a re-run of backlog 103
(a deny-list ABAC predicate evaluated over absent process variables returns ALLOW — measured
`nil`/ALLOW in spec §2.2 row 2). Any gated operation that has no instance and is NOT in this
enumeration falls through to the instance-scoped branch, whose two arms are "no `Attribute` →
pre-load" and "`Attribute` over `vars` → **load the instance first**". There is no instance to
load, so `vars` is absent and the predicate fail-opens — the exact defect D5 says "this record
must not reproduce it on a new surface."

**Commands run:**
```
grep -n -A14 "type DeliverMessageRequest struct" service/request.go
grep -n -A10 "type DeliverSignalRequest struct"  service/request.go
grep -n -A12 "type StartInstanceRequest struct"  service/request.go
sed -n '30,40p' service/service.go        # ListInstances signature
grep -n -A6 "type LineageAdmin interface" service/lineage.go
```

**Actual member set — the 20 gated operations partitioned by whether an instance ID exists at gate time:**

| operation | instance ID at gate time? | evidence |
|---|---|---|
| `OpStartInstance` | **NO** | `StartInstanceRequest{DefRef, Vars}` — `service/request.go:14-20`, no ID field |
| `OpGetInstance` | yes | `GetInstance(ctx, instanceID string)` — `service/service.go:33` |
| `OpListInstances` | **NO** ⛔ **MISSING FROM D5** | `ListInstances(ctx, filter kernel.InstanceFilter)` — `service/service.go:37`; a filter, not an ID |
| `OpDeliverSignal` | yes | `DeliverSignalRequest.InstanceID` — `service/request.go:25` |
| `OpDeliverMessage` | **NO** ⛔ **MISSING FROM D5** | `DeliverMessageRequest{Name, CorrelationKey, Payload}` — `service/request.go:38-45`. **No `InstanceID` field.** Its own godoc: *"the driver's internal message-waiter table routes the message"* — and per `Messaging`'s doc it may **start a new instance** when none is waiting (ADR-0121), so no instance exists even in principle |
| `OpResolveIncident` | yes | `ResolveIncidentRequest` |
| `OpResolveCompensationStall` | yes | `ResolveCompensationStallRequest` |
| `OpCancelInstance` | yes | `CancelInstanceRequest` |
| `OpAdminLineage` | **YES** ⛔ **WRONGLY INCLUDED BY D5** | `Lineage(ctx context.Context, instanceID string)` — `service/lineage.go:19`. Route `GET /admin/instances/{id}/lineage` — `transport/http/stdlib/groups.go`. It is instance-scoped |
| the other 11 admin ops | no | correct as stated |

⇒ The true "no instance at all" set has **15** members (`OpStartInstance`, `OpListInstances`,
`OpDeliverMessage`, + 11 admin ops other than `OpAdminLineage`), not the 13 D5 names, and D5's
13 includes one member (`OpAdminLineage`) that does not belong.

**Why "all 12 admin operations" is the load-bearing error, not a nitpick:** the phrase is a
*quantifier over a set the author did not re-derive*. `OpAdminLineage` being instance-scoped
means D5 as written would REFUSE a perfectly legitimate `Attribute` spec on lineage (a
false negative), while `OpListInstances` and `OpDeliverMessage` being omitted means D5 would
ADMIT an `Attribute` spec on two operations that have no vars to evaluate it against (a
fail-open — the harmful direction). This is the ADR-0165 shape exactly: a predicate that
refuses the useful case and admits the harmful one.

**Concrete fix.** Stop enumerating by prose and derive the partition from the `Subject`:
1. Restate D5's third bullet as: *"An operation whose `Subject.InstanceID` is empty at gate
   time cannot carry an `Attribute`; such a spec is a configuration error."* This is
   self-maintaining — it is a property of the value, not a list that rots.
2. Add the explicit member set above to the spec as a table, so the reader can check it.
3. Add a machine-checked guard (a sibling of T12) asserting that every `Operation` constant is
   classified into exactly one of {instance-scoped, not-instance-scoped} and that the
   classification matches whether the gate call site can supply a non-empty `Subject.InstanceID`.
   Without this the enumeration rots again at the next added operation.
4. Correct D5 in BOTH documents — ADR Decision 5 and spec §D5 carry the identical false
   sentence, so fixing one leaves the other wrong.

### C2 — CRITICAL — "There are three render paths, not two" is ALSO wrong. Path 3 has SIX entry points; the bundle wires TWO. The unauthenticated disclosure stays open on `POST /instances/{id}/signals`.

**Count attacked.** The bundle makes this its flagship self-correction and repeats it in all
three documents plus the guard rationale:
- ADR-0190 D6: *"**There are three render paths, not two** — the enumeration was wrong once
  during design and is therefore machine-checked by a guard test"*
- spec §D6: *"**Reaching the renderers — there are THREE, not two.** An earlier draft of this
  design said two, and that was wrong; the third was found by checking rather than by counting
  again."*
- spec §7 T12: *"⚠ This guard exists *because* the render-path enumeration was already wrong
  once during design: the spec said two, and there are three"*
- plan Task 7: *"the spec said two paths and there are three"*

The correction went 2 → 3 and stopped one step short. The bundle counted **render SHAPES**
(`instanceJSON`, `ActionableView`, `mapInstance`) and then wrote the fix against **render ENTRY
POINTS**, which is a different set.

**Command run:**
```
grep -rn "mapInstance(" --include='*.go' . | grep -v _test
```

**Actual member set — every `mapInstance` call site (path 3's real membership):**

| # | `transport/http/httpcore/endpoints.go` | enclosing endpoint | route | in bundle? |
|---|---|---|---|---|
| 1 | `:42` | `StartInstance` | `POST /instances` | ✅ named (`endpoints.go:42`) |
| 2 | `:52` | `GetInstance` | `GET /instances/{id}` | ✅ named (`endpoints.go:52`) |
| 3 | `:94` | `DeliverSignal` | `POST /instances/{id}/signals` | ⛔ **MISSED** |
| 4 | `:133` | `ClaimTask` | `POST /tasks/{token}/claim` | ⛔ **MISSED** |
| 5 | `:158` | `CompleteTask` | `POST /tasks/{token}/complete` | ⛔ **MISSED** |
| 6 | `:182` | `ReassignTask` | `POST /tasks/{token}/reassign` | ⛔ **MISSED** |

All six are literally `return http.Status…, mapInstance(mapper, pi.State()), nil` — the same
line, six times. The two cited line numbers (`endpoints.go:42,52`) are **individually exact**;
they are simply 2 of 6.

⇒ Counting entry points, the render surface is **8**: `GetInstanceSnapshot`
(`endpoints.go:60`, path 1), `GetActionableView` (`endpoints.go:72`, path 2), and the six
`mapInstance` sites above (path 3). Not three.

**Why this is Critical and not bookkeeping — the disclosure Phase 1 exists to close stays open.**
`DeliverSignal` (site 3) is mounted on `InstanceRoutes` at `POST /instances/{id}/signals`. The
bundle itself states, in three places, that this route is unauthenticated and open — spec §1:
*"`POST /instances`, `/signals`, `/messages` are state-changing and open"*, and D1 keeps it
that way deliberately. So after Phase 1 ships as planned, an unauthenticated caller who is
refused `variables` on `GET /instances/{id}` obtains the identical unredacted document by
sending `POST /instances/{id}/signals` instead. **The fix is bypassed by changing the verb.**

Sites 4–6 (the task verbs) are less severe — `resolveRequestActor`
(`transport/http/httpcore/resolve_actor.go:178-183`) fails closed with `ErrUnauthenticated`
when no resolver is configured, so those three routes do require an actor — but they are still
render paths that Phase 1 leaves inconsistent: with no gate concept in Phase 1 the effective
redaction set is global, so `GET /instances/{id}` would redact while `POST /tasks/{token}/claim`
returns the same instance in full.

**The guard (T12) does not save this, and would in fact ratify the error.** Plan Task 7 says the
guard "enumerates the three render paths (plus `AdminListInstances` as an asserted-exempt
entry)". A guard seeded from the bundle's own wrong enumeration pins **3** and stays green over
the four unwired sites — the ADR-0187 failure mode recorded in this repo's memory verbatim:
*a guard can be blind to the category of claim it was built to police.* Its prescribed ablation
("add a new unredacted render path and confirm it fails") also cannot detect this, because the
four missed sites are not new — they already exist.

**Concrete fix.**
1. Correct the count in **all four places** listed above. Distinguish *render shapes* (3) from
   *render entry points* (8) explicitly, because the bundle's own text uses one number for both.
2. Rewrite plan **Task 6 step 3** to wire all six `mapInstance` sites, not
   "`StartInstance` … and `GetActionableView`". Simplest correct form: redact inside
   `mapInstance` itself — change its signature to
   `mapInstance(mapper, st engine.InstanceState, red authz.RedactionSet)` and apply
   `view.RedactState` there. One choke point closes all six and cannot be forgotten at a
   seventh call site. This is strictly better than editing six call sites, and the plan's
   File Structure table already lists `endpoints.go` as the only file to modify.
3. Re-derive **T12** against `grep -c "mapInstance(" endpoints.go` rather than the literal 3, so
   the guard fails when a call site is added *or* when one is left unredacted.
4. Add a test at the exact bypass: unauthenticated `POST /instances/{id}/signals` must not
   return `variables`. Nothing in the current test plan (T1, T2, T3, T3a, T3b) touches a
   non-GET render path — T3b exercises the custom mapper, but through `GET /instances/{id}`.

### C3 — CRITICAL — `RedactVariables` has FOUR sites in `engine.InstanceState`, not three, and `RedactPolicy` has a site in state the bundle never lists. The custom-mapper leak D6 claims to close stays open, and the prescribed test T3b cannot detect it.

**Counts and quantifiers attacked** (the bundle emphasises this count harder than any other,
which is what made it worth re-deriving):
- ADR-0190 D6: *"`engine.InstanceState` has **three** — it additionally carries
  `StartVariables` … The discrepancy is deliberate and must not be 'harmonised'."*
- spec §D6: *"**state:** `InstanceState.Variables` + `StartVariables` + `Tokens[].Payload`
  (**3 sites**)"*
- plan Task 5: *"⚠⚠ **`RedactVariables` has THREE sites in `engine.InstanceState`, not two.**"*
- plan Self-review: *"Task 5 redacts **three**… An implementer who 'harmonises' these to one
  number breaks one of them."*
- The load-bearing quantifier this supports, ADR-0190 D6: feeding the mapper redacted state
  *"closes all three paths uniformly and **closes path 3 even for a custom mapper**."*

**Commands run:**
```
sed -n '265,+80p' engine/state.go                      # every InstanceState field
sed -n '<HumanTask>,+55p' humantask/humantask.go       # every HumanTask field
grep -n -A18 "type taskJSON struct" service/instance.go
```

**Actual member set — every field of `engine.InstanceState` (`engine/state.go:265`):**
```
ids idSource                    <- unexported (plan's claim CONFIRMED)
InstanceID, DefID, DefVersion, Status
Variables                 map[string]any        <- variables site 1
StartVariables            map[string]any        <- variables site 2
Tokens                    []Token               <- variables site 3 (Token.Payload)
StartedAt, EndedAt
History                   []NodeVisit           <- CLEAN, re-derived: NodeVisit is
                                                   {NodeID,TokenID,EnteredAt,LeftAt,TaskID,CloseKind}
Tasks                     []humantask.HumanTask <- ⛔ variables site 4 AND a policy site
Timers []timerRecord; ArmedEvents []armedEvent; Boundaries []boundaryArm;
Scopes []Scope; RootCompensations []CompensationRecord;
ArchivedCompensations map[string][]CompensationRecord;
EventTriggeredSubprocesses []eventTriggeredSubprocessArm
```

**Every field of `humantask.HumanTask`:**
```
TaskID, InstanceID, NodeID
Eligibility  authz.AuthzSpec   ⛔ THE eligibility policy — roles + attribute predicate
Candidates   []authz.Actor     (D6 lists this — actors)
State
Claim        *Claim            (D6 lists this — actors)
Completion   *Completion       (D6 lists this — actors + notes)
CreatedAt, DueAt
Vars         map[string]any    ⛔ PROCESS VARIABLES, a 4th variables site
```
`HumanTask.Vars` is not incidental: it is the map handed to the authorizer for attribute
predicates at `runtime/task/service.go:199,234,255,306` (`s.authz.Authorize(ctx,
task.Eligibility, actor, task.Vars)`) — i.e. it is *by construction* the process-variable map.

**Why the document-side count is nevertheless right** (re-derived, so the next reader need not):
`taskJSON` (`service/instance.go:233-242`) is
`{TaskID, NodeID, State, Candidates, Claim, Completion, CreatedAt, DueAt}` — it carries
**neither `Vars` nor `Eligibility`**. So D6's *document* count of 2 variables sites is correct,
and `ActionableTask` (`runtime/view/instance_actionable.go:25-42`) carries neither either. The
defect is confined to the **state** redactor — which is exactly the one feeding the
consumer-customizable seam.

**Consequence.** Plan Task 5's `RedactState` sets `out.Variables = nil`,
`out.StartVariables = nil` and clears `Tokens[i].Payload`, and never touches `Tasks[i].Vars`
or `Tasks[i].Eligibility`. Worse, its task-clone is guarded by
`needTaskCopy := red.Has(authz.RedactActors) || red.Has(authz.RedactNotes)`, so a consumer
configuring `WithRedaction(authz.RedactVariables)` alone does not even clone the task slice.
Under the **default** posture (all four categories) a custom `InstanceMapper` still receives:
- the complete process-variable map, via `st.Tasks[i].Vars`; and
- every open task's eligibility spec — roles and the ABAC predicate — via
  `st.Tasks[i].Eligibility`, which is the *same class of data* D6 redacts the embedded
  `Definition` to protect (*"the embedded `Definition`, which carries every node's
  `Eligibility`"*).

So D6's quantifier *"closes path 3 even for a custom mapper"* is **false as designed**, and
`RedactPolicy`'s site list is incomplete on the state side.

**The prescribed test cannot catch it — this is the bundle's own warned failure mode, reproduced.**
Plan Task 6 step 1's T3b asserts exactly:
```go
if _, ok := seen.Variables["ssn"]; ok { … }
```
It inspects one of the four sites. Spec §2.1 warns in bold that *"a redactor covering only
`variables` would look correct against this fixture"* and mandates a token payload in the
fixture — the same reasoning stops one site short of `Tasks[].Vars`. This is
"a guard tested with a fixture from the half that works", which this repo's memory records as
having recurred three times in ADR-0189's lineage.

**Concrete fix.**
1. Correct the count to **four** variables sites in state in all four places listed above, and
   add `InstanceState.Tasks[].Eligibility` to `RedactPolicy`'s site table.
2. In `RedactState`: change the clone guard to
   `needTaskCopy := red.Has(RedactActors) || red.Has(RedactNotes) || red.Has(RedactVariables) || red.Has(RedactPolicy)`,
   clear `out.Tasks[i].Vars` under `RedactVariables`, and clear
   `out.Tasks[i].Eligibility = authz.AuthzSpec{}` under `RedactPolicy`.
3. Strengthen T3b's fixture and assertions to cover **all four** variables sites and the
   eligibility spec — assert over the whole marshalled `seen`, e.g.
   `strings.Contains(fmt.Sprint(seen), "111-22-3333")`, rather than probing one map key.
   A per-field assertion is what let three of four sites through.
4. **Preferred, because it does not rot:** make the guard structural. `HumanTask` already has a
   `Clone()` method (`humantask/humantask.go`) — add a redaction-completeness test that
   reflects over `engine.InstanceState` and `humantask.HumanTask` and fails when a
   `map[string]any` or `authz.AuthzSpec`-typed field exists that `RedactState` does not
   mention. A field added to `HumanTask` later re-opens this silently otherwise.

### C4 — CRITICAL — A FOURTH render mechanism exists that no document mentions: three admin endpoints call `NewInstanceView(pi.State())` directly, bypassing `mapInstance` entirely, and render process variables.

**Enumeration attacked.** The bundle's render-path list is a closed set of three mechanisms
(self-marshalled `instanceJSON`, `ActionableView`, `mapInstance`), with exactly one asserted
exemption: ADR-0190 D6 — *"`AdminListInstances` is exempt — it projects only IDs, status,
timestamps and incident count — and the guard pins that exemption so it cannot silently acquire
a sensitive field."* Spec §D6 repeats it under **"Not affected"**. Treating
`AdminListInstances` as *the* admin render path is what hid this.

**Commands run:**
```
grep -rn "NewInstanceView("   --include='*.go' . | grep -v _test
grep -rn "NewActionableView(" --include='*.go' . | grep -v _test
grep -rn "return http.Status[A-Za-z]*, pi," --include='*.go' . | grep -v _test
```

**Actual member set — every render call site in the repo:**
```
# self-marshalled ProcessInstance (path 1)
transport/http/httpcore/endpoints.go:65        GetInstanceSnapshot   -> return …, pi, nil

# ActionableView (path 2)
transport/http/httpcore/endpoints.go:77        GetActionableView

# mapInstance (path 3) — six sites, see C2
transport/http/httpcore/endpoints.go:42,52,94,133,158,182

# ⛔ DIRECT NewInstanceView — a FOURTH mechanism, in NO document
transport/http/httpcore/admin_endpoints.go:111  ResolveIncident            POST /admin/instances/{id}/incidents/{incidentID}/resolve
transport/http/httpcore/admin_endpoints.go:121  CancelInstance             POST /admin/instances/{id}/cancel
transport/http/httpcore/admin_endpoints.go:514  ResolveCompensationStall   POST /admin/instances/{id}/compensation/resolve-stall

# the default mapper, for completeness
transport/http/httpcore/seam.go:162,181         ResolveConfig installs NewInstanceView as the DEFAULT InstanceMapper
transport/http/httpcore/endpoints.go:17         mapInstance's nil-mapper fallback
```

`NewInstanceView` sets `Variables: st.Variables` (`transport/http/httpcore/view.go:31` — the
bundle's own citation, and it is exact). So all three admin endpoints above render the full
process-variable map.

**Why this defeats the fix as planned.** C2's natural remedy — redact inside `mapInstance` —
does **not** reach these three, because they never call `mapInstance`. Neither does plan Task 6,
whose only prescribed edits are `GetInstance`, `StartInstance` and `GetActionableView`, and whose
File Structure table lists `endpoints.go` as the sole `httpcore` file to modify —
`admin_endpoints.go` appears nowhere in the plan except as the *exempt* `AdminListInstances`
citation. T12's guard, seeded from the bundle's three-path list plus one exemption, would
enumerate `AdminListInstances` as the admin entry and never look at its three neighbours in the
same file.

**Severity rationale.** These are `AdminRoutes`, which ADR-0095 makes default-absent, so they are
not reachable in a deployment that never mounts admin. But the bundle's own Context states the
12 admin operations *"have no authorization"*, and D7 leaves the decorators opt-in — so in a
mounted admin deployment without decorators, three state-changing admin endpoints return
unredacted process variables while the four instance read paths do not. The posture is
inconsistent in exactly the direction the delivery exists to fix.

**Concrete fix.**
1. Add the fourth mechanism to the render-path enumeration in all four places C2 lists. The
   honest statement is **four mechanisms across eleven entry points**, not "three render paths".
2. Redact at `NewInstanceView` itself — give it a `RedactionSet` parameter, or add
   `NewInstanceViewRedacted`. That single change covers the three admin sites *and* the default
   mapper installed at `seam.go:162,181`, leaving only genuinely custom mappers to the
   `RedactState` route.
3. Add `transport/http/httpcore/admin_endpoints.go` to the plan's File Structure table and to
   Task 6's file list.
4. Broaden T12: it must enumerate every call of `NewInstanceView`, `NewActionableView` and
   `mapInstance`, in **both** `endpoints.go` and `admin_endpoints.go`, rather than a
   hand-written list of three plus one exemption.

### C5 — MAJOR — The spec's own summary sentences still say "two" in three places, contradicting the correction the bundle is built around

**Quantifiers attacked.** The bundle's headline correction is 2 → 3 read/render paths, asserted
in ADR D6, spec §1, spec §2.1, spec §D6, spec §7 T12 and plan Task 7. Three *recap* sentences
were never updated — exactly the Premise Discipline failure mode ("the false claims that survive
review are the summary sentence appended to correct reasoning").

**Command run:**
```
grep -n "both read\|two read\|two render\|both endpoints\|two endpoints\|across two" \
  docs/specs/2026-08-26-*.md docs/adr/0190-*.md docs/plans/2026-08-26-*.md
```

**Actual member set — every surviving "two", all in the spec:**
```
:419  §5 Consequences  "The disclosure on both read endpoints is closed in the default deployment"
:506  §9 item 3        "tests that actually render the two endpoints or assert on the marshalled
                        instance document"
:530  §10 Phasing      "an operation gate over 20 operations, two wiring-time refusals, a
                        redaction policy across two render paths, five admin decorators"
```
The ADR and the plan are clean on this axis; the defect is confined to the spec, and `:530` is
the worst because it is the *scoping* sentence that justifies the phase split — it under-counts
the very work Phase 1 must do.

⚠ Note `:530`'s "two wiring-time refusals" is **correct** (D4a and D4b), so a reader skimming
that sentence has no signal that the adjacent "two" is stale. Do not fix by pattern-matching the
word.

**Concrete fix.** `:419` → *"on all three read paths"*; `:506` → *"the three read endpoints"*;
`:530` → *"a redaction policy across four render mechanisms"* (per C4). Then re-grep for
`\btwo\b` in the spec and check each survivor individually.

---

### C6 — MAJOR — The plan turns the spec's explicitly-uncounted candidate list into a definite count ("the eight files"), and two independent nets over that question are nearly disjoint

**Quantifier attacked.** Spec §9 item 3 lists 8 files and hedges the list explicitly:
> *"gives this member set — **listed, not counted**, because two different nets can agree on a
> total while being disjoint on members"*

The plan's Phase 1 verification checklist restates it as:
> *"⚠ Expect breakage in **the eight files** named in spec §9 item 3; each break must be
> adjudicated as *correct* … rather than patched away."*

Restating stripped the hedge — the sentence stops looking contingent and nobody checks it again.
This is the documented failure this repo names *"re-verify claims you inherit before restating
them"*, and it matters operationally: an implementer told "the eight files" adjudicates eight
and treats a ninth failure as a regression to patch away, which is the opposite of the intent.

**Commands run:**
```
grep -rln --include='*_test.go' -E 'NewInstanceView|NewActionableView|newInstanceJSON|/snapshot|/actionable' .
grep -rln --include='*_test.go' -E '"variables"|"candidates"|"allowed_actions"|"eligible_roles"|"claim"' .
```

**Net B (renderer/route net) — 10 files.** Overlaps the bundle's 8 on 7, and adds three the
bundle does not name:
```
+ transport/http/httpcore/view_test.go     <- tests NewInstanceView ITSELF, the function whose
                                              Variables rendering the fix changes (view.go:31)
+ internal/atrest/schema_test.go
+ runtime/human_example_test.go
- service/instance_test.go                 <- in the bundle's 8, NOT matched by this net
```
**Net C (JSON-key net) — 13 files.** Its intersection with the bundle's 8 is **one member**
(`service/instance_test.go`).

I did **not** confirm that all 13 break — that requires the change to exist, and I flag it as
such rather than asserting it. The finding is not "the number is 9" or "the number is 13"; it is
that **three nets over one question return three substantially different member sets**, which is
precisely why the spec hedged and why the plan may not un-hedge it.

⚠ `transport/http/httpcore/view_test.go` deserves a named callout regardless of the net
argument: it is the unit test for `NewInstanceView`, and under C4's recommended fix (redaction
inside `NewInstanceView`) it breaks with certainty. It is absent from the bundle's list.

**Concrete fix.**
1. Restore the hedge in the plan: *"the candidate set named in spec §9 item 3, which is
   explicitly listed-not-counted; expect members outside it."*
2. Add `transport/http/httpcore/view_test.go` to the candidate set.
3. Replace the prose estimate with a mechanical step the implementer actually runs:
   *"after Task 6, run `go test ./... 2>&1 | grep -E '^(---|FAIL)'` and adjudicate every failing
   test by name."* A list written before the change cannot be authoritative about it.

---

### C7 — MINOR — The gin/fiber parity "ASSUMPTION (unverified)" is resolvable in one command, and the answer is yes at the dispatch layer

**Quantifier attacked.** Spec §2.6: *"`ASSUMPTION (unverified)`: the gin and fiber adapters
expose the same five route groups with the same handler semantics as stdlib."* Spec §9 item 4
re-raises it, and plan "Phases 2 and 3" carries it forward as still-unverified. Premise
Discipline permits an assumption only when it *cannot be executed in reasonable time*; this one
takes seconds.

**Commands run:**
```
grep -n "^type .*Routes struct" transport/http/{stdlib,gin,fiber}/groups.go
for a in stdlib gin fiber; do grep -oE 'httpcore\.[A-Z][A-Za-z]+\(' transport/http/$a/groups.go \
  | sort -u > /tmp/set_$a.txt; done; diff /tmp/set_stdlib.txt /tmp/set_gin.txt; diff /tmp/set_gin.txt /tmp/set_fiber.txt
for a in stdlib gin fiber; do grep -c 'httpcore.RequestActor(' transport/http/$a/groups.go; done
```

**Observed output:**
```
stdlib: InstanceRoutes:27  MessageRoutes:98   TaskRoutes:124  AdminRoutes:216  HealthRoutes:484
gin:    InstanceRoutes:18  MessageRoutes:107  TaskRoutes:147  AdminRoutes:252  HealthRoutes:546
fiber:  InstanceRoutes:20  MessageRoutes:97   TaskRoutes:131  AdminRoutes:247  HealthRoutes:522
        -> five route groups per adapter, same five names. CLAIM CONFIRMED.

stdlib: 29 distinct httpcore calls
gin:    29        diff stdlib gin  -> IDENTICAL
fiber:  29        diff gin fiber   -> IDENTICAL
httpcore.RequestActor( per adapter: stdlib 3, gin 3, fiber 3   (= the "nine adapter call
        sites" named in transport/http/httpcore/resolve_actor.go:170)
```

⚠ **Scope of what this proves, stated so it is not over-restated later:** all three adapters
dispatch to the *identical set of 29 `httpcore` functions*, so the render and gate logic is
genuinely shared and one implementation does cover all three. It does **not** prove identical
body-decode, error-write or middleware ordering — those live in each adapter's own helpers
(`decodeRequestBody`, `writeErr`), which this net does not inspect. Downgrade the assumption to
that narrower open question rather than deleting it.

**Concrete fix.** Replace §2.6's first bullet with the executed result above and the narrowed
residual. Keep T13 (the parity suite) — it now guards a smaller claim.

### C8 — MAJOR — The repo ships a FOURTH `authz.Authorizer` that defeats BOTH of D4's refusals, and it lives in the consumer test harness

**Enumeration attacked.** ADR-0190 D4b and spec §D4b both name a closed set of authorizers
"known not to honour privileges": *"(`authz.RoleAuthorizer`, `authz.AllowAll`)"*, and the
residual is scoped to *"a **third-party** authorizer that silently ignores them"*. D4a's check is
`if _, ok := c.authz.(authz.AllowAll); ok`.

**Command run:**
```
grep -rn "func (.*) Authorize(ctx context.Context" --include='*.go' . | grep -v _test
grep -rn "_ authz.Authorizer = " --include='*.go' .
```

**Actual member set — every in-repo `authz.Authorizer` implementation:**
```
authz/authz.go:106        AllowAll                 (compile-assert authz/authz.go:97)   — honours nothing
authz/authz.go:124        RoleAuthorizer           (compile-assert authz/authz.go:98)   — ignores Privileges
casbinauthz/casbinauthz.go:162  *casbinauthz.Authorizer  — DOES honour Privileges; a WRAPPER, delegating
                                to internalcasbin.Authorizer (`a.inner.Authorize`, :163)
processtest/spyauthz.go:44      *processtest.SpyAuthorizer  ⛔ NOT IN THE BUNDLE
```

**`processtest.SpyAuthorizer` breaks both refusals:**
- Its godoc (`processtest/spyauthz.go:24-25`): *"By default it **allows all actors**"*. `Authorize`
  reads `decide := s.decide` and, when nil, leaves `err` nil and returns it (`:44-57`). It is
  behaviourally `AllowAll` — but it is **not of type `authz.AllowAll`**, so **D4a's type assertion
  does not fire**. A consumer wiring `processtest.NewSpyAuthorizer()` with
  `WithOperationPolicy` gets a construction that succeeds and a gate that permits everything —
  the precise scenario D4a exists to refuse.
- It ignores `spec` entirely unless programmed, so it does not honour `Privileges` either, and
  **D4b's known-authorizer list does not contain it** — a `Privileges`-only spec passes.

`processtest` is not third-party: it is this repo's **public consumer test harness**. So the
residual the ADR frames as *"outside what this check can see"* has an in-repo instance, and the
word "third-party" makes it sound out of scope when it is not.

**The general shape, which is the more important half.** `casbinauthz.Authorizer` demonstrates
that **wrapping is the normal idiom here**. Any decorator around `authz.AllowAll` — a
metrics-recording or audit-logging authorizer, the obvious thing a consumer builds — defeats
D4a's type check exactly as `SpyAuthorizer` does. ADR-0190 states this residual for D4b and
**does not state it at all for D4a**, whose Consequences entry claims flatly that
*"`AllowAll` beneath a configured policy"* is among the *"three measured fail-opens … refused
loudly instead of passing silently."* That quantifier is false for any wrapped `AllowAll`.

**Concrete fix.**
1. Add `processtest.SpyAuthorizer` to D4b's known-not-to-honour-privileges list, and drop
   "third-party" from the residual — say *"any authorizer this check does not name, including
   `processtest.SpyAuthorizer` in our own harness."*
2. State D4a's symmetric residual explicitly in the ADR: **a wrapped `AllowAll` is invisible to
   the type check**, so D4a refuses the naive fail-open and not the decorated one.
3. Consider making the capability explicit instead of type-sniffed: an optional interface
   `interface{ HonoursPrivileges() bool }` that `casbinauthz.Authorizer` implements, with
   "absent ⇒ does not honour" as the fail-closed default. That inverts the enumeration from a
   list-of-known-bad (which rots at every new authorizer) to a declaration, and it is the same
   optional-interface idiom the plan already adopts for `service.Redactable` in Task 4.

---

### C9 — MINOR — "six of them mutate" is five

**Count attacked.** ADR-0190 Context and spec §1, identically:
> *"The 12 admin operations have no authorization and no audit record; **six of them** mutate
> authorization policy or process state."*

**Command run:** `sed -n '1,60p'` over the four admin interface files (see the clean
re-derivation section above for the full 12-member set).

**Actual member set — the 12 admin operations partitioned by mutation:**
```
MUTATES (5):
  DeadLetterAdmin.Redrive       service/deadletter.go:25   re-dispatches dead-lettered messages -> process state
  PolicyAdmin.AddPolicy         service/policyadmin.go:28  authorization policy
  PolicyAdmin.RemovePolicy      service/policyadmin.go:31  authorization policy
  PolicyAdmin.AddRole           service/policyadmin.go:37  authorization policy
  PolicyAdmin.RemoveRole        service/policyadmin.go:40  authorization policy

READ-ONLY (7):
  DeadLetterAdmin.ListDeadLettered, LineageAdmin.Lineage, RelayStatsAdmin.OutboxStats,
  TimerAdmin.Stats, TimerAdmin.ListArmedPage, PolicyAdmin.ListPolicies, PolicyAdmin.ListRoles
```
⇒ **5**, not 6. (A plausible origin of the 6: counting the three *routes* on `AdminRoutes` that
mutate process state — resolve-incident, resolve-stall, cancel — would give 8, not 6, and those
are `Service` operations already counted among the 8 ungated ones, not among the 12.)

**Concrete fix.** *"five of them mutate authorization policy or process state (four policy
mutations on `PolicyAdmin`, plus `DeadLetterAdmin.Redrive`)"*, in both documents. Naming the
closed set instead of counting it is what Premise Discipline prescribes here.

---

### C10 — MINOR — The `Privileges` warning is quoted accurately but attributed to the wrong godoc, and the field's OWN godoc says the opposite

**Citation attacked.** Spec §2.2: *"`authz.AuthzSpec`'s **own godoc** records the cause:
'`Privileges` is reserved for future resource-privilege checks and is NOT evaluated by
RoleAuthorizer.'"* ADR-0190 Context repeats the quote.

**Command run:** `grep -rn "reserved for future\|NOT evaluated by RoleAuthorizer" --include='*.go' .`

**Actual:**
```
authz/authz.go:119   // Note: [AuthzSpec].Privileges is reserved for future resource-privilege checks
authz/authz.go:120   // and is NOT evaluated by RoleAuthorizer.
```
The text is **verbatim exact** — but lines 119-120 are part of **`RoleAuthorizer`'s** doc comment
(`type RoleAuthorizer struct{}` is at `:121`), not `AuthzSpec`'s (`:82`).

`AuthzSpec.Privileges`' own godoc, at `authz/authz.go:84`, reads:
```go
Privileges []string // resource-privilege tokens evaluated by a casbin-backed Authorizer (e.g. "finance-task claim")
```
— which **reads as a reassurance that privileges are evaluated**, with no hint that the default
authorizer ignores them.

This *strengthens* D4b rather than weakening it: an author writing a `Privileges`-only spec reads
line 84 and sees "evaluated"; the disclaimer sits 35 lines away on a different type. But the
attribution must be fixed, because a reader following the citation to `AuthzSpec`'s godoc finds
no such sentence and may conclude the premise was fabricated.

**Concrete fix.** Re-attribute to `authz/authz.go:119-120` (`RoleAuthorizer`'s godoc), and add
the observation above as a supporting argument for D4b: *"the field's own comment at
`authz/authz.go:84` says privileges are 'evaluated by a casbin-backed Authorizer' and does not
warn that the default authorizer ignores them — which is why the mistake is natural."*

---

### C11 — MAJOR — The prescribed purity-guard ablation cannot produce a RED: it creates an import cycle, so the test binary never builds

**Falsifiability claim attacked.** Plan Task 1 Step 5, on `authz/purity_test.go`:
> *"A test that does not exist cannot 'fail today', so its falsifiability is established by
> ablation. Ablate it: add `_ "github.com/kartaladev/wrkflw/engine"` to `authz/redaction.go`,
> run `go test -count=1 ./authz/...`, **observe RED**, then restore from a `cp` backup."*

**Commands run:**
```
cd authz && go list -f '{{join .Imports "\n"}}' ./          # the guard's own command
go list -f '{{join .Imports "\n"}}' ./engine | grep kartaladev
```
**Observed:**
```
# authz's direct imports — the guard as written is CORRECT and returns exactly one in-repo entry
context / errors / fmt / github.com/kartaladev/wrkflw/internal/expreval / maps / slices

# engine's direct imports INCLUDE authz:
github.com/kartaladev/wrkflw/action
github.com/kartaladev/wrkflw/authz          <---
github.com/kartaladev/wrkflw/definition/{activity,event,flow,model,schedule}
github.com/kartaladev/wrkflw/humantask
github.com/kartaladev/wrkflw/internal/expreval
```
`engine` imports `authz`, so importing `engine` **from** `authz` is an **import cycle**.
`go test ./authz/...` then fails at load time with *"import cycle not allowed"*: the package does
not compile, the test binary is never built, and the guard's `t.Errorf` loop never executes. The
run is red-coloured, but it is **not** evidence that the guard can fail — it is evidence that the
package can be broken. This repo's own rule: *a mutation that fails to compile is not a RED.*

⚠ Note the ablation would also fail *before* the loop even on a compiling package: the guard
`t.Fatalf`s when `go list` errors, so several plausible ablations fail in the fixture rather than
the assertion.

**Concrete fix.** Ablate with an in-repo package that `authz` may legally import — one that does
**not** depend on `authz`. `github.com/kartaladev/wrkflw/internal/expreval` is already allowed, so
pick a different acyclic one and verify acyclicity first:
```bash
go list -deps ./<candidate> | grep -q 'wrkflw/authz' && echo "CYCLE — pick another"
```
Then require the ablation to show the guard's **own message** in the output, not merely a red
exit — e.g. `grep -q 'authz must not import' /tmp/t1.log`. A red exit with an import-cycle
loader error must be treated as an invalid ablation and retried.

---

### C12 — MINOR — "`go list -deps ./authz` returns exactly one in-repo dependency" is not what the command prints

**Count attacked.** ADR-0190 D2 and spec §D2: *"**Measured, not assumed:** `go list -deps ./authz`
returns **exactly one** in-repo dependency, `internal/expreval`."*

**Command run:** `go list -deps ./authz | grep kartaladev`
**Observed output:**
```
github.com/kartaladev/wrkflw/internal/expreval
github.com/kartaladev/wrkflw/authz
```
**Two** in-repo lines: `go list -deps` includes the named package itself. The bundle's *meaning*
(one in-repo dependency besides itself) is correct, and the guard the plan actually ships uses
`.Imports`, not `-deps`, and is correct — but a reader who runs the quoted command sees a
different number than the sentence promises, which is how a "measured" claim loses its authority.

**Concrete fix.** Quote the command whose output matches the sentence:
`go list -deps ./authz | grep kartaladev | grep -v '/authz$'` → one line; or restate as
*"`go list -deps ./authz` lists `internal/expreval` as authz's only in-repo dependency (plus
authz itself, which `-deps` always includes)."*

### C13 — MINOR — D6's "except `Claim.Actor`" names one exception; there are two

**Quantifier attacked.** Spec §D6, "Wire shape":
> *"**All four categories sit on `omitempty` fields except `Claim.Actor`** (`json:"actor"`, no
> `omitempty`), so a redacted claimed task renders `"actor":{"id":""}` rather than dropping the
> key."*

**Command run:** `grep -n -A12 "^type Claim struct" humantask/humantask.go` and the same for
`Completion`, `authz.Actor`, `instanceJSON`, `taskJSON`, `tokenJSON`, `NextAction`.

**Actual member set — every field the four categories touch, with its tag:**
```
Actors:
  taskJSON.Candidates            `json:"candidates,omitempty"`   service/instance.go:237
  taskJSON.Claim                 `json:"claim,omitempty"`        service/instance.go:238
  humantask.Claim.Actor          `json:"actor"`      NO omitempty   humantask/humantask.go:61  <- named
  taskJSON.Completion            `json:"completion,omitempty"`   service/instance.go:239
  humantask.Completion.Actor     `json:"actor"`      NO omitempty   humantask/humantask.go:72  ⛔ NOT NAMED
  ActionableTask.Candidates      `json:"candidates,omitempty"`   runtime/view/instance_actionable.go:36
  ActionableTask.Claim           `json:"claim,omitempty"`        runtime/view/instance_actionable.go:33
Variables:
  instanceJSON.Variables         `json:"variables,omitempty"`    service/instance.go:125
  tokenJSON.Payload              `json:"payload,omitempty"`      service/instance.go:151
Notes:
  humantask.Completion.Note      `json:"note,omitempty"`         humantask/humantask.go:78
Policy:
  instanceJSON.Definition        `json:"definition,omitempty"`   service/instance.go:143
  NextAction.Condition           `json:"condition,omitempty"`    runtime/view/instance_actionable.go:18
```
⇒ **two** fields lack `omitempty`, not one. `Completion.Actor` behaves identically to
`Claim.Actor`: a redacted completed task renders `"completion":{"actor":{"id":""},…}`.

⚠ Supporting detail worth adding, because it makes the design *more* robust than the sentence
claims: `encoding/json`'s `omitempty` **has no effect on struct-typed fields at all**, so a
zeroed `authz.Actor` would render even if the tag carried `omitempty`. The ADR-0152
discriminator property the paragraph is defending therefore does not depend on the tag. And
`authz.Actor.ID` is `json:"id"` with no `omitempty` (`authz/authz.go:36`), so the promised
`{"id":""}` rendering is **exact** — that half of the claim verifies clean.

**Concrete fix.** *"…except `Claim.Actor` and `Completion.Actor` (both `json:"actor"`, no
`omitempty`) — and note that `omitempty` is inert on struct fields regardless, so both render as
`{"id":""}` by construction, not by tag."*

---

### C14 — MINOR — Plan Task 2 cites a `ResolveConfig` struct literal that does not exist in `service`, then implements the opposite of what it prescribes

**Citation attacked.** Plan Task 2:
> *"⚠ **Placement matters and has bitten this repo twice.** The default must live in
> `ResolveConfig`'s **struct literal**, not a post-loop nil-guard, for the same reason
> `MaxBodyBytes` and `BodyReadTimeout` do."*

**Command run:**
```
grep -rn "func ResolveConfig" --include='*.go' .
grep -rn "MaxBodyBytes:\|BodyReadTimeout:" --include='*.go' . | grep -v _test
sed -n '167,170p' service/service.go
```
**Observed:**
```
transport/http/httpcore/seam.go:159   func ResolveConfig[R any](...)   <- the ONLY ResolveConfig in the repo
transport/http/httpcore/seam.go:168       MaxBodyBytes:    defaultMaxBodyBytes
transport/http/httpcore/seam.go:169       BodyReadTimeout: defaultBodyReadTimeout

service/service.go:167  func NewProcessEngine(opts ...Option) (*ProcessEngine, error) {
service/service.go:168      c := &engineConfig{}        <- a BARE literal; no defaults, by construction
```
`service` has **no** `ResolveConfig` and **no** defaults-bearing struct literal. The cited
precedent lives in a different package, on a different config type, resolved by a different
function. An implementer told "put it in `ResolveConfig`'s struct literal" has nowhere to put it.

The paragraph then contradicts itself: Task 2 Step 3's own code is a **post-loop guard** —
`if !c.redactionSet { c.redaction = … }` — placed *"ALONGSIDE the existing authz default"*, i.e.
next to `if c.authz == nil { c.authz = authz.AllowAll{} }` at `service/service.go:199-201`, which
is exactly the post-loop-nil-guard shape the warning forbids.

The *substance* is right — a `bool` flag distinguishes "never called" from "called with no
categories", and that is the correct fix — but the rationale points at the wrong file and the
wrong mechanism, which is how a plan produces a confidently-wrong implementation.

**Concrete fix.** Rewrite the warning as: *"`service.engineConfig` is built from a bare
`&engineConfig{}` literal (`service/service.go:168`) and defaults are applied post-loop
(`c.authz == nil` at `:199`). A nil map cannot distinguish 'never called' from 'called with no
categories', so carry an explicit `redactionSet bool` and guard on that, not on nil. (The
struct-literal-default convention the reader may recall is `httpcore.ResolveConfig`
(`transport/http/httpcore/seam.go:159-169`) — a different package with a different mechanism;
it does not apply here.)"*

---

## Verified-clean inherited citations (re-derived, so they are not re-audited next round)

| claim | verdict | evidence |
|---|---|---|
| ADR-0189's header instructs *"0190 must argue against **ADR-0095 §'Admin-by-composition (default-absent)'**"* | ✅ **verbatim** | `docs/adr/0189-*.md`, "Split out to ADR-0190" bullet |
| ADR-0189 gives the reason as *"a decision round 2 found this bundle contradicting without ever citing it"* — i.e. a 0189 draft reintroduced default-deny | ✅ accurate; ADR-0190's re-derivation of the instruction's *purpose* is a fair reading, not a convenient one | same bullet |
| ADR-0095 says default-absent *"replaces the old default-deny (403) … this is safer"* | ✅ **verbatim** | `docs/adr/0095-*.md:159-165`: *"### Admin-by-composition (default-absent) … **Default-absent** replaces the old default-deny (403): admin endpoints simply do not exist in a deployment that does not mount `AdminRoutes`. This is safer than a built-in default-deny gate and idiomatic per framework."* |
| ADR-0189's own filing named only **two** disclosing endpoints | ✅ confirmed — *"actor attributes rendered to unauthenticated readers by `GET /instances/{id}/actionable` and `/snapshot`"*; `docs/plans/HANDOVER.md` repeats the pair. The bundle's "the handover … named two" is accurate | ADR-0189 Backlog bullet (ii); `HANDOVER.md` 🆕 section |
| backlog **52** = the `AllowAll` default authorizer | ✅ | `HANDOVER.md:200` *"D2 (backlog 52, the allow-all default authorizer)"*; listed under **Still open — Design tier** |
| backlog **53** = the empty `AuthzSpec` meaning allow-all | ✅ | `HANDOVER.md:201` *"D3 (backlog 53, the empty `AuthzSpec` that means allow-all)"*; listed still open |
| backlog **103** = a deny-list predicate over absent `vars` allows | ✅ | `HANDOVER.md:114` *"`vars.status` over empty vars and `actor.Attributes.status` with the key absent are byte-identical ALLOWs"*; listed still open |
| all three of 52/53/103 are **open** | ✅ | `HANDOVER.md` "Still open — Design tier" list contains 52, 53 and 103 |
| `service/service.go:200` is `c.authz = authz.AllowAll{}` | ✅ exact (the `if` is `:199`) | `sed -n '196,204p' service/service.go` |
| `service/service.go:316` is `if _, ok := c.authz.(authz.AllowAll); ok {` | ✅ **exact line** | `grep -n "c.authz.(authz.AllowAll)" service/service.go` |
| `transport/http/httpcore/errors.go:87` maps `authz.ErrNotAuthorized` → 403 | ✅ exact (`:87` is the `case`, `:88` the return) | `sed -n '82,92p'` |
| `view.go:31` is `Variables: st.Variables` in `NewInstanceView` | ✅ **exact line** | `sed -n '23,40p' transport/http/httpcore/view.go` |
| `admin_endpoints.go:88-96` — `AdminListInstances` projects IDs/status/timestamps/incident count only, carries none of the four categories | ✅ correct (`InstanceID` is `:87`, one line above the cited range) | `sed -n '80,100p'` |
| `runtime/view/instance_actionable.go:31-35` renders `Claim` and `Candidates` verbatim | ⚠ **off by one**: `Claim` is `:33`, `Candidates` is `:36` — outside the cited range. Substance right, range short | `sed -n '20,45p'` |
| `service/options.go:146` + *"This is a marshalling policy only"* | ⚠ **off by four**: `:146` is `func WithoutEmbeddedDefinition()`; the quoted sentence is at `:142`. Quote itself is verbatim | `sed -n '138,152p'` |
| `stdlib/groups.go:219` for the admin deps | ✅ inside `type AdminRoutes struct` (`:216-224`); `:219` is `Policies service.PolicyAdmin` | `sed -n '216,224p'` |
| plan Task 4: *"No in-repo type outside `service/instance.go` implements [`ProcessInstance`]"* | ✅ **exhaustively verified** — `grep -rn ") ActiveTasks()"` returns exactly one hit, `service/instance.go:72` | — |
| plan Task 5: `engine.InstanceState` has unexported fields (`ids idSource`) and unexported-typed slices | ✅ confirmed — `ids idSource`, plus `Timers []timerRecord`, `ArmedEvents []armedEvent`, `Boundaries []boundaryArm`, `EventTriggeredSubprocesses []eventTriggeredSubprocessArm` | `engine/state.go:265+` |
| `resolve_actor.go`'s *"nine adapter call sites"* | ✅ 3 adapters × 3 task verbs = 9 | `grep -c 'httpcore.RequestActor(' transport/http/{stdlib,gin,fiber}/groups.go` → 3,3,3 |
| `engine/purity_test.go` is the repo's ONLY purity test; `authz` has none | ✅ | `find . -name 'purity*_test.go'` → one hit |
| the *document*-side variables count of **2** (`instanceJSON.Variables`, `Tokens[].Payload`) | ✅ correct — `taskJSON` carries neither `Vars` nor `Eligibility` | `grep -A18 "type taskJSON struct"` |

⚠ **One residual I could not close by execution.** ADR-0190 Backlog says it *"closes the
unauthenticated read disclosure **filed by** ADR-0189."* ADR-0189 filed that item as prose ("to
be filed **on the day this ships**") and `HANDOVER.md`'s 🆕 section carries it **without a
backlog number**, while every other item the bundle references has one. The ADR is honest not to
invent a number, but "closes X" is untrackable when X has no identifier. **Recommend** assigning
it a number in `HANDOVER.md` as part of this delivery, so the closure can be verified later.

---

## Summary index (every label glossed, per CLAUDE.md General rule 13)

| ID | Sev | the count/quantifier attacked | true value |
|---|---|---|---|
| **C1** | **Critical** | D5's *"operations with no instance at all (`OpStartInstance` and **all 12 admin operations**)"* — the enumeration that decides which specs may carry an ABAC `Attribute` | **15 members, not 13**: `OpListInstances` and `OpDeliverMessage` are missing (both have no instance ⇒ their `Attribute` specs would evaluate over absent `vars` and **fail open**, reproducing backlog 103); `OpAdminLineage` is wrongly included (it *is* instance-scoped — `Lineage(ctx, instanceID)`) |
| **C2** | **Critical** | *"There are **three** render paths, not two"* + path 3 cited as `endpoints.go:42,52` | Path 3 has **six** `mapInstance` call sites (`endpoints.go:42,52,94,133,158,182`). The four unwired ones include `DeliverSignal` — and `POST /instances/{id}/signals` is **unauthenticated** (`RequestActor` is called on the 3 task verbs only), so the disclosure Phase 1 closes on `GET` is reachable by changing the verb |
| **C3** | **Critical** | *"`RedactVariables` has **THREE** sites in `engine.InstanceState`"* + *"closes path 3 **even for a custom mapper**"* | **Four** variables sites — `Tasks[].Vars` (`humantask.HumanTask.Vars`) is the fourth, and `Tasks[].Eligibility` is an unlisted **policy** site. `RedactState` clears neither, and its task-clone guard skips the loop entirely under `RedactVariables` alone. The prescribed test T3b checks only `seen.Variables["ssn"]`, so it cannot detect it |
| **C4** | **Critical** | The render enumeration's single asserted exemption, *"`AdminListInstances` is exempt"* | A **fourth render mechanism** exists in the same file and no document names it: `ResolveIncident` (`admin_endpoints.go:111`), `CancelInstance` (`:121`) and `ResolveCompensationStall` (`:514`) call `NewInstanceView(pi.State())` **directly**, bypassing `mapInstance`, and render process variables. Total: **4 mechanisms / 11 entry points** |
| **C5** | Major | three surviving *"two"* recaps in the spec (`:419` "both read endpoints", `:506` "the two endpoints", `:530` "two render paths") | contradict the bundle's own 2→3 correction; `:530` under-scopes the phase split |
| **C6** | Major | plan's *"the **eight** files named in spec §9 item 3"* | the spec hedged it as *"listed, not counted"*; the plan stripped the hedge. Three nets over the same question return substantially disjoint sets; `transport/http/httpcore/view_test.go` (the unit test for `NewInstanceView` itself) is absent from the list |
| **C7** | Minor | §2.6 *"`ASSUMPTION (unverified)`: gin and fiber expose the same five route groups…"* | **executable in one command, and CONFIRMED**: 5 groups per adapter; all three dispatch to an *identical* 29-member set of `httpcore` functions. Narrow the residual to decode/error-write helpers rather than leaving it whole |
| **C8** | Major | D4b's closed set *"(`authz.RoleAuthorizer`, `authz.AllowAll`)"* and its *"**third-party** authorizer"* residual | a **fourth** in-repo `Authorizer` exists — `processtest.SpyAuthorizer` (our own consumer harness), which **allows all by default** yet is not of type `authz.AllowAll`, so **D4a's type check does not fire** and D4b does not cover it. `casbinauthz.Authorizer` shows wrapping is the normal idiom, so **any** decorated `AllowAll` defeats D4a — a residual the ADR states for D4b and omits for D4a |
| **C9** | Minor | *"**six** of them mutate authorization policy or process state"* (of the 12 admin ops) | **five**: `Redrive` + the four `PolicyAdmin` mutators. The 7 others are read-only |
| **C10** | Minor | *"`authz.AuthzSpec`'s **own godoc** records the cause"* | quote is verbatim but lives at `authz/authz.go:119-120` on **`RoleAuthorizer`'s** godoc. `AuthzSpec.Privileges`' own comment (`:84`) says the opposite — *"evaluated by a casbin-backed Authorizer"* — which strengthens D4b but must be re-attributed |
| **C11** | Major | plan Task 1's purity-guard ablation: *"add `_ ".../engine"` to `authz/redaction.go` … **observe RED**"* | `engine` imports `authz`, so this is an **import cycle**: the package never compiles, the test binary never builds, the guard's assertion never runs. Not a RED |
| **C12** | Minor | *"`go list -deps ./authz` returns **exactly one** in-repo dependency"* | the command prints **two** in-repo lines (`-deps` includes the package itself). Meaning correct, quoted command mismatched |
| **C13** | Minor | *"All four categories sit on `omitempty` fields **except `Claim.Actor`**"* | **two** exceptions: `Completion.Actor` is equally untagged. (And `omitempty` is inert on struct fields, so the ADR-0152 property holds by construction, not by tag — `authz.Actor.ID` is `json:"id"`, so `{"id":""}` verifies exact) |
| **C14** | Minor | plan Task 2: *"the default must live in **`ResolveConfig`'s struct literal**"* | `service` has no `ResolveConfig` — the only one is `httpcore/seam.go:159`, a different package. `engineConfig` is built from a bare `&engineConfig{}` (`service/service.go:168`) with post-loop guards, and the task's own Step 3 code does the post-loop guard the warning forbids |

**Interaction note for the adjudicator.** C2, C3 and C4 are three faces of one enumeration and
must be fixed **together**: fixing C2 alone (redact inside `mapInstance`) leaves C4's three admin
sites open; fixing C4 alone (redact inside `NewInstanceView`) leaves custom mappers open; and
both leave C3's `Tasks[].Vars`/`Tasks[].Eligibility` disclosed through any path that hands out
`engine.InstanceState`. The single change that closes all three is to redact **state** at every
point where it crosses the transport boundary, and to derive the guard from
`grep`-able call sites rather than a prose list.
