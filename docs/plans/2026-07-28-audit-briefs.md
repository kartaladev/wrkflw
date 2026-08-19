# Adversarial audit briefs — signal & message delivery correctness

**Why this file exists.** The rule-#9 adversarial audit for this bundle was
dispatched at `62ff37e` and **all three auditors were killed mid-run by a session
limit**, returning no findings. The bundle has therefore NOT cleared the gate.
These are the three briefs verbatim so a fresh session can re-dispatch them
without reconstructing anything.

**How to dispatch.** Three `Agent` calls in ONE message so they run concurrently.
`subagent_type: "general-purpose"`, `model: "opus"` (rule #9 requires Opus for
audit agents). Update the HEAD SHA in each brief if the branch has moved.

**After they return:** *adjudicate* the findings — do not auto-apply them. Fold
accepted fixes into the spec / ADRs / plan, record rejected ones with a reason,
then `--amend` the docs commit. Only then start Task 1.

---

## Brief 1 — source-verify every factual claim

```
You are an ADVERSARIAL AUDITOR. Your job is to ATTACK a design bundle, not summarize it. Findings only — no praise, no restatement.

Repo: /Users/zakyalvan/Documents/RND/wrkflw (Go workflow engine library, branch feat/durable-waiters-delivery-correctness, HEAD 62ff37e).

Documents under audit:
- docs/specs/2026-07-28-signal-message-delivery-correctness.md
- docs/adr/0155-durable-waiter-projection.md
- docs/adr/0156-unified-delivery-bus-and-message-semantics.md
- docs/adr/0157-undelivered-wakeup-channel.md
- docs/adr/0158-signal-fires-every-matching-arm.md
- docs/plans/2026-07-28-signal-message-delivery-correctness.md

YOUR LENS: **factual accuracy against the actual codebase.** Every factual claim in these documents must be source-verified. The documents cite file paths, line numbers, symbol names, type shapes, and existing behaviour extensively. Find every one that is WRONG, STALE, or UNVERIFIABLE.

Specifically hunt for:
1. Cited file:line references that point at something other than what is claimed. Open each one.
2. Named symbols that do not exist, or whose signature/shape differs from what the docs assume (e.g. does `kernel.NormalizeLimit` exist? `kernel.EncodeCursor`/`DecodeCursor`? `transaction.JoinOrBegin`? `isNilDep`? `dialect.Dialect.TimestampsAsText()` — is it a method or field? `timeArg`/`parseTimeText` signatures? `engine.NewSignalReceived`/`NewMessageReceived` signatures? `engine.Step` signature? `idgen.Generator.NewID()`?).
3. Claims about existing behaviour that are false (e.g. "perform() runs after the commit", "Pruner has no PruneInstances", "SignalBus.Publish has no CAS retry", "store.Store does not implement kernel.InstanceLister", "MemInstanceStore does implement it", "only wrkflw_journal has an FK").
4. The plan's code samples: would they COMPILE against the real codebase? Check field names on `armedEvent`, `boundaryArm`, `eventTriggeredSubprocessArm`, `InstanceState`, `Token`. Check that `armMatchable`/`matchPtr` generics are used correctly. Check `engine.Status` constant names. Check whether `s.tokenIDsAwaitingSignal`, `s.boundaryArmIDsBySignal` etc. exist or are being introduced.
5. MySQL/SQLite migration specifics the plan hand-waves: read internal/persistence/store/migrations/mysql/0001_init.sql and sqlite/0001_init.sql and state EXACTLY what column types and index constraints the new tables must use. MySQL cannot index unbounded TEXT — what does wrkflw_timers actually declare?
6. Test-helper claims: does the plan reference engine test fixtures that do not exist? List which of `twoHostsWithSignalBoundaries`, `activeNodeIDs`, `countTokensAt`, `hasArmedSignalBoundary`, `startAndPark`, `signalNameOf`, `fixedNow` exist in engine/*_test.go today, and what the real equivalents are called.
7. `internal/dbtest` API: verify the exact signatures the plan cites.

For EACH finding give: severity (Critical/Important/Minor), the exact document + section, the claim, the verified truth with file:line evidence, and a CONCRETE fix (the corrected text or code).

Be exhaustive and skeptical. A claim you cannot verify is itself a finding. Do not fix anything — report only.
```

> **Partially pre-answered.** The controller already verified items 2 and 6 and part
> of 5 — see the plan's "Findings folded so far" table (F1–F7). Tell this auditor to
> treat those as known and concentrate on 1, 3, and 4.

---

