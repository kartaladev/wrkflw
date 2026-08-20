# Plan — authorization & security hardening (ADR-0185 + ADR-0186)

> ## ⚠ SUPERSEDED IN PART — 2026-08-21. The ADR-0186 half has MOVED to its own delivery.
>
> B3 was re-cut into three deliveries (owner decision, 2026-08-21). The
> untrusted-input-and-disclosure half now lives in its own bundle and **that is the
> authoritative version of it**:
> `docs/specs/2026-08-21-untrusted-input-and-disclosure.md` +
> `docs/adr/0186-untrusted-input-and-disclosure-posture.md` +
> `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`.
> ⚠ **Do NOT implement ADR-0186 material from this document** — six re-audit findings
> were folded into the new bundle and are not reflected here.
>
> What remains live here is the **ADR-0185 (identity) material**, which is **NOT yet
> re-cut** and still carries both failed audits' findings unresolved — in particular
> D3's two confirmed defects (`AuthzSpec` is durable in TWO places, and `Open *bool`
> makes a PUBLIC struct's zero value fail-OPEN) and the deferral of D4 (backlog 103)
> and D5 (backlog 124) to their own bundles.

> ## ⛔ RE-AUDIT FAILED — 2026-08-21. Phase 1 must NOT start.
>
> Second failed audit. ~13 distinct Criticals; five are holes this revision's own fixes
> opened in each other. Several prescribed tests are proven unable to catch what they
> are billed as deciding (phase-1 test 3, phase-2 test 5 vs phase-5 test 2, phase-6's
> untargeted migration). See `docs/plans/sweep-evidence/reaudit-b3-adjudication.md`.
> **A scope decision is pending before any further revision.**
>
> The 2026-08-20 draft failed its rule-#9 audit (58 findings, 12 Critical).
> Four ADR Decisions **changed**, so per rule #9 the bundle has not been audited:
> a re-audit of *this* revision is the next action, not phase 1.
> Adjudication: `docs/plans/sweep-evidence/audit-b3-adjudication.md`.
> Executed evidence for the revision:
> `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`.

## ▶ Progress

- **Branch:** `design/authz-security-b3` (docs-only; rebased onto `main` after the
  backlog sweep merged — do not quote its SHA, it is amended on every revision).
- **State:** design bundle **revised** 2026-08-21 against the failed audit.
  Rule-#9 **RE-audit PENDING**. Zero phases executed. No `.go` file touched.
- **Decisions changed since the draft** (owner-adjudicated 2026-08-21):
  1. ADR-0185 D3 — `Open` is a **tri-state `*bool`** plus a data migration phase.
  2. ADR-0185 D4 — escape hatch rewritten (`has()` does not exist); guard must
     **dominate** its use; zero-reference and dynamic-key predicates **deny**;
     strictness applied at **both** ABAC sites plus a **mint-time** check.
  3. ADR-0185 D5 — the claimant guard covers **`Reassign`**.
  4. ADR-0186 D2 — the `ctx` on `ConditionEvaluator` is **DROPPED**; the input
     bound is the whole mitigation.
  Plus ADR-0185 D1 gains 401 / 503 / `WithAnonymousActorAllowed` and the rename to
  `WithRequestActor`; ADR-0186 D4 moves redaction above `InstanceMapper`; ADR-0186
  D5 gains a value-free 400 rendering.
- **Adjudicated findings:** all 12 Criticals accepted; Majors folded per the
  adjudication record. Nothing was rejected as a false positive.
- **Verification commands:** see §6.

---

## 0. What the RE-AUDIT must attack

The revision's authors flag these as the likeliest places to still be wrong. Give
them to the auditors explicitly; do not let the lenses re-derive the list.

1. **The guard-dominance rule (ADR-0185 D4).** Execution already refuted one
   implementation of it: a tree-wide `in` collector calls
   `"tier" in vars or vars.tier != "blocked"` guarded while it **allows** on an
   absent key. The rule is now "the guard must dominate the use (left operand of
   `and`, or a ternary condition)". ⚠ **That rule itself has not been implemented,
   only specified.** Attack whether *dominance* is decidable over the shapes expr
   actually produces, and whether the ternary case is real.
2. **The zero-reference deny.** `get(vars,"k")` and pipe forms extract nothing and
   are therefore denied. Is that *too* aggressive — does it deny a predicate a
   reasonable policy author would write? Enumerate the zero-reference shapes.
3. **The tri-state migration.** `Open == nil` grandfathers pre-upgrade rows. Attack:
   is `nil` reachable from any *authoring* path (it must not be)? Does the backfill
   migration have to run on every dialect, and what happens if it is interrupted?
4. **The `Reassign` privilege (ADR-0185 D5).** It introduces a new privilege token
   into a dimension `RoleAuthorizer` **cannot evaluate** — so under D3 a spec
   carrying it denies unless casbin is wired. Is that coherent, or does D5 make
   reassignment unusable for `RoleAuthorizer` deployments? **This is the revision's
   most likely self-contradiction.**
5. **The env-element bound.** 10 000 ⇒ ~2.4 s is *extrapolated* from the
   2026-08-20 ladder, not re-measured. Re-measure it. Also attack whether
   "elements reachable from the env" is cheaply computable without walking a large
   map on every evaluation — the bound must not cost more than it saves.
6. **`ASSUMPTION (unverified)`, carried forward:** the fiber `len(c.Body())`
   mechanism; the 1 MiB default; and **that the request ctx reaches `httpcore`
   unmodified in all three adapters while `c.Locals` does not**. The last one was
   executed *by the previous audit's execution lens* and is load-bearing for
   deleting the adapter actor work — **re-run it, do not inherit it.**
7. **ADR-0186 Context §4's withdrawn claim.** "Mutating the view mutates instance
   state" is now `ASSUMPTION (unverified)` in **both** directions. Either execute it
   or confirm the hedge is the right resting place.
