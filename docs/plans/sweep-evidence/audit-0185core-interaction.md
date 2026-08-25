# ADR-0185-core — rule-#9 audit, INTERACTION lens

Bundle commit `5ce393f4`; worktree `scratchpad/wt-interaction` (bundle present, step 0 ✅).
Lens question: **take the changed decisions pairwise and derive what each does to the
other's premises.** Changed set: **D4 (backlog 103, strict attribute references) and D5
(backlog 124, the claimant guard) REMOVED**; **D1 (backlog 51, actor-in-context),
D2 (backlog 52, required authorizer), D3 (backlog 53, stated eligibility) SURVIVING and
each revised.**

Glossary of the labels used repeatedly below:
- **D1** — the actor travels in `context.Context`; the three task DTOs lose their actor
  fields; 401 unauthenticated, 503 on resolver error.
- **D2** — an authorizer is required at `NewProcessEngine`; `WithAllowAllAuthorizer()` is
  the explicit permissive opt-in; `AuthorizerProvider` is a new optional capability.
- **D3** — `AuthzSpec.Open bool` (zero value denies); a per-dialect `0002` data migration;
  a spec-shape gate hoisted into `runtime/task` and made authorizer-aware via the new
  `authz.DimensionEvaluator` capability.
- **F1 (re-audit)** — the prior round's Critical: an authorizer-BLIND hoisted gate denies
  every `Privileges`-carrying spec including under casbin, emptying D3's own escape hatch.

---

### F1 — D2 × D3: `DimensionEvaluator` does NOT dissolve the contradiction; `WithAllowAllAuthorizer()` still stops meaning allow-all, and the plan's OWN prescribed test proves it

**Severity: CRITICAL**

**The two things that interact:** D2's `service.WithAllowAllAuthorizer()` (the explicit
permissive posture, which must keep meaning "permit everything") × D3's spec-shape gate
hoisted into `runtime/task` above all four `Authorize` sites.

**What each assumes the other provides.** D2 assumes that choosing `AllowAll` yields a
deployment where every human-task verb succeeds — that is the entire content of the
posture it is making explicit. D3 assumes that making the gate *authorizer-aware* via
`DimensionEvaluator` is sufficient to preserve that: spec §1's D2×D3 row says the
contradiction is "Resolved in §4", and spec §5.3's declarations table says `authz.AllowAll`
declaring all three dimensions "**dissolves D2 × D3**; `WithAllowAllAuthorizer()` keeps
meaning allow-all". ADR Decision 3 repeats it: "`AllowAll` declares all three — which is
what keeps `WithAllowAllAuthorizer()` honest".

**Evidence (executed).** I transcribed the plan's Phase-2 Task-8 Step-3 `checkSpecStated`
implementation VERBATIM and ran the plan's Phase-2 Task-8 Step-1 test table against it
(scratch package `zzgateprobe`, `go test -count=1 -run '^TestSpecShapeGate$' -v`,
**EXIT=1**):

```
--- PASS: TestSpecShapeGate/1_states_nothing,_not_open_=>_denied
--- PASS: TestSpecShapeGate/2_explicitly_open_=>_allowed_through_the_gate
--- PASS: TestSpecShapeGate/3_MIXED_roles+privileges_under_RoleAuthorizer_=>_denied
--- PASS: TestSpecShapeGate/4_same_mixed_spec_under_an_all-dimensions_authorizer_=>_allowed
--- FAIL: TestSpecShapeGate/5_AllowAll_is_not_broken_by_the_gate
    Received unexpected error: workflow-runtime: taskservice: workflow-authz:
    not authorized: spec states nothing (set Open, or state a dimension)
```

**Why it matters.** This is not a wording slip, it is a **mechanism error**, and it is the
same shape as the interaction Criticals that killed the previous revision: a fix (the
capability interface) that is correct for the decision it was written for, and blind to
what the other decision needed. `checkSpecStated` has **two legs**, and
`DimensionEvaluator` reaches only one of them:

- the *unevaluatable* leg (`if !EvaluatesDimension(...) return ErrUnevaluatableSpec`)
  consults the authorizer — this is the leg that fixes re-audit F1, and case 4 proves it
  works;
- the *states-nothing* leg (`if spec.Open || stated { return nil }; return
  ErrSpecStatesNothing`) **never consults the authorizer at all**. No declaration any
  implementation can make changes its outcome. Declaring all three dimensions is
  vacuous here, because with an empty spec the loop `continue`s on all three arms and
  `stated` stays false regardless of what `az` declares.

So under `WithAllowAllAuthorizer()` an empty-spec task is denied **before
`AllowAll.Authorize` is ever called** — verbatim the failure spec §1 predicted for
authorizer-blind hoisting and claimed §4 had resolved. The population this hits is not
exotic: it is every task whose spec came from a source the `0002` migration does not
touch — a consumer-implemented `TaskStore` (ADR-listed residual 2), `MemTaskStore`, any
`authz.AuthzSpec{}` literal in consumer code or a table test — i.e. exactly the population
D3 §5.1 argues the `bool` (rather than `*bool`) exists to serve. The permissive posture
that D2 promises to keep supported is unreachable for them.

**Concrete proposed fix.** Choose one and state it in all three documents; do not leave
the contradiction adjudicated by prose:

1. *(preferred, smallest)* Give the states-nothing leg an authorizer-aware escape too, via
   a second, explicit capability rather than by overloading `DimensionEvaluator` — e.g.
   `authz.UnstatedSpecAdmitter interface { AdmitsUnstatedSpec() bool }`, implemented
   `true` by `AllowAll` alone, default `false`. Then case 5 passes *because AllowAll says
   so*, not because a dimension declaration accidentally leaks into an unrelated leg.
2. Or: place the states-nothing check **inside** the authorizer contract after all (which
   audit 1 rejected) — not recommended, it re-opens the hoisting rationale.
3. Or: **delete test case 5 and admit the consequence in the ADR** — "`AllowAll` no longer
   means allow-all for an unstated spec; the migration and `model.Validate` are what
   guarantee no unstated spec exists in a *store-backed* deployment, and consumers with
   their own `TaskStore` must set `Open: true` explicitly." This is a real option but it
   is a **behaviour change to a public, documented posture** and currently appears nowhere
   in Consequences/Negative.

Whichever is chosen, spec §1's D2×D3 row ("Resolved in §4"), spec §5.3's declarations
table ("dissolves D2 × D3"), and ADR Decision 3's "keeps `WithAllowAllAuthorizer()`
honest" must all be rewritten — today all three assert something the prescribed code does
not do.

---

