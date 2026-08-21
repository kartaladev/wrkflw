# RE-AUDIT of the B3 authz/security bundle — FAILURE MODES, GAPS, CONTRADICTIONS lens

Bundle commit: `dd76a17b` (REVISION of the 2026-08-20 draft that failed with 58 findings / 12 Critical).
Auditor lens: failure modes, gaps, cross-document contradictions, zombie scope.
Worktree: `.../scratchpad/reaudit-fail` (detached at `dd76a17b`).
Container-free: no Docker. All probes are `go build`/`go test`/`grep` in this worktree.

Findings appended as confirmed. Ranked index at the end.

---
## F1 — CRITICAL — The hoisted `CheckSpecStated` gate is authorizer-AGNOSTIC, but the rule it enforces is authorizer-DEPENDENT. As specified it destroys resource-privilege authorization for every deployment, casbin included.

**Claims attacked (three documents, mutually inconsistent):**

- ADR-0185 D3: *"A spec with **any** non-empty `Privileges` returns `authz.ErrUnevaluatableSpec`
  wrapped in `ErrNotAuthorized` **under `RoleAuthorizer`** … A consumer who wants privileges
  evaluated wires the casbin authorizer, which does evaluate them."*
- ADR-0185 Consequences/Positive: *"the spec-shape gate is evaluated in `runtime/task` before all
  four `Authorize` calls, so a consumer's own `Authorizer` inherits it too."*
- Plan §3 phase 2: *"`func CheckSpecStated(spec AuthzSpec) error` — the gate phase 5 hoists above all
  four `Authorize` sites so **every** `Authorizer`, including a consumer's own, inherits it."*
- Plan §3 phase 2 test 5: *"`TestCheckSpecStated` — **table mirroring test 1**, asserting the gate is
  decidable **without** an `Authorizer`."* — and test 1's table contains `Privileges`-only and
  `{Roles:["manager"], Privileges:["x"]}`, both required to satisfy `errors.Is(err, ErrUnevaluatableSpec)`.
- Plan §3 phase 5: *"call `authz.CheckSpecStated(task.Eligibility)` **before** `s.authz.Authorize(...)`
  at all **four** sites"* — unconditionally, with no authorizer discrimination.

**Evidence.** Two gates are conflated into one function.

1. "Is anything stated?" — a property of the **spec alone**. Decidable without an authorizer. This is
   what a hoisted gate can legitimately enforce.
2. "Can *this* authorizer evaluate what is stated?" — a property of the **(spec, authorizer) pair**.
   `CheckSpecStated(spec AuthzSpec) error` has no authorizer parameter and therefore cannot decide it.

The plan's signature and its test 5 commit to (2) while the ADR scopes (2) to `RoleAuthorizer`.
Re-derived: `internal/authz/casbin/authorizer.go:56-64` evaluates `spec.Privileges` for real —

```
  56	  if len(spec.Privileges) > 0 {
  57	      ok, err := a.anyPrivilege(actor, spec.Privileges)
  ...
  62	          return authz.ErrNotAuthorized
```

so a `{Roles, Privileges}` spec is a **fully evaluatable, correct** spec under the baseline
authorizer CLAUDE.md names. Hoisting `CheckSpecStated` above `Authorize` denies it before casbin is
ever consulted. Net effect: **`AuthzSpec.Privileges` becomes unusable repo-wide**, which contradicts
CLAUDE.md's own requirement (*"role, resource-privilege, **and attribute-based**"*) and empties
ADR-0185 D3's own escape sentence ("wire the casbin authorizer").

**Verdict: CONFIRMED.** The three documents cannot all be implemented. This is a hole the revision's
own fix (hoisting, accepted Critical D of the previous audit) opened.

**Proposed fix.** Split the gate explicitly, in all three documents:

- `authz.CheckSpecStated(spec) error` — hoisted, authorizer-agnostic, enforces **only**
  *"`Open` is nil-or-true, or at least one of `Roles`/`Privileges`/`Attribute` is non-empty"*.
  Privileges COUNT as a stated dimension here.
- The unevaluatable-dimension denial stays **inside `RoleAuthorizer.Authorize`** where the
  authorizer identity is known, exactly as ADR-0185 D3's prose (but not the plan) says.
- Rewrite plan phase 2 test 5 so it does NOT mirror test 1: it must assert
  `CheckSpecStated({Roles:["manager"],Privileges:["x"]}) == nil` and
  `CheckSpecStated({Privileges:["x"]}) == nil` — the control that stops the hoist from
  swallowing casbin. Without that control the over-applying implementation ships green.

---

## F2 — CRITICAL — D5's `reassign` privilege token, placed in `AuthzSpec.Privileges`, is applied by casbin to **all four** verbs. Requiring it to reassign makes it required to claim and complete.

**Claim attacked.** ADR-0185 D5: *"a reassignment whose `by.ID` differs from the current claimant
requires an explicit **`reassign` privilege token** in the spec — the seam Decision 3's privileges
leg opens — rather than bare eligibility."*

**Evidence.** All four `runtime/task` verbs authorize against the **same single** `task.Eligibility`
spec — re-derived:

```
runtime/task/service.go:199:  s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars)   // Claim
runtime/task/service.go:234:  s.authz.Authorize(ctx, task.Eligibility, by,    task.Vars)   // Reassign
runtime/task/service.go:255:  s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars)   // Complete
runtime/task/service.go:306:  s.authz.Authorize(ctx, task.Eligibility, by,    task.Vars)   // RefreshCandidates
```

There is exactly one spec per task and no per-verb spec anywhere. `internal/authz/casbin`'s step 2
(`authorizer.go:56-64`) is **unconditional**: whenever `len(spec.Privileges) > 0`, the actor must
hold at least one of them, on **every** `Authorize` call. So putting `"task reassign"` into
`Eligibility.Privileges` to gate reassignment simultaneously makes it a precondition for **Claim**,
**Complete** and **RefreshCandidates**. The ordinary user the task was minted for can no longer
claim it.

Under `RoleAuthorizer` the same spec is worse, per F1/D3: the whole task denies everyone.

⚠ This is the "ask what the guard must STILL DO" failure the brief warns about. The ADR reasons
only about what the privilege must *refuse* (a non-claimant reassign) and never asks what the spec
carrying it must still *permit* (claim, complete, refresh by ordinary eligible actors).

**Verdict: CONFIRMED.** D5's mechanism is unimplementable as written under either in-repo authorizer.
The plan's own §0 item 4 flags the D3 half; the four-verbs-one-spec half is not flagged anywhere.

**Proposed fix.** The reassign privilege must not live in the task's single `Eligibility` spec.
Concrete options, one of which the revision must pick and record:

