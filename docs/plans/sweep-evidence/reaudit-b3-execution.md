# Re-audit of the REVISED B3 authz/security bundle — EXECUTION lens (2026-08-21)

Bundle commit audited: `dd76a17b` ("docs(authz): revise the authz/security design
bundle against its failed audit — REVISED, awaiting RE-AUDIT").

Worktree: `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-wrkflw/ef1507c5-354f-4cfc-a150-63d4a2e84317/scratchpad/reaudit-exec`
Probe module: `.../scratchpad/reaudit-exec-probe` (`module probe`, `replace` → worktree).
Go 1.26.6 darwin/arm64. Pins: `expr-lang/expr v1.17.8`, `gofiber/fiber/v3 v3.4.0`,
`santhosh-tekuri/jsonschema/v6 v6.0.2`.

Step 0: all five bundle files verified present at `dd76a17b`.

---

## E1 — CRITICAL — The hoisted `CheckSpecStated` gate makes ADR-0185 D5's `reassign` privilege UNAUTHORABLE, and kills casbin's privilege dimension repo-wide

**Claims attacked (three, mutually inconsistent, all in this bundle):**

1. ADR-0185 Decision 5: *"a reassignment whose `by.ID` differs from the current
   claimant requires an explicit **`reassign` privilege token** in the spec — the
   seam Decision 3's privileges leg opens — rather than bare eligibility."*
2. ADR-0185 Decision 3: *"A spec with **any** non-empty `Privileges` returns
   `authz.ErrUnevaluatableSpec` wrapped in `ErrNotAuthorized` under
   `RoleAuthorizer` — including the mixed `{Roles, Privileges}` shape … A consumer
   who wants privileges evaluated wires the casbin authorizer, which does evaluate
   them."*
3. ADR-0185 Consequences/Positive + plan phase 5 + spec §4.3: *"the spec-shape gate
   is evaluated in `runtime/task` before all four `Authorize` calls, so a consumer's
   own `Authorizer` inherits it too"*; plan phase 2 declares the gate's signature as
   **`func CheckSpecStated(spec AuthzSpec) error`** — it receives **no `Authorizer`**
   — and plan phase 2 test 5 requires its table to **mirror** test 1, whose rows
   include `Privileges`-only **and** `{Roles:["manager"], Privileges:["x"]}`, both
   denied with `ErrUnevaluatableSpec`.

**Probe** — implemented `CheckSpecStated` verbatim to the plan's prescription and ran
the exact spec D5 forces an author to write
(`/private/tmp/.../reaudit-exec-probe/p1/main.go`, `go run ./p1`).

**Observed (verbatim):**

```
== ADR-0185 D5's REQUIRED spec, through the plan's hoisted gate ==
spec = {Roles:[manager] Privileges:[finance-task reassign] Attribute:} Open=false
  runtime/task Claim(:199)              CheckSpecStated -> workflow-authz: not authorized: workflow-authz: spec declares no dimension this authorizer evaluates  (unevaluatable=true)
  runtime/task Reassign(:234)           CheckSpecStated -> workflow-authz: not authorized: workflow-authz: spec declares no dimension this authorizer evaluates  (unevaluatable=true)
  runtime/task Complete(:255)           CheckSpecStated -> workflow-authz: not authorized: workflow-authz: spec declares no dimension this authorizer evaluates  (unevaluatable=true)
  runtime/task RefreshCandidates(:306)  CheckSpecStated -> workflow-authz: not authorized: workflow-authz: spec declares no dimension this authorizer evaluates  (unevaluatable=true)

== privileges-only, as the casbin baseline would author it ==
  CheckSpecStated -> workflow-authz: not authorized: workflow-authz: spec declares no dimension this authorizer evaluates
```

Source-verified corroboration: `internal/authz/casbin/authorizer.go:55-64` **does**
evaluate `spec.Privileges` (step 2, `a.anyPrivilege`). The four call sites are
confirmed at `runtime/task/service.go:199, 234, 255, 306`, and each passes
`task.Eligibility` — **one single `AuthzSpec` shared by all four verbs**.

**Verdict: CONFIRMED — and worse than the plan's §0 item 4 anticipated.** The plan
flagged only "does D5 make reassignment impossible under `RoleAuthorizer`". Executed,
three larger things are true:

- **Adding the `reassign` token does not merely block reassignment — it bricks the
  whole task.** `Eligibility` is one spec read by Claim, Complete, Reassign *and*
  RefreshCandidates. A task authored to satisfy D5 is unclaimable, uncompletable,
  unreassignable and unrefreshable.
- **It is not confined to `RoleAuthorizer`.** The gate is authorizer-blind by its own
  signature and hoisted *above* `Authorize`, so it denies under casbin and under a
  consumer's own `Authorizer` too. D3's escape hatch — *"wires the casbin
  authorizer, which does evaluate them"* — **cannot be reached**; the gate returns
  before `Authorize` is called. CLAUDE.md makes casbin **the baseline**, so this
  disables the baseline's entire resource-privilege dimension, which is one of the
  three authorization modes CLAUDE.md's Architecture section requires.
- **The ADR contradicts itself inside one document.** D3 scopes the denial *"under
  `RoleAuthorizer`"*; Consequences scopes the same gate to *"before all four
  `Authorize` calls … a consumer's own `Authorizer` inherits it too"*. Both cannot
  hold. The revision banner's own justification for hoisting (*"the draft fixed
  `RoleAuthorizer` while pointing consumers at the casbin authorizer it left
  fail-open"*) is the reason the hoist exists, and the hoist is what breaks D3's
  escape hatch.

**Proposed fix (concrete).** Split the gate along the axis it is actually deciding.
`CheckSpecStated` must answer only *"did the author state an intent?"*, which is
authorizer-independent; *"can this authorizer evaluate that intent?"* is not.

```go
// authz: authorizer-blind, hoistable. Privileges COUNT as a stated dimension.
func CheckSpecStated(spec AuthzSpec) error   // deny iff Open==ptr(false)/absent-and-
                                             // Roles==nil && Privileges==nil && Attribute==""
// authz: the RoleAuthorizer-local half, evaluated INSIDE Authorize.
// RoleAuthorizer cannot evaluate Privileges, so it (and only it) denies them.
func (RoleAuthorizer) Authorize(...) error   // + if len(spec.Privileges) > 0 {
                                             //     return fmt.Errorf("%w: %w", ErrNotAuthorized, ErrUnevaluatableSpec) }
```

Then: the hoisted gate admits `{Roles:["manager"], Privileges:["… reassign"]}`;
`RoleAuthorizer` denies it (correctly — it *cannot* evaluate the token, which is the
Context-finding-3 hole); casbin evaluates it (correctly — D3's escape hatch works
again). Update in the same edit:

- ADR-0185 D3 — state the two-layer split explicitly and delete the implication that
  the hoisted gate covers privileges.
- ADR-0185 D5 — add a stated consequence: *"a deployment on `RoleAuthorizer` cannot
  author the `reassign` privilege and therefore cannot permit non-claimant
  reassignment at all; that is the fail-closed posture, and casbin is the supported
  route to permitting it."* (Or choose the alternative below and say so.)
- Plan phase 2 test 5 — **must no longer mirror test 1.** Its `Privileges`-only and
  mixed rows must now assert `err == nil` from `CheckSpecStated` and
  `ErrUnevaluatableSpec` from `RoleAuthorizer.Authorize`. As written, phase 2 test 5
  and phase 5 test 2 (`TestReassignToSelfByNonClaimantRequiresPrivilege`) cannot both
  pass.

**Alternative, if the above is judged to strand `RoleAuthorizer` deployments:** make
D5's non-claimant guard key on something `RoleAuthorizer` *can* evaluate — a
dedicated `AuthzSpec.ReassignRoles []string`, or a boolean
`AuthzSpec.AllowNonClaimantReassign *bool`. This keeps D5 authorable everywhere and
removes its dependency on the privileges leg entirely. It costs one more `AuthzSpec`
field; it buys a decision that does not depend on which `Authorizer` is wired.

---
## E2 — CRITICAL — The guard-DOMINANCE rule is still unsound; the bundle's three-row "falsifying table" is not falsifying

**Claim attacked.** ADR-0185 Decision 4:

> *"A guard counts only when the existence test dominates the use: the **left operand
> of `and`**, or the **condition of a ternary** whose consequent holds the use. The
> three rows above are the falsifying table the implementation must be tested
> against."*

and the plan, phase 1 test 3 (`TestGuardMustDominateItsUse`), described as
**"the control that decides D4"**, whose table is exactly those three rows.
Spec §2.4.2 restates it: *"Those three rows are the falsifying table the
implementation must be tested against."*

**Probe.** Implemented the dominance rule **verbatim** as an inherited guard-set
descent over `expr-lang/expr` v1.17.8's AST — guards collected from the left operand
of `and` and from a ternary's condition, propagated into the right operand /
consequent only — then compared its verdict against what each predicate **actually
evaluates to** with `vars` empty.
(`/private/tmp/.../reaudit-exec-probe/p2/main.go`, `go run ./p2`.)

**Observed (verbatim):**

```
PREDICATE (vars as noted)                            EVALUATES D4-DOMINANCE VERDICT
"tier" in vars and vars.tier == "gold"               false     map[vars.tier:true]
"tier" in vars or vars.tier != "blocked"             true      map[vars.tier:false]
not ("tier" in vars) or vars.tier == "x"             true      map[vars.tier:false]
not ("tier" in vars) and vars.tier != "x"            true      map[vars.tier:true]
("tier" in vars) == false and vars.tier != "x"       true      map[vars.tier:true]
("a" in vars or "b" in vars) and vars.a != "x"       true      map[vars.a:true]
"tier" in vars ? vars.tier == "x" : true             true      map[vars.tier:true]
("tier" in vars and vars.tier == "x") or vars.tier != "y" true      map[vars.tier:false]
let g = "tier" in vars; g and vars.tier != "x"       false     map[vars.tier:false]
```

Read `true` in EVALUATES as **allows on an absent key** — the exact class D4 exists to
close. Read `:true` in the verdict as **"dominance says this key is guarded, so the
strict check stands down"**.

**Verdict: CONFIRMED.** The bundle's three rows (1–3) are handled correctly by the
dominance rule — and **four further rows defeat it**, three of them without any
exotic syntax:

| row | why it defeats "left operand of `and`" |
|---|---|
| `not ("tier" in vars) and vars.tier != "x"` | the guard is in the dominating position but at **negative polarity**. Semantically identical to the bundle's own row 3 (`not(G) or U` ≡ `not(G and not U)`); the bundle caught the `or` spelling and missed the `and` spelling of the same hole. |
| `("tier" in vars) == false and vars.tier != "x"` | same hole with no `not` token at all — a collector keying on `ast.UnaryNode{"not"}` will not even see it. |
| `("a" in vars or "b" in vars) and vars.a != "x"` (`vars={"b":1}`) | **no negation whatsoever.** The guard is disjunctive, so it does not establish `"a"`. Position is dominating; the *implication* is not. |
| `"tier" in vars ? vars.tier == "x" : true` | matches D4's ternary clause **word for word** — "the condition of a ternary whose consequent holds the use" — and allows on absence through the **alternate**, which D4 never constrains. |

The load-bearing consequence is about the **plan**, not only the ADR: phase 1 test 3
is billed as *"the control that decides D4"* and *"Without this test the naive
implementation ships and the suite is green."* Executed, an implementation that
passes all three of its rows **still ships the hole**, and the suite is still green.
The test is not vacuous, but it is **not sufficient for the claim it is asked to
carry**, and the ADR's *"the falsifying table"* (definite article, closed set) is
false as a quantifier.

**Proposed fix (concrete).** Position is the wrong predicate; **polarity + implication**
is the right one. Replace D4's rule with:

> A key `k` is guarded at a use site iff every path from the root to that use passes
> through a **positive-polarity** occurrence of an existence test for `k` that
> **implies** the use is reached. Implement as a descent carrying (a) an inherited
> guard set and (b) a polarity flag flipped by `not` / `!` / `== false` / `!=`
> against a boolean literal:
> - `A and B`: descend `A` at current polarity; descend `B` with guards inherited
>   from **positive-polarity, conjunctive-only** existence tests in `A`. A guard
>   inside an `or` in `A`, or at negative polarity in `A`, does **not** propagate.
> - `A or B`: no guard propagates in either direction.
> - `C ? T : F`: guards from positive-polarity conjunctive tests in `C` propagate to
>   `T` only; **`F` inherits nothing, and any unguarded use in `F` denies** —
>   this is the row D4 currently admits.
> - `not A` / `!A`: descend `A` with polarity flipped; a negative-polarity existence
>   test contributes **no** guard.
> Anything the descent cannot classify (`let` bindings, closures, `??` chains,
> pipelines) **denies**, consistent with the zero-reference rule.

And in the plan, phase 1 test 3's table **must be extended to the seven rows above**
(three currently there + the four in the table of this finding), each with its
executed `EVALUATES` value as the falsification witness. Change the ADR's
*"the falsifying table"* to *"a falsifying table; the rule must additionally reject
negative-polarity, disjunctive and ternary-alternate guards, enumerated in the plan"*.

⚠ Also worth stating in D4: the last row above (`let g = …; g and vars.tier != "x"`)
evaluates `false` and is denied — **sound but over-restrictive**. `let` is in expr
v1.17.8; the ADR should say `let`-bound guards are unsupported rather than leave a
reader to discover it.

---
## E3 — CRITICAL — The strict-reference check gives ZERO protection on the `actor` axis, and the bundle's depth-1 justification does not apply there

**Claims attacked.**

- ADR-0185 D4: *"Extraction is **depth-1**: `vars.order.total` yields `vars.order`.
  That is not a limitation to apologise for — `humantask.HumanTask.Vars`' own godoc
  already states the snapshot is a shallow `maps.Clone` and that "eligibility
  predicates should rely on top-level scalar variables only". **Depth-1 is precisely
  the documented supported surface.**"*
- ADR-0185 D1: *"Because the actor now arrives whole rather than being re-projected
  field by field, `Actor.Attributes` reaches the authorizer — **closing finding 4's
  second leg for free**."*
- Evidence file §3 row: `actor.attributes.clearance > 3` → *"`actor.attributes`
  {member}  depth-1 only"*.

**Probe.** `/private/tmp/.../reaudit-exec-probe/p3` and `/p4`, run against the
**shipped** `authz.RoleAuthorizer.Authorize` and the plan's prescribed depth-1
`referencedKeys` extractor.

**Observed (verbatim):**

```
=== A. The actor axis: depth-1 extraction cannot see actor attribute keys ===
actor.Attributes.suspended != true     refs=[actor.Attributes      ]  no-attrs=true | suspended-actor=false
not actor.Attributes.suspended         refs=[actor.Attributes      ]  no-attrs=false | suspended-actor=false
actor.Attributes.clearance != "none"   refs=[actor.Attributes      ]  no-attrs=true | suspended-actor=true
  (`true` on the left = ALLOWED. refs show what a depth-1 strict check would test.)
```

and, on how the real env resolves the actor at all:

```
== how the REAL env resolves actor.* (actor is a STRUCT) ==
"manager" in actor.Roles               out=true   err=<nil>
"manager" in actor.roles               out=<nil>  err=cannot fetch roles from authz.Actor (1:20)
actor.Attributes.clearance > 3         out=true   err=<nil>
actor.attributes.clearance > 3         out=<nil>  err=cannot fetch attributes from authz.Actor (1:7)
"Roles" in actor                       out=true   err=<nil>
"roles" in actor                       out=false  err=<nil>
```

**Verdict: CONFIRMED, two legs.**

**Leg 1 — the depth-1 justification is imported from the wrong contract.**
`authz.RoleAuthorizer` builds `env = {"actor": actor, "vars": vars}` where `actor` is
the **struct** `authz.Actor` (`authz/authz.go:127-130`), whose only map-valued field
is `Attributes`. An actor-attribute predicate is therefore **inherently depth-2**:
`actor.Attributes.suspended`. Depth-1 extraction yields `actor.Attributes` — a Go
struct field that **exists unconditionally**, for every actor, forever. So the strict
check can never fire on the actor axis, and row 1 above shows the consequence: a
deny-list predicate over an actor attribute **allows the actor who lacks the
attribute** (`no-attrs=true`) while correctly denying the one who has it. That is
verbatim the class ADR-0185 Context finding 4 defines, left completely open.
D1's *"closing finding 4's second leg for free"* is therefore **false**: D1 makes
actor attributes *reachable*, and D4 does not protect them.

The cited justification does not transfer: `humantask.HumanTask.Vars`' godoc
constrains **`Vars`**. Nothing in the repo says actor attributes are top-level
scalars, and they structurally cannot be — `Actor.Attributes` is
`map[string]any`, so every use of it is at depth 2.

**Leg 2 — the bundle's own example predicate does not run.** The evidence file §3
tabulates `actor.attributes.clearance > 3` as a reference-extraction example, and the
extraction verdict it records is correct *about the parse tree* while the predicate
itself is a **run-time error** against the real `authz.Actor`: expr fetches struct
fields by **Go field name**, and `Actor`'s `json:"..."` tags do not rename them.
Under `RoleAuthorizer` that error is wrapped as `ErrNotAuthorized`, so the predicate
**denies everyone** — the same failure mode as `has(vars,"k")` that this revision
exists to fix, reproduced one document later in the fix's own evidence.
Confirmed through the shipped authorizer:

```
"manager" in actor.roles               Authorize -> workflow-authz: not authorized: attribute predicate: ... cannot fetch roles from authz.Actor
actor.attributes.clearance > 3         Authorize -> workflow-authz: not authorized: attribute predicate: ... cannot fetch attributes from authz.Actor
actor.Attributes.clearance > 3         Authorize -> <nil>
```

**Proposed fix (concrete).**

1. **Extend the strict check to depth 2 *through `actor.Attributes` specifically***,
   which is a closed, known-at-compile-time path: when the extractor sees
   `actor.Attributes.<lit>`, record the ref as `actor.Attributes.<lit>` and test it
   against the actor's live `Attributes` map. This is not general depth-2 support —
   it is one hard-coded path whose shape the type system fixes.
2. **Deny bare `actor.Attributes` at depth 1** (i.e. `actor.Attributes != nil`-style
   uses) only if the team prefers uniformity; otherwise state it as out of scope.
3. Correct D4's justification paragraph: say *"depth-1 for `vars`, plus the single
   `actor.Attributes.<key>` path, because `Actor.Attributes` is the only map behind a
   struct field and every actor-attribute predicate is depth-2 by construction."*
   Delete the sentence that generalises `HumanTask.Vars`' godoc to the actor axis.
4. Withdraw D1's *"closing finding 4's second leg for free"* — it is not free and it
   is not closed; reachability ≠ protection.
5. Fix evidence file §3's row to `actor.Attributes.clearance > 3` and add a row
   showing the lowercase spelling is a run-time error, so the next reader does not
   copy it. Add the same warning to `SECURITY.md` / the D4 documentation table:
   **`actor.*` is a Go struct — use `ID`, `Roles`, `Attributes`, capitalised.**
6. Add to plan phase 1 a test `TestStrictReferencesCoversActorAttributes` with
   `actor.Attributes.suspended != true` and an actor whose `Attributes` is nil,
   asserting `ErrUndefinedReference`. **What makes it fail today:** executed above —
   it returns `err=<nil>` (allows).

---

## E4 — CRITICAL — The zero-reference rule does NOT close the `get()` bypass; one ordinary reference disarms it

**Claim attacked.** ADR-0185 D4, the residual-shape table:

> | zero references | `get(vars,"k")`, `vars \| first()` | **deny** — same reason;
> **this is what closes the `get()` bypass** |

and the evidence file §3: *"It must be handled by the zero-reference rule, not offered
as an escape hatch."* Plan phase 1 test 4 (`TestStrictReferencesDeniesUnresolvableReferences`)
tests `get(vars,"k") == "x"`, `vars | first() != "blocked"` and `vars[actor.ID] == "x"`
**each as a whole predicate**.

**Probe.** `/private/tmp/.../reaudit-exec-probe/p4`, section B: the plan's depth-1
`referencedKeys` extractor plus the shipped `RoleAuthorizer`, over predicates that
combine a `get()`/pipeline bypass with one ordinary reference.

**Observed (verbatim):**

```
=== B. Does the zero-reference rule actually close the get() bypass? ===
ADR-0185 D4: "zero references -> deny ... this is what closes the get() bypass"
get(vars,"blocked") != true                          refs=[] zeroRefRuleFires=true   allowsToday=true
vars.region == "eu" and get(vars,"blocked") != true  refs=[vars.region               ] zeroRefRuleFires=false  allowsToday=true
vars.region == "eu" or get(vars,"blocked") != true   refs=[vars.region               ] zeroRefRuleFires=false  allowsToday=true
(vars | first()) != "blocked" and vars.region == "eu" refs=[vars.region               ] zeroRefRuleFires=false  allowsToday=true
vars[actor.ID] == "x" or vars.region == "eu"         refs=[actor.ID                   vars.<dynamic>             vars.region               ] zeroRefRuleFires=false  allowsToday=true
```

**Verdict: CONFIRMED.** The rule is stated over the **whole predicate** ("zero
references"), so it fires only when the bypass is the entire expression — the one
shape the plan happens to test. Add a single satisfiable reference and
`zeroRefRuleFires` goes **false**, the strict check is satisfied by `vars.region`
alone, and the `get()` / pipeline subexpression is evaluated with the original
allow-on-absence semantics. `vars.region == "eu" or get(vars,"blocked") != true` is a
one-line, entirely natural policy that reintroduces the whole hole.

The plan's test 4 cannot catch this: all three of its rows are bare bypasses, so an
implementation that checks `len(refs) == 0` at the top level passes it and ships the
defect. This is the "a matching line of test text proves nothing about whether the
assertion can fail" pattern from CLAUDE.md — here the *fixture* (a bare bypass) is
what makes the test unable to discriminate.

**Proposed fix (concrete).** Restate the rule **per access site**, not per predicate:

> The extractor classifies **every** `vars` / `actor` access in the tree. Evaluation
> denies if **any** access is unresolvable — a dynamic key, a builtin/pipeline
> access (`get`, `first`, `|`, `filter`, `map`, `?.` over a computed base), or any
> node that reads `vars`/`actor` as a *value* rather than through a literal member
> access. A predicate with zero classifiable accesses is the degenerate case of the
> same rule, not the rule.

Implementation shape: after collecting literal member refs, walk the tree a second
time for **any remaining occurrence of the `vars`/`actor` identifier** that is not
already accounted for as the base of a literal `MemberNode`; if one exists, deny with
`ErrUndefinedReference`. That catches `get(vars,…)`, `vars | first()`, `len(vars)`,
`vars[actor.ID]` and every future builtin uniformly, without enumerating builtins.

⚠ Note it also catches `len(vars) == 0 or vars.status != "blocked"` — a row the
evidence file §3 currently records as extracting `vars.status` and passing. Decide
explicitly whether `len(vars)` is permitted (it reads the map as a value but cannot
observe a key) and state the verdict; do not leave it to the implementer.

Plan phase 1 test 4 must gain the **composite** rows above, each with its executed
`allowsToday=true` as the falsification witness. As written the test cannot fail
against the defective implementation.

---
## E5 — CRITICAL — `AuthzSpec` is persisted in TWO independent places; D3 cites the wrong one, and the phase-6 migration names neither

**Claims attacked.** ADR-0185 Decision 3, *"Why tri-state, and what it costs"*:

> *"Eligibility is a **stored** field, frozen into the task record at creation
> (`engine/step_nodes.go:723`); **all four `Authorize` sites read the stored spec**
> and never re-derive it from the definition. **The instance snapshot is
> `json.Marshal`ed (`internal/persistence/store/store_core.go:81`) and read back with
> a plain `json.Unmarshal` (`:174`)**, and `AuthzSpec` has no json tags."*

Evidence file §5 executes exactly and only that codec (*"The instance snapshot is
`json.Marshal`ed (`internal/persistence/store/store_core.go:81`, also `:231`) and read
back with a plain `json.Unmarshal` (`:174`)"*), and calls the result *"the executed
basis for ADR-0185 Decision 3"*. Plan phase 6: *"A migration that rewrites in-flight
open **human-task snapshots** whose `Eligibility` carries no dimension and no `Open`"*
— no table is named.

**Probe.** `/private/tmp/.../reaudit-exec-probe/p5/main.go` (`go run ./p5`), plus
source verification of both write sites.

**Observed (verbatim):**

```
PATH 1  wrkflw_instances.snapshot -> InstanceState.Tasks[].Eligibility:
  [{"Candidates":null,"Claim":null,"Completion":null,"CreatedAt":"0001-01-01T00:00:00Z","DueAt":null,"Eligibility":{"Attribute":"","Privileges":null,"Roles":null},"InstanceID":"i1","NodeID":"approve","State":0,"TaskID":"t1","Vars":null}]
PATH 2  wrkflw_human_task.eligibility column:
  {"Roles":null,"Privileges":null,"Attribute":""}
```

Source-verified anchors (all non-test):

- `engine/state.go:265` `type InstanceState struct`, `:286` `Tasks []humantask.HumanTask`
  — so every human task's `Eligibility` is **inside** the instance snapshot, written by
  `json.Marshal(capHistory(step.State, …))` at `internal/persistence/store/store_core.go:81`
  and `:231`, read at `:174`.
- `internal/persistence/store/migrations/sqlite/0001_init.sql:134-152`
  `CREATE TABLE wrkflw_human_task (… eligibility TEXT NOT NULL …)` — a **second,
  separate** column.
- `internal/persistence/store/humantask_store.go:157`
  `eligibility, err := json.Marshal(t.Eligibility)`; read back at `:397-399`
  `json.Unmarshal(eligibility, &t.Eligibility)`.
- `runtime/task/service.go:194-199` — `Claim` (and `:234` `Reassign`, `:255`
  `Complete`, `:306` `RefreshCandidates`) obtain the task via `s.store.Get(ctx, taskID)`,
  i.e. `humantask.TaskStore` ⇒ **PATH 2**, the `wrkflw_human_task.eligibility` column.

**Verdict: CONFIRMED — the mechanism survives, the premise and the migration do not.**

- **The good news, executed:** both paths are plain `encoding/json` over the same
  `authz.AuthzSpec`, so the tri-state *mechanism* (`Open *bool` → pre-upgrade row
  decodes `nil`) works identically on both. D3's **conclusion** stands.
- **The premise is the wrong code path.** D3 says *"all four `Authorize` sites read
  the stored spec"* and then evidences that sentence with the **instance-snapshot**
  codec. The four `Authorize` sites read PATH 2. The evidence file's §5 — labelled
  *"through the real snapshot codec"* and *"the executed basis for ADR-0185 Decision
  3"* — never touches `humantask_store.go`. It is the correct answer obtained from the
  wrong measurement, which is precisely the failure mode this revision was written to
  eliminate.
- **The migration is under-specified in a way that can silently half-apply.**
  Phase 6 says *"human-task snapshots"*, a phrase that names PATH 1's vocabulary
  (`snapshot`) and PATH 2's subject (`human task`). It must backfill **both** or the
  two copies diverge: `runtime/task`'s four verbs would read a backfilled
  `Open: true` from PATH 2 while ADR-0185 D5's completion guard in
  `handleHumanCompleted` reads `s.Tasks` from PATH 1, still `nil`. Divergent
  eligibility between the claim path and the completion path is exactly the class of
  defect ADR-0183 was written to close.
- Phase 6's three prescribed tests inherit the ambiguity: none of them names a table,
  so all three pass against a migration that fixes only one copy.

**Proposed fix (concrete).**

1. Correct D3's paragraph to name **both** stores and cite `humantask_store.go:157`
   and `:397-399` as the codec the four `Authorize` sites actually traverse; keep the
   `store_core.go` citation for the engine-side copy. State plainly: *"`AuthzSpec` is
   persisted twice — in `wrkflw_instances.snapshot` under `Tasks[].Eligibility`, and in
   `wrkflw_human_task.eligibility` — and both are plain `encoding/json` over the same
   type, so the tri-state behaves identically in both."*
2. Add the missing execution to the evidence file: round-trip a pre-upgrade
   `wrkflw_human_task.eligibility` value through `HumanTaskStore` on **SQLite**
   (`dbtest.RunTestSQLite`, pure Go, no container) and paste the result. The current
   §5 does not cover the path the decision rests on.
3. Rewrite phase 6 to prescribe **two** backfills, named by table and column, plus a
   fourth test `TestOpenBackfillCoversBothStores` asserting that after the migration
   `wrkflw_instances.snapshot`'s `Tasks[i].Eligibility.Open` and the matching
   `wrkflw_human_task.eligibility`'s `Open` **agree**. What makes it fail today: the
   migration does not exist, and against a single-table implementation the two values
   differ (`true` vs `nil`).
4. Add to ADR-0185 Consequences/Negative: *"the eligibility spec is stored twice and
   the two copies must be migrated together; a partial backfill produces a task whose
   claim path and completion path disagree."*

---
## E6 — MAJOR — D2 drops `ctx` to protect 99 ns/op, then replaces it with a mechanism costing 259 ns/op on a trivial env and 52 µs at its own bound

**Claim attacked.** ADR-0186 Decision 2:

> *"Honouring a ctx requires taking the goroutine path — measured on an ordinary
> gateway condition (`vars.amount > 100`): **99.43 ns/op / 3 allocs → 965.20 ns/op /
> 9 allocs**, ~9.7×, on CLAUDE.md's named hot path."*
> … *"`internal/expreval` gains `WithMaxEnvElements(n int)`: evaluation is refused,
> with a new `ErrEnvTooLarge`, when **the bounded element count reachable from the
> env** exceeds `n`."* … *"The default is 10 000 elements."*

The entire justification for dropping `ctx` is a per-evaluation cost on the token
step loop. **D2 never states the per-evaluation cost of the mechanism it substitutes.**

**Probe.** `zzprobe/bench_test.go` in the worktree (throwaway, deleted; `git status`
clean). `countElements` implements *"the bounded element count reachable from the
env"* as a reflect walk with early bail once the budget is exceeded — the only
reading of D2's sentence that can actually refuse an oversized env.
`go test -run XXX -bench Benchmark -benchtime=1s ./zzprobe/`.

**Observed (verbatim):**

```
goos: darwin
goarch: arm64
pkg: github.com/kartaladev/wrkflw/zzprobe
cpu: Apple M4 Pro
BenchmarkSyncFastPath-14             	12226282	        97.62 ns/op	      64 B/op	       3 allocs/op
BenchmarkGoroutinePath-14            	 1230066	       976.7 ns/op	     488 B/op	       9 allocs/op
BenchmarkCountElements_Typical-14    	 4637088	       259.0 ns/op	     168 B/op	       8 allocs/op
BenchmarkCountElements_AtBound-14    	   23006	     52008 ns/op	     168 B/op	       8 allocs/op
PASS
```

`_Typical` = a normal instance env (`{vars:{amount:250, items:[10 ints]}}`).
`_AtBound` = an env sized exactly at D2's proposed 10 000 default.

**Verdict: D2's benchmark HOLDS; D2's omission is CONFIRMED.**

- The dropped-ctx measurement **reproduces**: 97.62 vs the ADR's 99.43 ns/op, and
  **3 → 9 allocs exactly as stated**. That half of D2 is sound (see "Claims that
  held" below).
- But the substitute costs **259.0 ns/op on a ten-element env** — **2.65×** the
  97.62 ns evaluation it guards, and by itself **larger than the 99.43 ns figure the
  whole decision is written to defend**. At the bound it costs **52 µs**, ~530× the
  evaluation.
- The asymmetry is structural, not an artefact of a bad implementation: the walk can
  bail early only when the budget is *exceeded*. For every env **at or below** the
  bound — i.e. all legitimate traffic — the full walk runs, on **every** evaluation,
  on the same hot path. The bound is cheap exactly where it does nothing and
  expensive exactly where traffic lives.
- D2 argues the input bound is superior because *"an input bound is deterministic"*.
  That is true and unaffected. What is not established, and is asserted by omission,
  is that it is **cheaper**. On a typical env it is not.

**This is not a refutation of the decision — it is a refutation of the decision's
silence.** Dropping `ctx` may still be right. But the ADR presents a cost comparison
in which only one side is costed.

**Proposed fix (concrete).**

1. Add the measured cost of `WithMaxEnvElements` to D2, beside the 99.43/965.20 pair,
   at minimum for a typical env and at the bound. A decision that cites 9.7× as
   disqualifying must disclose its own 2.65×.
2. **Prescribe the counting strategy**, because the naive one is the expensive one.
   Concrete options to choose between, in the ADR:
   - **(a) Count once per `Step`, not per evaluation.** The env is the instance
     variable map; it does not change between the gateway evaluations of a single
     step. Hoist the count to `engine.Step`'s entry and pass a validated flag down.
     This makes the cost per-step rather than per-condition.
   - **(b) Bound only the top level** (`len(vars)`), an O(1) check that catches the
     `43 000 elements in one array` shape the ladder measures only if arrays are
     counted — so pair it with a per-value length check on slice-valued variables,
     still O(number of variables), not O(elements).
   - **(c) Count at the ingress boundary** (`service.WithMaxVariableBytes`'s
     neighbourhood) and store the count with the instance, so evaluation reads a
     number instead of recomputing a walk.
   (b) or (c) restores the hot path; (a) is the cheapest change. Pick one and say so —
   as written, an implementer will write the 259 ns version.
3. Add to plan phase 1 test 6 (`TestWithMaxEnvElementsRefusesOversizedEnv`) a sibling
   **benchmark** `BenchmarkMaxEnvElementsOverhead`, and state the regression budget.
   ADR-0186's Positive claims *"The engine default stays synchronous at ~99 ns/op"* —
   that sentence becomes **false** the moment `WithMaxEvalElements` is wired with the
   prescribed 10 000 default unless one of the fixes above is adopted. It must either
   be corrected or defended by a measurement.
4. ⚠ Correct the Consequences/Positive sentence *"The engine default stays synchronous
   at ~99 ns/op"* — it is true only for a driver that does **not** set
   `WithMaxEvalElements`, while the plan gives that option a **default of 10 000**.
   State which it is.

---

## E7 — MINOR — `runtime.WithMaxEvalElements` silently collides with the two existing options that set the same field

**Claim attacked.** ADR-0186 D2: *"`runtime.WithMaxEvalElements(n int)` is the
plumbing, and it is **real**: it constructs the driver's evaluator
(`expreval.New(expreval.WithTimeout(0), expreval.WithMaxEnvElements(n))`) and assigns
`driver.conditionEval`."* Plan phase 5 repeats it verbatim and adds
*"⚠ `runtime.WithConditionEvaluator` and `WithExpressionTimeout` … keep their
signatures."*

**Probe.** Source trace of the field the three options share (non-test):

```
runtime/processdriver_options.go:200:		driver.conditionEval = expreval.New(expreval.WithTimeout(d))
runtime/processdriver_options.go:220:			driver.conditionEval = eval
runtime/processdriver.go:107:	conditionEval engine.ConditionEvaluator
runtime/processdriver.go:674:			Evaluator:           driver.conditionEval,
```

and the existing godoc, `runtime/processdriver_options.go:196-197`:
*"WithExpressionTimeout and [WithConditionEvaluator] set the same field; **the last
option wins**."*

**Verdict: the plumbing claim HOLDS; the collision is CONFIRMED and undocumented.**
`driver.conditionEval` does reach `engine.Step` via `StepOptions.Evaluator` at the
single `engine.Step` call site in the repo (`runtime/processdriver.go:671`), so D2 is
**not** a zombie — that was the sharpest thing this finding looked for and it survived.

But `WithMaxEvalElements` would be the **third** option writing that one field, and
the existing two already document last-writer-wins. Consequences:

- `WithExpressionTimeout(5*time.Second)` + `WithMaxEvalElements(10000)` gives the
  consumer **whichever came last**, silently. There is no combination that yields both
  a wall-clock guard and an element bound — precisely the pairing an operator
  evaluating untrusted definitions would ask for.
- ADR-0186 D2 says the element bound is what *"actually stops the CPU burn"* and
  ADR-0056's timeout is what bounds latency. The two are complementary by the ADR's
  own argument, and the proposed API makes them mutually exclusive.
- If `WithMaxEvalElements` has a **default of 10 000** (plan phase 5), then a driver
  built with `WithExpressionTimeout` *loses* the default bound — or the default bound
  overwrites the timeout, depending on where the default is applied. The plan does not
  say which, and both are wrong.

**Proposed fix.** Do not add a third whole-evaluator option. Make the element bound a
**driver field** (`driver.maxEvalElements int`) applied when the driver *builds* its
evaluator, so it composes with both existing options:

```go
func WithMaxEvalElements(n int) Option { return func(d *ProcessDriver) { d.maxEvalElements = n } }
// at driver construction, after all options have run:
//   if d.conditionEval == nil { d.conditionEval = expreval.New(expreval.WithTimeout(0), expreval.WithMaxEnvElements(d.maxEvalElements)) }
```
and state explicitly what happens when a consumer supplies their own
`WithConditionEvaluator` — the bound **cannot** be applied to a foreign evaluator, so
document that `WithMaxEvalElements` is ignored in that case, or return a construction
error. Add plan phase 5 test `TestMaxEvalElementsComposesWithExpressionTimeout`.
**What makes it fail today:** neither option exists; against the ADR's prescribed
implementation the two options overwrite each other and the test observes only one.

---
## E8 — CRITICAL — ADR-0186 D5's value-free 400 is NOT implementable where it is prescribed: `runtime/validation` flattens the structured error with `%s`

**Claim attacked.** ADR-0186 Decision 5:

> *"`ClassifyError` gains a per-class message policy … **400** | **value-free
> rendering** … `*jsonschema.ValidationError` exposes `InstanceLocation []string` and
> `ErrorKind.KeywordPath() []string`, so the rendering is **built from the structured
> leaves**: `at '/ssn': violates pattern`. **Executed — the leaves are reachable from
> the public API, so this is feasible rather than aspirational.**"*

Evidence file §6 supports it with *"structured leaves via `*jsonschema.ValidationError`:
`InstanceLocation=[ssn]  ErrorKind.KeywordPath()=[maxLength]`"* — obtained by calling
the **jsonschema library directly**, not through the repo's own validation path.

**Probe.** `zzprobe/valerr_test.go` in the worktree (throwaway, deleted): run the real
`runtime/validation.Gate.Validate` over the repo's real
`definition/model/validate/jsonschema` strategy with the ADR's own
`{"ssn":"123-45-6789"}` input, then ask whether `httpcore.ClassifyError` can reach the
structured error.

**Observed (verbatim):**

```
=== RUN   TestValidationErrorStructureAtTheTransportBoundary
    valerr_test.go:28: err from Gate.Validate:
        workflow-validation: invalid input: workflow-validation/jsonschema: jsonschema validation failed with 'mem://schema.json#'
        - at '/ssn': maxLength: got 11, want 3
        - at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'
    valerr_test.go:29: errors.Is(err, validation.ErrInvalidInput) = true
    valerr_test.go:35: errors.As(err, **jsonschema.ValidationError) = false
    valerr_test.go:36: errors.Unwrap(err) = workflow-validation: invalid input
    valerr_test.go:39: ClassifyError -> status=400
    valerr_test.go:40: ClassifyError -> body.Message="workflow-validation: invalid input: workflow-validation/jsonschema: jsonschema validation failed with 'mem://schema.json#'\n- at '/ssn': maxLength: got 11, want 3\n- at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'"
--- PASS
```

Root cause, source-verified — `runtime/validation/gate.go:44-46`:

```go
if err := v.Validate(ctx, input); err != nil {
    return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
}
```

`%w` wraps the **sentinel**; the real error is interpolated with **`%s`** and then
**discarded**. `errors.Unwrap` yields `ErrInvalidInput` and nothing else, so
`errors.As(err, **jsonschema.ValidationError)` is **false** at every point downstream.

**Verdict: CONFIRMED, two halves.**

- **The leak half HOLDS.** The 400 body reproduces the submitted value verbatim
  (`'123-45-6789' does not match pattern`) and the `maxLength` length disclosure
  (`got 11, want 3`), exactly as ADR-0186 Context §5 and evidence file §6 state.
  That finding is real and this probe independently reproduces it through the full
  repo path, which the bundle's own evidence did not.
- **The fix half is placed where it cannot work.** D5 puts the value-free rendering in
  `ClassifyError`. At that point the structured leaves no longer exist — they were
  flattened into a string one layer down. `ClassifyError` would have to **parse the
  error text** to strip values, which is a fragile string-surgery fix nobody in the
  bundle chose. So D5's *"feasible rather than aspirational"* is **false as scoped**:
  feasible in `runtime/validation`, not feasible in `ClassifyError`.
- This is the same failure mode as E5 and the same one the revision was written to
  eliminate: the evidence measured the **vendor** where the decision acts on the
  **repo's wrapper**. Two independent instances in one bundle suggests it is a
  systematic habit, not a slip.

**Proposed fix (concrete).**

1. **Move the rendering to `runtime/validation/gate.go`**, the only place the
   structured error is alive. Change `Gate.Validate` to preserve it *and* render a
   value-free message:
   ```go
   if err := v.Validate(ctx, input); err != nil {
       return fmt.Errorf("%w: %s", ErrInvalidInput, renderValueFree(err)) // + keep err wrapped
   }
   ```
   Preferably wrap **both** so an operator-side logger can still reach the detail:
   `fmt.Errorf("%w: %s: %w", ErrInvalidInput, renderValueFree(err), err)` (Go 1.20+
   multi-`%w`), then `ClassifyError` needs no change for 400 at all.
2. `renderValueFree` lives beside the jsonschema adapter (it is the only component
   that may import `santhosh-tekuri/jsonschema/v6`) and walks
   `*jsonschema.ValidationError.Causes` emitting
   `at '<InstanceLocation>': violates <KeywordPath[0]>` for **every** keyword — the
   evidence file's own warning that a `pattern`-only fix leaves the `maxLength` length
   disclosure applies here.
3. **Correct ADR-0186 D5's placement sentence** and its Consequences: the change is in
   `runtime/validation` (+ the jsonschema adapter), not in `ClassifyError`. Note that
   this moves the work out of `transport/http` entirely, which changes the plan's
   phase assignment.
4. The plan currently has no phase for `runtime/validation`. Add one, with a test
   `TestGateRendersValueFreeMessage` asserting the message contains `'/ssn'` and
   `pattern` and **does not contain** `123-45-6789` **or** `11`.
   **What makes it fail today:** executed above — today's message contains
   `'123-45-6789' does not match pattern '^[0-9]{3}$'` and `got 11, want 3`.
5. Add to the plan a note that `errors.As` to `*jsonschema.ValidationError` is **not**
   available downstream of `gate.go`, so no future decision may assume it is.

---
## E9 — MINOR — "strictness is opt-in per evaluator instance" is false for `RoleAuthorizer`: its evaluator is a package-level global and it has no instance state

**Claim attacked.** ADR-0185 D4: *"The honest statement is that strictness is
**opt-in per evaluator instance**, so it must be turned on at every site that
evaluates `spec.Attribute`: 1. `authz.RoleAuthorizer` —
`expreval.New(expreval.WithStrictReferences())`."*

**Probe.** Source, `authz/authz.go:20-23` and `:113`:

```go
// attrEval is the package-level expression evaluator for attribute predicates.
// A single shared instance is safe for concurrent use; memoization is
// referentially transparent. Mirrors the pattern in engine/conditions.go.
var attrEval = expreval.New()
...
type RoleAuthorizer struct{}
```

**Verdict: CONFIRMED (wording defect, not a design defect).** `attrEval` is a
**package-level variable**, and `RoleAuthorizer` is a zero-size struct with nowhere to
carry a per-instance option. Changing that line makes strictness **global and
mandatory** for every `RoleAuthorizer` in every consumer's process — the opposite of
"opt-in per evaluator instance". The same is true of `internal/authz/casbin`, whose
`attrEval` *is* per-`Authorizer` (`New()` at `:30`) but is not configurable from
outside either.

The design is probably right — a security default should be on — but the sentence
describing it is wrong in a way that will mislead the implementer into looking for an
option plumb-through that has nowhere to attach.

**Proposed fix.** Replace the sentence with: *"Strictness is a property of the
evaluator instance, and each of the two ABAC sites owns its own: `authz`'s is the
package-level `attrEval` (`authz/authz.go:23`) and casbin's is per-`Authorizer`
(`internal/authz/casbin/authorizer.go:30`). Turning it on is therefore
**unconditional** for both — `RoleAuthorizer` is a zero-size struct and has no seam to
make it optional. A consumer who needs the permissive behaviour supplies their own
`Authorizer`."* Add the same statement to ADR-0185's Negative consequences, beside
*"Strict reference checking will deny predicates that work today"* — which currently
reads as though a consumer could turn it off.

---

# Load-bearing claims that HELD

The controller needs these as much as the findings. Each was executed, not read.

## H1 — ADR-0186 D2's dropped-ctx benchmark reproduces almost exactly

Claim: *"99.43 ns/op / 3 allocs → 965.20 ns/op / 9 allocs, ~9.7×"* for
`vars.amount > 100` on `expreval` with `WithTimeout(0)` vs a positive timeout.

```
BenchmarkSyncFastPath-14             	12226282	        97.62 ns/op	      64 B/op	       3 allocs/op
BenchmarkGoroutinePath-14            	 1230066	       976.7 ns/op	     488 B/op	       9 allocs/op
```

97.62 vs 99.43 and 976.7 vs 965.20; **alloc counts 3 and 9 match exactly**. ~10.0×
here vs the ADR's ~9.7×. The argument for dropping `ctx` is soundly measured.
(Its *omission* is E6; the measurement itself is not in doubt.)

## H2 — `expreval.run`'s synchronous fast path is exactly what D2 says

`internal/expreval/expreval.go:74-76`:

```go
func (e *Evaluator) run(p *vm.Program, env map[string]any) (any, error) {
	if e.timeout <= 0 {
		return expr.Run(p, env)
	}
```

Confirmed verbatim. *"there is no mechanism by which a ctx cancellation interrupts
it"* holds — no goroutine, no select, no ctx parameter anywhere on the path.

## H3 — The O(n²) ladder reproduces to within measurement noise, and the predicate is 80 bytes

The spec states its predicate — `count(vars.rows, {let x = #; count(vars.rows, {# == x}) == 1}) == len(vars.rows)`
— and asks the re-audit to re-measure. Re-measured on this machine (Apple M4 Pro):

```
=== RUN   TestSpecLadder
    ladder_test.go:14: predicate len = 80 bytes
    ladder_test.go:28: n=1000   elapsed=25ms       out=true
    ladder_test.go:28: n=2000   elapsed=99ms       out=true
    ladder_test.go:28: n=4000   elapsed=393ms      out=true
    ladder_test.go:28: n=8000   elapsed=1.57s      out=true
```

vs the spec's 25 ms / 98 ms / 391 ms / 1.563 s. Clean 4× per doubling. The **80
characters** correction the revision made against the draft's "44" is also confirmed
(`len(pred) == 80`). The `ASSUMPTION (unverified)` on the ladder can be **discharged**;
the extrapolated column (5 000 ≈ 610 ms, 10 000 ≈ 2.4 s) remains arithmetic, but it is
arithmetic on a ladder that now reproduces.

## H4 — The `ASSUMPTION (unverified)` on ctx propagation is TRUE in all four legs — discharge it

Evidence file §8: *"`ASSUMPTION (unverified)`: that the request context reaches
`httpcore` unmodified in all three adapters, and that fiber's `c.Locals` does **not**
propagate. … **the re-audit should re-run it.**"* Re-run end to end — real `Mount`,
real middleware, a `service.Service` double recording the ctx `httpcore` hands down:

```
=== RUN   TestCtxPropagation
    ctxprop_test.go:45: stdlib   (r.WithContext)      -> "stdlib-mw"
    ctxprop_test.go:58: gin      (Request.WithContext)-> "gin-mw"
    ctxprop_test.go:73: fiber    (c.SetContext)       -> "fiber-SetContext"
    ctxprop_test.go:88: fiber    (c.Locals)           -> "<ABSENT>"
--- PASS
```

ADR-0185 D1 is correct on **both** legs: the request context reaches `httpcore`
unmodified in all three adapters, and **`c.Locals` does not propagate** — a consumer
following fiber's most idiomatic path really does get a silently unauthenticated
request. Resolving the actor once in `httpcore` is sound, and the `SECURITY.md`
warning about `c.SetContext` is load-bearing and correct. **Relabel this from
`ASSUMPTION (unverified)` to executed, and cite this file.**

## H5 — `runtime.WithMaxEvalElements`'s plumbing is REAL, not a zombie

The sharpest thing this lens looked for. `driver.conditionEval`
(`runtime/processdriver.go:107`) is assigned by the existing options at
`processdriver_options.go:200,220` and read at `processdriver.go:674` as
`StepOptions.Evaluator`, into the repo's **single** `engine.Step` call site
(`processdriver.go:671`). `engine/conditions.go:49-53` `resolveEvaluator` prefers
`opt.Evaluator` over the package-global `conditions`. So a `runtime`-level evaluator
**does** reach the engine's condition evaluation. ADR-0186 D2's *"it is **real**"*
holds. (The option-collision problem is E7 and is separate.)

## H6 — The `get()` / `??` / `has()` corrections the revision made against the PREVIOUS AUDIT are all correct

The brief asked specifically whether the revision is wrong about the audit being
wrong. It is not — every one of the four checked out:

- `has(vars,"tier")` → `invalid operation: cannot call nil (1:1)` at **run** time,
  after a successful compile under `AllowUndefinedVariables()`. Confirmed.
- `vars.tier ?? "none" == "gold"` → **compile error**
  (*"Operator (==) and coalesce expressions (??) cannot be mixed"*); only the
  parenthesised form parses. Confirmed.
- `get(vars,"tier") == "gold"` → extracts **zero** references from the parse tree.
  Confirmed (see E4's probe output, `refs=[]`).
- The audit's proposed element bounds *"5 000 ≈ 40 ms, 10 000 ≈ 150 ms"* are wrong
  against the re-measured ladder: 4 000 alone is **393 ms** measured, so 5 000 cannot
  be 40 ms. The revision's ~15× correction is right.

## H7 — A guard must dominate its use — the direction is right, the rule is incomplete

The revision's core insight against the previous audit — that a tree-wide `in`
collector is unsound — is **correct and reproduced**: rows 2 and 3 of its table
evaluate `true` with `vars` empty while a naive collector calls them guarded.
Dominance is the right axis. It is just not sufficient (E2).

## H8 — The 400 value leak is real, through the full repo path

`ClassifyError` returns 400 with a body containing `'123-45-6789' does not match
pattern '^[0-9]{3}$'` and `maxLength: got 11, want 3`. ADR-0186 Context §5 and the
evidence file's resolution of spec §4.7's `ASSUMPTION (unverified)` both hold — and
now hold through the repo's own `Gate`, not just the vendor. (The *fix's* placement is
E8.)

---

# RANKED INDEX

| # | Severity | Finding | Verdict |
|---|---|---|---|
| **E1** | **Critical** | Hoisted `CheckSpecStated` makes D5's `reassign` privilege unauthorable and kills casbin's privilege dimension; ADR-0185 contradicts itself (D3 "under `RoleAuthorizer`" vs Consequences "above all four `Authorize` calls"); plan phase-2 test 5 and phase-5 test 2 cannot both pass | CONFIRMED |
| **E2** | **Critical** | The guard-DOMINANCE rule is still unsound — 4 further predicates (negated guard under `and`, `== false` guard, disjunctive guard, ternary **alternate**) allow on an absent key while satisfying D4 verbatim; the bundle's "the falsifying table" is not falsifying and plan phase-1 test 3 cannot catch it | CONFIRMED |
| **E3** | **Critical** | Strict references give **zero** protection on the `actor` axis: `actor.Attributes.*` is inherently depth-2 behind an always-present struct field, so deny-list actor predicates still allow; D1's "closes finding 4's second leg for free" is false; the evidence file's own `actor.attributes…` example is a run-time error that denies everyone | CONFIRMED |
| **E4** | **Critical** | The zero-reference rule does **not** close the `get()` bypass — one ordinary reference disarms it (`vars.region == "eu" or get(vars,"blocked") != true`); plan phase-1 test 4's bare-bypass fixtures cannot discriminate | CONFIRMED |
| **E5** | **Critical** | `AuthzSpec` is persisted **twice** (`wrkflw_instances.snapshot` → `Tasks[].Eligibility`, and `wrkflw_human_task.eligibility`); D3 evidences the tri-state against the copy the four `Authorize` sites do **not** read, and phase 6's migration names no table — a partial backfill makes the claim path and completion path disagree | CONFIRMED (conclusion survives, premise + migration do not) |
| **E8** | **Critical** | ADR-0186 D5's value-free 400 is not implementable in `ClassifyError`: `runtime/validation/gate.go:45` flattens the error with `%s`, so `errors.As(err, **jsonschema.ValidationError) == false` downstream; evidence measured the vendor, not the repo's wrapper | CONFIRMED (leak real, fix misplaced) |
| **E6** | **Major** | D2 drops `ctx` to protect 99 ns/op, then substitutes a bound costing **259 ns/op** on a ten-element env and **52 µs** at its own 10 000 default — undisclosed; "the engine default stays synchronous at ~99 ns/op" becomes false once the option is wired with its default | CONFIRMED (D2's own benchmark HELD) |
| **E7** | **Minor** | `runtime.WithMaxEvalElements` would be the **third** option writing `driver.conditionEval`, whose existing two document last-writer-wins — no way to combine a timeout with an element bound, and the default's interaction is unspecified | CONFIRMED (plumbing itself HELD) |
| **E9** | **Minor** | "strictness is opt-in per evaluator instance" is false for `RoleAuthorizer` — `attrEval` is a package-level global and `RoleAuthorizer` is `struct{}`; turning it on is unconditional and global | CONFIRMED (wording) |

**6 Critical · 1 Major · 2 Minor.**

**Held:** H1 the dropped-ctx benchmark · H2 the synchronous fast path · H3 the O(n²)
ladder (+ the 80-byte correction) · H4 ctx propagation in all three adapters and
`c.Locals` not propagating — **discharge the `ASSUMPTION`** · H5 `WithMaxEvalElements`
plumbing is real · H6 all four corrections the revision made against the previous
audit are right · H7 dominance is the right axis · H8 the 400 value leak, through the
full repo path.

**Meta-observation for the controller.** E5 and E8 are the *same* error twice: a
load-bearing claim was evidenced against the **vendor or a stand-in** where the
decision acts on the **repo's own wrapper one layer down**. The revision's stated
purpose was to stop exactly this. A cheap standing rule: *the probe must enter through
the same function the production caller enters through.* E1, E2, E3 and E4 are all in
ADR-0185 D3/D4/D5 — the three decisions the revision rewrote — which is where a
re-audit should expect to find them, and did.
