# Lens C — RE-COUNTING audit of the ADR-0179 bundle

Worktree: `/private/tmp/.../scratchpad/audit-c-c`, detached at bundle commit `caf0bdc8`,
whose parent is current `main` `954c2a05` (ADR-0181/0182 shipped). The bundle text was
authored against `12c9d7e3`, **two deliveries ago** — so every line-number citation is
presumed rotten until re-derived.

Verdicts: ✅ CONFIRMED / ❌ WRONG / ⚠️ IMPRECISE / ℹ️ DERIVED / ❌ STALE

| # | claim (doc + section) | claimed | derived | verdict | how |
|---|---|---|---|---|---|
| 1 | ADR Context / spec §1: "the short-circuit at `engine/step_triggers.go:292-294`" | short-circuit at 292-294 | at base `12c9d7e3` it WAS 292-293; at current `main`+bundle it is **`step_triggers.go:338-340`** | ❌ STALE | `git show 12c9d7e3:engine/step_triggers.go \| grep -n "StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID"` → 84, 292; same grep at HEAD → 109, **338** |
| 2 | spec §1: "before `effectiveRetryPolicy` (`:316`)" | `:316` | now `engine/step_triggers.go:362` | ❌ STALE | `grep -n effectiveRetryPolicy engine/step_triggers.go` → 362 |
| 3 | spec §1 / ADR: "**The** short-circuit" (singular) — the mechanism | one site | **TWO** structurally identical short-circuits exist: `handleActionCompleted` (:109) and `handleActionFailed` (:338). The `ActionFailed` one is the right one, but "the short-circuit at `<line>`" is only disambiguated by the line number — which has rotted (row 1) and now points into `handleCancelRequested`'s comment block | ⚠️ IMPRECISE | `grep -n "^func " engine/step_triggers.go`; line 292 today sits inside `handleCancelRequested` (:150-322) |
| 4 | ADR Consequences: "the load-bearing edit is *not* at `engine/step_compensation.go:1338-1344` — that range is a **comment**" | 1338-1344 is a comment | HEAD `step_compensation.go:1338-1344` is still a comment (`// …Incidents[0]…`), ending at 1344; the code line is 1345 | ✅ CONFIRMED | `sed -n '1330,1350p' engine/step_compensation.go` (see row 12) |
| 5 | ADR/spec §3.4: "`runtime/outbox.go:81` (`terminalEventErr`)" | :81 | `func terminalEventErr` **is** at `runtime/outbox.go:81`; the `Incidents[0].Error` read is at :83 | ✅ CONFIRMED | `grep -n terminalEventErr runtime/outbox.go` |
| 6 | ADR/spec §3.4: "`runtime/processdriver_action.go:31` (`terminalErr`)" | :31 | `func terminalErr` **is** at :31; the read at :33 | ✅ CONFIRMED | `grep -rn "func terminalErr" runtime/` |
| 7 | ADR/spec §3.5: "plus **`engine/state.go:475`** (`endInstance`)" | state.go:475 | the `retireCompensationStallIncidents` call in `endInstance` is now **`engine/state.go:516`**; `endInstance` itself starts at :488. 475 was correct at base `12c9d7e3` | ❌ STALE | `grep -rn retireCompensationStallIncidents engine/state.go` → 516; `git grep -n … 12c9d7e3` → 475 |
| 8 | ADR/spec §3.5: "`retireCompensationStallIncidents` is called at `step_compensation.go:{524,1287,1294,1345}`" | those four | **all four still exact at HEAD** — `step_compensation.go` was untouched by ADR-0177/0178/0180/0181/0182 | ✅ CONFIRMED | `grep -rn retireCompensationStallIncidents engine/step_compensation.go` → 524, 1287, 1294, 1345 |
| 9 | **"Incident retirement has FIVE sites, not four"** | 5 | **5 confirmed**: `step_compensation.go:{524,1287,1294,1345}` + `state.go:516`. No sixth anywhere in the repo (only the func def at `state.go:462` and a test comment) | ✅ CONFIRMED | `grep -rn retireCompensationStallIncidents . --include='*.go'` — 9 hits total: 4 calls + 1 call + 1 def + 2 doc comments + 1 test comment |
| 10 | **"There are four `compensationInvoke` sites today … adds a FIFTH"** | 4 today → 5 | **4 production call sites confirmed**: `step_compensation.go:{412,574,1301}` + `step_nodes.go:1139` (was :1134 at base — ❌ that line moved, though the bundle never cites it). Definition at `step_compensation.go:503`. Post-change count of 5 follows | ✅ CONFIRMED | `grep -rn compensationInvoke . --include='*.go'` filtered to non-test, non-def |
| 11 | spec §3.1: "The four dispatch sites are **exactly** where `ActiveCmdID` is set" | exact correspondence | **CONFIRMED**: `cur.ActiveCmdID = cmdID` at `step_compensation.go:{405,567,1296}` and `cursor.ActiveCmdID = cmdID` at `step_nodes.go:1131` — four assignments, each 5-7 lines above its `compensationInvoke`. No fifth assignment anywhere | ✅ CONFIRMED | `grep -rn ActiveCmdID engine/*.go \| grep -v _test` → only those 4 are assignments |
| 12 | ADR: "`removeOrphanedIncidents` **only deletes `TokenID != \"\"`**" | predicate is `TokenID != ""` | actual predicate is `inc.TokenID != "" **&& s.tokenByID(inc.TokenID) == nil**`. The conclusion (an empty-TokenID incident survives) holds, but as written the sentence describes a *deletion* rule that would also delete live-token incidents | ⚠️ IMPRECISE | `engine/state.go:445-449` |
| 13 | ADR/spec: the incident "must survive **both** `endInstance` sweeps" / "`endInstance`'s **two** sweeps" | 2 sweeps | **2 confirmed**: `retireCompensationStallIncidents` (state.go:516) then `removeOrphanedIncidents` (state.go:518). No third incident-touching sweep in `endInstance` | ✅ CONFIRMED | `sed -n '488,530p' engine/state.go` |
| 14 | ADR Context + spec §1: "a compensation walk holds **zero tokens** (measured every frame)" | 0 tokens, always | **FALSE as a universal.** Executed a scope-wide throw with a parallel sibling (`scopeWideDrainDef([]string{"A","B"})`): the walk is in flight with **1 token** at every frame. `startCompensationWalk`'s own comment states it: *"A throw resumes past itself, so **sibling branches keep running while the walk is in flight**"* (`engine/step_nodes.go:1115-1116`) | ❌ WRONG | probe `engine/zzz_lensc_probe_test.go`, `go test -count=1 -run '^TestLensCZeroTokensDuringWalk$' ./engine/` → `FRAME 3 status=compensating tokens=1 [lensc-t4@bodySibling/st1] activeCmd="lensc-c3"`; frames 4 and 5 also `tokens=1` |
| 15 | ADR/spec/plan (×4 restatements): "ADR-0178's guard refuses **every** compensation retry and this ADR **silently never works**" | all retries refused | **FALSE as a universal.** The guard is `if !rec.Kind.walkScoped() && !s.spawnsNewWork()` (`engine/step_triggers.go:596`) — it refuses only when the instance spawns no new work. `spawnsNewWork()` for `StatusCompensating` is `!s.Compensating.walkTerminates(s.PendingCancel)` (`engine/state.go:575-586`), and `walkTerminates` returns **false** for `walkPartial` and for throw walks with no pending cancel. Measured `spawnsNewWork=true` at every frame of the throw walk above. So without the `walkScoped()` extension the retry is refused on **terminating** walks only (`walkAdmin`, `walkReverse`, and throw-with-pending-cancel) | ❌ WRONG (over-general) | same probe: `spawnsNewWork=true` on all 5 frames; `sed -n '/func (c compensationCursor) walkTerminates/,/^}/p' engine/state_compensation.go` |
| 16 | spec §4 test 12 / plan step 14: "a **throw** walk measures `SpawnsNewWork = TRUE`" — hence use a cancel-started walk | throw ⇒ TRUE | ✅ **CONFIRMED by execution** (`spawnsNewWork=true`, frames 3-5). ⚠ Note this test-plan sentence **contradicts row 15's universal in the same bundle**: if a throw walk spawns new work, the guard cannot be refusing "every" retry | ✅ CONFIRMED | same probe |
| 17 | ADR/spec: `walkScoped()` "covers the stall kind only" (bundle A) | stall only | ✅ **CONFIRMED on shipped `main`**: `func (k TimerKind) walkScoped() bool { return k == TimerCompensationStall }` (`engine/state_timer_waiters.go:38-40`); its doc even names "ADR-0179's compensation-retry timer is the next such kind" | ✅ CONFIRMED | `grep -rn walkScoped . --include='*.go'`; `sed -n '29,41p' engine/state_timer_waiters.go` |
| 18 | plan P3: "`engine/command.go:66` — 'intermediate, deadline, in-wait, and retry timers' omits `TimerCompensationStall`" | line 66, omits stall | ✅ **CONFIRMED, line unchanged** from base. **FIVE** `TimerKind` constants exist (`TimerIntermediate`, `TimerDeadline`, `TimerInWait`, `TimerRetry`, `TimerCompensationStall`); the comment names four. ADR-0181/0182 added no new kind | ✅ CONFIRMED | `grep -n "intermediate, deadline, in-wait" engine/command.go` → 66 at both HEAD and `12c9d7e3`; `sed -n '18,42p' engine/command.go` |
| 19 | spec §3.3: `cancelCompensationStallTimers` "filters **strictly** on `Kind == TimerCompensationStall` in **both loops**" | 2 loops, strict | ✅ **CONFIRMED**: loop 1 `if tr.Kind == TimerCompensationStall`, loop 2 `if tr.Kind != TimerCompensationStall` (`engine/step_compensation.go:432-445`). It has **2** call sites: `:472` (`armCompensationStallTimer`) and `:867` (`stepCompensationFinish`) | ✅ CONFIRMED | `sed -n '432,450p' engine/step_compensation.go`; `grep -rn cancelCompensationStallTimers . --include='*.go'` |
| 20 | spec §3.3: "`stepCompensationFinish` zeroes the cursor and calls `cancelCompensationStallTimers`" | that caller | ✅ CONFIRMED — `stepCompensationFinish` starts at `engine/step_compensation.go:823`, the call is at `:867` | ✅ CONFIRMED | `grep -n "^func " engine/step_compensation.go` + `sed -n '855,870p'` |
| 21 | ADR Context: ADR-0034 Decision 4 quoted "**verbatim**" | verbatim | ✅ **VERBATIM** — `docs/adr/0034-*.md:32-33` reads exactly *"**Best-effort compensation:** an `ActionFailed` matching the cursor's `ActiveCmdID` while `StatusCompensating` routes to advance (skip+continue), never back into `propagateError`/retry."* Only the `4. ` list marker is dropped, and the ADR introduces it as "Decision 4" | ✅ CONFIRMED | `grep -n -A6 -B2 "Best-effort compensation" docs/adr/0034*.md` |
| 22 | ADR/spec §1: "ADR-0034's Consequences claim is false: it says the failure is '**logged/skipped**'" | that wording | ✅ exact — `docs/adr/0034-*.md:51` *"Best-effort means a compensation action's failure is logged/skipped, not retried"*. Only ONE occurrence of "logged" in ADR-0034 | ✅ CONFIRMED | `grep -n logged docs/adr/0034*.md` |
| 23 | ADR/spec §1: "`grep -c slog` over `stepCompensationAdvance` and `handleActionFailed` both return **0**" | 0 and 0 | ✅ **0 and 0 at HEAD.** `stepCompensationAdvance` (`step_compensation.go:514`, 66 lines) and `handleActionFailed` (`step_triggers.go:334`, 131 lines to :464) contain no `slog`. `step_compensation.go` uses `slog` at 6 other places (203, 1132, 1230, 1239, 1248, 1314) — none on this path | ✅ CONFIRMED | `awk '/^func stepCompensationAdvance/{f=1} f{print} f&&/^}$/{exit}' … \| grep -c slog` → 0; `sed -n '334,464p' engine/step_triggers.go \| grep -n slog` → no match |
| 24 | spec §3.2: "`cloneState` (`engine/step_state.go:361`) … the only cursor field deep-copied is `Compensating.Records`" | line 361, one field | ✅ **both confirmed, line unchanged** — `func cloneState` at `step_state.go:361`; the sole `s.Compensating.*` line in the body is `s.Compensating.Records = cloneCompensationRecords(...)` | ✅ CONFIRMED | `grep -n "func cloneState" engine/step_state.go`; `sed -n '/^func cloneState/,/^}/p'` |
| 25 | spec §3.2 / plan P1.5: "`TestStepDoesNotMutateInput` (`engine/step_test.go:94`)" | :94 | ✅ **exact, unchanged** | ✅ CONFIRMED | `grep -rn "func TestStepDoesNotMutateInput" engine/` → `step_test.go:94` |
| 26 | spec §3.2: "the cursor's own comments justify **its fields** as 'plain scalars, keeping this struct value-copyable by `cloneState`'" | about the cursor's fields | the quoted comment (`engine/state_compensation.go:163-164`) reads "**All three** are plain scalars…" and is scoped to `TeardownArchiveKey/Offset/Count` **only**, not to the cursor generally. Substance (a slice field breaks the value-copyable convention) still stands | ⚠️ IMPRECISE | `grep -rn "plain scalars" engine/`; `sed -n '160,168p' engine/state_compensation.go` |
| 27 | spec §3.11: "`Incident.Kind` already exists (ADR-0175); this adds a **constant**, not a field" | constant only | ✅ CONFIRMED — `type IncidentKind int` at `engine/state.go:156`, with **two** constants today (`IncidentAction`, `IncidentCompensationStall`). ⚠ note `cloneState`'s comment asserts "Incident is a flat value struct (all fields are plain scalars)" — unaffected by adding a constant | ✅ CONFIRMED | `sed -n '151,180p' engine/state.go` |
| 28 | premise-evidence §2a: "⚠ **FALSE ENUMERATION IN SHIPPED CODE** — `engine/step_nodes.go:1135-1136` says 'the third of the three compensation dispatch sites'" | still false in shipped code | **ALREADY FIXED on `main`.** `396b9505` (ADR-0177/0178/0180) rewrote it to "one of the **four** compensation dispatch sites … : beginCompensation, stepCompensationAdvance, retryStalledCompensation and this one" (`step_nodes.go:1140-1144`). The bundle carries a live defect report for a defect that no longer exists | ❌ STALE | `git show 12c9d7e3:engine/step_nodes.go \| sed -n '1135,1138p'` vs `sed -n '1140,1144p' engine/step_nodes.go`; `git log --oneline 12c9d7e3..954c2a05 -- engine/step_nodes.go` |
| 29 | premise-evidence §2a: "`armCompensationStallTimer` is called at **four** sites — `step_compensation.go:415`, `:577`, `:1302`, and `step_nodes.go:1139`" | 4 sites at those lines | **4 sites ✅**, but `step_nodes.go:1139` → **`:1145`** at HEAD (the fix in row 28 added a comment line). The three `step_compensation.go` lines are unchanged | ⚠️ IMPRECISE (count right, one line stale) | `grep -rn armCompensationStallTimer engine/*.go \| grep -v _test` |
| 30 | premise-evidence §2a table: dispatch site 4 at `step_nodes.go:1134`; `consumeDispatchedRecord` at `:1132`; inherited lens-c C1 cites `ActiveCmdID =` at `step_nodes.go:1126` | those lines | HEAD: `compensationInvoke` at **:1139**, `consumeDispatchedRecord` at **:1137**, `cursor.ActiveCmdID = cmdID` at **:1131**. `startCompensationWalk` decl 1117 → **1122** | ❌ STALE | `grep -rn compensationInvoke engine/step_nodes.go`; `sed -n '1122,1145p' engine/step_nodes.go` |
| 31 | premise-evidence §2b: "`beginCompensation` — **4** call sites" (`step_errors.go:268`, `step_triggers.go:232`, `step_compensation.go:235`, `:1090`) | 4 | **4 confirmed**, but `step_triggers.go:232` → **`:278`** at HEAD; the other three lines are unchanged | ⚠️ IMPRECISE (count right, one line stale) | `grep -rn "beginCompensation(" engine/*.go \| grep -v _test` |
| 32 | premise-evidence §2b: "`startCompensationWalk` — **2** call sites (`step_nodes.go:1174`, `:1222`)" | 2 | **2 confirmed**; lines now **:1180** and **:1228** | ⚠️ IMPRECISE (count right, lines stale) | same grep |
| 33 | premise-evidence §2c: "**3 routes** into `stepCompensationAdvance`" (`step_triggers.go:85`, `:293`, `step_compensation.go:1262`) | 3 | **3 confirmed**; lines now `step_triggers.go:110`, `:339`, `step_compensation.go:1262` (last unchanged) | ⚠️ IMPRECISE (count right, 2 lines stale) | `grep -rn "stepCompensationAdvance(" engine/*.go \| grep -v _test` |
| 34 | premise-evidence §2d: "`ErrTokenNotFound` … gives **9** `return` sites"; the two compensation-reachable ones at `step_triggers.go:94` and `:302` | 9 total, 2 relevant | **9 confirmed** (`step_triggers.go:` 119, 348, 468, 499, 521, 768, 781, 1088, 1123). The two relevant ones are now **:119** and **:348**; the "other 7" are cited as `:422 :453 :475 :679 :692 :999 :1034` — **all seven stale** | ⚠️ IMPRECISE (count right, all 9 lines stale) | `grep -rn ErrTokenNotFound engine/*.go \| grep -v _test` |
| 35 | spec §1: "Executed across **four walk shapes** — cancel-started, scope-wide throw, targeted throw, and **with an explicit retry policy**" | 4 shapes | **THREE shapes, not four.** The premise-evidence's own headings are Scenario A (cancel), B (scope-wide throw), **C ("does an explicit retry policy change anything?" — a re-run of A's shape, not a shape)**, D (targeted throw). The engine has **FIVE** `walkMode`s (`walkAdmin`, `walkThrowTargeted`, `walkThrowScopeWide`, `walkPartial`, `walkReverse`); **`walkPartial` and `walkReverse` were never measured** — and they are exactly the two whose `walkTerminates` results differ | ❌ WRONG | `grep -n "^### Scenario" docs/specs/2026-08-13-adr-0179-premise-evidence.md`; `sed -n '208,227p' engine/state_compensation.go` |
| 36 | premise-evidence §6 table: "**A compensation walk holds ZERO tokens.** Measured `tokens=0` at every frame of Scenarios A, B **and D**" | hedged to 3 named scenarios | the hedged form is honest about its coverage — but the ADR and spec **restated it as "measured every frame"** with no scenario list, and row 14 shows the general claim is false. Classic inherited-claim-restated-as-fact | ⚠️ IMPRECISE at source, ❌ WRONG as restated | `sed -n '478p' docs/specs/2026-08-13-adr-0179-premise-evidence.md` vs ADR line 33 / spec line 46 |
| 37 | spec §11 header: "Bundle A's audit landed 4 of its **6 Criticals**" | 6 Criticals | **6 confirmed**: lens-a FINDING 1, lens-b B1/B2/B3/B7/B10, lens-c 0. Of those, B1/B2/B3/B7 = **4** land on ADR-0179 | ✅ CONFIRMED | `grep -n "CRITICAL" docs/specs/2026-08-13-adr-0179-inherited-audit-lens-{a,b,c}.md` |
| 38 | spec §11: "~12 of its **27 findings**" | 27 total | **not derivable.** The three lens files carry **7 + 13 + 47 = 67** numbered items (lens-c is a 47-row verification table of which only **7** are ❌ and 0 are ⚠️). No arithmetic on the committed records yields 27 | ⚠️ IMPRECISE (unverifiable) | `grep -c "^## FINDING" …lens-a.md` → 7; `grep -c "^### B[0-9]" …lens-b.md` → 13; `grep -c "^| C[0-9]" …lens-c.md` → 47; `grep -c "❌" …lens-c.md` → 7 |
| 39 | ⚠⚠ **The inherited audit ALREADY prescribed the fix for row 15, and the rewrite took only half of it.** lens-a FINDING 4: *"the sentence justifying it is a **false universal** and must be rewritten to name the closed set"* | fix = fixture + quantifier | The rewrite adopted the **fixture** half (spec §4 test 12 and plan step 14 both say "use a **cancel-started** walk"), and **dropped the quantifier** half — restating "refuses **every** compensation retry" in the ADR, the spec §3.11, the plan ▶Progress and plan Trap 6. Four restatements of a claim the inherited audit measured false | ❌ WRONG | `sed -n '109,146p' docs/specs/2026-08-13-adr-0179-inherited-audit-lens-a.md` |
| 40 | spec §3.11 (last bullet): "An instance cancelled while a retry backoff is pending receives **ADR-0180's new 409**" | 409 | **422.** ADR-0180 was **amended in-bundle**: *"⚠ Amended in-bundle — **422, not 409**. `service.ErrConflict` and `engine.ErrInvalidTransition` both classify to 422 (`transport/http/httpcore/errors.go:48`); **409 is `kernel.ErrConcurrentUpdate` alone** (`:34`). The pre-implementation text said 409 in three places."* Source-verified: `errors.go:48` → `StatusUnprocessableEntity`, `errors.go:34` → `StatusConflict`. ⚠ ADR-0180's own line 160 is the **un-amended fourth** occurrence — which is exactly where ADR-0179 inherited it from | ❌ WRONG (inherited) | `sed -n '84,88p;158,162p' docs/adr/0180-*.md`; `sed -n '28,50p' transport/http/httpcore/errors.go` |
| 41 | spec §2 item 4: a late reply returns `ErrTokenNotFound`, "which wraps `ErrInvalidTransition` → **HTTP 422**" | 422 | ✅ CONFIRMED — `engine/errors.go:23` wraps `ErrInvalidTransition`; `httpcore/errors.go:48` maps it to `StatusUnprocessableEntity` | ✅ CONFIRMED | as above |
| 42 | spec §3.11: "Persistence is whole-state `json.Marshal` with **no `DisallowUnknownFields`**" | none | ✅ CONFIRMED — `internal/persistence/store/store_core.go:78/216` `json.Marshal(capHistory(step.State, …))`, `:164` plain `json.Unmarshal(snap, &stateOut)`. The repo's only `DisallowUnknownFields` uses are `runtime/kernel/cursorcodec.go:45` (pagination cursors) and `definition/model/node_wire.go` — neither on the instance snapshot | ✅ CONFIRMED | `grep -rn DisallowUnknownFields --include='*.go' .`; `sed -n '74,82p;160,168p' internal/persistence/store/store_core.go` |
| 43 | ADR Decision, "Rejected" para: "`abandonCompensationWalk` is refused on a **resuming** walk, so a parked *throw* walk would have only `retry` and `skip`" | throw refused | ✅ CONFIRMED, and **stronger than stated**: `abandonCompensationWalk` accepts `walkMode() == walkAdmin` **only** (`engine/step_compensation.go:1313`, else `ErrCompensationWalkResumes`). So `walkPartial` and `walkReverse` are refused too — `walkReverse` despite `walkTerminates` reporting it as terminating | ⚠️ IMPRECISE (understates) | `sed -n '1305,1320p' engine/step_compensation.go` |
| 44 | spec §1: "`consumeDispatchedRecord` is a no-op unless the cursor carries an `ArchiveKey` or a live teardown window — record loss is **targeted-throw-only**" | one shape | **the recap contradicts the clause it compresses.** The body (`state_compensation.go:573-587`) has **two** live paths: `ArchiveKey != ""` (= `walkThrowTargeted`) **and** the `TeardownArchiveKey` window — and `TeardownArchiveKey` is stamped by `partitionForLiveWalk`, documented as "a **scope-wide** compensation throw walk that is draining that very scope" (`state_compensation.go:357`, `:417-419`). So record loss is targeted-throw **or torn-down scope-wide throw** | ❌ WRONG (recap) | `sed -n '/func (s \*InstanceState) consumeDispatchedRecord/,/^}/p' engine/state_compensation.go`; `grep -rn "TeardownArchiveKey =" engine/*.go` |
| 45 | ADR §3.3 / spec §3.3: a leaked retry timer fires against a zeroed cursor, "the shape ADR-0171 documents as having **panicked in the pure core in a consumer's process**" | that claim | ✅ CONFIRMED — `docs/adr/0171-*.md:34` `panic: runtime error: index out of range [0] with length 0`, `:38` "**This is a panic in the pure engine core**", `:232` "the panic ran in the consumer's process" | ✅ CONFIRMED | `grep -rn panic docs/adr/0171*.md` |
| 46 | plan "Execution order": "**Two phases**, ordered not parallel" | 2 | the table immediately below has **THREE** rows: P1 `engine`, P2 `runtime`, P3 doc-only. The plan then has three `## P1/P2/P3` sections | ❌ WRONG | `sed -n '29,40p' docs/plans/2026-08-13-*.md` |
| 47 | plan ▶ Progress: "off `main`; **newest code on `main` is the ADR-0176 merge `52bf0f80`**"; spec header "**Base**: `main` at the ADR-0176 merge `52bf0f80`" | main @ 0176 | **stale by two deliveries.** The bundle commit's actual base is `12c9d7e3` (3 docs-only commits after `52bf0f80`, so true at authoring). `main` is now `954c2a05`, carrying `a5b33e4c` (ADR-0177/0178/0180) and `1ac140f6` (ADR-0181/0182) | ❌ STALE | `git log --oneline 52bf0f80..12c9d7e3` → 3 docs commits; `git log --oneline -5 954c2a05` |
| 48 | ADR/spec/plan: "Closes backlog **16** and **3g**" / "`HANDOVER.md` ▶ NEXT WORK item **3**" | those items | ✅ CONFIRMED against current `HANDOVER.md`: `:51` "**C** — NEXT WORK item 3. Retry + visibility for a failed compensation action (backlog 16, 3g)"; `:116` "⚠ Items **16, 3g** are addressed by in-flight bundle C" | ✅ CONFIRMED | `grep -n "3g\|NEXT WORK" docs/plans/HANDOVER.md` |
| 49 | ⚠⚠ **spec §4 prescribes 12 tests; the plan prescribes 12 — but the SETS DIFFER.** | 1:1 mapping implied | Mapping spec§4 → plan: 1→P1.1, 2→P1.3, 3→P1.5, 4→P1.10, 5→P2.1, 6→P1.13, 7→P1.7, 8→P1.9, 9→P1.11, 11→P1.6, 12→P1.14. **spec §4 test 10 — "Late reply to a superseded command is benign, both reply kinds, including a superseded retry command (§3.6). Fails today: measured 422 for both" — has NO plan step.** The plan builds the predicate (P1.4 GREEN) and tests only that the ACTIVE id is not swallowed (P1.3), never that a genuinely superseded id is answered benignly. That is **backlog 3g itself — the ADR's Decision item 4**. Conversely plan P1.12 (exhaustion skips and continues) is a test **not** in spec §4's list, which is what makes the two totals coincidentally match | ❌ WRONG | line-by-line of `docs/specs/…:216-235` against `docs/plans/…:41-77` |
| 50 | plan "Traps (**all confirmed by the inherited audit**)" — 7 traps | all 7 confirmed | Traps 1-4 ← lens-b B1/B2/B3/B7 ✅; Trap 5 ← lens-a FINDING 5 ✅; Trap 7 ← lens-a FINDING 4 ✅. **Trap 6 ("`walkScoped()` not extended → **every** compensation retry refused, silently") is the one claim the inherited audit REFUTED** — FINDING 4 measured `SpawnsNewWork=TRUE` on a throw walk and demanded the universal be "rewritten to name the closed set". So "all confirmed" is false for 1 of 7 | ❌ WRONG | rows 15/39; `sed -n '109,146p' …lens-a.md` |
| 51 | premise-evidence §5/§6 header claim (restated in ADR): "`retryStalledCompensation` … (a) retires **stall** incidents on **both** branches; (b) arms `armCompensationStallTimer`; (c) has no policy, no counter, no exhaustion branch" | 3 defects | ✅ CONFIRMED — `retryStalledCompensation` (`step_compensation.go:1273`) calls `retireCompensationStallIncidents` at **:1287 and :1294** (two branches ✅), `armCompensationStallTimer` at **:1302** ✅, and contains no `RetryPolicy`/attempt/exhaustion reference | ✅ CONFIRMED | `sed -n '1273,1310p' engine/step_compensation.go` |
| 52 | ADR/spec: `IncidentCompensationFailed` in "ADR-0175's walk-scoped shape (`TokenID: ""`, keyed by `CommandID`)" survives `endInstance` | survives | ✅ mechanically true — `removeOrphanedIncidents` keeps `TokenID == ""` and `retireCompensationStallIncidents` filters `Kind == IncidentCompensationStall`. ⚠ **but the bundle enumerates only TWO positional `Incidents[0]` consumers** (`runtime/outbox.go:83`, `runtime/processdriver_action.go:33`). A **third** exists in a shipped module-root package: `processtest/park.go:309` `incidentNode()` returns `state.Incidents[0].NodeID`, and a walk-scoped incident carries no token/node the way an action incident does. `examples/scenarios/admin_monitoring/main.go:248/250` is a fourth (example only) | ⚠️ IMPRECISE / incomplete enumeration | `grep -rn "Incidents\[0\]" . --include='*.go' \| grep -v _test` → 6 hits: the 2 runtime + processtest/park.go:309 + 2 example lines + comments |
| 53 | premise-evidence §4: "`ErrTokenNotFound` wraps `ErrInvalidTransition` … and **per `engine/step_triggers.go:667`** that maps to HTTP 422" | that citation | conclusion (422) ✅ but the **citation is wrong in kind**: base `step_triggers.go:667` is a prose comment about the **human-task** ghost-id route ("→ ErrInvalidTransition → 422"), not the mapping and not the compensation route. The real mapping is `transport/http/httpcore/errors.go:48`. The comment is now at `step_triggers.go:756` | ⚠️ IMPRECISE + ❌ STALE | `git show 12c9d7e3:engine/step_triggers.go \| sed -n '660,670p'`; `grep -n "→ 422" engine/step_triggers.go` |
| 54 | premise-evidence §4: the late-reply 422 holds because "(`tokens=0` in every probe frame) — so `tok == nil`" | reason = zero tokens | **conclusion survives, reason does not.** Row 14 shows a walk can hold live sibling tokens; `s.tokenAwaiting(cmdID)` still returns nil for a superseded *compensation* id because the sibling awaits a different command. The 422 is real; the stated justification is the same false universal | ⚠️ IMPRECISE | rows 14 + 34 |