- **(a)** A separate `AuthzSpec` field or a distinct spec for elevated verbs, e.g.
  `humantask.HumanTask.ReassignEligibility *authz.AuthzSpec`, authorized only at `:234`, defaulting
  to nil = "claimant only". This is additive, snapshot-compatible (nil is the pre-upgrade state,
  matching D3's tri-state precedent) and does not disturb the other three verbs.
- **(b)** `TaskService.Reassign` builds an ad-hoc spec
  `authz.AuthzSpec{Privileges: []string{s.reassignPrivilege}}` (a service-level option, default
  `"task reassign"`) and calls `Authorize` a **second** time with it, only when
  `by.ID != claimant`. The task's own spec is untouched.
- **(c)** Drop the privilege entirely and make non-claimant reassign refuse unconditionally
  (`ErrNotClaimant`), with the escalation path left to the consumer. Smallest, and consistent with
  D5's stated purpose.

Whichever is chosen, ADR-0185 D5, plan phase 5, and plan phase 5 test 2/3 must be rewritten
together, and a **control** must be added: *"a task whose reassign gate is set is still claimable
and completable by an ordinary eligible actor"* — without it the over-applying implementation ships
and the suite stays green.

---
## F3 — CRITICAL — The tri-state migration is designed against the WRONG TABLE. `AuthzSpec` is not stored in `wrkflw_instances.snapshot`; it is a dedicated column in `wrkflw_human_task`. Phase 6's migration as prescribed would run green and change nothing.

**Claims attacked (three documents say the same wrong thing):**

- ADR-0185 D3: *"The instance snapshot is `json.Marshal`ed
  (`internal/persistence/store/store_core.go:81`) and read back with a plain `json.Unmarshal`
  (`:174`), and `AuthzSpec` has no json tags."*
- Evidence file §5 heading: *"Tri-state `Open` across the upgrade, **through the real snapshot
  codec**"*, citing `store_core.go:81`, `:231`, `:174`.
- Plan §4: *"the snapshot is `json.Marshal`ed with no tags and read with a plain `json.Unmarshal`."*
- Plan phase 6 test 1: *"write a **snapshot** with the pre-upgrade `AuthzSpec` shape, run the
  migration, assert `Open == ptr(true)`."*

**Evidence — re-derived.**

`store_core.go:81` marshals `capHistory(step.State, …)` into `wrkflw_instances.snapshot`; `:174`
unmarshals it into `engine.InstanceState`. Re-derived `engine.InstanceState`'s field list
(`engine/state.go:271-282`): `InstanceID, DefID, DefVersion, Status, Variables, StartVariables,
Tokens, StartedAt, EndedAt` (+ history/timers/incidents). **There is no human-task field and no
`AuthzSpec` anywhere in it** — verified: `grep -rn "AuthzSpec{" --include='*.go' . | grep -v _test`
returns exactly **two** sites, `engine/step_nodes.go:723` and `processtest/taskstoreconformance.go:119`;
neither is in an instance snapshot.

The real codec is a different file, a different table and a different column:

```
internal/persistence/store/humantask_store.go:157   eligibility, err := json.Marshal(t.Eligibility)
internal/persistence/store/humantask_store.go:186   ... eligibility, candidates, vars, ...   -> INSERT INTO wrkflw_human_task
internal/persistence/store/humantask_store.go:397   if len(eligibility) > 0 {
internal/persistence/store/humantask_store.go:398       json.Unmarshal(eligibility, &t.Eligibility)
```

and the schema, re-derived in all three dialects:

```
migrations/sqlite/0001_init.sql:134,147     CREATE TABLE wrkflw_human_task ( … eligibility TEXT  NOT NULL … )
migrations/postgres/0001_init.sql:138,151   CREATE TABLE wrkflw_human_task ( … eligibility JSONB NOT NULL … )
migrations/mysql/0001_init.sql:126,139      CREATE TABLE wrkflw_human_task ( … eligibility JSON  NOT NULL … )
```

**Verdict: CONFIRMED, partly mitigating.** The tri-state *mechanism* survives — `json.Marshal` /
plain `json.Unmarshal` / no tags / no `DisallowUnknownFields` (re-derived: the only
`DisallowUnknownFields` non-test sites are `runtime/kernel/cursorcodec.go:45` and
`definition/model/node_wire.go:190`, neither on this path), so a pre-upgrade `eligibility` value
still decodes to `Open == nil`. But **every citation supporting it is wrong**, and the plan's phase-6
migration, written against `wrkflw_instances.snapshot`, would touch **zero** rows carrying an
`AuthzSpec`. Test 1 as written ("write a snapshot with the pre-upgrade AuthzSpec shape") is
constructible only by putting an `AuthzSpec` somewhere it never goes — a **vacuous fixture** of
exactly the class this repo has shipped repeatedly.

**Proposed fix.**
1. Re-anchor ADR-0185 D3, evidence §5 and plan §4 to `humantask_store.go:157` (write) / `:398` (read)
   and to `wrkflw_human_task.eligibility` in all three dialect DDLs.
2. Rewrite plan phase 6 as an `UPDATE wrkflw_human_task SET eligibility = <spec with Open:true>
   WHERE state = 'unclaimed'/'claimed' AND <eligibility has no dimension and no Open key>` — with the
   JSON predicate written **per dialect** (`jsonb_…`/`->>` for Postgres, `JSON_EXTRACT` for MySQL,
   `json_extract` for SQLite), which is a materially larger job than the plan budgets for.
3. Re-word phase 6 test 1 to write a **task row**, not a snapshot, and add a fixture assertion that
   the row is readable through `humantask_store`'s own decoder before and after.

---

## F4 — CRITICAL — `Open *bool` makes the Go zero value of a PUBLIC-API struct fail-OPEN. `nil` is reachable from three authoring paths the bundle says it is not reachable from.

**Claim attacked.** ADR-0185 D3: *"`nil` is a **migration state, not a supported authoring state**:
`model.Validate` refuses to *mint* it, so the population can only shrink."* Plan §4: *"`nil` is never
authorable — `model.Validate` (phase 3) rejects it and the engine (phase 4a) mints
`ptr(true)`/`ptr(false)`, never `nil`. So the grandfathered population can only shrink."*

**Evidence.** `AuthzSpec` is an exported struct in the **module-root public package** `authz` — the
thing CLAUDE.md calls the product — and it is embedded **by value** in two more exported root-package
types. Re-derived:

```
authz/authz.go:82           type AuthzSpec struct { Roles; Privileges; Attribute }   (+ Open *bool)
humantask/humantask.go:97   Eligibility authz.AuthzSpec      // field of exported HumanTask
engine/command.go:150       Eligibility authz.AuthzSpec      // field of exported AwaitHuman command
```

`model.Validate` cannot police any of these. Three reachable producers of `Open == nil` that are not
pre-upgrade rows:

1. **A consumer writing `authz.AuthzSpec{Roles: …}` in Go.** Zero-valued `Open` is `nil`. This is the
   normal idiom for a Go struct literal and the repo does it itself
   (`processtest/taskstoreconformance.go:119`, non-test, a **shipped public helper**).
   `authz.AuthzSpec{}` with no dimension at all ⇒ grandfathered open ⇒ **allow-all**, which is
   verbatim the fail-open ADR-0185 exists to close.
2. **A consumer-implemented `humantask.TaskStore`.** `TaskStore` is an exported interface
   (`humantask/humantask.go:185`) and a supported extension point; anything it returns with a
   zero `Open` is grandfathered open, forever, with no migration reachable from `internal/`.
3. **`humantask.MemTaskStore`** (`humantask/memory.go:22`), the in-memory store used by reference
   wiring, has no upgrade at all — every `HumanTask` a consumer hands it carries whatever `Open` the
   Go literal had.

⚠ Contrast with the rejected `Open bool`: its zero value **denies**. The tri-state fixed the
stranding failure by inverting the zero value from fail-closed to fail-**open**, in the public API,
and the ADR asserts the opposite ("the population can only shrink") without ever considering the
Go-literal path. The stranding fix and the fail-open fix now pull in opposite directions and the
bundle only states one of them.

**Verdict: CONFIRMED.** This is a hole the revision's own fix opened, and it is the same class of
defect (an unstated dimension defaulting to permit) the bundle was written to eliminate.

**Proposed fix — one of, stated explicitly in ADR-0185 D3:**

- **(a) Narrow `nil`'s meaning to the decode path only.** Make grandfathering a property of the
  *store decoder*, not of the type: `humantask_store.go`'s row decoder (and `MemTaskStore`, and the
  documented `TaskStore` contract) sets `Open = ptr(true)` when the decoded JSON **lacked the key**,
  using `json.RawMessage`/`map[string]json.RawMessage` key presence rather than nil-ness. Then
  `RoleAuthorizer` can treat `nil` as **deny**, restoring a fail-closed zero value. This is the only
  option that keeps both properties.