### F2 — D3's migration × D3's wire format × ADR-0167: the migration text prescribes ONE JSON key for THREE copies that use TWO different spellings, and the wrong one in the definitions copy makes the whole definition UNLOADABLE

**Severity: CRITICAL**

**The two things that interact:** D3's `0002_*.sql` data migration ("backfills `"Open":
true` … across all three durable copies") × the wire encodings of the three durable copies
(spec §2.1), one of which is governed by **ADR-0167 strict definition decoding**
(`DisallowUnknownFields` inside `ProcessDefinition.UnmarshalJSON`,
`definition/model/node_wire.go:186-191`).

**What each assumes the other provides.** The migration assumes the three copies share a
key spelling — spec §5.2 and ADR Decision 3 both say, verbatim, that it backfills
`"Open": true` across all three. The wire format assumes nothing of the sort: copies 1 and
2 serialize `authz.AuthzSpec`, which carries **no JSON tags**, while copy 3 serializes
`model.NodeWire`, whose eligibility fields are snake_case-tagged (`eligible_roles`,
`eligible_privileges`, `eligible_expr`), and the ADR itself elsewhere states the new wire
key is `eligible_open`.

**Evidence (executed** — worktree with `authz.AuthzSpec.Open bool` and
`model.NodeWire.EligibleOpen bool \`json:"eligible_open,omitempty"\`` added exactly as the
plan prescribes; `go test -count=1 -v ./zzkeyprobe/`, **EXIT=0**, both tests `--- PASS`**):**

```
COPY1 human_task.eligibility = {"Roles":null,"Privileges":null,"Attribute":"","Open":true}
COPY2 snapshot task          = {"TaskID":"t1",…,"Eligibility":{…,"Open":true},…}
COPY3 definition (roles)     = {…"nodes":[…{"id":"t1","kind":"userTask","eligible_roles":["manager"]}…]}

LOAD eligible_open (the ADR's stated wire key)                  -> err=<nil>
LOAD "Open" (what the spec/ADR migration text literally says)   -> err=json: unknown field "Open"
LOAD "open" (what the plan's Task 3 godoc + Task 5 comment say) -> err=json: unknown field "open"
```

So the migration needs **`"Open"`** for `wrkflw_human_task.eligibility` and
`wrkflw_instances.snapshot`, and **`eligible_open`** for `wrkflw_definitions.definition`.

**Why it matters.** Three compounding problems, and the third changes the failure class:

1. The bundle states one key for all three copies. An implementer following the ADR
   literally writes `"Open"` everywhere.
2. The bundle names **three** different spellings for the same field across its own
   documents — ADR/spec migration text `"Open"`; ADR Decision 3 / plan File-Structure /
   plan Task 5 *Interfaces* `eligible_open`; plan Task 3's prescribed godoc (*"wire key
   `open`"*) and plan Task 5 Step 1's test comment (*"NodeWire gains `Open bool
   json:"open,omitempty"`… marshals to `"open":true`"*) say `open`. Plan Phase 5 Task 14
   Step 1 seeds a fixture keyed on *"no `open` key"*. An implementer cannot satisfy all of
   them.
3. **ADR-0167 turns the mistake from a degradation into an outage.** In copies 1 and 2 a
   wrong key is silently ignored (plain `json.Unmarshal`, no strictness) — the task simply
   stays closed, which is the stranding the migration exists to prevent. In copy 3 the
   wrong key is a **hard decode error on the whole `ProcessDefinition`**, so every stored
   definition the migration touched stops loading: no minting, no rehydration, no
   `Lookup`. That is strictly worse than not running the migration at all, and it is
   irreversible-in-place (the rows are already rewritten). Note the direction of the
   interaction: ADR-0167 is cited in this bundle **only** as a reason to prefer a
   snake_case key for *readability* (plan Task 5 Interfaces); nowhere does the bundle
   observe that strict decoding makes a mis-keyed migration catastrophic rather than inert.

**Concrete proposed fix.**
- State the key **per copy** in ADR Decision 3 and spec §5.2, with the executed marshal
  output above as the evidence: `"Open"` for `wrkflw_human_task.eligibility` and for
  `InstanceState.Tasks[].Eligibility` inside `wrkflw_instances.snapshot`;
  **`eligible_open`** for the `nodes[]` entries inside `wrkflw_definitions.definition`.
- Fix the two `open`-spelled sites in the plan (Task 3's godoc block, Task 5 Step 1's
  comment) and Task 14 Step 1's fixture description; then `grep -n '"open"' ` the bundle,
  per the ADR-0187 lesson that round 1 fixed each value where it was *defined* and left
  every place it was *consumed*.
- Add to Phase 5 Task 14 a **post-migration load test** that reads every migrated
  definition row back through `DefinitionStore.GetDefinition` (not through raw JSON) and
  asserts no decode error — the only assertion that can catch a mis-keyed definitions
  backfill, and one the currently-prescribed test (which asserts `Open == true` on a task
  row) cannot.
- Consider making the definitions backfill **not** a SQL string edit at all: a Go
  data-migration that decodes → sets `EligibleOpen` → re-encodes is immune to this whole
  class, at the cost of leaving goose's per-dialect SQL for the two flat copies only.

---

### F3 — D2 × D3: D2's carve-out "no human tasks configured needs no authorizer" describes a configuration that DOES NOT EXIST; every `ProcessEngine` is a live human-task engine, so the carve-out leaves backlog 52 open exactly where D3's gate then breaks it

**Severity: CRITICAL**

**The two things that interact:** D2's constructor guard, scoped to "**when human tasks are
configured**" (ADR Decision 2; plan Phase 1 Task 4 case 4 — *"no human tasks configured
needs no authorizer"*, `opts: nil` ⇒ `require.NoError`) × D3's hoisted gate, which runs on
**every** `runtime/task` verb of **every** engine regardless of how it was configured.