---

## ❌ Rows requiring a fix, with a concrete fix for each

**C-1 (CRITICAL, row 14) — "a compensation walk holds ZERO tokens (measured every frame)" is FALSE.**
Executed: a scope-wide throw with a parallel sibling holds 1 token at every frame while the walk is
in flight, and `engine/step_nodes.go:1115-1116` says so in shipped code ("sibling branches keep
running while the walk is in flight"). The premise-evidence hedged correctly ("Scenarios A, B and
D"); the ADR and spec stripped the hedge.
**Fix**: replace both occurrences (ADR Context ¶4, spec §1 last ¶) with the closed set —
*"A compensation walk has no token OF ITS OWN: the walk's in-flight record is not held by any
token, so `Token.RetryAttempts`/`RetryStartedAt`/`TokenIncident` cannot carry its attempt state.
The instance may still hold unrelated tokens — measured `tokens=1` on a scope-wide throw with a
parallel sibling (`scopeWideDrainDef`), `tokens=0` on cancel-started, sequential-throw and
targeted-throw walks (Scenarios A, B, D)."* The design conclusion (state lives on
`compensationCursor`) is unaffected and should be kept.

**C-2 (CRITICAL, rows 15 + 39 + 50) — "ADR-0178's guard refuses EVERY compensation retry" is FALSE, and the inherited audit already said so.**
The guard `!rec.Kind.walkScoped() && !s.spawnsNewWork()` bites only when `spawnsNewWork()` is
false, i.e. on a **terminating** walk. Measured `spawnsNewWork=true` on every frame of a throw
walk. lens-a FINDING 4 demanded exactly this rewrite; the rewrite took the fixture half and
dropped the quantifier half, restating the universal in **four** places (ADR Consequences bullet 6,
spec §3.11 bullet 1, plan ▶Progress, plan Trap 6).
**Fix**: in all four, substitute — *"Without this edit, ADR-0178's guard refuses a compensation
retry timer on every **terminating** walk (`walkAdmin`, `walkReverse`, and any throw/partial walk
carrying `PendingCancel`) — measured `SpawnsNewWork=false` — and, worse, `removeTimer`s the record
and emits `CancelTimer`, discarding the retry silently. A **resuming** walk (throw or partial
rollback without a pending cancel) measures `SpawnsNewWork=true` and would retry fine. The edit is
still required: the terminating walks are the ones a compensation retry most needs."*
Then drop "all confirmed by the inherited audit" from the plan's Traps heading, or exempt Trap 6.