8. **One lens must be the counting lens** (CLAUDE.md rule #9). The draft rotted five
   enumerations; this revision re-derived all of them (§5). Assume there is a sixth.
9. **Every auditor gets the step-0 worktree check**: worktrees are created at the
   base commit and the bundle is typically **absent**. Verify it is present; STOP if
   not. Create the worktree **detached at the bundle commit**.

---

## 1. Fan-out rules for this delivery

- **Fan out by Go package.** Concurrent agents inside one package break each other's
  `go test` compile even on disjoint files.
- **Phase 4 (`engine`) stays INLINE in the controller.** It carries the claimant
  guard and the mint-time reference check — the pieces every later phase's fixtures
  depend on.
- **Each phase migrates its own fixtures.** The runtime rule (ADR-0185 D3) breaks
  no-eligibility `UserTask` fixtures in `engine`, `runtime`, `processtest` and
  `service` — **128** sites (§5). They are not a separate phase; each package's
  agent fixes its own, because a cross-package fixture agent would collide with
  every other phase.
- **Docker:** the standing carve-out covers the Verification runs only. Any agent
  needing containers must be told so **explicitly in its brief**. Container-free
  here: `engine`, `service`, `runtime/task`, `transport/http/*`, `processtest`,
  `definition/*`, `internal/expreval`, `authz`, `action/httpcall`.
  ⚠ **`internal/authz/casbin` needs Postgres** (pgxpool + LISTEN/NOTIFY) and
  ⚠ **`internal/persistence/store` needs containers** — phases 6 and 10 must say so.
  ⚠ `./runtime/...` **as a whole** is not container-free; scope phase 5's verify
  command or state the requirement.
- **`golangci-lint`:** probe `command -v golangci-lint` and run it; if absent, say so
  and offer install-or-skip. Never substitute `go vet`.
- **`engine/` mixes `package engine` and `package engine_test`** — `head -1` any
  existing test file before writing into it.
- ⚠ **A mutation ablation gets its own `git worktree`.** A live ablation in a shared
  tree cost another agent ~40 minutes as a phantom hang.

---

## 2. Phase table

⚠ **The draft's phases 3 and 4 were circular**: phase 3 (`engine`) was told to write
`Open: ut.OpenEligibility`, a field phase 4 created. `definition` now lands **first**
— it is purely additive and does not break `engine`'s compile.

| # | package(s) | ADR decisions | depends on | fan-out |
|---|---|---|---|---|
| 1 | `internal/expreval` | 0185 D4, 0186 D2 | — | 1 agent |
| 2 | `authz` | 0185 D1, D3, D4 | 1 | 1 agent |
| 3 | `definition/activity`, then `definition/model` | 0185 D3 | 2 | 1 agent, serial inside |
| 4 | `engine` | 0185 D3(wiring), D4(mint), D5 | 2, 3 | **controller, inline** |
| 5 | `runtime`, `runtime/task` | 0185 D3(gate), D5, 0186 D2 | 4 | 1 agent |
| 6 | `internal/persistence/store` | 0185 D3 (migration) | 2 | 1 agent ⚠ Docker |
| 7 | `service` | 0185 D2, 0186 D1(b) | 5 | 1 agent |
| 8 | `action/httpcall` | 0186 D3 | 1 | 1 agent (‖ 7) |
| 9 | `transport/http/httpcore` | 0185 D1, 0186 D1, D4, D5 | 2, 7 | 1 agent |
| 10 | `casbinauthz` + `internal/authz/casbin` | 0185 D4, D6 | 2 | 1 agent (‖ 9) ⚠ Docker |
| 11 | `processtest` | 0185 D3 (fixtures) | 5 | 1 agent (‖ 9, 10) |
| 12 | `transport/http/stdlib` \| `gin` \| `fiber` | 0186 D1(a) + pins | 9 | **3 agents in parallel** |
| 13 | `transport/http/parity` | — (test fallout) | 12 | 1 agent |
| 14 | `examples/*` | — (wiring) | 12 | 1 agent |
| 15 | docs | all | 14 | controller |

Phases 7 and 8 may run concurrently; 9, 10 and 11 may run concurrently. Phase 12's
three agents are the only true fan-out.

---

## 3. Phases

### Phase 1 — `internal/expreval`: strict references and an input bound

⚠ **No `ctx` methods.** ADR-0186 D2's ctx half is dropped; do not add
`EvalBoolContext` or its siblings. The existing three methods keep their signatures.

**Symbols to add:**
- `func WithStrictReferences() Option` — evaluation denies with a new
  `ErrUndefinedReference` when the predicate references a key absent from `env`.
- `func WithMaxEnvElements(n int) Option` — refuses an env whose bounded element
  count exceeds `n` with a new `ErrEnvTooLarge` (0 = unbounded, current behaviour).
- unexported `referencedKeys(code string) ([]ref, error)` — `parser.Parse` +
  `ast.Walk`, cached beside the compiled program. ⚠ `ast.Walk` takes an
  `ast.Visitor` **interface**, not a bare func; a one-method adapter type is needed.

**Tests, and what makes each fail today:**

1. `TestStrictReferencesDeniesAbsentKey` — table over the five **sampled** forms
   (`vars.status != "blocked"`, `vars.blocked != true`, `!(vars.blocked == true)`,
   `vars.tier == nil or vars.tier == "gold"`, `vars.status == "ok" or "manager" in
   actor.Roles`) plus `not vars.blocked` and `vars.a == vars.b`, plus the positive
   control `vars.region == "eu"`.
   **Fails today:** with `env = {"vars": map[string]any{}}` every deny-list form
   returns `(true, nil)` — executed. Under `WithStrictReferences()` each must return
   `ErrUndefinedReference`.
   ⚠ **Fixture check:** the `vars` map must be **empty**. A fixture that populates
   `status` cannot fail for this reason.
   ⚠ Do not describe the table as "the class" — ADR-0185 D4 states the class is
   unbounded and these are a sample.
2. `TestStrictReferencesTreatsGuardedKeyAsOptional` — `"tier" in vars and vars.tier
   == "gold"` and `vars?.tier == "gold"` stay evaluable with `vars` empty.
   **Fails today:** the option does not exist → compile error.
   ⚠ **Do NOT write `has(vars,"tier")`** — executed, it is not a builtin in
   v1.17.8 and fails at run time.
   ⚠ **Do NOT write `vars.tier ?? "none" == "gold"`** — executed, it is a compile
   error; only the parenthesised form parses.
3. `TestGuardMustDominateItsUse` — **the control that decides D4.** Table, `vars`
   empty:
   | predicate | expected |
   |---|---|
   | `"tier" in vars and vars.tier == "gold"` | evaluates, `false` |
   | `"tier" in vars or vars.tier != "blocked"` | **`ErrUndefinedReference`** |
   | `not ("tier" in vars) or vars.tier == "x"` | **`ErrUndefinedReference`** |
   **Fails today:** the option does not exist. ⚠ **It also fails against the naive
   implementation** — a tree-wide `in` collector returns `(true, nil)` for rows 2
   and 3, which is the hole D4 exists to close. Executed: both rows evaluate `true`
   today. **Without this test the naive implementation ships and the suite is green.**
4. `TestStrictReferencesDeniesUnresolvableReferences` — `get(vars,"k") == "x"`,
   `vars | first() != "blocked"` and `vars[actor.ID] == "x"` all return
   `ErrUndefinedReference`.
   **Fails today:** the option does not exist. ⚠ Executed: the first two extract
   **zero** references, so an implementation that denies only on *known-absent* keys
   passes them through — this test is what forces the fail-closed reading.
5. `TestReferencedKeysExtractsBracketAndDepthOne` — `vars["dept"]` yields
   `vars.dept`; `vars.order.total` yields `vars.order` (depth-1, documented).
6. `TestWithMaxEnvElementsRefusesOversizedEnv`.
   **Fails today:** the option does not exist → compile error.

**Do NOT** call `expr.MaxNodes` — the inversion is executed and stated in the vendor
godoc (`expr@v1.17.8/expr.go:221`): not calling it leaves `DefaultMaxNodes = 1e4`
active, and `MaxNodes(0)` would *disable* the existing protection.

**Verify:** `go test -race -count=1 ./internal/expreval/...`

---

### Phase 2 — `authz`: identity in context, tri-state `Open`, strict ABAC, the hoistable gate

**Symbols to add:**
- `ContextWithActor(ctx context.Context, a Actor) context.Context`
- `ActorFromContext(ctx context.Context) (Actor, bool)` — unexported key type.
- `AuthzSpec.Open *bool` — ⚠ **pointer, not bool.** See §4.
- `var ErrUnevaluatableSpec = errors.New("workflow-authz: spec declares no dimension this authorizer evaluates")`
- `func CheckSpecStated(spec AuthzSpec) error` — the gate phase 5 hoists above all
  four `Authorize` sites so **every** `Authorizer`, including a consumer's own,
  inherits it.
- `RoleAuthorizer`'s evaluator becomes
  `expreval.New(expreval.WithStrictReferences())`.

**Tests, and what makes each fail today:**

1. `TestRoleAuthorizerDeniesUnstatedSpec` — table: zero spec, `Roles: []string{}`,
   `Roles: nil`, `Privileges`-only, **and `{Roles:["manager"], Privileges:["x"]}`**.
   **Fails today:** all five return `err=<nil>` for the **zero actor** — executed for
   the first four; the mixed row passes the role check and silently drops the
   privilege. Each must return `errors.Is(err, ErrNotAuthorized)`; the two
   privilege-carrying rows must additionally satisfy
   `errors.Is(err, ErrUnevaluatableSpec)`.
   ⚠ **The mixed row is the one the draft's *"whose **only** dimension"* wording
   left open, and it is the row that looks configured.** For it the actor **must**
   carry `Roles: ["manager"]`, or it denies on the role check and the test proves
   nothing about privileges.
   ⚠ **Fixture check:** for the other four rows the actor must be the **zero** actor.
2. `TestRoleAuthorizerAllowsExplicitOpenSpec` — `AuthzSpec{Open: ptr(true)}` admits
   an actor. **Fails today:** `AuthzSpec.Open` does not exist → compile error.
3. `TestOpenNilIsGrandfatheredOpen` — `AuthzSpec{Open: nil}` with no dimension
   **allows**, and `AuthzSpec{Open: ptr(false)}` with no dimension **denies**.
   ⚠ **This is the tri-state's whole point** and the only test that distinguishes
   `*bool` from `bool`. **Fails today:** compile error; and against a `bool`
   implementation the two rows are indistinguishable, so it fails there too.
4. `TestRoleAuthorizerDeniesDenyListPredicateOverAbsentVariable` — the phase-1 table
   through `Authorize`. **Fails today:** measured — all return `err=<nil>`.
5. `TestCheckSpecStated` — table mirroring test 1, asserting the gate is decidable
   **without** an `Authorizer`.
6. `TestActorRoundTripsThroughContext` — including `Attributes`, and that
   `ActorFromContext(context.Background())` returns `(Actor{}, false)`.
7. Testable example `ExampleContextWithActor` — public root-package API a consumer
   wires by hand; Golang rule #6 requires it.

**Verify:** `go test -race -count=1 ./authz/...`

---

### Phase 3 — `definition/activity`, then `definition/model`  *(moved BEFORE `engine`)*

Two packages, one agent, **serial inside** (model depends on activity). Purely
additive to `engine`, which is why it now precedes it.

- `activity.WithOpenEligibility()`; `UserTask.OpenEligibility bool` (the **authoring**
  form stays a plain bool — `nil` is a migration state, never an authorable one);
  wire key `open` in `node_wire.go` **and** `yaml.go`.
- `model.Validate` **rejects** a `UserTask` with neither `open` nor any eligibility
  dimension.

⚠ **The draft prescribed a fourth item — a non-fatal `model.Validate` diagnostic for
unguarded deny-list predicates — and it is DROPPED.** ADR-0185 D4 moves that check to
**task creation** (phase 4), where the variable snapshot it must check against
actually exists. `model.Validate` cannot see process variables, so the authoring-time
version could only guess. The draft's own test for it was flagged `vacuity-risk`
because no warning channel on `model.Validate` was ever verified to exist; none is
added.

**Tests, and what makes each fail today:**

1. `TestValidateRejectsUserTaskWithNoEligibilityAndNoOpenMarker`.
   **Fails today:** `NewUserTask("t1")` with no options validates cleanly —
   ADR-0117 Decision 1 made that the supported shape and
   `definition/activity/options.go:221` documents it.
2. `TestValidateAcceptsExplicitOpenEligibility`.
   **Fails today:** `WithOpenEligibility` does not exist → compile error.
3. `TestOpenEligibilityRoundTripsThroughYAMLAndJSON` — `open: true` survives both
   wire forms. ⚠ ADR-0167 made decoding **strict**, so the tag must be added to
   *both* `node_wire.go` and `yaml.go` or the round-trip fails asymmetrically.

**Fixture fallout in this package:** migrate `definition`'s own no-eligibility
`NewUserTask` fixtures.

**Verify:** `go test -race -count=1 ./definition/...`

---

### Phase 4 — `engine`: `Open` wiring, the mint-time reference check, the claimant guard  ⚠ CONTROLLER, INLINE

**4a — wire `Open` into the minted spec.** `engine/step_nodes.go:723`'s
`authz.AuthzSpec{...}` construction gains `Open:` from `ut.OpenEligibility`
(pointer-valued: an authored `true` becomes `ptr(true)`, an authored absence becomes
`ptr(false)` — ⚠ **never `nil`**, which is reserved for pre-upgrade rows).

**4b — the mint-time reference check (ADR-0185 D4).** Minting a task whose
`Attribute` predicate references a key absent from the task's creation variable
snapshot fails there, with a node-scoped message.

**Test:** `TestAwaitHumanRejectsPredicateOverVariableAbsentAtCreation` — a `UserTask`
with `eligible_expr: vars.approvedBy != actor.ID` and a creation snapshot lacking
`approvedBy`.
**Fails today:** no such check exists; the task mints and the predicate then allows
at authorization (executed: absent ⇒ nil ⇒ allow).
⚠ **Control, or the check over-applies:** a second case whose referenced key **is**
present in the snapshot must still mint. Without it an implementer who rejects every
predicate breaks every ABAC task and the suite stays green.

**4c — a claimed task may only be completed by its claimant (ADR-0185 D5).**
`handleHumanCompleted` compares `t.Actor.ID` to `task.Claim.Actor.ID`. New sentinel
`engine.ErrNotClaimant` (`workflow-engine:` prefix).

⚠ **Cite the symbol, not the line.** The draft said `:839`; it is `:849` at this
bundle's commit, and `:839` lands inside `applyOutcomeExposure` — a *different*
function. Navigate with
`awk '/^func handleHumanCompleted/,/^}/' engine/step_triggers.go`.

**Test:** `TestHumanCompletedRejectsNonClaimant`, a table with **four** cases:
- claimed by `alice`, completed by `mallory` ⇒ `errors.Is(err, ErrNotClaimant)`;
- claimed by `alice`, completed by `alice` ⇒ succeeds;
- **unclaimed**, completed by `alice` ⇒ succeeds (the control that decides the ADR —
  without it, every direct-completion and role-only-eligible task breaks);
- ⚠ **reassigned to `mallory` by `mallory`, then completed by `mallory`** ⇒ the
  two-hop bypass. Under ADR-0185 D5 the *reassign* is what must be refused (phase 5);
  this case pins that the completion guard alone does **not** stop it, so nobody
  later mistakes the completion check for the whole fix.

**Fails today:** re-derived over the whole function body,
`grep -c "Candidates\|Eligibility\|Claim"` → **zero hits**, so the mallory case
returns a successful `StepResult` with `Completion.Actor == mallory`.
⚠ **Fixture check, not line check:** the first two cases' task must actually carry a
non-nil `Claim`. `require.NotNil(t, task.Claim)` before acting, or the rejection
assertion is unreachable and the test is vacuous.
⚠ Re-read **ADR-0183** (the claim invariant enforced on write) before touching this
handler — the new guard must not contradict it.

**4d — fixture fallout.** `engine`'s no-eligibility `UserTask` fixtures. ⚠ Most
`engine` tests build `&model.ProcessDefinition{...}` **struct literals**, which never
reach `model.Validate` — so they break on the **runtime** rule (phase 2), not the
authoring gate. Expect the fallout here to be larger than `definition`'s.

**Verify:** `go test -race -count=1 ./engine/...` then `go vet ./...` (which compiles
the Docker-only test packages).

---

### Phase 5 — `runtime` + `runtime/task`: the hoisted gate, the reassign privilege, the input bound

⚠ **This package had NO phase in the draft**, and it owns three of the delivery's
load-bearing pieces.

- **Hoist the spec-shape gate:** call `authz.CheckSpecStated(task.Eligibility)`
  **before** `s.authz.Authorize(...)` at all **four** sites
  (`runtime/task/service.go:199` Claim, `:234` Reassign, `:255` Complete, `:306`
  RefreshCandidates). This is what makes ADR-0185's "closed end to end" true for a
  consumer-supplied `Authorizer` as well as for both in-repo ones.
- **`Reassign` requires a privilege when `by` is not the current claimant**
  (ADR-0185 D5). Self-reassignment by the claimant stays on the eligibility check.
- **`runtime.WithMaxEvalElements(n int)`** — constructs
  `expreval.New(expreval.WithTimeout(0), expreval.WithMaxEnvElements(n))` and assigns
  `driver.conditionEval`, reaching the engine through `StepOptions.Evaluator`
  (ADR-0056). Default **10 000**. This is ADR-0186 D2's plumbing; without it the
  bound is a zombie knob.
  ⚠ `runtime.WithConditionEvaluator` and `WithExpressionTimeout`
  (`runtime/processdriver_options.go:198,217`) keep their signatures — D2's ctx is
  dropped, so **`runtime` has no breaking change** in this bundle.

**Tests, and what makes each fail today:**

1. `TestClaimDeniesUnstatedSpecEvenWithAllowAllAuthorizer` — the gate's whole point.
   **Fails today:** `AllowAll` returns nil and nothing precedes it.
   ⚠ **This is the test that distinguishes hoisting from fixing `RoleAuthorizer`.**
   With `authz.AllowAll{}` wired, a `RoleAuthorizer`-only fix cannot fail it.
2. `TestReassignToSelfByNonClaimantRequiresPrivilege` — mallory, eligible but not the
   claimant, reassigns alice's task to herself.
   **Fails today:** `Reassign` authorizes `by` against `task.Eligibility` only — the
   same check as `Claim`, by the repo's own godoc at `runtime/task/service.go:206-217`
   — so it succeeds, and the two-hop completion bypass follows.
3. `TestClaimantMayReassignTheirOwnTask` — the control. Without it an implementer who
   requires the privilege unconditionally breaks ordinary hand-off and the suite
   stays green.
4. `TestWithMaxEvalElementsBoundsTheDriverEvaluator`.
   **Fails today:** the option does not exist → compile error.

**Fixture fallout:** `runtime`'s no-eligibility `UserTask` fixtures (including
`runtime/manual_task_test.go`, one of the 5 sites that reach `model.Validate`).

