# ADR-0185-core — rule-#9 audit adjudication

**Date:** 2026-08-23 · **Bundle audited:** `5ce393f4` (spec + re-cut ADR-0185 + plan)
**Lenses:** execution · failure-modes · counting · interaction — four Opus agents, four detached
worktrees at the bundle commit, **step-0 presence check passed in all four** (the worktrees were
created AT the bundle commit rather than at `main`, so the documents were present by construction).
**Reports:** `audit-0185core-{execution,failure-modes,counting,interaction}.md` beside this file.

## ⛔ VERDICT: THE BUNDLE FAILS ITS AUDIT. Not an input to implementation.

**58 findings raw across four lenses; 22 raw Criticals.** This is the **third** consecutive failed
audit for this lineage (2026-08-20 five decisions; 2026-08-21 five decisions revised; 2026-08-23
three decisions).

⚠⚠ **The finding count did not fall when the bundle was cut from five decisions to three** — 58 raw
here against 58 and 38 in the two prior rounds. **That is ADR-0186's trend reproducing exactly**, and
it is the fourth consecutive data point: the finding *rate* is a property of the process, not the
bundle size. What the cut was supposed to buy was the removal of the *interaction* failure class.
**It did not buy that either** — see the verdict on D3 below. That is the single most important
thing this round established, and it is addressed in the recommendation.

## Cross-lens convergence — the strongest signal this process produces

Five findings were reached independently by two or more lenses. Every one is confirmed by the
controller's own execution.