- **(b) Keep nil⇒open but state the cost loudly**: add to ADR-0185 D3 Negative that the public
  zero-value `AuthzSpec{}` is allow-all, add a `//nolint`-proof godoc warning on the field, and add a
  `humantask.Validate` write-side rejection of `Eligibility.Open == nil` for **newly created** tasks
  (ADR-0183's precedent — `humantask.Validate` already runs on every store write at
  `humantask_store.go:153`), so a consumer-built task cannot introduce nil after the migration.
- Either way, add a plan test: `TestZeroValueAuthzSpecDeniesInGoConstructedTask` — it fails against
  the current design, which is the point.

---
## F3-AMENDMENT (self-correction, and it makes F3 WORSE, not better)

My first pass said the snapshot carries no `AuthzSpec`. That was wrong, and I caught it by reading
`handleHumanClaimed`, which calls `s.TaskByID`. Re-derived the full struct
(`awk '/^type InstanceState struct/,/^}/' engine/state.go`):

```
	// Tasks holds the in-flight human-task records for this instance.
	Tasks []humantask.HumanTask
```

So `AuthzSpec` is durable in **TWO** places, written by two different codecs:

| copy | written by | read by |
|---|---|---|
| `wrkflw_instances.snapshot` → `InstanceState.Tasks[].Eligibility` | `store_core.go:81` `json.Marshal(capHistory(step.State,…))` | `engine` — `handleHumanClaimed`/`handleHumanCompleted` via `s.TaskByID` |
| `wrkflw_human_task.eligibility` | `humantask_store.go:157` `json.Marshal(t.Eligibility)` | **`runtime/task`'s four `Authorize` sites**, via `s.store.Get(ctx, taskID)` |

**The correction strengthens the finding.** The bundle names **only** the copy the authorization path
does **not** read. The four `Authorize` calls and the hoisted `CheckSpecStated` (plan phase 5) all
operate on the `humantask.TaskStore` copy, re-derived at `runtime/task/service.go:196, 231, 252, 302`
(`task, err := s.store.Get(ctx, taskID)`). Phase 6's snapshot-only migration therefore leaves the
authoritative-for-authz copy at `Open == nil` indefinitely, and ADR-0185 D3's *"the population can
only shrink"* is false for it.

⚠ There is a second, unstated hazard the two copies create: the migration must keep them
**consistent**, or a task authorized as open from the task store is re-serialized from a snapshot
that says otherwise (or vice versa) on the next `UpdateTask` round-trip
(`engine/step_triggers.go` → `UpdateTask{Task: task.Clone()}` → `humantask_store.Upsert`). The bundle
has no decision for a split-brain `Open` between the two copies.

**Amended fix.** Phase 6 must migrate **both** copies, in one transaction per instance, and the ADR
must state which copy wins if they disagree. Add a plan test
`TestOpenBackfillMigratesSnapshotAndTaskStoreConsistently` whose fixture writes a divergent pair —
without it a migration that fixes one copy passes.

---

## F5 — CRITICAL — The hoisted gate is in `runtime/task`, but `engine.NewHumanCompleted` + `ProcessDriver.ApplyTrigger` is a fully public, supported path that reaches task state with NO authorization at all. ADR-0185's "closed" claim is still false.

**Claim attacked.** ADR-0185 Consequences/Positive: *"The chain in Context §1–6 is closed **at both
`Authorizer` implementations and above them**: the spec-shape gate is evaluated in `runtime/task`
before all four `Authorize` calls, so a consumer's own `Authorizer` inherits it too. The draft's
'closed end to end' sentence was false while the baseline authorizer stayed fail-open; **this is what
makes it true.**"*

**Evidence — BYPASSERS enumerated, not callers.** Re-derived every non-test producer of a human
trigger and every `Authorize` caller:

```
$ grep -rn "NewHumanClaimed(\|NewHumanCompleted(\|NewHumanReassigned(\|NewHumanCandidatesResolved(" --include='*.go' . | grep -v _test
runtime/task/service.go:203,238,259,314              <- the gated path
internal/persistence/store/trigger_codec.go:172,175,177,179   <- durable replay, UNGATED
engine/trigger.go:399,415,424,434                     <- the PUBLIC constructors themselves

$ grep -rn "\.Authorize(" --include='*.go' . | grep -v _test
casbinauthz/casbinauthz.go:163      (delegation, not a gate)
runtime/task/service.go:199,234,255,306
```

Three bypassers of the hoisted gate, all reachable without `runtime/task`:

1. **Direct trigger application — the library-first path.** `engine.NewHumanCompleted`
   (`engine/trigger.go:399`) and `runtime.ProcessDriver.ApplyTrigger`
   (`runtime/processdriver.go:556`) are both **exported module-root API**. A consumer embedding the
   engine — the deployment shape CLAUDE.md calls *the product* — can construct and apply a
   `HumanCompleted`/`HumanClaimed`/`HumanReassigned` trigger for any actor without ever touching
   `TaskService`. No `Authorize`, no `CheckSpecStated`, no spec-shape gate.
2. **Durable trigger replay.** `internal/persistence/store/trigger_codec.go:172-179` rehydrates the
   same four triggers from stored envelopes. A trigger persisted before the upgrade replays after it
   with no gate.
3. **`humantask.MemTaskStore` / a consumer `TaskStore`** — orthogonal, covered in F4.

For claim specifically the exposure is total today and remains so: re-derived, `handleHumanClaimed`
(`engine/step_triggers.go`) does **no** eligibility work — its whole body is
`TaskByID` → `IsOpen` → `task.Claim = &humantask.Claim{Actor: t.Actor, …}`. And repo-wide,
`grep -rn "Eligibility\|Authoriz" --include='*.go' engine/ | grep -v _test` returns **four** hits,
all of them the `AwaitHuman`/mint plumbing (`command.go:147,150`, `step_nodes.go:732,811`) — **zero**
authorization in the engine.

**Verdict: CONFIRMED.** D5's *claimant* guard does land in the engine and so does cover bypasser 1 for
completion — that part holds and is worth keeping. But the **spec-shape gate** and the whole
`Authorizer` chain do not, so the ADR's "closed … this is what makes it true" is an over-claim of the
same shape as the sentence the previous audit rejected. ⚠ This is a **cross-package reachability
gap**, the exact class this repo's memory records as one a design audit structurally struggles to
find; it is visible here only because I enumerated bypassers rather than callers.

**Proposed fix.**
1. Replace the Consequences sentence with a scoped one: *"closed for every caller that goes through
   `runtime/task.TaskService` — which is every path the HTTP transports use. A consumer applying
   `engine` triggers directly via `ProcessDriver.ApplyTrigger` is outside the gate by design; that is
   the trusted embedding path and `SECURITY.md` must say so."*
2. Add the statement to `SECURITY.md` (plan phase 15) and to `ProcessDriver.ApplyTrigger`'s godoc —
   an exported method whose security contract is currently unstated.