**What each assumes the other provides.** D2 assumes `engineConfig` can tell a
human-task deployment from a non-human-task one, so it can keep the permissive default for
the latter. D3 assumes the authorizer that reaches `runtime/task` is one a consumer chose —
that is the whole justification for hoisting the gate above it ("so a consumer's own
`Authorizer` inherits it").

**Evidence (executed).** `service/options.go:77-86` — `WithHumanTasks` writes only
`c.taskStore` and `c.authz`; it sets **no marker**, so `engineConfig` carries no predicate
for "human tasks are configured". `service/service.go:189-191` defaults `c.taskStore` to
`humantask.NewMemTaskStore()` in the non-durable path, `:199-200` defaults `c.authz` to
`authz.AllowAll{}`, and `:217` builds a `TaskService` **unconditionally**. The durable path
*requires* a task store (`:302 case c.taskStore == nil`). Probe
(`go test -count=1 -run '^TestZeroOptionEngineHasALiveHumanTaskSurface$' -v ./zzsvcprobe/`,
**EXIT=0**, `--- PASS`):

```
ClaimTask on a zero-option engine -> err=workflow-service: claim task: workflow-runtime:
  taskservice: get task: workflow-humantask: task not found
  is ErrTaskNotFound (i.e. the surface is LIVE, it just looked)? true
```

`service.NewProcessEngine()` with **zero options** returns an engine whose `ClaimTask`
reaches the store and reports "task not found" — it is a fully functional human-task
engine backed by `MemTaskStore` and `authz.AllowAll{}`.

**Why it matters.** The carve-out is not a narrowing of the guard, it is a **hole the size
of the default constructor**, and D2 is the decision whose entire purpose is closing
"the default authorizer permits everything" (backlog 52). Any consumer who does not call
`WithHumanTasks` — which is every consumer using the in-memory default, i.e. the shape in
`examples/readme_quickstart` and most of `examples/scenarios` — gets the pre-ADR-0185
silently-permissive engine, and `NewProcessEngine` returns no error because, by D2's own
predicate, "no human tasks are configured". The two decisions then fail in **opposite
directions in the same deployment**: D2 leaves it allow-all (hole open), while D3's gate
denies its unstated specs (per F1, since the states-nothing leg ignores the authorizer) —
so the consumer gets neither the security D2 promises nor the working allow-all D3 claims
to preserve.

Note also that plan Task 4 case 4 is labelled *"the **regression guard** for the narrowing:
it passes today and must keep passing"*. It will pass — and passing is the defect. A test
whose author believes it guards a narrowing, but which actually pins the hole open, is
worse than no test.

**Concrete proposed fix.**
1. Add an explicit marker to `engineConfig` — `humanTasksConfigured bool`, set by
   `WithHumanTasks`, `WithTaskStore` (if one exists) and by the durable path's provider
   task store — and scope D2's error to it. Then state in the ADR, in one sentence, what
   the default-`MemTaskStore` engine does.
2. **Decide and record the zero-option posture explicitly**, because it is now a decision
   and not an accident. The two honest options: (a) `NewProcessEngine()` with no
   authorizer **errors** even for the default `MemTaskStore` — maximally consistent with
   D2's thesis, and breaking for every existing zero-option consumer, so it needs a
   Consequences/Negative line and an examples sweep; or (b) the default in-memory engine
   keeps `AllowAll` but is **WARN-logged by the same record D2 adds**, and the ADR states
   plainly that backlog 52 stays open for it. Silence is not an adjudication.
3. Rewrite plan Task 4 case 4 either way: as written it asserts `NoError` for a
   configuration the ADR believes is out of scope and the code says is a live human-task
   engine.

---

### F4 — D1 × removed D5: the re-derived empty-`Actor.ID` rationale is about the AUDIT RECORD, but the rule is scoped to the CLAIM PATH, so it serves nothing it claims to; and D1's own anonymous opt-in falsifies its second premise

**Severity: MAJOR**

**The two things that interact:** D1's rule *"an empty `Actor.ID` is rejected as a claimant
identity **in the claim path**"* × the **removal** of D5 (backlog 124, the claimant guard
on completion), which is where the rule's original justification lived.

**What each assumes the other provides.** The rule's re-derived rationale, stated
identically in spec §3 and ADR Decision 1, has two legs: (i) *"the audit trail must not
record `""` as an actor"* / *"as a completer"*, and (ii) *"under the 401 rule a caller that
reached the handler is authenticated and therefore has an ID"*. Leg (i) assumes some other
part of the bundle stops `""` reaching the durable record; under the old bundle that was
D5. Leg (ii) assumes every caller reaching a task handler is authenticated.

