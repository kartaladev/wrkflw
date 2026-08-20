# B3 authz/security bundle — rule-#9 RE-AUDIT adjudication

**Date:** 2026-08-21 · **Bundle audited:** `dd76a17b` (the *revised* bundle: spec + ADR-0185 +
ADR-0186 + plan + `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`)
**Lenses:** execution · failure-modes · counting — three Opus agents, three detached worktrees at
the bundle commit, step-0 presence check passed in all three.
**Reports:** `reaudit-b3-{execution,failure-modes,counting}.md` beside this file (2,631 lines).

## ⛔ VERDICT: THE REVISED BUNDLE FAILS ITS RE-AUDIT. Still not an input to implementation.

**38 findings across three lenses; ~13 distinct Criticals** after de-duplication (16 raw, with
three pairs found independently by two lenses each).

This is the **second** failed audit for this bundle. The shape of the failure changed, and that
change is the most useful thing the re-audit produced:

- The 2026-08-20 draft failed on **individual decisions** being wrong.
- The 2026-08-21 revision fixed those, and **failed on the interactions between the decisions it
  rewrote**. Five of the failure-modes lens's nine Criticals are holes the revision's *own* fixes
  opened in each other. Four Decisions changed and their pairwise consequences were never
  re-derived.

⚠ **Two Criticals were found independently by two lenses each** (the two persistence locations;
the 400 rendering being unimplementable where prescribed). That convergence is the strongest
signal a design audit produces, and both are confirmed by the controller's own execution.

## Accepted Criticals

Grouped by the decision they falsify. **All accepted; none rejected.**

### A. ADR-0185 D4 — strict references: the mechanism is the wrong SHAPE (4 Criticals)

The revision rewrote D4's escape hatch after the draft's `has(vars,"k")` turned out not to exist.
The rewrite is also unsound, in four independent ways, all executed:

1. **The dominance rule still admits deny-list predicates** (exec E2, controller-confirmed). On an
   empty `vars`: `not ("tier" in vars) and vars.tier != "blocked"` → **true**;
   `("tier" in vars) == false and vars.tier != "x"` → **true**;
   `"tier" in vars ? true : vars.tier != "blocked"` → **true** — and the last matches D4's own
   wording ("the condition of a ternary") word for word. The bundle's three-row "falsifying table"
   does not falsify, and plan phase-1 test 3 — billed as *"the control that decides D4"* — cannot
   catch any of them.
2. **The dominance rule also DENIES a correct predicate** (fail F10). `and` is left-associative, so
   in `"tier" in vars and vars.active and vars.tier == "gold"` the enclosing `and`'s left operand is
   a `BinaryNode`, not the guard. The rule as worded rejects a legitimate policy. The prescribed
   table is entirely two-operand and structurally cannot see it.
3. **The zero-reference rule is disarmed by any single ordinary reference** (exec E4,
   controller-confirmed). `vars.region == "eu" or get(vars,"blocked") != true` extracts one
   reference, satisfies the rule, and evaluates **true** with `blocked` absent. The rule only fires
   when the *whole* predicate extracts nothing.
4. **The `actor` axis gets ZERO protection** (exec E3, controller-confirmed). `actor` is a
   **struct**; `Attributes` is a field that always exists, so depth-1 extraction always reports it
   present. `actor.Attributes.clearance != 5` and `actor.Attributes.tenant != "acme"` both evaluate
   **true** against a nil map. ⚠ **ADR-0185 D1's "closes finding 4's second leg for free" is
   FALSE.** Worse, the evidence file's own example `actor.attributes.clearance > 3` is a *run-time
   error* that denies everyone — the `has()` failure reproduced inside the fix's own evidence.

**Root cause, and why this is not a wording fix.** Three rounds have produced three disjoint sets of
holes. Inferring *"did the author guard this key?"* by syntactic analysis of an unbounded expression
language cannot be made sound — every round finds more shapes. **D4 must change shape**, not
wording. See "Open decision" below.

Additionally: **D4's runtime rule re-introduces the upgrade-stranding that D3 exists to fix**
(fail F11), through `Attribute` instead of `Open`: a pre-upgrade task whose predicate references a
key absent from its frozen snapshot becomes unclaimable, uncompletable, unreassignable **and**
unrefreshable, forever, with no migration and no repair verb.

### B. ADR-0185 D5 — the claimant guard: blocked by a missing model (2 Criticals)

