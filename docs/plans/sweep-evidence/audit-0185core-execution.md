# ADR-0185-core audit — EXECUTION lens

Worktree: `.../scratchpad/wt-execution`, detached at `5ce393f4`.
Bundle present (step 0 ✅): spec, ADR, plan all found.
Every finding below was established by RUNNING code, not by reading it.

### F1 — `Actor.Clone()` does NOT deep-copy `Attributes`; the plan's proposed godoc claims it does, and the plan's own verification step cannot detect the gap

**Severity: Major**

**What the bundle says.** Plan Task 1 Step 3 (`docs/plans/2026-08-23-authz-identity-core.md`,
the `ContextWithActor` godoc it prescribes verbatim for committed code):

> "The actor is cloned, so a later mutation of the caller's slices or maps cannot change
> what an authorization decision sees."

Plan Task 1 Step 5 is titled **"Verify `Actor.Clone` deep-copies `Attributes`"** and calls the
property load-bearing: *"this task now depends on it for a security property … If this fails,
`Clone` shallow-copies the map: **stop and report it**"*. The check it prescribes is a
**top-level** map write (`got.Attributes["tenant"] = "evil"`).

**What I ran** (`authz/zz_probe_test.go`, `package authz_test`, deleted after the run):

```
go test -count=1 -run '^TestZZProbe' -v ./authz/   → EXIT=0
PROBE top-level: orig.tenant=acme clone.tenant=evil     <- top level IS isolated
PROBE nested   : orig.nested.k=EVIL                     <- nested map SHARED
PROBE slice    : orig.slice[0]=EVIL                     <- nested slice SHARED
PROBE roles    : orig.roles[0]=manager                  <- Roles IS isolated
```

