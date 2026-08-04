# Delivery 2b — premise sweep against the post-2a base (2026-08-04)

**This is not a rule-#9 audit.** The 2b bundle survived one. This is the narrower
re-validation that became necessary when **delivery 2a shipped and rewrote the
very files 2b edits** — see `audited-bundle-decays-when-base-moves` in the
session memory, and the `▶ Progress` block of
`docs/plans/2026-08-02-terminal-transitions.md`.

**Base verified:** `main` @ `87181b3` (engine tree identical to merge `168fb06`).
2a touched **only** `engine/` — `git diff --stat 17e148b 168fb06 -- runtime/ service/`
is empty, so every `runtime/…` citation in the bundle is unaffected by 2a.

**Bottom line: no phase is fundamentally invalidated.** Phase 1 needs a real
restructure (§1) plus renumbering; Phases 2 and 3 are sound and need only
renumbering; Phase 4 needs one test re-pointed. **One item requires an owner
decision (§4, I-3) and one documentation fix is non-negotiable (§2, F-1).**

---

## 1. DANGEROUS — must be fixed before Phase 1 runs

### D-1 — "Delete `:318`" would delete ADR-0162's permanent-wedge fix

Plan step 1.4 says: *"`exitRootEventSubprocessScope` emits
`removeEventTriggeredSubprocessArmsForScope("")` at `engine/step_nodes.go:318`
… **Delete `:318`** — it is subsumed by `cancelAllScheduledWork`."*

That was true at `17e148b`. At current HEAD:

- `engine/step_nodes.go:318` is `if c.s.hasChildScopeWithTokens("", currentScopeID) {`
  — **ADR-0162's subtree drain guard**, shipped in 2a. Deleting it re-opens the
  permanent-instance-wedge defect and silently un-fixes
  `TestRootEventSubprocessExitSeesGrandchildOfRoot`.
- The arm-retirement call moved to **`engine/step_nodes.go:324`**, inside
  `exitRootEventSubprocessScope` (function `:310-333`).

### D-2 — the same ordering problem exists at a second site the plan never mentions

`exitNestedEventSubprocessScope` emits
`appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(parentScopeID))`
at **`engine/step_nodes.go:378`**, *before* its completion block at `:407-412`.
Pre-existing, not 2a-induced — but the plan's ordering-invariant argument applies
identically and the plan is silent. Splicing `endInstance` in at `:407-412` as
written yields `[CancelTimer…, CompleteInstance, CancelTimer…]` there.

### D-3 — the "delete it, `cancelAllScheduledWork` subsumes it" fix does not generalize, and is unsafe at the nested site

`exitNestedEventSubprocessScope` reaches `:378` on the **non-terminal resume
path** too (`resumeInParentScope` returns true → `:417 return cmds, true, nil`,
instance still Running). Deleting `:378` would leave the enclosing scope's ESP
arms armed after `closeScope(parentScopeID)` — a real regression. The root site
(`:324`) has a weaker version of the same property: it runs before the
`len(c.s.Tokens) == 0` test, so it can execute on a non-completing return.

**Replacement instruction — symbols, not line numbers:**

> In `exitRootEventSubprocessScope`, move the
> `removeEventTriggeredSubprocessArmsForScope("")` call **into the non-completing
> branch** and let `endInstance` own the completing one:
>
> ```go
> if len(c.s.Tokens) == 0 {
>     return append(cmds, c.s.endInstance(StatusCompleted, c.at,
>         CompleteInstance{Result: copyVars(c.s.Variables)})...), true, nil
> }
> cmds = appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(""))
> return cmds, true, nil
> ```
>
> Apply the same restructure in `exitNestedEventSubprocessScope` for
> `removeEventTriggeredSubprocessArmsForScope(parentScopeID)` — but there the
> call must **also** still run on the `resumeInParentScope` success path, so
> hoist it into both non-completing returns rather than deleting it.

---

## 2. Factually wrong claims

### F-1 — ⚠ ADR-0164 never mentions incidents, while shipped 2a code cites it for exactly that

`grep -E '[Ii]ncident'` over ADR-0164 → **zero hits**. Its `endInstance` snippet
has no `s.Incidents = nil`; its godoc says "status, `EndedAt`, a cleared
compensation cursor, and the projection sweeps"; its Consequences are silent.

But the **plan** implements it, and **shipped, pushed code on `main` forward-
references ADR-0164 for it**:

- `engine/step_cancel.go:53-55` — *"Closing that is `endInstance`'s job in
  ADR-0164 (delivery 2b, audit finding C1), which clears `s.Incidents` at the
  single terminal site."*
- `docs/adr/0163-cancelling-a-token-cancels-its-task.md:188-191` — same claim, in
  a shipped ADR.

The spec omits it too. The incident clear entered only via the round-2 audit's
C1 and was never folded back. The spec shipped with 2a and cannot be amended, so
**ADR-0164 must be updated** or 2a's shipped comments point at a decision that
does not exist. **Non-negotiable.**

### F-2 — "four sites drop tokens" is wrong

Carried twice in the plan (the C1 row, and the `endInstance` godoc). Verified:
`grep 's.Tokens = nil' engine/*.go` → exactly **two** — `engine/step_nodes.go:531`
(`forceTerminate`) and `engine/step_triggers.go:233` (`handleCancelRequested`
immediate branch). The other two named sites end the instance **with tokens in
place**. Use shipped ADR-0163's wording verbatim: *"four terminal transitions end
an instance without going through `cancelTokenWaits` — two drop every token
wholesale, two leave the tokens in place."*

### F-3 — the Progress block is stale on its own premises

Branch base, "blocked on 2a merging", "spec … read it on the 2a branch" (it is on
`main` now), and "its only dependency on 2a was nominal" — that last is no longer
true, because 2a shipped a *documentation* dependency (F-1) that 2b must
discharge.

### F-4 — Phase 4's arm-retirement test would be dead under its own mutation

The mutation table pairs `TestCompletionRetiresNonInterruptingRootEventSubprocessArm`
with *"drop `cancelAllScheduledWork` from `endInstance`"*. If the test completes
through `exitRootEventSubprocessScope`, the root arms are **already** retired at
`engine/step_nodes.go:324` today, so it passes **with the mutation applied** — a
dead test of exactly the class this project keeps finding.

The scenario `engine/step_eventsubprocess.go:225-228` actually describes: a root
**non-interrupting** ESP fires (arm stays armed), its child scope drains while the
root scope still holds a token (so `exitRootEventSubprocessScope` returns early),
and the instance later completes when the root token reaches its End via
**`exitRootScope`** — which sweeps nothing. **Pin the test to that path.**

### F-5 — claims re-verified as still true

`handleSubInstanceFailed` still emits `FailInstance` first;
`handleCancelRequested`'s immediate branch still carries its
`cancelActionCmds`/`nodeCancelCmds` prefix (2a's deletion was on the
**compensation** branch only); `applyTerminate` unchanged; the ordering invariant
still holds at four sites and is still violated at `handleSubInstanceFailed`.

---

## 3. Stale citations

Every `engine/*.go:NNN` in the bundle was re-checked. **Fix by naming symbols,
not by re-numbering** — that is what caused this.

| bundle location | says | actually now |
|---|---|---|
| plan files list; ADR row 1 | `step_nodes.go:216-220` `exitRootScope` | function `:214-223`, completion block `:216-221` |
| plan; ADR row 2 | `step_nodes.go:321-324` root ESP exit | `exitRootEventSubprocessScope` function `:310-333`, completion `:326-331` |
| plan 1.4 (D-1) | arm retirement at `:318` | `:324`. `:318` is now `hasChildScopeWithTokens` |
| plan; ADR row 3 | `step_nodes.go:384-387` nested ESP exit | function `:350-418`, completion `:407-412`; **unmentioned** arm retirement `:378` |
| plan; ADR row 4 | `step_nodes.go:478-504` `forceTerminate` | function `:523-550` |
| plan 1.4 | delete `ended := c.at` at `:479-480` | `:524-525`. ⚠ **four** `ended := c.at` in the file (`:218`, `:328`, `:409`, `:524`) — disambiguate by function |
| ADR row 5 | `step_errors.go:246-255` | **unchanged** ✓ |
| ADR row 6 | `step_triggers.go:216-234` cancel immediate | `:225-243` |
| ADR row 7 | `step_triggers.go:830-838` sub-instance failed | `:847-855` |
| ADR row 8 | `step_compensation.go:766-787` `applyTerminate` | **unchanged** ✓ |
| plan 1.1 | `Compensating` at `state.go:238` | `:239` |
| plan O1 row, Phase 3 | `step_triggers.go:84-91` | `handleActionCompleted`: `tokenAwaiting` `:88`, `if tok == nil` `:89-91`. ⚠ two other `tokenAwaiting(t.CommandID)` sites — `:267`, `:785` |
| plan Phase 3 comment | `handleResolveIncident` tolerance `:940-944` | `:957-961` |
| plan Phase 3 comment | `CompensateThrow` consumes own token `step_nodes.go:983` | `startCompensationWalk` `:1027-1039`, `consumeToken` `:1028` |
| ADR | `startCompensationWalk` `:982-994`, `ResumeNode` `:986`, `ActiveCmdID` `:989` | `:1027-1039`, `:1031`, `:1034` |
| plan; ADR | `forceTerminate` skips compensation `:476-477` | `:521-522` |
| ADR; plan 4.1 | "harmless" claim `step_eventsubprocess.go:222-225` | block `:221-228`, sentence `:225-228`; 2a added *"and this fire path is status-guarded"* |
| ADR | `NewCompensateRequested` `trigger.go:348` | `:352` (doc `:348-351`) |
| plan 2.4 | `NewReverseToStart` terminal rejection `:356-359` | doc `:359-363`, func `:364` |
| ADR; **ADR-0109's correction note** | `NewReverseToNode` `:369-370` | `:373-374` (doc `:368-372`) |
| ADR | facade pre-check `runtime/processdriver_reverse.go:100-102` | `:99-101` (off-by-one, pre-existing) |
| — | `state_arms.go:180-184`, `step_compensation.go:667-676`, `:552-567`, `:130-133`, `:120-129`, `:114-119`, `applyFinish` `:683`/`:719-733` | **all unchanged** ✓ |