**Evidence.**
- **Leg (i) is unserved by the rule's own scope.** The audit records are written on
  *three* paths, not one: `engine/step_triggers.go:587`
  (`task.Claim = &humantask.Claim{Actor: t.Actor, …}`), `:643`
  (`task.Claim = &humantask.Claim{Actor: authz.Actor{ID: t.To}}` — the **reassign** path,
  whose actor ID is the request's `to` string, never validated non-empty), and `:941`
  (`task.Completion = &humantask.Completion{Actor: t.Actor, …}` — the **completion**
  record, which is what the rationale's word *"completer"* actually names). A guard on the
  claim path touches exactly one of the three. `Complete` with a zero actor still writes
  `Completion{Actor:{ID:""}}`.
- **The durable invariant does not catch it either.** `humantask/validate.go:40-56` —
  ADR-0183's claim invariant — checks only `State`↔`Claim`-presence consistency
  (`Claimed` requires a claim; `Unclaimed` must not carry one). It does **not** check
  `Claim.Actor.ID != ""`. So nothing at the store boundary enforces the property leg (i)
  asserts, and the ADR's own residual 1 says `ProcessDriver.ApplyTrigger` and
  `engine.NewHumanCompleted` reach these writers while bypassing `runtime/task` entirely.
- **Leg (ii) is falsified by D1 itself.** `httpcore.WithAnonymousActorAllowed()` — added by
  the same decision — exists precisely so a caller who authenticated with nothing reaches
  the handler. Under it, "a caller that reached the handler is authenticated" is false by
  construction.
- **Empty IDs are already load-bearing elsewhere, in the opposite direction.**
  `runtime/task/service.go:227-233`: `Reassign` compares `from` against
  `claimant`, where `claimant` is `""` for an unclaimed task — so `from == ""` is the
  *supported* way to reassign an unclaimed task. A rule that treats `""` as "no identity"
  must not be extended to `from`/`to` without noticing this.

**Why it matters.** This is the removal-grid row the spec claims to have re-derived
(§1, D1 × D5: *"The rule **survives on re-derived grounds**"*). The re-derivation is not
sound: it justifies a guard on the **completion/audit-record** boundary and then places
one on the **claim verb**, where it prevents no record the rationale objects to. That is
exactly the "dangling citation to a decision not in the bundle" failure the same grid row
says it is avoiding — with the citation laundered into a rationale rather than removed.

**Concrete proposed fix.** Pick one and say which:
1. **Keep the rule and fix its scope**: reject an empty `Actor.ID` on *all three*
   actor-bearing verbs (`Claim`, `Complete`, and `Reassign`'s `by`), in `runtime/task`
   where the gate already sits, so `ApplyTrigger`'s bypass is the only remaining hole and
   is already a stated residual. Leave `Reassign`'s `from` alone and say why
   (`from == ""` means "currently unclaimed").
2. Or **drop the rule from this bundle** and record it as part of the deferred backlog-124
   work, where the completion-side guard it actually serves will live. This is honest and
   costs nothing D1 needs.
3. Either way, **delete leg (ii)** from spec §3 and ADR Decision 1 — "a caller that
   reached the handler is authenticated" is false wherever `WithAnonymousActorAllowed()`
   is set, which is the configuration the ADR itself prescribes for all three `examples/`
   mains.

---

### F5 — D3's authoring gate × D1's anonymous opt-in × Phase 6's build-only verification: `Open` stops meaning "authenticated", and the blast-radius set the plan names is wrong in both directions

**Severity: MAJOR**

**The two things that interact:** D3's `model.Validate` rejection of an unstated
`UserTask` (and D3's semantics for `Open`) × D1's `httpcore.WithAnonymousActorAllowed()`
and the plan's Phase 6, whose only stated verification is `go build ./examples/...`.

**What each assumes the other provides.** D3 assumes `Open` means *"any **authenticated**
actor"* — stated verbatim in spec §3 and ADR Decision 1, and used as the argument that the
new `Open` marker is not a fail-open. D1's anonymous opt-in assumes it is safe to let an
unauthenticated caller through, because *something downstream* still authorizes. Phase 6
assumes a compile is enough to prove the examples still work.

**Evidence.**
1. **`Open` + anonymous is an unauthenticated allow-all.** Under
   `WithAnonymousActorAllowed()` the handler proceeds with (necessarily) a zero or
   synthesized actor. A task whose spec is `{Open: true}` passes D3's gate and passes both
   `AllowAll.Authorize` and `RoleAuthorizer.Authorize` (`authz/authz.go:124-131`: with
   `len(spec.Roles) == 0` the role check is skipped). So in exactly the deployments the
   opt-in exists for, `Open` means "anyone on the network", not "any authenticated actor".
   The bundle never states what actor the opt-in yields — an omission that has to be
   closed before the semantics can even be argued.
2. **The plan's blast-radius set is wrong in both directions.** Plan Task 5 says *"Expect
   to fix fixtures in `engine/`, `runtime/`, `processtest/` and `service/`"*. Derived
   (balanced-paren scan over every `NewUserTask(`/`AddUserTask(` call in the repo,
   classifying each by whether the call carries any `WithEligible*` option):

   ```
   TOTAL NewUserTask/AddUserTask call sites : 280
   WITHOUT any WithEligible* option         : 130
     engine     91
     definition 28
     runtime     9
     examples    2
   ```

   `processtest` (21 sites) and `service` (6 sites) have **zero** bare sites — every one
   already states a dimension — while **`definition` (28)**, the second-largest group, and
   **`examples` (2)** are absent from the plan's list. (Not every bare site reaches
   `model.Validate`; the *set of packages* is what is wrong, and it is wrong in both
   directions.)
3. **`examples/scenarios/manual_task` dies at run time and the plan's gate cannot see it.**
   `examples/scenarios/manual_task/main.go:45-46` builds two user tasks with no
   eligibility and no open marker (`activity.WithManual(false)` / `(true)` are
   completion-mode options, not eligibility). `definition/model/builder.go:133` calls
   `Validate(&def)` inside `Build()`, and the main does `log.Fatal` on the error — so after
   D3 this example **fails on the first line it runs**. `find examples -name '*_test.go'`
   returns exactly one file (`examples/migrate/main_test.go`), and no script or workflow
   references `examples/`, so nothing executes it. Phase 6's `go build ./examples/...`
   compiles fine. Phase 6 Task 16 touches only the three `*_wiring` mains and never
   mentions `examples/scenarios`.
4. **Minor, same interaction:** the three `*_wiring` mains that Task 16 *does* change
   contain **no user task at all** — each builds `start → NewServiceTask("charge") → end`
   (`production_wiring:189-195`, `sqlite_wiring:217-223`, `mysql_wiring:201-207`). They
   mount the task routes but never mint a human task, so the anonymous opt-in the ADR says
   they "need" changes only the status code of a route nothing exercises. The example that
   would genuinely demonstrate the identity seam is the one Task 16 does not touch.

**Why it matters.** The combination ships a public example that cannot start, and a
documented `Open` semantics that the bundle's own opt-in contradicts. Both are invisible to
every check the plan prescribes.

**Concrete proposed fix.**
- **Specify what `WithAnonymousActorAllowed()` yields** — the zero actor, or a named
  sentinel such as `authz.Actor{ID: "anonymous"}` — and state the consequence for `Open` in
  ADR Decision 3 in one sentence: *"with the anonymous opt-in set, `Open` means any caller;
  the opt-in is demo-only and `SECURITY.md` says so."* Then reconcile with F4: if `Claim`
  rejects an empty ID, a zero-actor anonymous caller cannot claim, and the opt-in is
  self-defeating — pick the sentinel.
- **Re-derive the blast radius by package before implementation** and put the real list in
  the plan (`definition` and `examples` included); the numbers above are a starting point,
  not a substitute.
- **Fix `examples/scenarios/manual_task`** in Phase 6 (add `WithOpenEligibility()` to both
  tasks — it is the intended semantics there) and add a Phase 6 step that *runs* the
  scenario mains, or at minimum `go vet` plus a `Build()`-only smoke test, since a compile
  cannot see a `Validate` failure.
- Point the production example at the real shape: give `production_wiring` a user task, so
  the commented authentication-middleware example the plan asks for has something to guard.

---

### F6 — D3's migration × ADR-0187 D11: the bundle predicted the WRONG ADR-0187 defect. A data-only `0002` does not "silently derive no keyed fact" — ADR-0187's parser HARD-ERRORS on `UPDATE`, taking 14 tests and `SECURITY.md` generation down with it

**Severity: CRITICAL**

**The two things that interact:** D3's per-dialect `0002_*.sql` **data** migration (pure
`UPDATE`/backfill; no DDL) × **ADR-0187 D11**, the fail-closed migration parser
(`internal/atrest/schema.go:98-139`), whose `default` arm returns
`workflow-atrest: unrecognised statement` for anything that is not `CREATE TABLE`,
`CREATE INDEX` or `CREATE UNIQUE INDEX`.

**What each assumes the other provides.** D3 assumes the only ADR-0187 interaction is the
**parked, silent** one: spec §2.7, ADR Consequences and plan Task 15 all say this bundle
"**activates ADR-0187's parked latent defect** — a `CREATE INDEX` naming a table declared
in a *different* migration file derives no `keyed` fact silently". ADR-0187 D11 assumes
that any statement it cannot parse is a *schema* statement whose columns would otherwise
go unclassified, and therefore refuses to continue — its own comment names "a future
`ALTER TABLE`" as the anticipated case. Neither side anticipated a **data** migration.

**Evidence (executed).** I wrote the real file
`internal/persistence/store/migrations/sqlite/0002_authz_open.sql` (a `json_set` backfill
with a `json_remove` down) into the worktree and ran the suite:

```
go test -count=1 ./internal/atrest/...          EXIT=1     15 FAILs
  … --- FAIL: TestRender / TestSecurityMdInSync / TestLoadSchemas_KeepsTheMySQLDeclaredColumnName
      TestKeyedIsDialectDependent / TestNormalizedKeySetAgreesAcrossDialects
      TestLoadSchemas_ColumnCensus / TestKeyedLowerBound_Postgres
      TestClassificationCoversTheSchemaExactly / TestKeyedCountPerDialect
      TestRenderStructureIsDerivedNotRetyped / TestRenderIsDeterministic
      TestRenderCasbinAbsenceViolationsAreDeterministic
      TestRenderPolicyLocationsAreDerivedAndChecked
      TestRenderCasbinPolicyColumnsAreDerivedNotRetyped        (14 from this file)
  error: workflow-atrest: parse …/sqlite/0002_authz_open.sql:
         workflow-atrest: unrecognised statement: UPDATE wrkflw_human_task SET eligibility = …
```

Removing that one file drops the suite to a single unrelated failure (F7), so the
attribution is exact: **the data migration alone breaks 14 tests.** Direct unit evidence:
`atrest.ParseSQL` returns `unrecognised statement` for the postgres, mysql **and** sqlite
forms of the backfill (`go test -v ./zzatrest/`, EXIT=0, `--- PASS`), and
`internal/atrest/discover.go:300-317` `mergeSQLFilesInto` propagates the parse error rather
than skipping the file. Separately confirmed the *predicted* defect is real but
**inapplicable**: `ParseSQL("sqlite", "CREATE INDEX idx_t_b ON t (b);")` alone yields
`err=<nil>, cols=0` — the keyed fact silently vanishes — but a backfill contains no
`CREATE INDEX`, so this bundle does not activate it at all.

**Why it matters.** Phase 5 Task 15 Step 1 tells the implementer to *"run
`scripts/gen-at-rest.sh`, confirm it round-trips with a clean tree, and if the new file
trips the defect, fix it here."* The implementer will instead find the entire at-rest
package red and the generator unable to run — a **blocking, whole-package failure** that
the plan budgets one checkbox for, described as a different defect with a different fix.
This is the cross-decision blindness the interaction lens exists to catch: the bundle
reasoned carefully about ADR-0187 and reasoned about the **wrong clause of it**.

Note the second-order point: ADR-0187 D11 is *correct* to fail closed on unknown DDL. The
right resolution is not to weaken it into a silent skip, or the next `ALTER TABLE` becomes
invisible — exactly the harm D11 was written for.

**Concrete proposed fix.**
1. Teach `ParseSQL` a **third recognised class: statements that provably declare no
   column** — `UPDATE`, `INSERT`, `DELETE` — matched by keyword prefix and skipped
   deliberately, with a comment tying the exemption to ADR-0187 D11's rationale (a DML
   statement cannot introduce an unclassified column, which is the harm D11 guards). Keep
   the `default` arm fail-closed for everything else, `ALTER TABLE` included. Add a test
   pinning that `ALTER TABLE` still errors, so the exemption cannot widen.
2. Replace the spec §2.7 / ADR Consequences / plan Task 15 Step 1 text: the activated
   interaction is **D11's fail-closed parser meeting a DML migration**, not the
   `CREATE INDEX` cross-file defect. Keep a one-line note that the cross-file `keyed`
   defect remains parked and unactivated, with the executed evidence above.
3. Re-scope Task 15: it is now a change to `internal/atrest/schema.go` with its own RED,
   sitting **before** Task 14 in the phase order (Task 14 cannot go green while the
   at-rest suite is red), not a verification checkbox after it.

---

### F7 — D3's new wire field × ADR-0187's `PolicyAtRestLocations`: adding `EligibleOpen` to `NodeWire` breaks an ADR-0187 guard on its own, and the bundle never mentions the coupling

**Severity: MAJOR**

**The two things that interact:** D3's `model.NodeWire.EligibleOpen` field (plan Phase 1
Task 5) × ADR-0187's `atrest.PolicyAtRestLocations`, whose `wrkflw_definitions.definition`
entry **enumerates NodeWire's eligibility JSON keys in its `Detail` prose**
(`internal/atrest/classification.go:74-80`), policed by
`TestDefinitionEligibilityFieldsAreTheDeclaredSet`.