**Verify:** `go test -race -count=1 ./runtime/task/... ./runtime` — ⚠ `./runtime/...`
as a whole is **not** container-free; scope it or state the Docker requirement.

---

### Phase 6 — `internal/persistence/store`: the `Open` backfill migration  ⚠ DOCKER REQUIRED

⚠ **The draft had no persistence phase at all.** This is the phase that stops the
upgrade stranding live work.

- A migration that rewrites in-flight open human-task snapshots whose `Eligibility`
  carries no dimension and no `Open`, setting `Open: true`, so the grandfathered
  `nil` population is bounded and observable rather than permanent.
- It must run on **all three dialects** (Postgres, MySQL, SQLite) and be
  **idempotent** — an interrupted run must be safe to re-run.

**Tests, and what makes each fail today:**

1. `TestOpenBackfillGrandfathersPreUpgradeTasks` — write a snapshot with the
   pre-upgrade `AuthzSpec` shape, run the migration, assert `Open == ptr(true)`.
   **Fails today:** the migration does not exist.
2. `TestOpenBackfillIsIdempotent` — running it twice is a no-op.
3. `TestOpenBackfillLeavesStatedSpecsAlone` — the control: a task with
   `Roles: ["manager"]` or `Open: ptr(false)` is untouched. Without it a backfill
   that opens *every* task ships and the other two tests still pass.