**Adjacent, not this bundle's bug:** 2a left two shipped comments with stale
self-citations — `engine/step_stale_commands.go:33-36` cites
`step_nodes.go:982-993` (now `:1027-1039`) and `:74` cites `cancelOpenTasks
(state.go:297-306)` (now `:312`). Backlog; 2b touches neither.

---

## 4. Interaction with 2a — design level

### I-1 — no `UpdateTask` double-emission ✓ verified, no plan change needed

`cancelTokenWaits` sets `Cancelled` on a pointer **into `s.Tasks`**;
`cancelOpenTasks` only emits for `IsOpen()`. A task already retired by 2a's
per-token teardown is skipped. 2a already re-baselined
`engine/step_fail_tasks_test.go` to assert exactly this. **Add to Phase 1.5's
triage notes so the churn is pre-adjudicated.**

### I-2 — 2a's deleted prepend and 2b's `endInstance` do not interact, but need a guard rail

Mutually exclusive branches of `handleCancelRequested`: 2a deleted
`taskCancelCmds` from the **compensation** branch; 2b routes the
**immediate-termination** branch. On the immediate branch tokens are dropped via
`closeVisitAs` + `s.Tokens = nil`, **never** through `cancelTokenWaits` — so 2a's
"`beginCompensation` covers it now" argument does **not** transfer, and
`endInstance`'s `cancelOpenTasks` is the only task sweep there. **Note it in
Phase 1.4** so a later reader does not "simplify" it away by analogy.

### I-3 — ⚠⚠ OWNER DECISION REQUIRED: clearing `s.Incidents` has consumers outside `engine/`

No conflict with 2a — wholesale clearing at a terminal site is a superset of
`removeIncidentsForToken`, and is what shipped ADR-0163 and `step_cancel.go`
promise. **But `s.Incidents` is read post-terminal in four places, and 2b makes it
always empty there:**

| consumer | effect after 2b |
|---|---|
| `runtime/outbox.go:71-93` `terminalEventErr` | preference order 1 (`st.Incidents[0].Error`) becomes unreachable; `instance.failed`/`instance.terminated` payloads fall back to `FailInstance.Err` — e.g. `"cancelled"` instead of the concrete incident text. `runtime/outbox_test.go:39` uses a hand-built state, so it **still passes while describing dead behaviour** |
| `runtime/processdriver_action.go:31-41` `terminalErr` | same degradation for the child-instance failure message |
| `service/instance.go:253-264` | the ProcessInstance audit view (ADR-0144–0151) renders `incidents: []` on every terminal instance — an operator loses the record of *why* it terminated |
| `dialect.{postgres,mysql,sqlite}` `incident_count` + `runtime/kernel/memstore.go:195` | terminal instances always report `IncidentCount: 0`; listing filters on it silently exclude them |

