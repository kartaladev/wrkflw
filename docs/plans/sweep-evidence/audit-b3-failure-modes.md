# Audit B3 — failure modes, gaps and contradictions

Lens: failure modes / gaps / cross-document contradictions.
Bundle commit: `3f317b63` (docs-only on top of `main` @ `70a631e9`).
Worktree: `scratchpad/audit-fail`. Step 0: all four bundle files present — VERIFIED.

Every source citation below was re-derived in this worktree (grep / sed / awk),
not inherited from the bundle. Where I could not execute, the finding says so.

---

## F1 — CRITICAL — The casbin authorizer is untouched, so the chain is NOT closed end to end

**Claim attacked.** ADR-0185 Consequences/Positive: *"The chain in Context §1–5 is
closed end to end. An unauthenticated caller … no longer benefits from an unstated
spec or a deny-list predicate over a missing variable."*
Decision 3: *"`RoleAuthorizer` denies a spec that is neither `Open` nor carries a
dimension it can evaluate."*
Decision 4: *"This is scoped to the **ABAC path only**, and the scoping is
structural rather than conventional: `authz` already owns a separate evaluator
instance (`authz/authz.go:23`) from the engine's."*

**Evidence.** There is a **third** evaluator instance and a **second**
`Authorizer` implementation, and the bundle's structural argument silently
excludes it:

```
internal/authz/casbin/authorizer.go:23   attrEval *expreval.Evaluator
internal/authz/casbin/authorizer.go:30   return &Authorizer{enforcer: e, attrEval: expreval.New()}
internal/authz/casbin/authorizer.go:33   // Authorize evaluates the three checks in order… An empty spec allows.
internal/authz/casbin/authorizer.go:44       if len(spec.Roles) > 0 {      // skipped when empty
internal/authz/casbin/authorizer.go:56       if len(spec.Privileges) > 0 { // skipped when empty
internal/authz/casbin/authorizer.go:66       if spec.Attribute != "" { … a.attrEval.EvalBool(…) }
internal/authz/casbin/authorizer.go:76       return nil                    // empty spec ⇒ ALLOW
```

So for a consumer wired with `casbinauthz`:
- an empty spec still allows (finding 3 unfixed);
- `AuthzSpec.Open` is not read at all — the new field is inert;
- the deny-list-over-absent-variable class (finding 4) is unfixed, because
  `attrEval` is a third `expreval.New()` with no `WithStrictReferences()`.

The plan confirms the omission: the only phase touching casbin is **phase 8**,
whose scope is `lastSuccessfulLoad` / `HealthCheck` / `WithStalePolicyBudget`
(plan §3 phase 8). No phase edits `internal/authz/casbin/authorizer.go`.

**Why this is Critical, not Major.** ADR-0185 Decision 3 *routes consumers to the
unfixed authorizer by name*: *"a consumer who wants privileges evaluated wires the
casbin authorizer."* The bundle's own migration advice moves a deployment from the
hardened authorizer to the unhardened one. `casbinauthz` is also the ADR-designated
production authorizer (CLAUDE.md: "casbin as the baseline").