**Verify:** `go test -race -count=1 ./internal/persistence/store/...` (Docker; the
SQLite leg is pure Go via `dbtest.RunTestSQLite`)

---

### Phase 7 — `service`: an authorizer must be chosen

- `WithAuthorizer(az authz.Authorizer) Option`; `WithAllowAllAuthorizer() Option`.
- `NewProcessEngine` errors when human tasks are configured and neither was supplied.
- Allow-all is logged at **WARN as a SEPARATE record**. ⚠ The level is at
  `service/service.go:323`, **not** `:315-317` (which computes the *label*), and that
  one `LogAttrs` call carries four unrelated attributes — promoting it would move the
  whole construction summary to WARN. Leave the summary at DEBUG; emit a new line.
- `AuthorizerProvider interface { Authorizer() authz.Authorizer }` — an **optional
  capability interface** type-asserted on the `DurableProvider`, per ADR-0081's
  `Notifier`/`Locker` precedent. Do **not** add the method to `DurableProvider`.
- `WithDurableStore` applies **`taskStore` only** as a default for a leaf the
  consumer set explicitly. ⚠ **Do not generalise this to all six leaves** — the
  last-writer-wins precedence is documented at `service/options.go:157-160` and
  changing it would break `WithInstanceStore`-before-`WithDurableStore` for existing
  consumers, a fifth breaking change this bundle is not making.
