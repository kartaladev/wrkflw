# Audit — ADR-0185-core bundle — FAILURE-MODES lens

Worktree: `wt-failure-modes`, detached at `5ce393f4`. Bundle verified present (step 0 OK).
Docker NOT available to this lens; probes are container-free packages only.

Findings appended as established.

### F1 — The migration's literal instruction (`"Open": true` into the definitions copy) makes the ENTIRE stored definition undecodable, not just the task

**Severity: Critical**

**What the bundle says.** ADR `0185-...md:237-239`: *"A per-dialect `0002_*.sql` backfills
`"Open": true` into stored specs carrying no dimension, **across all three durable copies**,
so no ambiguous row survives into the new binary."* Spec `§5.2:296-298` repeats it verbatim.
The third copy is `wrkflw_definitions.definition` (spec §2.1 table, ADR:104).

**Evidence (executed, in `wt-failure-modes`).** `wrkflw_definitions.definition` does **not**
store an `AuthzSpec` object at all — it stores `NodeWire`, whose eligibility is **three flat
fields** `eligible_roles` / `eligible_privileges` / `eligible_expr`
(`definition/model/node_wire.go:27-29`). There is no `Open` key to backfill, and
`ProcessDefinition.UnmarshalJSON` applies `DisallowUnknownFields`
(`node_wire.go:187-190`, ADR-0167 D1). Probe `definition/model/zz_probe_fm_test.go`
(`go test -count=1 -run '^TestZZProbeMigrationKeyInjection$' -v ./definition/model/` →
`EXIT=0`, `--- PASS`):

```
BASE JSON: {"id":"d","version":1,"nodes":[{"id":"t1","kind":"userTask","eligible_roles":["manager"]}],"flows":null}
CASE A  inject "Open":true  => err=json: unknown field "Open"
```

**Why it matters.** The failure is not a denied task — it is `GetDefinition`/`Lookup`
returning a decode error for the whole `ProcessDefinition`. Every node in that definition,
every running instance of it, and every future mint dies. The migration written to *prevent*
stranding would instead brick the definitions it touched, and it would do so silently at the
first read after upgrade. The ADR names this copy *"the one that keeps generating bad rows if
skipped"* (`:241`) — the prescribed fix for that is the thing that breaks it.

**Concrete proposed fix.** State the per-copy key explicitly in the ADR and spec, because the
three copies do **not** share a shape:
- `wrkflw_human_task.eligibility` → `AuthzSpec` object → key `"Open"`.
- `wrkflw_instances.snapshot` → `InstanceState.Tasks[].Eligibility` → key `"Open"`, nested.
- `wrkflw_definitions.definition` → `NodeWire` → key **`"eligible_open"`**, nested per node,
  and only on nodes with `"kind":"userTask"`.
Replace the ADR's "backfills `"Open": true` … across all three durable copies" sentence with
a three-row table naming the JSON path per copy. Add a plan step requiring the migration test
to assert the migrated definition **round-trips through `ProcessDefinition.UnmarshalJSON`**,
not merely that the SQL ran.

### F2 — Migration 0002 is irreversible and breaks any rolling deploy: an OLD binary cannot decode a migrated definition AT ALL

**Severity: Critical**

**What the bundle says.** Nothing. The bundle has no rollback, rolling-deploy, or
mixed-version section. ADR Consequences names only *"A **migration** is required, not a
CHANGELOG note"* (`:319`) and the consumer-`TaskStore` residual (`:320-322`). Plan Phase 5
Task 14 prescribes forward migration tests only (`:1140-1160`).

**Evidence (executed, same probe, CASE B).** With the *correct* key from F1:

```
CASE B  inject "eligible_open":true => err=json: unknown field "eligible_open"
```

`DisallowUnknownFields` (`definition/model/node_wire.go:190`) means the pre-0185 binary —
which has no `NodeWire.EligibleOpen` field — cannot decode **any** definition row the
migration touched. Not "loses the openness": fails the whole `ProcessDefinition`.

**Why it matters.** Three concrete operational failures the bundle does not cover:
1. **Rolling deploy.** Migrations and the new binary ship together; while the new pod
   migrates, every old pod still serving traffic starts failing `Lookup`/`GetDefinition` for
   every migrated definition. This is a full outage of those processes, not a task-level
   denial, and it hits definitions that merely *contain* a dimension-less `UserTask`.
2. **Rollback.** Once 0002 has run, rolling the binary back leaves the database in a state
   the old binary cannot read. There is no down-migration in the plan, and stripping
   `eligible_open` back out would re-create the ambiguity 0002 exists to remove.
3. It is exactly the class the ADR claims to have designed around — *"provenance is resolved
   … in the database, before the new binary reads it"* (`:234-235`) reasons about ONE
   direction of one binary and never asks what the OLD binary does with the migrated row.

**Concrete proposed fix.** Pick one and state it in the ADR:
- **(a) Two-release migration** (preferred, and the only rollback-safe one): release N adds
  `NodeWire.EligibleOpen` as a *tolerated, unused* field (decode-compatible, no behaviour
  change) and ships no migration; release N+1 runs 0002 and turns on the deny semantics.
  Then no binary in either version window meets an undecodable row.
- **(b)** Make the definitions decoder tolerate `eligible_open` specifically — impossible
  retroactively for already-deployed binaries, so this is not a real option and should be
  written off explicitly.
- **(c)** Do not migrate the definitions copy at all; grandfather at the mint site instead
  (the alternative §5.2:304-305 already floats but the ADR then discards). This trades F1/F2
  for a mint-site provenance problem, which should be adjudicated on the record rather than
  left implicit.
Whichever is chosen, the ADR needs an explicit **"Upgrade and rollback"** subsection; today
it has none, and rule-#10 handover cannot describe an ordering nobody wrote down.
### F3 — A task the migration misses is permanently stuck: the gate is wired at ALL FOUR verbs, so Reassign — the repair verb — denies too

**Severity: Critical**