3. Decide explicitly (the bundle has no decision for this): should `handleHumanClaimed` gain the same
   treatment `handleHumanCompleted` gets under D5 — i.e. an engine-level check that the claiming
   actor is non-empty (D1's empty-ID rule) — so bypasser 1 cannot mint a `Claim{Actor:{ID:""}}` that
   then satisfies D5's `"" == ""` degenerate comparison? D1 puts the empty-ID rejection *"in the
   claim path"* without saying **which** claim path; `runtime/task.Claim` and
   `engine.handleHumanClaimed` are two different ones and only the engine covers both.

---
## F6 — CRITICAL — ADR-0186 D5's value-free 400 rendering is NOT IMPLEMENTABLE at `ClassifyError`. `runtime/validation.Gate` stringifies the typed error two layers earlier. EXECUTED.

**Claim attacked.** ADR-0186 D5: *"`*jsonschema.ValidationError` exposes `InstanceLocation []string`
and `ErrorKind.KeywordPath() []string`, so the rendering is built from the structured leaves:
`at '/ssn': violates pattern`. **Executed — the leaves are reachable from the public API, so this is
feasible rather than aspirational.**"* Evidence file §6 makes the same claim, with output.

**Evidence — I executed it through the real path, which the bundle's probe did not.**
Throwaway `runtime/validation/zzz_probe_test.go` (written, run, deleted):

```
    gate err        = workflow-validation: invalid input: workflow-validation/jsonschema: jsonschema validation failed with 'mem://schema.json#'
        - at '/ssn': maxLength: got 11, want 3
        - at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'
    Is ErrInvalidInput = true
    errors.As(*jsonschema.ValidationError) = false          <-- THE TYPE IS GONE
    CONTROL raw err  = workflow-validation/jsonschema: jsonschema validation failed with …
    CONTROL errors.As = true                                <-- present BEFORE the gate
--- PASS (0.00s)
```

The destroying line, re-derived:

```
runtime/validation/gate.go:45    return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
                                                       ^^ %s on err.Error(), not %w
```

`ClassifyError` (`transport/http/httpcore/errors.go:37,50`) matches on
`errors.Is(err, validation.ErrInvalidInput)` and copies `err.Error()` into the body. By then the
`*jsonschema.ValidationError` is an unstructured string containing the submitted value. The bundle's
own §6 probe called the jsonschema library **directly**, bypassing `Gate` — so it executed the wrong
object and drew a feasibility conclusion the real path refutes.

**Verdict: CONFIRMED, and it is the bundle's own Premise-Discipline failure mode repeating** —
"reachable from the public API" was true of the library and false of the error `ClassifyError`
receives.

**Proposed fix.**
1. Change `gate.go:45` to `fmt.Errorf("%w: %w", ErrInvalidInput, err)` so the typed leaf survives.
   ⚠ This is a **behavioural change in `runtime/validation`, a package the plan gives no phase for
   this purpose** (phase 5 covers `runtime` only for the gate hoist / reassign / eval bound).
   Add it explicitly, with its own RED test: `TestGatePreservesTypedValidationError`, whose falsifier
   is the executed `errors.As == false` above.
2. Add the `%w` change to the plan's breaking-change list — a consumer matching on the current
   flattened message shape is affected.
3. Re-run evidence §6 **through `Gate`**, not against the library, and replace the §6 output.

---

## F7 — CRITICAL — The 400 arm carries NINE sentinels and the repo has FOUR validation strategies. D5's rendering covers ONE of each, and the only prescribed test covers the one that is covered.

**Claim attacked.** ADR-0186 D5: *"**400** | **value-free rendering** — see below"*, and
*"The rendering must be built for **every** keyword, not by special-casing `pattern`."* The
quantifier the ADR polices is *keyword*; the quantifiers it never asks about are *sentinel* and
*strategy*.

**Evidence — re-derived, both enumerations.**

The 400 arm of `ClassifyError` (`transport/http/httpcore/errors.go:36-50`) matches **nine**
sentinels, not one:

```
kernel.ErrBadCursor · kernel.ErrBadArmedTimerCursor · ErrBadInput · validation.ErrInvalidInput ·
engine.ErrInvalidOutcome · engine.ErrOutcomeRequired · engine.ErrEmptyTriggerKey ·
engine.ErrEmptyReassignTarget          (8 sentinels across 5 `errors.Is` groups)
```

— all rendered by the single `Message: err.Error()` at `:50`. Only `validation.ErrInvalidInput` is
even in scope for a jsonschema-leaf rendering; the other seven fall through to verbatim
`err.Error()` with no decision anywhere in the bundle.

And `validation.ErrInvalidInput` wraps **four** strategies, re-derived
(`definition/model/validate/*/`): `jsonschema`, `expr`, `avro`, `callback`. Their error text:

```
definition/model/validate/jsonschema/jsonschema.go:87  fmt.Errorf("workflow-validation/jsonschema: %w", err)
definition/model/validate/expr/expr.go:64              fmt.Errorf("workflow-validation/expr: predicate %q: %w", v.source[i], err)
definition/model/validate/expr/expr.go:68              fmt.Errorf("workflow-validation/expr: predicate %q not satisfied", v.source[i])
definition/model/validate/avro/avro.go:46              fmt.Errorf("workflow-validation/avro: does not conform to avro schema: %w", err)
definition/model/validate/callback/callback.go         consumer-supplied Validate — arbitrary text
```

⚠ **The `expr` strategy leaks the predicate SOURCE into the 400 body** — `%q` on `v.source[i]` —
which is *the same disclosure* ADR-0186 Context §5 spends a paragraph establishing for the 403
eval-error arm, in the arm D5 declares fixed. `callback` can emit literally anything a consumer's
validator writes. Neither is a `*jsonschema.ValidationError`, so both bypass the structured
rendering entirely.

The plan's only 400 test — phase 9 test 6, `TestValidationErrorDoesNotEchoSubmittedValue` — uses a
jsonschema `pattern` (+ a `maxLength` row). It is green against a fix that covers jsonschema alone,
while `expr`, `avro`, `callback` and the seven non-validation sentinels keep leaking. ⚠ **A cited
test is not a covering test**, and here the test's coverage is exactly the fix's coverage, so it can
never reveal the gap.

**Verdict: CONFIRMED.** D5's "400 says everything except the value" is true for 1 strategy of 4 and
1 sentinel group of 5.

**Proposed fix.**
1. ADR-0186 D5 must state the **default** for a 400 whose error is not a recognised structured type.
   The only fail-closed answer consistent with the rest of the ADR: a static
   `"invalid input"` + the correlation id, with the raw error to the logger — i.e. **allow-list**
   structured rendering, deny-by-default text. State it, do not leave it to the implementer.
2. Fix `expr.go:64,68` to stop echoing `v.source[i]` on the runtime-validation path, or route it
   through the same allow-list.
3. Add plan phase 9 tests for the **uncovered** cases and say what fails today:
   `TestExprValidationErrorDoesNotEchoPredicateSource` (fails today: `%q` on `v.source[i]`),
   `TestNonSchemaBadRequestIsRenderedStatically` over at least
   `engine.ErrInvalidOutcome` and `kernel.ErrBadCursor`.
4. Reconcile with the ADR's own Consequences: *"the two 4xx arms proven to leak stop leaking"* — 403
   and 400 — is only true for part of the 400 arm. Rewrite it.

---

## F8 — MAJOR — `service.WithAllowAllAuthorizer()` and `httpcore.WithAnonymousActorAllowed()` are the same posture split across two packages with no plumbing between them. Setting the documented one still 401s.

**Claims attacked.** ADR-0185 D2: *"`service.WithAllowAllAuthorizer()` is added as the **explicit**
opt-in to the permissive posture."* ADR-0185 D1: *"`ActorFromContext` reports `ok == false` ⇒
`httpcore` returns **401**"* and *"`httpcore.WithAnonymousActorAllowed()` is the explicit opt-in for
demo and example wiring."*

**Evidence — logical, plus the packages.** The two knobs live in different packages
(`service/options.go` vs `transport/http/httpcore/seam.go`) and there is no path between them: the
401 is decided in `httpcore` **before** any `Authorizer` is consulted, so a consumer who declares the
permissive posture the ADR names — `service.WithAllowAllAuthorizer()` — still gets 401 on every task
route until they *also* find and set an unrelated option in a transport package. No diagnostic, no
cross-check, and the ADR describes them as if they were one posture.

⚠ This is structurally the same defect as ADR-0186 D2's accepted-Critical **zombie** ("the same knob"
built as two unconnected halves in two packages with two units). The revision fixed that instance and
introduced this one.