**What each assumes the other provides.** D3 assumes adding a wire field is local to
`definition/model`. ADR-0187 assumes the eligibility key set is stable enough to be named
in published prose, and installed a guard to notice if it is not.

**Evidence (executed).** With **only** `model.NodeWire.EligibleOpen bool
\`json:"eligible_open,omitempty"\`` added and no migration file present,
`go test -count=1 ./internal/atrest/...` → EXIT=1, exactly one failure:

```
--- FAIL: TestDefinitionEligibilityFieldsAreTheDeclaredSet
    expected: ["EligibleExpr eligible_expr,omitempty", "EligiblePrivileges …", "EligibleRoles …"]
    actual  : [… + "EligibleOpen eligible_open,omitempty"]
    Messages: NodeWire's eligibility fields changed; PolicyAtRestLocations'
              wrkflw_definitions.definition entry names these by JSON key and must be re-derived
```

**Why it matters.** Three things follow, none of them in the bundle:

1. **Phase 1 Task 5 cannot go green without editing `internal/atrest`** — a package it
   does not list, owned by a *different* phase (Task 15) and a different agent. Under the
   plan's fan-out rule ("fan out by Go package"), the Task 5 agent will hit a red test in
   a package outside its brief and has no instruction for it. Sequence this explicitly.
2. **`SECURITY.md` becomes materially wrong the moment `Open` ships** unless the `Detail`
   is updated: the published sentence tells operators that `eligible_roles`,
   `eligible_privileges` and `eligible_expr` are the eligibility rules inside the
   definition JSON. After D3, `eligible_open` — the field that decides whether a task is
   open to *every* authenticated actor — is also in there and is *not* named. That is the
   same undercount as backlog 141, introduced by this bundle rather than inherited.