**Proposed fix.** Either
(a) extend Decisions 3 + 4 to `internal/authz/casbin.Authorizer` — construct its
`attrEval` with `WithStrictReferences()`, add the `Open`/no-evaluatable-dimension
denial before step 1, and add the corresponding tests to phase 8 (they are
container-free: `Authorize` needs only a `SyncedEnforcer` from strings); or
(b) hoist the spec-shape gate out of the authorizers entirely — a
`authz.CheckSpecStated(spec) error` called by `runtime/task.TaskService` *before*
`s.authz.Authorize(...)` at all four sites, so every `Authorizer` implementation
(including a consumer's own) inherits it. (b) is the stronger fix and also closes
the identical hole in a consumer-supplied `Authorizer`, which (a) does not.
Either way ADR-0185's "closed end to end" sentence must be narrowed or the fix
must make it true.

---

## F2 — CRITICAL — Upgrading denies every in-flight open task; the deployment gate points the wrong way

**Claim attacked.** Plan §4 "Deployment-order gate": *"An **older** binary reading a
**newer** row silently drops `Open`… Readers must be upgraded before writers."*
ADR-0185 Consequences: *"Every existing definition with no eligibility becomes
invalid until it declares `open: true` or a dimension."*

Both statements are about **definitions**. The break that actually happens on
upgrade is about **persisted tasks**, and the bundle never mentions it.

**Evidence.** Eligibility is not re-derived from the definition at claim/complete
time — it is a **stored field on the task**, snapshotted at task creation:

```
humantask/humantask.go   type HumanTask struct { … Eligibility authz.AuthzSpec … }
runtime/task/service.go:199  s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars)   // Claim
runtime/task/service.go:234  s.authz.Authorize(ctx, task.Eligibility, by,    task.Vars)   // Reassign
runtime/task/service.go:255  s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars)   // Complete
runtime/task/service.go:306  s.authz.Authorize(ctx, task.Eligibility, by,    task.Vars)   // RefreshCandidates
```

`InstanceState.Tasks` is part of the persisted snapshot (`engine/step_state.go`
`cloneState` deep-copies `Tasks`; `internal/persistence/store/humantask_store.go`
persists them). So on the **forward** upgrade — new binary, old row, the direction
that always happens — every open task created before the upgrade carries
`Eligibility{Roles:nil, Privileges:nil, Attribute:"", Open:false}` and
`RoleAuthorizer` under Decision 3 **denies** it. Not a definition-authoring
migration: those tasks are already minted, and re-deploying the definition with
`open: true` does **not** rewrite them. They become permanently unclaimable,
uncompletable, and un-reassignable (all four sites deny), with no operator verb to
repair them.

Note the asymmetry the plan gets backwards: the direction it protects
(old reader / new row, `Open` dropped ⇒ deny) has the *same* failure as the
direction it ignores — but the ignored one needs no mixed-version deployment at
all. CLAUDE.md/memory records that this repo declares mixed-version replicas
unsafe, so the old-reader case is arguably out of contract; the single-version
forward upgrade is not.

**Proposed fix (pick one, but decide it in the ADR):**
1. **Grandfather by absence.** Make the denial predicate distinguish "spec written
   before `Open` existed" from "spec that omitted every dimension". That requires a
   tri-state (`Open *bool`, or a `SpecVersion` byte on `AuthzSpec`) so a
   nil/legacy value keeps today's open semantics for **already-persisted tasks**
   while new tasks must state intent. Ugly, but it is the only option that does
   not strand live work.
2. **A migration command** that rewrites open tasks whose eligibility is empty,
   setting `Open: true` — plus a release note that it must run between deploy and
   traffic. Requires a store-level write path that does not exist today.
3. **Scope Decision 3 to task creation, not authorization.** The engine refuses to
   *mint* a task from a `UserTask` with an unstated spec (`engine/step_nodes.go`
   AwaitHuman construction) while `RoleAuthorizer` keeps allowing an empty stored
   spec. Fail-closed at the boundary where the definition is read, which is also
   where the author can act on it. This is the cheapest and it composes with
   `model.Validate`'s authoring gate.

Whichever is chosen, plan §4 must be rewritten: as written it gates the wrong
direction and would let this ship.

---

## F3 — CRITICAL — `Reassign` bypasses the claimant guard in two hops, so Decision 5 closes nothing it claims to

**Claim attacked.** ADR-0185 Decision 5 and Consequences: *"an unauthenticated
caller … can no longer complete a task somebody else holds."*
And the mitigation sentence: *"The claimant guard can strand a task whose claimant
has left the organisation. Mitigation: `Reassign` already exists and is authorized
separately; the guard deliberately does not touch it."*

**Evidence.** `Reassign` is authorized against **the same eligibility spec as
Claim**, by the repo's own godoc, and it *overwrites the claim*:

```
runtime/task/service.go:206-217 (godoc)
   "Authorization policy: the reassigner (by) must satisfy the task's eligibility
    spec — the same check as Claim. A distinct admin/reassign-privilege model is
    deferred."
runtime/task/service.go:234      s.authz.Authorize(ctx, task.Eligibility, by, task.Vars)
engine/step_triggers.go:643      task.Claim = &humantask.Claim{Actor: authz.Actor{ID: t.To}, At: t.OccurredAt()}
engine/step_triggers.go:644      task.State = humantask.Claimed
```

So mallory, who is merely *eligible* (exactly the actor Decision 5 exists to stop),
calls `Reassign(taskID, from="alice", to="mallory", by=mallory)` — `from` matches
the current claimant, the eligibility check passes because it is the same check
Claim uses — and `handleHumanReassigned` makes her the claimant. She then completes
as claimant and Decision 5's comparison succeeds.

`Reassign` is *not* "authorized separately"; it is authorized **identically**. The
ADR's mitigation sentence and its security claim are the same sentence read in two
directions, and only one of them is true. The ADR names backlog 90 (claim theft on
the *claim* path) as the adjacent open item, but the *reassign* path is a **third**
route it does not name at all.

**Proposed fix.** Decision 5 must either
(a) extend the guard to `Reassign`: a reassignment whose `by.ID != claimant.ID`
requires something the eligibility spec cannot express — the smallest honest
version is a new `AuthzSpec` dimension or a dedicated `reassign` privilege token,
which is a design increment this bundle has not budgeted; or
(b) **state plainly in the ADR that the guard is defeatable by an eligible actor in
two calls, and that the residual property is only "a non-eligible or
unauthenticated actor cannot complete"** — which is still worth shipping, but is a
much smaller claim than the Consequences paragraph makes. Then move backlog 90 and
the reassign path into one named follow-up.
(b) with a corrected Consequences paragraph is acceptable; the current text is not.

---

## F4 — MAJOR — `httpcore.WithActorResolver` collides with the existing `service.WithActorResolver`, which means something else

**Claim attacked.** ADR-0185 Decision 1 / spec §4.1 Option C / plan phase 7:
*"`httpcore.WithActorResolver(func(context.Context) (authz.Actor, error))` is
offered as an override."*

**Evidence.** The name is already taken in this library's public API, for the
opposite concept:

```
service/options.go:99    func WithActorResolver(r humantask.ActorResolver) Option
humantask/humantask.go:170-176   // ActorResolver expands an eligibility spec together with process
                                 // variables into … type ActorResolver interface
authz/authz.go:30-33     "There is no first-class username or email — populate Attributes
                          from your [ActorResolver] if you need them."
```

`humantask.ActorResolver` answers *"who are the candidates for this spec?"*. The
proposed `httpcore.WithActorResolver` answers *"who is the authenticated caller?"*.
Two exported options, one library, same name, unrelated meanings — and the
`authz.Actor` godoc already points at the existing one, so that sentence becomes
ambiguous the moment the new name lands.

The spec's §2.2 enumeration (`grep -n "^func With" service/options.go` → 10
options) is correct — I re-derived exactly 10 — but the bundle read that list for
"is there an authorizer setter?" and did not notice the collision sitting on
line 99 of the same output.