| # | finding | lenses | controller check |
|---|---|---|---|
| **C1** | `checkSpecStated` **fails the plan's own case 5** | interaction F1, execution F8 | ✅ ran it: `AllowAll` + empty spec → `ErrSpecStatesNothing`, where the plan asserts `NoError` |
| **C2** | the migration **corrupts** `wrkflw_definitions.definition` (wrong key + strict decoding) | failure-modes F1, interaction F2, execution F5/F6 | ✅ `node_wire.go:190` `DisallowUnknownFields()`, `yaml.go:209` `KnownFields(true)` |
| **C3** | adding `EligibleOpen` breaks ADR-0187's `TestDefinitionEligibilityFieldsAreTheDeclaredSet` | counting F4, interaction F7 | ✅ read the guard: it pins the `Eligible*` set to exactly three by reflection |
| **C4** | *"when human tasks are configured"* **is not a state that exists** | interaction F3, execution F19 | ✅ `service.go:189-191` defaults `taskStore` to `MemTaskStore`; `:217` builds a `TaskService` unconditionally |
| **C5** | the authoring gate's blast radius is misstated in **both** directions | failure-modes F13, execution F17 | accepted on the lenses' ablations (both measured; execution's is the completer of the two) |

## Accepted Criticals, grouped by what they falsify

### A. D3's gate — the mechanism does not do what the bundle claims (C1)

**Accepted, and it is the headline.** `checkSpecStated` has two legs. `EvaluatesDimension` is
consulted only *inside* the per-dimension loop, so it can rescue the **unevaluatable** leg and is
**structurally incapable** of rescuing the **states-nothing** leg (`if spec.Open || stated`), which
never consults the authorizer at all.

Therefore three separate sentences in this bundle are **false**: spec §1's D2×D3 row
(*"Resolved in §4"*), spec §5.3's table (*"dissolves D2 × D3"*), and ADR Decision 3 (*"keeps
`WithAllowAllAuthorizer()` honest"*).

⚠⚠ **This is the same interaction class that killed the 2026-08-21 revision, moved from
`Privileges` to the empty spec** — a fix correct for its own decision and blind to the leg of the
neighbouring decision it was written to rescue. The removal grid in spec §1 was written specifically
to catch this shape and did not, because the grid reasoned about decisions pairwise while the defect
lives *inside one decision's own implementation*.

**Adjudicated fix (a design change, not a wording change):** collapse the two legs into **one
authorizer-aware question**. The gate should ask the authorizer *"can you evaluate this spec as
written?"* — one capability method taking the whole spec — rather than asking about dimensions and
then applying a second, authorizer-blind rule. `AllowAll` answers yes unconditionally;
`RoleAuthorizer` answers no for an unstated spec and no for `Privileges`. This is strictly simpler
than the two-capability alternative and removes the leg that cannot be reached.

### B. D3's migration — corrupts data three separate ways, and is forbidden outright (C2 + execution F3/F6/F14)

All accepted.

1. **Wrong key, fatal.** The ADR says backfill `"Open": true` *"across all three durable copies"*.
   The three copies **do not share a shape**: two hold a marshalled `AuthzSpec` (no json tags ⇒ key
   `Open`), the third holds `NodeWire`'s flat `eligible_*` keys and no `AuthzSpec` at all. Under
   ADR-0167's strict decoding the injected key makes the **whole definition** fail to load —
   *strictly worse than not migrating*. The bundle spells the field three ways across its own
   documents (`"Open"` / `eligible_open` / `open`).
2. **The prescribed SQL silently corrupts.** `json_set(definition,'$.nodes[#]...')` **appends a
   phantom node** and succeeds, after which the definition does not decode. The working form is a
   `json_each`/`json_group_array` array rebuild — and the snapshot copy's `$.Tasks[*]` needs the
   same, so **2 of 3 copies** do.
3. **One malformed row aborts the entire backfill** on SQLite (`SQL logic error: malformed JSON`,
   **0 rows migrated**) because SQLite's column is plain `TEXT` where Postgres/MySQL are
   `JSONB`/`JSON`. **The three dialects are not transliterations of each other.**
4. ⚠ **`TestMigrations_OneFilePerDialect` (`internal/persistence/store/migrations_count_test.go:16`)
   forbids a `0002` file at all.** All three subtests fail. The bundle does not mention it. The guard
   is also **stricter than the ADR-0132 it cites**, which anticipates numbered files above the
   baseline — so the guard, not the migration, is what needs correcting, and that is now in scope.

### C. D3's copy-priority model is backwards for WRITES (failure-modes F10)

**Accepted.** Spec §2.1 calls `wrkflw_human_task.eligibility` the copy that matters and the snapshot
a projection *"read by instance rehydration"*. For **reads** that is right. For **writes** it is
inverted: `handleHumanClaimed` (`engine/step_triggers.go:578`) takes the task from the
snapshot-derived `InstanceState` and emits `UpdateTask{Task: task.Clone()}` (`:591`), which
`performUpdateTask` (`runtime/processdriver_action.go:509`) **Upserts wholesale** into the task row.
Seven other emit sites do the same.

⇒ a snapshot the migration misses **does not stay stale — it reverts the repair**, and the
successful claim is the event that strands the task. Combined with the gate applying to all four
verbs (leaving **no repair verb**), that is **unrecoverable loss of in-flight human work, caused by
the migration written to prevent it.**

**Adjudicated fix:** correct §2.1; make the snapshot backfill a **prerequisite**, not one of three
parallel targets; and evaluate dropping `Eligibility` from the write-back entirely, since the engine
never mutates it. The last of those is the real fix and is a design increment this bundle did not
budget.

### D. D3 breaks two blessed, documented configurations

- **ADR-0118's no-eligibility manual task** (execution F18). `examples/scenarios/manual_task` dies
  at `go run` (EXIT=1). ADR-0118's Consequences names ADR-0117's *optional* eligibility as its
  **prerequisite**, and only ADR-0117 is amended. **Accepted:** the authoring gate must exempt
  manual tasks, and ADR-0118 must be annotated.
- **YAML authoring** (counting F3). `definition/model/yaml.go:19-27` declares **`nodeYAML`**, a
  second wire struct with its own `yaml:"eligible_*"` tags, mapped at `yaml.go:106`. It appears
  **zero times** in the bundle. With `KnownFields(true)`, `eligible_open: true` is a parse error
  while the same task without it is rejected by the new gate ⇒ **an open user task is unauthorable
  in YAML**, one of only two authoring forms. **Accepted.**

### E. D2's trigger condition does not exist (C4)

**Accepted.** A bare `service.NewProcessEngine()` already serves human tasks on a defaulted
`MemTaskStore` + `AllowAll`. So *"human tasks are configured"* has no implementable signal, and D2
must either break **every default engine** — in which case plan Task 4 case 4 (billed as the
regression guard) fails — or leave Context finding 2's hole open in the commonest wiring. **This
needs a design increment the bundle does not have:** either the engine stops defaulting a task
surface, or the error triggers on explicit human-task wiring only, which is a narrower promise than
the ADR makes.

### F. D1's two smaller Criticals

- **`WithAnonymousActorAllowed()` and the empty-`Actor.ID` rejection void each other**
  (failure-modes F5): the three demo mains cannot claim. **Accepted** — the anonymous opt-in must
  synthesize a non-empty sentinel identity, and the ADR must say which.
- **A consumer decorator silently downgrades casbin to roles-only** (failure-modes F8), undetected
  at wiring time. **Accepted**, and it becomes moot under §A's single-question capability only if
  the same forwarding discipline is required; keep it as an explicit requirement.

## Findings inside the bundle's own evidence — again

The spec's §2 was written to stop unexecuted claims entering the bundle. It contains its own:

- **execution F20 / counting F8** — §2.6's second "vacuous 403" pin is **wrong**.
  `gin/gin_coverage_test.go:244` asserts **404** on a nonexistent token; gin has **no** 403
  assertion at all. ✅ Controller-confirmed. **Both vacuous-403 pins are in `stdlib`**
  (`errors_test.go:158` and `:187`). ⚠ Counting's *"wrong on both members"* **overstates it** — the
  `:187` member is correct and only its partner was misattributed. Counting is **partially
  rejected** on that point; execution's version is precise and is the one folded.
- **execution F10** — Task 3 Step 4 tells the implementer to record the repo-wide compile breakage
  from adding `Open`. Executed: **the list is empty.** `go build`, `go vet` and the whole
  container-free suite pass. That is not merely a wrong instruction: it means **no test anywhere
  pins the durable eligibility shape**, which is itself a gap this bundle should close.
- **counting F5** — `CustomizeConfig` has **eight** fields at `seam.go:20-80`, not the six at
  `:19-33` the ADR inherited from the pre-ADR-0186 draft. §2.5's "anchors still resolve" list did
  not cover it.
- **counting F3/F1/F2, interaction F5** — the enumerations were derived over the packages the author
  was editing and then asserted over the repo. Same root as C2 and C3.

## Rejected / partially rejected

- **counting F8**, partially — see above; the `stdlib:187` member is correct.
- Nothing else is rejected. **21 of 22 raw Criticals are accepted**; the 22nd is the counting-F8
  half above, which is accepted in substance and corrected in detail.

## What HELD — do not re-litigate

- ⭐ The **29 / 9 files / 5 packages** pin table is **exact**, per-file and per-package, and the
  `validate_test.go:61` exclusion is right. The only error in §2.6 is *which* two pins are vacuous.
- ⭐ **Three** durable `AuthzSpec` copies — **no fourth**. Counting checked every JSON column, event,
  outbox and journal payload. The snapshot copy survives a real SQLite store round-trip
  (execution F16).
- ⭐ **Five** `Authorizer` implementations; the five non-test `.Authorize(` sites with their verb
  labels; one migration per dialect; three `examples/` mains; `DurableProvider`'s six methods; the
  five `LogAttrs` attributes; ~20 further line anchors.
- ⭐ `RoleAuthorizer` **does** silently discard `Privileges` including in the mixed spec — Context
  finding 3's most dangerous case is real (execution F2).
- ⭐ fiber `SetContext` propagates and `Locals` does **not** (execution F13).
- ⭐ **All three adapters tolerate unknown body keys**, so *"ignored, not rejected"* is correct
  (execution F12). This **answers plan Task 9 Step 4's open question** — the ADR-0167 strictness
  concern does not reach the DTO decode path.
- ⭐ The mint site is singular and `AwaitHuman` reuses the same variable; goose accepts a data-only
  `0002`.

## Root causes, stated once

1. **D3 is carrying three unrelated hard problems at once** — a gate whose placement is contested, a
   three-shape data migration under strict decoding, and an authoring rule that collides with two
   blessed configurations. **Nineteen of the 22 raw Criticals are D3's.** D1 and D2 have two each.
2. **The gate's defect was intra-decision, and the removal grid only looked inter-decision.** The
   interaction discipline needs to cover *a decision against its own implementation*, not only
   decision-vs-decision.
3. **The enumerations were scoped to the packages being edited.** `nodeYAML`, the atrest guard, the
   migration-count guard and the `CustomizeConfig` field count were all outside that frame.
4. ⚠ **The bundle predicted the wrong ADR-0187 interaction.** It named a *silent* `CREATE INDEX`
   miss; the real one is a **hard parse failure** on a data-only migration (the at-rest parser
   fail-closes on `UPDATE`), plus the `NodeWire` reflection pin. Predicting an interaction is not
   the same as deriving it.

## ⚠ OPEN DECISION — the owner's, not an agent's

Three audits, three scopes, no convergence, and the finding count flat across all three. The
composition argument that bound 51+52+53 together is now in tension with the measured fact that this
lineage does not converge as a multi-decision bundle. Options are recorded in the session summary.
**Nothing is to be re-cut, revised, or implemented until that decision is made.**