3. **It bears directly on the backlog-141 scope question.** The bundle asks whether fixing
   141 (adding `wrkflw_instances.snapshot` to `PolicyAtRestLocations`) is in scope. This
   finding settles half of it: the bundle **must** touch `PolicyAtRestLocations` anyway,
   because its own field change breaks the guard on that entry. The marginal cost of also
   adding the snapshot entry is now small, which strengthens the case for taking 141 here.
   Note the guard that catches *this* is a NodeWire-shape guard, not the ClassPolicy
   completeness guard spec §2.2 discusses — the two are different mechanisms and the spec
   should not be read as saying the entry is unguarded.

**Concrete proposed fix.**
- Add to plan Task 5 an explicit step: update the `wrkflw_definitions.definition` `Detail`
  in `internal/atrest/classification.go` to name `eligible_open`, and update
  `TestDefinitionEligibilityFieldsAreTheDeclaredSet`'s expected set, in the same change as
  the field — with the reason (the guard exists precisely to force this).
- Move all `internal/atrest` work — this, backlog 141, and F6's parser change — into **one
  agent's brief**, since three phases otherwise touch the same package concurrently, which
  the plan's own fan-out rule forbids.
- Regenerate `SECURITY.md` once, at the end, and state in ADR Consequences that the
  published policy-location prose changes in two ways: the new `eligible_open` key, and
  (if 141 is taken) the location count 3 → 4.

---

### F8 — D2 × the durable/production wiring topology: there are TWO authorizer slots, and the one the production example wires into `runtime.WithHumanTasks` is DEAD — `ProcessDriver.authz` is assigned and never read

**Severity: MAJOR**

**The two things that interact:** D2's thesis — *"constructing a `ProcessEngine` without an
authorizer is an error"*, i.e. the authorizer is the thing a consumer must consciously
supply × the actual durable wiring shape, in which a human-task deployment must build a
`runtime.ProcessDriver` **separately** and hand it a **second** authorizer.

**What each assumes the other provides.** D2 assumes `service`'s `c.authz` is *the*
authorizer of a deployment. The durable path assumes the opposite: `WithDurableStore`'s
own godoc (`service/options.go:150-168`) says the driver it builds *"does **not** arm
human-task nodes or a scheduler. For a durable graph whose processes use human tasks …
supply a fully-wired `*runtime.ProcessDriver` via `WithProcessDriver` (built with
`runtime.WithHumanTasks`/`WithScheduler`)"* — and `runtime.WithHumanTasks(resolver, tasks,
az authz.Authorizer)` (`runtime/processdriver_options.go:93-99`) takes an authorizer of its
own.

**Evidence.** `examples/production_wiring/main.go` — the reference durable shape — supplies
the authorizer **twice**: `:214 runtime.WithHumanTasks(resolver, taskStore, az)` and
`:256 service.WithHumanTasks(taskStore, az)`. And the first one does nothing:

```
grep -rn "\.authz" --include='*.go' runtime/ | grep -v _test.go
  runtime/processdriver_options.go:97:    driver.authz = az      <- the only write
  runtime/task/service.go:199,234,255,306: s.authz.Authorize(…)  <- a DIFFERENT struct
```

`ProcessDriver.authz` (`runtime/processdriver.go:41`) has exactly one assignment and
**zero reads** in the whole non-test tree.

**Why it matters for this bundle specifically.**
1. D2's Context finding 2 says *"the natural durable wiring lands on allow-all silently"*.
   The remedy is scoped to `service`, so a consumer who correctly reads `WithDurableStore`'s
   godoc, builds their driver with `runtime.WithHumanTasks(..., myAuthorizer)`, and passes
   it via `WithProcessDriver` has supplied an authorizer to a **dead slot** — and if they
   did not *also* call `service.WithAuthorizer`, D2's new constructor error fires on a
   configuration where the consumer believes they supplied one. The error message must
   therefore name *which* slot, or it will read as a false positive to exactly the
   most-careful consumer.
2. Conversely, if D2's error does **not** fire (they also called `service.WithHumanTasks`),
   the two slots can hold **different** authorizers with no diagnostic — one live, one
   inert. D3's gate then consults only the live one, so a consumer's dimension
   declarations on the driver-side authorizer silently do not apply.
3. This also bears on D2's `AuthorizerProvider`: adding a *third* place an authorizer can
   come from, while a second one is dead, is how a "required authorizer" ends up ambiguous.

**Concrete proposed fix.**
- Decide the fate of `ProcessDriver.authz` **in this bundle**, since D2 is the decision
  that makes "where does the authorizer come from" load-bearing. Either (a) delete the
  parameter from `runtime.WithHumanTasks` (BREAKING, but it is a public option whose third
  argument does nothing — the honest fix, and it forces every consumer to the one real
  slot), or (b) keep it and document it as reserved with a `//nolint`-style justification,
  and add a test pinning that it is unread so it cannot quietly acquire a second meaning.
  Record whichever in ADR Consequences and add backlog entry for the other.
- Make `service.ErrAuthorizerRequired`'s message name the option the consumer must call
  (`service.WithAuthorizer` / `service.WithAllowAllAuthorizer`), so a consumer who supplied
  the driver-side one is told where the real slot is.
- Fix `examples/production_wiring` to stop implying the driver authorizes.

---

### F9 — D2's `AuthorizerProvider` × `WithDurableStore`'s last-writer-wins: the precedence of the NEW seventh provider leaf is unspecified, and the default it inherits can silently REPLACE an explicitly-supplied authorizer

**Severity: MAJOR**

**The two things that interact:** D2's new `AuthorizerProvider` capability, type-asserted at
wiring time × `WithDurableStore`'s documented precedence rule, which D2 deliberately
declines to change.

**What each assumes the other provides.** `AuthorizerProvider` assumes there is an obvious
rule for how a provider-supplied leaf combines with an option-supplied one. D2 states the
rule for the existing leaves and explicitly freezes it: *"**Only `taskStore` is rescoped to
apply-as-default**; the documented last-writer-wins precedence for the other **five**
provider leaves (`service/options.go:157-160`) stays"* — and then never says which rule the
new authorizer leaf follows.

**Evidence.** `service/options.go:150-168`: *"Precedence is last-writer-wins in option
order: a finer per-leaf override (e.g. `WithInstanceStore`) placed AFTER `WithDurableStore`
replaces that single leaf; placed before, it is overwritten by the provider."* If the
authorizer leaf inherits that rule, then:

```
service.WithAuthorizer(myStrictAuthorizer),   // explicit, deliberate
service.WithDurableStore(p),                  // p also implements AuthorizerProvider
```