**C-3 (MAJOR, row 49) — the plan drops the test for spec §4 item 10, which IS backlog 3g.**
"Late reply to a superseded command is benign, both reply kinds, **including a superseded retry
command**" has no step in plan P1 or P2. P1.3/P1.4 only cover the *active* id. The two totals both
read "12" because plan P1.12 (exhaustion) is a test the spec never listed.
**Fix**: insert a P1 step between 4 and 5 — *"**RED/GREEN** —
`TestLateReplyToASupersededCompensationCommandIsBenign`: table over `ActionCompleted` and
`ActionFailed`, each replaying a command id that `DispatchedCmdIDs` holds and `ActiveCmdID` does
not, asserting `err == nil` and an unchanged cursor. **Fails today**: both return
`ErrTokenNotFound` → 422 (source-verified `engine/errors.go:23` + `httpcore/errors.go:48`). Re-run
the case with a superseded **retry** command id after step 8."* And add P1.12 to spec §4's list.

**C-4 (MAJOR, row 40) — "ADR-0180's new 409" is 422.**
ADR-0180 was amended in-bundle to 422; its own line 160 is the un-amended occurrence ADR-0179
inherited.
**Fix**: change spec §3.11's last bullet to "ADR-0180's `ErrCancelNotApplicable` → **422**", and
separately correct `docs/adr/0180-*.md:160` (a one-word docs fix on `main`, outside this bundle).