**What the bundle says.** ADR `:234`: *"any encoding where absence means deny **strands
in-flight work**"* — the design's whole answer is *"provenance is resolved … in the
database"* (`:235`). Plan Task 8 Step 4 (`:947-949`) then wires the gate at **all four**
sites: *"`:199`, `:234`, `:255`, `:306` — each calling `checkSpecStated(s.authz,
task.Eligibility)` **before** `s.authz.Authorize`."* Residual 2 (spec `§6:376-378`) says a
consumer-implemented durable `TaskStore` gets no migration and *"its pre-upgrade
dimension-less rows will deny"* — described as *"a release-note item"*.

**Evidence (executed, `wt-failure-modes`).**
`grep -n '\.Authorize(' runtime/task/service.go` → exactly four, all reading
`task.Eligibility`: `:199` (Claim), `:234` (Reassign), `:255` (Complete), `:306`
(RefreshCandidates). `grep -n '^func (s \*TaskService)' runtime/task/service.go` → those
four exported verbs plus unexported `resolveCandidates`. **There is no admin, force, or
override verb.** So an unstated-spec task is simultaneously unclaimable, uncompletable,
unreassignable and un-refreshable — the exact state the ADR calls "stranded", reached
through the door the ADR opened.

The only escapes that exist are both outside the supported task API:
- `humantask.TaskStore.Upsert` (`humantask/humantask.go:195`) — a consumer holding the store
  handle can rewrite `Eligibility.Open = true` directly. Crude, undocumented, unmentioned in
  the bundle.
- `ProcessDriver.ApplyTrigger` / `engine.NewHumanCompleted` (`engine/trigger.go:399`) — the
  bundle's **residual 1**, which it frames purely as a security hazard
  (spec `§6:372-375`). It is also, after this bundle, the **only in-engine way to unstick a
  stranded task**. The bundle never notices the inversion.

**Why it matters.** "Will deny" understates it by a category. A denial you can repair is an
inconvenience; a denial with no repair verb is data loss of the in-flight process. Residual 2
is the case the bundle *admits* has no migration, so the bundle knowingly ships an
unrecoverable state and grades it "release-note". This repo's `/code-review` gate has twice
refused the documented-vs-mitigated distinction (ADR-0186's two MEDIUMs) and will refuse it
again here, where the documented residual is a Critical-severity outcome rather than a
MEDIUM one. Worse, the residual-1 escape is scheduled for closure by the deferred backlog 124
— so the roadmap removes the only repair path while the strand remains reachable.

**Concrete proposed fix.** Do not ship the gate on all four verbs without a repair verb.
Pick one, and put it in the ADR's Decision 3 rather than a residual:
- **(a) Exempt `Reassign` from `checkSpecStated`** and document it as the designated repair
  verb: an unstated spec still denies Claim/Complete/RefreshCandidates, but an actor
  authorized by the *configured Authorizer* may reassign, which re-runs `resolveCandidates`
  and gives the operator a way out. This keeps the security property (nobody *acts* on an
  unstated spec) while restoring recoverability. ⚠ Note this needs its own reasoning about
  who may reassign, since `Reassign` authorizes `by` against the same unstated spec — the
  exemption must skip the gate, not the `Authorize` call, and the ADR must say which.
- **(b) Add an explicit `TaskService.SetEligibility` (or `RepairEligibility`) verb**, gated
  on the configured `Authorizer` rather than the task's own spec, and name it in the release
  note as the remedy for residual 2.
- **(c)** At minimum, if neither is taken: state in the ADR *"a task with an unstated spec
  has no repair verb in the public API; recovery requires direct `TaskStore.Upsert`"*, and
  add a startup-time scan/warning that counts unstated-spec rows so an operator learns of
  them before a user does. Silence here is not an adjudication.

### F4 — `examples/scenarios/manual_task` stops working the moment `model.Validate` tightens, and the plan's own examples check (`go build`) cannot see it

**Severity: Major**

**What the bundle says.** Plan Task 5 (`:672-690`): *"`model.Validate` **rejects** a
`UserTask` carrying neither the open marker nor any eligibility dimension"*, and the
blast-radius warning tells the implementer to *"expect to fix fixtures in `engine/`,
`runtime/`, `processtest/` and `service/`"* — `examples/` is **not** in that list. Task 16
(`:1191-1199`) touches only the three *wiring* mains and verifies with
*"`go build ./examples/...` must pass."*

**Evidence (executed).** `grep -rn "NewUserTask(\|AddUserTask(" --include='*.go' .` over
non-test, non-`definition/` files: every example declares a dimension **except**
`examples/scenarios/manual_task/main.go:45-46`:

```go
Add(activity.NewUserTask("handOverBadge", activity.WithManual(false))).
Add(activity.NewUserTask("recordOrientation", activity.WithManual(true))).
```

The wire form confirms it states nothing (`definition/model/zz_probe_fm2_test.go`,
`--- PASS`): `{"id":"handOverBadge","kind":"userTask","manual":true}` — no `eligible_*` key.
And it works today: `go run ./examples/scenarios/manual_task/` → **`EXIT=0`**, printing
*"instance completed — both manual steps confirmed"*.

`find examples -name '*_test.go'` returns exactly one file, `examples/migrate/main_test.go`
— **nothing tests the scenario mains**. `Validate` is called from
`definition/model/builder.go:133`, i.e. at `Build()` **runtime**, so `go build ./examples/...`
compiles cleanly while the binary now fails at first execution.

**Why it matters.** Two separate defects. (1) A shipped, documented example breaks and the
delivery gate cannot detect it — the repo would ship a reference program that exits with an
error, which is exactly the `examples/`-as-product-documentation failure CLAUDE.md warns
about. (2) It is *evidence about the design*: `WithManual` tasks are the case where "state
who may act" has no natural answer — a manual task is a record-keeping step, not an
assignment — so the authoring gate's usability, not just its migration, needs an answer.

**Concrete proposed fix.**
1. Add `examples/scenarios/manual_task/main.go` to Task 5's blast-radius list explicitly, and
   fix it with `activity.WithOpenEligibility()` on both nodes (this is precisely what the
   option is for) — and say so in the ADR as the canonical use of the new option.
2. Replace Task 16's `go build ./examples/...` with `go run` (or a smoke test) over the
   scenario mains, or state plainly that examples are unverified. A build check on a package
   whose failure mode is a runtime `Build()` error is a check that cannot fail.
3. Re-derive the full authoring-form set as Task 5 already instructs, but include
   `examples/` — the `grep` above found the one site the plan's four-package list omits.
### F5 — `WithAnonymousActorAllowed()` and "an empty `Actor.ID` is rejected" are mutually exclusive; the three examples the opt-in exists for cannot claim a task

**Severity: Critical**

**What the bundle says.** Two rules, four lines apart, in the same Decision-1 bullet list:
- ADR `:161-164`: *"`httpcore.WithAnonymousActorAllowed()` is the explicit opt-in for demo
  and example wiring — `examples/production_wiring`, `examples/sqlite_wiring` and
  `examples/mysql_wiring` all mount the task routes via `stdlib.Mount` and have no
  authentication."*
- ADR `:167-171`: *"An **empty `Actor.ID` is rejected as a claimant identity** in the claim
  path. ⚠ The rationale is re-derived … the audit trail must not record `""` as an actor,
  **and under the 401 rule a caller that reached the handler has an ID**."*

**Evidence.** The second rule's justification is explicitly *conditional on the 401 rule* —
and `WithAnonymousActorAllowed` is precisely the switch that turns the 401 rule off. An
anonymous request has, by construction, no actor in the context; whatever the handler
synthesises is the zero `authz.Actor`, whose `ID` is `""`. So in anonymous mode every claim
hits the empty-ID rejection. The bundle never says what actor an anonymous request carries —
`ActorFromContext` returns `authz.Actor{}` with `ok == false`
(plan `:143-147` asserts exactly that), and no alternative is specified anywhere in ADR,
spec or plan.

Consequence chain, all inside this bundle: Phase 6 Task 16 (`:1191-1195`) adds the anonymous
opt-in to the three mains → their task routes now accept requests → every claim through them
is refused on empty ID. The plan's only check for those files is `go build ./examples/...`
(F4), which cannot see it.

**Why it matters.** This is the interaction class CLAUDE.md's rule-#9 corollary is written
for: two individually-correct rules from the same decision that void each other. It ships a
public option (`WithAnonymousActorAllowed`) whose sole stated purpose — making the three
demo wirings work — it does not achieve, and the failure is a runtime refusal in reference
code a consumer copies.

**Concrete proposed fix.** Decide and write down what an anonymous request's actor *is*.
Options, in preference order:
- **(a)** `WithAnonymousActorAllowed(id string)` — take a required non-empty synthetic
  identity (e.g. `"anonymous"`), so the audit trail records something real, the empty-ID rule
  stays universal, and the option is self-documenting at the call site. The examples pass
  `"demo-user"`.
- **(b)** Keep the no-arg form but define it as injecting `authz.Actor{ID: "anonymous"}`, and
  say so in the ADR and the option's godoc.
- **(c)** Scope the empty-ID rejection to *authenticated* requests only — weakest, because it
  re-admits `""` into the audit trail, which is the rule's own re-derived rationale.
Whichever is chosen, add a test driving Claim through an anonymously-configured `httpcore`
and asserting a 200, not a refusal. Without it this interaction is untested in both bundles
that touched it.

### F6 — The empty-`Actor.ID` rule is scoped to Claim while its stated rationale covers Complete and Reassign, and Complete is where `""` actually reaches the audit record

**Severity: Major**

**What the bundle says.** ADR `:167`: *"An empty `Actor.ID` is rejected as a claimant
identity **in the claim path**."* Rationale (`:169-170`): *"the audit trail must not record
`""` as an actor"*. The ADR's own Context (`:119-121`) states the audit-record fact about the
**completion** path: *"`handleHumanCompleted` copies `t.Actor` into the audit record
unvalidated"*.

**Evidence (source-verified, `wt-failure-modes`).** All three verbs pass the actor straight
into a trigger with no ID check: `runtime/task/service.go:203`
`engine.NewHumanClaimed(s.clk.Now(), taskID, actor)`, `:237`
`engine.NewHumanReassigned(…, by)`, `:258` `engine.NewHumanCompleted(…, c, actor)`.
`Reassign` guards only the *target* (`:220` `if to == ""` → `engine.ErrEmptyReassignTarget`),
never `by`. Nothing guards `actor.ID` on any path today.

**Why it matters.** The rule as written protects the verb where the rationale is weakest and
skips the two where it is strongest — Complete is the one the ADR itself cites as writing the
actor into the durable audit record. Combined with F5, an anonymous deployment records `""`
as the completer of every task while the claim path refuses. The scope is an artefact of the
rule's *previous* justification (the deferred backlog-124 `"" == ""` degeneracy, which was
claim-shaped); the re-derivation changed the rationale but the ADR kept the old scope. That is
the "restating strips the hedge" failure in reverse — re-deriving kept the wrong boundary.

**Concrete proposed fix.** Apply the empty-ID rejection to **all three actor-bearing verbs**
(Claim `actor`, Complete `actor`, Reassign `by`), and state the scope explicitly in ADR
Decision 1 rather than saying "the claim path". Enforce it once — at the `httpcore` resolution
seam where the actor is produced, not per-endpoint — so a future verb inherits it. Classify it
as **400** for consistency with the two existing empty-identity sentinels
(`engine.ErrEmptyTriggerKey` ADR-0152, `engine.ErrEmptyReassignTarget` ADR-0183, both on the
400 arm at `transport/http/httpcore/errors.go:65-77`); the bundle currently specifies **no
status at all** for this rejection.

### F7 — The 401 sentinel's position in `ClassifyError` is unspecified, and the natural implementation makes 401 unreachable

**Severity: Major**

**What the bundle says.** Plan Task 9 Step 4 (`:1044-1047`): *"⚠ **Arm the 401/503 arms
relative to the existing ordered arms.** ADR-0186 put the 413 arm *before* the ordered 400 arm
for exactly this reason. Read `errors.go`'s existing order before inserting."* That is the
only guidance; no position is specified and no co-matching arm is named.

**Evidence (source-verified).** `transport/http/httpcore/errors.go:40-41` puts
`errors.Is(err, authz.ErrNotAuthorized)` → **403** as the *second* arm of the switch. This
bundle simultaneously establishes the convention that new authorization sentinels **wrap**
`ErrNotAuthorized` — ADR `:283-284`: *"both **wrapped so `errors.Is(err,
authz.ErrNotAuthorized)` keeps holding**"*, and plan `:317-321` pins it with
`TestSpecSentinelsWrapErrNotAuthorized`. An implementer defining
`ErrNoAuthenticatedActor` the same way — the pattern the same bundle just taught them, twice —
produces an error matching the 403 arm first, and **401 is never returned**. The 401 tests in
plan Task 9 would then fail confusingly, or worse, be "fixed" by inserting the arm above 403
without noticing that any *genuinely* forbidden error also wrapping the new sentinel would
flip to 401.

`errors.go:51-55` states the repo's own standing invariant for this exact hazard: *"STANDING
INVARIANT for any arm added to this switch: state its position relative to the arms it can
co-match, and carry a test asserting an error matching two arms resolves to the intended
one."* The bundle adds two arms and satisfies neither half.

**Why it matters.** 401-vs-403 is the load-bearing distinction of Decision 1 — *"refused,
never downgraded"* — and it is the one thing in the decision that a positional accident
silently reverses. A 403 on an unauthenticated request tells the client "you are known and
not allowed" instead of "authenticate", breaking the retry semantics every HTTP client
implements.

**Concrete proposed fix.** Write both properties into ADR Decision 1 and plan Task 9:
1. **`ErrNoAuthenticatedActor` and `ErrActorResolutionFailed` MUST NOT wrap
   `authz.ErrNotAuthorized`** — they are absence-of-identity and infrastructure failure, not
   authorization decisions. Say it, and pin it with `assert.NotErrorIs`.
2. State the position: both arms go **above** the `ErrNotAuthorized`/403 arm, with the
   errors.go comment convention naming what they co-match and why.
3. Add the co-match test the file's standing invariant requires, for each new arm.
4. State that the 503 body follows the file's 5xx policy (`Message` omitted,
   `errors.go:25-33`) — a resolver error's text can carry identity-provider internals.
### F8 — A consumer decorator around the casbin authorizer silently downgrades it to roles-only; nothing detects it at wiring time, and the resulting tasks are stranded (F3)

**Severity: Critical**

**What the bundle says.** ADR `:277-279`: *"`casbinauthz.Authorizer` forwards its inner's
declaration; `processtest.SpyAuthorizer` declares all three. **Anything else defaults to
roles only**, fail-closed, with an error naming the capability."* Consequences `:331-333`
grades it: *"The default of **roles only** for a non-declaring `Authorizer` is fail-closed
but breaking for a consumer whose own authorizer evaluates more; they must implement one
method."*

**Evidence (source-verified).** The in-repo construction routes are safe — every
`casbinauthz` entry point returns `*casbinauthz.Authorizer`
(`casbinauthz/casbinauthz.go:177` `newFromEnforcer`, `:187` `newFromStrings`, `:216`
`newFromDB`, all through `NewCasbinAuthorizer` `:112`), so the forwarding method lands. The
hazard is the layer above. `authz.Authorizer` is a 1-method interface and the repo *teaches*
decoration around its ports: `persistence.CachingTaskStore` (`persistence/caching_task_store.go:22`)
wraps `humantask.TaskStore` the same way, and `casbinauthz.PolicyAdminFor(a authz.Authorizer)`
(`casbinauthz/policyadmin.go:26`) exists precisely because consumers hold the *interface*, not
the concrete type. A consumer's audit-logging or metrics decorator around their casbin
authorizer implements `Authorize` and not `EvaluatesDimension`.

Blast radius of that one omission, under this bundle: `authz.EvaluatesDimension` returns the
roles-only default, so **every `Privileges`-carrying and every `Attribute`-carrying spec
denies** — including the repo's own demonstrated baseline
(`examples/scenarios/attribute_authz/main.go` wires casbin *and* attribute predicates, and
CLAUDE.md makes casbin the baseline). Per **F3** those tasks are then also unreassignable, so
the consequence is not "breaking" — it is silent, deferred, unrecoverable stranding of live
human work, triggered by a wrapper that compiles cleanly and passes every existing test.

Nothing in the bundle detects it. `NewProcessEngine` (Decision 2) is the one place that
already inspects the authorizer, and neither the ADR nor plan Task 4 proposes a capability
check there. The failure surfaces at the first claim of a privileges-scoped task, in
production, arbitrarily long after deployment.

**Concrete proposed fix.** Move the detection to wiring time, where it is cheap and legible:
1. In `NewProcessEngine` / `NewTaskService`, type-assert `authz.DimensionEvaluator` and
   **log at WARN** when it is absent, naming the concrete type and stating that only
   `DimensionRoles` will be honoured. This is the same disclosure posture Decision 2 already
   adopts for allow-all — one WARN record, its own line — so it costs nothing new.
2. Add `authz.EvaluatesAllDimensions` (or an embeddable `authz.AllDimensions` struct) so a
   decorator can opt in with one embedded field instead of a method, and document
   *"if you wrap an Authorizer, forward `EvaluatesDimension`"* in the `DimensionEvaluator`
   godoc — the wrapping case is the one that will actually happen and the godoc drafted in
   plan `:361-372` never mentions it.
3. Add a plan task covering the decorator case as a test: a wrapper type around
   `casbinauthz.Authorizer` that forwards only `Authorize`, asserting the WARN fires. Today
   this case appears in no test in the bundle.

### F9 — `ErrUnevaluatableSpec` names neither the dimension, the authorizer, nor the method to implement, so the ADR's "an error naming the capability" is not what the plan builds

**Severity: Major**

**What the bundle says.** ADR `:279`: *"Anything else defaults to roles only, fail-closed,
**with an error naming the capability**."*

**Evidence (source-verified against the plan's own code).** Plan `:383-384` defines:

```go
var ErrUnevaluatableSpec = fmt.Errorf("%w: spec states a dimension this authorizer does not evaluate", ErrNotAuthorized)
```

and plan `:925-935`'s `checkSpecStated` returns
`fmt.Errorf("workflow-runtime: taskservice: %w", authz.ErrUnevaluatableSpec)` — discarding
`d.dim`, which is in scope in the loop directly above. The operator's whole diagnostic is:

```
workflow-runtime: taskservice: workflow-authz: spec states a dimension this authorizer does not evaluate
```

No dimension. No authorizer type. No mention of `authz.DimensionEvaluator`. And per
`transport/http/httpcore/errors.go:40-41` this reaches the client as a **403 with the message
echoed** — so the text is simultaneously the operator's only clue and something handed to an
unauthenticated-ish caller.

**Why it matters.** This is the sole feedback channel for F8's silent downgrade. A consumer
whose tasks suddenly 403 gets a sentence that does not tell them which of three dimensions
tripped, which authorizer is configured, or that a one-method interface exists to fix it.
The ADR promises legibility the plan does not implement — a rule-#11 divergence caught before
implementation rather than after.

**Concrete proposed fix.**
1. Give `Dimension` a `String()` method (`roles` / `privileges` / `attribute`) — it is an
   iota enum with no stringer today, and `NodeKind` in `definition/model` sets the precedent.
2. Wrap with context at the gate, keeping the sentinel identity:
   `fmt.Errorf("workflow-runtime: taskservice: %w: spec states %s but %T does not evaluate it; implement authz.DimensionEvaluator", authz.ErrUnevaluatableSpec, d.dim, az)`.
3. ⚠ Then re-check the HTTP surface: the 403 arm echoes `err.Error()`, so `%T` of the
   consumer's authorizer would leak an internal type name to the caller. Either log the
   detailed form and return the terse sentinel to the client, or state on the record that the
   type name is acceptable to expose. The bundle currently does neither, and
   `errors.go:25-33`'s client-safe-body contract makes this a decision that must be taken
   explicitly.
### F10 — The snapshot copy is written BACK over the task row on every task transition, so a snapshot the migration misses silently reverts the migrated task row — and the successful claim is what strands the task

**Severity: Critical**

**What the bundle says.** Spec §2.1's table (`:80-84`) assigns each copy a "read by" role:
`wrkflw_human_task.eligibility` → *"**all four `Authorize` sites**"*;
`wrkflw_instances.snapshot` → *"instance rehydration"*. The framing throughout is that the
task row is the authoritative copy and the snapshot is a passive projection —
ADR `:102-103` repeats it. Nothing in ADR, spec or plan mentions a write-back direction.

**Evidence (executed, `wt-failure-modes`).** The snapshot is not passive: it is the *source*
of every write to the copy authorization reads.

Chain, source-verified: `handleHumanClaimed` takes the task from the in-memory
`InstanceState` (`engine/step_triggers.go:578` `task := s.TaskByID(t.TaskID)`) — which is
rehydrated from `wrkflw_instances.snapshot` — and emits
`UpdateTask{Task: task.Clone()}` (`:591`), carrying that state's `Eligibility` wholesale.
`ProcessDriver.performUpdateTask` then `Upsert`s the whole record into the task store
(`runtime/processdriver_action.go:509`). The same shape appears at seven other emit sites
(`grep -rn "UpdateTask{" engine/` non-test: `step_timers.go:93`, `step_triggers.go:591,622,647,951`,
`state.go:656`, `step_cancel.go:40`, `step_stale_commands.go:171`).

Probe `engine/zz_probe_fm_test.go` (`package engine`,
`go test -count=1 -run '^TestZZProbeSnapshotEligibilityFlowsBackToTaskStore$' -v ./engine/`
→ `EXIT=0`, `--- PASS`):

```
UpdateTask.Task.Eligibility written back to the TaskStore = {Roles:[] Privileges:[] Attribute:}
source of that value: InstanceState.Tasks[0].Eligibility (the SNAPSHOT copy) = {Roles:[] Privileges:[] Attribute:}
discriminator: migrated snapshot writes back = {Roles:[manager] Privileges:[] Attribute:}
```

(The second case discriminates — the assertion is not vacuous on a constant.)

**Why it matters.** Order of events, all of them ordinary:
1. Migration backfills `wrkflw_human_task.eligibility` → `Open: true`. Task is claimable. ✅
2. The snapshot copy for that instance is missed, fails, or is written by a node still on
   the old binary.
3. Alice claims the task. The gate passes (it reads the *migrated task row*).
4. `handleHumanClaimed` reads the **stale snapshot**, `UpdateTask` carries `Open: false`,
   `performUpdateTask` Upserts it over the migrated row.
5. The task is now unstated ⇒ per **F3**, unclaimable, uncompletable **and unreassignable**.

The successful claim is the thing that strands the task. Re-running the 0002 task-row
backfill repairs it and the next transition reverts it again — the migration is idempotent as
SQL but not as an outcome. This also makes the copies' relationship the opposite of what the
spec's evidence section states, and §8 explicitly invites attacking that section.

**Concrete proposed fix.**
1. **Correct spec §2.1's table**: the snapshot's role is not "instance rehydration", it is
   *"rehydration, and the source of the `UpdateTask` write-back that overwrites
   `wrkflw_human_task.eligibility` on every task transition"*. This changes the copy-priority
   argument the whole migration rests on.
2. **Make the snapshot backfill a hard prerequisite, not one of three parallel targets.**
   Plan Task 14 Step 1 currently says *"Repeat for the instance snapshot and a stored
   definition"* (`:1143`) — reorder so the snapshot is migrated **first** and the task row
   last, and state that a partial run in the other order is worse than no run at all.
3. **Add the regression test that this bundle has no analogue of**: seed a migrated task row
   with a *stale* snapshot, drive a claim, and assert the task row still reads `Open == true`
   afterwards. Nothing in Task 14 exercises the write-back path; every prescribed test reads
   the row back through the store without transitioning the instance.
4. Consider making the write-back **not** carry `Eligibility` at all (the engine never mutates
   it; only `Claim`, `State`, `Candidates` and `DueAt` change) — that removes the whole class.
   This is the structurally cleanest fix and should be adjudicated explicitly.

### F11 — The snapshot backfill is nested-array JSON surgery in three dialects, and the plan budgets it as "repeat for"

**Severity: Major**

**What the bundle says.** Plan Task 14 Step 1 (`:1140-1145`): *"seed a pre-upgrade row (a task
whose `eligibility` JSON has no dimension and no `open` key), run migrations, read it back
through the real store, assert `Open == true`. **Repeat for the instance snapshot and a
stored definition.**"* Step 3 (`:1149-1151`): *"per dialect: Postgres `jsonb` operators, MySQL
`JSON_SET`/`JSON_CONTAINS_PATH`, SQLite `json1`."*

**Evidence (source-verified).** The three copies are not three instances of one problem:
- `wrkflw_human_task.eligibility` — a **whole-column** `AuthzSpec` object
  (`internal/persistence/store/humantask_store.go:157` marshal / `:398` unmarshal). A
  one-line `JSON_SET` per dialect. Easy.
- `wrkflw_instances.snapshot` — the `AuthzSpec` lives at
  `$.Tasks[*].Eligibility`, **an unbounded array**, and only elements whose spec states
  nothing may be touched. Postgres needs a `jsonb_array_elements` + re-aggregate rewrite (not
  `jsonb_set`, which cannot express a filtered per-element update); MySQL needs `JSON_TABLE`
  or a generated-index loop; SQLite needs `json_each` plus reassembly. Three genuinely
  different, error-prone statements.
- `wrkflw_definitions.definition` — a different schema entirely (`NodeWire`, per **F1**),
  nested per node under `$.nodes[*]`, filtered on `kind == "userTask"`, and guarded by
  `DisallowUnknownFields` on read-back.

The plan's `0002_authz_open.sql` is one file per dialect (File Structure, `:98`) covering all
three tables, which at least makes it atomic under goose's default per-file transaction
(no `-- +goose NO TRANSACTION` marker exists anywhere in
`internal/persistence/store/migrations/`, and all three are DML so MySQL's non-transactional
DDL is not in play). That is the one thing the plan gets right by accident rather than by
statement.

**Why it matters.** The plan's effort estimate is what determines whether this gets an agent
with a Docker brief and time to iterate, or is treated as a mechanical follow-on to the easy
case. The backlog-sweep lesson is exactly this: *"triage graded EFFORT and was read as grading
RISK — all 3 HIGH review findings were in the one item tiered 'Small'."* Here the hardest and
highest-consequence SQL in the delivery is one clause of one step. And per **F10** the
snapshot is the copy whose omission silently reverts the others.

**Concrete proposed fix.**
1. Split Task 14 into **three** tasks, one per copy, each with its own RED test and its own
   dialect matrix, and state in the plan that the snapshot one is the hard one.
2. Write the three statements into the plan (or the spec) rather than naming operators —
   `jsonb_array_elements`/`JSON_TABLE`/`json_each` are not interchangeable with the
   `JSON_SET`/`JSON_CONTAINS_PATH` the step names, and an implementer who follows the step
   literally will reach for the wrong tool.
3. Add the "states no dimension" predicate explicitly per copy — the definitions copy's
   predicate is over three *separate absent keys* (`eligible_roles`, `eligible_privileges`,
   `eligible_expr`) filtered to `kind == "userTask"`, not over one absent object.
4. Require a **count assertion** in each migration test: rows matched must equal rows
   intended, so a statement that silently matches nothing (the classic JSON-path typo) fails
   RED instead of passing as "nothing to migrate".
### F12 — YAML is a first-class authoring form with its OWN strict tag set, and the plan never adds `eligible_open` to it: every YAML author of a dimension-less user task is stranded with no expressible remedy

**Severity: Critical**

**What the bundle says.** Plan Task 5's file list (`:667-669`): *"Modify
`definition/activity/activity.go`, `definition/activity/options.go`,
`definition/model/node_wire.go`, `definition/model/validate.go`; tests alongside."*
`definition/model/yaml.go` is **not** in it — nor in the plan's File Structure table
(`:88-97`), nor anywhere in the ADR. The plan *does* know YAML is a third authoring form: its
own blast-radius warning (`:705-707`) names *"YAML `kind: userTask` (`activity.go:236`)"* as
one of three forms `Validate` reaches. It draws no conclusion from that.

**Evidence (executed, `wt-failure-modes`).** `nodeYAML` is a **separate struct with its own
yaml tags** (`definition/model/yaml.go:19-27`):

```go
EligibleRoles      []string `yaml:"eligible_roles,omitempty"`
EligiblePrivileges []string `yaml:"eligible_privileges,omitempty"`
EligibleExpr       string   `yaml:"eligible_expr,omitempty"`
```

— a **fourth** eligibility mapping site, alongside the two in `activity.go` the plan does name
(`:240`, `:251`) and `NodeWire`. And YAML decoding is strict: `yaml.go:209`
`dec.KnownFields(true)`. Probe `definition/model/zz_probe_fm3_test.go`
(`--- PASS`, the probe records the error):

```
ParseYAML with eligible_open: err=workflow-definition: parse YAML: yaml: unmarshal errors:
```

**Why it matters.** CLAUDE.md states the authoring forms are *"YAML and direct Go code"* —
YAML is not a convenience, it is half the product's authoring surface. After this bundle a
YAML author with a dimension-less user task is in a closed trap:
- `model.Validate` rejects their existing definition (Decision 3's authoring gate);
- the prescribed remedy, `eligible_open`, is **rejected by the YAML decoder** as an unknown
  field;
- there is no other way to state openness in YAML.

Their only escape is to abandon YAML for Go, or to invent an eligibility they do not want. And
this compounds F1/F2: the migration is supposed to backfill stored definitions, but a YAML
author re-authoring from source cannot produce a definition that validates.

**Concrete proposed fix.**
1. Add `EligibleOpen bool \`yaml:"eligible_open,omitempty"\`` to `nodeYAML`
   (`definition/model/yaml.go`), and add `definition/model/yaml.go` to Task 5's file list and
   the File Structure table.