## Brief 2 — correctness holes, races, missing failure modes

```
You are an ADVERSARIAL AUDITOR. ATTACK this design bundle — do not summarize it. Findings only.

Repo: /Users/zakyalvan/Documents/RND/wrkflw (Go workflow engine shipped as a library; BPMN-inspired token state machine; Postgres/MySQL/SQLite behind one neutral store; branch feat/durable-waiters-delivery-correctness, HEAD 62ff37e).

Documents under audit:
- docs/specs/2026-07-28-signal-message-delivery-correctness.md
- docs/adr/0155-durable-waiter-projection.md (durable waiter table written in the commit tx)
- docs/adr/0156-unified-delivery-bus-and-message-semantics.md (one bus, 3 policies; message fan-out default; deliver AND start)
- docs/adr/0157-undelivered-wakeup-channel.md (dead-letter for inbound wake-ups + replay)
- docs/adr/0158-signal-fires-every-matching-arm.md (engine: fire all matching arms)
- docs/plans/2026-07-28-signal-message-delivery-correctness.md

YOUR LENS: **correctness holes, unstated assumptions, missing failure modes, and races.** Assume the design is wrong somewhere and find where.

Attack these specifically:

1. **Multi-replica claims.** The design asserts multi-replica safety. Is that actually achieved? Trace: replica A parks instance X (writes waiter rows in its commit tx). Replica B broadcasts. B reads the table, calls ApplyTrigger(X) → Load + Step + Commit with CAS. What races exist? What if A and B broadcast the SAME signal concurrently — does the instance get the signal TWICE? Is that correct BPMN? Is there any dedup? Consider engine idempotency of a repeated SignalReceived.

2. **Transaction interleaving.** ReplaceWaiters runs inside commitFn. What isolation level do the stores use? Can a concurrent reader see a half-written waiter set (DELETE committed, INSERT not)? Read internal/persistence/store/store_core.go and txrunner.go. What about the no-TxRunner degraded path — what exactly breaks?

3. **The deliver-AND-start decision (ADR-0156).** Hunt for scenarios where this is destructive beyond what the ADR admits. Consider: a long-running instance that awaits the same message repeatedly (a loop); a message-start on a definition the operator did not intend to be reachable; interaction with correlation keys that CHANGE over an instance's life. Quantify the keyless-non-singleton amplification with a concrete scenario.

4. **Fan-out default for messages.** Where does fan-out break an existing invariant? Consider ADR-0125's 1:1 contract, `wrkflw_processed_message` dedup, call-activity parent/child correlation, and chained instances. Does anything downstream assume exactly one recipient?

5. **ADR-0158 arm fan-out.** Attack the snapshot approach. Can firing arm 1 make arm 2's *identity* still resolvable but semantically wrong (e.g. the host token was consumed and a NEW token with the same id placed)? Can an interrupting event-sub-process fire, then a boundary arm from the cancelled scope still resolve? What about arms in DIFFERENT scopes? What is the correct ordering — is gateway→boundary→eventsub→token still right when all fire? Read engine/step_triggers.go, step_boundaries.go, step_eventsubprocess.go, step_gateways.go, state_arms.go.

6. **Replay (ADR-0157).** Replay reuses the stored OccurredAt. What if the instance has since advanced past the waiter and re-armed a DIFFERENT waiter on the same name? Does replay then fire something that should not fire? Is "idempotent no-op" actually true? What about replaying a wake-up for an instance that has since completed?

7. **Self-heal on ErrInstanceNotFound.** Deleting the waiter row on a not-found. Can this delete a LEGITIMATE row — e.g. a race where the instance is being created concurrently, or Load fails transiently with a wrapped not-found? Is ErrInstanceNotFound reliably distinguishable from a transient error?

8. **Ordering and determinism.** The docs promise ascending-instance-ID fan-out order. Does that survive paging, multi-replica, and partial failure? Is order actually meaningful/observable?

9. **Anything the bundle does NOT mention that it should** — migration/rollback story for existing deployments, observability (which metrics/traces), the `service` layer and HTTP transports (do they need to expose any of this? spec §4 lists no transport changes — is that right given there IS a transport surface for signals/messages?), authz on replay.

For EACH finding: severity (Critical/Important/Minor), the document+section it belongs to, the concrete failure scenario (specific inputs/state → wrong outcome), and a CONCRETE proposed fix. Prefer few high-quality findings over many shallow ones, but be exhaustive about Critical ones. Do not modify any file — report only.
```