**C-5 (MAJOR, row 35) — "four walk shapes" is three shapes plus a policy variant, and omits two of the engine's five walk modes.**
`walkPartial` and `walkReverse` were never measured, and they are precisely the two whose
`walkTerminates` verdict differs from the measured shapes (partial resumes; reverse terminates
despite resuming).
**Fix**: spec §1 → *"Executed across **three** walk shapes — cancel-started (`walkAdmin`),
scope-wide throw and targeted throw — plus a fourth run of the cancel shape with an explicit retry
policy. Two of the engine's five `walkMode`s, `walkPartial` and `walkReverse`, were **not**
measured; the retry design must state its behaviour for them explicitly (both reach
`stepCompensationAdvance`, so no behaviour is expected to differ, but that is an ASSUMPTION
(unverified))."*

**C-6 (MAJOR, row 44) — "record loss is targeted-throw-only" contradicts the clause it summarises.**
`consumeDispatchedRecord` has two live paths; the second (`TeardownArchiveKey`) is stamped by
`partitionForLiveWalk` for a **scope-wide** throw draining its own scope.
**Fix**: spec §1 → "record loss happens on a targeted throw (`ArchiveKey`) **and** on a scope-wide
throw whose scope was torn down mid-walk (`TeardownArchiveKey` window, ADR-0173)".