- `WithMaxVariableBytes(n int64) Option`, default 256 KiB, refused before persist
  with a sentinel. ⚠ Document it as a **payload/storage** bound; ADR-0186 D2 states
  the CPU bound is `WithMaxEvalElements`, and the draft's framing of 256 KiB as the
  CPU mitigation is refuted by its own O(n²) table.

**Tests, and what makes each fail today:**

1. `TestNewProcessEngineRequiresAnAuthorizer` — `NewProcessEngine(WithDurableStore(p))`
   returns an error.
   **Fails today:** it returns a working engine with `authz.AllowAll{}`
   (`service/service.go:199-200`).
   ⚠ Assert on the **returned error**, not on log text.
2. `TestWithAllowAllAuthorizerLogsAtWarn` — capture a `slog` handler, assert a record
   at `slog.LevelWarn` exists **and** that the construction summary is still at
   DEBUG. **Fails today:** there is exactly one record and it is DEBUG.
   ⚠ Assert on the **level**, not the message string.
3. `TestDurableStoreDoesNotOverrideAnExplicitTaskStore` — ⚠ **renamed from the
   draft's `TestDurableStoreOptionOrderIsIrrelevant`, whose fixture could not fail.**
   `WithDurableStore` never writes `c.authz`, so with the draft's `WithHumanTasks(nil, az)`
   both orders **already agree today** and the prescribed test was green from the
   start. The fixture must pass a **non-nil** task store:
   `WithHumanTasks(myStore, az)` then `WithDurableStore(p)` must keep `myStore`.
   **Fails today:** `options.go:169-181` overwrites `c.taskStore` unconditionally.
4. `TestWithMaxVariableBytesRefusesOversizedVariables`.
   **Fails today:** the option does not exist → compile error.

**Fixture fallout:** `service`'s no-eligibility `UserTask` fixtures.

**Verify:** `go test -race -count=1 ./service/...` (container-free)

---

### Phase 8 — `action/httpcall`: SSRF posture  *(parallel with phase 7)*

- Route `WithURLExpr` through `internal/expreval`.
- Restricted transport for **expression-derived** URLs only: `net.Dialer.Control`
  refusing loopback / link-local / RFC1918 / ULA / metadata; `CheckRedirect` refusing
  a host outside the allowlist.
- `WithAllowedHosts([]string)`, `WithUnrestrictedTransport()`. `WithBaseURL`
  unchanged.

**Tests, and what makes each fail today:**

1. `TestURLExprRefusesLinkLocalAddress` — `vars.url = "http://169.254.169.254/latest/meta-data/"`
   returns a non-retryable error.
   **Fails today:** `grep -rnE "CheckRedirect|expreval" action/httpcall/` → 0, so the
   request is attempted.
   ⚠ **Do not dial a real link-local address in CI.** Assert on the dialer control's
   refusal, not a network result.
2. `TestURLExprRefusesRedirectToLoopback` — an `httptest` server that 302s to
   `127.0.0.1`. **Fails today:** `http.Client` follows by default.
3. `TestBaseURLIsUnrestricted` — ⚠ **the ADR's load-bearing control.** A static
   `WithBaseURL` pointing at the `httptest` loopback server still works. Without it,
   an implementer who over-applies the restriction breaks every existing user and the
   suite stays green.
4. `TestURLExprUsesTheBoundedEvaluator`.
   **Fails today:** `httpcall.go:127` calls raw `expr.Compile`.
   ⚠ If the evaluator in use is not observable without new public API, mark this
   **vacuity-risk** and rely on 1–3.