> **Highest value of the three.** Nothing in this brief has been pre-answered. If
> only one auditor can be run, run this one.

---

## Brief 3 — plan executability and project-rule compliance

```
You are an ADVERSARIAL AUDITOR. ATTACK this implementation plan — do not summarize it. Findings only.

Repo: /Users/zakyalvan/Documents/RND/wrkflw (Go workflow engine library, branch feat/durable-waiters-delivery-correctness, HEAD 62ff37e).

PRIMARY DOCUMENT: docs/plans/2026-07-28-signal-message-delivery-correctness.md
Supporting: docs/specs/2026-07-28-signal-message-delivery-correctness.md and docs/adr/015{5,6,7,8}-*.md
Project rules: /Users/zakyalvan/Documents/RND/wrkflw/CLAUDE.md (read it in full — it is binding)
Project skills: .claude/skills/table-test/SKILL.md, .claude/skills/use-mockgen/SKILL.md, .claude/skills/use-testcontainers/SKILL.md (read all three — they OVERRIDE the generic Go testing skill)

YOUR LENS: **is this plan actually executable by a fresh session with zero transcript context, and does it satisfy the project's binding rules?**

Attack these specifically:

1. **Executability.** Walk each task as if you must implement it with no other context. Where does it say WHAT to do but not HOW? Where does a step reference a symbol/type/helper defined in no task? Where would an implementer have to guess? Flag every placeholder-in-disguise ("per spec §X", "mirror timerstore.go", "check how X is declared", ellipses in code, "/* ... */").

2. **CLAUDE.md compliance.** Check the plan against every binding rule: TDD Operational Discipline (is a visible RED state actually produced in every task before implementation? are any of the Forbidden Patterns present?); commit granularity (CLAUDE.md mandates ONE feature-bundle commit, no micro-commits, fold via --amend — but the plan has per-task commits: is the reconciliation in the Delivery Gate adequate or contradictory?); hot-path-first coverage; verification commands; the "verify via exit code not a pipeline" rule.

3. **Test strategy adequacy — the user explicitly asked for ALL hot paths.** Enumerate the hot paths of this change yourself from the spec and ADRs, then check each is covered by a named test case in the plan. Hot paths include at minimum: the projection on every commit, the in-tx waiter write, the delivery fan-out loop, CAS retry, self-heal, undelivered record, the SQL waiter lookup on every delivery, handleSignalReceived tiers 1-3, commit with and without TxRunner, RehydrateWaiters paging, replay. Which are MISSING or only shallowly covered? Which FAILURE BRANCHES are untested?

4. **table-test skill compliance.** The plan's test code must use the assert-closure form, testify require/assert, t.Context(), and a ctx modifier with a cancelled-context case for context-sensitive components. Find every test sketch that violates this. Note: the plan uses context.Background() in one concurrency test with a justification — is that justification sound?

5. **use-mockgen compliance.** //go:generate directives, --typed, --source mode, same-package placement, <file>_mock.go naming. Is the plan correct that mocks go in package kernel? Does that create an import cycle or a test-only dependency problem?

6. **use-testcontainers / dbtest compliance.** The plan cites dbtest helpers. Are the cited signatures right? Is one-container-per-test vs per-suite handled? Does adding ~2 new conformance suites × 3 dialects materially worsen the already-noted ~60s container-boot problem, and should the plan say something about that?

7. **Task decomposition and parallelism.** Are the declared dependencies correct? Tasks 5 and 6 are declared parallel-after-4 — do they actually touch disjoint files? Tasks 8b and 9 are INLINE because compile-breaking — is that boundary drawn correctly, and is anything else secretly compile-breaking (e.g. does Task 2 or 3 break anything)? Is Task 1 truly independent?

8. **Missing tasks.** What does the spec/ADRs require that NO task implements? Check especially: observability (metrics/traces for the new paths), the service/ layer and transport/http surfaces, godoc/examples for new public API, the `runtime/monitor` package, README updates, and whether `service.Service` needs to expose replay.

For EACH finding: severity (Critical/Important/Minor), exact task/step, what is wrong, and a CONCRETE fix (corrected command, corrected code, or the text of a missing task/step). Be exhaustive. Do not modify any file — report only.
```

> **Partially pre-answered.** Item 2's exit-code-through-a-pipe defect was found and
> fixed by the controller (17 occurrences). Items 5 and 6's mock-placement and
> `gomock.Cond` questions were verified sound. Tell this auditor to concentrate on
> 1, 3, 7, and 8 — item 3 (hot-path coverage) is the one the project owner called
> out explicitly.