**C-7 (MAJOR, row 28) — the premise-evidence reports a shipped-code false comment that ADR-0177/0178/0180 already fixed.**
`engine/step_nodes.go` now reads "one of the **four** compensation dispatch sites … : beginCompensation,
stepCompensationAdvance, retryStalledCompensation and this one".
**Fix**: mark premise-evidence §2a's "⚠ FALSE ENUMERATION IN SHIPPED CODE" block as **RESOLVED by
`396b9505`**, and note that this ADR's fifth site will falsify the *corrected* comment too — so add
it to plan P3's doc-only sweep alongside `engine/command.go:66`.

**C-8 (MAJOR, rows 1, 2, 7, 30-34, 47, 53) — every line-number citation authored against `12c9d7e3` has rotted.**
Confirmed stale: `step_triggers.go:292-294` → **338-340**; `:316` → **362**; `:85` → **110**;
`:293` → **339**; `:94` → **119**; `:302` → **348**; `:232` → **278**; `:667` → **756**;
`state.go:475` → **516**; `step_nodes.go:1134` → **1139**, `:1132` → **1137**, `:1126` → **1131**,
`:1139` → **1145**, `:1174/:1222` → **1180/1228**; ErrTokenNotFound's "other 7"
(`:422 :453 :475 :679 :692 :999 :1034`) → **468, 499, 521, 768, 781, 1088, 1123**.
Still exact: `step_compensation.go:{412,503,524,574,1287,1294,1301,1345}`, `step_state.go:361`,
`step_test.go:94`, `command.go:66`, `runtime/outbox.go:81`, `runtime/processdriver_action.go:31`.
**Fix**: worth one pass replacing line citations with **symbol names** (the repo's own lesson,
[[audited-bundle-decays-when-base-moves]]), and updating spec/plan "Base" to `954c2a05` /
"newest code on `main` is the ADR-0181/0182 merge `1ac140f6`".

