# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack of twenty "PREVIOUS RESUME
> POINT" blocks and was silently abandoned for 45 ADRs — see
> `docs/plans/HANDOVER-archive.md`. Per-delivery detail does **not** belong here:
> it belongs in that delivery's plan under a `▶ Progress` block, where it dies
> with the plan. This file carries only: where `main` is, what is in flight, and
> what to do next.

## State — updated 2026-08-08 (implementation session)

**▶ Pick up here: ADR-0167 (strict definition decoding) is IMPLEMENTED and has
PASSED the full Delivery Gate on `feat/strict-definition-decoding`.**
`/code-review` found 6, all fixed and folded; `/security-review` found 0.
**All that remains is `git merge --no-ff` to `main` and the push** — held for
owner confirmation, since merge-to-main is the one step this project's cadence
says to confirm. ⚠ The branch is pushed, so the push needs `--force-with-lease`.

⚠ **Two bugs on `main` were found while re-deriving ADR-0158, both executed, both
untested, both independent of any in-flight delivery — see "Bugs found on `main`"
below. One reports a rolled-back process as `completed`.** They are the work after
this delivery, each needing its own ADR.

### ADR-0167 — implemented, awaiting the Delivery Gate

Bundle: `docs/specs/2026-08-07-*` + `docs/adr/0167-*` + `docs/plans/2026-08-07-*`.
Per-delivery detail lives in that plan's `▶ Progress` block — read it before
touching anything; it carries the verification table, the mutation table and the
four ways implementation corrected the design.

- **Scope:** two call sites. `ParseYAML` decodes via `yaml.NewDecoder` +
  `KnownFields(true)`, mapping `io.EOF` back to today's empty-document meaning.
  `ProcessDefinition.UnmarshalJSON` applies `DisallowUnknownFields()` to its
  **internal** decode — the only place it survives a custom unmarshaler — plus an
  explicit trailing-token check, because `Decoder.Decode` would otherwise *loosen*
  what `json.Unmarshal` rejected. `ParseYAML` additionally drains the decoder so a
  second YAML document cannot smuggle content past strictness, and caps the
  per-field error count. 91 inserted production lines, comments included.
- **Delivery Gate 4/4.** `/code-review` **6 findings → 6 fixed** (godoc lost on
  `ParseYAML`; the ADR-0144 migration trigger; an untagged-field hole in the
  over-strictness guard; no test on the store's real `json.Unmarshal` path; an
  input-dependent error type; the bound missing from the multi-document loop).
  `/security-review` **0 vulnerabilities** — fail-open, silent field-dropping,
  error-text leakage to HTTP and YAML deserialization each checked by execution.
- **Gates, run on the implemented tree:** `go test -race ./...` **EXIT=0, 64
  packages, 0 races**; repo coverage **73.9 %** (baseline 73.8 %, no regression);
  `definition/model` **94.9 %**; `go vet ./...` EXIT=0; `golangci-lint run ./...`
  0 issues; **7 mutations applied, 7 caught**, all compiled and restored clean.
- ⚠ **Docker was approved by the owner for that one suite run.** The
  ask-before-Docker rule is unchanged for the next session.
- ⚠ **It is a DATA migration.** `DefinitionStore.GetDefinition`/`Lookup` decode
  persisted blobs through the now-strict `UnmarshalJSON`. New standing constraint:
  **a `NodeWire` field may not be removed without a migration.** `go vet` cannot
  prove this safe — it compiles, it does not decode.

**Adversarial Opus stand-ins ran before the owner gate** (two, separate
worktrees, both briefed to EXECUTE against a `main` baseline) and produced seven
fixes, all folded. The one that matters: **YAML strictness stopped at the first
document**, so everything after a `---` was silently discarded — a *live instance
of the very bypass this ADR closes*, since an overlay document declaring
`eligible_roles` parsed clean and still built a task with none. `ParseYAML` now
rejects any later document carrying content, while a bare trailing `---` stays
legal. Five further findings were adjudicated record-not-fix and are filed below.