5. **The `reassign` privilege bricks all four verbs** (fail F2, exec E1, controller-confirmed).
   There is **one** `Eligibility` spec, and `internal/authz/casbin/authorizer.go:56-64` applies
   `Privileges` unconditionally for every verb. A `reassign` token in the spec is therefore also
   required to Claim, Complete and RefreshCandidates. The guard refuses the useful case — the
   ADR-0165 failure mode the audit brief exists to catch.
6. **The hoisted `CheckSpecStated` is authorizer-blind but enforces an authorizer-dependent rule**
   (fail F1, exec E1). Hoisted above all four `Authorize` sites it denies every `Privileges`-carrying
   spec **including under casbin**, emptying the very escape hatch D3 names (*"a consumer who wants
   privileges evaluated wires the casbin authorizer"*). ADR-0185 contradicts itself: D3 says "under
   `RoleAuthorizer`", Consequences says "above all four `Authorize` calls". Plan phase-2 test 5 and
   phase-5 test 2 cannot both pass.

**Root cause:** there is no **per-verb** authorization model. Any requirement expressed in the single
shared spec applies to all four verbs. D5 needs a design increment the bundle never budgeted.

### C. ADR-0185 D3 — tri-state `Open`: right mechanism, wrong surface (2 Criticals)

7. **`AuthzSpec` is durable in TWO places, and the migration targets the wrong one** (fail F3,
   exec E5, controller-confirmed). The authorization path reads the task-store row —
   `TaskService.Claim` → `s.store.Get()` → `internal/persistence/store/humantask_store.go:157`
   (marshal) / `:398` (unmarshal), a dedicated `eligibility` column. The bundle evidences the
   tri-state against `wrkflw_instances.snapshot` / `store_core.go:81,:174`, which is the copy the
   four `Authorize` sites do **not** read. Phase 6's migration names no table and would touch zero
   relevant rows; a partial backfill makes the claim path and the completion path disagree.
   ⚠ The `*bool` → `nil` **mechanism** still holds (both locations use `encoding/json`); the
   citation, the migration target and the "two locations" fact are what is wrong.
8. **`Open *bool` makes the zero value of a PUBLIC struct fail-OPEN** (fail F4). `authz.AuthzSpec`
   is exported module-root API. `authz.AuthzSpec{}` written in ordinary Go — by a consumer, by a
   consumer-implemented `TaskStore`, by `MemTaskStore` — yields `Open == nil`, which D3 defines as
   *grandfathered open*. This refutes the ADR's *"nil is never authorable … the population can only
   shrink"*, and it is the exact fail-open the bundle exists to close.

### D. ADR-0186 D5 — the value-free 400 is not implementable where prescribed (2 Criticals)

9. **`errors.As` cannot reach the jsonschema error at `ClassifyError`** (fail F6, exec E8,
   controller-confirmed). `runtime/validation/gate.go:45` is
   `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` — it flattens the typed error to a
   **string**. The bundle's §6 probe called the vendor directly and never went through `Gate`. The
   leak is real; the fix must move into `gate.go` (or the gate must wrap with `%w`).
10. **The 400 arm is far wider than one strategy** (fail F7). It carries **9** sentinels, and
    `validation.ErrInvalidInput` wraps **4** strategies. The fix and its single prescribed test cover
    one. The `expr` strategy echoes the predicate source (`expr.go:64,68`) — the same disclosure the
    ADR fixes for 403 — inside the arm D5 declares fixed.

### E. Cross-cutting (2 Criticals)

11. **The hoisted gate does not close the chain** (fail F5). `ProcessDriver.ApplyTrigger`
    (`runtime/processdriver.go:556`) and `engine.NewHumanCompleted` (`engine/trigger.go:399`) are
    exported module-root API that reach task state without passing through `runtime/task`.
    ADR-0185's *"this is what makes it true"* is still an over-claim. ⚠ The lens enumerated
    **bypassers**, not callers — the lesson from ADR-0179.
12. **"Only 5 `NewUserTask` sites reach `model.Validate`" is derived from the wrong net**
    (count R-8, controller-confirmed). `grep NewUserTask(` covers **one of three authoring forms**.
    `definition/build.Builder.AddUserTask` (`build.go:117`, public API) and YAML `kind: userTask`
    (`activity.go:236`) are structurally invisible to it. Re-derived: **≥13** no-eligibility UserTask
    nodes in **6** files reach the gate, including `engine/step_signal_fanout_test.go` with six
    `require.NoError(t, model.Validate(def))` assertions. ⚠ This **falsifies ADR-0185's own
    sentence** *"Definitions built as `model.ProcessDefinition` struct literals — the dominant idiom
    in `engine`'s tests — are never validated"*.

## ⚠⚠ Three findings are in the controller's own evidence file

`docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md` was written specifically to stop
unexecuted claims entering the bundle. It contains three of its own:

- **The 274 / 128 / 5 triple was INHERITED VERBATIM from the previous audit** (count R-18) while §7
  is captioned *"re-derived here rather than inherited"*. `274` was re-run and matched, so the other
  two were never checked — and all three are wrong (273 / 121+6 / ≥13). **This is the `"by"`-grep
  failure repeating one round later, in the document written to prevent it.** Re-running a command
  is not re-deriving a claim when the command is the wrong net.
- **§2's `??` measurement is false as labelled** (count R-16). The probe ran with `vars` **empty**;
  §2 sits directly under §1's declared `vars = {"tier":"gold"}`, under which the recorded
  `(vars.tier ?? "none") == "gold" → out=false` would be `true`. Confirmed against the probe source.
- **§6's jsonschema probe measured the vendor, not the repo's wrapper** — finding 9 above.

⚠ The execution lens's meta-observation is the sharpest thing in the whole re-audit:
**a load-bearing claim evidenced against the vendor or a stand-in, where the decision acts on the
repo's wrapper one layer down.** It happened twice (`gate.go`, `store_core.go` vs
`humantask_store.go`), and it is exactly the habit the revision existed to stop.