**C-9 (MINOR, row 46) — plan says "Two phases", table lists three (P1/P2/P3).**
**Fix**: "Three phases".

**C-10 (MINOR, row 52) — the `Incidents[0]` positional-reader enumeration is two, and there are more.**
`processtest/park.go:309` (`incidentNode`) is a third positional reader, in a **shipped module-root
package**; `examples/scenarios/admin_monitoring/main.go:248/250` is a fourth.
**Fix**: state the closed set in spec §3.4 — "two **cause-of-death** publishers in `runtime`, plus
`processtest.incidentNode`, which reads `Incidents[0].NodeID` for park classification and is out of
scope because it reports a node, not a cause of death (ASSUMPTION: harmless; a walk-scoped incident
carries `NodeID` of the compensating node)". Or verify and test it.

**C-11 (MINOR, rows 12, 26, 38, 43) — imprecise restatements.**
`removeOrphanedIncidents`'s predicate is `TokenID != "" && tokenByID(TokenID) == nil`, not
`TokenID != ""`. The "plain scalars / value-copyable" comment is scoped to the three
`TeardownArchive*` fields, not to the cursor generally. "~12 of its 27 findings" is not derivable
from the committed lens records (7 + 13 + 47 items; 7 ❌ rows in lens-c). `abandonCompensationWalk`
is refused on every non-`walkAdmin` walk, not only a throw walk.

---

## Counts that CHECKED OUT (no change needed)

`retireCompensationStallIncidents` = **5** sites · `compensationInvoke` = **4** production sites,
**5** after this ADR · dispatch sites ≡ `ActiveCmdID` assignment sites (**4**, exact) ·
`armCompensationStallTimer` = **4** · `beginCompensation` callers = **4** ·
`startCompensationWalk` callers = **2** · `stepCompensationAdvance` callers = **3** ·
`ErrTokenNotFound` returns = **9** · `endInstance` incident sweeps = **2** · `TimerKind`
constants = **5**, `command.go:66` names 4 · `IncidentKind` constants = **2**, this adds a 3rd ·
`walkScoped()` = stall kind only · `cancelCompensationStallTimers` = 2 strict loops, 2 call sites ·
ADR-0034 Decision 4 quoted **verbatim** · `grep -c slog` = **0** on both functions · bundle A's
**6** Criticals, **4** of them landing here · backlog **16**/**3g** and NEXT WORK item **3**.