**Verdict: CONFIRMED.**

**Proposed fix.** Either (a) state plainly in ADR-0185 D1 and in `SECURITY.md` that these are two
independent knobs and both are required for an anonymous deployment, and add a plan phase-9 test
`TestAllowAllAuthorizerStill401sWithoutAnonymousOptIn` pinning the intended behaviour; or (b) make
`WithAnonymousActorAllowed` the single knob and have `httpcore` synthesize the zero actor only when
it is set, dropping the pretence that `WithAllowAllAuthorizer` affects HTTP at all. Do not leave the
two documented as one posture.

---

## F9 — MAJOR — D1's own rationale for keeping the ignored body field contradicts the 401 it introduces in the same Decision.

**Claim attacked.** ADR-0185 D1: *"A body that still carries `"actor"` or `"by"` is ignored (not
rejected): the field is out of the contract and its value is never read, so a 400 would **break
consumers' rollout windows** for no security gain."*

**Evidence — internal contradiction.** The same Decision makes an unauthenticated request return
**401**. A consumer mid-rollout is, by construction, one who has not yet installed authentication
middleware — so every one of their task requests fails with 401 regardless of what the body carries.
The rollout window the leniency preserves does not exist: the *body* is tolerated and the *request*
is refused. The bundle offers no transitional mechanism (no deprecation shim, no
`WithBodyActorFallback`, no warn-then-enforce period) anywhere in ADR-0185, the plan or §4
Migration — §4 covers only the `Open` tri-state.

**Verdict: CONFIRMED** as a contradiction in the stated *reason*. The *decision* (ignore, don't
reject) may still be right; the justification is not.

**Proposed fix.** Replace the rationale with the true one — *"rejecting an out-of-contract field is
gratuitous strictness and ADR-0167's strict decoding is scoped to definitions, not request DTOs"* —
and add a §4 sub-section "Rolling out the identity change", stating the actual sequence: install
middleware **first** (it is a no-op against the old binary), then upgrade. Without that sentence the
upgrade is a hard cutover the migration section does not mention.

---
## F10 — CRITICAL — The guard-dominance rule, taken literally, DENIES the most natural guarded predicate there is: a chained `and`. The plan's 3-row falsifying table structurally cannot catch it. EXECUTED.

**Claim attacked.** ADR-0185 D4: *"A guard counts only when the existence test dominates the use: the
**left operand of `and`**, or the **condition of a ternary** whose consequent holds the use. The three
rows above are the falsifying table the implementation must be tested against."* Plan phase 1 test 3
prescribes exactly those three rows, all two-operand.

**Evidence — executed.** Throwaway `internal/expreval/zzz_probe_test.go` (written, run, deleted),
`parser.Parse` + `ast.Walk` (post-order), `vars` empty:

```
"tier" in vars and vars.tier == "gold"
  -> [Binary(in){L=String R=Ident}  Binary(==){L=Member R=String}  Binary(and){L=Binary R=Binary}]
  -> eval=false err=<nil>

"tier" in vars and vars.active and vars.tier == "gold"
  -> [Binary(in){...}  Binary(and){L=Binary R=Member}  Binary(==){L=Member R=String}  Binary(and){L=Binary R=Binary}]
  -> eval=false err=<nil>                         <-- CORRECTLY GUARDED, and it must keep working

vars.active and "tier" in vars and vars.tier == "gold"
  -> eval=<nil> err=interface conversion: interface {} is nil, not bool (1:13)

"tier" in vars ? vars.tier == "gold" : false
  -> [Binary(in){...}  Binary(==){...}  Conditional]      <-- the ternary case is real
  -> eval=false err=<nil>
```

`and` is **left-associative**: row 2 parses as `(("tier" in vars) and vars.active) and (vars.tier == "gold")`.
For the use `vars.tier`, the immediate **left operand** of the enclosing `and` is a `BinaryNode(and)`
— **not the guard**. An implementation written to the ADR's literal wording ("the guard must be the
left operand of the `and`") therefore **denies row 2**, a correct, ordinary, fully guarded predicate.