`authz/authz.go:49-57` uses `maps.Clone`, and its **own godoc already says so**
(`:43-45`: *"Attributes are cloned one level deep: nested maps and slices inside an
attribute value remain shared"*).

**Why it matters.** Two distinct defects, both shipping into committed code:

1. The prescribed `ContextWithActor` godoc is a **false claim about current behaviour**
   entering a comment — the exact class CLAUDE.md's Delivery Gate item 2 says is cheapest
   to kill before the gate. `Actor.Attributes` values are `any`; a middleware that puts a
   `map[string]any` claim set (the natural shape for JWT claims) into `Attributes` gets a
   context copy that **aliases** it.
2. Plan Task 1 Step 5's check **passes today** and would therefore be recorded as
   "verified", while the property it was written to establish is false. It is a test that
   cannot fail in the dimension it claims to police — the twelfth-and-counting instance of
   the class §7 of the spec itself warns about.

**Concrete fix.** (a) Rewrite the prescribed godoc to state the real bound, e.g. *"The actor
is cloned one level deep (see [Actor.Clone]): a later mutation of the caller's `Roles` slice
or of the top-level `Attributes` map cannot change what an authorization decision sees;
values nested inside an attribute remain shared."* (b) Replace Task 1 Step 5's check with a
**nested** mutation and make the expected result the *documented* one, so the step measures
the real boundary instead of a boundary that already holds. (c) If the deep property is
actually wanted, that is a change to `Actor.Clone` with its own blast radius
(`CloneActors`, cached human-task records, cloned instance state) — decide it explicitly,
do not let a godoc assert it.

---

### F2 — CONFIRMED (author's premise holds): `RoleAuthorizer` silently discards `Privileges`, including in the mixed spec

**Severity: n/a — confirmation, recorded because the whole capability-interface design rests on it**

Spec §2.3 row 2 and ADR Context finding 3's ⚠ paragraph claim the mixed spec
`{Roles:["manager"], Privileges:["finance-task approve"]}` passes on roles alone. Executed:

```
PROBE RoleAuthorizer zero spec / zero actor                  => err=<nil>
PROBE RoleAuthorizer empty roles slice / zero actor          => err=<nil>
PROBE RoleAuthorizer privileges-only / zero actor            => err=<nil>
PROBE RoleAuthorizer MIXED roles+privileges / manager        => err=<nil>   <- the dangerous one
PROBE RoleAuthorizer MIXED roles+privileges / zero actor     => err=workflow-authz: not authorized
PROBE RoleAuthorizer attribute only / zero actor, empty vars => err=<nil>   (backlog 103, deferred)
```

All four allow-legs reproduce. The premise is sound.

### F3 — CRITICAL: one malformed `eligibility` value aborts the WHOLE SQLite backfill, and the migration is the thing that stops the upgrade stranding every dimension-less task

**Severity: Critical**

**What the bundle says.** ADR Decision 3 / spec §5.2: *"A per-dialect `0002_*.sql` backfills
`"Open": true` into stored specs carrying no dimension, across all three durable copies, so
no ambiguous row survives into the new binary. After it runs, Go may safely treat absent as
deny."* Plan Task 14 Step 3: *"per dialect: Postgres `jsonb` operators, MySQL
`JSON_SET`/`JSON_CONTAINS_PATH`, SQLite `json1`."* Neither document mentions guarding on
JSON validity.

**What I ran** (`zzprobe/zz_sqlite_migration_test.go`, `dbtest.RunTestSQLite`, real schema
via `store.MigrateSQLite`, deleted after the run). Seven well-formed pre-upgrade rows plus
one whose `eligibility` is the text `not json at all`, then the obvious backfill:

```sql
UPDATE wrkflw_human_task
   SET eligibility = json_set(eligibility, '$.Open', json('true'))
 WHERE json_array_length(coalesce(json_extract(eligibility,'$.Roles'),      json('[]'))) = 0
   AND json_array_length(coalesce(json_extract(eligibility,'$.Privileges'), json('[]'))) = 0
   AND coalesce(json_extract(eligibility,'$.Attribute'),'') = ''
```

Without the bad row (`TestZZProbeSQLiteNaiveBackfill`, EXIT=0):

```
PROBE naive backfill rows_affected=4
PROBE after-naive t1-zero       {"Roles":null,"Privileges":null,"Attribute":"","Open":true}
PROBE after-naive t2-emptyslice {"Roles":[],"Privileges":[],"Attribute":"","Open":true}
PROBE after-naive t3-roles      {"Roles":["manager"],"Privileges":null,"Attribute":""}      <- correctly skipped
PROBE after-naive t4-privsonly  {"Roles":null,"Privileges":["finance-task approve"],...}    <- correctly skipped
PROBE after-naive t5-attronly   {"Roles":null,"Privileges":null,"Attribute":"vars.amount < 100"} <- correctly skipped
PROBE after-naive t6-emptyobj   {"Open":true}
PROBE after-naive t7-null       null                                    <- MATCHED but UNCHANGED (see F4)
```

With one malformed row present (`TestZZProbeSQLiteNaiveBackfillWithGarbageRow`, EXIT=0):

```
PROBE garbage-row backfill ERR=SQL logic error: malformed JSON (1)
PROBE after-garbage t1-zero  {"Roles":null,"Privileges":null,"Attribute":""}   <- NOT updated
... every single row unchanged ...
```

**Zero rows were migrated.** The statement is atomic and one bad value kills all of it. The
same behaviour shows up on a pure SELECT: `TestZZProbeSQLiteJSONInspect` returned 7 rows and
then `ROWS_ERR=SQL logic error: malformed JSON (1)` — a `json_extract` over a mixed column
fails *mid-stream*, so a migration that reads before it writes can also half-report.

**Why it matters.** This is SQLite-specific and the bundle never separates the dialects:
`wrkflw_human_task.eligibility` is `JSONB` in Postgres (`postgres/0001_init.sql:151`) and
`JSON` in MySQL (`mysql/0001_init.sql:139`) — both validate on write, so malformed content
cannot exist there. In SQLite it is **plain `TEXT`** (`sqlite/0001_init.sql:147`), validated
by nothing. The same asymmetry applies to `wrkflw_instances.snapshot` (JSONB / JSON / TEXT)
and `wrkflw_definitions.definition` (JSONB / JSON / TEXT).

The failure mode is the worst available: goose aborts the migration, the operator sees a
startup failure, and if they force past it (or if a partial/multi-statement migration lets
some statements land) **every dimension-less task denies** — the exact stranding Decision 3
built the migration to prevent. ADR-0187 already established that this repo's SQLite path
accepts what the other two reject; nothing in the bundle carries that forward.

**Concrete fix.**
1. In the SQLite `0002`, guard every `json_*` call with `json_valid(<col>) = 1` in the
   `WHERE`, so a malformed row is *skipped* rather than fatal — and add a companion
   `SELECT count(*) … WHERE json_valid(eligibility) = 0` reported as a warning, so skipped
   rows are visible rather than silent.
2. Write the plan's Task 14 Step 1 test to **seed a malformed row** in the SQLite fixture.
   As written ("seed a pre-upgrade row … assert `Open == true`") it cannot fail on this.
3. State in the ADR that the three dialects differ in column type and that only SQLite can
   hold non-JSON, so the three `0002` files are **not** transliterations of one another.

---

### F4 — a JSON `null` eligibility matches the backfill predicate, is reported as updated, and is left unchanged — a silently stranded task

**Severity: Major**

**What the bundle says.** ADR Decision 3: *"so **no ambiguous row survives** into the new
binary."* Spec §5.2 repeats it.

**What I ran** — same probe as F3, row `t7-null` with `eligibility = 'null'`:

```
PROBE naive backfill rows_affected=4        <- 4 reported, only 3 values actually changed
PROBE after-naive t7-null       null        <- still `null`, no Open key
```

`json_extract('null','$.Roles')` is SQL NULL, so the row satisfies every leg of the
"states no dimension" predicate; `json_set` on a JSON scalar cannot create an object member,
so it returns the document unchanged. SQLite still counts the row in `RowsAffected`.

**Why it matters.** Two things break at once. (a) The row survives as ambiguous, so after the
upgrade `json.Unmarshal("null", &spec)` leaves the zero `AuthzSpec` and the task **denies** —
stranded, which is the failure Decision 3 exists to prevent. (b) `RowsAffected` over-reports,
so any migration verification that counts rows (the natural thing for Task 14 Step 1 to
assert) reports success. A test asserting only `Open == true` on a *well-formed* seeded row
cannot see either.

**Concrete fix.** Restrict the predicate to `json_type(<col>) = 'object'` in all three
dialects, and handle non-object documents explicitly — either overwrite them with
`'{"Open":true}'` (a deliberate decision to grandfather) or leave them and **report** them.
Add `null`, a JSON array, and a JSON string to the plan's Task 14 fixture set; today the
prescribed fixture is one well-formed object and cannot discriminate.

### F5 — CRITICAL: the migration as specified writes the WRONG KEY into `wrkflw_definitions.definition`, and doing what the ADR literally says BRICKS every stored definition

**Severity: Critical**

**What the bundle says.** ADR Decision 3, "The migration": *"A per-dialect `0002_*.sql`
backfills **`"Open": true`** into stored specs carrying no dimension, **across all three
durable copies**."* Spec §5.2 repeats it verbatim: *"backfills `"Open": true` into stored
specs that carry no dimension, across the copies in §2.1"* — and §2.1's third copy is
`wrkflw_definitions.definition`.

**What I ran.** First, what a dimension-less `UserTask` actually looks like on disk
(`zzprobe/zz_defjson_test.go`, EXIT=0):

```
PROBE stored definition JSON:
{"id":"d1","version":1,"nodes":[{"id":"s","kind":"startEvent"},{"id":"ut","kind":"userTask"},
 {"id":"e","kind":"endEvent"}],"flows":[…]}
PROBE model.Validate(dimension-less userTask) => <nil>
PROBE decode after injecting "Open":true into the node        => err=json: unknown field "Open"
PROBE decode after injecting "eligible_open":true (field not yet added) => err=json: unknown field "eligible_open"
```

**Why it matters.** The definitions copy does **not** store an `authz.AuthzSpec`. It stores
`model.NodeWire`, whose eligibility fields are `eligible_roles` / `eligible_privileges` /
`eligible_expr` (`definition/model/node_wire.go:27-29`), and a dimension-less user task
carries **none of them** — the node object is literally `{"id":"ut","kind":"userTask"}`.
`"Open"` is not a key in that shape at any nesting level.

And this is not a harmless no-op: `ProcessDefinition.UnmarshalJSON` applies
`DisallowUnknownFields` inside the custom unmarshaler (ADR-0167 D1,
`node_wire.go:185-190`). So a migration that does what the ADR says produces
`json: unknown field "Open"` — **every migrated definition becomes undecodable**, on the
`Lookup(latest)` hot path, for every consumer, immediately after the upgrade. This is the
identical class as the stored-camelCase-keys hazard already in `HANDOVER.md` ("34/34 probed
tags rejected on the `Lookup(latest)` hot path"), except this bundle would *create* the bad
rows itself.

Note the second probe line: the correct key `eligible_open` is *also* rejected until Task 5
adds the Go field. That constrains ordering — the `0002` migration and the `NodeWire` field
must ship in the same binary, which they do, but the plan never states the dependency and
Phase 5 is dispatched as an independent phase.

**Concrete fix.**
1. Correct the ADR's and spec's migration sentence: the human-task and snapshot copies get
   `"Open": true` **inside an `AuthzSpec` object**; the definitions copy gets
   **`"eligible_open": true` inside each dimension-less `userTask` node of `$.nodes`**.
   They are two different surgeries on two different shapes, not "the same backfill across
   three copies".
2. Add a plan step that decodes a migrated definition through
   `model.ProcessDefinition.UnmarshalJSON` (not `json.Unmarshal` into a map) and asserts
   `err == nil` — the strict decoder is the only thing that catches a wrong key, and
   nothing in Task 14 currently exercises it.
3. State the ordering dependency explicitly: `0002` may only run against a binary whose
   `NodeWire` already declares `eligible_open`.

---

### F6 — CRITICAL: the definitions backfill cannot be written with the obvious `json_set`; the obvious form appends a PHANTOM NODE and makes the definition undecodable

**Severity: Critical**

**What the bundle says.** Plan Task 14 Step 3: *"Implement, per dialect: Postgres `jsonb`
operators, MySQL `JSON_SET`/`JSON_CONTAINS_PATH`, SQLite `json1`. Backfill **only** rows
whose spec states no dimension."* Step 4 adds *"`wrkflw_definitions.definition` is the copy
that keeps minting bad rows if skipped. Cover it."* No document acknowledges that this
target is an **array of node objects**, not a single spec object, or that a per-element
conditional update needs a different SQL construct entirely.

**What I ran** (`zzprobe/zz_sqlite_defs_test.go` / `zz_sqlite_defs2_test.go`, real schema via
`dbtest.RunTestSQLite`, sqlite_version=3.53.2, EXIT=0). Seeded one definition with a
dimension-less `userTask` (`ut`) and a roles-bearing one (`ut2`).

Attempt 1 — the `JSON_SET`-shaped statement the plan names:

```sql
UPDATE wrkflw_definitions SET definition = json_set(definition, '$.nodes[#].eligible_open', json('true'))
```
```
PROBE attempt1 err=<nil>                         <- SUCCEEDS SILENTLY
PROBE phantom-node result: … {"id":"e","kind":"endEvent"},{"name":"x"}] …
PROBE phantom-node decode err=workflow-definition: node kind not registered
      (blank-import .../definition/kinds): "unspecified"
```

`[#]` in SQLite json1 means *append past the end*, so the statement adds a **new array
element** rather than touching any node. The definition then fails to decode at all. (The
phantom-node run used the pre-existing key `name` so the failure is attributable to the
phantom element and not to F5's unknown-field rejection.)

Attempt 2 — what actually works: a full `json_each` + `json_group_array` rebuild of the
array:

```sql
UPDATE wrkflw_definitions
   SET definition = json_set(definition, '$.nodes',
        (SELECT json_group_array(
             CASE WHEN json_extract(e.value,'$.kind') = 'userTask'
                   AND json_extract(e.value,'$.eligible_roles') IS NULL
                   AND json_extract(e.value,'$.eligible_privileges') IS NULL
                   AND coalesce(json_extract(e.value,'$.eligible_expr'),'') = ''
                  THEN json_set(e.value,'$.eligible_open', json('true'))
                  ELSE json(e.value) END)
           FROM json_each(wrkflw_definitions.definition,'$.nodes') e))
 WHERE json_valid(definition)
```
```
PROBE rebuild-only:
{"id":"d1","version":1,"nodes":[{"id":"s","kind":"startEvent"},
 {"id":"ut","kind":"userTask","eligible_open":true},
 {"id":"ut2","kind":"userTask","eligible_roles":["manager"]},
 {"id":"e","kind":"endEvent"}],"flows":[…]}
```

Correct — and node order is preserved on SQLite.

**Why it matters.** The plan hands the implementer a construct that *succeeds* and corrupts.
It also understates the work by an order of magnitude: this is a three-dialect array-rebuild,
not a `JSON_SET`. Two further hazards follow from the rebuild shape and are
**ASSUMPTION (unverified — no Docker in this worktree)**:

- **MySQL 8**: the equivalent is `JSON_TABLE` + `JSON_ARRAYAGG`, and MySQL documents
  `JSON_ARRAYAGG` as **not guaranteeing result order**. A reordered `nodes` array is a
  silent semantic change to every stored definition. Must be verified before Task 14 is
  written, not after.
- **Postgres**: `jsonb_array_elements` + `jsonb_agg` needs
  `WITH ORDINALITY … ORDER BY ord` to preserve order; `jsonb_agg` over an unordered subquery
  does not.

**Concrete fix.**
1. Replace Task 14 Step 3's one-line dialect note with the three real statements, each
   written and executed against its dialect (Docker) before the phase is dispatched.
2. Make node-order preservation an **asserted** property of the migration test, not an
   assumption: seed a multi-node definition, migrate, decode, and compare the node ID
   sequence. Nothing in the plan asserts it today.
3. Add a negative fixture with a `userTask` that already states a dimension and assert it is
   byte-identical after the migration (the rebuild rewrites the whole array, so "untouched"
   is no longer free).

### F7 — `Open: true` produces a task that NO actor can find: both `ClaimableBy` implementations match on role overlap only, and the bundle never touches the read path

**Severity: Major** (Critical if "Open" is meant to be usable rather than merely permitted)

**What the bundle says.** ADR Decision 1: *"`Open` (Decision 3) therefore means **any
authenticated actor**"*. Decision 3 makes `activity.WithOpenEligibility()` the supported
replacement for the empty spec, and `model.Validate` will **force** every author of a
dimension-less `UserTask` to choose it. The migration deliberately stamps `Open: true` onto
every dimension-less stored task and definition. No document mentions `ClaimableBy`, task
listing, or candidate resolution.

**What I ran** (`zzprobe/zz_claimable_test.go`, EXIT=0) — four unclaimed tasks in a
`humantask.MemTaskStore`, queried by a manager and by a role-less actor:

```
PROBE ClaimableBy(actor=alice roles=[manager]) => [b-roles]
PROBE ClaimableBy(actor=bob   roles=[])        => []
PROBE StaticActorResolver.Candidates(spec={Roles:[] …}) => []
PROBE StaticActorResolver.Candidates(spec={Roles:[manager] …}) => [{alice [manager] map[]}]
```

`a-emptyspec`, `c-privsonly` and `d-attronly` are returned to **nobody**. The rule is
role-overlap-or-candidate in both implementations —
`humantask/memory.go:103` and `internal/persistence/store/humantask_store.go:251`
(`htCandidateContains(t.Candidates, actor.ID) || htHasRoleOverlap(actorRoles, t.Eligibility.Roles)`).
An `Open` spec states no roles, and `StaticActorResolver` resolves no candidates from it, so
an open task matches neither leg.

**Why it matters.** After this bundle, the *only* supported way to author "anyone may act" is
`WithOpenEligibility()`, and a task authored that way is **unclaimable in practice**: it never
appears in any actor's inbox and has no candidates. It can still be claimed by a caller who
already holds the token, which is exactly the discoverability posture the engine's own inbox
API exists to remove. The migration then creates this shape *at scale*, on every pre-upgrade
dimension-less row — so "no work is stranded" is true only in the narrow sense that a direct
token POST still succeeds.

The bundle's composition argument stops at the four `Authorize` sites. `ClaimableBy` is a
**fifth** eligibility-evaluating path and it encodes its own copy of the "who may act" rule.
Note also the asymmetry this creates in the other direction: after the gate lands, a
privileges-only spec under `RoleAuthorizer` **denies** at `Authorize` while `ClaimableBy`
already hides it — consistent by accident, not by design.

**Concrete fix.** Decide and record what `Open` means to the read path, then implement it:
either (a) `ClaimableBy` returns every unclaimed `Open` task to every actor — the reading
that matches the ADR's "any authenticated actor" — with the SQL leg done in Go beside the
existing filter (both implementations already post-filter in Go, so this is cheap); or
(b) `Open` is explicitly documented as *permitted but not listed*, and
`WithOpenEligibility()`'s godoc says so. Silence is not an adjudication. Add a
`ClaimableBy`-level test for an `Open` task in whichever direction is chosen — there is none
today, in either store.

---

### CONFIRMED (no finding) — the mint site is singular and `AwaitHuman` reuses the same variable

Plan Task 8a Step 5 asks for this to be re-derived, not assumed. Derived over the whole repo,
non-test:

```
grep -rn "AuthzSpec{" --include='*.go' . | grep -v _test.go
  processtest/taskstoreconformance.go:119   (a fixture, not a mint)
  engine/step_nodes.go:723                  <- the only mint
grep -rn "Eligibility:" --include='*.go' . | grep -v _test.go
  runtime/processdriver_action.go:464  Eligibility: cmd.Eligibility   (durable projection of the command)
  engine/step_nodes.go:732             Eligibility: spec
  engine/step_nodes.go:811             AwaitHuman{TaskID: taskID, Eligibility: spec}
```

`engine/step_nodes.go:811` does reuse the `spec` variable built at `:723`, so one edit covers
both. The four `Authorize` sites re-derive as `runtime/task/service.go:199,234,255,306` —
also confirmed. `wrkflw_journal` stores `trigger`, not commands, so it is **not** a fourth
durable copy of `AuthzSpec`.

### F8 — ⚠⚠ CRITICAL: the plan's own `checkSpecStated` FAILS the plan's own case 5. The capability interface does NOT dissolve the D2×D3 interaction, and the bundle asserts three times that it does

**Severity: Critical** — this is the exact interaction that killed the previous revision.

**What the bundle says.** Three separate assertions:

- Spec §1 removal grid, row **D2 × D3**: *"if the gate is hoisted above the authorizer,
  `WithAllowAllAuthorizer()` stops meaning allow-all … D2 and D3 contradict each other unless
  the gate is authorizer-aware. **Resolved in §4**."*
- Spec §5.3 declarations table: *"`authz.AllowAll` | all three ⇒ **dissolves D2 × D3**;
  `WithAllowAllAuthorizer()` keeps meaning allow-all"*.
- ADR Decision 3: *"`AllowAll` declares all three — which is what keeps
  `WithAllowAllAuthorizer()` honest"*.
- Plan Task 8 Step 1, **case 5**, marked *"⚠ Case 5 is re-audit's D2×D3 interaction. If it
  fails, `WithAllowAllAuthorizer()` has stopped meaning allow-all"*:

```go
{
    name: "AllowAll is not broken by the gate",
    az:   authz.AllowAll{},
    spec: authz.AuthzSpec{},
    assert: func(t *testing.T, err error) { assert.NoError(t, err, …) },
},
```

**What I ran.** I applied the bundle's own code to the real tree: added `Open bool` to
`authz.AuthzSpec`, created `authz/dimension.go` exactly as plan Task 2 Step 3 prescribes
(including `AllowAll.EvaluatesDimension` returning `true` for everything), and copied plan
Task 8 Step 3's `checkSpecStated` **verbatim** into `zzprobe/zz_gate_test.go`. EXIT=0:

```
PROBE gate P1 states nothing, RoleAuthorizer           => DENY(states-nothing)
PROBE gate P2 explicitly open, RoleAuthorizer          => ALLOW
PROBE gate P3 MIXED roles+privs, RoleAuthorizer        => DENY(unevaluatable)
PROBE gate P4 MIXED under all-dims                     => ALLOW
PROBE gate P5 AllowAll + empty spec                    => DENY(states-nothing)   ← plan asserts ALLOW
```

**Cases 1–4 produce the claimed verdicts. Case 5 does not.**

**Why.** `EvaluatesDimension` is only consulted **inside** the loop, and the loop `continue`s
on every dimension the spec does not state. For the empty spec all three legs are skipped,
`stated` stays `false`, `spec.Open` is `false`, and the function returns
`ErrSpecStatesNothing` — regardless of what the authorizer declares. **Declaring dimensions
can only rescue the *unevaluatable* leg; it is structurally incapable of rescuing the
*states-nothing* leg.** The two legs are orthogonal, and the bundle's central "authorizer-
aware gate dissolves the contradiction" argument conflates them.

**Why it matters.** The bundle ships with a self-contradiction in its most load-bearing task:
Task 8 Step 1 case 5 is RED against Task 8 Step 3, so the implementing agent will either
(a) delete the case, (b) weaken it, or (c) "fix" the gate by special-casing `AllowAll` —
and only (c) preserves the documented posture, at the cost of the hoisted gate no longer
being authorizer-agnostic. The re-audit's finding **F1**-class hole is therefore *not* closed;
it has been moved from `Privileges` to the empty spec.

Concretely, after this bundle a consumer who chose `service.WithAllowAllAuthorizer()` — the
explicit, supported permissive posture Decision 2 creates — still gets **403** on any task
whose spec states nothing: every `authz.AuthzSpec{}` written by a consumer-implemented
`TaskStore`, every `MemTaskStore` fixture, every `processtest` harness table row, and every
pre-upgrade row on a store the migration did not reach (spec §6 residual 2 names exactly
that population).

**Concrete fix — the bundle must pick one and say so.**
1. **Make `Open` implied by the authorizer**, i.e. the states-nothing leg also consults the
   authorizer: skip the `ErrSpecStatesNothing` return when the authorizer declares that an
   unstated spec is acceptable. That needs a *fourth* capability question
   (e.g. `AcceptsUnstatedSpec() bool`) — `EvaluatesDimension` cannot express it. `AllowAll`
   answers true; everything else false.
2. **Accept that allow-all no longer covers unstated specs**, delete plan case 5, and correct
   all three documents: spec §1's D2×D3 row is *not* resolved, §5.3's "dissolves D2 × D3" is
   false, and ADR Decision 3's "keeps `WithAllowAllAuthorizer()` honest" is false. Then state
   the migration/consumer consequence in Consequences/Negative.
3. **Special-case `AllowAll` by type** in the gate — cheapest, but it silently excludes
   `casbinauthz` and any consumer wrapper of `AllowAll`, so it must be written as an explicit
   documented exception, not an `if`.

Whichever is chosen, plan Task 8 Step 1's case 5 must be re-derived, because as written it is
a prescribed test the prescribed implementation cannot pass.

---

### F9 — `Open: true` combined with an unevaluatable dimension DENIES; nothing in the bundle covers it, and `model.Validate` will happily author it

**Severity: Major**

**What the bundle says.** ADR Decision 3: `Open` means *"any authenticated actor may act"*.
`model.Validate` *"rejects a `UserTask` carrying **neither** the open marker **nor** any
eligibility dimension"* — so `WithOpenEligibility()` **plus** `WithEligiblePrivileges(…)`
is explicitly valid authoring. The plan's five-case gate table has no such row.

**What I ran** (same verbatim `checkSpecStated`, EXIT=0):

```
PROBE gate X1 Open:true AND privileges, RoleAuthorizer => DENY(unevaluatable)
PROBE gate X2 Open:true AND attribute,  RoleAuthorizer => ALLOW
PROBE gate X3 privileges-only,          RoleAuthorizer => DENY(unevaluatable)
PROBE gate X8 AllowAll + privileges                    => ALLOW
```

X1: an author writes `NewUserTask("t", WithOpenEligibility(), WithEligiblePrivileges("x"))`.
`model.Validate` accepts it. Under the default `RoleAuthorizer` the gate returns
`ErrUnevaluatableSpec` and the task is **unclaimable by anyone**, including the "any
authenticated actor" `Open` promises. `Open` does not act as an escape hatch because the
unevaluatable check runs *before* the `spec.Open ||` test.

**Why it matters.** The authoring gate and the runtime gate disagree about the same spec:
one accepts it, the other refuses every actor. That is precisely the "looks configured and
is not" failure mode Context finding 3 exists to kill, re-created in a new shape by this
bundle's own additions.

**Concrete fix.** Decide the precedence explicitly and encode it in **both** gates. Either
(a) `Open` short-circuits — move the `if spec.Open { return nil }` test *above* the loop, so
an open spec is never refused for an unevaluatable dimension (and say in the godoc that an
open spec's other dimensions are advisory under an authorizer that cannot evaluate them); or
(b) `Open` + a dimension is a **contradiction rejected at authoring time** by
`model.Validate`, so it can never reach the runtime gate. Add the case to plan Task 8's table
either way — today the table cannot discriminate between the two designs.

---

### F10 — `AuthzSpec` has no JSON tags and no `omitempty`, so the durable key is `"Open"` (capital) and **every** stored spec grows the key; also, no test in the repo pins the durable eligibility shape

**Severity: Minor** (raises the cost of getting F5's migration path wrong)

**What I ran** (with the field added, EXIT=0):

```
PROBE marshalled open spec:  {"Roles":null,"Privileges":null,"Attribute":"","Open":true}
PROBE marshalled roles spec: {"Roles":["manager"],"Privileges":null,"Attribute":"","Open":false}
PROBE decode lowercase {"open":true} => Open=true
PROBE decode uppercase {"OPEN":true} => Open=true
```

And, with `Open bool` added to `authz.AuthzSpec` and `authz/dimension.go` created:

```
go build ./...  → EXIT=0
go vet   ./...  → EXIT=0     (compiles the Docker-only test packages too)
go test -count=1 ./authz/... ./engine/... ./runtime/{task,signal,calllink}/... ./service/...
        ./processtest/... ./transport/http/... ./internal/atrest/... ./definition/...
        ./humantask/... ./casbinauthz/...   → EXIT=0, zero failures
```

**Why it matters.** Three things worth writing down before Phase 5:

1. The durable key is **`"Open"`**, not `"open"`. Plan Task 3 Step 3's prescribed godoc says
   *"wire key `"open"`"* — see F11. SQL JSON paths are case-sensitive in all three dialects,
   so `json_extract(…,'$.open')` finds nothing in a row Go wrote. Go's own decoder is
   case-insensitive (both probe lines above), which means a wrong-case migration would
   **appear to work in a Go round-trip test and fail in SQL**.
2. There is no `omitempty`, so after the upgrade every re-saved spec carries
   `"Open":false` — including specs the migration skipped. Any migration predicate written
   against *key absence* rather than *dimension absence* becomes wrong the moment a row is
   re-saved. The plan's predicate is dimension-based, which is correct; state that it is
   deliberate.
3. Plan Task 3 Step 4 says to *"run `go build ./...` to enumerate the repo-wide compile
   breakage this field introduces. **Record the list** — it is the work of phases 1–4."*
   Executed: **the list is empty**. Adding the field breaks nothing and fails no test. The
   breakage in phases 1–4 comes entirely from the *DTO field removals*, not from
   `AuthzSpec`. Correct the step so the implementer is not hunting for a list that does not
   exist. It also means **no existing test pins the durable eligibility shape** — a silent
   change to the at-rest JSON of a policy column went unnoticed by the whole suite,
   including `internal/atrest`.

### F11 — the new 503 arm cannot be placed as the plan assumes: a wrapped resolver error is swallowed by the 403 arm at position 2, and a 503 with a message violates the switch's own documented 5xx policy

**Severity: Major**

**What the bundle says.** Plan Task 9 Step 4: *"`ok == false` ⇒ `ErrNoAuthenticatedActor` ⇒
**401** … Resolver error ⇒ `ErrActorResolutionFailed` ⇒ **503**. ⚠ **Arm the 401/503 arms
relative to the existing ordered arms.** ADR-0186 put the 413 arm *before* the ordered 400
arm for exactly this reason. Read `errors.go`'s existing order before inserting."* That
warning names the 413/400 pair and nothing else.

**What I ran** (`transport/http/httpcore/zz_probe_test.go`, EXIT=0) — the two proposed
sentinels put through the real `httpcore.ClassifyError`:

```
PROBE classify bare ErrNoAuthenticatedActor                       => 500 internal_error msg=""
PROBE classify bare ErrActorResolutionFailed                      => 500 internal_error msg=""
PROBE classify resolution failure WRAPPING authz.ErrNotAuthorized => 403 forbidden  msg="…actor resolution failed: …not authorized"
PROBE classify resolution failure WRAPPING kernel.ErrInstanceNotFound => 404 not_found msg="…"
PROBE classify no-actor wrapped in ErrBadInput                    => 400 bad_request msg="…"
PROBE classify ErrSpecStatesNothing-shaped (wraps ErrNotAuthorized)=> 403 forbidden  msg="…"
```

**Why it matters.** `ClassifyError`'s arm order (`transport/http/httpcore/errors.go:35-88`) is
404 → **403** → 409 → 413 → 400 → 422 → 500. Two hazards the plan's warning does not cover:

1. **A wrapped resolver error never reaches a 503 arm placed after the existing ones.** The
   repo's universal idiom for a resolver failure is `fmt.Errorf("%w: %w", sentinel, err)`
   (`stdlib/body.go:143`, `gin/groups.go:38`, `fiber/groups.go:149` all do it). An
   authentication resolver that returns `authz.ErrNotAuthorized` — the single most likely
   error an identity resolver produces, and the sentinel this very module exports for it —
   turns "the IdP is down" into **403 forbidden** with the IdP's message echoed to the
   caller. That is precisely the "downgrade" ADR Decision 1 says must not happen
   (*"a transient identity-provider failure must not become an open door"*), reached by a
   different route: not an open door, but an indistinguishable one, and it hands the client
   a permanent-looking 403 for a retryable outage.
   Moving the 503 arm to the top does **not** fix it — it would then swallow a genuine
   404/403 from the same wrap.
2. **`ClassifyError`'s godoc (`:32-33`) states: *"For 5xx the Message is empty; callers log
   the raw error instead of exposing it."*** A 503 arm must therefore return an empty
   `Message`, unlike every 4xx arm. Nothing in the bundle says so, and the natural
   copy-paste from the neighbouring arms produces a 503 that leaks the resolver's error
   text — which for an IdP client routinely contains a URL, a tenant id, or a token
   fragment.

**Concrete fix.**
1. Do **not** wrap the resolver's error inside the classification sentinel. Have the endpoint
   detect a non-nil resolver error and return `ErrActorResolutionFailed` **bare**, logging
   the cause via `slog` (the pattern `stdlib/body.go:128-131` already documents for the
   oversize sentinel: *"The oversize sentinel is passed to writeErr BARE. Wrapping it in
   `ErrBadInput` … would be silently absorbed by the ordered switch"*). That comment is the
   repo's existing convention for exactly this problem and the bundle should cite it.
2. State the two new arms' positions and their co-match set, per the STANDING INVARIANT
   already written into `errors.go:51-54`: *"state its position relative to the arms it can
   co-match, and carry a test asserting an error matching two arms resolves to the intended
   one."* Add that test for both new arms — the plan prescribes none.
3. Give the 503 arm an empty `Message`, and say why, beside the 413 arm's identical note.

---

### F12 — CONFIRMED: all three adapters tolerate an unknown body key, so "ignored, not rejected" holds. Plan Task 9 Step 4's open question is answered — and `httpcore.Actor` becomes an orphaned public type

**Severity: Minor** (the orphan half)

Plan Task 9 Step 4 asks the implementer to *"verify the decoder does not use
`DisallowUnknownFields`; if it does … an ignored key would become a **400** and silently
break every existing client. **Check, and report what you find**."* Answered by execution so
the implementer does not have to:

- **stdlib** — `transport/http/stdlib/body.go:143`, plain `json.NewDecoder(body).Decode(dst)`,
  no `DisallowUnknownFields`, no trailing-data check.
- **gin** — `ShouldBindJSON`; `EnableDecoderDisallowUnknownFields` is set nowhere in the repo
  (grepped: the only `DisallowUnknownFields` hits are `runtime/kernel/cursorcodec.go` and
  `definition/model/node_wire.go`). Measured (`transport/http/gin/zz_probe_test.go`, EXIT=0):
  ```
  PROBE gin claim body=map[actor:map[id:alice] totally_unknown:1] => status=404   (decoded fine)
  ```
- **fiber** — `c.Bind().JSON(&in)`. Measured (`transport/http/fiber/zz_probe_test.go`, EXIT=0):
  ```
  PROBE fiber claim body=map[actor:… totally_unknown:1] => status=404
     body={"error":"not_found","message":"…task not found"}
  ```

**The claim holds.** Only `model.ProcessDefinition` is strict (ADR-0167), and the task DTOs
are not decoded through it.

⚠ **The orphan.** After all three fields are removed, `httpcore.Actor`
(`transport/http/httpcore/dto.go:11-15`) has **no non-test referent** — grepped, its only
remaining uses are `dto_test.go:47` and six `endpoints_test.go` sites, all of which the pin
rewrite deletes. The bundle's "BREAKING in four places" list does not mention it. Decide
explicitly: remove the exported type (a fourth breaking change, and the honest one) or keep
it with a godoc saying why it survives. Leaving a dead exported struct in the package
CLAUDE.md calls "the product" is the worse option.

---

### F13 — CONFIRMED: fiber's `c.SetContext` propagates and `c.Locals` does not

Spec §3 / ADR Decision 1: *"⚠ For fiber the middleware idiom is `c.SetContext`, not
`c.Locals` — `c.Locals` does not propagate into the context `httpcore` receives."*
Measured against fiber v3.4.0 (`transport/http/fiber/zz_probe_test.go`, EXIT=0):

```
PROBE fiber SetContext value seen in handler ctx = from-SetContext
PROBE fiber Locals value visible via c.Context().Value("probe") = <nil> ; via c.Locals = from-Locals
```

`DefaultCtx.Context()` (`fiber@v3.4.0/ctx.go:134-144`) reads a fasthttp user value written
only by `SetContext`. The premise is sound and the API exists in the pinned version.

⚠ One thing the bundle does not say and should: `c.Context()` returns
**`context.Background()`** when no middleware called `SetContext` (`ctx.go:136-142`) — it is
not the request's cancellation context. So on fiber the actor-bearing context has no request
deadline unless the consumer's middleware builds one. Worth one sentence in `SECURITY.md`
beside the `SetContext` guidance.

### F14 — CRITICAL: an existing test **forbids** a second migration file per dialect, and the bundle neither mentions it nor plans to amend it

**Severity: Critical** (it blocks the delivery outright, and the fix is an ADR decision)

**What the bundle says.** Spec §2.7: *"Migrations today: **exactly one file per dialect** …
⚠ Cross-delivery consequence: this bundle lands the repo's first `0002_*.sql`"* — stated as
an incidental fact whose only consequence is ADR-0187's parked defect. Plan Task 14 creates
`migrations/{postgres,mysql,sqlite}/0002_authz_open.sql`. Nothing in spec, ADR or plan
mentions `TestMigrations_OneFilePerDialect` or ADR-0132.

**What I ran.** Created the three `0002_authz_open.sql` files in my worktree with a trivial
`UPDATE` body, then:

```
go test -count=1 -run '^TestMigrations_OneFilePerDialect$' -v ./internal/persistence/store/
EXIT=1
  --- FAIL sqlite:   "[migrations/sqlite/0001_init.sql migrations/sqlite/0002_authz_open.sql]"
                     should have 1 item(s), but has 2
  --- FAIL postgres: … same …
  --- FAIL mysql:    … same …
```

All three subtests fail.

**Why it matters.** `internal/persistence/store/migrations_count_test.go:11-15` is not an
incidental assertion — its godoc says it *"enforces the project requirement (ADR-0132) that
every supported database dialect ships a SINGLE consolidated migration file. Adding a second
`*.sql` file to any dialect directory … **fails here**."* This bundle's central mechanism is
precisely that.

There is also a **contradiction between the guard and the ADR it cites**, which the bundle
must adjudicate rather than route around. ADR-0132's Context explicitly anticipates this
bundle: *"Once released, adding schema changes will resume as new numbered files on top of
the consolidated baseline; this squash is a one-time pre-1.0 cleanup, **not a new policy of
rewriting history**."* So ADR-0132 permits `0002`; its guard does not. One of them is wrong,
and the ADR-0185 bundle is the delivery that discovers it.

**Concrete fix.**
1. Add an explicit plan step: relax `TestMigrations_OneFilePerDialect` to assert
   "`0001_init.sql` is the single **consolidated baseline**, and any additional file is
   strictly numbered above it and never edits `0001`" — the invariant ADR-0132 actually
   argues for — rather than a raw `Len == 1`.
2. Annotate **ADR-0132 in place** recording that its guard was stricter than its decision,
   and that ADR-0185 is the first delivery past the baseline. (The bundle already carries
   an "amend ADR-0117 in place" step; this is the same shape and is currently missing.)
3. Fold the guard change into this bundle's commit — it is not a follow-up; without it the
   repo-wide `go test ./...` gate in Phase 7 cannot pass.

---

### F15 — CRITICAL: the at-rest parser FAILS CLOSED on `UPDATE`, so a data-only `0002` breaks `TestSecurityMdInSync` and `scripts/gen-at-rest.sh` — and the ADR-0187 interaction the bundle names is the WRONG one

**Severity: Critical**

**What the bundle says.** ADR Consequences and spec §2.7, identically: *"this bundle lands
the repo's **first `0002_*.sql`**, which **activates ADR-0187's parked latent defect**: a
`CREATE INDEX` naming a table declared in a different migration file derives no `keyed` fact
**silently**."* Plan Task 15 Step 1: *"Run `scripts/gen-at-rest.sh`, confirm it round-trips
with a clean tree, and **if the new file trips the defect**, fix it here."*

**What I ran.** With the three trivial `0002_authz_open.sql` files present (body:
`UPDATE wrkflw_human_task SET eligibility = eligibility;`):

```
go test -count=1 -run '^TestSecurityMdInSync$' -v ./internal/atrest/
EXIT=1
  render_test.go:409: Received unexpected error:
    workflow-atrest: parse …/migrations/mysql/0002_authz_open.sql:
    workflow-atrest: unrecognised statement: UPDATE wrkflw_human_task SET eligibility = eligibility
--- FAIL: TestSecurityMdInSync
```

**Why it matters.** Three things, in order of importance:

1. **The named premise is wrong.** `internal/atrest/schema.go:114-134` handles exactly three
   statement forms — `CREATE TABLE`, `CREATE INDEX`, `CREATE UNIQUE INDEX` — and its
   `default` arm returns an error, deliberately: *"Fail closed (ADR-0187 D11): a migration
   statement this parser does not recognise (e.g. a future ALTER TABLE) must never be
   silently skipped."* This bundle's `0002` is a **pure data migration** — no DDL at all —
   so it does not "silently derive no `keyed` fact". It **hard-fails the parser**, and with
   it the drift guard and the generator script. The `CREATE INDEX`-across-files caveat the
   bundle cites is real but irrelevant here: this migration creates no index.
2. **Task 15 Step 1 is written for the wrong outcome.** "Confirm it round-trips with a clean
   tree" cannot succeed; `gen-at-rest.sh` will abort on the same parse error before it can
   regenerate anything. The step needs to be "extend the parser", not "confirm and, if
   tripped, fix".
3. **The fix is an ADR-0187 amendment, not a code tweak.** Teaching `ParseSQL` to skip DML
   (`UPDATE`/`INSERT`/`DELETE`) **weakens D11's fail-closed rule**, which was written to stop
   exactly this kind of "add a branch so my file parses" pressure. Per rule #11 the ADR must
   be amended in the same bundle, with this measurement, stating the new boundary: *DDL is
   parsed and classified; DML is explicitly recognised and contributes no schema; anything
   else still fails closed.* An `ALTER TABLE` must keep failing.

**Concrete fix.**
1. Add a `hasKeywordPrefix(stmt, "UPDATE")` / `"INSERT"` / `"DELETE"` branch to
   `ParseSQL`'s switch that records nothing and returns nil, with a comment naming
   ADR-0185 as the delivery that introduced data-only migrations, and keep the `default`
   fail-closed arm intact.
2. Add a test asserting `ALTER TABLE` still errors (the existing
   `TestParserFailsClosedOnUnrecognisedStatements`, `discover_test.go:216`, must keep
   passing and should gain the DML-is-accepted case beside it).
3. Amend ADR-0187 D11 in place with the boundary above and the measured error string.
4. Rewrite plan Task 15 Step 1 to prescribe the parser extension, and re-derive which
   ADR-0187 caveat is actually activated — the `CREATE INDEX` one is not.

---

### F16 — CONFIRMED: spec §2.1's snapshot claim survives a REAL store round-trip — and the snapshot copy is also a nested ARRAY, so it needs F6's rebuild too

**Severity: Minor** (the array half; the confirmation half is a confirmation)

Spec §2.1 evidenced the snapshot claim with `json.Marshal(engine.InstanceState)` in a
throwaway `engine_test`. Re-run independently **through the real SQLite store**
(`zzprobe/zz_snapshot_test.go`: `store.New(db, dialect.NewSQLite())`, `Create`, raw
`SELECT snapshot`, then `Load`), EXIT=0:

```
PROBE raw snapshot column:
{"InstanceID":"i1",…,"Tasks":[{"TaskID":"t1","InstanceID":"i1","NodeID":"ut",
 "Eligibility":{"Roles":["manager"],"Privileges":["finance-task approve"],
 "Attribute":"vars.amount < 100"},"Candidates":null,"State":0,…}],…}
PROBE Load err=<nil>
PROBE rehydrated Eligibility = {Roles:[manager] Privileges:[finance-task approve] Attribute:vars.amount < 100}
```

The claim holds end-to-end, and `capHistory` (`internal/persistence/store/history_cap.go`)
trims only `History`, never `Tasks`, so nothing strips it. Backlog 141 is real.

⚠ **What the bundle misses:** the snapshot's specs live at `$.Tasks[*].Eligibility` — a
**nested array**, exactly like the definitions copy. So **two of the three** durable copies
need F6's `json_each`/`JSON_TABLE`/`jsonb_array_elements` rebuild, not a `JSON_SET`. Only
`wrkflw_human_task.eligibility` is a flat single-object column. Plan Task 14 treats all three
as one kind of statement. Also worth stating: a snapshot rebuild rewrites **every row of
`wrkflw_instances`**, which on a large deployment is a long-running, table-wide write inside
a goose transaction — a migration-window cost the bundle does not mention.

### F17 — the authoring gate's blast radius, MEASURED: 14 test functions in 3 packages — and the plan names the wrong three

**Severity: Major**

**What the bundle says.** Plan Task 5: *"The failed bundle claimed 'only 5 `NewUserTask` call
sites reach `model.Validate`' and the re-audit **falsified it** … **Re-derive the affected set
across all three forms before changing `Validate`**, and report the number you get — do not
inherit `5` or `≥13`. **Expect to fix fixtures in `engine/`, `runtime/`, `processtest/` and
`service/`**."*

**What I ran.** I implemented the gate in the real tree — inserted, in
`definition/model/validate.go`'s existing single-pass `KindUserTask` loop (the natural site,
beside `ErrManualTaskValidation`), a rejection when the wire form states no
`eligible_roles`/`eligible_privileges`/`eligible_expr` — then ran the whole suite:

```
go test -count=1 ./... > /tmp/blast.log 2>&1 ; EXIT=1
grep -c "PROBE eligibility not stated" /tmp/blast.log  →  22 occurrences
```

Failing test functions, by package (my own throwaway probe excluded):

| package | count | tests |
|---|---|---|
| `definition/model` | **7** | `TestValidateUserTaskOutcomes`, `TestParseYAMLUserTaskManual`, `TestParseYAMLUserTaskManualImmediate`, `TestParseYAMLUserTaskOutcomes`, `TestPersistedDefinitionRoundTripsThroughStrictJSON`, `TestYAMLNodeLabel`, `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` |
| `engine` | **3** | `TestSignalFiresEveryMatchingBoundaryArm`, `TestMessageDeliveryStillFiresOnlyTheFirstArm`, `TestSignalDoesNotFireAnArmThisDeliveryCreated` |
| `runtime` | **4** | `TestManualTaskCompletesOnBareTrigger`, `TestManualWaitTaskRejectsPayload`, `TestImmediateManualTaskAutoCompletes`, `TestForceTerminationWarnings` |
| **total** | **14** | in **3** packages |

`processtest`, `service`, `runtime/task` and all four `transport/http` packages were
**clean** (`internal/persistence/store` also passed, 66.4 s).

**Why it matters.** The plan's predicted set (`engine`, `runtime`, `processtest`, `service`)
is wrong in **both** directions: it misses `definition/model`, which carries **half** the
failures and all of the YAML-authoring ones, and it names two packages that are unaffected.
An implementer who fans out by the plan's list dispatches two agents with nothing to do and
none to the package that needs the most work. Since `definition/model` is also the package
Task 5 edits, the fixture repair is **inside** the changing package — that is a serialization
constraint the plan's "fan out by Go package" rule needs to know about.

Note the composition of the `definition/model` failures: five of the seven are **YAML
decoding** tests, including `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` — the ADR-0167
strict-tag test. YAML is one of the three authoring forms the plan warns about, and it is the
one that breaks hardest.

**Concrete fix.** Replace the plan's guessed package list with this measured one, and add
`definition/model` fixture repair as an explicit step inside Task 5 (not a fan-out target).

---

### F18 — CRITICAL: Decision 3 breaks ADR-0118's blessed "no-eligibility UserTask" and a shipped `examples/` main — and the bundle amends only ADR-0117

**Severity: Critical** (a documented, ADR-blessed authoring pattern is silently removed)

**What the bundle says.** ADR-0185's header amends *"**ADR-0117 Decisions 1 and 3**"* and its
Relates-to line names ADR-0118 only as context. Spec §5.1 and plan Task 17 list exactly two
godocs and one ADR to correct. Nothing mentions that ADR-0118 **depends** on the rule being
removed.

**What I ran.** With the same gate mutation live, the shipped example:

```
go run ./examples/scenarios/manual_task/    →  EXIT=1
2026/08/23 23:33:31 build def:workflow-definition: PROBE eligibility not stated: node "handOverBadge"
workflow-definition: PROBE eligibility not stated: node "recordOrientation"
exit status 1
```

The source (`examples/scenarios/manual_task/main.go:45-46`):
```go
Add(activity.NewUserTask("handOverBadge", activity.WithManual(false))).
Add(activity.NewUserTask("recordOrientation", activity.WithManual(true))).
```

And **ADR-0118 Consequences, first bullet** (`docs/adr/0118-manual-user-task.md:85-89`):

> "A form-less human checkpoint is expressible as `NewUserTask("confirm", WithManual(false))`
> (wait) or `NewUserTask("noted", WithManual(true))` (immediate) — **a no-eligibility
> UserTask. This is why ADR-0117's optional eligibility was a prerequisite.**"

**Why it matters.** ADR-0118's *entire manual-task ergonomic* is the no-eligibility user task,
and it names ADR-0117's optional eligibility as its **prerequisite**. ADR-0185 removes that
prerequisite. So the amendment set is not "ADR-0117 Decisions 1 and 3" — it is ADR-0117
**and ADR-0118**, and ADR-0118's consequence bullet becomes false the day this ships. Plan
Task 17's checklist would leave a reader of ADR-0118 alone actively misled, which is the exact
harm Task 17 cites for ADR-0117.

There is also a design question the bundle never asks: **should a manual task be exempt?**
A `WithManual(true)` immediate task auto-completes with no actor at all, so requiring an
eligibility declaration on it is ceremony with no authorization content. Either exempt manual
tasks from the gate (and say so in both ADRs), or accept that every manual checkpoint must now
carry `WithOpenEligibility()` — and then fix `examples/scenarios/manual_task/main.go`, which
plan Task 16 does not list (it names only the three wiring mains).

**Concrete fix.**
1. Add ADR-0118 to the amended-in-place set, correcting its Consequences bullet.
2. Decide and record whether `Manual` user tasks are exempt from `ErrEligibilityNotStated`;
   add the case to plan Task 5's table either way (today it has three cases, none manual).
3. Add `examples/scenarios/manual_task/main.go` to plan Task 16, and add
   `go run ./examples/scenarios/...` (or at minimum a `Build()`-level test over every example
   definition) to Phase 7 — `go build ./examples/...`, which is all Task 16 prescribes,
   **passes** on a definition that fails validation at run time.

### F19 — CRITICAL: "human tasks are configured" is not a state that exists. A bare `NewProcessEngine()` already has a fully working human-task path on `AllowAll`, so D2's rule either breaks every default engine or closes nothing

**Severity: Critical**

**What the bundle says.** ADR Decision 2 / spec §4: *"`NewProcessEngine` returns an error
**when human tasks are configured** and neither option supplied an authorizer. Allow-all
becomes a thing you say, not a thing you get."* Plan Task 4's table pairs
case 1 — `WithHumanTasks(store, nil)` ⇒ `ErrAuthorizerRequired` — with
case 4 — *"no human tasks configured needs no authorizer"*, `opts: nil`, `require.NoError`,
labelled *"the **regression guard** for the narrowing: it passes today and must keep passing."*

**What I ran** (`zzprobe2/zz_service_test.go`, EXIT=0):

```
PROBE NewProcessEngine() err=<nil> engine!=nil=true
PROBE ClaimTask on a bare engine =>
  workflow-service: claim task: workflow-runtime: taskservice: get task:
  workflow-humantask: task not found
```

An engine built with **zero options** reaches the task service and the task store. It fails
with *"task not found"*, not *"no task store configured"* — because
`service/service.go:188-190` unconditionally defaults `c.taskStore = humantask.NewMemTaskStore()`
in the non-durable path, and `:199-201` then defaults `c.authz = authz.AllowAll{}`.

**Why it matters.** There is no configuration state meaning "human tasks are not configured".
Every non-durable engine has them, always. So Decision 2's predicate must be one of:

- **`c.taskStore != nil`** — then *every* default `NewProcessEngine()` errors, and plan Task 4
  **case 4 fails**. It is not a regression guard; it is the case the rule breaks.
- **"the consumer called `WithHumanTasks`"** — a new intent flag that does not exist today
  (and note `WithHumanTasks(store, nil)` currently records *nothing* about authorization:
  `service/options.go:82-84` ignores a nil `az`). Under this reading case 4 passes — but the
  **default MemTaskStore path keeps its silent `AllowAll`**, and that path is fully
  claimable/completable over HTTP through `stdlib.Mount`. Context finding 2's hole survives
  in the single most common wiring, while the ADR claims *"Constructing a `ProcessEngine`
  without an authorizer is an error"*.

Neither reading matches the ADR's title, and the plan's table silently assumes the second
while the ADR's prose asserts the first.

**Concrete fix.** Pick the predicate explicitly and write it into the ADR:
1. Add `c.humanTasksRequested bool`, set by `WithHumanTasks` **and** by `WithAuthorizer`/
   `WithAllowAllAuthorizer`/`AuthorizerProvider`, and require an authorizer only then — then
   state plainly in Consequences that the **defaulted in-memory task store stays allow-all**,
   which is a documented, deliberately-not-closed residual (spec §6 has no such entry today);
   **or**
2. Require an authorizer for every engine and accept that `NewProcessEngine()` with no
   options now fails — a much larger break than "29 pins", touching every consumer and most
   of this repo's own tests. Then delete plan Task 4 case 4 and re-derive the break count.

Either way the plan's Task 4 table cannot stand as written: cases 1 and 4 encode reading 2,
and the ADR's Decision text encodes reading 1.

---

### F20 — spec §2.6 names the wrong second "vacuous 403" pin: `gin/gin_coverage_test.go:244` asserts **404**, and the real second 403 is `stdlib/errors_test.go:158`

**Severity: Major** (a false claim inside the bundle's own evidence section, and it misroutes a mutation step)

**What the bundle says.** Spec §2.6, ADR Decision 1 and plan Task 10-13 Step 2 all state,
identically: *"⚠ **Two of the 29 must be REWRITTEN, not recompiled**:
`stdlib/errors_test.go:187` and `gin/gin_coverage_test.go:244` **assert 403**, and after D1
they would still return 403 **from the zero actor** — passing while testing nothing.
**Confirmed present in the enumeration above** (both are `"by"` sites)."*

**What I derived and ran.** Every `StatusForbidden` assertion in the five pin packages:

```
grep -rn "StatusForbidden" transport/http/{httpcore,stdlib,gin,fiber}/*_test.go transport/http/parity/*_test.go
  transport/http/httpcore/errors_test.go:37   (a pure ClassifyError unit test — no request, no actor)
  transport/http/stdlib/errors_test.go:158    <- Complete, "Forbidden actor → service error"
  transport/http/stdlib/errors_test.go:190    <- Reassign, "Unauthorized reassigner"
```

There is **no 403 assertion anywhere in the gin package**. `gin_coverage_test.go:241-248`:

```
241: func TestTaskRoutes_Reassign_ErrorPath(t *testing.T) {
243: 	resp := post(t, newTaskSrv(t), "/tasks/bad-token/reassign", map[string]any{
244: 		"from": "alice", "to": "carol", "by": map[string]any{"id": "alice"},
246: 	if resp.StatusCode != http.StatusNotFound {
247: 		t.Fatalf("want 404, got %d", resp.StatusCode)
```

Line 244 is the **body literal** of a test that asserts **404** on a *nonexistent token*. Its
status is independent of the actor entirely — it is not a vacuous 403, and rewriting it to
"assert the reason: a 401 for no actor, or a 403 for a present-but-unauthorized actor"
(plan Task 11 Step 2) would change what the test is *for*.

The stdlib half is correct: `errors_test.go:187` is the `"by"` line of a test asserting 403 at
`:190`, and the zero actor (no roles) would still be denied by `RoleAuthorizer`, so it would
indeed pass while testing nothing. **But its sibling `stdlib/errors_test.go:154-158` — the
Complete path, actor `bob`/`viewer` — has exactly the same defect and is named nowhere in the
bundle.**

**Why it matters.** Three downstream effects. (a) Plan Task 11 (gin) is briefed to rewrite a
test that does not need rewriting and to mutation-prove a RED that cannot occur — the agent
will either force a wrong change or stall. (b) Plan Task 10 (stdlib) is briefed for **one**
rewrite where **two** are needed. (c) The claim is marked *"Confirmed present in the
enumeration above"* in a section (§2.6) whose whole purpose is a re-derived count — this is a
finding **inside the bundle's own evidence file**, which spec §8 predicted and which this lens
was asked to look for.

**Concrete fix.** Replace both citations with the derived set: the two vacuous-403 pins are
`transport/http/stdlib/errors_test.go:158` (Complete) and `:190` (Reassign); gin has none.
Re-brief Task 10 for two rewrites and drop the rewrite step from Task 11. Derive the set with
`grep -rn StatusForbidden` rather than by memory, and record the grep in §2.6 beside the
count.

---

### CONFIRMED (no finding) — spec §2.3 row 5 and §2.5's D2 anchors

- `processtest.SpyAuthorizer.Authorize` (`processtest/spyauthz.go:44-58`) does allow when
  `decide == nil` (`var err error; if decide != nil { err = decide(...) }; … return err`), and
  its constructor godoc says *"allows every actor until programmed otherwise"*. Spec §2.3's
  ⚠ is correct.
- `service/service.go` defaults `c.authz = authz.AllowAll{}` when nil; `WithHumanTasks`
  (`service/options.go:77-86`) nil-guards both arguments. Decision 2's premise that
  `WithDurableStore` never writes `c.authz` is consistent with what I read, though I did not
  execute an option-ordering matrix — **ASSUMPTION (unverified) in this lens**; leave it to
  the counting lens.

### CONFIRMED (no finding) — goose accepts a data-only `0002` on SQLite, and the json_valid-guarded predicate works

With a real `migrations/sqlite/0002_authz_open.sql` containing only the guarded `UPDATE`
from F3's fix, `store.MigrateSQLite` (via `dbtest.RunTestSQLite`) applied it cleanly:

```
PROBE goose_db_version rows=3
PROBE goose version=0 applied=true
PROBE goose version=1 applied=true
PROBE goose version=2 applied=true
```

So the migration *mechanism* is fine. Everything that goes wrong (F3, F4, F5, F6, F14, F15)
is in the SQL, the target shapes, and the two guards that forbid the file existing at all.

---

## Summary — EXECUTION lens

**20 findings: 8 Critical, 8 Major, 3 Minor, plus 5 confirmations.**

| # | severity | one line |
|---|---|---|
| F1 | Major | `Actor.Clone` is one level deep; the prescribed `ContextWithActor` godoc over-claims, and Task 1 Step 5's check cannot detect it |
| F2 | — | confirmed: `RoleAuthorizer` silently discards `Privileges`, mixed spec included |
| F3 | **Critical** | one malformed `eligibility` value aborts the ENTIRE SQLite backfill (`malformed JSON`, 0 rows migrated) |
| F4 | Major | a JSON `null` spec matches the predicate, counts as affected, and is left unchanged → stranded |
| F5 | **Critical** | the migration writes `"Open"` into `wrkflw_definitions.definition`, whose shape is `NodeWire`; strict decoding then rejects **every** migrated definition |
| F6 | **Critical** | the obvious `json_set(…'$.nodes[#]'…)` APPENDS a phantom node and makes the definition undecodable; the real fix is a 3-dialect array rebuild |
| F7 | Major | `Open: true` tasks are returned by `ClaimableBy` to **nobody** — both stores match on roles/candidates only |
| F8 | **Critical** | the plan's own `checkSpecStated` FAILS the plan's own case 5; the capability interface cannot dissolve D2×D3, and 3 documents claim it does |
| F9 | Major | `Open` + an unevaluatable dimension denies everyone, and `model.Validate` will happily author it |
| F10 | Minor | durable key is `"Open"`; no `omitempty`; `go build ./...` breakage from the field is **empty**, contradicting Task 3 Step 4 |
| F11 | Major | a wrapped resolver error is swallowed by the 403 arm at position 2; a 503 with a message violates the switch's documented 5xx policy |
| F12 | Minor | confirmed all three adapters tolerate unknown keys; `httpcore.Actor` becomes an orphaned public type |
| F13 | — | confirmed: fiber `SetContext` propagates, `Locals` does not (+ `c.Context()` is `Background()` when unset) |
| F14 | **Critical** | `TestMigrations_OneFilePerDialect` FORBIDS a `0002` file; all three subtests fail. Unmentioned by the bundle |
| F15 | **Critical** | the at-rest parser fails closed on `UPDATE` → `TestSecurityMdInSync` and `gen-at-rest.sh` break; the ADR-0187 interaction the bundle names is the wrong one |
| F16 | Minor | snapshot claim confirmed through a real store; but `$.Tasks[*]` is also an array → F6's rebuild applies to two of three copies |
| F17 | Major | authoring-gate blast radius MEASURED: 14 tests in `definition/model`(7)/`engine`(3)/`runtime`(4); the plan names `processtest` and `service`, which are clean, and misses `definition/model` |
| F18 | **Critical** | Decision 3 breaks ADR-0118's blessed no-eligibility manual task and `examples/scenarios/manual_task` (`go run` → EXIT=1); only ADR-0117 is amended |
| F19 | **Critical** | "human tasks are configured" is not a state that exists — a bare `NewProcessEngine()` already serves human tasks on `AllowAll`; D2 either breaks every default engine or closes nothing |
| F20 | Major | `gin/gin_coverage_test.go:244` asserts **404**, not 403; the real second vacuous pin is `stdlib/errors_test.go:158` |

**The single most important finding is F8**: the plan prescribes a test (Task 8 Step 1 case 5)
that the plan's own prescribed implementation (Task 8 Step 3) cannot pass, and the
contradiction is exactly the D2×D3 interaction that killed the previous revision. Spec §1,
spec §5.3 and ADR Decision 3 each assert it is dissolved by the capability interface; it is
structurally not, because `EvaluatesDimension` gates only the *unevaluatable* leg and the
empty spec never reaches that leg.

**Runner-up, and the one that would ship silent data corruption: F5 + F6.** Following the
ADR's migration sentence literally writes the wrong key into the wrong shape, and following
the plan's `JSON_SET` hint appends a phantom node — both producing definitions that no longer
decode, on the `Lookup` hot path, with no test in the bundle that would catch either.

**Cleanup:** every probe file was deleted; the worktree is back to `git status --short` empty
and `go build ./...` clean.