**⚠⚠ The one lesson worth carrying out of this delivery.** The rule-#9 audit's
README finding named four camelCase lines. Re-deriving the list found **seven** —
`deadlineDuration`, `deadlineFlow`, `deadlineAction` were missed — so following
the audited plan literally would have shipped a README that still did not parse.
Execution also found a *second*, unrelated README defect the audit never saw:
`errorEndEvent` was documented as a valid `kind` and has never been registered.
**An enumeration inherited from an upstream document — even an audited one — must
be re-counted, never restated.** That is ADR-0159's lesson arriving for the third
time.

⚠⚠ **And it recurred inside the fix for itself.** The `CHANGELOG.md` entry I wrote
*while correcting* that rotted enumeration contained two fresh false claims,
caught by the stand-in review: "seven camelCase keys" (seven is the number of
*lines*; there are 10 occurrences of 9 distinct names), and "`errorEndEvent` has
never been registered" — an over-reaching *never* that is simply false, since
ADR-0127 retired it (`dcfe3f1`, with `Name: "errorEndEvent"` still present at
`dcfe3f1^`). **Verify the recap sentence, not just the analysis it summarises** —
the summary is where the false claims live.

### Bugs found on `main` (not caused by any in-flight delivery)

Both surfaced by the ADR-0158 premise re-derivation, both executed, **neither
covered by any existing test**.

📄 **Full executed evidence — read before acting on either:**
`docs/specs/2026-08-08-adr-0158-premise-evidence.md`. It holds the writer table
(every path that sets a terminal or `Compensating` status, and which arm families
each drains), the verbatim probe output for both bugs, and Appendices A and B —
24 re-derived ADR-0158 claims with their real locations on today's `main`. That
file is **evidence, not a decision**; it is the input a 0158 rewrite starts from,
and it exists because the reports would otherwise have died in a session
scratchpad.

1. **An arm firing during compensation destroys the rollback.** A boundary arm
   fired while `status=compensating` consumes its host and completes the process:
   `compensating → completed`, `CompleteInstance{Result:map[]}` published,
   `Compensating.ActiveCmdID` cleared, and the outstanding compensation action's
   `ActionCompleted` then refused by dispatch's terminal guard (`outcome=dropped`).
   **The rollback silently never finishes.** `IsTerminal()` reads false here and
   does not stop it; `s.Status != StatusRunning` does. Verified fix shape:
   `if s.Status != StatusRunning { return nil, nil }` in `fireBoundaryArm` —
   engine suite stays green with it. `grep -c 'Status'` on `step_boundaries.go`
   and `step_gateways.go` returns **0 and 0**. Needs its own ADR.
2. **Post-terminal resurrection via tier 4.** `handleUnhandledError`'s failFast
   branch fails the instance *without* dropping tokens, so a token resumes and
   drives on a dead instance, arming a boundary and emitting a `ScheduleTimer`
   that ADR-0161's filter exempts. ⚠ Check against ADR-0164's five known
   resurrection routes — this may be a sixth.

ADR-0166 (`processtest` sees every signal/message waiter source) shipped
2026-08-07, closing pre-v0.1.0 blocker 6. ADR-0165 shipped 2026-08-06.

| | |
|---|---|
| `main` | ADR-0166 is the newest bundle. ⚠ **Do not trust a `main` SHA written here** — this file is committed onto `main`, so any SHA it quotes for `main` is stale the moment it lands. Re-derive: `git rev-parse --short main` |
| **`feat/strict-definition-decoding`** | **ADR-0167, implemented and green; owner gate outstanding.** ⚠ Pushed deliberately, so folding review fixes needs `git push --force-with-lease` |
| `feat/processtest-waiter-enumeration` | merged; safe to delete |
| `feat/terminal-trigger-guard` | merged (ADR-0165); safe to delete |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165's ten pre-squash commits, provenance only. Delete when you no longer want the phase-by-phase history |
| **`parked/scope-and-fanout-design`** | **delivery 3's draft ADR-0158 lives here** (`docs/adr/0158-signal-fires-every-matching-arm.md`, plan `docs/plans/2026-07-31-signal-arm-fanout.md`). ⚠ It also carries a **superseded ADR-0162 draft — do not read it.** ⚠ Its diff vs `main` is now **~18,000 deleted lines** of tests that `main` has since gained: the branch predates 2a, 2b, 0165 and 0166 entirely |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept only for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — older parked bundle, docs only. Owner DECIDED not to push it; do not re-raise |
| Latest ADR | **0166** on `main`; **0167 is written, audited and implemented** but unmerged on `feat/strict-definition-decoding`. 0158 lands with delivery 3; 0155–0157 are reserved by the older parked branch. Next free is **0168** |
| v0.1.0 | not tagged |