⚠ This is the failure mode the brief names: the rule is exhaustively right about what it must
**refuse** (rows 2 and 3 of the ADR's table) and wrong about what it must still **permit**. The
plan's prescribed table has **no** three-operand row, so the over-strict implementation ships and the
suite is green — while every deployment whose policy conjoins more than two clauses is locked out.

Row 3 is a bonus correction: the bundle nowhere records that a *leading* absent-key boolean in an
`and` already fails closed today with a **run-time error** (`interface conversion … not bool`), which
`RoleAuthorizer` maps to `ErrNotAuthorized` (`authz/authz.go:136-138`). Part of the class D4 targets
is already closed by expr itself.

**Verdict: CONFIRMED.** The plan's own §0 item 1 says *"That rule itself has not been implemented,
only specified. Attack whether **dominance** is decidable over the shapes expr actually produces"* —
it is decidable, but not by the definition given.

**Proposed fix.**
1. Restate the rule in ADR-0185 D4 in terms that survive associativity:
   *"a key is guarded for a use U when a guard for that key appears in the set of nodes that are
   **evaluated-before and short-circuit** U — computed by walking the `and`/`&&` spine and the
   ternary condition, not by inspecting a single immediate sibling."*
   Concretely: flatten the left-associated `and` spine into a list, and a guard at position *i*
   covers every use at position *j > i*.
2. **Extend plan phase 1 test 3's table with the rows that decide over-strictness** — the controls
   the ADR is missing:
   | predicate | expected | why |
   |---|---|---|
   | `"tier" in vars and vars.active and vars.tier == "gold"` | **evaluates, `false`** | ⚠ the over-strict implementation denies this |
   | `"tier" in vars ? vars.tier == "gold" : false` | evaluates, `false` | pins the ternary arm |
   | `vars.tier == "gold" and "tier" in vars` | `ErrUndefinedReference` | guard after use is not a guard |
   State for the first row: *fails against an implementation that checks only the immediate left
   sibling* — that is what makes it non-vacuous.
3. Note in D4 that `vars.active and …` (leading absent boolean) already errors in expr, so the
   strict rule's incremental value is on the *deny-list* shapes, not all of them.

---

## F11 — CRITICAL — D3's tri-state migration removes upgrade-stranding for the `Open` dimension; D4's strict-reference rule REINTRODUCES it for the `Attribute` dimension, in the same ADR, with no migration and no repair verb.

**Claim attacked.** ADR-0185 D4: *"Today that silently allows. Under the runtime rule alone it would
silently **deny, forever, with no repair verb**. Failing at creation puts the diagnostic where the
author can act on it… **The runtime rule remains the *guarantee*** (it covers pre-upgrade tasks and
specs the mint-time check never saw)."*

**Evidence — logical, and the ADR states both halves itself.** The mint-time check (D4 site 3,
plan phase 4b) applies **only to tasks minted after the upgrade**. Every human task already open at
upgrade time whose `Attribute` predicate references a key absent from its frozen `Vars` snapshot is
then hit by the runtime rule, which the ADR says *"remains the guarantee"* — i.e. it **denies**.

Re-derived, the denial is total. All four verbs go through the same `Authorize` on the same stored
spec (`runtime/task/service.go:199, 234, 255, 306`), and `Vars` is frozen — the ADR itself says
*"`RefreshCandidates` refreshes candidates, not `Vars`"*. So such a task becomes **unclaimable,
uncompletable, unreassignable and unrefreshable**, permanently. That is verbatim the accepted-Critical
**C** of the 2026-08-20 audit ("The upgrade strands every in-flight human task"), re-created through
a different dimension.

And D3's remedy does not reach it: the tri-state is on `Open`; plan phase 6's backfill sets
`Open: true`. `Open: true` does not bypass the attribute predicate — `RoleAuthorizer.Authorize`
evaluates `spec.Attribute` **unconditionally** whenever it is non-empty
(`authz/authz.go:131`: `if spec.Attribute != "" { … }`), with no `Open` short-circuit anywhere in the
proposed design. So the backfill leaves these tasks stranded.

**Verdict: CONFIRMED.** The bundle solved stranding for one dimension and did not check the other,
despite having just been failed for exactly this.

**Proposed fix — pick one and record it in ADR-0185 D4 and plan §4:**
- **(a) Grandfather by mint time.** The strict rule applies only to specs the mint-time check
  validated. Requires a marker on the task (e.g. `AuthzSpec.StrictRefs *bool`, same tri-state
  precedent as `Open`, `nil` ⇒ legacy ⇒ lenient), and phase 6 backfills it. Consistent with D3.
- **(b) Give the stranded population a repair verb.** An admin route / `TaskService` method that
  re-snapshots `Vars` from the live instance for an open task, so the absent key can appear. The
  bundle has no such verb today and D4 explicitly notes `RefreshCandidates` is not one.
- **(c) Scope the strict rule to mint time only**, and drop the runtime half. Cheapest; costs the
  "guarantee" property the ADR wants, which must then be restated honestly.

Whichever is chosen, add plan phase 2 test
`TestStrictReferencesDoesNotStrandPreUpgradeTask` and state its falsifier: *fails against the current
design, where a pre-upgrade task's predicate denies at all four verbs.*

---

## F12 — MAJOR — Oversize bodies will classify as **400, not 413**: every adapter decode site already wraps its error in `httpcore.ErrBadInput`, and `ClassifyError`'s switch is order-dependent.

**Claim attacked.** ADR-0186 D1: *"each adapter converts its own oversize signal into that sentinel
**before** calling `ClassifyError` (`errors.As` for stdlib and gin; the pre-check for fiber);
`ClassifyError` maps the sentinel → **413**."* Plan phase 12: same.

**Evidence — re-derived.** All 13 decode sites per adapter already double-wrap:

```
transport/http/gin/groups.go:33-35     if err := gc.ShouldBindJSON(&in); err != nil {
                                           writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
transport/http/fiber/groups.go:33-34   if err := c.Bind().JSON(&in); err != nil {
                                           return writeErr(cfg, c, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
```

(13 sites in each of `stdlib`/`gin`/`fiber` `groups.go` — re-derived, matching the bundle's count.)

`ClassifyError` (`transport/http/httpcore/errors.go:27-59`) is a **`switch { case … }` with ordered
arms**: 404 (`:28`), 403 (`:32`), 409 (`:34`), **400 incl. `ErrBadInput` (`:36-50`)**, 422 (`:51`),
default 500 (`:57`). An error wrapping **both** `ErrBadInput` and a new `ErrBodyTooLarge` matches the
400 arm first if the 413 arm is appended anywhere after `:50`. The bundle never mentions the existing
`ErrBadInput` wrap, so an implementer following the plan literally converts the oversize signal, keeps
the `ErrBadInput` wrapper, and ships **400**.

⚠ Phase 12 has **no 413 test at all** — its only prescribed test is `TestBodyActorIsIgnored`. The 413
assertion lives in phase 9 (httpcore unit, no adapter wrap) and phase 13 (parity). So the failure
surfaces two phases downstream of the code that causes it.

**Verdict: CONFIRMED.**

**Proposed fix.**
1. State in ADR-0186 D1 and plan phase 12 that the adapter must return the bare
   `httpcore.ErrBodyTooLarge` (or a wrap that does **not** include `ErrBadInput`) on the oversize
   path — the `ErrBadInput` wrap is for decode failures only.
2. Belt and braces: place the 413 arm **before** the 400 arm in `ClassifyError` and say why in a
   comment (order-dependence of `errors.Is` arms).
3. Add `TestOversizedBodyReturns413` to **each** of phase 12's three agents' briefs, with the
   falsifier stated: *fails against an implementation that keeps the `ErrBadInput` wrapper.*

---

## F13 — MAJOR — The mint-time reference check's FAILURE MODE is unspecified. "Fails there" is not a decision, and the engine has three incompatible ways to fail.

**Claim attacked.** ADR-0185 D4 site 3: *"**Task creation**, in `engine/step_nodes.go`, as the
*diagnostic*: minting a task whose predicate references a key absent from the creation snapshot
**fails there**, with a node-scoped message."* Plan phase 4b repeats "fails there" verbatim and its
test asserts only that it does not mint.

**Evidence.** `userTaskStrategy.enter` (`engine/step_nodes.go:719`) returns
`([]Command, bool, error)`. Re-derived, an error out of a node strategy in this engine can become at
least three different observable outcomes, and the bundle picks none:

- a **retryable** token error → retry budget → `Incident` (`engine/state.go:219-244`,
  `Token.RetryAttempts`/`RetryStartedAt` at `:125,129`);
- a **non-retryable** error → `TokenIncident` immediately;
- an unhandled error → `handleUnhandledError` → deferred terminal
  (`InstanceState.PendingFinalStatus`, `state.go:127`).

These differ enormously for an operator: an incident is resolvable (`ResolveIncident`), a terminal
failure is not, and a retry loop on a **deterministic** authoring defect burns the budget for nothing.
⚠ Retrying is clearly wrong here — the predicate and the snapshot are both fixed at that instant, so
every attempt fails identically — but nothing in the bundle says so.

There is a second unstated consequence: whether the key is present depends on **which branch reached
the node**, so the same definition can mint fine on one path and hard-fail the instance on another.
A definition that works in staging can fail in production on a data-dependent path. The ADR presents
the check as an authoring-time diagnostic; it is a **run-time, data-dependent** one.

**Verdict: CONFIRMED** (unstated decision, not a wrong one).

**Proposed fix.** ADR-0185 D4 must state: the mint failure is **non-retryable** and raises an
`Incident` (not an instance termination), carrying node id, task id and the absent key(s), with a new
`engine.ErrPredicateReferencesAbsentVariable` sentinel (`workflow-engine:` prefix). Plan phase 4b's
test must then assert the **classification**, not merely the absence of a task —
`errors.Is(err, engine.ErrPredicateReferencesAbsentVariable)` plus an assertion that no retry is
scheduled. Add the data-dependence caveat to Consequences/Negative.

---

## F14 — MINOR — ADR-0185 D1's *"the request context already reaches `httpcore` unmodified in all three"* is false for fiber: `Ctx.Context()` returns `context.Background()` when nothing set.

**Claim attacked.** ADR-0185 D1: *"The request context already reaches `httpcore` unmodified in all
three (`stdlib` `req.Context()`, `gin` `gc.Request.Context()`, `fiber` `c.Context()`)."*
Evidence §8 lists this as inherited-and-not-re-executed.

**Evidence — re-derived from the pinned vendor,
`$(go env GOMODCACHE)/github.com/gofiber/fiber/v3@v3.4.0/ctx.go:134-144`:**

```go
func (c *DefaultCtx) Context() context.Context {
	if c.fasthttp == nil { return context.Background() }
	if ctx, ok := c.fasthttp.UserValue(userContextKey).(context.Context); ok && ctx != nil { return ctx }
	ctx := context.Background()
	c.SetContext(ctx)
	return ctx
}
```

and `SetContext` at `:147`. So fiber delivers **whatever middleware stored under `userContextKey`**,
defaulting to a fresh `context.Background()` — not the request's context. The *operative* conclusion
the ADR draws (an actor set with `c.SetContext` reaches `httpcore`; `c.Locals` uses a different key
and does not) **HOLDS by source**. What is wrong is the word "unmodified" and the implication that
fiber propagates request scope: it does not carry the request's deadline or cancellation at all.

**Verdict: PARTLY REFUTED** — the load-bearing half holds, the descriptive sentence does not.

**Proposed fix.** Reword D1 to *"the context `httpcore` receives is `req.Context()` for stdlib,
`gc.Request.Context()` for gin, and for fiber whatever middleware installed via `c.SetContext`
(defaulting to `context.Background()` — fiber does not propagate request cancellation)."* Keep the
`c.Locals` warning. Note in phase 13 that the three adapters are **not** equivalent in cancellation
semantics, so a parity case asserting context behaviour must not over-generalise.

---
## F15 — MAJOR — `RedactVariables` in `mapInstance` misses `GetInstanceSnapshot` and `GetActionableView`, which take no mapper and return process variables directly.

**Claim attacked.** ADR-0186 D4: *"⚠ **It is applied in `mapInstance`, before the mapper is called**"*;
Consequences/Positive: *"**Redaction cannot be bypassed by the response-customization feature.**"*
Plan phase 9: *"`RedactVariables` applied in **`mapInstance`, before the mapper**."*

**Evidence — re-derived, the mapper-less endpoints.** `mapInstance` has exactly **6** call sites
(`endpoints.go:42, 52, 94, 124, 140, 155`). But `httpcore` exports **two** more instance-read
endpoints that never take a `mapper` at all:

```
transport/http/httpcore/endpoints.go:60   func GetInstanceSnapshot(ctx, svc, id) (int, any, error)
                                              return http.StatusOK, pi, nil          <- raw service.ProcessInstance
transport/http/httpcore/endpoints.go:72   func GetActionableView(ctx, svc, id) (int, any, error)
                                              return http.StatusOK, view.NewActionableView(pi.State(), pi.Definition()), nil
```

and `service.ProcessInstance`'s JSON projection carries the variables verbatim:

```
service/instance.go:125    Variables  map[string]any  `json:"variables,omitempty"`
service/instance.go:344    Variables:    st.Variables,
```

So `GET …/snapshot` returns **unredacted process variables** after this bundle ships, and
`GetActionableView` renders open human tasks (whose `HumanTask.Vars` is the per-task variable
snapshot) with no redaction either. ⚠ Both are **non-admin** routes — precisely the groups
ADR-0186 Context §4 flags as carrying no `SECURITY:` caveat.

`AdminListInstances` was checked and is **clean**: `admin_endpoints.go:81-95` projects only
`InstanceID/DefID/DefVersion/Status/StartedAt/EndedAt/IncidentCount`. That one HELD.

**Verdict: CONFIRMED.** The Consequences sentence is true as literally written (the *mapper* cannot
bypass it) and false as intended (variables still escape unredacted).

**Proposed fix.** Apply `RedactVariables` at the **`ProcessInstance` → response** boundary rather
than in `mapInstance`: either give `GetInstanceSnapshot`/`GetActionableView` the same `CustomizeConfig`
access the other six have, or move redaction one level down into a helper both paths call. Add plan
phase 9 tests `TestSnapshotEndpointRedactsVariables` and
`TestActionableViewRedactsTaskVars`, each stating the falsifier: *fails against a fix confined to
`mapInstance`.* Rewrite the Consequences bullet to name the covered set rather than claim closure.

---

## F16 — MINOR — Three independent "D<n>" numbering schemes in one bundle. "D3" means three different decisions depending on the document.

**Evidence — re-derived from the headings.**

| | D1 | D2 | D3 | D4 | D5 | D6 |
|---|---|---|---|---|---|---|
| **spec §4.x** | principal | empty spec / open | ABAC missing vars | redaction | expression cost | SSRF |
| **ADR-0185** | actor in ctx | authorizer required | `Open` stated | strict refs | claimant guard | stale policy |
| **ADR-0186** | body caps | eval bound | SSRF | redaction | 4xx policy | at-rest |

(spec also has D7 4xx, D8 policy reload, D9 size caps.)

The plan carries **six** unqualified `D<n>` references — `plan:28` ("D5 gains a value-free 400
rendering"), `:54-55` ("under D3 … does D5 make reassignment unusable"), `:173`, `:182` ("the control
that decides D4"), `:364` ("D2's ctx is dropped"), `:555` ("the control D4 was missing"). Rule #11
dispatches one fresh subagent per task with a prescriptive brief; a brief saying "implement D4" is
ambiguous between *strict references* (0185) and *redaction* (0186) — and `plan:555`'s "D4" is
0186's while `plan:173`'s "D4" is 0185's, **eleven lines of table apart in the same document**.

**Verdict: CONFIRMED.**

**Proposed fix.** Qualify every decision reference as `ADR-0185 D4` / `ADR-0186 D4` / `spec §4.4`
throughout the plan (mechanical), and add a one-line legend at the top of §3. Cheap; prevents a
mis-dispatched brief.

---

## What HELD (do not re-litigate)

Re-derived and confirmed correct in the revision:

- **`AuthzSpec` / `RoleAuthorizer` premises.** `authz/authz.go:23` `expreval.New()`; `:79-86` the
  *"An empty spec means allow-all"* godoc; `:119-120` `Privileges` documented as not evaluated;
  `:124-146` the zero spec returns `nil`; `:136-138` run errors wrapped as `ErrNotAuthorized`.
- **The four `Authorize` sites** at `runtime/task/service.go:199/234/255/306` — exactly four, exactly
  those lines, all on `task.Eligibility`.
- **`Reassign`'s godoc really does say it is the same check as `Claim`** —
  `runtime/task/service.go:206-217`: *"the reassigner (by) must satisfy the task's eligibility spec —
  the same check as Claim."* D5's two-hop premise holds.
- **`Candidates` is a projection, not an ACL** — stated in source at `runtime/task/service.go`'s
  `RefreshCandidates` godoc. D5's refusal to compare against it is right.
- **The engine does no authorization.** `grep -rn "Eligibility\|Authoriz" engine/ | grep -v _test` →
  4 hits, all `AwaitHuman`/mint plumbing. `handleHumanClaimed`'s body is `TaskByID` → `IsOpen` →
  assign `Claim`. Context finding 5 holds and is understated.
- **`internal/authz/casbin` is a second real ABAC site** — `authorizer.go:30` own `expreval.New()`,
  `:33` own *"An empty spec allows"* godoc, `:68` evaluates `spec.Attribute`. The sixth-finding
  correction is sound.
- **`view.go:31` aliases** — `Variables: st.Variables`, confirmed verbatim.
- **`mapInstance` exists and precedes the mapper** — `endpoints.go:15`, 6 call sites; the placement
  argument against `NewInstanceView` is correct (see F15 for what it still misses).
- **`CustomizeConfig` has exactly six fields** and no identity seam (`seam.go`), as claimed. ⚠ it is
  generic (`CustomizeConfig[R any]`), which the ADRs never write — cosmetic only.
- **13 decode sites in each of `stdlib`/`gin`/`fiber` `groups.go`** — re-derived, 13/13/13 = 39.
- **`ClassifyError`'s switch has exactly six arms and five echo `err.Error()`** —
  `errors.go:31, 33, 35, 50, 56` echo; `:58` blanks. Closed set, as claimed.
- **fiber `SetContext`/`Locals` distinction** — confirmed from the pinned vendor
  (`fiber/v3@v3.4.0/ctx.go:134-147`); `Context()` reads `userContextKey`, which only `SetContext`
  writes. (Wording quibble only — F14.)
- **`ErrorBody` has no correlation-id field today** (`errors.go:19-22`), so D5's "breaking wire
  change" classification is right.
- **The tri-state decode mechanism** — no `DisallowUnknownFields` on either persistence path
  (only `runtime/kernel/cursorcodec.go:45` and `definition/model/node_wire.go:190` use it), so
  `Open *bool` genuinely round-trips nil/true/false. (The *citations* are wrong — F3.)
- **`AdminListInstances` does not disclose variables** — summary projection only.
- The **`??` unparenthesised compile failure**, the **`has()` non-existence**, and the **`get()`
  zero-reference bypass** all reproduce as the evidence file records them (re-checked against §1–3;
  I did not re-run §1–2 end to end — see "not executed" below).

---

## What I did NOT execute (labelled, per Premise Discipline)

- `ASSUMPTION (unverified)` on my side: the **env-element bound's ~2.4 s at n=10 000**. I did not
  re-measure the O(n²) ladder; the plan's §0 item 5 asks for it and it remains open. ⚠ I also did not
  test whether *counting* elements reachable from the env is cheap — the plan flags that the bound
  "must not cost more than it saves" and nothing in the bundle bounds the counting walk itself. **The
  re-audit's execution lens should own both.**
- `ASSUMPTION (unverified)`: the fiber `len(c.Body())` cap mechanism. Source-reasoned only, as the
  bundle also states.
- I did not run the repo-wide suite or coverage — **no Docker permission in this brief**, and it was
  not needed for any finding above. Every probe I ran was container-free and is quoted verbatim.
- I did not re-derive the **274 / 128 / 5** `NewUserTask` counts or the **29** pin count; those are
  the counting lens's job and I deliberately did not duplicate it. ⚠ One adjacent observation I could
  not resolve: `model.Validate` is called from `engine/step_fuzz_test.go:48` inside a **fuzz target**,
  so phase 3's new authoring rejection changes fuzz-corpus behaviour in a package (`engine`) whose
  phase mentions only fixture fallout. Worth one line in phase 4d.

---

## RANKED INDEX

| # | Sev | Title | Docs touched | Verdict |
|---|---|---|---|---|
| **F1** | **Critical** | Hoisted `CheckSpecStated` is authorizer-agnostic but enforces an authorizer-dependent rule ⇒ kills `Privileges` repo-wide, casbin included | 0185 D3 + Consequences, plan ph2 t5, ph5 | CONFIRMED |
| **F2** | **Critical** | D5's `reassign` privilege in `AuthzSpec.Privileges` is applied by casbin to **all four** verbs ⇒ requiring it to reassign requires it to claim/complete | 0185 D5, plan ph5 | CONFIRMED |
| **F3** | **Critical** | Tri-state migration designed against the wrong table/codec; **two** durable copies of `AuthzSpec` exist and the bundle names only the one authorization does not read | 0185 D3, evidence §5, plan §4 + ph6 | CONFIRMED |
| **F4** | **Critical** | `Open *bool` makes the public-API zero value fail-**open**; `nil` is reachable from Go literals, consumer `TaskStore`s and `MemTaskStore` | 0185 D3, plan §4 | CONFIRMED |
| **F5** | **Critical** | `engine.NewHumanCompleted` + `ProcessDriver.ApplyTrigger` bypasses the hoisted gate entirely; "closed" is still an over-claim | 0185 Consequences, plan ph5, ph15 | CONFIRMED |
| **F6** | **Critical** | D5's value-free 400 is **not implementable**: `runtime/validation/gate.go:45` `%s`-flattens the typed error. `errors.As` = **false**, EXECUTED | 0186 D5, evidence §6, plan ph9 t6 | CONFIRMED |
| **F7** | **Critical** | 400 arm has 9 sentinels and 4 validation strategies; the fix and its only test both cover 1 of each. `expr` strategy leaks the predicate source | 0186 D5 + Consequences, plan ph9 | CONFIRMED |
| **F10** | **Critical** | Guard-dominance rule as worded DENIES chained-`and` guarded predicates; plan's 3-row table cannot catch it. EXECUTED | 0185 D4, plan ph1 t3 | CONFIRMED |
| **F11** | **Critical** | D4's strict rule re-introduces upgrade-stranding on the `Attribute` dimension that D3's tri-state removed on `Open` — no migration, no repair verb | 0185 D4, plan §4 + ph6 | CONFIRMED |
| **F8** | Major | `service.WithAllowAllAuthorizer()` and `httpcore.WithAnonymousActorAllowed()` are one posture split across two packages with no plumbing | 0185 D1, D2 | CONFIRMED |
| **F9** | Major | D1's rationale for tolerating the ignored body field contradicts the 401 it introduces; no rollout path stated | 0185 D1, plan §4 | CONFIRMED |
| **F12** | Major | Oversize ⇒ **400 not 413**: adapters already wrap in `ErrBadInput` and `ClassifyError`'s arms are ordered; phase 12 has no 413 test | 0186 D1, plan ph12 | CONFIRMED |
| **F13** | Major | Mint-time check's failure mode unspecified (retryable? incident? terminal?) and it is data-dependent, not authoring-time | 0185 D4, plan ph4b | CONFIRMED |
| **F15** | Major | `RedactVariables` in `mapInstance` misses `GetInstanceSnapshot` and `GetActionableView`, which return variables with no mapper | 0186 D4 + Consequences, plan ph9 | CONFIRMED |
| **F14** | Minor | "the request context reaches `httpcore` unmodified in all three" is false for fiber (`Context()` defaults to `context.Background()`) | 0185 D1 | PARTLY REFUTED |
| **F16** | Minor | Three independent `D<n>` schemes; the plan carries 6 unqualified references, two meaning different ADRs 11 lines apart | plan (throughout) | CONFIRMED |

**Totals: 16 findings — 9 Critical, 4 Major, 2 Minor, 1 partly-refuted (counted in the Minors).**

⚠ **Recommendation: this revision FAILS its re-audit.** F1, F2, F6, F10 and F11 are each a decision
that cannot be implemented as written, not a text fix. F3 and F4 invalidate the migration story the
revision was written to supply. Five of the nine Criticals (F1, F2, F4, F10, F11) are **holes the
revision's own fixes opened** — the hoist, the privilege seam, the tri-state, the dominance rule and
the strict-reference rule. That pattern is the finding: four Decisions changed and their pairwise
interactions were never re-derived.