**Verify:** `go test -race -count=1 ./action/httpcall/...`

---

### Phase 9 — `transport/http/httpcore`: identity, size, redaction, disclosure

- Read the actor from `authz.ActorFromContext` at `endpoints.go:119,132,150`; add
  **`WithRequestActor`** — ⚠ **not `WithActorResolver`**, which is already exported by
  `service`, `runtime/task` and `processtest` meaning *candidate expansion*.
- **401** when no actor resolved; `WithAnonymousActorAllowed()` opt-in; **503** on a
  resolver error — ⚠ never a downgrade to the zero actor.
- **Remove** `Actor` from `ClaimInput`/`CompleteInput` and `By` from `ReassignInput`
  (`dto.go:44,50,66`).
- `CustomizeConfig.MaxBodyBytes` (default 1 MiB); `ErrBodyTooLarge` sentinel;
  `ClassifyError` maps it → **413**.
- `RedactVariables` applied in **`mapInstance`, before the mapper** — ⚠ not inside
  `NewInstanceView`, which `CustomizeConfig.InstanceMapper` bypasses wholesale.
- `view.go:31` copies rather than aliases.
- `ClassifyError`: static 403; value-free 400 rendering; correlation id on every body
  (OTel span id when recording, else a random hex id).
- Correct `CustomizeConfig.Logger`'s godoc — it says *"receives 5xx raw error
  details"* and now also receives 400/401/403's.

**Tests, and what makes each fail today:**

1. `TestClaimUsesContextActorNotBody` — put `bob/viewer` in the request context, send
   a body claiming `{"id":"alice","roles":["manager"]}`, assert **denied**.
   **Fails today:** `endpoints_test.go:405,422` pin the opposite. That inversion *is*
   the escalation.
2. `TestNoActorInContextReturns401` and `TestActorResolverErrorReturns503`.
   ⚠ For the 503 case, assert **both** that the status is not 2xx **and** that
   `svc.ClaimTask` was never called (a stub recording calls) — without the second
   assertion the test passes against an implementation that refuses *after* acting.
   **Fails today:** neither arm exists in `ClassifyError`.
3. `TestAnonymousActorAllowedOptsBackIn` — the control for test 2.
4. `TestActorAttributesReachTheAuthorizer`.
   **Fails today:** all three sites project only `{ID, Roles}`.
5. `TestClassifyErrorDoesNotEchoPredicateSource` — build the real 403 from
   `authz.RoleAuthorizer{}.Authorize` with an erroring predicate, assert
   `body.Message` does **not** contain `"internalApprovalLimit"`.
   ⚠ **Mandatory control:** `require.Error(t, err)` **and**
   `require.Contains(t, err.Error(), "internalApprovalLimit")` *before* classifying.
   Both the deny path and the eval-error path produce 403 and only the latter leaks;
   without this control a predicate that quietly evaluates `false` makes the
   assertion pass vacuously.
6. `TestValidationErrorDoesNotEchoSubmittedValue` — post `{"ssn":"123-45-6789"}`
   against a `pattern`-constrained schema; assert the 400 body contains `"ssn"` and
   `"pattern"` and **not** `"123-45-6789"`.
   **Fails today:** executed — `- at '/ssn': '123-45-6789' does not match pattern
   '^[0-9]{3}$'` is copied verbatim into `ErrorBody.Message`.
   ⚠ **This replaces the draft's second control**, which asserted 400/409 messages
   are "still present" and would have **pinned the leak in**. Assert on the error
   *code* and on the value's absence, never on message presence.
   ⚠ Add a `maxLength` row too — its leaf discloses `got 11, want 3`, so a
   `pattern`-only fix passes a `pattern`-only test.
7. `TestInstanceViewCopiesVariables` — mutate the returned `view.Variables`, assert
   the source `InstanceState.Variables` is unchanged.
   **Fails today:** `view.go:31` aliases the map.
   ⚠ This is a unit test on `NewInstanceView` and is sound regardless of ADR-0186
   Context §4's withdrawn "mutates instance state" claim. Do not restate that claim
   in the test's comment.
8. `TestRedactionAppliesUnderCustomInstanceMapper` — ⚠ **the control D4 was missing.**
   Set a custom `InstanceMapper` **and** `RedactVariables`; assert the mapper never
   sees the redacted key. **Fails today:** `RedactVariables` does not exist; and
   against the draft's placement (inside `NewInstanceView`) this test fails while a
   default-mapper test passes.
9. `TestMaxBodyBytesRejectsOversizedBody` — one byte over the cap ⇒ **413**.
   **Fails today:** the field does not exist → compile error. ⚠ The *behavioural*
   claim that stdlib/gin currently return 201 for a 256 MiB body is
   `ASSUMPTION (unverified)` — inherited, never re-derived. The compile error is the
   real RED; do not claim the 201 was observed.

**Verify:** `go test -race -count=1 ./transport/http/httpcore/...` (container-free)

---

### Phase 10 — `casbinauthz` + `internal/authz/casbin`  ⚠ DOCKER REQUIRED

*(may run concurrently with phases 9 and 11)*

⚠ **The agent's brief must state that this phase needs a running Docker daemon**
(`internal/authz/casbin` uses pgxpool + LISTEN/NOTIFY). The standing Docker carve-out
is scoped to the Verification runs and does **not** extend to subagents.

**10a — the strict evaluator.** ⚠ **The draft never touched this file, while
ADR-0185 D3 told consumers to wire it and CLAUDE.md makes casbin the baseline.**
`internal/authz/casbin/authorizer.go:30` builds its own `expreval.New()`; it becomes
`expreval.New(expreval.WithStrictReferences())`. Its *"An empty spec allows"* godoc
(`:33`) is corrected, and the hoisted `CheckSpecStated` gate (phase 5) covers it
regardless.

**10b — stale policy (ADR-0185 D6).** Track `lastSuccessfulLoad` alongside the
existing failure counter (`internal/authz/casbin/db.go:76-99`); `casbinauthz` exposes
a `HealthCheck` reporting staleness; `WithStalePolicyBudget(d)` makes `Enforce` deny
past `d`. **Defaults: health check enabled, deny-budget disabled.**