## ▶ The work after ADR-0167's gate: delivery 3 (ADR-0158)

**A broadcast signal must fire every matching arm per family, not just the first.**
The draft is on `parked/scope-and-fanout-design`. Its prerequisites are now both
shipped: ADR-0165 (terminal-trigger guard) and ADR-0166 (the harness can finally
drive an arm-only park, so delivery 3's headline scenario is testable at all).

**Before you act on that bundle, re-derive it.** It has never survived a rule-#9
audit, and every engine file it touches has moved under it:

- `engine/step_triggers.go`, `engine/trigger.go`, `engine/trigger_validate.go`
  were reshaped by **ADR-0165**, which deleted eight hand-copied terminal guards
  and moved enforcement into `dispatch` via `terminalPolicy()` on the sealed
  `Trigger` interface.
- `engine/state.go` and the scope lifecycle moved under **ADR-0162/0163** and
  **ADR-0164**.
- The parked branch's own test files are ~18k lines behind `main`.

So: re-read the draft, **execute** every claim it makes about current behaviour
(Premise Discipline — it predates that section of CLAUDE.md entirely), rewrite the
spec/ADR/plan against today's code, then run the rule-#9 audit on the rebuilt
bundle before any implementation.

⚠ **ADR-0154 left "first match per family" OPEN deliberately** — that is exactly
the gap 0158 closes. See `docs/adr/0154-*` and the signal-waiters topic memory.

### Then, in priority order

1. The **two ADRs still owed by delivery 2b** — incident-history retention (owner
   chose REVISIT; likely carry the incident error into the terminal-event payload)
   and **zombie scopes** (ADR-0162 ships a stale sentence claiming `endInstance`
   closes them; it never touches `s.Scopes`).
2. A pre-v0.1.0 blocker from the list below.

## How ADR-0166 shipped, and the one lesson worth carrying

`Classify` now delegates to `engine.InstanceState.SignalWaiters()`/
`MessageWaiters()` instead of re-deriving source 1 from token fields, so boundary,
event-gateway and event-subprocess arms are finally visible to `PublishSignal`/
`DeliverMessage`. `Park.AwaitingMessages` became `[]engine.MessageWaiter`
(breaking), `Park.Node` falls back for arm-derived parks, and the `ReasonTimer`
promotion widened so `AutoTimers()` keeps working beside a live arm.

Gate 4/4: `processtest` **90.2 %** (baseline 88.0), full suite **EXIT=0 / 64
packages / 0 races / repo 73.8 %** run **twice** (the first run certified a tree
`/code-review`'s fixes then changed), **`/code-review` 4 findings → 4 fixed**,
**`/security-review` 0 vulnerabilities**. 20 tests; 15 mutations, 15 caught.

### ⚠⚠ The delivery bound was refuted FOUR times, always by execution

This is the transferable lesson. Four successive rounds each produced a bound that
looked obviously correct, and each was falsified by *running* it, never by review:

1. **rule-#9 audit** killed "deliver each name at most once" — two sequential token
   catches of one name is ordinary BPMN.
2. **implementation** killed the token-id fingerprint — a token **keeps its id** as
   it advances; only its node changes.
3. **adversarial stand-in review** killed the fingerprint idea outright — a loop
   re-enters the *same* node; the arm-slice counts are instance-wide, so the arm's
   own branch arming anything re-authorises delivery forever; and one last-key slot
   *displaces* across instances (an arm fired 4–28 times under concurrency).
4. **`/code-review`** killed the waiter COUNT that replaced it — two sequential
   *arms* of one name each report a single waiter, so the second was silently
   suppressed. **That is the audit's own finding (1) reproduced one level up, on
   the arm side.**

What ships: a token catch is **never** bounded (it is consumed when it fires and
cannot re-match); an **arm** is bounded per instance per **parked node**, because
an arm's real identity is unreachable — the arm slices have unexported element
types — and parked nodes are the closest observable proxy.

**If you touch this, execute the shapes in spec §2.6–2.7 before believing any
argument about it.** And the meta-lesson: *when a review kills a bound for
construct A, immediately check the same shape for construct B.*

### Two smaller lessons from the same delivery

- **A mutation that fails to COMPILE is not a RED**, and **a mutation that cannot
  DISCRIMINATE is not verification.** Three of this delivery's mutation attempts
  were invalid on the first try; each would otherwise have been recorded as proof.
  One test asserted `ErrUnhandledPark` + `"human-task"`, which the mutated build
  also produced — only asserting *the clock did not move* separated them.
- **One of this delivery's own added tests could not fail**, and it was written
  during a *coverage* round — the situation where a vacuous test is easiest to
  write. Assert the returned value, not a downstream error both branches reach.

### Verification facts worth keeping

- **Container-free packages**: `engine`, `runtime/calllink`, `runtime/signal`,
  `runtime/task`, `service`, `processtest`, `transport/http`. ⚠ **`./runtime/...`
  as a whole is NOT** — `main_test.go`, `rehydrate_durable_test.go`,
  `jobstore_rehydrate_durable_test.go` and `timer_txflow_test.go` import
  `internal/dbtest`.
- ⚠ **`go vet ./...` compiles every test file**, including the Docker-only ones.
  For a breaking type change it is the cheap way to prove no consumer is hiding
  behind the container gate — it caught nothing here only because nothing was wrong.
- **Baselines to hold**: `processtest` **90.2 %**, `engine` **91.9 %**, repo
  **73.8 %**, lint 0.
- ⚠ **`service` sits at 52.6 %**, below the 85 % floor — long pre-existing, not a
  regression. Backlog item 4; worth a decision rather than silence.
- **Cross-package godoc links resolve against the PACKAGE's imports, not the
  file's** — verified by running `go/doc` over `./engine`.

## Known gaps still owed by delivery 2b (ADR-0164)

ADR-0165 discharged the first of three. Two remain, plus a small third it added:

- **Incident-history retention.** `forceTerminate` and cancel's immediate branch
  still erase every incident, so the audit view renders empty, `incident_count`
  drops to 0, and `terminalEventErr` degrades to `"cancelled"`. Owner-decided to
  REVISIT in its own ADR — most likely carrying the incident error into the
  **terminal event payload**.
- **Zombie scopes.** Four terminal transitions set a terminal `Status` without
  pruning `s.Scopes`. ADR-0162 ships a **stale sentence** claiming `endInstance`
  closes them; it never touches `s.Scopes`. ⚠ ADR-0165 sharpened this: a terminal
  instance with a still-**open** scope holding compensation records now has those
  records unreachable by any walk. Correct behaviour, but it is this gap's shadow.
- **Third, small:** `stepCompensateRequested`'s surviving-records check stays a
  hand-written in-handler guard because it reads state, not the trigger. Any
  future state-dependent terminal policy faces the same choice.

## Pre-v0.1.0 blockers

1. ✅ **Strict definition decoding — IMPLEMENTED 2026-08-08, awaiting the owner
   Delivery Gate** on `feat/strict-definition-decoding` (ADR-0167). Both decoders
   reject unknown fields, no opt-out. ⚠ Not closed until it merges to `main`.
   ⚠ It does **not** close the fail-open `AuthzSpec` itself — a hand-authored
   empty spec still admits everyone. That is deliberate design on the type and
   takes its own ADR.
2. **A zero `next_run` cannot be armed on MySQL.** `runtime/timerops.go` arms a
   zero `nextRun` when `TriggerSpec.Next` reports `ok == false`;
   `DATETIME(6) NOT NULL` rejects `'0000-00-00'` under strict mode. Postgres and
   SQLite store it fine. Needs a reject-vs-normalise ADR.
3. `Upsert` can persist `State: Claimed, Claim: nil` — the read path upholds the
   invariant, the write path does not.
4. ✅ **ADR-0159's misnamed symbols — CLOSED 2026-08-07** (branch
   `docs/adr-0159-symbol-names`). ⚠ It was **three** symbols, not the two recorded
   here: `ErrBadArmedCursor` → `ErrBadArmedTimerCursor` was missed by the original
   entry, and two test functions (`TestArmedCursorRoundTrip`,
   `TestDecodeArmedCursorRejectsGarbage`) carried the dead name while a third in the
   same file already used the right one. Corrected in place across the ADR, spec and
   plan; the ADR carries a provenance note recording the original spelling.
   **Lesson: the blocker's own enumeration had rotted — re-count, don't inherit.**
5. **`TestPgxNotifierListenDrainsBeforePollInterval` is load-flaky.**
   `require.Eventually(..., 5s, 25ms)` at
   `internal/persistence/store/notifier_pgx_test.go:98` waits on a NOTIFY-driven
   relay drain while a dozen containers boot. Interacts with item 7; **do not
   silence it**.
6. ✅ **`processtest` cannot drive an arm-only park — CLOSED by ADR-0166.**
   Kept as a pointer because blocker 9 below is its unclosed twin.
7. **Suite speed.** `internal/dbtest`'s `sync.Once` container boot fires per
   package → 12 Postgres + 7 MySQL boots (~60s of a ~2min suite). Fix: honour
   `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as
   fallback, plus `scripts/testdb.sh up|down` and CI service wiring.
8. **The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.**
   No test combines `WithForceTermination` with a boundary-arming host, and
   `s.Boundaries = nil` has no semantic cover anywhere. Cheapest real fixture:
   `engine/step_terminal_test.go:669`, which already arms an error boundary on a
   sibling branch that survives the transition.
9. **`Park.HasArmedTimers` has blocker 6's defect, for TIMER arms — still OPEN.**
   ⚠ Found by ADR-0166's audit and deliberately left out of its scope.
   `HasArmedTimers: len(state.Timers) > 0` reads one source, so a boundary or
   event-gateway timer arm is invisible. Measured: a user task with a boundary
   timer gives `len(Timers)=0, len(Boundaries)=1, HasArmedTimers=false`.
   Consequence: after ADR-0166 ships, a definition parked purely on a **timer**
   arm is still undriveable through the harness, and `AutoTimers()` cannot see
   it. Closing it needs an engine-side `TimerWaiters()`/`ArmedTimerNodes()`
   authority mirroring `SignalWaiters` — its own ADR. **Until then, no document
   may claim the ADR-0154 class is closed in `processtest` outright.**

## Follow-ups opened by ADR-0167's adversarial review

Each was **executed**, none is caused by ADR-0167 alone, and each was
deliberately left out of that delivery to keep an already-breaking change
bounded. In priority order:

0. ⚠ **BEFORE DEPLOYING ADR-0167: audit stored definitions for five camelCase
   keys.** ADR-0144 (`8179c0b`) moved the definition wire to snake_case; the five
   tags that were camelCase before it — `compensateAction`, `compensationAction`,
   `completionAction`, `correlationKey`, `messageName` — now fail strict decoding
   (each verified: `json: unknown field "compensateAction"`). A row written
   before `8179c0b` carrying any of them stops loading and every instance of that
   definition becomes unrunnable. Found by `/code-review`. ⚠ Its report said
   ADR-0144 renamed "*every* `NodeWire` json tag" and listed `eligibleRoles`,
   `retryPolicy`, `deadlineFlow`, `timerTrigger` — **re-derived, that is wrong**:
   only those five ever existed as camelCase (`git grep -ho 'json:"[a-z]\+[A-Z]...'
   8179c0b^`). The conclusion held; the enumeration did not. Again.

1. **An undecodable stored definition degrades silently, not loudly**
   (`internal/persistence/store/definitions.go`). Three of five `Lookup` call
   sites treat any decode error as not-found: `runtime/jobstore.go` skips the
   timer and logs `"definition not found"` — so **every armed timer for that
   definition is skipped forever**, deadlines and reminders included, behind a
   misleading log; `runtime/calllink/notifier.go` `continue`s on a comment
   asserting the failure is transient (now false), so the queue retries forever;
   `service/service.go` serves a definition-less view. No sentinel, no metric, no
   fallback, no migration tool. Historical exposure is **nil today** — 74 wire
   tags added across 26 commits, **zero ever removed** — so the guard is cheap
   now and expensive after the first removal. Wants `ErrDefinitionUndecodable`,
   Error-level logging at those three sites, and a deploy-time `VerifyAll`.
2. **The persisted human-task eligibility blob is still leniently decoded**
   (`internal/persistence/store/humantask_store.go`). `authz.AuthzSpec` has no
   struct tags and is decoded with a plain `json.Unmarshal`: both
   `{"Role":["manager"]}` and `{"RolesX":["manager"]}` give
   `err=<nil>, roles=[] → ALLOWED`. The store's own comment calls this column
   load-bearing for authorization. Engine-written, so narrower than the
   definition path — but it is the **last lenient decode of an authz-bearing
   struct in the repo**.
3. **`eligible_privileges` is never evaluated by `RoleAuthorizer`**, so a task
   secured only by privileges is allow-all under the default authorizer. Pairs
   naturally with the fail-open `AuthzSpec` ADR below.
4. **The fail-open `AuthzSpec` itself** — ADR-0167 explicitly does not close it.
   Executed: an absent spec **and** `eligible_roles: []`, `eligible_roles:` and
   `eligible_roles: null` all parse cleanly and yield allow-all, so an author
   writing `[]` for "nobody" gets "everybody". Needs its own ADR; a Warn log when
   authorizing on an empty spec is a cheap interim mitigation.
5. **Memory amplification on deeply nested subprocesses: 10.5x.** Each nesting
   level builds a `json.Decoder` that buffers its input, so cost is
   O(input x depth) where `json.Unmarshal` decoded in place. Measured at depth
   3000 / 806 KB: 4.40 GB allocated vs 0.42 GB baseline, wall time unchanged.
   Depth self-limits near 3300 via `encoding/json`'s token guard; **input size is
   unbounded and the library imposes no size limit anywhere**. Capping subprocess
   nesting is a semantic decision about definitions, hence its own item.

## Standing constraints

- ⚠ **Ask before using Docker** (owner, 2026-07-31 — other sessions saturate the
  daemon). Per-run approvals do **not** carry over. See the container-free list above.
- **Run the suite on the MERGED tree**, and **re-run after any `/code-review` fix**.
- `/code-review` and `/security-review` are **owner-invoked only**. **Run
  adversarial Opus stand-ins first anyway** — on ADR-0166 two stand-ins found five
  substantive issues (three executed regressions vs `main`) and replaced a whole
  decision before the gate ever ran. They still miss what the real gate catches:
  `/code-review` then found four more, including the headline defect. Brief
  stand-ins to **execute against a `main` baseline**, in a `git worktree`.
- Push on merge (standing preference).
- Fan out subagents **by Go package** — concurrent agents inside one package break
  each other's `go test` compile even on disjoint files.
- **An agent that must measure against a patched tree gets a `git worktree`**, and
  the brief must say so. "Clean up afterwards" is not isolation: an ADR-0166
  auditor patched the live tree while another was measuring against it.

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's
  plan in `docs/plans/`.
- **ADR-0166's full record** — every refuted form of the delivery bound, the
  mutation table, and what each review round found — the `▶ Progress` block of
  `docs/plans/2026-08-07-processtest-waiter-enumeration.md`, plus spec §2.5–2.7.
- **ADR-0165's task-by-task record** — every mutation, adjudication and false
  claim — `.superpowers/sdd/2026-08-05-structural-terminal-trigger-guard/progress.md`.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template. ADR-0165 carries **six**
  correction blocks added during implementation and the gate; read them, they
  supersede the original text.
- **Designs** — `docs/specs/`. ADR-0165's spec §9 carries its audit record;
  ADR-0166's spec §2.3 carries what its audit refuted and §7 the audit summary.
- **Conventions and gates** — `CLAUDE.md`, including the new **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.