2. Add `definition/model/yaml.go` mapping to/from `activity.UserTask.EligibleOpen` in
   `fromNodeYAML` (`yaml.go:85`).
3. Add a YAML round-trip case to Task 5's tests, and extend the existing
   `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` guard — it exists precisely to catch a
   tag added in one struct and not the other, and it is the test that would have caught this.
4. State the mapping-site count as **four** (activity→wire, wire→activity, `NodeWire` tag,
   `nodeYAML` tag), replacing the plan's *"TWO mapping sites, not one"* (`:695`).

### F13 — The plan's blast-radius list for the `model.Validate` tightening is wrong in both directions (measured), and Task 5's package-scoped verification cannot see the breakage it causes

**Severity: Major**

**What the bundle says.** Plan Task 5 (`:703-709`): *"**Re-derive the affected set across all
three forms before changing `Validate`**, and report the number you get — do not inherit `5`
or `≥13`. **Expect to fix fixtures in `engine/`, `runtime/`, `processtest/` and
`service/`.**"*

**Evidence (executed ablation, in this lens's OWN worktree, restored afterwards).** I patched
`validateStructure` with the post-bundle rule (reject a `UserTask` whose
`EligibleRoles`/`EligiblePrivileges`/`EligibleExpr` are all empty — i.e. the gate minus
`EligibleOpen`, which does not exist yet), then ran the container-free set:

```
go test -count=1 ./engine/... ./runtime/calllink/... ./runtime/signal/... ./runtime/task/... \
  ./service/... ./processtest/... ./transport/http/... ./definition/...   → EXIT=1
```

Result — **10 failing tests across 2 packages**, 17 occurrences of the new error:

| package | failing tests | in the plan's list? |
|---|---|---|
| `definition/model` | 7 (`TestValidateUserTaskOutcomes`, `TestParseYAMLUserTaskManual`, `TestParseYAMLUserTaskManualImmediate`, `TestYAMLNodeLabel`, `TestPersistedDefinitionRoundTripsThroughStrictJSON`, `TestParseYAMLUserTaskOutcomes`, `TestAllDeclaredYAMLTagsParseUnderStrictDecoding`) | ❌ **not named** |
| `engine` | 3 (`TestSignalDoesNotFireAnArmThisDeliveryCreated`, `TestMessageDeliveryStillFiresOnlyTheFirstArm`, `TestSignalFiresEveryMatchingBoundaryArm`, all in `engine/step_signal_fanout_test.go`) | ✅ named |
| `service` | **0** | ❌ named, does not break |
| `processtest` | **0** | ❌ named, does not break |
| `runtime/{calllink,signal,task}` | **0** | ❌ named ("runtime/"), does not break in the container-free part |

Restored from a `cp` backup (`diff` clean; `go test ./definition/model/` → `EXIT=0`).
⚠ **Partial**: `./runtime/...` as a whole is not container-free, so only three of its
subpackages ran; the `runtime` row is unverified beyond those.

**Why it matters.** Two distinct problems.
1. **The list misdirects.** It sends the agent to two packages that do not break and omits
   `definition/model` — which is both the largest hit (7 of 10) and the package Task 5 is
   *already editing*. Three of the seven are the ADR-0167 strict-decoding and YAML-tag guards
   (**F12**'s evidence), so an agent that "fixes" them by weakening a fixture would be
   disarming the guards that catch F12.
2. **Task 5's own verification cannot see half of it.** Every task's Step 5 is a
   package-scoped `go test`. Task 5 owns `definition`, so it will observe the seven
   `definition/model` failures and **not** the three in `engine`. Phase 1 then completes
   "green", and Phase 2's Task 8a agent inherits an `engine` package that is already red for
   reasons it did not cause — the exact confusion that costs a subagent its budget.

**Concrete proposed fix.**
1. Replace the plan's guessed list with the measured one above, marked as measured and with
   the `runtime` row labelled partial-pending-Docker.
2. Give Task 5 a **repo-wide** verification step (`go test ./... ` or at minimum
   `go vet ./...` plus the container-free set), not a package-scoped one — it is the one
   Phase-1 task whose change is behavioural and cross-package. CLAUDE.md's "fan out by Go
   package" rule guards same-package *compile* collisions; it does not cover a behavioural
   change that breaks another package's fixtures, and this plan needs that stated.
3. Move Task 5 **out of the Phase 1 fan-out** and run it inline/serially like Phase 0, for the
   same reason Phase 0 is inline: it is a repo-wide behavioural change every other phase
   builds on.
4. Name `engine/step_signal_fanout_test.go` explicitly — its three fixtures build user tasks
   for signal-fan-out reasons with no interest in eligibility, so the correct repair is
   `WithOpenEligibility()`, not inventing roles.
### F14 — Decision 2's predicate "when human tasks are configured" has no implementable signal: a task store is ALWAYS present, so the natural implementation breaks every zero-option consumer and the plan's own regression guard cannot pass

**Severity: Critical**

**What the bundle says.** ADR Decision 2 (`:194-195`): *"`NewProcessEngine` returns an error
**when human tasks are configured** and neither option supplied an authorizer."* Spec §4
(`:250-251`) repeats it. Plan Task 4's table has this as case 4 (`:509-516`):

```go
{
	name: "no human tasks configured needs no authorizer",
	opts: nil,
	assert: func(t *testing.T, eng *service.ProcessEngine, err error) {
		require.NoError(t, err)
		assert.NotNil(t, eng)
	},
},
```

described (`:527-528`) as *"the **regression guard** for the narrowing: it passes today and
must keep passing."*

**Evidence (executed, `wt-failure-modes`).** There is no configuration in which human tasks
are *not* configured. `service/service.go:189-191`, inside the defaults block:

```go
if c.taskStore == nil {
	c.taskStore = humantask.NewMemTaskStore()
}
```

— applied on the **non-durable** path; and on the durable path `:302` `case c.taskStore == nil:`
is a hard validation error. So `c.taskStore` is non-nil in both branches by the time the
authorizer check would run, and `:217` unconditionally builds
`task.NewTaskService(c.taskStore, c.authz, …)`.

Probe (`service/zz_probe_fm_test.go`, `package service_test`,
`go test -count=1 -run '^TestZZProbeDefaultTaskStoreAlwaysPresent$' -v ./service/` →
`EXIT=0`, `--- PASS`):

```
NewProcessEngine() with NO options: OK, eng=*service.ProcessEngine
ClaimTask on a no-option engine: err=workflow-service: claim task: workflow-runtime: taskservice: get task: workflow-humantask: task not found
errors.Is(err, humantask.ErrTaskNotFound) = true
```

A zero-option engine has a **live, working** task service backed by the default
`MemTaskStore` — it answers `ErrTaskNotFound`, not a nil panic. `opts: nil` **is** an engine
with human tasks configured.

**Why it matters.** The implementer has exactly two options and both are bad:
- **Implement the predicate as `c.taskStore != nil`** — the only signal that exists. Then
  `NewProcessEngine()` with no options returns `ErrAuthorizerRequired`, plan case 4 fails, and
  **every consumer who never touches human tasks** — plus every existing test and example that
  constructs a bare engine — breaks. The ADR's "BREAKING in four places" accounting
  (`:314-318`) does not include this, and it would be the largest break in the bundle by far.
- **Weaken the check to make case 4 pass** — e.g. skip it whenever the store was defaulted.
  Then the durable path is the only one guarded, and the *"natural durable wiring lands on
  allow-all silently"* case the ADR opens with (`:69-70`) is the only one covered while the
  in-memory path keeps its silent allow-all. Decision 2 is then half-defeated, silently, and
  no test in the plan would notice.

This is the ADR-0165 class: a predicate whose stated intent is right and whose available
signal cannot express it, passed by an audit because everyone read the *intent*.

**Concrete proposed fix.** Give the predicate a real signal, and say what it is in the ADR:
1. Track **explicit intent**, not presence: add an unexported `c.humanTasksRequested bool`
   set by `WithHumanTasks`, by `WithDurableStore` (whose provider yields a task store), and by
   `WithAuthorizer`/`WithAllowAllAuthorizer`. Require an authorizer only when it is true. This
   makes `NewProcessEngine()` keep working and makes case 4 honest — it is testing
   "human tasks not *requested*", which is a thing that exists.
2. Rewrite ADR Decision 2's sentence to name the signal: *"…when human tasks are **explicitly
   requested** (`WithHumanTasks`, or a `DurableProvider` supplying a task store)…"* — the
   current wording is what made this look implementable.
3. Add a case the plan lacks: `WithDurableStore(p)` where `p` supplies a task store and no
   authorizer ⇒ `ErrAuthorizerRequired`. That is the ADR's own motivating scenario
   (`:69-70`) and no case in Task 4's table covers it.
4. Add a case asserting `NewProcessEngine()` (zero options) still succeeds **and** that its
   default posture is disclosed — otherwise fix (1) silently restores the exact silent
   allow-all Decision 2 exists to abolish. ⚠ Ask what the guard must STILL DO: the zero-option
   engine is the one every quickstart uses, and it must keep working *while* saying so.
### F15 — The plan's File Structure promises a "`RoleAuthorizer` denial" that no task implements; the new `AuthzSpec` contract is documented on a public type but enforced in exactly one package

**Severity: Major**

**What the bundle says.** Plan File Structure row for `authz/authz.go` (`:87`):
*"`AuthzSpec.Open`, two sentinels, **`RoleAuthorizer` denial**, declarations, three falsified
godocs"* — phase 0. And Task 3 Step 3 (`:434-437`) rewrites the public `AuthzSpec` godoc to:
*"A spec that states no dimension and is not Open **DENIES** (ADR-0185). Before ADR-0185 such
a spec allowed everyone; the zero value now fails closed…"*

**Evidence (executed).** No task step implements any `RoleAuthorizer` behaviour change. Task 2
(`:328-411`) adds `Dimension`, `DimensionEvaluator`, `EvaluatesDimension`, the two sentinels
and the two `EvaluatesDimension` methods. Task 3 (`:415-462`) adds the `Open` field and edits
godocs. Neither touches `RoleAuthorizer.Authorize`. The denial lives only in
`runtime/task`'s `checkSpecStated` (Task 8).

So the shipped behaviour is unchanged at the authorizer (probe `authz/zz_probe_fm_test.go`,
`--- PASS`, at the bundle commit):

```
RoleAuthorizer{}.Authorize(zero spec              , ZERO actor) = <nil>
RoleAuthorizer{}.Authorize(empty roles slice      , ZERO actor) = <nil>
RoleAuthorizer{}.Authorize(privileges only        , ZERO actor) = <nil>
AllowAll{}.Authorize(zero spec, ZERO actor)                     = <nil>
```

(The mixed roles+privileges case denies the *zero* actor because the role check fails — the
ADR's `:80-85` warning is about a *manager* actor and is correct as written; verified, not
assumed.) `internal/authz/casbin/authorizer.go:33` likewise documents *"An empty spec
allows."* and all three of its checks are `len(...) > 0` / `!= ""` guarded (`:45,:56,:68`), so
casbin allows an empty spec too — which is what makes `Open: true` work at all.

**Why it matters.** Two operational consequences.
1. **The File Structure row is an unbuilt promise.** An implementer following it will add a
   denial to `RoleAuthorizer.Authorize` — which *contradicts* the decision the ADR actually
   took (`:315-316`: audit 1 killed per-authorizer placement precisely so a consumer's own
   authorizer inherits the rule from above). The result is a double-deny path that makes
   `WithAllowAllAuthorizer()` and the `Open` marker behave differently depending on which
   authorizer is wired — the F1-class hole the bundle already fixed once.
2. **A public type now documents a contract its own package does not honour.**
   `authz.AuthzSpec`'s godoc will say an unstated spec DENIES; a consumer calling
   `authz.RoleAuthorizer{}.Authorize` directly — which is exported, documented module-root API
   — gets `nil`. The rule holds only for callers who go through `runtime/task`. That is a
   false claim in a committed godoc, the class CLAUDE.md's Delivery Gate item 2 exists to kill,
   and it is cheapest to kill now.

**Concrete proposed fix.**
1. **Delete "`RoleAuthorizer` denial" from the File Structure row** (`:87`) — it names a change
   the ADR deliberately does not make.
2. **Scope the godoc to the truth.** Rewrite Task 3's `AuthzSpec` godoc as: *"A spec that
   states no dimension and is not Open is REJECTED by the task service before any Authorizer
   is consulted (ADR-0185 Decision 3, enforced in `runtime/task`). Individual Authorizer
   implementations still treat an empty spec as permissive; do not rely on calling one
   directly."* State where the rule lives, since it does not live here.
3. Apply the same correction to the `internal/authz/casbin/authorizer.go:33` godoc the plan
   also queues for rewrite — *"An empty spec allows"* remains **true of that method** and must
   not be edited into a falsehood in the sweep.
4. Add an assertion to Task 8's table pinning the split explicitly: `RoleAuthorizer{}` still
   returns `nil` for the zero spec while `checkSpecStated` denies it. That is the invariant a
   future refactor would silently break, and nothing currently pins it.

---

## Summary — FAILURE-MODES lens

**15 findings: 7 Critical, 8 Major, 0 Minor.**

| # | claim | sev |
|---|---|---|
| F1 | migration's `"Open": true` into the definitions copy makes the whole `ProcessDefinition` undecodable (`DisallowUnknownFields`) | Critical |
| F2 | 0002 is irreversible: an OLD binary cannot decode a migrated definition ⇒ no rolling deploy, no rollback | Critical |
| F3 | the gate on all four verbs leaves NO repair verb — a missed task is unclaimable, uncompletable AND unreassignable | Critical |
| F4 | `examples/scenarios/manual_task` breaks on the authoring gate; `go build ./examples/...` cannot see it | Major |
| F5 | `WithAnonymousActorAllowed()` and the empty-`Actor.ID` rejection void each other — the three demo mains cannot claim | Critical |
| F6 | the empty-ID rule is scoped to Claim while its rationale (the audit record) points at Complete | Major |
| F7 | 401's position in `ClassifyError` unspecified; the bundle's own wrap-`ErrNotAuthorized` convention makes 401 unreachable | Major |
| F8 | a consumer decorator silently downgrades casbin to roles-only; nothing detects it at wiring time ⇒ F3 strand | Critical |
| F9 | `ErrUnevaluatableSpec` names no dimension, no authorizer, no method — the ADR's "error naming the capability" is not built | Major |
| F10 | the snapshot copy is written BACK over the task row on every transition, so a missed snapshot reverts the migration — the successful claim strands the task | Critical |
| F11 | the snapshot backfill is nested-array JSON surgery in 3 dialects, budgeted as "repeat for" | Major |
| F12 | YAML has its own strict tag set and never gets `eligible_open` — YAML authors stranded with no expressible remedy | Critical |
| F13 | the `model.Validate` blast-radius list is wrong both ways (measured: `definition/model` 7 + `engine` 3; `service`/`processtest` 0) | Major |
| F14 | Decision 2's "when human tasks are configured" has no implementable signal — a task store is ALWAYS present | Critical |
| F15 | File Structure promises a `RoleAuthorizer` denial no task builds; the new `AuthzSpec` godoc contradicts its own package | Major |

**Most important: F10.** Every other finding is a hole in a plan; F10 is a hole in the model
the plan is built on. The bundle's whole migration argument rests on spec §2.1's claim that
`wrkflw_human_task.eligibility` is the copy that matters and the snapshot is a passive
projection read only at rehydration. Executed, the opposite is true: the snapshot is the
*source* of the `UpdateTask` write-back that overwrites the task row on every transition. So a
snapshot the migration misses does not merely stay stale — it **reverts the repair**, and the
successful claim is the event that strands the task. Combined with F3 (no repair verb) the
result is unrecoverable loss of in-flight human work, produced by the migration written to
prevent exactly that.

**Verified-and-dropped** (recorded so the next lens does not re-chase them): the `examples/`
scenarios correctly route through `TaskService` before `ApplyTrigger` (checked
`attribute_authz:138`, `input_validation:136`) — they do not teach the bypass;
`service.ClaimTask` reaches the gate via `e.tasks.Claim` (`service/service.go:417`), so the
HTTP path is covered and residual 1 is correctly scoped; every `casbinauthz` construction
route returns `*casbinauthz.Authorizer` (`:177`, `:187`, `:216`), so the forwarding
declaration lands; `NewProcessEngine` already returns `(*ProcessEngine, error)`, so Decision 2
is not a signature break; the ADR's mixed roles+privileges warning (`:80-85`) is correct as
written; the single `0002_*.sql` per dialect is atomic under goose's default per-file
transaction (no `NO TRANSACTION` marker exists in the migrations tree).

**Not reached (UNVERIFIED)** — Docker was unavailable to this lens: the actual SQL behaviour of
any `0002` statement on Postgres/MySQL; `internal/persistence/store` round-trips; `./runtime/...`
beyond `calllink`, `signal`, `task` (so F13's `runtime` row is partial).

**Probe hygiene:** five throwaway probes written, run and deleted
(`definition/model/zz_probe_fm{,2,3}_test.go`, `engine/zz_probe_fm_test.go`,
`service/zz_probe_fm_test.go`, `authz/zz_probe_fm_test.go`); one mutation ablation on
`definition/model/validate.go` applied in **this lens's own worktree**, restored from a `cp`
backup, `diff` clean, `go test ./definition/model/` → `EXIT=0`. Worktree ends clean
(`git status --porcelain` empty).