Concrete reachable case: an instance parked on an incident (`raiseIncident`
leaves it **Running**) receives `CancelRequested` with no compensation records →
immediate branch → today the incident survives and `terminalEventErr` reports it;
after 2b it reports `"cancelled"`.

This does **not** re-litigate the owner's "`endInstance` unifies state and
sweeps" decision, which stands. What was missing is the *adjudication of this
consequence*.

### ✅ RESOLVED — owner decision, 2026-08-04: **narrow to token-linked incidents**

`endInstance` must **not** do `s.Incidents = nil`. It clears only incidents whose
token no longer exists. Rationale: that is literally what shipped ADR-0163
promises — *"incidents do not outlive **the token they describe**"* — so
wholesale clearing over-delivers on the invariant and pays for it in lost
terminal diagnostics.

Consequences of this decision for implementation:

- **The two token-dropping sites** (`forceTerminate`, `handleCancelRequested`'s
  immediate branch — the only two, per F-2) orphan their incidents, so those are
  cleared. **The two that leave tokens in place** (`handleUnhandledError`'s
  immediate-failure branch, `handleSubInstanceFailed`'s tail) keep theirs, and
  `terminalEventErr` still finds the concrete error.
- **This makes F-2 load-bearing, not cosmetic.** The two-vs-four distinction now
  determines behaviour, not just wording. Get it wrong and the fix is either a
  no-op or the wholesale clear by another name.
- **Ordering matters:** the incident sweep must run **before** `s.Tokens = nil`
  at the two dropping sites, or every incident looks orphaned and the narrowing
  collapses back into a wholesale clear. Decide explicitly whether `endInstance`
  performs the sweep itself (and therefore must run before the caller nils
  tokens) or whether the caller sweeps first — and pin it with a test that a
  wholesale clear would fail.
- **ADR-0164 must record the decision and its reasoning** — this discharges F-1,
  which is otherwise non-negotiable, and keeps the shipped forward-references in
  `engine/step_cancel.go` and ADR-0163 truthful. Note there that the promise
  kept is the *token-linkage* invariant, not "no incidents on terminal
  instances".
- **Needs its own test**, and a mutation: an instance parked on an incident,
  failed via `handleUnhandledError` (tokens survive) → assert the incident is
  still present and `terminalEventErr` reports it; and force-terminated
  (tokens dropped) → assert it is gone. A wholesale `s.Incidents = nil` must
  fail the first case.

### I-4 — the Phase-2 reproduction still reproduces ✓

2a did not touch `forceTerminate`'s body, the guard, or `walkPartial`/
`applyFinish`. 2a's per-token `cancelTokenWaits` in `beginCompensation` is a
no-op on a force-terminated instance because `s.Tokens` is nil.

### I-5 — Phase 1's test affordances improved

2a added `engine/export_test.go` shims including `CancelOpenTasks`. An
`EndInstance` shim is now the established pattern if Phase 1 wants a white-box
unit test. `engine/step_terminal_test.go` does not exist — no collision.

### I-6 — 2a renamed or deleted no test

`git diff 17e148b 168fb06 -- engine/ | grep -E '^-func Test'` → empty. 27 tests
added. Two files re-baselined (`step_compensation_test.go`'s new
`requireCompensationStart` now asserts `[UpdateTask, InvokeAction]`;
`step_fail_tasks_test.go` per I-1). Phase 1.5's triage should expect
`requireCompensationStart`'s `require.Len(cmds, 2)` to move first if command
counts shift on the compensation-start path.

---

## 5. Verified unchanged

All eight terminal sites still exist and still behave as the ADR's table says
(only line numbers moved). The C3 footnote — `applyTerminate` has no cursor
assignment of its own, the only clear is `stepCompensationFinish:552-567` — is
still exactly true. The compensation guard, the sibling in-flight guard, the
plain-full-rollback comment, `applyFinish`'s shared resume block,
`NewReverseToNode`/`NewReverseToStart`/`NewCompensateRequested` semantics, the
`runtime/` facade pre-check, `cancelAllScheduledWork`'s composition, and
`Compensating` being an exported field (so `assert.Zero` needs no shim) — **all
unchanged**.

⚠ The spec (`docs/specs/2026-08-02-scope-lifecycle-correctness.md` §1.7–1.9,
§3.3) carries the **same pre-2a line numbers and the same incident omission**. It
shipped with 2a and is pushed, so it cannot be amended: **implementers must treat
the corrected plan and ADR-0164 as authoritative over the spec.**