silently discards `myStrictAuthorizer`. Because `service/service.go:199-200`'s
`if c.authz == nil` default runs *after* all options, no error and no log distinguishes
this from the intended case. The mirror-image ordering discards the provider's. Note this
is a *new* trap: D2's own analysis correctly establishes that today
"`WithDurableStore` **never writes `c.authz`** — so the authorizer is not lost to option
order in either direction". **`AuthorizerProvider` is precisely the change that makes
`WithDurableStore` write `c.authz`, and therefore creates the trap D2 just finished
proving does not exist.** The ADR text ("the trap is narrower than the failed draft
claimed") is a true statement about the *current* code that becomes false in the same
decision that states it.

**Why it matters.** An authorizer is not an instance store: silently taking the *later* one
is a security-relevant coin flip, and the failure is invisible — the losing authorizer
simply never runs.

**Concrete proposed fix.**
- State the rule explicitly in ADR Decision 2: the authorizer leaf is **apply-as-default**
  (`if c.authz == nil { c.authz = ap.Authorizer() }`), i.e. an explicitly-supplied
  authorizer always wins regardless of option order — the same rescoping `taskStore` gets,
  for the same reason, and the one that cannot silently weaken a deployment.
- Correct the ADR/spec sentence that says only `taskStore` is rescoped: with this decision
  it is `taskStore` **and** the authorizer. Then re-count "the other five provider leaves".
- Add a table test with **both orderings** × {provider supplies, provider does not} ×
  {option supplies, option does not}, asserting the resulting `c.authz` identity — the
  only test shape that can catch an order-dependent authorizer.

---

### F10 — D3's migration predicate × D3's gate predicate: "states no dimension" is a Go three-term expression and a SQL `WHERE` clause that must agree across FOUR wire encodings of "empty", with no shared definition

**Severity: MAJOR**

**The two things that interact:** D3's gate, whose notion of "stated" is
`len(spec.Roles) > 0 || len(spec.Privileges) > 0 || spec.Attribute != ""` (plan Task 8's
`checkSpecStated`) × D3's `0002` migration, told only to *"backfill **only** rows whose
spec states no dimension"* (plan Task 14 Step 3) in three SQL dialects.

**What each assumes the other provides.** The gate assumes every row it sees was correctly
classified by the migration. The migration assumes "states no dimension" has an obvious SQL
spelling.

**Evidence (executed** — `go test -count=1 -run '^TestHowManyWaysIsASpecEmptyOnTheWire$'
-v ./zzkeyprobe/`**):**

```
zero value                   -> {"Roles":null,"Privileges":null,"Attribute":"","Open":false}  gate:stated=false
empty non-nil slices         -> {"Roles":[],  "Privileges":[],  "Attribute":"","Open":false}  gate:stated=false
nil roles, empty privileges  -> {"Roles":null,"Privileges":[],  "Attribute":"","Open":false}  gate:stated=false
STATED (roles)               -> {"Roles":["manager"],…}                                       gate:stated=true
```

Three JSON shapes all mean "unstated" to the gate, mixing **JSON null**, **empty array**
and **empty string** in a single document, and the null/`[]` distinction is a genuine
historical fact of the data (`humantask.HumanTask.Clone` at `humantask.go:139-140` uses
`slices.Clone`, which maps nil→nil and `[]`→`[]`, so both shapes survive a round trip). A
`WHERE` clause that only tests `IS NULL` under-matches and strands the `[]` rows; one that
tests emptiness in one dialect's idiom and not another's strands them per-dialect. Note
also that `AuthzSpec` carries **no `omitempty`**, so `"Open":false` is written explicitly on
every post-upgrade row — the "absent key" signal exists only on pre-upgrade rows, and only
until any code path rewrites them.

**Why it matters.** A migration that under-matches is exactly the stranding D3 exists to
prevent, and per **F1** it strands *harder* than the bundle believes, because the
allow-all posture no longer rescues an unstated spec. The bundle spends a page on *which
copies* to backfill and one clause on *which rows*, and the row predicate is the half that
must be reimplemented three times in a language with different JSON semantics.

**Concrete proposed fix.**
- Write the predicate **once, in prose, in ADR Decision 3**, as the SQL contract:
  *a spec is unstated iff (`Roles` is null or an empty array) and (`Privileges` is null or
  an empty array) and (`Attribute` is null, absent, or the empty string).* Then each
  dialect implements that sentence, and a reviewer can check three files against one line.
- Add a **cross-checking test** rather than three hand-written per-dialect assertions:
  seed the four shapes above (plus a stated control) into each dialect via
  `dbtest.RunTestSQLite` / `RunTestDatabase` / `RunTestMySQL`, run the migration, read the
  rows back through the real store, and assert `Open == !gateSaysStated` — i.e. assert the
  SQL agrees with the **Go predicate**, not with a transcription of it. That is the only
  assertion that cannot drift from `checkSpecStated`.
- Consider deriving them from one source: export the predicate as
  `authz.AuthzSpec.StatesNothing() bool` and have both `checkSpecStated` and the migration
  test call it, so a change to one side breaks the other.

---

### F11 — D3's sentinels × D3's residual 2: post-upgrade stranding is indistinguishable from a normal denial — same 403, same error identity, no metric

**Severity: MEDIUM**

**The two things that interact:** D3's choice to wrap both new sentinels so
`errors.Is(err, authz.ErrNotAuthorized)` keeps holding — *"leaving existing callers and the
403 classification unchanged"* × D3's own residual 2, *"a consumer-implemented durable
`TaskStore` receives no migration; its pre-upgrade dimension-less rows will deny."*

**What each assumes the other provides.** The wrapping decision assumes the new denials are
authorization outcomes. Residual 2 assumes an operator will notice the stranding and act on
the release note.

**Evidence.** `authz.ErrSpecStatesNothing` and `authz.ErrUnevaluatableSpec` both wrap
`ErrNotAuthorized`, so at the transport they render as **403**, identical to a genuine
"this actor is not eligible". `runtime/task/service.go` increments its
`wrkflw_human_tasks_total` counter **only on success** (`:201`, `:236`, `:257` all sit
after the `Authorize` error return), so a denial emits no metric at all, and the four verbs
log nothing on the denial path. Combined with **F1** and **F3**, the population that
suddenly returns 403 after upgrade is larger than the ADR's residual 2 describes: consumer
`TaskStore`s, `MemTaskStore`, every `AuthzSpec{}` literal, and every unstated spec under
`WithAllowAllAuthorizer()`.

**Why it matters.** The bundle's answer to "we may strand in-flight work" is a release note.
A release note is only actionable if the operator can tell stranded work apart from normal
denials — and here they cannot, from status code, error identity, log, or metric. This is
the ADR-0186 lesson the spec §6 preamble cites (a documented residual is still a shipped
defect) applying to the very residual that cites it.

**Concrete proposed fix.**
- Keep the 403 (compatibility is the right call) but make the two new denials
  **observable**: a `slog` WARN at each gate rejection naming the task ID, the spec shape
  and the sentinel; and a distinguishing attribute on the existing counter (e.g.
  `attribute.String("event", "denied")` plus `attribute.String("reason",
  "spec_states_nothing"|"unevaluatable_spec")`), so an operator can alert on a post-upgrade
  spike.
- State in the ADR that the *reason phrase* is the diagnostic, and that
  `errors.Is(err, authz.ErrSpecStatesNothing)` is the supported programmatic check for a
  consumer building their own repair tooling.

---

### F12 — D3's `Open` × candidate resolution: an "open to any authenticated actor" task resolves to ZERO candidates, and no bundle document mentions `ActorResolver` at all

**Severity: MEDIUM**

**The two things that interact:** D3's `Open` marker, whose stated meaning is *"any
**authenticated** actor may act"* × the candidate-resolution path
(`humantask.ActorResolver.Candidates(ctx, spec, vars)`), called from
`runtime/task/service.go:321-333` (`RefreshCandidates`) and
`runtime/processdriver_action.go:297` (task minting), which expands an eligibility spec
into the concrete actor list stored on the task and rendered by every task UI.

**What each assumes the other provides.** D3 assumes `Open` is a complete statement of
eligibility — it satisfies `model.Validate`, it passes the gate, and the ADR presents it as
the explicit replacement for what an empty spec used to mean. Candidate resolution assumes
the spec names *dimensions it can expand*, and every existing resolver expands roles.

**Evidence (executed** — `go test -count=1 -run '^TestCandidatesForAnOpenSpec$' -v
./zzkeyprobe/`, `--- PASS`**):**

```
roles-stated                                     -> candidates=[{alice [manager] map[]}] err=<nil>
OPEN (ADR-0185: any authenticated actor may act) -> candidates=[]                        err=<nil>
legacy empty spec (pre-ADR-0185 allow-all)       -> candidates=[]                        err=<nil>
```

**Why it matters.** The *behaviour* is unchanged from the legacy empty spec, so this is not
a regression — but the **claim** is new. Before D3, nobody asserted that a dimension-less
spec meant "everyone"; the empty candidate list was consistent with a spec that said
nothing. After D3, the ADR says `Open` means every authenticated actor may act, and the
system will report that **nobody** is a candidate for such a task. A consumer's task inbox,
which is built from `task.Candidates`, shows an open task to no one — so the marker the
bundle introduces as the safe, explicit replacement for the old permissive default is also
the one that makes a task invisible in the UI. `RefreshCandidates` is one of the four verbs
the gate now guards, so this is inside the decision's own blast radius, and neither the
spec, the ADR nor the plan mentions `ActorResolver` anywhere.

**Concrete proposed fix.**
- State the semantics in ADR Decision 3 in one sentence: *"`Open` is an authorization
  statement, not a candidate statement. An open task has no derived candidate list;
  consumers listing work for an actor must treat `Open` tasks as visible to all
  authenticated actors."* That is cheap and closes the surprise.
- Add an `Open` case to the gate's seam-level `RefreshCandidates` test asserting an empty
  list is returned **without error**, so the behaviour is pinned rather than incidental.
- File a backlog entry for the ergonomic gap (a task nobody is listed for) rather than
  solving it here — solving it means teaching every `ActorResolver` about `Open`, which is
  a public-interface change and out of this bundle's scope.

---

## Surfaces checked that produced NO finding

Recorded so the next reader knows they were examined, not skipped.

- **D3's gate vs D1's 401 — no status leak.** The actor is resolved in `httpcore` *before*
  `svc.ClaimTask` is called (`transport/http/httpcore/endpoints.go:116-125`), and the gate
  lives in `runtime/task`, reached only through the service. So an unauthenticated request
  against an unstated spec returns **401**, never a 403 that would disclose the
  misconfiguration; and an authenticated request against an unstated spec returns 403. The
  order is correct in both directions. ⚠ One caveat for the implementer, not a finding:
  `errors.go:35-41` puts the `authz.ErrNotAuthorized` arm **second**, before every
  bad-input arm, so a new `ErrNoAuthenticatedActor` that wraps `ErrNotAuthorized` "for
  consistency" would classify **403 instead of 401**. Keep the 401/503 sentinels
  independent of `ErrNotAuthorized`.
- **D2 × D4 and D2 × D5 (the removal grid's "None" row) — confirmed correct.** D2 touches
  only construction-time wiring; neither the deleted attribute-reference rule nor the
  deleted claimant guard appears on any path D2 changes.
- **D3 × D4 (the grid's "cut CLOSES a hazard" row) — confirmed correct.** With D4 gone,
  the upgrade-stranding-through-`Attribute` hazard has no mechanism left.
- **Phase-ordering / intermediate loss of `Open` (surface 7).** Task 5 (`activity` +
  `NodeWire`) lands in Phase 1 and Task 8a (the `engine` mint site) in Phase 2, so the
  order is right, and the whole delivery is one amended commit, so no intermediate commit
  can ship a definition that round-trips without minting `Open`. The real risk here is F7
  (Task 5 cannot go green without touching `internal/atrest`), not sequencing.

---

## Summary — interaction lens

| severity | count | findings |
|---|---|---|
| CRITICAL | 4 | F1 (AllowAll × the hoisted gate), F2 (migration key × ADR-0167 strict decoding), F3 (D2's carve-out × every engine being a human-task engine), F6 (data migration × ADR-0187 D11's fail-closed parser) |
| MAJOR | 6 | F4 (empty-ID rationale × removed D5), F5 (`Open` × anonymous opt-in × build-only examples gate), F7 (`EligibleOpen` × ADR-0187's `PolicyAtRestLocations` guard), F8 (D2 × the dead `ProcessDriver.authz` slot), F9 (`AuthorizerProvider` × last-writer-wins), F10 (migration predicate × gate predicate) |
| MEDIUM | 2 | F11 (sentinel wrapping × residual 2's observability), F12 (`Open` × candidate resolution) |
| **total** | **12** | |

**The single most important finding is F1.** The bundle's central claim — that making the
gate authorizer-aware "dissolves" the D2 × D3 contradiction — is false, and the plan's own
prescribed test proves it against the plan's own prescribed implementation. This is the
same failure class that killed the previous revision: a fix (the `DimensionEvaluator`
capability) that is correct for the decision it was written for and blind to the leg of the
other decision it was supposed to rescue. It must be resolved before implementation, not
during it.