**Proposed fix.** Rename the new seam: `httpcore.WithPrincipalResolver` +
`authz.ContextWithPrincipal` / `authz.PrincipalFromContext`, or keep `Actor` but
qualify: `WithActorFromContext(fn)`. Whatever is chosen, ADR-0185 Decision 1 must
name the collision and say which term means which, and phase 12 must fix the
`authz.Actor` godoc's `[ActorResolver]` link.

---

## F5 — MAJOR — Phases 3 and 4 are circularly dependent; phase 3 cannot compile

**Claim attacked.** Plan §2 phase table: phase 3 (`engine`) *depends on 1, 2*;
phase 4 (`definition/*`) *depends on 2*. Plan §3 phase 4 body:
> *"`engine/step_nodes.go:723`'s `authz.AuthzSpec` construction gains
> `Open: ut.OpenEligibility` — ⚠ **this line is in `engine`**, so it lands in
> phase 3, not here."*

**Evidence.** `ut.OpenEligibility` is a field the plan creates **in phase 4**
(*"`activity.WithOpenEligibility()`; `UserTask.OpenEligibility bool`"*). Phase 3
runs **before** phase 4 and is explicitly the inline controller phase. Writing
`Open: ut.OpenEligibility` in phase 3 is a compile error until phase 4 exists;
omitting it means phases 3–11 all run with `AuthzSpec.Open` permanently `false`
for every task the engine mints — i.e. every phase after 2 runs against an engine
that denies every UserTask, which will produce a wave of unrelated red tests that
the agents will "fix" by weakening assertions.

I verified the referenced line is in `engine`:

```
$ grep -n "AuthzSpec{" engine/step_nodes.go
```
(see §Appendix A for the exact output)