**Tests, and what makes each fail today:**

1. `TestCasbinAuthorizerDeniesDenyListPredicateOverAbsentVariable` — mirrors phase 2
   test 4 against `internal/authz/casbin`. **Fails today:** its `attrEval` is a plain
   `expreval.New()`, so the predicate allows. ⚠ **Container-free** — `Authorize`
   needs only a `SyncedEnforcer` built from strings.
2. `TestStalePolicyHealthCheckReportsAfterFailedReload`.
   **Fails today:** no staleness state exists; the callback logs and counts, then
   returns (`db.go:87-98`). Compile error is the RED.
3. `TestEnforceDeniesPastStalePolicyBudget`.
   **Fails today:** `Enforce` answers from the last good policy indefinitely, so a
   revoked permission returns `true, err=nil` — the parked decision.
4. `TestStalePolicyBudgetDisabledByDefault` — the control. Without it, an implementer
   who defaults the budget *on* turns one transient DB blip into a full outage and
   the suite stays green.

**Verify:** `go test -race -count=1 ./casbinauthz/... ./internal/authz/casbin/...`

---

### Phase 11 — `processtest`  *(parallel with 9, 10)*

Fixture fallout only: `processtest`'s no-eligibility `UserTask` fixtures, plus any
harness default that mints one. ⚠ `processtest.WithActorResolver` exists and means
candidate expansion — do not rename or shadow it.

**Verify:** `go test -race -count=1 ./processtest/...` (container-free)

---

### Phase 12 — `transport/http/stdlib` | `gin` | `fiber`  ⚠ THREE PARALLEL AGENTS

One agent per package. **Never two agents in one of them.**

⚠ **The actor half of the draft's adapter phase is DELETED.** The request context
already reaches `httpcore` unmodified in all three adapters, so resolution happens
once in `httpcore` (phase 9). Duplicating it here would triple the resolver
invocation and the error classification. Each agent therefore does **two** things:

- **Body cap** at all **13** decode sites in that package (`groups.go`):
  - `stdlib` — `http.MaxBytesReader` before `json.NewDecoder`;
  - `gin` — assign `gc.Request.Body = http.MaxBytesReader(...)` **before**
    `ShouldBindJSON`;
  - `fiber` — a `len(c.Body())` pre-check before `c.Bind().JSON`. ⚠ `BodyLimit` is a
    `fiber.Config` field on `fiber.New`, which a mounted route group does not own.
    **This mechanism is `ASSUMPTION (unverified)`** — the fiber agent's first task is
    to establish it by execution and report back **before** editing 13 sites.
  - Each converts its oversize signal to `httpcore.ErrBodyTooLarge` before
    classifying (`errors.As(*http.MaxBytesError)` for stdlib/gin).
- **Migrate that package's pinned tests.** ⚠ Re-derived counts — the draft's were
  low because its grep missed `"by"`:

  | package | draft said | actual |
  |---|---|---|
  | `stdlib` | 3 across 3 files | **5** across 3 files |
  | `gin` | 5 across 2 files | **7** across 2 files |
  | `fiber` | 4 in 1 file | **5** in 1 file |

  ⚠ **`stdlib/errors_test.go:187` and `gin_coverage_test.go:244` assert 403.** After
  ADR-0185 D1 they still return 403 — from the zero actor — so they pass while
  testing nothing. **Rewrite them; do not merely recompile.**

**Test per package:** `TestBodyActorIsIgnored` — post a body carrying
`"actor":{"id":"alice","roles":["manager"]}` with `bob/viewer` in the request
context, assert denied. **Fails today:** the body actor is the only actor
(`stdlib_test.go:471`, `gin_test.go:413`, `fiber_test.go:563` pin exactly this).

⚠ **Fiber additionally documents `c.SetContext`** in its package godoc: `c.Locals`
does **not** propagate into the context `httpcore` receives, so a consumer following
fiber's most idiomatic middleware path is silently unauthenticated.

**Verify (per agent):** `go test -race -count=1 ./transport/http/<pkg>/...`

---

### Phase 13 — `transport/http/parity`

Migrate `parity_test.go:497` and add parity cases asserting all three adapters agree
on: the context actor winning, **401** with no actor, **413** on oversize, and the
blanked 403. **Fails today:** the parity suite posts an actor body and expects it
honoured.

**Verify:** `go test -race -count=1 ./transport/http/...`

---

### Phase 14 — `examples/`

⚠ **Re-derived, because the draft's number was wrong twice.**
`grep -rln "runtime\.WithHumanTasks" examples/` → **16** files: **12** under
`scenarios/` (the draft said 13) and **4** `*_wiring` mains (`cache_wiring`,
`mysql_wiring`, `production_wiring`, `sqlite_wiring`) which the draft's
`scenarios/`-scoped sentence excluded but which carry `UserTask`s all the same.

**Three of them mount the task routes** — `production_wiring/main.go:264`,
`sqlite_wiring/main.go:278`, `mysql_wiring/main.go:262`, each via `stdlib.Mount`,
which registers `TaskRoutes` (`stdlib/mount.go:17-21`). All three must gain a
demonstration authentication middleware populating `authz.ContextWithActor`, or
`WithAnonymousActorAllowed()` with a comment saying why an example may do that and a
deployment may not.

Scenario mains declaring `UserTask`s with no eligibility dimension need
`WithOpenEligibility()` or a real dimension. **Enumerate mechanically** with
`grep -rLn "WithEligible" $(grep -rln "NewUserTask" examples/scenarios/)` — do not
guess, and do not reuse the draft's list.

⚠ **vacuity-risk:** `examples/` has no tests. The falsifier is
`go build ./examples/...` plus running each scenario main. Do not prescribe an
assertion here.

**Verify:** `go build ./examples/... && go vet ./examples/...`

---

### Phase 15 — documents (controller)

- `SECURITY.md`: the at-rest posture (ADR-0186 D6) naming
  `wrkflw_instances.snapshot` and `wrkflw_journal.trigger`; the stale-policy health
  check the consumer must wire; **the fiber `c.SetContext` idiom**; the amended
  "Authorization" bullet.