## What HELD (do not re-litigate)

- ⭐ **The revision fixed BOTH structural defects the first audit found.** Every citation now
  resolves at the bundle's own commit, and the pin net is closed *by construction* (`dto.go` declares
  exactly three `Actor` fields) rather than by a grep. The anchor problem and the net problem, as
  the first audit framed them, are gone.
- ⭐ **All four of the revision's corrections AGAINST the previous audit are confirmed right**:
  `has()` does not exist; `??` does not parse unparenthesised; `get()` extracts zero references; and
  the audit's element bounds were wrong by ~15×. The counting lens adjudicated the last one
  formally: k = 1.563/8000² ⇒ 5 000 = 610 ms, 10 000 = 2.442 s (15.3× / 16.3×), and noted the
  previous audit contradicted its own formula 16 lines after using it correctly.
- `WithMaxEvalElements`'s plumbing is **real**, not a zombie — the ADR-0162 failure did not recur.
- The O(n²) ladder reproduces (25 / 99 / 393 ms, 1.57 s); the predicate is confirmed 80 bytes.
- ADR-0186 D2's benchmark reproduces exactly (97.62 → 976.7 ns/op, allocs 3 → 9).
- The `ASSUMPTION (unverified)` on ctx propagation is **TRUE in all four legs**, including
  `c.Locals` returning absent — **it can now be discharged**.
- The arithmetic was right everywhere, in every lens, again.

## Accepted Majors (fold when the bundle is revised)

- **ADR-0186 D2's replacement bound is more expensive than the cost it refused** (exec E6). Dropping
  the ctx saved 866 ns/op; counting env elements costs **~19 µs** at the 10 000 default
  (controller's own measurement; the lens measured ~52 µs with a different implementation — same
  order, same conclusion). Undisclosed in the ADR. Fixable by counting once per env change rather
  than per evaluation, which the ADR currently does not say.
- **`authz/authz.go`'s own three godocs are falsified by D3 and prescribed nowhere** (count R-11) —
  `:80-81` *"An empty spec means allow-all"* and `:111` *"spec.Roles is empty (open access)"*. Phase
  15 fixes casbin's godoc and the two `definition/activity` godocs but not `authz`'s own.
- Plus the Majors recorded in each lens report; fold with the revision.

## Root causes, stated once

1. **D4 is a syntax problem that cannot be solved with syntax.** Three rounds, three disjoint hole
   sets.
2. **D5 needs a per-verb authorization model that does not exist.** One spec, four verbs.
3. **D3's mechanism is sound; its surface was under-modelled** (two durable locations; a public
   zero value).
4. **Bundle size is the multiplier.** Both failures were interaction failures, and the interaction
   surface is the product of the number of simultaneously-changing decisions.

## ⚠ OPEN DECISION — the owner's, not an agent's

The composition argument that justified one bundle applies to **51 + 52 + 53** (= D1, D2, D3), which
the spec says *"must ship as a set"*. **103 (D4) and 124 (D5)** were only *"belong with them"*, and
they are where both audits concentrated. Options are recorded in the session summary; nothing has
been re-cut without a decision.

**Do not implement any part of this bundle until the scope decision is made and the resulting
bundle passes an audit.** A bundle whose Decisions changed has not been audited — and this one's
Decisions have now changed twice.