**Proposed fix.** Reorder: `definition/activity` + `definition/model` field
addition becomes phase 3a (it is additive and does not break `engine`'s compile),
`engine`'s wiring + ctx change becomes 3b, and `model.Validate`'s new **rejection**
lands last (it is the piece that breaks fixtures repo-wide). Alternatively land
`UserTask.OpenEligibility` as a one-line inline controller edit at the head of
phase 3. Either way the phase table's `depends on` column must show the edge
`3 → 4`, which today it denies.

---

## F6 — MAJOR — The 256 KiB variable cap does not stop the attack the bundle measured

**Claim attacked.** ADR-0186 Decision 2: *"the input bound is what stops the CPU
burn. Shipping only the ctx would be a mitigation that looks like one and is not."*
ADR-0186 Decision 1: *"`service.WithMaxVariableBytes`, default **256 KiB** for an
instance's variable map."*

**Evidence — the bundle's own measurement refutes the chosen default.**
Spec §2.8 / ADR-0186 Context 2 measured, for the 44-character predicate
`count(vars.rows, {let x = #; count(vars.rows, {# == x}) == 1}) == len(vars.rows)`:

| n | elapsed |
|---|---|
| 1 000 | 25 ms |
| 2 000 | 98 ms |
| 4 000 | 391 ms |
| 8 000 | 1.563 s |

Clean O(n²) — the bundle says so. Now apply the chosen cap. A JSON array of
integers costs roughly 5–7 bytes per element, so 256 KiB admits on the order of
**40 000–50 000 elements**. Extrapolating the bundle's own quadratic:

- n = 43 000 ⇒ (43000/8000)² × 1.563 s ≈ **45 s**
- n = 50 000 ⇒ (50000/8000)² × 1.563 s ≈ **61 s**

per evaluation, on one goroutine, unpreemptible (the ADR itself says Go cannot
preempt a running expr goroutine, so the ctx does not stop it either). That is
*worse* than the 37.66 s figure the spec inherited and flagged as the motivating
number. The two mechanisms Decision 2 says must ship "both, or neither" ship
together and still admit a ~1-minute CPU burn per request.

**This is an arithmetic contradiction inside the bundle**, not a judgement call —
ADR-0186 already labels 1 MiB / 256 KiB as *"judgement calls, not measurements …
the most likely numbers in this record to be wrong"*, but it does not notice that
its own table already falsifies them.

**Proposed fix.** Bound the axis that was measured, not a proxy for it:
- cap **collection cardinality reachable from the env** (the plan's phase-1
  `WithMaxEnvElements` is the right *shape*) and pick the number from the
  measurement — e.g. 5 000 elements ≈ 40 ms at the measured curve, 10 000 ≈ 150 ms;
- state the target explicitly ("no single evaluation exceeds ~100 ms of CPU at the
  measured curve") so the number is derivable rather than asserted;
- keep `MaxVariableBytes` as a *storage/payload* bound with its own rationale, and
  stop describing it as the CPU mitigation.

---

## F7 — MAJOR — Zombie scope: ADR-0186 D2's "reuse Decision 1's variable cap as the same knob" is never built

**Claim attacked.** ADR-0186 Decision 2, second bullet: *"The evaluator refuses an
env whose bounded size exceeds a configured limit, **reusing Decision 1's variable
cap as the same knob**."*

**Evidence — the plan builds two unconnected knobs, in different packages, in
different units:**

```
plan §3 phase 1 (internal/expreval):  func WithMaxEnvElements(n int) Option
                                       "refuses an env whose bounded element count exceeds n"
plan §3 phase 5 (service):            WithMaxVariableBytes(n int64) Option
                                       "default 256 KiB, refused before persist"
```

`int` **elements** vs `int64` **bytes**; `internal/expreval` vs `service`. Nothing
in any phase plumbs a `service`-level configuration value into the *engine's*
default evaluator (`engine/conditions.go:43` constructs
`expreval.New(expreval.WithTimeout(0))` as a package-level default — I re-read it,
it takes no configuration from `service` at all). Phase 3 touches
`ConditionEvaluator`'s signature, not its construction.

This is exactly the ADR-0162 zombie-scope shape rule #11 exists for: the ADR
promises one knob, the plan builds two halves of two different knobs, and neither
is wired to the other.

**Proposed fix.** Decide which unit is authoritative (elements, per F6), then make
the plan carry the plumbing explicitly: a `service` option that constructs the
engine's `ConditionEvaluator` with the configured bound and passes it via
`engine.StepOptions`/the driver — and name the file and symbol that does it. If
the plumbing is judged too large for this bundle, delete the sentence from
ADR-0186 D2 and say the engine evaluator's input bound is deferred.

---

## F8 — MAJOR — Redaction is placed where a consumer's `InstanceMapper` bypasses it

**Claim attacked.** ADR-0186 Decision 4: *"`httpcore.CustomizeConfig.RedactVariables
func(map[string]any) map[string]any`. `view.go` routes `st.Variables` through it."*

**Evidence.** `RedactVariables` sitting inside `NewInstanceView` is reachable only
when the **default** mapper runs. `CustomizeConfig.InstanceMapper` replaces it
wholesale and receives the **raw** `engine.InstanceState`:

```
transport/http/httpcore/seam.go:26-28
    // InstanceMapper customises the process-instance response shape. nil-safe:
    // ResolveConfig defaults it to NewInstanceView.
    InstanceMapper func(engine.InstanceState) any
transport/http/httpcore/seam.go:41
    InstanceMapper: func(st engine.InstanceState) any { return NewInstanceView(st) },
transport/http/httpcore/endpoints.go:124,140,156
    return http.StatusOK, mapInstance(mapper, pi.State()), nil
```

So the seam CLAUDE.md lists as a *product feature* ("API response customization …
customizing the `ProcessInstance` response shape") silently disables the security
control the same ADR adds. A consumer who sets `InstanceMapper` — the documented,
encouraged path — gets unredacted variables and no diagnostic. The spec notices the
*admin snapshot* gap (§4.4 Option A "Against") but not this one, and ADR-0186 D4
mentions neither.

**Proposed fix.** Apply redaction at the **response boundary**, not inside the
default mapper: redact `st.Variables` in `mapInstance` **before** calling the
mapper (so the mapper never sees the unredacted map), or change the mapper's
signature to receive an already-redacted state. Then state in ADR-0186 D4 that
redaction precedes `InstanceMapper`, and add a plan test
`TestRedactionAppliesUnderCustomInstanceMapper` — which is the control this
decision is missing, and which would otherwise pass vacuously against the default
mapper.

---

## F9 — MAJOR — "The view aliases the engine's map, so mutating the view mutates instance state" was not executed, and is probably false

**Claim attacked.** ADR-0186 Context 4: *"`view.go:31` assigns `Variables:
st.Variables` — an **alias** of the engine's map, not a copy, so anything mutating
the view mutates instance state."* Repeated in spec §2.6 and listed in ADR-0186
Consequences/Positive as *"A live aliasing bug (`view.go:31`) is fixed."*

**Evidence.** The alias half is true (`transport/http/httpcore/view.go:31`,
re-read). The **consequence** half is unverified and contradicted by the read path:

```
persistence/caching_instance_store.go:73-76
    // cloneInstanceEntry deep-copies an entry so cached live values (value-cache
    // substrates) can never be aliased by a caller.
    func cloneInstanceEntry(e instanceEntry) instanceEntry {
        return instanceEntry{State: e.State.Clone(), Version: e.Version}
    }
engine/step_state.go:361-363
    func cloneState(st InstanceState) InstanceState {
        s := st
        s.Variables = copyVars(st.Variables)
service/instance.go:70
    func (p processInstance) State() engine.InstanceState { return p.st }
```

The cached read path hands out a clone whose `Variables` is a fresh map; the
uncached path decodes a fresh snapshot from the row. So the map the view aliases is
a **per-request value**, and mutating it reaches neither the cache nor the
database. The residual defect is a real convention violation (every other escape
boundary in this repo clones — `HumanTask.Clone`, `Actor.Clone`, `ActiveTasks`),
but "mutates instance state" is a claim about current behaviour entered without
execution, in the position that justifies calling this a *live bug*.

I did **not** execute a mutation probe (that needs a running service); the
counter-evidence above is source-verified only. `ASSUMPTION (unverified)` on my
side too — which is the point: neither the bundle nor I may assert it.

**Proposed fix.** Either execute it (construct a `ProcessEngine` over
`persistence` in-memory stores, GET an instance, mutate `view.Variables`, GET
again, print) and record the real numbers per Premise Discipline, or downgrade the
sentence to *"the view aliases the caller's map, violating the repo's
clone-on-escape convention; no path from there to persisted state was
demonstrated"*. The prescribed test (`TestInstanceViewCopiesVariables`) is fine
either way — it is a unit test on `NewInstanceView` and fails today — but the
ADR's severity language must match what was shown.

---

## F10 — MAJOR — There is no decision for "nothing put an actor in the context"

**Claim attacked.** ADR-0185 Decision 1: *"No actor in context ⇒ the zero actor ⇒
any spec that states an eligibility dimension denies. **Fail-closed by
construction.**"*

**Evidence — the construction is fail-closed only for specs that state a
dimension, and Decision 3 exists specifically to create the population that does
not:**

- `AuthzSpec{Open: true}` admits the zero actor by design (plan phase 2 test 2
  asserts exactly this: *"`AuthzSpec{Open: true}` admits the zero actor"*).
- `authz.AllowAll{}` — which Decision 2 keeps as an explicit, supported posture
  via `WithAllowAllAuthorizer()` — admits it unconditionally
  (`authz/authz.go:103-105`).

So after this bundle an **unauthenticated** HTTP caller can claim and complete every
`Open` task, and the audit record it writes is `Actor{ID: ""}` —
`engine/step_triggers.go:587` copies the trigger actor into `task.Claim`
verbatim, and I found **no** empty-actor-ID guard anywhere:

```
$ grep -rn 'Actor.ID == ""|ErrEmptyActor|actor.ID == ""' --include='*.go' . | grep -v _test
(no output)
```

Two consequences the bundle does not state:
1. The completion audit trail degrades from "whatever the body said" (at least a
   string) to "" — ADR-0147's faithful-passthrough audit record becomes
   unattributable for the whole `Open` population.
2. **Decision 5's guard degenerates for that population.** Anonymous claimant has
   `Claim.Actor.ID == ""`; anonymous completer has `t.Actor.ID == ""`; `"" == ""`
   passes. Any anonymous caller may complete any other anonymous caller's claimed
   task. The guard is not merely weak there — it is a no-op.
   `Reassign` is worse: `runtime/task/service.go:229-233` computes
   `claimant = ""` when `task.Claim == nil`, so `from=""` is ambiguous between
   "unclaimed" and "claimed by the anonymous actor".

**Proposed fix.** Make the absent-actor case an explicit decision in ADR-0185 D1:
- default: `httpcore` returns **401** when the resolver reports no actor (i.e.
  `ActorFromContext` returns `ok == false`), rather than manufacturing a zero
  actor. `Open` then means "any *authenticated* actor", which is what a
  deliberately-unrestricted *engine gate* was always supposed to mean — ADR-0117
  Decision 1's own words are that authorization *"defers to the consumer's
  transport layer"*, i.e. it presumes the transport authenticated somebody;
- an explicit `httpcore.WithAnonymousActorAllowed()` opt-in for the demo/example
  posture (the `examples/` mains of phase 11 need it);
- and, independently of the transport choice, reject an empty `Actor.ID` in
  `handleHumanClaimed` / the `Claim` invariant (ADR-0183's neighbourhood), so
  `""` can never become a claimant identity.

---

## F11 — MAJOR — `ActorResolver`'s error path is undefined, which is where a fail-open will be re-introduced

**Claim attacked.** ADR-0185 Decision 1 defines
`WithActorResolver(func(context.Context) (authz.Actor, error))` and says nothing
about what `httpcore` does with a non-nil error. Plan phase 7 repeats the signature
and prescribes no test for it.

**Evidence.** The bundle prescribes exactly six phase-7 tests
(plan §3 phase 7, items 1–6); none covers a resolver error. The default
(`authz.ActorFromContext`) cannot error, so the branch is unexercised by every
prescribed test, and an implementer's cheapest reading — "on error, fall through to
the zero actor" — silently re-creates the anonymous path of F10 for consumers who
adopted the override precisely because they take identity seriously.

This is the "must STILL DO" mirror of the whole bundle: the guard must deny a
forged actor **and** must not turn a transient identity-provider failure into an
`Open`-task free-for-all.

**Proposed fix.** ADR-0185 D1 gains one sentence: *a resolver error is a **500**
(or 503) and the request is refused; it is never downgraded to the zero actor* —
and plan phase 7 gains `TestActorResolverErrorRefusesTheRequest`, whose falsifier
is that the option does not exist today (compile error), with a follow-up
assertion that the status is not 2xx and that `svc.ClaimTask` was never called
(a stub service recording calls; without that second assertion the test passes
against an implementation that refuses *after* acting).

---

## F12 — MAJOR — Strict references meet a task-creation variable snapshot: predicates over later-set variables become permanent denials

**Claim attacked.** ADR-0185 Decision 4: *"evaluation denies when a referenced key
is absent from the env"*, mitigated only by the `in`/`has` guard escape.

**Evidence.** The env's `vars` for a human task is **not** the live process
variable map — it is a snapshot frozen at task creation:

```
humantask/humantask.go (HumanTask.Vars godoc)
   "Vars is a snapshot of the process Variables at task-creation time … set by the
    runtime when an AwaitHuman command is performed and must not be aliased to the
    live process-variable map."
runtime/task/service.go:191-193 (Claim godoc)
   "task.Vars (snapshotted at task-creation by the runner's AwaitHuman perform) are
    forwarded to the Authorizer"
```

So a predicate like `vars.approvedBy != actor.ID` or `vars.escalationTier == 2`,
where the variable is written by a *parallel branch* or a *boundary/timer path*
after the task was minted, references a key that is absent from the snapshot **for
the whole life of the task**. Today: absent ⇒ nil ⇒ frequently allow. Under
Decision 4: absent ⇒ **deny, forever**, with no runtime remedy — the snapshot is
immutable and `RefreshCandidates` refreshes candidates, not `Vars`.

The bundle's Against-list for D3 Option A names only the *optional variable* idiom
(`vars.tier == nil or …`). This is a different and larger class: a predicate that
is **correct today and unfixable after**, because the guard escape (`"k" in vars`)
changes the predicate's meaning rather than restoring it.

**Proposed fix.** Bound the strictness to references the *definition author can
guarantee*: require referenced `vars.*` keys to be present **at task creation**
and fail there (an authoring/creation-time diagnostic with the node id), rather
than at authorization time. Failing that, ADR-0185 D4 must add this class to its
Consequences and the plan must add a phase-2 case
`TestStrictReferencesAgainstTaskCreationSnapshot` with an explicit adjudication of
what a deployment should do about it.

---

## F13 — MAJOR — `Roles` + `Privileges` still silently drops the privilege dimension

**Claim attacked.** ADR-0185 Decision 3: *"A spec whose **only** dimension is
`Privileges` denies under `RoleAuthorizer`."*

**Evidence.** The word *only* is load-bearing and leaves the mixed case fail-open.
`authz/authz.go:119-120` documents `Privileges` as reserved and NOT evaluated by
`RoleAuthorizer`. Under the new rule, a spec
`{Roles: ["manager"], Privileges: ["finance-task approve"]}` is non-empty and
carries an evaluatable dimension, so `RoleAuthorizer` evaluates the roles and
**silently ignores the privilege requirement**: any manager passes a gate the
author wrote to require an explicit approve grant. That is a strictly worse failure
than the privileges-only case the decision does close, because it looks configured.

**Proposed fix.** Deny (or refuse at construction) whenever a spec carries **any**
dimension the authorizer cannot evaluate, not only when it is the sole dimension:
`RoleAuthorizer.Authorize` returns `ErrUnevaluatableSpec`-wrapped
`ErrNotAuthorized` for any non-empty `Privileges`. Add the mixed case to plan
phase 2 test 1's table — as written that table has four rows and none of them is
the mixed shape, so the decision's actual gap is untested.

---

## F14 — MINOR (but it is the plan's own falsifier) — the `handleHumanCompleted` citations are 10 lines off and the prescribed grep window straddles the function

**Claim attacked.** Spec §2.5: *"`engine/step_triggers.go` `handleHumanCompleted`
(declared `:839`)"*; ADR-0185 finding 5: *"`engine/step_triggers.go:839`, write at
`:931-936`"*; plan §3 phase 3b: *"`handleHumanCompleted`
(`engine/step_triggers.go:839`)"* and the falsifier
`awk 'NR>=839 && NR<=960' engine/step_triggers.go | grep -n "Candidates\|Eligibility\|Claim"`.

**Evidence — re-derived in this worktree:**

```
$ grep -n "^func handleHuman" engine/step_triggers.go
577:func handleHumanClaimed(...)
603:func handleHumanCandidatesResolved(...)
630:func handleHumanReassigned(...)
849:func handleHumanCompleted(...)          ← not 839

$ grep -n "Completion = &humantask.Completion" engine/step_triggers.go
941:	task.Completion = &humantask.Completion{
942:		Actor:   t.Actor,                    ← not 931-936 / :932

function body spans 849–973 (next func at 984)

$ awk 'NR>=849 && NR<=973' engine/step_triggers.go | grep -c "Candidates|Eligibility|Claim"
0        ← ZERO hits over the real function

$ awk 'NR>=839 && NR<=960' engine/step_triggers.go | grep -n "Candidates|Eligibility|Claim"
10:// task-lifetime key described on [handleHumanClaimed].   ← absolute line 848, OUTSIDE the function
```

The bundle's window starts 10 lines **before** the function (its single "hit" is a
godoc line belonging to the function's own doc comment, not its body) and ends 13
lines **before** the function does. The conclusion ("no comparison exists") happens
to be right — in fact stronger than claimed, zero hits — but the command the plan
hands to an implementation agent as the RED falsifier does not measure what it says
it measures, and re-running it after any edit above line 849 will drift again.

**Proposed fix.** Replace every line citation of this handler with a symbol
citation plus a range-derived command, e.g.
`awk '/^func handleHumanCompleted/,/^}/' engine/step_triggers.go | grep -n 'Claim'`,
and correct `:839` → `:849` and `:931-936` → `:941-942` in the spec, both ADR
mentions and the plan. (Memory's standing lesson: *an audited bundle decays when
its base moves — prefer symbol names over line numbers.*)

---

## F15 — MINOR — 413 is asserted but never derived; `MaxBytesReader` surfaces as a decode error

**Claim attacked.** ADR-0186 Decision 5's table adds *"413 (new) — static `request
too large`"*; Decision 1 says `service.WithMaxVariableBytes` is *"refused before
persist with a sentinel classified 4xx (413 at the transport …)"*; plan phase 7
test 6 asserts *"one byte over the cap ⇒ 413"*.

**Evidence/gap.** `http.MaxBytesReader` does not produce a status — it makes the
next `Read` fail, which surfaces inside `json.NewDecoder(...).Decode(...)` as an
error the adapters currently classify through the 400 arm
(`transport/http/httpcore/errors.go`, the 400 arm at `:50`). Nothing in the bundle
says how `ClassifyError` recognises it: Go 1.19+ returns `*http.MaxBytesError`
which needs `errors.As`, and gin's `ShouldBindJSON` wraps it again. For fiber the
plan's `len(c.Body())` pre-check produces a different, home-grown error. Three
adapters, three error shapes, one prescribed status, no mapping specified — and
plan phase 10 asks the parity suite to assert *"the 413 status"* across all three.

Related, unstated: a `len(c.Body())` pre-check runs **after** fiber has already
buffered the body, so it rejects but does not prevent the memory amplification that
motivates the cap (fiber's own `DefaultBodyLimit` is what actually prevents it, and
ADR-0186 Context 1 correctly notes that limit *"is the framework's, not ours"*).

**Proposed fix.** ADR-0186 D5 names the mapping explicitly: a new
`httpcore.ErrBodyTooLarge` sentinel; each adapter converts its own oversize signal
(`errors.As(&*http.MaxBytesError{})` for stdlib/gin, the pre-check for fiber) into
that sentinel *before* `ClassifyError`; `ClassifyError` maps the sentinel → 413.
Plan phase 9 gains that conversion as an explicit per-agent deliverable, and
phase 10's parity assertion becomes meaningful. Add one sentence to D1 conceding
that fiber's pre-check is a rejection, not a prevention.

---

## F16 — MINOR — `ErrorBody`'s new correlation id is an unowned wire change and is missing from the breaking-change list

**Claim attacked.** ADR-0186 D5: *"Every error body gains a correlation id, echoed
in the log line."* Plan §3 phase 12 lists exactly four breaking changes: *"the three
task DTOs, `NewProcessEngine`'s new error, the `AuthzSpec` meaning change,
`ConditionEvaluator`'s signature."*

**Evidence/gap.** The correlation id adds a field to an exported response DTO
(`ErrorBody`) on **every** error response of all three adapters, and the bundle
never says where the value comes from: a new random id per response, a propagated
`traceparent`, or the OTel span id (the repo already carries
`TracerProvider`/`MeterProvider` on `CustomizeConfig`, `seam.go:30-31`). Different
answers imply different dependencies (an id generator in `httpcore`, which today
has none) and different testability. It is also absent from the migration list even
though ADR-0186 itself flags `ErrorBody.Message` as *"an exported field consumers
may match on"* — the same argument applies to adding a field.

**Proposed fix.** Decide the source in ADR-0186 D5 (recommendation: reuse the OTel
span id when a span is recording, else a `idgen.Generator`-minted id, so `httpcore`
gains no new dependency); add `ErrorBody` to phase 12's breaking-change list; and
add a phase-7 assertion that the id in the body equals the id in the captured log
record — the whole justification for blanking the 403 is that an operator can join
the two, and no prescribed test checks the join.

---

## F17 — CRITICAL — ADR-0186 D2's `ctx` either breaks the locked determinism invariant or is the no-op it rejects Option C for being

**Claim attacked.** ADR-0186 Decision 2: *"All three `ConditionEvaluator` methods
gain `ctx context.Context` … `Step` already carries a ctx and the engine core
already imports `context`, so core purity (`engine/purity_test.go`, which forbids
OTel and clockwork imports) is unaffected — **a `ctx` is not a wall clock**."*
And its rejection of the alternative: *"A second, optional
`ContextConditionEvaluator` interface was considered and rejected: it leaves the
**default** path — the one every consumer gets — unbounded, which is the entire
problem."*

**Evidence — the ADR answers the *import* question and never the *semantic* one,
and the semantic one is a locked invariant recorded in the very file it edits:**

```
engine/conditions.go:29-43
   // The wall-clock evaluation guard (expreval.WithTimeout, ADR-0049) is explicitly
   // DISABLED here: the engine core must stay wall-clock-free and side-effect-free
   // (locked invariant, ADR-0003), so the default Step never spawns the guard's
   // goroutine/timer.
   //
   // A consumer that needs the DoS guard for in-engine evaluation supplies its own
   // timeout-capable [ConditionEvaluator] via [StepOptions.Evaluator] (ADR-0056);
   // that is an explicit opt-in trading the DETERMINISTIC-REPLAY GUARANTEE for DoS
   // protection.
   var conditions = expreval.New(expreval.WithTimeout(0))
```

`engine/purity_test.go` enforces only two things — no `go.opentelemetry.io`
import, and an AST wall-clock detector over `engine/` + `definition/`. It does
**not** and cannot enforce determinism. So the ADR's purity argument is true and
irrelevant, and the real invariant goes unaddressed. The dilemma:

- If the **default** evaluator honours the ctx, then `Step(ctx, …)` replayed over
  the same state and trigger can now return a different result depending on
  ambient cancellation/deadline — the deterministic-replay guarantee ADR-0003/0056
  locked, and which `conditions`' own godoc says is only ever traded away by
  *explicit opt-in*, is now traded away for **everyone, by default**.
- If the default evaluator **ignores** the ctx (`expreval.WithTimeout(0)` keeps its
  current unguarded behaviour and the new parameter is accepted and discarded),
  then the change is cosmetic for the default path — which is verbatim the reason
  ADR-0186 gives for rejecting Option C.

The ADR must pick one and say so; today it implies the first while arguing only
about imports, and the plan's phase 3a repeats the purity framing
(*"confirm the purity test still passes rather than assuming it"*) without
mentioning determinism at all. The plan's prescribed test
`TestStepCancellationReachesEvaluator` will pass under **either** horn, so it
cannot detect which one shipped.

**Proposed fix.** ADR-0186 D2 gains an explicit sub-decision on the default
evaluator, with ADR-0003/0056 named:
recommended — the ctx reaches the evaluator, the **default** evaluator honours
**cancellation only** (not deadlines it invents), and the ADR states plainly that
`Step` is deterministic *for a non-cancelled ctx* and that a cancelled ctx is a
terminal, non-replayable outcome rather than a routing difference. Then add the
control the current plan lacks: `TestStepIsDeterministicUnderAnUncancelledCtx` —
two `Step`s over the same state/trigger with different (uncancelled) deadlines
produce byte-identical `StepResult`s. Without that control, the horn that shipped
is unobservable. If the sub-decision cannot be made cleanly, Option C plus a
default-path input bound (F6) is the honest fallback, and the rejection paragraph
must be rewritten rather than left contradicting the outcome.

---

## F18 — MAJOR — `runtime` is a public breaking surface of ADR-0186 D2 and has no phase

**Claim attacked.** Plan §2 phase table enumerates twelve phases; the packages are
`internal/expreval`, `authz`, `engine`, `definition/*`, `service`,
`action/httpcall`, `transport/http/httpcore`, `casbinauthz` + `internal/authz/casbin`,
the three transport adapters, `transport/http/parity`, `examples/*`, docs.
**`runtime` appears nowhere.**

**Evidence.** `runtime` exports two options typed on the interface D2 changes:

```
runtime/processdriver_options.go:198  func WithExpressionTimeout(d time.Duration) Option
   "builds a long-lived, timeout-capable expression evaluator"
runtime/processdriver_options.go:217  func WithConditionEvaluator(eval engine.ConditionEvaluator) Option
runtime/processdriver.go:107          conditionEval engine.ConditionEvaluator
```

`WithExpressionTimeout` *constructs* an evaluator internally, so it must be
updated for the new method set; `WithConditionEvaluator` takes a consumer-supplied
one, so it is a **public breaking change** in a root package — and it is not in
phase 12's four-item breaking-change list either (that list names
`ConditionEvaluator`'s signature but not the two `runtime` options a consumer
actually calls).

`runtime` is also where ADR-0185 D5's callers live (`runtime/task/service.go`'s
four `Authorize` sites), so it is the natural home for the spec-shape gate if
F1's option (b) is adopted.

**Proposed fix.** Add a `runtime` phase between phases 3 and 5 (it depends on
`engine`, and `service` depends on it via `WithProcessDriver`,
`service/options.go:39`), covering: the two option updates, ctx threading through
`ProcessDriver`'s eval call sites, and — if F1(b) is chosen — the pre-Authorize
spec gate. Add `runtime.WithConditionEvaluator` to phase 12's breaking list. Note
for fan-out: `runtime` and `runtime/task` are separate packages but
`./runtime/...` as a whole is **not** container-free (memory: standing note), so
the brief must scope its verify command to the container-free subset or state the
Docker requirement.

---

## F19 — MINOR — the `examples/` enumeration is wrong again (the fourth rotted count the plan predicted)

**Claim attacked.** Plan §3 phase 11: *"The **13** `examples/scenarios/*` mains that
call `runtime.WithHumanTasks(...)` do **not** mount HTTP…"*. Plan §0 item 6 tells
the counting lens to *"Assume there is a fourth"* rotted enumeration. It is this one.

**Evidence — re-derived:**

```
$ grep -rln "runtime.WithHumanTasks" examples/ | wc -l          → 16
$ grep -rln "runtime.WithHumanTasks" examples/scenarios/ | wc -l → 12
```

The twelve under `scenarios/` are attribute_authz, boundary_action,
completion_action, input_validation, instance_cancellation, inwait_reminder,
manual_task, message_boundary, reverse_rollback, terminate_end,
usertask_approval, usertask_deadline. The other **four** are
`cache_wiring`, `mysql_wiring`, `production_wiring`, `sqlite_wiring` — the plan's
sentence excludes them by saying "scenarios", but they carry `UserTask`s under
Decision 3 exactly like the scenarios do, and three of them additionally mount
`stdlib.Mount` (spec §6.2, which I re-confirmed: `production_wiring:264`,
`sqlite_wiring:278`, `mysql_wiring:262`).

Blast radius Decision 3 actually has here, re-derived by grepping
`WithEligible*` across `examples/scenarios/*/main.go`: **`manual_task`,
`usertask_approval` and `usertask_deadline` declare UserTasks with no
`WithEligibleRoles`/`WithEligibleExpr`/`WithEligiblePrivileges` at all** — those
are the mains that stop working under Decision 3 and that phase 11 must fix,
plus whatever the four wiring mains declare. The plan says *"Enumerate them
mechanically; do not guess"* and then guesses the count in the same paragraph.

**Proposed fix.** Replace the number with the command
(`grep -rln "runtime.WithHumanTasks" examples/`) and the derived list, and split
the sentence: 12 scenario mains + 4 wiring mains, of which 3 mount task routes.
Name the three eligibility-less scenarios explicitly so the phase-11 agent has a
closed set rather than a search.

---

## Appendix A — commands re-derived in this worktree

```
git log --oneline -1                      → 3f317b63 (bundle commit)
grep -rn "\.Authorize(" --include='*.go' . | grep -v _test | grep -v mock
    → casbinauthz/casbinauthz.go:163, runtime/task/service.go:199,234,255,306   (5 sites, 4 of them the ones the spec names)
grep -n "^func With" service/options.go   → 10 options (spec §2.2 correct), incl. :99 WithActorResolver
grep -n "authz.Actor{" transport/http/httpcore/*.go | grep -v _test
    → endpoints.go:119,132,150            (3 sites, spec correct)
grep -n "^func handleHuman" engine/step_triggers.go
    → 577, 603, 630, 849                  (spec/ADR/plan say 839)
grep -n "Completion = &humantask.Completion" engine/step_triggers.go → 941 (ADR says 931-936)
awk 'NR>=849 && NR<=973' … | grep "Candidates|Eligibility|Claim" → 0 hits
grep -rn 'Actor.ID == ""' --include='*.go' . | grep -v _test → no output (no empty-actor guard)
```
grep -rn "ConditionEvaluator" runtime/ --include='*.go' | grep -v _test
    → processdriver.go:107, processdriver_options.go:204,215,217 (no phase in the plan)
grep -rln "runtime.WithHumanTasks" examples/            → 16 files (12 under scenarios/)
grep -rn "stdlib.Mount" examples/                       → production_wiring:264, sqlite_wiring:278, mysql_wiring:262
sed -n '33,76p' internal/authz/casbin/authorizer.go     → "An empty spec allows"; own expreval.New(); no Open, no strict refs
grep -n "AuthzSpec{" engine/step_nodes.go               → 723 (single construction site; the field `Open` must be set here)