- `STABILITY.md` + `CHANGELOG.md`: the breaking changes — the three task DTOs,
  `NewProcessEngine`'s new error, the `AuthzSpec` meaning change, and ⚠ **`ErrorBody`**
  (message content for 400/403, plus the new correlation-id field), which the draft
  omitted. ⚠ **`ConditionEvaluator` is NOT in this list any more** — ADR-0186 D2's
  ctx is dropped, so its signature and `runtime`'s two options are unchanged.
  Each entry gets a copy-pasteable migration diff, including the `Open` migration.
- **`docs/adr/0117-optional-usertask-eligibility.md`**: annotate **Decisions 1 AND 3**
  in place. ⚠ The draft named only Decision 1; Decision 3's *"any combination
  (including none) is valid"* is reversed too, and leaving it unannotated leaves live
  ADR text asserting the proposition ADR-0185 overturns.
- ⚠ **Both** godocs stating the open default as fact —
  `definition/activity/activity.go:159` (on `NewUserTask`, the one every consumer
  reads) and `definition/activity/options.go:221`. `grep -rn "engine gate is open"
  --include='*.go' .` finds exactly these two; the draft named one.
- ⚠ `authz/authz.go:34`'s godoc links `[ActorResolver]` from package `authz`, where
  no such symbol exists — a broken link today, and newly ambiguous once
  `ContextWithActor` lands. Fix it to name `humantask.ActorResolver`.
- `docs/plans/HANDOVER.md` + this plan's `▶ Progress`, per rule #10.
- Close backlog **51, 52, 53, 54, 65, 98, 99, 103, 104, 124** and the parked half of
  **102**. ⚠ **Strike 53 from `HANDOVER.md`'s B3 item list and remove the blocker-1
  tail cross-reference — do NOT delete the B3 line itself**, which enumerates the
  other eleven items.
- **Do not** close 90, 100, 101, 106, 62, 32.

---

## 4. Migration and deployment (do not skip)

⚠ **The draft gated the wrong direction.** Its "Deployment-order gate" covered an
**older** binary reading a **newer** row — which needs a mixed-version deployment,
already out of contract here — and said nothing about the forward upgrade, which
always happens.

**The forward upgrade is handled by design, not by an operator instruction.**
`AuthzSpec.Open` is `*bool`; the snapshot is `json.Marshal`ed with no tags and read
with a plain `json.Unmarshal` (no `DisallowUnknownFields`). Executed: a pre-upgrade
row decodes to `Open == nil`, distinguishable from an explicit `false`, and
`nil`/`true`/`false` all round-trip. `nil` ⇒ **grandfathered open**, so no in-flight
task is stranded.

Order of operations for a deployment:

1. Deploy the new binary. In-flight tasks carry `Open == nil` and keep working.
2. Run phase 6's backfill migration; the `nil` population goes to zero.
3. Re-author definitions that relied on the open default (`open: true` or a real
   dimension). New tasks then mint with a stated `Open`.

`nil` is never authorable — `model.Validate` (phase 3) rejects it and the engine
(phase 4a) mints `ptr(true)`/`ptr(false)`, never `nil`. So the grandfathered
population can only shrink.

⚠ The reverse direction (old binary, new row) silently drops `Open` — executed,
`err=<nil>`. Release notes must still say **do not run mixed versions**, which is
this repo's existing position, not a new constraint.

---

## 5. Enumerations, re-derived at this bundle's commit

Every number below was re-derived for the revision, not inherited. Raw commands and
outputs: `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md` §7.

| what | draft said | actual |
|---|---|---|
| body-actor test pins | 23 / 9 files / 5 pkgs | **29** — httpcore 11, gin 7, fiber 5, stdlib 5, parity 1 |
| `handleHumanCompleted` | `:839`, write `:931-936` | **`:849`**, write **`:941`**; `:839` is inside `applyOutcomeExposure` |
| …claim comparisons in its body | "one hit, a comment" | **zero** over the true body |
| allow-all log level | `service.go:315-317` | level **`:323`**; `:315-317` is the label |
| `expreval.New(` instances | 2 (the "structural" argument) | **4** |
| ABAC evaluation sites | 1 (`authz`) | **2** (+ `internal/authz/casbin`) |
| "engine gate is open" godocs | 1 | **2** |
| ADR-0117 decisions amended | 1 | **2** |
| `NewUserTask` sites / no eligibility / reach `Validate` | uncounted, "every definition" | **274 / 128 / 5** |
| `examples/scenarios` mains | 13 | **12** (+4 `*_wiring`) |
| predicate length (probe 7) | 44 chars | **80** |
| deny-list class | "five predicate forms wide" | **unbounded**; five were sampled |
| spec §4.7 per-class policy | omits 413 | 401, 413 and 503 all added |

---

## 6. Verification checklist

- [ ] **Rule-#9 RE-audit** run over the whole revised bundle, findings adjudicated
      and folded. **Nothing below starts until this is checked.** A bundle whose
      Decisions changed has not been audited.
- [ ] Every phase's tests observed **RED before GREEN**, in the transcript.
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out`
      ≥ 85 % over hand-written code — hot paths and their failure branches first
      (Golang rule #8). Probe `docker info` first; if the daemon is down, say so and
      label any container-free subset as partial.
- [ ] `go test ./...` from the repo root — no regressions.
- [ ] `golangci-lint run ./...` (repo-wide, not package-scoped) clean; if the binary
      is absent, say so and offer install-or-skip.
- [ ] `go vet ./...` — compiles the Docker-only test packages.
- [ ] `go build ./examples/...`
- [ ] The migration runs green on **all three dialects**.
- [ ] Documents describe what shipped: re-read the spec, both ADRs and this plan
      against the built code and correct every divergence. Per rule #11, expect
      implementation to have corrected the design — **amend the ADR in the same
      bundle**, with the measurement that refuted it.
- [ ] Sweep the diff's comments for unexecuted claims and over-reaching quantifiers
      (Premise Discipline).
- [ ] `/code-review` — all findings fixed, folded via `--amend`.
- [ ] `/security-review` — all findings fixed, folded via `--amend`.
- [ ] `HANDOVER.md` rewritten in place; this plan's `▶ Progress` updated; auto-memory
      topic file written and pointing at `HANDOVER.md`.

## 7. Commit shape

One feature bundle, one commit, amended (never stacked):

```
feat(authz,transport,engine): the authorization principal is not self-asserted
```

carrying implementation, tests, the spec, both ADRs, this plan, the evidence records
and the doc updates from phase 15.
