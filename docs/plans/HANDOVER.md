# wrkflw Engine Core — Execution Handover

This document lets a **fresh session with zero prior context** understand the state of `wrkflw`
and pick up the next work. Read it top to bottom before starting.

## ⏩ CURRENT RESUME POINT (read this FIRST — supersedes the dated blocks below) — updated 2026-06-29 (built-in service actions)

> **State:** work lives on branch **`feat/builtin-actions`** (HEAD `92c39fc`, base `main` `67e2c01`) — **NOT merged, NOT pushed** (maintainer chose "keep branch as-is" for review before integration). `go build ./...` clean, `go test ./...` 27 pkgs / 0 failures (incl. mailpit testcontainers integration), `golangci-lint run ./...` 0 issues. **engine/ + model/ are zero-diff vs main (verified).** **Next free ADR: 0076** (0074 + 0075 consumed this run). Ledger: `.superpowers/sdd/progress.md` (section "Built-in service actions — SDD progress ledger"). Spec: `docs/specs/2026-06-29-builtin-actions-design.md`. Plan: `docs/plans/2026-06-29-builtin-actions.md`.
>
> **✅ Built-in / template service-action catalog — DONE on branch (8 tasks, subagent-driven, each task-reviewed + final opus whole-branch review "Ready to merge", 0 Critical/Important).** Ships four public `action/*` subpackages + a retry contract, all on stdlib + the already-present `expr-lang` (NO new runtime dependency; mailpit is test-only):
> 1. **`action/retry.go`** (ADR-0074) — `Retryabler` interface, `NonRetryable(err)`, `IsRetryable(err)` (default true; honours `Retryabler` via `errors.As`). `runtime/runner.go:785` now passes `action.IsRetryable(err)` into the `ActionFailed` trigger instead of hardcoded `true` — the ONLY core change; engine/model untouched.
> 2. **`action/httpcall`** — `NewHTTPCall(opts...)`; `net/http`; options `WithBaseURL/Method/Header/HTTPClient/BodyKey/OutputKeys/URLExpr`; retry classification (4xx exc 408/429 → NonRetryable; 408/429/5xx/transport → retryable); `WithURLExpr` = expr URL interpolation; outputs `httpStatus/httpBody/httpHeaders`.
> 3. **`action/email`** — `NewEmail(opts...)`; `net/smtp` + `text/template` (missingkey=error); unexported `sender` seam + exported `SenderFunc`/`WithSender`; mailpit integration test. **`WithTLS`/`WithStartTLS` are intentional NO-OPS** (consumer wires TLS via a custom `SenderFunc`). **Security: header CRLF-injection guard** (`validateHeader` rejects `\r`/`\n` in rendered subject + from + each recipient → NonRetryable, sender never called).
> 4. **`action/transform`** — `NewTransform(opts...) (ServiceAction, error)` (eager expr compile, errors at wiring time); `Set(outKey, expr)`; later Sets can reference earlier OUTPUTS (chaining).
> 5. **`action/logaction`** — `NewLog(opts...)`; pass-through `slog` of selected vars; never errors (fire-and-forget safe).
> ADRs: **0074** (retry contract), **0075** (built-in catalog). Action coverage total 94.1% (all pkgs >85%).
>
> **🟢 NEXT SESSION — remaining (non-blocking) follow-ups for this feature:**
> 1. **`examples/` reference wiring** for the four actions into a process definition (ADR-0075 explicitly defers this; it is the main loose end).
> 2. Optional real-TLS email sender (implicit-TLS/STARTTLS) as a shipped `SenderFunc`, or drop the no-op `WithTLS`/`WithStartTLS` options to avoid the false affordance (maintainer taste call).
> 3. Cosmetic: transform int-identity test robustness (expr return type); httpcall "missing base URL" test spins an unused `httptest.Server`.
>
> **⚠️ INTEGRATION PENDING:** branch `feat/builtin-actions` is unmerged. Merge with `git checkout main && git merge --no-ff feat/builtin-actions` after review, then push. The backlog-follow-ups block below (local main ahead by 5, push held) is still also pending and independent of this branch.
>
> ---

## ⏩ PRIOR RESUME POINT — updated 2026-06-29 (backlog follow-ups: relay + tests)

> **State:** `main` HEAD `38fd3d0` (merge) — **NOT yet pushed to `origin/main`** (local main is ahead by 5 commits; push held for maintainer review). `go build ./...` clean, `golangci-lint run ./...` 0 issues, gofmt clean on touched dirs, working tree clean. **Next free ADR: 0074** (none consumed this run). Ledger: `.superpowers/sdd/progress.md` (section "Backlog follow-ups run — 2026-06-29").
>
> **✅ Four non-blocking backlog pickups — DONE & merged to local main (branch `fix/backlog-followups-relay-tests`, merge `38fd3d0`).** Executed via subagent-driven-development; each task individually reviewed (all Approved) + a final opus whole-branch review (Ready to merge, 0 Critical/Important).
> 1. **(`c51e973`) Postgres relay `Run` ctx-guard fix** — the documented bug: 3 guard sites changed `errors.Is(err, context.Canceled)` → `if ctx.Err() != nil` (parity with the already-fixed mysql relay), `errors` import removed. New `internal/persistence/postgres/relay_ctx_cancel_test.go` (2 deterministic tests, RED-proved against old guard). NOTE: pg timeout test needed `pg.Migrate` because `RunTestDatabase` (unlike `RunTestMySQL`) does NOT auto-migrate.
> 2. **(`ba5e8a7`) Relay span `wrkflw.batch_size` semantics** — now records the CLAIMED count `len(claims)` (not published); new `wrkflw.published_count` attr carries the published total. Applied **byte-identically to BOTH backends** (postgres+mysql `DrainOnce`). `DrainOnce` return value / metrics counter / debug log all stay published-based (return-value switch would infinite-loop `drainUntilEmpty`). New `internal/persistence/mysql/relay_observability_test.go` mirrors postgres (7 tests); poison-row test pins `batch_size=1, published_count=0`. **This changes the meaning of an internal span attr (not a consumer API) — no ADR; recorded here.**
> 3. **(`3bfb0b3`) MySQL facade test minors** — `TestNewMySQLLister_ListsInstances` tightened to `Len==2` + set-membership on both seeded IDs; new `TestNewMySQLAdvisoryLockOwnership_ClosedDBReturnsError` covers the `(nil,nil,err)` branch. Test-only.
> 4. **(`f658b76`) Builder fluent equivalence test widened** to all 19 `AddX` kinds (was 7) — table-test closure form, `reflect.DeepEqual` per kind that `AddX` ≡ `Add(NewX(...))`. `builder_fluent.go` stays 100%. Test-only.
>
> **🟢 NEXT SESSION — two OPTIONAL cosmetic follow-ups (confirmed-Minor by final review; non-blocking, sweep together if desired):**
> 1. Relay empty-drain early-return sets only `wrkflw.batch_size=0`; add `wrkflw.published_count=0` too (BOTH backends, keep symmetric) so the attribute set is consistent across drain paths. `postgres/relay.go` ~L464 / `mysql/relay.go` ~L391.
> 2. Happy-path span tests (`TestRelayBatchSpan` / `TestMySQLRelayBatchSpan`) assert `batch_size` but not `published_count==1` — add the assertion (the poison-row tests already pin both).
> 3. (Trivial) Task-4 equivalence table node-count mismatch uses `t.Errorf` not `t.Fatalf` (`model/builder_fluent_test.go`); `nodeByID` t.Fatals on a missing node anyway, so effectively moot.
>
> **⚠️ PUSH PENDING:** local `main` (`38fd3d0`) is ahead of `origin/main` (`c3772a8`) by 5 commits. Push with `git push origin main` after review. The older pre-pushed pickups below (postgres relay ctx bug, batch_size drift, mysql minors, builder widening) are now ALL RESOLVED by this run — disregard them in the older block.
>
> ---

## ⏩ PRIOR RESUME POINT — updated 2026-06-29 (ADR-0072 Option A + MySQL backend program)

> **State:** `main` HEAD `4802e37` — **pushed to `origin/main`** (in sync). Full `go test -race ./...` GREEN (zero failures), `golangci-lint run ./...` 0 issues, `go build ./...` clean, working tree clean. **Next free ADR: 0074.**
> Ledger: `.superpowers/sdd/progress.md`. Plan: `docs/plans/mysql-persistence-backend.md`. Spec: `docs/specs/2026-06-28-mysql-persistence-backend-design.md`. Builder-sugar spec/plan: `docs/specs/2026-06-29-builder-fluent-node-methods-design.md` / `docs/plans/builder-fluent-node-methods.md`.
>
> **🟢 NEXT SESSION — pending pickups (all non-blocking; nothing in flight):**
> 1. **Postgres relay `Run` ctx-cancellation bug** (clearest pickup): `internal/persistence/postgres/relay.go` `Run` loop only checks `errors.Is(err, context.Canceled)`, missing `context.DeadlineExceeded`/driver-wrapped cancels — change both guard sites to `if ctx.Err() != nil`. The MySQL relay already does this; Postgres was left untouched per the program's "don't modify postgres" constraint. Small, well-scoped fix (mirror commit `b504250`'s deterministic test approach).
> 2. Relay span attr `wrkflw.batch_size` records published-count not claimed-count (BOTH backends — fix both or neither to avoid drift).
> 3. MySQL minors: `TestNewMySQLLister_ListsInstances` facade test asserts `len>=2` not specific IDs; `NewMySQLAdvisoryLockOwnership` facade error path uncovered. Both trivial.
> 4. Builder-sugar test thoroughness: `TestBuilderFluentEquivalentToAdd` covers 7 of 19 kinds via `reflect.DeepEqual` (rest kind/option-checked; bodies are identical forwarding) — optional widening.
>
> Everything below this State block is historical context. The two big efforts of 2026-06-29 (MySQL backend ADR-0073 + ADR-0072 Option A + builder fluent methods + README docs) are DONE & merged & pushed.
>
> **✅ ADR-0072 Option A (multi-replica timer failover) — MERGED.** Elector now re-arms persisted timers on leadership *acquisition* (not just startup): internal `WithOnLeadershipAcquired(func(ctx))` on the elector fires async (wg-tracked, coalesced, goleak-clean) at the leadership transition; façade `scheduling.WithOnLeadershipAcquired` threads it through `WithTimerElector`; consumers wire it to `runtime.Runner.RehydrateTimers`. ADR-0072 flipped Proposed→Accepted. Option B (DB-claim scheduler) remains the recorded future target if load-distribution is needed. No engine/model/runtime changes.
>
> **✅ MySQL 8.0+ persistence backend (ADR-0073) — COMPLETE, all 5 phases merged.** New PARALLEL non-exported `internal/persistence/mysql/` (16 impl files) mirroring `internal/persistence/postgres/` behind the SAME `runtime.*` ports; PostgreSQL stays primary and is **provably zero-diff** (verified `git diff` over `internal/persistence/postgres/` + `internal/scheduling/gocron/elector.go`). Phase merges: P1 `b0ff42e` (deps `go-sql-driver/mysql`, goose MySQL migrations [2 files, 8 tables; journal col is `trigger_`], `database.RunTestMySQL` testcontainers helper [auto-migrates], `DBTX` seam, `Store` w/ optimistic CAS, `TimerStore`, facade `OpenMySQL`/`MigrateMySQL`/`NewMySQLTimerStore`); P2 `c3f22ec` (POLL-ONLY relay [no LISTEN/NOTIFY] + DLQ/redrive, `Deduper`, facade); P3 `47fd456` (`CallLinkStore` [SKIP LOCKED lease, no RETURNING], `Ownership` [GET_LOCK on dedicated `*sql.Conn`, SHA-256 64-char keys], `ChainLinkStore`, `Lister` [keyset + `JSON_LENGTH` incident count], facade + `NewMySQLCallNotifier`); P4 `5557e10` (`DefinitionStore` [rich polymorphic-Node JSON round-trip verified], `Pruner`, MySQL health `PingCheck`, facade); P5 `ddd24d7` (`MySQLElector` [GET_LOCK, heartbeat step-down, Option-A hook, unconditional RELEASE_ALL_LOCKS], `scheduling.WithMySQLTimerElector` [3-way mutual exclusion], `examples/mysql_wiring`). Final hygiene `7b44f10`.
>
> **MySQL dialect decisions (for future maintainers):** CAS via `RowsAffected()==0` + deadlock `1213`/lock-wait `1205`→`runtime.ErrConcurrentUpdate`, dup `1062`→`ErrInstanceExists`; `ON DUPLICATE KEY UPDATE` upserts; `SELECT … FOR UPDATE SKIP LOCKED` + follow-up `UPDATE` in place of `RETURNING`; `GET_LOCK`/`RELEASE_ALL_LOCKS` session locks (dedicated conn, never the pool); `JSON`/`DATETIME(6)`/`BIGINT AUTO_INCREMENT`; `LIMIT %d`-of-int (MySQL 8 rejects `?` LIMIT alongside locking clauses) — safe, int-only. `MySQLDeduper` is a SEPARATE facade interface (postgres `Deduper.Seen` takes `pgx.Tx`, incompatible with `*sql.Tx`); all other MySQL ctors return the SAME `runtime.*`/facade interface types as postgres. Facade lives in `persistence/mysql.go` (`MySQL*` ctors + `MySQLOption`/`MySQLRelayOption`/`MySQLCallLinkOption` aliases) — postgres `persistence.go` untouched. Final whole-branch opus review: READY TO MERGE, 0 Critical/Important.
>
> **Backlog follow-ups (Minor, non-blocking, from MySQL reviews):**
> - **Postgres relay `Run` loop shares a latent ctx bug**: it only checks `errors.Is(err, context.Canceled)`, missing `context.DeadlineExceeded`/driver-wrapped cancels. The MySQL relay was fixed (`ctx.Err() != nil`); postgres was left untouched per the program's "don't modify postgres" constraint. **Queue a separate postgres fix.**
> - Relay span attr `wrkflw.batch_size` records published-count not claimed-count (both backends mirror each other — fixing only mysql would create drift; fix both or neither).
> - `TestNewMySQLLister_ListsInstances` facade test asserts `len>=2` not specific IDs (internal Lister test is strong; facade is wiring-only).
> - `NewMySQLAdvisoryLockOwnership` facade error path uncovered (trivial delegation).
>
> **✅ Builder fluent per-node-type methods — DONE (merged `e4e7be5`).** 19 `AddX` methods on `model.DefinitionBuilder` (`AddStartEvent`/`AddServiceTask`/`AddUserTask`/… — named `AddUserTask` to mirror `NewUserTask`/`KindUserTask`, NOT AddHumanTask) forwarding 1:1 to `Add(NewX(...))`; generic `Add` retained; additive-only (no constructor/validation/YAML changes); `model/builder_fluent.go` 100% covered. Spec `docs/specs/2026-06-29-builder-fluent-node-methods-design.md`, plan `docs/plans/builder-fluent-node-methods.md`. Review minors (non-blocking): equivalence test covers 7 of 19 kinds via reflect.DeepEqual (rest are kind/option-checked; bodies are identical forwarding).

## ⏩ PRIOR RESUME POINT — updated 2026-06-27 (autonomous backlog-completion program)

> **State:** `main` HEAD `e92844f` (pushed to `origin`). Full build/vet/lint/gofmt/config-verify clean.
> **Next free ADR: 0073.** The autonomous backlog-completion program of 2026-06-27 is **✅ COMPLETE** —
> index + triage in `docs/plans/2026-06-27-backlog-completion-program.md`, ledger in
> `.superpowers/sdd/progress.md`. All 7 implementation tracks merged + the T2 proposal written:
>
> - **T1 (ADR-0068)** snapshot action metadata + gRPC `GetInstanceSnapshot`/`GetActionableView` RPCs.
> - **H1** gofmt enforced via golangci-lint v2 `formatters` + migrated config to valid v2 schema.
> - **H2** service coverage 84.7%→91.8% (covered `GetInstanceWithDefinition`); dropped a redundant test.
> - **H3 (ADR-0069)** gocron/clockwork clock seam optional via `WithSchedulerClock`/`WithClock` (BREAKING ctor sig).
> - **L1 (ADR-0070)** observability: CallNotifier `wrkflw.callnotifier.batch` span, relay + REST meters now emit, route-template REST span naming.
> - **P1 (ADR-0071)** ENGINE FIX: serialize concurrent compensation throws (`DeferredCompensationThrows` queue) — fixes the Macro-mode parallel-branch cursor overwrite. `recordCompensation` dedup investigated → not reproducible, left untouched.
> - **P2** hardened flaky `TestListenLoopExitsOnContextCancellation` via a test-only `listenReady` signal (no more sleep).
> - **T2 (ADR-0072 _Proposed_, NOT implemented)** multi-replica timer exclusivity — `docs/specs/2026-06-27-multi-replica-timer-exclusivity-proposal.md`. **Awaiting maintainer decision** (3 questions in the proposal); double-fire is already CAS-safe so this is an optimization. SKIPPED entirely (need approval): broker constructors, streaming/grpc-gateway, casbin ABAC/FilteredAdapter, CI.
>
> **Process note:** P2 (`4471882`) was committed directly to `main` rather than via a feature branch (a branch-discipline slip) — code is verified green, but flagging it for transparency.
>
> **New Minor follow-ups queued from this program's reviews (all non-blocking):**
> - T1: `runtime.NewInstanceSnapshot`/`NewActionableView` alias engine `Candidates`/`Payload` into the DTO (pre-existing; defensive-copy pass worth doing); `snapshotToProto` `toStruct` error branches uncovered (75.8%).
> - L1: relay `wrkflw_relay_batch_duration_seconds` not recorded on error/empty drains (move to `defer`); REST `statusRecorder` records last (not first) `WriteHeader` and does not forward `http.Flusher`/`Hijacker`/`Pusher` (note before adding any SSE/streaming/WebSocket endpoint).
> - P1: a parked token with empty `AwaitCommand` is matchable by `tokenAwaiting("")` (pre-existing across all empty-AwaitCommand parks); consider rejecting empty `CommandID` in `handleActionCompleted`/`handleActionFailed`. `FuzzStep` generator can't synthesize `CompensateRef` nodes (serialize path covered by dedicated tests instead).
>
> **✅ Merged 2026-06-27 — T1: snapshot action metadata + gRPC snapshot RPCs (ADR-0068).**
> `runtime.InstanceSnapshot` now carries `ScopedActions []string` + `ActionBindings []ActionBindingView`
> (`{NodeID,NodeKind,Action,Inline}`), populated by `NewInstanceSnapshot` from the definition (the
> formerly-ignored `def` param is now used). `model.ProcessDefinition.ScopedActionNames()` added (additive;
> engine untouched). New gRPC RPCs `GetInstanceSnapshot`/`GetActionableView` mirror the REST endpoints with
> full proto projections. Whole-branch opus review: Ready to merge (0 Critical/Important).
>
> **Triage for the rest of the program:** T2 (multi-replica timer exclusivity) is DEFERRED to spec/proposal
> only — major architectural (distributed failover scheduler); double-fire is already correct via the engine
> CAS so it is an optimization, needs explicit approval. SKIPPED (need approval): broker constructors
> (Kafka/NATS/SNS), streaming/grpc-gateway, casbin ABAC/FilteredAdapter, and the CI pipeline.
>
> **New follow-ups queued (Minor, from T1 review):** (a) `runtime.NewInstanceSnapshot`/`NewActionableView`
> still alias engine `Candidates`/token `Payload` into the DTO (pre-existing; harmless over the wire, but a
> direct library consumer could observe tearing — defensive-copy pass worth doing); (b) `snapshotToProto`
> `toStruct` error branches uncovered (75.8%) — one non-JSON-var test closes it.

## ⏩ PRIOR RESUME POINT — updated 2026-06-26 (transactional SendTask outbox)

> **State:** `main` HEAD `0b4da71` (pushed to `origin`), full suite green (`go test -race ./...`), golangci-lint 0, gofmt clean, `engine/`+`model/` zero-diff. **Next free ADR: 0068.** The ADR-0067 async-e2e follow-up is **DONE** (`eventing/message_e2e_test.go` — real Postgres+relay → `NewMessageHandler` → `DeliverMessage` resumes a parked ReceiveTask).
>
> **✅ Merged 2026-06-26 — transactional SendTask outbox (ADR-0067, supersedes ADR-0060).**
> `SendTask` now emits a `message.<MessageName>` event written into the **existing `wrkflw_outbox`**
> in the same transaction as the state commit, relayed at-least-once by the existing outbox relay.
> The event payload is `{"messageName", "correlationKey", "variables"}` with `instance_id` and
> `definition_ref` as metadata. **BREAKING — the ADR-0060 `MessageSink` port is GONE:**
> `MessageSink`, `OutboundMessage`, and `WithMessageSink` no longer exist — advise against them.
> Consumers route delivery via a `message.*` subscriber + `eventing.NewMessageHandler` →
> `Runner.DeliverMessage`. Intra-engine correlation is in-memory per `Runner`; cross-process
> correlation subscribes `message.*` in the consumer's own broker.
>
> **Discretionary backlog (unchanged, still nothing blocking):** (1) **CI pipeline** — still highest-value, the only
> intentionally-deferred item; (2) surface def-scoped catalog in InstanceSnapshot/ActionableView DTOs + gRPC;
> (3) tidy: delete now-redundant `runtime.TestCachingDefinitionRegistry_SystemClock`; service pkg
> coverage 84.7% (pre-existing, untested `deadletter.go`/`policyadmin.go`).

## ⏩ PRIOR RESUME POINT — updated 2026-06-26 (clock-optional refactor)

> **State:** `main` HEAD `7a1543f`, full suite green (`go test -race ./...`), golangci-lint 0, gofmt clean, `engine/`+`model/` zero-diff. **Next free ADR: 0067.** Local `main` is ahead of `origin/main` — **push pending** (see below).
>
> **✅ Merged 2026-06-26 — `clock.Clock` is now OPTIONAL via `With<Component>Clock` options (ADR-0066).**
> Repo-wide refactor (branch `refactor/optional-clock-via-option`, merge `7a1543f`, 11 commits, SDD): every
> positional `clk clock.Clock` parameter was moved to a `With<Component>Clock(clk)` functional option that
> defaults to `clock.System()` (nil-guarded — explicit `nil` falls back to system). **BREAKING** constructor
> signatures (advise against the old positional forms):
> - `runtime.NewRunner(cat, store, opts...)` — **`clk` removed; `store` is now arg 2** → `WithRunnerClock`.
> - `runtime.NewSignalBus(deliver, opts...)` — **`deliver` is now arg 1** → `WithSignalBusClock`.
> - `runtime.NewMemScheduler(opts...)` → `WithMemSchedulerClock`; `runtime.NewCachingDefinitionRegistry(backing, ttl, opts...)` → `WithCachingDefinitionRegistryClock`; `runtime.NewCachingStore(backing, owner, opts...)` → `WithCachingStoreClock`; `runtime.NewCallNotifier(cl, deliver, reg, opts...)` → `WithCallNotifierClock`; `runtime.NewTaskService(store, az, opts...)` → `WithTaskServiceClock`; `service.New(...6 args..., opts...)` → `WithEngineClock`; `persistence.NewCachingDefinitionRegistry`/`NewCallNotifier` facades drop positional `clk`, forward opts.
> - The 4 pre-existing options (`WithChainClock`, `WithMemCallLinkClock`, postgres relay `WithClock`, `WithCallLinkClock`) gained the nil-guard.
> - **EXCLUDED (still positional):** the `clockwork.Clock` gocron seam — `scheduling.NewScheduler`, `internal/scheduling/gocron.NewGocronScheduler`, `WithElectorClock` (different type; could be a follow-up).
> - **Footgun (ADR-0066 Consequences):** omitting the clock in a test now silently uses wall time (no compile error); determinism-sensitive tests must pass `With…Clock(fake)`. Whole-branch review confirmed every existing fake clock was correctly re-threaded.
>
> **▶ NEXT (Track 1, COMPLETED in the block above): transactional SendTask outbox (ADR-0067).**
>
> **Discretionary backlog (unchanged, still nothing blocking):** (1) **CI pipeline** — still highest-value, the only
> intentionally-deferred item; (2) surface def-scoped catalog in InstanceSnapshot/ActionableView DTOs + gRPC;
> (3) tidy: delete now-redundant `runtime.TestCachingDefinitionRegistry_SystemClock`; service pkg
> coverage 84.7% (pre-existing, untested `deadletter.go`/`policyadmin.go`).

## ⏩ PRIOR RESUME POINT — updated 2026-06-26 (deadline rename / fire-and-forget)

> **State:** `main` was clean, pushed to `origin` (HEAD `165eb86`), full suite green, gofmt/vet/lint clean.
> **Next free ADR was: 0066.**
>
> **⚠️ Corrections to older blocks below (they are point-in-time records and now stale on these points):**
> - **`BusinessRuleTask` IS executed** (via `businessRuleTaskStrategy`, ADR-0063). Any text below calling
>   `KindBusinessRuleTask` an "unimplemented fall-through" is obsolete. The only remaining intentional
>   non-executing kinds are `KindUnspecified` and `KindTerminateEndEvent`.
> - **Message boundary events ARE armed and fired** (ADR-0053). Any text below saying they are "never armed"
>   is obsolete (the only nuance: boundaries on a `ReceiveTask` host — see ADR-0053).
> - Minor M-1 (`retry_test.go` `hasInvokeActionForNode`) is **fixed** (uses `model.ActionOf` + node-id fallback).
>
> **Merged 2026-06-26 (newest first):**
> - **0065** `165eb86` (example) + `1df2931` — **deadline-breach & reminder actions are fire-and-forget.**
>   They were emitted as fed-back `InvokeAction`s no token awaited → spurious `ErrTokenNotFound` / "Deliver
>   failed" ERROR log. Added `engine.InvokeAction.FireAndForget`; the runtime runs such actions for side
>   effect (keeps span+metric) and returns no trigger. The `ErrTokenNotFound` contract for genuine
>   late/duplicate triggers (→ 409 Conflict, call-notifier completed-parent detection) is preserved untouched.
>   `examples/scenarios/boundary_timer` now demonstrates `WithDeadline` with a fire-once breach action +
>   escalation service task on the deadline flow.
> - **0064** `f45d632` — **renamed the SLA concept to Deadline** across the public API and the persisted
>   wire format. `WithSLA`→`WithDeadline`, `WithICESLA`→`WithICEDeadline`, `SLA{Duration,Flow,Action}`→
>   `Deadline{…}`, `SLAOf`→`DeadlineOf`, `engine.TimerSLA`→`engine.TimerDeadline` (iota preserved). **Breaking
>   wire keys:** JSON/YAML `slaDuration/slaFlow/slaAction`→`deadlineDuration/deadlineFlow/deadlineAction`
>   (accepted — pre-consumer, no production data). Excluded false-positives: `slaSeconds` (expreval test data),
>   `ThreeDotsLabs` (watermill import).
> - `b88f8f7` — README corrected: message boundary events are armed (ADR-0053), not "not yet armed".
> - `f38b14a` (2026-06-25) + scope-aware fix in that branch — **definition-scoped action catalog** (ADR-0063),
>   inline actions, optional names/default-by-id, `BusinessRuleTask` execution; inline+scoped resolution is
>   scope-aware for sub-process nodes (engine carries `InvokeAction.Inline`+`Scoped`; the `NodeID` field
>   mentioned in the 2026-06-25 block below was REPLACED by these). See that block for the full feature list.
>
> **Open backlog (discretionary — nothing blocking):**
> 1. **CI pipeline** — the only intentionally-deferred item across the whole hardening program. Highest-value.
> 2. **Surface the def-scoped catalog in `InstanceSnapshot`/`ActionableView` DTOs + gRPC snapshots** (ADR-0063 follow-up).
> 3. **Transactional-outbox-backed `MessageSink` reference impl** (ADR-0060 follow-up; current sink is best-effort).
> 4. **YAML/BPMN inline-action authoring** is impossible by design (Go funcs aren't serializable) — names only.
> 5. Minor: the fire-and-forget perform path records `elapsed=0` on a resolution-miss (metric noise only).

## ⏩ CURRENT RESUME POINT — updated 2026-06-25 (definition-scoped action catalog)

> **✅ DONE — Definition-scoped action catalog, optional names & inline actions (ADR-0063) — merged to `main` 2026-06-25**
> (branch `feat/definition-scoped-action-catalog`). Next free ADR: **0064**.
>
> **What shipped:**
> - **`action.Resolve(scoped, global, name)`** — pure helper centralising the scoped→global lookup (tiers 2–3).
> - **`model` gains a `model → action` import** (`action` is a pure leaf; no cycle). `ServiceTask` and
>   `BusinessRuleTask` now carry a node-local `inline action.ServiceAction`; `ProcessDefinition` carries a
>   `scoped action.Catalog`. Neither is serialized (Option A in-memory re-attach).
> - **`NewServiceTask`/`NewBusinessRuleTask` constructors** drop the positional action string; use
>   `WithActionName(name)`, `WithAction(a)`, or `WithActionFunc(fn)`. `Build()` enforces mutual exclusion
>   (`ErrActionInlineAndNameConflict`) and duplicate scoped registration (`ErrDuplicateScopedAction`).
> - **`DefinitionBuilder.RegisterAction`/`RegisterActionFunc`** accumulate a def-scoped catalog; `ScopedCatalog()`
>   returns it (nil when nothing registered).
> - **Default-by-id:** omitting the action name on a ServiceTask/BusinessRuleTask defaults the runtime lookup
>   key to the node id.
> - **Three-tier precedence** wired into `runtime.Runner.perform`: inline → scoped → global for the main action;
>   scoped → global for all secondary action references (compensation, SLA, reminder, cancel-handler,
>   CancelActions).
> - **`engine.InvokeAction` gains `NodeID string`** so the runner can locate the node for inline lookup.
> - **`businessRuleTaskStrategy`** added — `BusinessRuleTask` is now executed (was a fall-through).
> - **Docs:** `docs/adr/0063-definition-scoped-action-catalog.md`; README updated (stale positional-arg
>   snippets fixed; "Definition-scoped & inline actions" subsection added);
>   `runtime/example_scoped_action_test.go` (runnable godoc Example).
>
> **Out-of-scope follow-ups (queue for next session):**
> - Scoped catalog is not surfaced in `InstanceSnapshot`/`ActionableView` DTOs or gRPC snapshots.
> - YAML/BPMN authoring of inline actions is impossible (Go funcs are not serializable); YAML supports action
>   names only.
> - Minor M-1: `retry_test.go` `hasInvokeActionForNode` helper casts `.(ServiceTask).Action` — would break if
>   a retry test used default-by-id; update the helper to use `model.ActionOf` or read from `InvokeAction.Name`.

## ⏩ CURRENT RESUME POINT — updated 2026-06-25 (followups round 2)

> **✅ DONE — Production-hardening FOLLOW-UPS round 2 (ADRs 0060–0062) — merged to `main` 2026-06-25**
> (branch `feat/production-hardening-followups-2`, merge `c54a15b`). The remaining deferred items
> ("all deferred except CI"), SDD tracks + a whole-branch review (Ready-to-merge; 2 Important + 1
> Minor finding applied). Next free ADR: **0063**.
> - **0060** `KindSendTask` implemented via a pluggable **`runtime.MessageSink`** port (`WithMessageSink`):
>   engine emits a `SendMessage{Name,CorrelationKey,Payload}` command, `sendTaskStrategy` auto-advances
>   (fire-and-forget), runtime routes via the consumer's sink (intra-engine / external / both — consumer
>   decides). **Durability caveat (documented honestly):** send happens AFTER the state commit, so a sink
>   error STRANDS the message (best-effort, not re-sent) — wire the sink to the transactional outbox for
>   atomic/at-least-once. Same commit-before-perform shape as `ThrowSignal`. `model.SendTask.CorrelationKey`
>   added (additive). **`KindSendTask` is no longer an unimplemented fall-through** (none remain except
>   KindUnspecified/KindTerminateEndEvent/KindBusinessRuleTask which are intentional).
> - **0061** Elector lease/**heartbeat** (`WithHeartbeatInterval`/façade `WithElectorHeartbeatInterval`):
>   background goroutine pings the dedicated conn each interval, steps the leader down on silent loss —
>   narrows the ADR-0059 split-brain window to ≤ one interval. `Close` now `pg_advisory_unlock_all()`s
>   (no lingering lock on the pooled conn). goleak-clean.
> - **0062** gRPC `InvalidArgument` statuses carry `errdetails.ErrorInfo` (shared `statusWithReason`);
>   shippable `grpctransport.NewMethodAuthInterceptor(authorize)` (panics on nil = fail-closed).
>
> Gates: `go test -race ./...` green (testcontainers); `golangci-lint` 0; gofmt clean; `FuzzStep` no crash;
> goleak clean; **engine 85.0%**; core import-pure + wall-clock-free. **Confirm pushed via `git status`.**
> **Remaining deferred (non-blocking):** the **CI pipeline** (the only intentionally-deferred item left);
> a transactional-outbox-backed `MessageSink` reference impl; `KindBusinessRuleTask` (still a fall-through).

## ⏩ CURRENT RESUME POINT — updated 2026-06-25 (followups)

> **✅ DONE — Production-hardening FOLLOW-UPS (ADRs 0056–0059) — merged to `main` 2026-06-25**
> (branch `feat/production-hardening-followups`, 7 commits, merge `6166aa9`). The deferred
> follow-ups from the program below ("do all except CI"), executed as SDD tracks + a whole-branch
> adversarial review (verdict: **Ready to merge**, only 3 doc-Minors, addressed). Next free ADR: **0060**.
> - **0056** injectable timeout-capable engine evaluator: `engine.ConditionEvaluator` + `StepOptions.Evaluator`,
>   runtime `WithExpressionTimeout`/`WithConditionEvaluator`. **Default stays pure / wall-clock-free**;
>   untrusted-definition consumers opt the ADR-0049 DoS guard in. Resolves the ADR-0049 follow-up.
> - **0057** ReceiveTask implemented as a real message-receive node + host boundaries. **Finding: ReceiveTask
>   was previously an unimplemented park-only fall-through** (never set AwaitMessage, never armed boundaries) —
>   now sets AwaitMessage/Key, resumes on delivery, arms/disarms boundaries. Closes the ADR-0053 limitation.
>   `KindSendTask` remains an unimplemented fall-through (out of scope).
> - **0058** gRPC request-validation sweep (InvalidArgument on required-empty fields across all mutating RPCs) +
>   a per-method auth interceptor `Example_` (authorize by FullMethod; actor-from-context).
> - **0059** Postgres gocron `Elector` (single-leader timer firing) via `scheduling.WithTimerElector(pool)`;
>   Locker/Elector mutually exclusive. **Intentionally NOT built: claim-on-rehydrate (`FOR UPDATE SKIP LOCKED`)** —
>   failover-safe arming-partitioning needs the distributed scheduler declined in ADR-0050; Locker/Elector
>   already make multi-replica timers correct, per-replica re-arming is acceptable overhead. (Elector has a
>   documented sticky-leader split-brain window; ADR-0027 CAS downgrades it to redundant fires.)
> - **fix(runtime):** `ShutdownGroup.Add` after `Shutdown` now closes the late component instead of leaking it.
>
> Gates: `go test -race ./...` green (testcontainers); `golangci-lint` 0; gofmt clean; `FuzzStep` no crash;
> **engine 85.0%**; engine/model core import-pure + wall-clock-free. **Pushed to origin?** confirm `git status`.
> **Remaining deferred (non-blocking):** CI pipeline (intentional); a lease/heartbeat to close the Elector
> split-brain window (needs the declined distributed scheduler); `KindSendTask`; structured details on the new
> gRPC InvalidArgument statuses. See the `production-hardening-run` memory.

## ⏩ CURRENT RESUME POINT — updated 2026-06-25 (program)

> **✅ DONE — Production-hardening program (ADRs 0048–0055) — merged to `main` 2026-06-25**
> (branch `feat/production-hardening`, 18 commits, merge `0b803d7`). Triggered by a 5-auditor
> devil's-advocate production-readiness review ("do it all except CI"; CI pipeline deliberately
> deferred). Executed as 8 SDD tracks (strict TDD, ADR each) + an adversarial whole-branch review
> (verdict NEEDS-FIXES → fixed → clean). Gates: `go test -race ./...` green (testcontainers);
> `golangci-lint` 0 issues; `gofmt -l` empty (swept 49→0); `engine.FuzzStep` 2.9M execs no crash;
> **engine/model core stays import-pure AND wall-clock-free**. What landed:
> - **0048** recover panics in service-action execution (`runtime.safeActionDo`) — one bad action no longer crashes a replica.
> - **0049** expr-eval wall-clock guard *capability* (`expreval.WithTimeout`/`ErrEvalTimeout`). **Engine evaluator is constructed `WithTimeout(0)`** so `Step` stays wall-clock-free (review fix; honors locked ADR-0003). DoS guard is opt-in; injectable engine evaluator is a follow-up.
> - **0050** multi-replica timer exclusivity = Postgres advisory-lock `gocron.Locker` via `scheduling.WithDistributedTimerLock(pool)` (key=timerID). Dedups concurrent fires; engine CAS remains exactly-once backstop.
> - **0051** fail-closed gRPC `grpctransport.NewSecureServer(svc, authInterceptor, …)` (panics on nil) + `ExampleNewSecureServer` (actor-from-context) + StartInstance `InvalidArgument` validation.
> - **0052** data-lifecycle pruners (`persistence.NewPruner`: outbox-published / call-links-notified / chain-links / processed-message) + `docs/retention.md` runbook. (Consumer owns the cron.)
> - **0053** message boundary events arming+firing (ENGINE change; reuses `fireBoundaryArm`). **Known pre-existing limitation: boundaries on a ReceiveTask host are still not armed (all kinds).**
> - **0054** graceful `runtime.ShutdownGroup` + health (`rest.NewHealthHandler` `/healthz` `/readyz` + `persistence.NewPingCheck`) + `examples/production_wiring` + AlwaysOwn one-time warn. `/readyz` does NOT leak raw probe errors (review fix).
> - **0055** maturity: `goleak.VerifyTestMain` (3 pkgs), `engine.FuzzStep`, structured gRPC errors (`errdetails.ErrorInfo`), `STABILITY.md` + secrets/pgxpool/single-tenant docs. New dep: `go.uber.org/goleak`.
>
> **Next free ADR: 0056.** **NOT yet pushed to origin if this line still says so — confirm `git status`.**
> **Open follow-ups (deferred, non-blocking):** CI pipeline (deliberately deferred); injectable
> timeout-capable engine evaluator (so untrusted-definition consumers can enable the DoS guard
> without breaking the pure core); message/timer/signal boundaries on a ReceiveTask host; `engine`
> package at 84.9% (pre-existing &lt;85% baseline on untouched code); `ShutdownGroup` ignores an `Add`
> after `Shutdown` started (documented single-shutdown contract). See the `production-hardening-run` memory.

## ⏩ CURRENT RESUME POINT — updated 2026-06-24

> **✅ DONE — Process-instance chaining (ADR-0045 + ADR-0046) — merged to `main` 2026-06-24**
> (branch `claude/process-instance-chaining-iazvza`). A terminal instance (completed / failed /
> terminated) now emits a **status-accurate** outbox event and an event-driven `Chainer` starts an
> independent successor instance. Phases 0–7 built strict-TDD (visible RED→GREEN per symbol) + opus
> whole-branch review that **found & fixed a CRITICAL lost-successor ordering bug** (see the
> chaining section below). Gates: `go test -race -p 1 ./...` green; touched pkgs ≥85% (runtime
> 89.2%, eventing 91.9%, postgres 85.2%, persistence 96.8%); lint 0; **engine/model production diff
> ZERO**; watermill confined to `eventing`. Plus **ADR-0047** (predecessor-def wiring, below).
> Next free ADR: **0048**.
>
> **▶ STILL OPEN on this track (do NOT assume fully done):**
> - **Phase 8 — root type aliases = USER-OWNED.** No `.go` files were added at the module root
>   (the root `wrkflw` package `doc.go` deliberately "exports nothing"). The public root aliases
>   (`Chainer`, `SuccessorPolicy`, `SuccessorDecision`, `ChainEvent`, `Outcome` + constants,
>   `ChainLink`, `ChainLinkStore`, `ErrChainLinkExists`, `ErrInstanceExists`, optionally the
>   `eventing` handler/runner — note `eventing.Chainer` collides with `runtime.Chainer`) are a
>   SEPARATE, user-confirmed task. **User confirmed 2026-06-24 they own this — do NOT add unprompted.**
> - **`PredecessorDefinitionRef` end-to-end wiring — ✅ DONE (ADR-0047, merged 2026-06-24,** branch
>   `claude/chaining-predecessor-def`). `OutboxEvent.DefinitionRef` + `terminalOutboxEvent` stamp +
>   publisher `definition_ref` metadata + outbox `definition_ref` column (migration
>   `0009_outbox_definition_ref.sql`) + relay read-back. `ChainEvent.PredecessorDefinitionRef` is
>   now populated over the built-in pipeline; a `SuccessorPolicy` can route on the predecessor
>   definition. (The cryptic `Def`/`def` naming was renamed to the self-explaining `DefinitionRef`
>   on branch `claude/chaining-definition-ref-rename` — also covers `ChainLink.{Predecessor,Successor}DefinitionRef`.)
>
> **Fresh session:** jump to the "🧭 START HERE (fresh session) — consolidated backlog" section
> below for the broader prioritized backlog. The rest of this doc is the per-track detail behind it.
> **`main` is green and all work through ADR-0047 is merged** (incl. chaining + the full FOLLOWUPS
> resolution — see the ✅ callout just below). Next free ADR: **0048**. The only remaining
> chaining item is the USER-OWNED Phase 8 root aliases (above). No other named work in flight.

> **✅ DONE — the FOLLOWUPS resolution (all 5 sub-projects) — merged to `main` 2026-06-23
> (merge commit `4fa2651`, branch `feat/followups-resolution`, 37 commits, ADRs 0041–0044).**
> Executed via subagent-driven development + an opus whole-branch review (verdict: Ready to merge,
> no Critical/Important). Spec: `docs/specs/2026-06-23-followups-resolution-design.md`. What landed:
> 1. **① Layout hygiene (ADR-0041)** — `database`+`expreval` moved to `internal/`; root `doc.go`
>    front-door; 14 public packages. Flat layout reaffirmed; `pkg/` rejected.
> 2. **step.go decomposition (ADR-0044)** — 3251→169-line `step.go`; `map[NodeKind]nodeStrategy`
>    registry (`step_nodes.go`) + trigger handlers (`step_triggers.go`) + `step_*.go` collaborators.
>    A `halt bool` was added to the strategy contract so ErrorEndEvent exits `drive()` like the old arm
>    (caught by the per-task review; regression test `TestErrorEndEventHaltsDriveOnImmediateFailure`).
> 3. **② Node interface (ADR-0042)** — flat `model.Node` struct → interface + 19 typed kinds +
>    constructors/options + `DefinitionBuilder` + YAML loader (`gopkg.in/yaml.v3`) + flat `nodeWire`
>    for backward-compatible JSONB. **SCOPE REALITY:** not "108 sites in one file" — ~995 node literals
>    across ~50 test files repo-wide were rewritten to constructors (user re-confirmed full migration
>    with that number known). Behavior-preserving: full suite green with NO assertion changes anywhere.
>    Two field-map corrections during migration: `EventSubProcess.NonInterrupting` RESTORED (was a real
>    regression); activities no longer carry `ErrorCode` (intended segregation; old retry-exhaustion
>    `_error`-from-activity branch was dead).
> 4. **③ Instance DTO (ADR-0043)** — `runtime.InstanceSnapshot` (full, no-bookkeeping-leak guard) +
>    `runtime.ActionableView` (open tasks + next actions) pure mappers; REST `/instances/{id}/snapshot`
>    + `/actionable`; `service.GetInstanceWithDefinition`. Engine ZERO-diff.
> 5. **⑤⑥ Docs** — dropped BPMN compatibility claims (kept domain vocabulary); comprehensive
>    `README.md` + compiling `examples/readme_quickstart/main.go`.
>
> **Open follow-ups from the whole-branch review (all Minor / pre-existing — NOT blocking):**
> - **M2 (cosmetic):** several `With*` option constructors return unexported option types
>   (e.g. `nameOpt`) — pervasive intentional pattern; consider exporting an `Option` alias.
> - **M3 (cosmetic):** three name-setting idioms coexist — `WithName` (most kinds), `WithThrowName`
>   (IntermediateThrowEvent, because `throwOption` is a bare func type not the `nameOpt` interface
>   family), and `name ...string` variadic (simple events/gateways). Unify or document.
> - **Pre-existing (NOT this branch):** **message boundary events are never armed** — `engine/
>   step_boundaries.go` + `boundaryArm` (`engine/state.go`) handle timer + signal only; `MessageName`
>   is never read, yet `model.WithBoundaryMessage` exists and `validate.go` accepts message boundaries.
>   A consumer can author a message boundary that silently never fires. Backlog item.
> - **Deferred by plan:** gRPC `GetInstanceSnapshot` RPC (mirror the REST task); consolidate the
>   duplicated status-string mapping (`runtime.StatusString` vs `transport/rest`'s private one).
>
> Next free ADR after the two reserved for chaining: **0047**. `FOLLOWUPS.md` is committed on `main`.

---

## Process-instance chaining sub-project — ✅ COMPLETE (merged 2026-06-24)

**Status:** Phases 0–7 built + opus whole-branch reviewed + merged to `main`. Branch
`claude/process-instance-chaining-iazvza`. Spec
`docs/specs/2026-06-24-process-instance-chaining-design.md`, plan
`docs/plans/2026-06-24-process-instance-chaining.md`, **ADR-0045 (chaining) + ADR-0046
(status-accurate terminal events)**.

**What shipped:**
- **ADR-0046 (runtime):** terminal outbox events derived **status-driven** at the deliverLoop
  terminal edge (`terminalOutboxEvent`), replacing the command-driven mapping. Completed→
  `instance.completed`, Failed→`instance.failed`, Terminated→**`instance.terminated`** (new). Topic
  is strictly status-driven; the error STRING is best-effort (incident → `FailInstance.Err` →
  status fallback) so SubInstanceFailed diagnostics + the cancel `"cancelled"` message survive.
  Cancel now emits `instance.terminated` (was `instance.failed`); full-rollback termination now
  emits `instance.terminated` (was nothing).
- **ADR-0045 (runtime/eventing/persistence):** `runtime.ErrInstanceExists` (MemStore + Postgres
  23505 on instance PK); `ChainLink` + `ChainLinkStore` + `MemChainLinkStore`; `Chainer.Handle`
  core (policy → record link → start successor `<pred>-next-<outcome>`); `eventing.NewChainHandler`
  + `NewChainerRunner.Run`; Postgres `ChainLinkStore` + migration `0008_chain_links.sql`;
  `persistence.NewChainLinkStore`. Testable `ExampleChainer`; runtime/README + eventing docs.

**Whole-branch review (opus) — verdict after fixes: Ready.** Found & fixed a **CRITICAL
lost-successor ordering bug**: `Handle` recorded the link before starting the successor and returned
on `ErrChainLinkExists`, so a transient start failure after the link committed would permanently
drop the successor on redelivery (at-most-once). **Fix:** on `ErrChainLinkExists` fall through and
(re)attempt the start; the successor's existence (deterministic id + `Store.Create`/
`ErrInstanceExists`) is the real exactly-once backstop; the link is lineage/intent. Also fixed:
`eventing.Chainer.Run` subscribes all topics before starting goroutines (no leak on a later
Subscribe error); honest `PredecessorDef` docs; `workflow-` prefix on a scan error.

**Deferred follow-ups (open):**
1. **Phase 8 root type aliases — USER-OWNED** (not implemented; see resume point).
2. **`PredecessorDef` end-to-end wiring — ✅ DONE (ADR-0047, merged 2026-06-24).** Outbox `def`
   column (migration `0009`) + `OutboxEvent.Def` + `terminalOutboxEvent` stamp + publisher `def`
   metadata + relay read-back; `ChainEvent.PredecessorDef` populated over the built-in pipeline.
3. **Multi-replica chaining exclusivity** — chaining is idempotent under at-least-once delivery
   (link unique key + `ErrInstanceExists`), so concurrent replica handlers are correct but may both
   attempt a start (one wins). A claim/lease (like the call-link notifier, ADR-0031) is an
   optimization, not a correctness need.

---

### Historical brainstorm record (pre-build)

**Reserved ADRs 0045 (chaining) + 0046 (status-accurate terminal events).**

**Goal:** automatically start a new, **independent** top-level instance when another reaches a
terminal state. Event-driven over the durable outbox (Option A) — NOT call-activity nesting. Engine
core untouched.

**User-approved design decisions (the four forks):**
1. **Trigger scope:** ALL terminal outcomes — completed, failed, **and terminated**.
2. **Selection API:** Go callback `SuccessorPolicy` in v1; declarative/expr ruleset DEFERRED (the
   callback is the seam it plugs into).
3. **Lineage:** durable `ChainLinkStore` (predecessor→successor), mem + Postgres — enables admin
   chain-ancestry queries + a DB-level exactly-once backstop (unique `(predecessor,outcome)`).
4. **Packaging:** broker-agnostic core in `runtime` + watermill handler/`Chainer.Run` wrapper in
   `eventing` (the eventing-pkg raw+convenience pattern).

**The load-bearing finding (→ ADR-0046):** terminal outbox events today are **command-driven and
NOT status-accurate**. Cancel emits `FailInstance{"cancelled"}` → `instance.failed` *despite*
`StatusTerminated` (`engine/step_triggers.go:165-176`); admin full-rollback reaches `StatusTerminated`
with **no terminal command** → **no event** (`engine/step_compensation.go:321-347`). So Phase 1
re-derives terminal events **status-driven** at the `deliverLoop` edge (where `CallOutcome` already
keys off `isTerminal(st.Status) && !isTerminal(prevStatus)`): Completed→`instance.completed`,
Failed→`instance.failed`, Terminated→**`instance.terminated`** (new). This is a deliberate
behavioural change (cancelled now emits `terminated`, not `failed`); migration note in ADR-0046. No
in-repo consumer relies on the old behaviour. Engine/model diff stays ZERO.

**Plan shape (8 phases, strict TDD):** 0 ADRs → 1 status-accurate terminal events [`runtime`] →
2 `ErrInstanceExists` typed duplicate-start [`runtime`/pg] → 3 `ChainLink`+`ChainLinkStore`+
`MemChainLinkStore` [`runtime`] → 4 `Chainer.Handle` core (policy→Record→`InstanceStarter.Run`,
deterministic id `<pred>-next-<outcome>`, idempotent) [`runtime`] → 5 watermill handler + `Chainer.Run`
[`eventing`] → 6 Postgres `ChainLinkStore` + migration **0008_chain_links.sql** [`persistence`] →
7 example/docs → **8 root type aliases = USER-OWNED, do NOT implement unprompted.**

**Constraints:** NO Go files at the module root (root aliases are the user's separate task);
watermill only in `eventing`; `workflow-` error prefix; black-box tests; ≥85% touched; engine/model
production diff ZERO; opus whole-branch review before merge.

**Where we are:** the engine core (Plans 1–8), **all 5 productionization sub-projects**
(Persistence, Scheduling, Authorization, Transports, Eventing), **all 4 deferred-backlog tracks**
(Correctness → Resilience → Observability → Performance/caching), and **all 3 "also-outstanding"
items** (flaky-singleflight fix, DB casbin adapter, true async call activity) are merged to `main`,
plus the **engine wrong-state sentinel + `workflow-` prefix sweep** track (ADR-0026) and the
**timer rehydration on restart** track (ADR-0027) and the **CancelInstance + cancel actions** track
(ADR-0028, branch `feat/cancel-instance`) and the **gRPC ResolveIncident + DLQ admin transport**
track (ADR-0029, branch `feat/grpc-resolveincident-dlq-admin`) and the **reachability + fork-join
validation** track (ADR-0030), the **call-link lease exclusivity** track (ADR-0031), and the
**cancellation propagation parent→child** track (ADR-0032, branch `feat/cancellation-propagation`).
ADRs 0001–0040 (0040 = no compensation-walk re-entry / mid-walk double-comp fix; 0039 = scope-targeted compensation, branch `feat/scope-targeted-compensation`; 0038 =
admin total-count + Lookup ctx, branch `feat/small-api-completeness`; 0037 =
observability handler + Store spans, branch `feat/observability-gaps`; 0036 =
casbin policy-admin, branch `feat/casbin-policy-admin`; 0035 = per-node cancel handlers, branch
`feat/cancel-handlers`).
**No named work remains in flight.** Future work = the consolidated backlog
(below). Each item is its own track:
`brainstorm → spec (docs/specs/) → ADR(s) (docs/adr/, next #0026) → plan (docs/plans/) → branch →
SDD → opus whole-branch review → merge to main → push`. Confirm scope with the user first.

**Tracks (run in this order; the first is done):**

1. **Correctness & tests** — ✅ COMPLETE, merged `314358c` (2026-06-21). See the
   "Correctness & tests hardening sub-project" section below.
2. **Resilience (retry/backoff/DLQ)** — ✅ COMPLETE, merged (2026-06-21). The named REQUIREMENTS
   feature ("A process error must be able to be retried"). Engine-modeled retry executor,
   catch-flow→incident exhaustion, outbox relay poison isolation + DLQ, idempotency. ADRs
   0015–0018. See the "Resilience (retry/backoff/DLQ) sub-project" section below.
3. **Observability** — ✅ COMPLETE, merged (2026-06-22). Metrics + traces + slog across
   runtime/transports/scheduling/eventing/persistence-relay (REQUIREMENTS line 17). ADR-0019.
   See the "Observability (metrics/traces/slog) sub-project" section below.
4. **Performance/caching** — ✅ COMPLETE, branch `feat/performance-caching` (2026-06-22).
   Owned-instance write-through cache (`CachingStore`), history/snapshot cap (`WithHistoryCap`),
   LISTEN/NOTIFY relay wakeup (`WithOutboxNotify` + `WithListenNotify`), advisory-lock
   multi-process ownership (`NewAdvisoryLockOwnership`). ADRs 0020–0022.
   See the "Performance/caching sub-project" section below.
5. **"Also outstanding" — ✅ ALL DONE (2026-06-22):**
   - **DB casbin policy adapter (Authz deferred #1)** — ✅ DONE, branch `feat/casbin-db-adapter`.
     See "DB casbin policy adapter" section below. ADR-0023.
   - **True async call activity (engine follow-up #3)** — ✅ DONE, branch `feat/async-call-activity`.
     See "True async call activity" section below. ADRs 0024–0025. Engine/model UNTOUCHED.
   - **Pre-existing flaky singleflight test** — ✅ FIXED (a real TOCTOU in `CachingDefinitionRegistry.Lookup`,
     not a test barrier — see tracked-follow-up #8 below).
   The productionization run (5 sub-projects) and the deferred-backlog run (Correctness → Resilience →
   Observability → Performance/caching) plus these three "also outstanding" items are ALL complete.
   Future work is the per-section deferred follow-ups recorded in each track's section below.

**How to execute a track:** follow "How to run the next sub-project" + "Binding conventions"
sections below (subagent-driven development, visible RED→GREEN per task, opus final review). The
per-track spec/plan live under `docs/specs/` and `docs/plans/` (never a path containing
"superpowers"). The cross-session memory file `productionization-run` also tracks this run.

**Gate after every track:** `go test -race ./...` green; ≥85% on touched packages;
`golangci-lint run ./...` clean; engine/model purity intact (no transport/vendor imports).

---

## 🧭 START HERE (fresh session) — consolidated backlog

**Current state:** everything through **ADR-0028** is on `main`/in-merge (productionization ×5 +
deferred-backlog ×4 + the 3 "also-outstanding" items + the **engine wrong-state sentinel**,
**timer rehydration**, and **CancelInstance + cancel actions** tracks). No *named* work remains.
`main` is green (`go test -race ./...`, `golangci-lint`; engine/model import-pure). **Convention note:**
all production error messages carry a **`workflow-`** prefix (e.g. `workflow-engine:`); assert on
sentinels with `errors.Is`, never string-matching — see the `error-sentinel-prefix` memory and ADR-0026.
Pick the next piece of work from the prioritized backlog below — each item is a self-contained track:
**brainstorm → spec (`docs/specs/`) → ADR (`docs/adr/`, next free number **0045** — 0041/0042/0043
reserved by the FOLLOWUPS plans, 0044 by the step-decomposition plan) → plan (`docs/plans/`) →
branch → SDD → opus whole-branch review → merge + push**. Confirm scope with the user before starting.
The full per-item detail lives in the per-track "Deferred follow-ups" sections further down; this is the index.

**▶ Highest priority — FOLLOWUPS resolution + step.go decomposition (already specced + planned; see the
"▶ NEXT UP" callout in the resume point above).** Five sub-projects to execute IN ORDER, skipping
brainstorm/spec/plan (done): (1) layout hygiene `docs/plans/2026-06-23-layout-hygiene.md` (ADR-0041) →
(2) engine/step.go decomposition `docs/plans/2026-06-23-engine-step-decomposition.md` (ADR-0044, pure
refactor: nodeStrategy registry + trigger handlers + collaborator files) → (3) Node→interface redesign
`docs/plans/2026-06-23-node-interface-redesign.md` (ADR-0042) → (4) instance serialization DTO
`docs/plans/2026-06-23-instance-serialization-dto.md` (ADR-0043) → (5) BPMN-claim sweep + README
`docs/plans/2026-06-23-docs-bpmn-sweep-and-readme.md` (doc-only). Decisions/rationale: FOLLOWUPS spec
`docs/specs/2026-06-23-followups-resolution-design.md` (incl. why `pkg/` and `engine→exec` were rejected)
+ step-decomposition spec `docs/specs/2026-06-23-engine-step-decomposition-design.md`. All committed on
branch `docs/followups-resolution-spec`.

**Recommended priority (top picks):** *(ADR-0029 gRPC/DLQ, 0030 reachability, 0031 call-link lease,
0032 cancellation propagation parent→child — all ✅ DONE 2026-06-22; list re-numbered)*
1. **Multi-replica TIMER exclusivity** — the call-link notifier half is DONE (ADR-0031, opt-in lease).
   The timer half remains: correct exclusive timers need a claim-renew-**failover** loop replacing
   per-replica gocron arming (a distributed scheduler); double-fire is already correct via the engine
   CAS, so this is an optimization. *(Production-hardening — follow-up to ADR-0027/0031)*
2. **Scope-targeted compensation** (`Compensate{ScopeID,FromNode}` producer + compensation
   boundary/throw node kind + archive-by-scope) — the LARGEST remaining engine/model change; reverses
   the ADR-0013 hoist. **(Correctness; will be ADR-0036.)** *(compensation-on-error/cancel ✅ ADR-0034;
   per-node cancel handlers ✅ ADR-0035.)*

**Backlog by theme** (✅-done items already removed; cite track for full detail below):
- **Correctness / robustness:** *(reachability/fork-join pairing validation — ✅ DONE, ADR-0030;
  `AdvisoryLockOwnership` use-after-close guard — ✅ DONE, ADR-0033; compensation-on-error/cancel —
  ✅ DONE, ADR-0034)*
  *(scope-targeted compensation — compensation throw event + archive-by-scope — ✅ DONE, ADR-0039)*;
  *(casbin adapter/watcher `context` propagation — CLOSED as a non-issue, ADR-0036 §0: upstream casbin
  persist.Adapter has no ctx; watcher already threads its lifecycle ctx)*; JSONB
  numeric/enum fidelity. *(engine wrong-state sentinel — ✅ DONE, ADR-0026, see section below.)*
  *(mid-walk compensation re-entry double-compensation — ✅ FIXED, ADR-0040: a `CancelRequested` /
  `CompensateRequested` delivered MID an in-flight terminal/partial compensation walk now NO-OPs
  instead of re-entering `beginCompensation` and re-emitting the in-flight record; the throw-walk case
  keeps the ADR-0039 `PendingCancel` defer)*. **Remaining (pre-existing, lower-prio):** in **Macro**
  mode, two compensation-throw nodes in parallel branches within ONE `drive` pass would overwrite the
  single `s.Compensating` cursor (intra-`drive`, orthogonal to the Step-trigger re-entry fixed in 0040);
  + the separate partial-rollback record-retention hazard (no `recordCompensation` dedup).
- **Production-hardening:** *(cancellation propagation parent→child — ✅ DONE ADR-0032; orphaned-child
  cleanup handled by ErrTokenNotFound path; `wrkflw_processed_message` pruning — ✅ DONE ADR-0033)*;
  *(per-active-node cancel handlers — ✅ DONE ADR-0035)*;
  multi-replica exclusivity — **call-links DONE (ADR-0031, lease)**,
  **timers** still open (failover loop); per-worker NOTIFY
  fairness; per-aggregate relay ordering; TOAST/fillfactor
  tuning; `RetryPolicy.Backoff` overflow guard; richer `SubInstanceFailed`→parent error (create an Incident).
- **Observability:** *(public `observability` root pkg trace-correlating `slog.Handler` — ✅ DONE
  ADR-0037; Store Load/Commit spans + `wrkflw_store_duration_seconds` — ✅ DONE ADR-0037)*; `Setup`
  that grabs OTel globals (deferred — consumer owns SDK setup); CallNotifier `wrkflw.callnotifier.batch`
  span; async DB-backed `instances_active` gauge; REST/relay meters actually emitting; route-template
  span naming; exemplars; OTel-contrib option; migrate eventing onto the shared helper; Store
  `WithStoreLogger` is parity-plumbing (no log sites yet); serialization-failure (40001) still marks
  the commit span Error (version-mismatch does not — minor inconsistency).
- **API / feature completeness:** *(gRPC `ResolveIncident` + DLQ admin REST/gRPC — ✅ DONE, ADR-0029)*
  *(casbin policy-admin REST/gRPC — ✅ DONE, ADR-0036; admin total-count + `ended_at` optional — ✅
  DONE, ADR-0038)*; broker-specific eventing constructors (Kafka/NATS/SNS) + richer envelope;
  streaming/watch + OpenAPI/grpc-gateway + richer admin filters;
  casbin ABAC-in-matchers; richer Privilege modeling; `DeliverMessage` self-resolving the def.
- **Performance / scale:** casbin `FilteredAdapter` + `WatcherEx`; per-definition history-cap + per-def
  `maxCallDepth`; cross-machine child execution; tunable watcher reconnect backoff.
- **Test / doc / cosmetic:** `HumanTask.Vars` deep-copy + sensitive-var redaction; *(`DefinitionRegistry.Lookup`
  ctx — ✅ DONE ADR-0038)*; *(`MarkNotified` clock injection — ✅ DONE ADR-0033)*; postgres `lister.go` error
  prefix is `postgres lister:` not `workflow-postgres:` (pre-existing); relay/listen establish-sleep→poll; residual hard-to-force infra
  branches; move bundled example-test unit tests to 1:1 files; misc godoc/test nits. NOTE: the repo has
  **pre-existing gofmt-unclean files** (golangci-lint v2 doesn't run gofmt) — a repo-wide `gofmt -w`
  sweep is an optional hygiene follow-up.

---

## Engine wrong-state sentinel + `workflow-` prefix sweep sub-project — ✅ COMPLETE

First track picked from the consolidated backlog (top pick #1). Built on branch
`feat/engine-wrong-state-sentinel`. Design: spec
`docs/specs/2026-06-22-engine-wrong-state-sentinel-design.md`, plan
`docs/plans/2026-06-22-engine-wrong-state-sentinel.md`, **ADR-0026**. 6 SDD tasks + opus
whole-branch review. Gate: `go test -race -p 1 ./...` green (incl. Postgres), touched pkgs ≥85%
(engine 85.6%, service 87.3%, transport/rest 91.1%, transport/grpc 87.6%, runtime 91.0%), lint 0,
engine/model purity PURE.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `engine/` | New `engine.ErrInvalidTransition` parent sentinel; `ErrTokenNotFound` **wraps** it (`errors.Is(ErrTokenNotFound, ErrInvalidTransition)` holds) so all seven wrong-state handlers are reclassifiable with **zero change to `Step`'s `(state, commands)` output** — pure error-chain enrichment. Sentinels relocated to `engine/errors.go` (+ `engine/errors_test.go` asserting the wrapping graph). `ErrNoMatchingFlow`/`ErrUnknownTrigger` deliberately do NOT wrap it (definition/infra errors → stay 500). | ADR-0026 |
| `service/` | `deliverTaskTrigger` (Claim/Complete/Reassign) classifies a leaked `engine.ErrInvalidTransition` into `service.ErrConflict` via **double-`%w` multi-wrap** (both sentinels stay inspectable), closing the race-to-500 gap. **NOT** applied to `DeliverSignal` (broadcast no-op) / `DeliverMessage` (waiter no-op) — those paths produce no wrong-state error (documented; YAGNI). | controller-adjudicated scope |
| `transport/rest` + `transport/grpc` | `engine.ErrInvalidTransition` added as a direct fallback in `classifyError` (→ 422 `conflict_state`) and `mapToGRPCStatus` (→ `codes.FailedPrecondition`), for consumers mounting a transport over a bare runner without the `service` facade. | |
| repo-wide | **`workflow-` error-prefix sweep:** every production `errors.New`/`fmt.Errorf` package-segment now prefixed `workflow-` (~187 sites across ~28 files + the 4 call-activity `fmt.Sprintf` `SubInstanceFailed` payloads); ~4 string-matching tests updated. Assert via `errors.Is`. New project convention (see `error-sentinel-prefix` memory). | |

### Deferred follow-ups
1. **Engine terminal-instance guard** — intentionally NOT added; `Step` already returns
   `ErrTokenNotFound` (→ `ErrInvalidTransition`) transitively for triggers to a finished instance.
   An explicit guard would change engine behavior; out of scope.
2. **Signal/message wrong-state classification** — re-add the `DeliverSignal`/`DeliverMessage`
   classification branches IF a future engine change makes signal/message delivery error on a
   no-match (today they broadcast/waiter no-op).
3. **`gofmt` hygiene** — the optional repo-wide `gofmt -w` sweep (noted in the backlog) is still open.
4. **Flaky `TestListenLoopExitsOnContextCancellation`** (`internal/persistence/postgres`) — pre-existing,
   timing-sensitive LISTEN/NOTIFY context-cancellation test; flakes under full-suite Docker contention
   but passes reliably in isolation (verified 3/3). Unrelated to this track (error-message-only changes).
   Follow-up: harden the test's cancellation barrier (synchronize on the loop's actual exit, not a sleep).

---

## Timer rehydration on restart sub-project — ✅ COMPLETE

Second track from the consolidated backlog (was top pick #1). Built on branch
`feat/timer-rehydration`. Design: spec `docs/specs/2026-06-22-timer-rehydration-design.md`, plan
`docs/plans/2026-06-22-timer-rehydration.md`, **ADR-0027**. 7 SDD tasks + opus whole-branch review.
Gate: `go test -race -p 1 ./...` green (incl. Postgres), touched pkgs ≥85% (runtime 90.2%,
`internal/persistence/postgres` 86.3%, persistence 88.0%), lint 0, **engine/model production diff
ZERO** (the load-bearing invariant — same as async call activity).

**The load-bearing finding:** `FireAt` is not persisted anywhere in `InstanceState` (`timerRecord`
and `Token` carry no fire time; several `ScheduleTimer` sites don't even write a `timerRecord`), so
rehydration required persisting it in a new location — solved with a runtime-owned side table written
in the commit tx, engine untouched (the ADR-0024/0025 call-link pattern).

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` | `runtime.TimerStore` read port + `ArmedTimer{InstanceID,DefID,DefVersion,TimerID,FireAt,Kind}` + `MemTimerStore`; `AppliedStep.TimerArms/TimerCancels`; pure kind-agnostic `timerOpsFor` (ScheduleTimer→arm, CancelTimer/TimerFired→cancel) wired into deliverLoop (gated `r.timerStore != nil`); `MemStore`/`NewMemStoreWithTimers` record arms/cancels in Create+Commit. | ADR-0027 |
| `runtime/` | Fire-callback extracted into `r.armTimer(def,instanceID,timerID,fireAt)` (behavior-preserving — byte-for-byte) shared by `perform(ScheduleTimer)` and rehydration; **`Runner.RehydrateTimers(ctx)`** one-shot re-arm (lists armed timers, resolves def via registry, re-arms; skips+counts unresolved defs; requires `WithScheduler`+`WithTimerStore`+`WithDefinitions`). Opt-in via `WithTimerStore`. | |
| `internal/persistence/postgres/` | Migration `0005_timers.sql` (`wrkflw_timers` PK(instance_id,timer_id) + `fire_at` index); `upsertTimer`/`deleteTimer`/`applyTimerOps` applied in `Store.Create`+`Commit` **inside the tx before commit** (atomic with state/journal/outbox); `postgres.TimerStore.ListArmed` (ordered by fire_at). | |
| `persistence/` | `persistence.NewTimerStore(pool) runtime.TimerStore` façade + compile-time assertion. | |
| tests | Mem rehydration e2e (discard runner+scheduler → fresh → `RehydrateTimers` → advance → resume) and Postgres crash-safety e2e (fresh `Store`+`TimerStore`+`Runner` → `RehydrateTimers` + `Tick` → resume, **no manual `TimerFired` deliver**). | |

### Deferred follow-ups
1. **Multi-replica rehydration exclusivity** — two replicas both `RehydrateTimers` → double-arm →
   correct-but-redundant (idempotent re-fire). `FOR UPDATE SKIP LOCKED` / ownership claim is the
   follow-up (shared with the call-link notifier; now top pick #4).
2. **Orphan-row pruning** — defense-in-depth sweep for `wrkflw_timers` rows of instances that reached
   terminal without a clean cancel (the in-tx delete on fire/cancel should keep it clean).
3. **Rehydration observability** — count re-armed / span (align with the observability track).
4. **`Commit` `applyTimerOps` error not wrapped via `mapConflict`** — defensive consistency nit
   (the version CAS returns `ErrConcurrentUpdate` before timer ops run, so not a live bug).

---

## CancelInstance + definition-level cancel actions sub-project — ✅ COMPLETE

Third track from the consolidated backlog (was top pick #1). Built on branch `feat/cancel-instance`.
Design: spec `docs/specs/2026-06-22-cancel-instance-design.md`, plan
`docs/plans/2026-06-22-cancel-instance.md`, **ADR-0028**. 7 SDD tasks + opus whole-branch review.
Gate: `go test -race -p 1 ./...` green, touched pkgs ≥85% (model 95.9%, engine 85.6%, runtime 90.0%,
service 87.5%, transport/rest 91.4%, transport/grpc 87.8%), lint 0, engine/model **import-pure**
(determinism + `Step` purity preserved). NOTE: this track **intentionally modifies engine/model** (the
new command + the `CancelActions` field) — the only track to do so since the engine-core plans.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `model/` | `ProcessDefinition.CancelActions []string` (optional, ordered, opt-in); `Validate` rejects empty entries (`ErrEmptyCancelAction`). `cloneState` untouched (field is on the def, not state). | |
| `engine/` | New fire-and-forget command `InvokeCancelAction{Name, Input}` (NO CommandID; sealed `Command` set). `CancelRequested` emits one per `def.CancelActions` in definition order **before** `FailInstance`; `Step` stays deterministic + pure; empty list ⇒ unchanged. | ADR-0028 |
| `runtime/` | `perform(InvokeCancelAction)` runs the action **best-effort** — logs missing-catalog / unresolved / `Do`-error via slog and ALWAYS returns `(nil, nil)` (no result fed back, never fails the cancel — this is what prevents an `ErrInvalidTransition` re-delivery against the terminal instance). `Runner.CancelInstance` = `Deliver(NewCancelRequested(...))`. | |
| `service/` | `CancelInstanceRequest{InstanceID}`; `Engine.CancelInstance` — `resolveDefinition` → `isTerminal`→`ErrConflict` → `runner.CancelInstance`. Added to the `Service` interface. | |
| `transport/rest` | Admin-gated `POST /admin/instances/{id}/cancel` (default-deny), mirroring resolve-incident; 200 + mapped instance / `WriteHTTPError` (422 `conflict_state` / 404). | |
| `transport/grpc` | `rpc CancelInstance(CancelInstanceRequest) returns (InstanceResponse)`; regenerated `workflowpb`; `server.CancelInstance` mirrors `StartInstance`. Exposed RPC secured by consumer interceptors (no admin-middleware seam — REST/gRPC auth asymmetry). | |

### Deferred follow-ups
1. **Per-active-node cancel handlers** — `CancelActions` is process-level; per-active-node cancel
   handlers (BPMN-native) are future work.
2. **Cancellation propagation parent→child** — `CancelInstance` terminates one instance; propagating
   cancel to child call activities + orphaned-child cleanup is a follow-up (shared with the call-link track).
3. **Cancel reason / audit** — `CancelRequested`/`CancelInstanceRequest` carry no reason.
4. **Cancel-action observability** — span/counter per cancel action (today slog-only).
5. **gRPC admin-interceptor sample** — document/ship an interceptor mirroring the REST admin gate.
6. **`//go:generate` PATH-with-spaces quirk** — the directive in `transport/grpc/errors.go` fails when
   `$PATH` contains directories with spaces (run protoc directly as a workaround); fix = quote the path
   assignment or a Makefile target.

---

## gRPC ResolveIncident + DLQ admin transport sub-project — ✅ COMPLETE

Fourth track from the consolidated backlog (was top pick #1). Built on branch
`feat/grpc-resolveincident-dlq-admin`. Design: spec
`docs/specs/2026-06-22-grpc-resolveincident-dlq-admin-design.md`, plan
`docs/plans/2026-06-22-grpc-resolveincident-dlq-admin.md`, **ADR-0029**. 5 SDD tasks
(visible RED→GREEN per symbol) + opus whole-branch review (**Ready to merge: Yes-with-nits**, no
blockers). Gate: `go test -race -p 1 ./...` green, touched pkgs ≥85% (service 87.5%, persistence
88.0%, transport/grpc 87.2%, transport/rest 90.0%), lint 0, **engine/model production diff ZERO**,
proto regen reproducible (byte-identical re-run).

Closes the Resilience deferred follow-up #1 (transport surface for incident-resolve + DLQ) on
**both** transports.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `service/` | New optional `service.DeadLetterAdmin` interface (`ListDeadLettered`/`Redrive`) — method set identical to `persistence.Relay`'s, so the relay satisfies it with no adapter. Compile-time guard in a black-box `persistence_test`. `Service` interface + `New(...)` UNCHANGED. | ADR-0029 |
| `transport/grpc` | Proto: `ResolveIncident`, `ListDeadLetters`, `RedriveDeadLetters` RPCs + `DeadLetter`/request/response messages (regen via `go generate`). `WithDeadLetterAdmin(service.DeadLetterAdmin)` option (nil-panics). `ResolveIncident` handler mirrors `CancelInstance`; DLQ handlers return `codes.Unimplemented` when unwired, else delegate (`NormalizeLimit`, `deadLetterToProto`). SECURITY doc extended (DLQ RPCs rely on consumer interceptor, like `ListInstances`). | |
| `transport/rest` | `WithDeadLetterAdmin` option (nil-panics). `GET /admin/dead-letters` + `POST /admin/dead-letters/redrive` — **registered only when wired** (else 404), behind the default-deny admin middleware. `deadLetterView` JSON DTO; `{"items":[...]}` / `{"redriven":N}` envelopes. | |

**Key decision (ADR-0029):** DLQ exposed via a *separate optional seam*, not folded into
`service.Service` (DLQ is an outbox-relay concern; MemStore-only consumers have no relay). Per-
transport not-configured behaviour: REST route absent → 404; gRPC → `Unimplemented` (fixed service
contract). Authz stays the consumer's transport-gate responsibility.

### Deferred follow-ups
1. **REST redrive/resolve empty-body fidelity** — a genuinely empty body (Content-Length 0) hits
   `decodeBody`'s `io.EOF`→400; `{}`/`{"ids":[]}` work. Inherited from the resilience deferred list
   (#3); accept EOF as defaults or require a `{}` body. (Not re-fixed here.)
2. **DLQ list pagination** — uses a simple `limit` (no keyset cursor like `ListInstances`); add a
   cursor if dead-letter volume warrants.
3. **casbin-gated DLQ/incident authz** — admin middleware / consumer interceptor remains the v1
   boundary (shared with the resilience deferred #5).
4. **gRPC per-method auth interceptor sample** — ship/document an interceptor mirroring the REST
   admin gate (shared with the CancelInstance deferred #5).

---

## Scope-targeted compensation sub-project — ✅ COMPLETE

Backlog top pick (Correctness). The LARGEST engine/model change. Branch
`feat/scope-targeted-compensation`. Spec `docs/specs/2026-06-23-scope-targeted-compensation-design.md`,
plan `docs/plans/2026-06-23-scope-targeted-compensation.md`, **ADR-0039**. 4 SDD phases (P1/P4 sonnet-
Approved; P2/P3 independent-opus-Approved) + opus whole-branch review that **found & reproduced a real
double-compensation bug (B1)** four per-phase reviews missed → fixed → opus re-review **Ready: Yes**.
Gate: `go test -race ./engine/ ./model/ ./runtime/` green, cov 88.1%, lint 0, **engine touched
deliberately (user-authorized)**.

### What shipped
A BPMN **compensation throw event** for scope-targeted compensation. **(model)** `Node.CompensateRef`
on `KindIntermediateThrowEvent` + `Validate` (`ErrCompensateRefNotFound`). **(engine, reverses
ADR-0013's hoist)** completed sub-process compensation records are **archived by sub-process node id**
(`InstanceState.ArchivedCompensations`) instead of hoisted to root; the cancel/error/admin walk
`consolidateArchiveIntoRoot()` compensates root+archive. Reaching a compensation throw runs that
sub-process's archived compensations (reverse order) via the cursor (`ArchiveKey`/`ResumeNode`/
`ResumeScope`) then **deletes the archive key and resumes** past the throw. **Single ownership** (MOVE
at every hop) ⇒ no double-compensation across throw/cancel/error. B1 fix: a `CancelRequested` mid-throw-
walk **defers** (`PendingCancel`) until the throw drains, then runs a full cancel over the remaining
records — no double-run, no under-compensation.

### Deferred follow-ups
1. **⚠️ HIGH-PRIO (pre-existing, same money-double-run class as B1):** cancel-during-an-in-flight-
   TERMINAL-cancel/error-walk re-emits the in-flight record (ADR-0034 `RootCompensations` walk,
   `ResumeNode==""`). Generalize the `PendingCancel` defer idiom to mid-terminal-walk re-cancel. (See
   the START-HERE Correctness bullet.)
2. Compensation throw concurrent with an open parallel fork — v1 limitation (throw-on-main-flow); add
   a `Validate` rule or pause siblings (ADR-0039 Deferred).
3. Compensation **boundary** events; nested-scope addressing beyond a single node id; `command.go`
   doc drift on the now-partly-built `Compensate` struct (cosmetic).
4. Pre-existing partial-rollback (`toNode!=""`) retains already-compensated root records (no
   `recordCompensation` dedup) → a later full cancel can re-run them — ADR-0035-era, engine-level.

---

## Small API completeness sub-project — ✅ COMPLETE

Backlog (API completeness). Branch `feat/small-api-completeness`. Spec
`docs/specs/2026-06-23-small-api-completeness-design.md`, plan `docs/plans/2026-06-23-small-api-completeness.md`,
**ADR-0038**. 2 SDD tasks (both Approved) + whole-branch review (**Ready: Yes-with-nits**, no blockers).
Gate: full `go test -race -p 1 ./...` green, lint 0, **engine/model diff ZERO**.

### What shipped
**(B)** `runtime.DefinitionRegistry.Lookup` gained `ctx` — threaded through the port + all 3 impls
(Map ignores it, Caching passes it to the backing incl. the singleflight closure, Postgres
`DefinitionStore` uses it instead of `context.Background()`) + all 8 call sites (runner ×3,
call-notifier, service ×3). A breaking port change (external implementers add `ctx`). **(A)** Opt-in
admin-list total-count: `InstanceFilter.IncludeTotal` + `InstancePage.TotalCount`; mem + Postgres
listers run `count(*)` over the same status predicate (no cursor/limit) **only when requested**;
surfaced as REST `GET /admin/instances?total=true`→`total_count` and gRPC
`include_total`→`total_count` (proto field 4, additive). `IncludeTotal=false` = exact prior behaviour.
The **`ended_at` optional** item was confirmed already-satisfied (handlers already conditional;
nullable Timestamp / `*time.Time,omitempty`) — no change.

### Deferred follow-ups
1. Postgres `lister.go` uses the `postgres lister:` error prefix instead of `workflow-postgres:`
   (pre-existing file-wide; the new count error is consistent with its file).
2. Singleflight `Lookup` shares the first caller's ctx among coalesced callers (idiomatic; an
   early-cancelling first caller's flight errors and the others retry).

---

## Observability gaps sub-project — ✅ COMPLETE

Backlog (Observability). Branch `feat/observability-gaps`. Spec
`docs/specs/2026-06-23-observability-gaps-design.md`, plan `docs/plans/2026-06-23-observability-gaps.md`,
**ADR-0037**. 2 SDD tasks (both Approved; Task 2's impl was completed by a subagent that hit a session
limit before committing — controller verified build/test/lint green + committed + applied a review fix)
+ whole-branch review (**Ready: Yes-with-nits**, no blockers). Gate: full `go test -race -p 1 ./...`
green, lint 0, **engine/model diff ZERO**, defaults to noop.

### What shipped
**(A)** A new **public `observability` root package** with a trace-correlating `slog.Handler`
(`NewHandler`/`NewLogger`): mounting it on the logger you pass to the `WithLogger` options gives
trace-correlated logs (auto `trace_id`/`span_id` from the record's span) library-wide, no per-call-site
changes. Depends only on stdlib + `otel/trace`. **(B)** Postgres `Store` instrumentation:
`WithStore{Logger,TracerProvider,MeterProvider}` options (mirroring the relay); `Load`/`Commit` emit
`wrkflw.store.load`/`wrkflw.store.commit` spans + a `wrkflw_store_duration_seconds{op}` histogram;
façade re-exports the options. A `ErrConcurrentUpdate` CAS conflict records a `wrkflw.concurrent_update`
attribute (NOT a span error — it's expected retryable control flow). Defaults to noop.

### Deferred follow-ups
1. `Setup`-that-grabs-OTel-globals (consumer owns SDK setup).
2. `WithStoreLogger` is parity-plumbing today (no store log sites yet).
3. Serialization-failure (SQLSTATE 40001 → `ErrConcurrentUpdate`) still marks the commit span Error
   while a plain version-mismatch does not — minor span-status inconsistency on a rare path.
4. Migrate internal call sites off manual `LogAttrs` onto the public handler; remaining ADR-0019
   observability deferrals (CallNotifier span, instances_active gauge, exemplars, etc.).

---

## casbin policy-admin sub-project — ✅ COMPLETE

Backlog (API completeness). Branch `feat/casbin-policy-admin`. Spec
`docs/specs/2026-06-23-casbin-policy-admin-design.md`, plan `docs/plans/2026-06-23-casbin-policy-admin.md`,
**ADR-0036**. 3 SDD tasks (Task 1 Approved; Task 2 Needs-fixes→fixed = wrong response type + missing
DELETE 403 tests; Task 3 Approved) + opus whole-branch review (**Ready: Yes-with-nits**, no blockers).
Gate: full `go test -race -p 1 ./...` green, lint 0, **engine/model diff ZERO**, casbin confinement
extended + enforced.

### What shipped
A casbin **policy-admin** through an optional `service.PolicyAdmin` seam (`AddPolicy`/`RemovePolicy`/
`ListPolicies` over `p` rules + `AddRole`/`RemoveRole`/`ListRoles` over `g` role-bindings;
`PolicyRule`/`RoleBinding` value types, stdlib-only). `casbinauthz.PolicyAdminFor(authz.Authorizer)
(service.PolicyAdmin, bool)` non-breakingly type-asserts the concrete `*Authorizer` and adapts its
**shared** `*SyncedEnforcer` (mutations immediately authoritative + persist + propagate via the DB
adapter/watcher). REST: `WithPolicyAdmin` + `/admin/policies` & `/admin/role-bindings` (GET/POST/DELETE),
registered only when wired (else 404), behind the default-deny admin middleware. gRPC: `WithPolicyAdmin`
+ 6 RPCs (proto regen) returning `codes.Unimplemented` when unwired. Mirrors the DLQ-admin optional-seam
pattern (ADR-0029). The confinement guard now covers `service/`+transports (casbin-free enforced).

**Closed as non-issue:** casbin adapter/watcher ctx-propagation (ADR-0036 §0 — upstream `persist.Adapter`
has no ctx; watcher already threads its lifecycle ctx).

### Deferred follow-ups
1. gRPC request validation (empty `rule`/`binding` accepted → empty-string policy; consistent with the
   rest of the transport which does no request validation).
2. Richer Privilege modeling / casbin ABAC-in-matchers / `FilteredAdapter`+`WatcherEx` (separate backlog).

---

## Per-node cancel handlers sub-project — ✅ COMPLETE

Backlog top pick #2 (follow-up to ADR-0028/0032). Branch `feat/cancel-handlers`. Spec
`docs/specs/2026-06-23-cancel-handlers-design.md`, plan `docs/plans/2026-06-23-cancel-handlers.md`,
**ADR-0035**. 2 SDD tasks (Task 1 Approved + 3 controller-applied Minors; Task 2 test-only) +
whole-branch review (**Ready: Yes-with-nits**, no blockers). Gate: full `go test -race -p 1 ./...`
green, lint 0, **model diff = ONE additive field**, `Step` pure/deterministic, no runtime change.

### What shipped
`model.Node.CancelHandler string` — an optional per-node cleanup action run **fire-and-forget** for
each **active/in-flight** node when the instance is cancelled, emitted via the existing ADR-0028
`InvokeCancelAction` path (no runtime change, no new command, no migration). Collected from the live
tokens (scope-aware via `defForScope`) *before* the compensation/immediate branch clears them, and
ordered `[def.CancelActions…, per-node CancelHandlers…, (compensation walk | FailInstance)…]`. The
**definition-scoped cancel handler is the existing `def.CancelActions`** (ADR-0028) — documented, not
rebuilt. Three distinct activity-lifecycle hooks kept separate: error (failed→route), cancel
(active→cleanup, this), compensation (completed→undo, ADR-0034). Back-compat: no `CancelHandler` set
⇒ byte-for-behaviour identical.

### Deferred follow-ups
1. A distinct definition-scoped *handler* shape (vs the `CancelActions` action-name list) if a future
   need arises — `CancelActions` is the def-scoped handler for now (flagged for spec review).
2. Cosmetic test nits (ordering-assertion coverage for def.CancelActions-before-per-node; redundant
   clockwork import alias).

---

## Compensation on error/cancel sub-project — ✅ COMPLETE

Top pick (Correctness) — first user-authorized ENGINE change of the autonomous run. Branch
`feat/compensation-on-error-cancel`. Spec `docs/specs/2026-06-23-compensation-on-error-cancel-design.md`,
plan `docs/plans/2026-06-23-compensation-on-error-cancel.md`, **ADR-0034**. 3 SDD tasks (each
Approved; Task 1 had a sentinel-doc fix) + opus whole-branch review (**Ready: Yes-with-nits**) which
**caught a real publicly-reachable idempotency bug** (re-delivered `CancelRequested` re-ran the
compensation walk) — **fixed pre-merge** (commit `75a4b1a`). Gate: full `go test -race -p 1 ./...`
green, lint 0, **model production diff ZERO**, `Step` pure/deterministic, `cloneState` extended.

### What shipped
On an **unhandled terminal error** and on **cancel**, the engine now runs the existing reverse-order
compensation walk **before terminating** (when `RootCompensations` is non-empty). Mechanism:
`compensationCursor` gained `FinalStatus`/`FinalErr`; `beginCompensation` extracted; `stepCompensationFinish`
applies the outcome (error⇒`StatusFailed`+`FailInstance{errorCode}`; cancel⇒`StatusTerminated`+
`FailInstance{cancelled}`; admin full-rollback⇒unchanged). `ActionFailed` during compensation routes
to advance (best-effort skip). `InvokeCancelAction` (ADR-0028) still fires alongside. **Idempotency
fix:** `RootCompensations` cleared on full-rollback finish so a re-delivered cancel is a clean no-op.
Empty-records + admin-compensation + retry/boundary/incident paths are byte-for-behaviour unchanged.

### Deferred follow-ups
1. **Partial-rollback (`toNode != ""`) record non-clearing** — admin-only, no public trigger; records
   after ToNode aren't cleared (pre-existing). Low priority.
2. **Compensation-action retry/incident** on repeated failure (today best-effort skip).
3. `FailInstance` now emitted at end of walk (after compensation), not before — documented semantic shift.

---

## Ops-hardening trio sub-project — ✅ COMPLETE

Non-engine bundle (Production-hardening + Test/doc). Branch `feat/ops-hardening`. Spec
`docs/specs/2026-06-23-ops-hardening-design.md`, plan `docs/plans/2026-06-23-ops-hardening.md`,
**ADR-0033**. 2 SDD tasks (both Approved, only cosmetic Minors) + whole-branch review (**Ready:
Yes-with-nits**, no blockers). Gate green, ≥85% touched pkgs, lint 0, **engine/model diff ZERO**.

### What shipped
1. **`Deduper.Prune(ctx, before time.Time) (int64, error)`** (internal + `persistence.Deduper`
   interface) — `DELETE FROM wrkflw_processed_message WHERE processed_at < $1`; caller supplies an
   absolute cutoff (no clock dep, no migration); operator-scheduled retention for the dedup table.
2. **`MarkNotified` clock injection** — uses the store's injected `c.clk.Now().UTC()` (default
   `clock.System()`) instead of wall-clock; now deterministic under a fake clock.
3. **`AdvisoryLockOwnership` close guard** — new `ErrOwnershipClosed` sentinel + a `closed bool`
   guard so post-`Close` `Acquire`/`Release` return the sentinel (not "undefined behaviour"); `Close`
   is idempotent.

### Deferred follow-ups (cosmetic, from reviews)
- `Prune` impl godoc terser than the interface godoc; add a façade-level `Prune` test (currently
  direct + compile-time assertion); ownership guard test could acquire-then-close for defence-in-depth.
- `ErrOwnershipClosed` lives in `internal/persistence/postgres` — consumers using the façade
  `runtime.Ownership`+`io.Closer` can't `errors.Is` it directly (intentional; re-export if needed).

---

## Cancellation propagation parent→child sub-project — ✅ COMPLETE

Top pick (Production-hardening). Branch `feat/cancellation-propagation`. Spec
`docs/specs/2026-06-22-cancellation-propagation-design.md`, plan
`docs/plans/2026-06-22-cancellation-propagation.md`, **ADR-0032**. 2 SDD tasks (Task 1 Approved after
an I1 fix; Task 2 Approved) + opus whole-branch review (**Ready: Yes-with-nits**, no blockers, all 6
merge-gate criteria pass). Gate: `go test -race -p 1 ./...` green, touched pkgs ≥85%, lint 0,
**engine/model production diff ZERO** (pure runtime).

### What shipped
`runner.CancelInstance` now propagates cancellation down the **async call tree**: it terminates the
instance (as before), then — when `WithCallLinks`+`WithDefinitions` are wired — recursively cancels
each still-running child call-activity instance, **best-effort** (every list/load/lookup/child-deliver
error logged + swallowed; the parent cancel never fails). New port read method
`CallLinkStore.ListRunningChildren(parentID)` (Mem + Postgres) + partial index migration `0007`
(`(parent_instance_id) WHERE status='running'`). A single **shared `visited` set** threads the whole
recursion (`propagateCancel` recurses into itself, not back through `CancelInstance`) → no infinite
recursion on cyclic data and no diamond double-cancel (guarded by `TestCancelPropagationDiamond`).
**Parent-first ordering** avoids a notifier resume race; a cancelled child reaches `StatusTerminated`
→ its call link flips terminal (so it isn't re-listed and the notifier resolves the terminated parent
via `ErrTokenNotFound`). Gated: `callLinks==nil` or `defsReg==nil` ⇒ exact prior behaviour.

### Deferred follow-ups
1. **Per-active-node cancel handlers** — a `Node`-scoped cancel action (vs process-level
   `CancelActions`); a model+engine change (top pick #2). Confirm scope first.
2. **Sync call activities** need no propagation (a sync child completes inside `perform`).
3. Cosmetic: unused `pool` param in a postgres test helper (mirrors a pre-existing pattern);
   `sort.Slice`→`slices.SortFunc` in mem_calllink; test-comment cruft.

---

## Call-link notifier lease exclusivity sub-project — ✅ COMPLETE

Top pick (Production-hardening, call-link half of "multi-replica exclusivity"). Branch
`feat/calllink-lease-exclusivity`. Spec `docs/specs/2026-06-22-calllink-lease-exclusivity-design.md`,
plan `docs/plans/2026-06-22-calllink-lease-exclusivity.md`, **ADR-0031**. 3 SDD tasks (implementer +
reviewer per task, all Approved 0 Crit/Imp) + opus whole-branch review (**Ready: Yes**, no blockers,
cross-layer lease semantics confirmed consistent, at-least-once preserved). Gate: `go test -race -p 1
./...` green, persistence 92.6% / runtime / internal-postgres ≥85%, lint 0, **engine/model diff ZERO**.

### What shipped
Opt-in **lease**-based multi-replica exclusivity for the async call-link notifier. The notifier's
`deliver` runs outside the claim tx, so `FOR UPDATE SKIP LOCKED` alone can't gate it; instead a
`claimed_at`/`claimed_by` lease (migration `0006`) reserves a row for a TTL so other replicas skip it
across the claim→deliver→`MarkNotified` window. Store-level config (`WithCallLinkLease(owner, ttl)` +
`WithCallLinkClock`) on **both** `runtime.MemCallLinkStore` and the Postgres `CallLinkStore`; the
`runtime.CallLinkStore` port and `CallNotifier` are UNCHANGED. Postgres claim is
`UPDATE…FROM(SELECT…WHERE notified_at IS NULL AND (claimed_at IS NULL OR claimed_at<=now-ttl) ORDER BY
child_instance_id FOR UPDATE SKIP LOCKED [LIMIT])…RETURNING`. `ttl=0` (default) = exact prior
behaviour (backward-compatible). Façade re-exports the options on `persistence.NewCallLinkStore`;
leased-notifier wiring composes via `NewCallLinkStore(pool, WithCallLinkLease(...))` → `runtime.NewCallNotifier`.

### Deferred follow-ups
1. **Multi-replica TIMER exclusivity** — the harder half (see top pick #1): needs a claim-renew-failover
   loop replacing per-replica gocron arming. Double-fire already correct via engine CAS; optimization only.
2. **`persistence.NewCallNotifier` lease ergonomics** — can't take a second variadic (Go); leased
   notifier composes via `NewCallLinkStore` (documented). A non-variadic config alternative is optional polish.
3. **`MarkNotified` clock injection** — uses `time.Now().UTC()` not the injected clock (pre-existing;
   `notified_at` isn't in the lease predicate so determinism is unaffected). Shared with the
   `MarkNotified` clock-injection backlog item.
4. **transient-failure retry latency** under lease = `ttl` (vs poll interval at ttl=0) — documented trade-off.

---

## Reachability + conservative fork-join validation sub-project — ✅ COMPLETE

Fifth track from the consolidated backlog (was top pick #1). Built on branch
`feat/reachability-forkjoin-validation`. Design: spec
`docs/specs/2026-06-22-reachability-forkjoin-validation-design.md`, plan
`docs/plans/2026-06-22-reachability-forkjoin-validation.md`, **ADR-0030** (follow-up to ADR-0014,
which deferred fork-join pairing over false-positive risk). 2 SDD tasks (visible RED→GREEN, inline) +
opus whole-branch review (**Ready: Yes-with-nits**, zero blockers, **no false-positive risk found** —
the soundness argument was confirmed). Gate: `go test -race ./...` green, `model` 96.6%, lint 0,
**engine production diff ZERO** (pure `model/`).

### What shipped (both in `model/validate.go`, recursing into sub-processes via the existing `validate()`)

| Rule | What | Notes |
|---|---|---|
| `ErrUnreachableNode` | Every node must be reachable from the **single** start event (forward BFS), with boundary events seeded from their reachable host (to a fixpoint) and event-sub-processes treated as event-triggered roots. Runs only when exactly one start (else the start-count error already fires). | model-local `forwardReachable` helper (no engine import) |
| `ErrUnpairedJoin` | **Parallel-join-only** conservative pairing: a `KindParallelGateway` join is flagged iff **no** parallel/inclusive split can deliver ≥2 concurrent tokens toward it (via `hasConcurrencySource`). Exclusive/event joins (first-arrival) and inclusive joins (engine self-adjusts via runtime reachability) are excluded — they don't deadlock. Skipped for unreachable joins and when start count is ill-defined. | ADR-0030 |

**Key decisions (ADR-0030):** false-positive-averse by design (validation runs at load; bias to
false-negatives). Engine semantics that shaped it: a **non-gateway node with multiple outgoing flows
follows only its *first* flow** (`moveAlongSingleFlow` takes `out[0]`), so only parallel/inclusive
gateway splits create concurrency. This finding exposed (and fixed) a latently-unsound existing test
fixture (`start → a,b → parallel-join` would deadlock). **`model.Validate` has zero production
callers** — blast radius is the `model` package's own fixtures only.

### Deferred follow-ups
1. **No-path-to-end / sink detection** — every non-end node can reach some end event (must account for
   boundary/error escapes); higher false-positive risk, deferred.
2. **Conditional-deadlock pairing** — an exclusive choice *inside* a parallel region that can starve a
   join branch is not caught (the documented false-negative bias). Full structured-soundness / SESE
   region analysis is the complete solution.
3. **Inclusive/exclusive join pairing** — intentionally not checked (those joins don't deadlock);
   revisit only if engine firing semantics change.

---

## Status: engine-core sub-project #1 is COMPLETE ✅ (historical — see resume point above for current state)

**All 8 engine-core plans are merged to `main`.** The pure engine core + reference runtime now
covers the broad BPMN scope. Total line coverage **88.2%**; `go test -race ./...` green;
`golangci-lint run ./...` clean.

| Plan | Scope | Merge commit | Status |
|---|---|---|---|
| 1 | Foundations: model+Validate, clock, Trigger/Command, InstanceState, pure `Step` (linear), action catalog, runtime + fakes | `4a2b092` | ✅ merged |
| 2 | Gateways: `expreval`, Exclusive (XOR), Parallel (AND fork+join) | `6d3733d` | ✅ merged |
| 3 | Inclusive (OR) gateway: OR-fork + reachability OR-join | `51c4f44` | ✅ merged |
| 4 | Human tasks: `authz`, `humantask`, AwaitHuman, claim/reassign/complete + audit + bucket | `e9a9d65` | ✅ merged |
| 5 | Timers & SLA: ScheduleTimer/TimerFired, timer intermediate, SLA breach, in-wait reminders, `MemScheduler` | `320dae1` | ✅ merged |
| 6 | Events & event-based gateway: signal/message catch+throw, first-event-wins, boundary timer/signal, `SignalBus` | `8499c32` | ✅ merged |
| 7 | Sub-processes & call activity: scope tree, scope-aware `drive`, embedded + event sub-process, call activity, `DefinitionRegistry` | `2be77e9` | ✅ merged |
| 8 | Errors, compensation & micro-step: error end/boundary error propagation, compensation rollback, cancel, real Micro mode | `f4b2b85` | ✅ merged |

The plan files remain under `docs/plans/*.md` as the record of what was built. ADRs `0001`–`0005`
in `docs/adr/`. The design spec `docs/specs/2026-06-20-engine-core-design.md` is still the contract.

## What `wrkflw` is

A library-first, BPMN-flavored Go workflow engine (Go 1.25), shipped as an importable module (no
daemon we own). Authoritative references — **read these first**:

- `REQUIREMENTS.md` — original loose requirements.
- `CLAUDE.md` — project rules (TDD discipline, root-package layout, locked tech stack, required Go
  skills). **Binding.**
- `docs/specs/2026-06-20-engine-core-design.md` — the engine-core design **spec** (the contract).
  When a plan and the spec disagree, the spec wins.
- ADRs: `0002` (pure stepper returning **Commands** driven by **Triggers**), `0003` (time via
  in-repo `clock.Clock`, impl by clockwork at the edge), `0004` (public packages at module root, no
  `pkg/`), `0005` (Runner functional-options construction).

## Core invariants (never violate — still binding for any future engine work)

1. **Pure core.** `engine` and `model` import only stdlib (+ `model`/`authz`/`humantask`/`expreval`
   as the spec allows). No transport/storage/bus/time-vendor in the core.
2. **No clock in the engine.** `Step` never calls `time.Now()`. Time enters as `Trigger.OccurredAt`;
   `FireAt = OccurredAt + duration`. `clockwork` only enters via the runtime's `clock.Clock`/`Scheduler`
   (and is imported **only in test files**; `clock/clock.go` is the edge adapter).
3. **`Step` is deterministic** — identical `(state, trigger)` ⇒ identical `(state, commands)`. All IDs
   (command `-c`, token `-t`, task `-h`, timer `-tm`, scope `-s`, gateway sentinel `evtgw:`, call
   child `-sub-`) come from in-`InstanceState` counters, never randomness or the clock. Flows in
   **definition order**; all bookkeeping slices iterated in slice order; no map iteration into command order.
4. **`Step` is pure** — never mutates its input `InstanceState` (`cloneState` deep-copies every slice +
   nested map: Tokens/Payload, Variables, History, Tasks, Timers, ArmedEvents, Boundaries,
   EventSubprocesses, Scopes+Compensations, RootCompensations, the compensation cursor). **Extend
   `cloneState` for every new state field.**
5. **`Step` signature is stable:** `Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)`.
6. **Sealed sets:** `Trigger` (`isTrigger()`+`OccurredAt()`) and `Command` (`isCommand()`) are closed;
   adding a variant is a deliberate edit in `engine`.

## Quick map of the merged code

- `model/` — `ProcessDefinition`, `Node` (all BPMN kinds incl. gateways/events/boundary/sub-process/
  call-activity), `SequenceFlow`, lookups, recursive `Validate` (+ sentinels, cycle guard).
- `clock/` — `Clock` interface + `System()`; clockwork is the fake-clock edge.
- `expreval/` — the only `expr-lang/expr` wrapper: `EvalBool`/`EvalDuration`/`EvalString` (memoized).
- `authz/` — `Actor`, `AuthzSpec`, `Authorizer` (+ `AllowAll`, `RoleAuthorizer`).
- `humantask/` — `HumanTask`, `TaskState`, `ActorResolver`, `TaskStore` (+ `MemTaskStore`, `StaticActorResolver`).
- `action/` — `ServiceAction`, `Catalog`, `MapCatalog`, `Func`.
- `engine/` — `trigger.go`/`command.go` (sealed sets), `state.go` (`InstanceState` + all bookkeeping +
  helpers + `cloneState`/`Clone`), `step.go` (`Step`, scope-aware `drive`, every node-kind case, all
  the gateway/event/boundary/timer/human/sub-process/error/compensation/micro logic), `conditions.go`.
- `runtime/` — `runner.go` (`Runner`, `NewRunner(cat, clk, store, jnl, out, ...Option)` with
  `WithHumanTasks`/`WithScheduler`/`WithSignalBus`/`WithDefinitions`, `Run`, `Deliver`, `deliverLoop`,
  `perform`), `ports.go`, `memory.go`, `scheduler.go` (`MemScheduler`), `broadcast.go` (`SignalBus`),
  `definition_registry.go`, `taskservice.go`.

## Tracked follow-ups (discovered during execution — address before / during productionization)

These are deliberately deferred, not bugs in the shipped scope. The most important first:

1. **Nested-scope compensation — DONE (ADR-0013).** On regular sub-process exit, the closing
   scope's `Compensations` are now hoisted into the parent scope's compensation list before
   `closeScope` discards the child scope, so `CompensateRequested` can roll back activities that
   ran inside a now-closed sub-process. The `Correctness & tests hardening` sub-project
   (Task 1) implemented and tested this via `hoistCompensations(childID, parentID)` in `engine/step.go`.
2. **`Compensate` command is reserved for scope-targeted use.** `Compensate{ScopeID,FromNode}`
   is in the sealed set but not emitted or consumed (godoc says so honestly). It is reserved to
   be wired as the scope-targeted rollback command (companion to item 1 above) — a deliberate
   deferred follow-up.
3. **Async call activity.** `perform StartSubInstance` runs the child **synchronously** via `r.Run`; a
   child that parks (human task/timer/signal) returns a clear "synchronous runner does not support
   parked children" error. True async call activity (parent stays parked; `SubInstanceCompleted`
   delivered when the child finishes independently) is a later architectural change. Child instance id
   is linear (`<parent>-sub-c<n>`); depth guard = 64.
4. **Typed/paired gateway validation — PARTIALLY DONE (ADR-0014).** The mixed-gateway rule
   (a node may not mix both `KindExclusiveGateway` incoming/outgoing flows with parallel-join
   semantics) was added to `model.Validate` as `ErrMixedGateway` in the `Correctness & tests
   hardening` sub-project (Task 2). Full diverging-vs-converging structural validation (diamond
   pairing, reachability checks) remains a deferred follow-up.
5. **Inner-scope topology tests — DONE.** Tests for boundary-event, event-based gateway, inclusive
   gateway, and SLA-timer scope propagation *inside* a sub-process were added in the `Correctness
   & tests hardening` sub-project (Task 6). A `fireBoundaryArm` scope-resolution bug was found
   and fixed as part of this work (commit `82badcd`): the boundary outgoing flow was being resolved
   from the root definition instead of the containing sub-process scope.
6. **Retry/backoff/poison executor — DONE (ADRs 0015–0018).** Built in the `Resilience` sub-project:
   engine-modeled retry executor (retries ride the timer machinery; runtime-recorded jitter keeps
   `Step` deterministic), catch-flow→error-boundary→incident exhaustion, outbox relay per-row poison
   isolation + dead-letter quarantine, and idempotency (stable action key + `Deduper`). See the
   "Resilience (retry/backoff/DLQ) sub-project" section below.
7. **Minor test hardening** (non-blocking): a few `*_example_test.go`-bundled unit tests could move to
   same-named files (project convention is 1:1, see the test-file-naming memory); root-level event
   sub-process and message-arm-gateway paths have light coverage.
8. **Pre-existing flaky singleflight test — ✅ FIXED (2026-06-22, merge see below).** Root cause was a
   check-then-act gap in `CachingDefinitionRegistry.Lookup` between the fast-path cache check and
   `singleflight.Group.Do`: a straggler that missed the fast path could start a fresh flight after the
   first flight had already cached and freed the key, issuing a redundant `backing.Lookup` (2–4 calls
   observed under `-race`). Fixed by double-checking the cache at the top of the `Do` closure so any
   flight running after the cache is populated short-circuits — collapsing stragglers to exactly one
   backing call regardless of scheduling. Verified 500× `-race`. (Not a "timing-sensitive barrier" in
   the test as previously assumed — a real TOCTOU in the production code.)

## Persistence (PostgreSQL) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/persistence-postgres` (range `fb39a87..9f9ab0f`), reviewed (per-task + opus
whole-branch), merged to `main`. All 10 plan tasks complete. Design: spec
`docs/specs/2026-06-21-persistence-postgres-design.md`, plan `docs/plans/2026-06-21-persistence-postgres.md`,
ADRs 0006–0008.
Gate: `go test -race ./...` green, total coverage 87.3% (model 96.2%, runtime 94.6%,
`internal/persistence/postgres` 86.0%, `persistence` 100%), lint clean (0 issues), no forbidden imports
(`watermill`/`casbin`/`gocron`/`clockwork`) in production code.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` port collapse | Replaced 3 separate in-memory ports (`StateStore`/`Journal`/`OutboxWriter`) with a single transactional `Store` (`Create`/`Load`/`Commit`) + `JournalReader`; per-applied-trigger atomic commit with optimistic-version CAS (`ErrConcurrentUpdate`); outbox events derived by the pure `outboxEventsFor` helper. `runtime.Publisher`/`CachingDefinitionRegistry` (read-through TTL + singleflight; definitions immutable per `id:version` so no invalidation). | ADR-0007; `MemStore` is the in-memory reference impl |
| `internal/persistence/postgres/` | Postgres `Store`: transactional snapshot-JSONB writes with optimistic-CAS + journal + outbox in one tx; `DefinitionStore`; broker-agnostic outbox `Relay` (`FOR UPDATE SKIP LOCKED`, at-least-once); goose migrations — **4 tables: `wrkflw_instances`, `wrkflw_journal`, `wrkflw_outbox`, `wrkflw_definitions`**; trigger codec (`MarshalTrigger`/`UnmarshalTrigger`, exhaustive over the 13 sealed variants) | ADR-0006, ADR-0008 |
| `persistence/` root façade | `OpenPostgres`, `Migrate`, `NewRelay`, `NewDefinitionStore` — **all return stable port/interface types** (`Store`, `DefinitionStore`, `Relay` interfaces), never internal concrete structs (ADR-0008); sentinel aliases `ErrInstanceNotFound`/`ErrConcurrentUpdate` | ADR-0008 |
| `database/` | `RunTestDatabase` testcontainers helper — shared by all Postgres integration tests; returns a `*pgxpool.Pool` backed by `postgres:17` | test-helper-only package (0% own coverage is expected) |
| `model/` | `NodeKind` now has name-based JSON (`MarshalJSON`/`UnmarshalJSON`) so the persisted definition format is stable against iota reordering | whole-branch-review fix |

### Key design decisions (ADRs)
- **ADR-0006** — snapshot-as-JSONB storage shape (one row per instance: JSONB `snapshot` source-of-truth + plain engine-written projected columns `status`/`def_id`/`version`/timestamps for indexed admin queries; no tree normalization in v1).
- **ADR-0007** — per-step transactional `Store`: three runtime ports collapsed into one so `Commit` writes snapshot + journal + outbox atomically per applied trigger; optimistic CAS keeps the engine pure (no concurrency token in `InstanceState`).
- **ADR-0008** — `persistence` (root façade) over `internal/persistence/postgres` (impl): consumers import only the root package, which exposes interface types; all pgx/goose wiring stays unexported.

### Relay design note
The `Relay` is broker-agnostic: it polls the outbox with `FOR UPDATE SKIP LOCKED` and calls a `runtime.Publisher` interface (re-exported as `persistence.Publisher`). The **Eventing** sub-project will provide a watermill-backed `Publisher` — watermill is never imported here.

### Deferred follow-ups (deliberate, not bugs — backlog for later sub-projects)
1. **Owned-instance state cache** — instance state is fetched from Postgres on every `Run`/`Deliver`. A *single-writer* (instance-leased) cache is the only safe way to cache mutable state (a version-CAS protects writes but stale reads already fire side-effects); deferred. v1 caches only immutable definitions.
2. **History / snapshot cap** — the `snapshot` JSONB grows with inline `History`; an optional retention cap is deferred (the journal `wrkflw_journal` remains the unbounded audit source).
3. **LISTEN/NOTIFY relay trigger** — relay polls on a fixed interval; a Postgres `LISTEN`/`NOTIFY` push would cut latency/DB load (layered on the poll fallback).
4. **Per-aggregate relay ordering** — `SKIP LOCKED` gives throughput, not strict per-instance order; partition claiming by `instance_id` if strict in-order delivery is needed.
5. **Retry/DLQ + relay head-of-line** — full-batch rollback on a publish error means a persistent poison event blocks its batch (at-least-once intact). Poison isolation / retry-backoff executor is the resilience sub-project.
6. **Parked-async persistence resume e2e — DONE.** `TestPostgresParkedTimerResumesAfterReload`
   and `TestPostgresParkedBoundaryResumesAfterReload` (in `internal/persistence/postgres/resume_test.go`)
   added in the `Correctness & tests hardening` sub-project (Task 5): parks on a timer/boundary,
   reloads from Postgres via a brand-new `Store`, advances the fake clock, and resumes to
   `StatusCompleted`. Proves the JSONB round-trip of `token.AwaitCommand` and `Boundaries`.
7. **TOAST / fillfactor tuning** — the per-transition snapshot rewrite causes TOAST write amplification; lower `fillfactor` on `wrkflw_instances` + autovacuum tuning is a DBA step.
8. **Numeric fidelity** — process-variable integers round-trip from JSONB as `float64` (standard `encoding/json`, documented spec §7); `json.Decoder.UseNumber()` is the escape hatch if a consumer needs int fidelity.
9. **Instance-snapshot int enums** — `Status`/`TokenState`/`TimerKind` still serialize as ints in the snapshot (self-consistent within a version, unlike the now-name-based `NodeKind`); name-encode them too if cross-version snapshot stability is ever needed.
10. **`DefinitionRegistry.Lookup` lacks `ctx`** — the Postgres impl uses `context.Background()`; adding `ctx` to the port is a follow-up.

---

## Scheduling (gocron) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/scheduling-gocron` (HEAD `87c0ca6`, including the whole-branch-review
fix wave). Design: spec
`docs/specs/2026-06-21-scheduling-gocron-design.md`, plan `docs/plans/2026-06-21-scheduling-gocron.md`,
ADR-0009.
Gate: `go test -race ./...` green, `internal/scheduling/gocron` 87.5%, `scheduling` 85.7%,
`runtime` 94.5%, lint clean (0 issues), gocron not imported from `engine`/`runtime`/`model`
production code, clockwork not in `engine`/`runtime`/`model` production code.

### Whole-branch-review fixes (post-implementation, HEAD `87c0ca6`)

- **R4a (`runtime/memstore.go`)** — `MemStore` is now goroutine-safe (`sync.RWMutex` guards
  all five methods). The async scheduler makes concurrent `Deliver` real, so the e2e
  `syncStore` wrapper was removed in favour of `runtime.NewMemStore()` directly.
- **R4b (`runtime/runner.go`)** — the timer-fire callback no longer silently drops
  `TimerFired` on a CAS conflict: it now retries `Deliver` (reload-per-attempt) up to 5 times
  on `ErrConcurrentUpdate`, logging loudly if all attempts are exhausted. Non-CAS errors keep
  the single log-and-return behaviour.
- **Minor (`internal/scheduling/gocron/scheduler.go`)** — a non-future `fireAt` now fires
  immediately (`gocron.OneTimeJobStartImmediately()`) instead of being dropped.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `internal/scheduling/gocron/` | `GocronScheduler` — implements `runtime.Scheduler` backed by gocron v2.21.2; mutex-guarded `timerID→uuid` map; `Schedule` replaces any existing job for the same timer (cancel + re-add); `Cancel` is a no-op for unknown IDs (`ErrJobNotFound`-safe); `Close` calls gocron `Shutdown`; `AfterJobRuns` hook cleans the map entry after the job fires so the map stays bounded. Shares the same `clockwork.Clock` instance as the `Runner` so a single `FakeClock.Advance` drives both the engine and the scheduler in tests (ADR-0003). | ADR-0009 |
| `scheduling/` root façade | `NewScheduler(clock, ...Option) (runtime.Scheduler, io.Closer)` — consumers import only this root package; compile-time `var _ runtime.Scheduler` + `var _ io.Closer` assertions guard the contract. Never exposes internal gocron types. | ADR-0009 |
| `runtime/` | `MemScheduler` retained — tests that require only the pure engine (no gocron dependency) still use it. `runner.go` already accepts `runtime.Scheduler`. The whole-branch-review fix wave added two runtime changes (see "Whole-branch-review fixes"): a goroutine-safe `MemStore` (R4a) and bounded retry-with-reload on the timer-fire `Deliver` (R4b). | |
| Tests | Unit tests for `GocronScheduler` use `clockwork.NewFakeClock`, `BlockUntilContext` arm barrier before `Advance`, and synchronize on actual callback execution via WaitGroup/channel (not on `Advance` returning). Capstone e2e: one shared fake clock is both the runner's `clock.Clock` and the scheduler's `clockwork.Clock`; a timer-waiter process drives start→fire→resume→`StatusCompleted`. | |

### Key design decision (ADR)

- **ADR-0009** — `scheduling` root façade over `internal/scheduling/gocron` (impl): the same
  layer pattern as ADR-0008 for persistence — consumers import only the façade, which returns
  `runtime.Scheduler` + `io.Closer`; all gocron wiring stays unexported. Ensures gocron is
  **never imported transitively from engine/runtime/model code**.

### Deferred follow-ups

1. **Timer-fire CAS-drop [HIGH]** — `runner.go`'s `ScheduleTimer` fire callback only LOGS
   `ErrConcurrentUpdate`, silently dropping the `TimerFired` trigger under concurrent `Deliver`.
   Needs retry-with-reload on the fire path so a lost timer-fire is retried rather than silently
   discarded.
2. **`runtime.MemStore` not goroutine-safe** — async schedulers make concurrent `Deliver` real
   (the fire callback runs on a gocron goroutine while the caller may be in `Run`/`Deliver`).
   Add a mutex or a `runtime.NewSyncStore` wrapper so MemStore is safe under real concurrency.
3. **Rehydration on restart** — timers persist in `InstanceState.Timers` (snapshot-JSONB via
   the Postgres store) but full re-arming on startup requires a persistence "list pending timers"
   enumeration query. Not built in v1: a restart loses in-memory gocron jobs until rehydration
   lands. The Persistence `Store` is the prerequisite.

---

## Authorization (casbin) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/authz-casbin` (HEAD `a7edee0`). Design: spec
`docs/specs/2026-06-21-authz-casbin-design.md`, plan `docs/plans/2026-06-21-authz-casbin.md`,
ADR-0010.
Gate: `go test -race ./...` green, total coverage **87.5%** (`internal/authz/casbin` 92.0%,
`casbinauthz` 85.7%, `authz` 100%, `runtime` 94.5%, `humantask` 100%), lint clean (0 issues),
casbin not imported outside `internal/authz/casbin` + `casbinauthz`, pinned at `v2.135.0`.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `internal/authz/casbin/` | `*Authorizer` wrapping `*casbin.SyncedEnforcer` — **hybrid evaluator**: (1) role check with hierarchy via `GetImplicitRolesForUser` (degrades to `RoleAuthorizer` any-match with no `g` policy); (2) resource-privilege via `Enforce(sub, obj, act)` per privilege (activates the previously-reserved `AuthzSpec.Privileges` field); (3) attribute predicate via `expreval` over `{actor, vars}` (preserves the `expr-lang` dialect — no govaluate fork). Empty spec → allow. Deny / failed check → `authz.ErrNotAuthorized` (fail-closed). Casbin engine error → plain wrapped error (distinguishable from "policy says no"). `SyncedEnforcer` makes concurrent authorizations race-safe. | ADR-0010; only casbin imports in the codebase |
| `casbinauthz/` root façade | `NewCasbinAuthorizer(e *casbin.SyncedEnforcer) authz.Authorizer` (consumer-owned enforcer) + `NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error)` (builds enforcer internally; `DefaultModel` used when `modelText` is empty). Both **return the `authz.Authorizer` interface** — no internal-concrete leak. `ReloadPolicy() error` available via type assertion for hot-reload. Compile-time `var _ authz.Authorizer` guard. | ADR-0008 template; ADR-0010 |
| `humantask/` | Added `Vars map[string]any` field to `HumanTask` to carry a process-variable snapshot. | Prerequisite for attribute-based-over-data-variables |
| `runtime/` | `perform engine.AwaitHuman` snapshots a **defensive copy** of `st.Variables` into the `HumanTask.Vars` at task creation time; `TaskService.Claim`/`Reassign`/`Complete` pass `task.Vars` (not `nil`) to `Authorize`. Makes attribute eligibility deterministic and auditable (evaluated against task-creation-time state). | ADR-0010 §Vars plumbing |
| `authz/` | **Unchanged** — `AllowAll`/`RoleAuthorizer` retained as pure built-ins; casbin NOT added. | The pure `authz` package stays stdlib + expreval only |

### Key design decision (ADR)

- **ADR-0010** — hybrid casbin + expr evaluator behind the `authz.Authorizer` port, following
  the ADR-0008 façade/internal template. Preserves the `expr-lang` dialect for attribute
  predicates (avoiding a govaluate fork); adds role-hierarchy and resource-privilege on top.
  `SyncedEnforcer` for race safety. Deny → fail-closed `ErrNotAuthorized`. `AllowAll`/`RoleAuthorizer`
  retained as pure built-ins — casbin is an additional implementation, not a replacement.

### Deferred follow-ups

1. **DB policy adapter** — v1 accepts model+policy as strings or a consumer-built `SyncedEnforcer`. A **pgx/gorm/sqlx casbin adapter** loading policy from the `wrkflw` Postgres database (with a watcher for multi-node reload) is a follow-up. The façade is adapter-agnostic — landing this requires only a new `casbinauthz.NewCasbinAuthorizerFromDB(pool, ...)` constructor.
2. **casbin ABAC-in-matchers** — the `Attribute` predicate today runs via `expreval` *outside* the casbin matcher (to keep the expr dialect). A casbin-native ABAC path (govaluate expression in the matcher, vars injected as subject attributes) is a viable alternative for consumers who prefer unified policy files; deferred because it forks the expression dialect.
3. **Shallow snapshot caveat** — `HumanTask.Vars` is a top-level-keys defensive copy of `st.Variables`: top-level scalars/strings/bools are independent copies, but any nested `map[string]any` values remain aliased to the instance state. Only top-level scalars are safe from mutation; deeply nested vars may reflect later engine writes. Full deep-copy (e.g. `encoding/json` round-trip) is the follow-up if nested-map fidelity is required.
4. **Richer resource modeling for Privileges** — `AuthzSpec.Privileges` today is a `[]string` carrying space-delimited `"obj act"` tokens; the `casbinauthz` façade splits on space into `(obj, act)` for `Enforce`. Richer modeling (domains/tenants, object hierarchies, wildcard policies) is a follow-up once the DB adapter provides a policy-management API.
5. **Sensitive-variable persisting in task snapshot** — `HumanTask.Vars` snapshots ALL top-level process variables into the task record; once a Postgres-backed `TaskStore` lands (currently in-memory), this would persist potentially sensitive process data (PII/secrets) — consider a snapshot allowlist or redaction before persisting tasks.

---

## Eventing (watermill) sub-project — ✅ COMPLETE

Built on branch `feat/eventing-watermill` (HEAD `38a1a47`). Design: spec
`docs/specs/2026-06-21-eventing-watermill-design.md`, plan `docs/plans/2026-06-21-eventing-watermill.md`,
ADR-0012 (watermill adapter behind the `eventing` façade; never imported from engine/model/runtime).
Gate: `go test -race ./...` green (all packages including `internal/persistence/postgres` via Docker),
total coverage on touched packages **97.6%** (`eventing` 100%, `internal/eventing/watermill` 96.6%),
lint clean (0 issues), watermill not present in `engine`/`model`/`runtime` (vendor-isolation guard: CLEAN).
watermill v1.5.2, OTel API v1.43.0 (SDK only in test files).

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` — `OutboxEvent` extended | Added `DedupKey string` and `InstanceID string` fields to `runtime.OutboxEvent` so the watermill adapter can set a stable message UUID and per-instance metadata without reaching into engine internals. | Task 1 |
| `internal/persistence/postgres/relay.go` | Relay scans and maps `DedupKey`/`InstanceID` columns from `wrkflw_outbox`; a column-order comment documents the `rows.Scan` projection order. | Task 1 |
| `internal/eventing/watermill/` | `Publisher` adapter: maps one `OutboxEvent` → one watermill message (DedupKey→UUID, InstanceID→metadata); emits one OTel span (`eventing.publish`) and increments `wrkflw_eventing_published_total` counter (attributes: `status=ok/error`); records error status on the span on failure. `NewWatermillLogger` slog bridge: forwards watermill's `Info`/`Debug`/`Trace`/`Error`/`With` to a `*slog.Logger`. `WithLogger`/`WithTracerProvider`/`WithMeterProvider` options. | Tasks 2–3 |
| `eventing/` root façade | `NewPublisher(pub, ...Option) runtime.Publisher` — wraps any `message.Publisher` as a `runtime.Publisher`; `NewGoChannelPublisher(...Option) (runtime.Publisher, message.Subscriber, io.Closer)` — in-process GoChannel for tests and simple deployments. Façade re-exports the three option constructors; watermill is confined to this package and `internal/eventing/watermill`. Compile-time `var _ runtime.Publisher` guard. | Tasks 4–5 |
| GoChannel e2e | `ExampleNewGoChannelPublisher` exercises start→subscribe→publish→receive in a single in-process test; confirms message UUID, metadata, and payload round-trip correctly. | Task 5 |

### Key design decision (ADR)

- **ADR-0012** — watermill adapter behind the `eventing` façade (same layer pattern as ADR-0008/ADR-0009): consumers import only `eventing.NewPublisher`/`NewGoChannelPublisher` which return `runtime.Publisher`; all watermill wiring stays in `internal/eventing/watermill`. Ensures watermill is never transitively imported from engine/model/runtime code.

### Deferred follow-ups

1. **Broker-specific constructors** — `NewGoChannelPublisher` ships for in-process use; production deployments need constructor helpers for Kafka, NATS, AWS SNS, etc. Each is a thin `eventing.NewKafkaPublisher(cfg, ...Option)` wrapping the corresponding watermill adapter. Deferred to a broker-specific sub-project.
2. **Richer event envelope / topic-mapping** — the current mapping uses `ev.Topic` verbatim as the watermill topic and stores it in `Metadata["topic"]`. A topic-routing function option (e.g. `WithTopicMapper(fn)`) and a richer envelope schema (schema version, event type, causation/correlation IDs) are deferred.
3. **Retry / DLQ poison isolation** — the `Relay` rolls back the entire batch on a publisher error (head-of-line blocking). Poison-event isolation and retry-with-backoff are the resilience sub-project; the relay deliberately defers this.
4. **Optional LISTEN/NOTIFY relay trigger** — the relay polls on a fixed interval; a Postgres `LISTEN`/`NOTIFY` push would cut latency and DB load (layered on the poll fallback). Deferred from the Persistence sub-project and still unbuilt.

---

## What's next: productionization sub-projects (each its own brainstorm → spec → plan → SDD cycle)

The engine core depends on interfaces only. The next sub-projects implement them (per CLAUDE.md):

- **Persistence** — ✅ COMPLETE, merged to `main`. See section above.
- **Eventing** — ✅ COMPLETE, merged to `main`. See "Eventing (watermill) sub-project" section above.
- **Scheduling** — ✅ COMPLETE, merged to `main`. See "Scheduling (gocron) sub-project" section above.
- **Authorization** — ✅ COMPLETE, merged to `main`. See "Authorization (casbin) sub-project" section above.
- **Transports** — ✅ COMPLETE, merged to `main`. See "Transports (REST/gRPC) sub-project" section below.
- **Admin monitoring** + **`ProcessInstance` response customization** — ✅ included in the Transports sub-project (admin middleware + keyset pagination + `WithInstanceMapper` response customization).

## How to run the next sub-project (the workflow that built Plans 1–8)

This repo was built with **subagent-driven development**, autonomously, per the user's cadence
(`working-cadence-autonomous-sdd` memory): brainstorm → spec (`docs/specs/`) → plan (`docs/plans/`) →
execute, with ADRs for significant decisions, branch → SDD → merge to `main` → push.

1. **Brainstorm + spec + plan** the sub-project (`superpowers:brainstorming` → `writing-plans`).
2. **Branch:** `git switch -c feat/<sub-project-slug>` (never implement on `main`).
3. **Execute with SDD** (`superpowers:subagent-driven-development`): per task — `scripts/task-brief PLAN N`
   → dispatch a fresh implementer (TDD, visible RED→GREEN) → `scripts/review-package BASE HEAD` → dispatch
   a task reviewer → fix Critical/Important → re-review → mark done in `.superpowers/sdd/progress.md`. The
   scripts live under the SDD plugin dir. Cheap model for transcription, sonnet for integration, **opus
   for the final whole-branch review**. Always set the model explicitly.
4. **Finish:** final whole-branch review (opus) → `superpowers:finishing-a-development-branch` → merge to
   `main`, push, delete the branch.

### Binding conventions (learned the hard way across Plans 1–8)

- **TDD strict** — every new symbol gets a failing test first with a **visible RED** before the impl
  (CLAUDE.md "TDD Operational Discipline"). The SDD per-task flow makes this auditable.
- **Tests:** black-box (`package <pkg>_test`); table-driven with an **`assert` closure per case** (project
  `table-test` skill, *not* `want`/`wantErr`); `t.Context()`; **pair each `foo.go` with `foo_test.go`**
  (reserve `*_example_test.go` for genuine e2e — see the test-file-naming memory).
- **Lint:** `golangci-lint` is **v2** (`.golangci.yml`, `version: "2"`).
- **Verify on completion:** `go test -race ./...` green; coverage ≥ 85% on touched packages; lint clean.
- **Commits:** Conventional Commits scoped to the area; end with the
  `Co-Authored-By: Claude Opus 4.8 (1M context)` trailer. Commit per logical change.
- **Hard-won lesson:** the plan's example code can be wrong — **trust the test, not the plan listing**;
  observe the red state. Ground every edit against the then-current code (the engine grew a lot).
- **gitignored scratch:** `cover.out`, `.superpowers/` (SDD briefs/reports/ledger/diffs). Don't commit them.

---

## Transports (REST/gRPC) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/transports-rest-grpc` (HEAD `24a4644`). Design: spec
`docs/specs/2026-06-21-transports-rest-grpc-design.md`, plan `docs/plans/2026-06-21-transports-rest-grpc.md`,
ADRs 0011 (consumer-mounted transports, no shipped binary) and 0004 (public packages at module root).
Gate: `go test -race ./...` green, `service` 86.7%, `transport/rest` 90.1%, `transport/grpc` 86.0%
(`workflowpb` generated package excluded from bar — 0% expected), `runtime` 94.7%, lint clean (0 issues),
no grpc/protobuf/net-http in `engine`/`model`; `service` is transport-neutral.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` — `InstanceLister` port | `InstanceLister` interface (`List(ctx, cursor, limit, filters) ([]InstanceSummary, nextCursor string, err)`) with keyset cursor codec (`encodeCursor`/`decodeCursor`, base64url-encoded `id:created_at` pairs). `MemStore` implements `InstanceLister` via an in-memory sorted scan; `internal/persistence/postgres` implements it with a keyset SQL query. `InstanceSummary` carries `ID`, `DefID`, `DefVersion`, `Status`, `CreatedAt`, `EndedAt`, `Variables`. | Task 1 |
| `service/` — transport-agnostic facade | `service.New(runner, lister, reg) *Service` — eight operations: `StartInstance`, `GetInstance`, `DeliverSignal`, `DeliverMessage`, `ClaimTask`, `ReassignTask`, `CompleteTask`, `ListInstances`. Resolves `ProcessDefinition` by `DefID:DefVersion` from the registry; passes all instance ops through `runtime.Runner`. Fully usable without any transport import. Note: `CancelInstance` is NOT in v1 — it is a deferred follow-up. | Task 2 |
| `transport/rest` — stdlib HTTP handler | `rest.NewHandler(svc, ...Option) http.Handler` — stdlib `*http.ServeMux` (no third-party router), mountable under `http.StripPrefix`. Routes: `POST /instances`, `GET /instances/{id}`, `POST /instances/{id}/signals`, `POST /messages`, `POST /tasks/{token}/claim`, `POST /tasks/{token}/complete`, `POST /tasks/{token}/reassign`. There is no `POST /instances/{id}/cancel` and no unauthenticated `GET /instances` — the only list route is `GET /admin/instances` (admin only, see Task 4). `WithInstanceMapper(fn)` customizes the `InstanceResponse` shape applied to ALL instance-returning endpoints. `WriteHTTPError(w, err)` maps sentinel errors to HTTP status codes. | Task 3 |
| `transport/rest` — admin monitoring | `GET /admin/instances` — keyset-paginated instance list scoped to admin callers; `?status=`, `?limit=` filters; cursor-based `next_cursor` in response. `WithAdminMiddleware(mw)` installs a middleware gate on all `/admin/*` routes; default-deny (403) when no middleware is configured. Admin routes are fully separated from consumer routes so the consumer can mount them on a different sub-path or omit them. | Task 4 |
| `transport/grpc` — gRPC service | `workflowpb` package: `.proto` at `transport/grpc/proto/workflow.proto`; committed generated `workflow.pb.go` + `workflow_grpc.pb.go` via `protoc` (no `buf` needed at build time). `RegisterWorkflowServiceServer(s grpc.ServiceRegistrar, svc *service.Service)` registers the implementation. `mapToGRPCStatus(err) error` translates sentinel errors to gRPC status codes. Tested end-to-end via `google.golang.org/grpc/interop/grpc_testing` + `bufconn` in-process dialer (no real network). | Task 5 |

### Key design decisions (ADRs)

- **ADR-0011** — consumer-mounted transports, no shipped binary. Both `rest.NewHandler` and
  `RegisterWorkflowServiceServer` return/register into caller-provided server infrastructure. The
  consumer chooses how to compose, secure, and start the server. `wrkflw` ships no `main`.
- **ADR-0004** — public packages at module root (no `pkg/` prefix). `service/`, `transport/rest/`,
  `transport/grpc/` are all importable directly as `github.com/zakyalvan/krtlwrkflw/service`, etc.

### Deferred follow-ups (deliberate, not bugs)

1. **422/FailedPrecondition for wrong-state transitions — DONE (`service.ErrConflict`).** Added
   `service.ErrConflict` sentinel in the `Correctness & tests hardening` sub-project (Tasks 3–4):
   `service.ClaimTask` and `service.DeliverSignal` wrap wrong-state errors with `ErrConflict`;
   `transport/rest` maps it to HTTP 422 with body error code `"conflict_state"`;
   `transport/grpc` maps it to `codes.FailedPrecondition`. The engine-level wrong-state sentinel
   (for callers using the engine directly, bypassing `service/`) remains a deferred follow-up.
2. **`DeliverMessage` requires a `*ProcessDefinition` from the caller** — `Runner.DeliverMessage`
   accepts a `*model.ProcessDefinition`; the `service.Service` facade currently requires the caller to
   supply a `DefRef` (id + version) so it can resolve the definition. A cleaner API would have the
   runner/facade look up the definition from the matched waiting instance directly. Deferred until
   the runner's `Deliver` port is revisited.
3. **REST deny-body Content-Type** — FIXED (pre-merge): `denyAllMiddleware` now emits a proper
   JSON body with `Content-Type: application/json` via `writeJSON`.
4. **Admin pagination total-count assertion** — the no-skip (first-page) assertion in the admin list
   tests does not cover total-count (no `X-Total-Count` header or `total` field shipped). Add total-count
   if admin UI needs it.
5. **Streaming/watch endpoints, OpenAPI/grpc-gateway, richer admin filters** — the current surface is
   request/response only. Server-streaming (watch for instance-state changes), an OpenAPI spec (via
   grpc-gateway or swag), and richer admin query filters (date range, def-id filter, multi-status) are
   follow-up features.
6. **`ended_at` non-optional in proto** — `EndedAt` is a `google.protobuf.Timestamp` (nullable) in the
   proto but non-optional in `InstanceSummary.EndedAt time.Time`; the gRPC mapping emits a zero-value
   timestamp for running instances. Make `EndedAt` a `*time.Time` in `InstanceSummary` or add a separate
   `has_ended` bool in the proto to avoid the zero-timestamp ambiguity.

---

## Correctness & tests hardening sub-project — ✅ COMPLETE, merged to `main`

First track of the **deferred-backlog run** (see the resume point at the top). Built on branch
`feat/correctness-hardening` (merge `314358c`, 2026-06-21). Design: spec
`docs/specs/2026-06-21-correctness-hardening-design.md`, plan
`docs/plans/2026-06-21-correctness-hardening.md`, ADRs 0013 (compensation hoist) and 0014
(mixed-gateway validation). 7 SDD tasks + opus whole-branch review (Ready to merge: Yes).
Gate: `go test -race ./...` green, touched pkgs all ≥85% (engine 85.4%, model 96.4%, service 86.6%,
transport/grpc 86.1%, transport/rest 90.1%, internal/persistence/postgres 86.1%), lint 0,
engine/model purity CLEAN.

### What shipped

| Work-stream | What | Notes |
|---|---|---|
| Nested-scope compensation **MUST-FIX** | `engine` hoists a closing sub-process scope's `Compensations` into its parent (root) in completion order *before* `closeScope` (`hoistCompensations` in `state.go`), so the existing root `CompensateRequested` walk rolls back completed-sub-process activities. No new `InstanceState` field; reverse-order saga semantics proven by ordering + two-level-nesting tests. | ADR-0013 |
| Mixed split+join gateway validation | `model.Validate` rejects a gateway with both >1 incoming AND >1 outgoing (`ErrMixedGateway`), recursively into sub-processes. | ADR-0014 |
| `service.ErrConflict` wrong-state sentinel | Closed-task / terminal-instance ops classified at the `service` seam → REST **422** (`"conflict_state"`), gRPC **`codes.FailedPrecondition`**. Engine/runtime taxonomy unchanged; not-found stays 404. | Tasks 3–4 |
| Parked-async Postgres resume e2e | `internal/persistence/postgres/resume_test.go`: park → persist → reload via a **fresh `Store`** → resume to `StatusCompleted` (timer + boundary variants). Note: intermediate-timer ids live on `token.AwaitCommand`, not `InstanceState.Timers` (which holds boundary/SLA arms). | "Highest-value missing test" |
| Inner-scope topology tests | Boundary / event-gateway / inclusive / SLA constructs nested inside a sub-process. **Surfaced + fixed a real bug:** `fireBoundaryArm` resolved the boundary's outgoing flow from the top-level def instead of the host token's scope def — fixed via `defForScope(def, s, hostTok.ScopeID)` (root-scope non-regression verified). | engine |

### Deferred follow-ups (still open after this track)

1. **Scope-targeted compensation** — the reserved `Compensate{ScopeID,FromNode}` command stays inert;
   true per-scope targeting needs an archive-by-scope + a producer (a BPMN compensation boundary/throw
   event). The hoist makes *root* rollback correct; per-scope targeting is future work.
2. **Reachability / fork-join pairing validation** — the mixed-gateway rule is the focused first cut;
   matching every converging join to a diverging fork (and condition-placement checks) is deferred.
3. **Engine-level wrong-state sentinel** — `ErrConflict` is classified only at the `service` seam;
   embedded consumers calling the engine/runtime directly still get untyped wrong-state errors.
4. **Compensation-on-error / cancel paths** — only *normal* sub-process exit hoists; error/cancel
   scope-close compensation semantics are a separate design.
5. **Pre-existing flaky singleflight test — ✅ FIXED (2026-06-22).** Was a TOCTOU in
   `CachingDefinitionRegistry.Lookup` (fast-path check / `singleflight.Do` gap), not a test barrier;
   fixed by an in-flight cache re-check. See tracked-follow-up #8 in the engine-core section.

---

## Resilience (retry/backoff/DLQ/idempotency) sub-project — ✅ COMPLETE, merged to `main`

Second track of the **deferred-backlog run**. Built on branch `feat/resilience-retry-dlq`
(HEAD `4673325`), 20 SDD tasks + opus whole-branch review (**Ready to merge: Yes-with-nits**;
no blocking issues, all 5 binding invariants confirmed). Design: spec
`docs/specs/2026-06-21-resilience-retry-dlq-design.md`, plan
`docs/plans/2026-06-21-resilience-retry-dlq.md`, ADRs 0015–0018.
Gate: `go test -race ./...` green, touched pkgs all ≥85% (engine 85.6%, model 95.8%, runtime 94.8%,
service 87.0%, transport/rest 90.2%, `internal/persistence/postgres` 86.8%, `persistence` 100%),
lint 0, engine/model purity CLEAN (`math/rand` only in `runtime/jitter.go`; `clockwork` test-only).
**Run the Postgres package with limited container parallelism** (`go test -p 1` / `-parallel N`) — high
concurrency exhausts Docker and surfaces spurious testcontainers startup failures (NOT regressions).

### Key design (ADRs 0015–0018)

- **ADR-0015 — engine-modeled retry executor.** A retry IS a timer: the runtime samples a jitter
  fraction at the edge and records it on `ActionFailed.JitterFraction`; the pure `Step` computes a
  deterministic `FireAt = OccurredAt + JitterFraction × Backoff(attempt)` (Full Jitter) and emits
  `ScheduleTimer{Kind: TimerRetry}`; the existing `Scheduler` fires it; `TimerFired{TimerRetry}`
  re-invokes the action. **Retry is opt-in** — absent an effective `RetryPolicy` (node policy or
  `StepOptions.DefaultRetryPolicy`), `ActionFailed` behaves exactly as before (`propagateError`).
- **ADR-0016 — exhaustion precedence Catch → boundary → Incident.** On a terminal failure with a
  policy: route `Node.RecoveryFlow` (injecting `_error`/`_errorMessage`/`_errorAttempts`) → else the
  existing error-boundary `propagateError` (now via a `raiseIncidentOnUnhandled` flag) → else raise an
  `Incident` (token → `TokenIncident`, instance stays `StatusRunning`), admin-resumable via the new
  `ResolveIncident` trigger.
- **ADR-0017 — outbox relay poison isolation / DLQ.** `wrkflw_outbox` gains
  `status`/`retry_count`/`next_attempt_at`/`last_error`; the relay claims
  `WHERE status='pending' AND next_attempt_at<=now`, isolates per row (a publish error quarantines
  only that row with backoff, → `dead` after `maxDelivery`; healthy peers still commit `published`),
  fixing head-of-line blocking. **Contract change:** `Run`/`DrainOnce` no longer fail-fast on a
  publish/broker error (they absorb + quarantine); infra errors still propagate. `ListDeadLettered` /
  `Redrive` admin API.
- **ADR-0018 — idempotency.** Engine stamps a stable `_idempotencyKey = instanceID:nodeID` (attempt-
  independent) on the primary service-task action; a `wrkflw_processed_message` table + `Deduper`
  (`Seen(ctx, tx, subscriber, messageID)` via `INSERT … ON CONFLICT DO NOTHING`, committed in the
  consumer's own tx) give consumers exactly-once *effect* over at-least-once delivery.

### What shipped (by layer)

| Layer | What |
|---|---|
| `model/` | `RetryPolicy` value type (`Backoff`/`Normalize`/`IsNonRetryable`/`DefaultRetryPolicy`); `Node.RetryPolicy *RetryPolicy` + `Node.RecoveryFlow`; `Validate` sentinels `ErrInvalidRetryPolicy`/`ErrInvalidRecoveryFlow` (recursive). |
| `engine/` | `ActionFailed.JitterFraction`; `ResolveIncident` trigger; `TimerRetry` kind; `Token.RetryAttempts`/`RetryStartedAt`; `Incident`/`InstanceState.Incidents`/`IncidentSeq`; `TokenIncident`; `StepOptions.DefaultRetryPolicy`; retry-schedule + `handleRetryFired` + exhaustion + `ResolveIncident` + `reinvokeServiceAction` + `serviceActionInput` (idempotency key); `cloneState` extended. |
| `runtime/` | `JitterSource` port + `math/rand/v2` impl + `WithJitterSource`; `WithDefaultRetryPolicy`; `Runner.ResolveIncident`; `InstanceSummary.IncidentCount`. |
| `internal/persistence/postgres/` | trigger codec for jitter + `ResolveIncident`; migration `0003_resilience.sql` (outbox DLQ cols + `wrkflw_processed_message`); relay per-row isolation + `RelayBackoff` + dead quarantine + `ListDeadLettered`/`Redrive`; `Deduper`; `TestPostgresParkedRetryResumesAfterReload`. |
| `persistence/` | `Relay` interface +`ListDeadLettered`/`Redrive`; `WithRelayClock`/`WithMaxDeliveryAttempts`/`WithRelayBackoff`; `Deduper`/`NewDeduper`. |
| `runtime/` shared | `runtime.DeadLetter` value type (façade return type, no import cycle). |
| `service/` + `transport/rest` | `service.ResolveIncident`; REST `POST /admin/instances/{id}/incidents/{incidentID}/resolve` (admin-gated, default-deny 403); `IncidentCount` in the admin list response. |

### Deferred follow-ups (recorded by the opus whole-branch review)

1. **gRPC `ResolveIncident` RPC + DLQ admin REST** (`GET /admin/dead-letters`, redrive) — ✅ **DONE**
   (2026-06-22, ADR-0029, branch `feat/grpc-resolveincident-dlq-admin`). Shipped on REST **and** gRPC
   via the optional `service.DeadLetterAdmin` seam + `WithDeadLetterAdmin`. See the "gRPC
   ResolveIncident + DLQ admin transport sub-project" section below.
2. **`wrkflw_processed_message` retention/pruning job** — the dedup table grows unbounded; a TTL prune
   (well past `maxDelivery × max backoff`) is an operator task.
3. **REST resolve-incident empty-body fidelity** — a genuinely empty body (Content-Length 0) hits
   `decodeBody`'s `io.EOF`→400; tests only send `{}`. Accept EOF as "defaults" or require a `{}` body.
4. **`RetryPolicy.Backoff` overflow guard** — safe in-engine (always `Normalize()`d, positive
   `MaxInterval` caps first), but a directly-constructed non-normalized policy with `MaxInterval==0` +
   huge attempt could overflow `time.Duration(d)`; consider a defensive cap inside `Backoff`.
5. **casbin-gated per-incident authz** — `ResolveIncident` is gated only by the transport admin
   middleware in v1; a per-incident attribute rule is future work.
6. **Cosmetic test/doc nits** (non-blocking, from per-task reviews): a few single-case tests use bare
   `t.Fatal` vs testify; `countUnpublished` counts dead rows as unpublished; `RelayBackoff` dead
   post-loop guard; godoc phrasings (`Backoff` "0=first retry", `RetryStartedAt` "wall-clock").
   The one timing-sensitive relay test was deflaked pre-merge (poll instead of `time.Sleep`).

---

## Observability (metrics/traces/slog) sub-project — ✅ COMPLETE, merged to `main`

Third track of the **deferred-backlog run**. Built on branch `feat/observability`.
Design: spec `docs/specs/2026-06-21-observability-design.md`, plan
`docs/plans/2026-06-21-observability.md`, ADR-0019.
Gate: `go test -race ./runtime/...` green, lint 0, engine/model purity CLEAN (confirmed by
`TestCorePurityNoOTel` guard), OTel SDK packages confined to test files in production code.

### Design forks (key decisions)

- **OTel-API-direct** — `go.opentelemetry.io/otel`, `.../metric`, `.../trace` imported directly
  from production code; no in-repo tracing/metrics port. The OTel API *is* the vendor-neutral
  abstraction (unlike watermill/casbin/gocron). SDK packages appear in test files only.
- **Manual transport spans** — `transport/rest` and `transport/grpc` open spans manually at
  handler/interceptor entry; no `otelhttp`/`otelgrpc` contrib dependency.
- **Full metric catalog** — 9 instruments covering the complete instance lifecycle, step timing,
  action timing, retries, incidents, and human-task events.
- **Runtime is the boundary** — engine core (`engine/`, `model/`) never sees a span, meter, or
  logger; all instrumentation wraps around the pure `Step` call in `runtime.Runner`.

### What shipped (by layer)

| Layer | What |
|---|---|
| `internal/observability/` | Shared `Telemetry` struct + `New(scope, ...Option)` constructor; `WithLogger`/`WithTracerProvider`/`WithMeterProvider` options; `LogAttrs` helper injecting `trace_id`/`span_id` into slog records. Used by all instrumented components. |
| `runtime/` — `Runner` | `WithLogger`/`WithTracerProvider`/`WithMeterProvider` functional options; `runnerObs` bundles all instruments; spans on `Run` (`wrkflw.runner.Run`), `Deliver` (`wrkflw.runner.Deliver`), each `engine.Step` (`wrkflw.step`), each `InvokeAction` (`wrkflw.action <name>`); 9 metric instruments: `wrkflw_instances_started_total`, `wrkflw_instances_completed_total`, `wrkflw_instances_active`, `wrkflw_step_duration_seconds`, `wrkflw_action_duration_seconds`, `wrkflw_action_retries_total`, `wrkflw_incidents_raised_total`, `wrkflw_incidents_resolved_total`, `wrkflw_human_tasks_total`; injected logger for timer-fire retry logging. |
| `runtime/` — `TaskService` | `WithTaskServiceMeterProvider` option; increments `wrkflw_human_tasks_total{event=claimed/reassigned/completed}` on successful lifecycle transitions. |
| `transport/rest/` | `WithLogger` option; manual `wrkflw.rest METHOD` span at handler entry; W3C `traceparent`/`tracestate` propagation; injected logger replaces any package-global `slog.*` calls. |
| `transport/grpc/` | `WithLogger` option; per-RPC `wrkflw.grpc /<svc>/<method>` span at interceptor level; gRPC metadata propagation. |
| `internal/scheduling/gocron/` | `WithLogger` option; injected logger for scheduler lifecycle events. |
| `internal/persistence/postgres/relay.go` | `wrkflw.relay.batch` span wrapping each `DrainOnce` batch; structured logs for relay errors. |
| `engine/` — purity guard | `TestCorePurityNoOTel` in `engine/purity_test.go`: asserts via `go list -f {{.Deps}}` subprocess that neither `engine` nor `model` transitively imports any `go.opentelemetry.io` package. Runs as part of `go test ./engine/...`. |
| `runtime/` — testable example | `ExampleRunner_observability` in `runtime/observability_example_test.go`: documents the complete consumer wiring (SDK `TracerProvider` + `MeterProvider` + `*slog.Logger` → `With*` options → `r.Run`). |

### Deferred follow-ups

1. **Public `observability` root package** — a consumer-facing package exporting a ready-made
   trace-correlating `slog.Handler` (injects `trace_id`/`span_id`) and a convenience `Setup` helper
   configuring OTel globals + injecting into a runner. Currently `internal/observability` is unexported.
2. **Migrate eventing adapter onto the shared helper** — `internal/eventing/watermill` has its own
   With-option wiring; it should delegate to `internal/observability.New` for consistency.
3. **Async DB-backed `instances_active` gauge** — current UpDownCounter resets to zero on restart;
   a true gauge would query `wrkflw_instances` for the live count (periodic background query or
   async observable gauge callback).
4. **`instances_active` mid-run abort caveat** — hard errors aborting `deliverLoop` before terminal
   state do NOT decrement `wrkflw_instances_active`; the instance is non-terminal in the store
   (intentional semantic, documented).
5. **Store-commit / `perform` error span coverage** — `wrkflw.step` span ends before `store.Commit`;
   commit and command-execution errors surface on the parent `Run`/`Deliver` span only. Deeper span
   hierarchy is a follow-up.
6. **OTel contrib transport option** — `transport/rest` and `transport/grpc` could accept an
   `otelhttp`/`otelgrpc`-based option for consumers preferring automatic propagation; manual is v1.
7. **Persistence `Store` (Load/Commit) spans and metrics** — Postgres store operations are not
   instrumented; a `wrkflw_store_duration_seconds` histogram is a follow-up for the
   Performance/caching track.
8. **REST route-template span naming** — span name is currently `wrkflw.rest METHOD`; the route
   pattern is unavailable at middleware time (`r.Pattern` not accessible). High-cardinality path
   parameters must not appear in span names; per-handler span creation is the follow-up.
9. **Histogram exemplars** — exemplars linking data points to trace IDs are not yet configured;
   `exemplar.AlwaysOnFilter` option is a follow-up.
10. **REST/relay `WithMeterProvider` parity** — both accept the option for future use but emit no
    metrics yet; route-level request counters/latency histograms and relay throughput counters are
    a follow-up.

---

## Performance/caching sub-project — ✅ COMPLETE

Fourth track of the **deferred-backlog run**. Built on branch `feat/performance-caching`
(2026-06-22; merge-base `610982e` from `main` after the Observability track). 9 SDD tasks
+ opus whole-branch review (**Ready to merge: With fixes** — one Important issue I1 fixed
pre-merge, see below). Design: spec `docs/specs/2026-06-22-performance-caching-design.md`,
plan `docs/plans/2026-06-22-performance-caching.md`, ADRs 0020–0022.

Gate (final, controller-verified):
- `runtime`: **94.9%** ✅ — `go test -race ./runtime/...` green
- `persistence` façade: **100.0%** ✅
- `internal/persistence/postgres`: **85.3%** ✅ (`go test -race -p 1`, ~35s)
- `golangci-lint run ./...`: **0 issues** ✅
- `go test ./engine/... -run TestCorePurity` (`TestCorePurityNoOTel`): **PASS** ✅
- Vendor purity grep (`watermill|casbin|gocron|clockwork` in `engine`/`model` deps): **PURE** ✅

### Opus whole-branch review outcome (pre-merge fix)

The final review flagged one Important issue (**I1**): `Ownership.Release`'s godoc claimed it
"triggers cache eviction", but `CachingStore` never called `owner.Release` and had no
Release→evict hook — a latent stale-read hazard on the advisory-lock multi-process path (the
default `AlwaysOwn` path is immune). **Fixed** by adding a `CachingStore.Release(ctx, id)` seam
that evicts the cache entry *then* forwards to `owner.Release`, with godoc on both
`CachingStore.Release` and `Ownership.Release` and a warning on `NewAdvisoryLockOwnership` that
consumers using a cache MUST relinquish ownership through `CachingStore.Release` (not the bare
`Ownership`) so the cache stays coherent on hand-off. Test `TestCachingStoreReleaseEvicts`
proves a post-`Release` Load re-reads the backing. Minor doc corrections also landed (poll path
now `drainUntilEmpty`/drain-to-empty, not "unchanged"; `capHistory` append-order note;
`OpenPostgres` godoc `WithHistoryCap` example).

### What shipped (by layer)

| Layer | What | Task |
|---|---|---|
| `internal/persistence/postgres/` — `capHistory` | `capHistory(history []engine.NodeVisit, n int)` keeps every open visit (nil `LeftAt`) plus the n most-recent closed visits; input not mutated; n≤0 is a no-op. | 1 |
| `internal/persistence/postgres/` — `WithHistoryCap` | `WithHistoryCap(n int) StoreOption` wires `capHistory` into `Store.Create`/`Commit` before the JSONB snapshot write; default (unset) preserves full inline history; `persistence.WithHistoryCap` façade re-exports it. | 2 |
| `internal/persistence/postgres/` — NOTIFY | `WithOutboxNotify() StoreOption` emits a transactional `NOTIFY wrkflw_outbox` inside the same transaction when at least one outbox row was inserted; opt-in, default off. | 3 |
| `internal/persistence/postgres/` — LISTEN relay | `WithListenNotify() RelayOption` opens a dedicated `LISTEN wrkflw_outbox` connection; on each `NOTIFY` the relay calls `DrainOnce` immediately, well before the poll-interval tick; the poll-fallback remains active. | 4 |
| `runtime/` — `Ownership` port | `Ownership` interface (`Acquire(ctx, id) (bool, error)` / `Release(ctx, id) error`); `AlwaysOwn{}` (always owns, no-op release) for single-replica or sticky deployments. | 5 |
| `runtime/` — `CachingStore` | `CachingStore` write-through LRU+TTL store decorator (`NewCachingStore(backing, owner, clk, ...CachingStoreOption)`). Owned instances are served from cache; non-owned bypass. `ErrConcurrentUpdate` evicts the stale entry. Per-instance keyed mutex serializes concurrent Load/Commit (held across Load's `[get→backing.Load→put]` for coherence). `Release(ctx, id)` evicts-then-relinquishes ownership (the required seam for cache-coherent hand-off, added in the final-review fix). `WithCacheTTL` / `WithCacheMaxEntries` options. | 6 |
| `runtime/` — `CachingStore` tests | TTL expiry forces reload; LRU evicts at cap; concurrent Load/Commit coherent under `-race`. | 7 |
| `internal/persistence/postgres/` — advisory-lock `Ownership` | `NewAdvisoryLockOwnership(ctx, pool)` holds a dedicated connection; `Acquire` uses `pg_try_advisory_lock` (sticky); `Release` uses `pg_advisory_unlock`; tests: A acquires, B blocked, A releases, B acquires. `persistence.NewAdvisoryLockOwnership` façade. | 8 |
| `runtime/` — testable example | `ExampleNewCachingStore` in `runtime/caching_store_example_test.go`: wires `NewCachingStore(NewMemStore(), AlwaysOwn{}, clock.System())` as the runner store, parks an instance at a signal-catch node, delivers `SignalReceived("approved")` — the second Deliver is served from cache — prints `"completed"`. | 9 |

### Key design decisions (ADRs)

- **ADR-0020** — `CachingStore` + `Ownership` port: write-through, single-writer cache gated by
  `Ownership.Acquire`; the optimistic-concurrency CAS (`ErrConcurrentUpdate`) is the backstop.
  `AlwaysOwn` for in-process / sticky; Postgres advisory lock for multi-replica.
- **ADR-0021** — history cap: `capHistory` keeps all open visits (never dropped) plus the n
  most-recent closed; the journal table remains the complete audit source; cap is per-store, not
  per-definition.
- **ADR-0022** — LISTEN/NOTIFY relay trigger: opt-in transactional `NOTIFY` from `Store` + opt-in
  `LISTEN` goroutine in the relay, layered on top of the existing poll fallback so the relay
  remains correct without NOTIFY.

### Deferred follow-ups

1. **Lease-column ownership alternative** — the advisory-lock implementation ties ownership to
   a Postgres session; a `lease_owner` column + heartbeat approach survives connection churn. A
   follow-up ADR can weigh the trade-offs.
2. **Per-worker push fairness** — with multiple relay workers each `LISTEN`ing, all receive every
   `NOTIFY`; they all race to claim. A single designated listener that fans out internally avoids
   thundering-herd. Deferred.
3. **`Store` Load/Commit spans and metrics** — Observability follow-up #7: `wrkflw_store_duration_seconds`
   histogram for Postgres store operations. Still unbuilt.
4. **History-cap per-definition granularity** — the cap is set at store construction; a per-definition
   cap (e.g. `model.Node.HistoryCap`) would allow fine-grained control. Deferred.
5. **`AdvisoryLockOwnership` use-after-close guard** — after `Close`, the dedicated connection is
   released but non-nil; a subsequent `Acquire`/`Release` would use a returned-to-pool connection
   (doc-warned, shutdown-only call). A cheap `closed bool` guard returning a sentinel would harden
   it. Deferred (opus-review Minor M2).
6. **Residual hard-to-force infra branches uncovered** — `maybeNotify`'s NOTIFY-exec-error path and
   a few `DrainOnce`/`listenLoop` infrastructure-failure branches are not deterministically
   forceable; package totals clear ≥85% without them. Fault-injection (a failing/closeable conn
   wrapper) could cover them if desired.
7. **Relay LISTEN test establish-sleep** — `relay_listen_test.go` uses a fixed 200 ms wait for the
   listener to establish before writing the event; on a very slow CI host this could race. Prefer
   polling for the `LISTEN` to be established (opus-review Minor; test-only, non-blocking).

---

## DB casbin policy adapter — ✅ COMPLETE

Fifth track of the **deferred-backlog run**. Built on branch `feat/casbin-db-adapter`
(merge-base `610982e`, the `feat/performance-caching` merge onto `main`, 2026-06-22).
Design: spec `docs/specs/2026-06-22-casbin-db-adapter-design.md`,
plan `docs/plans/2026-06-22-casbin-db-adapter.md`, ADR-0023.

Gate (final, Task 6 verified — 2026-06-22):
- `go test -race` (all non-Docker packages): **PASS** ✅ — 18 packages, 0 failures
- `go test -race -p 1 ./internal/authz/casbin/... ./casbinauthz/... ./internal/persistence/postgres/...`: **PASS** ✅
- `casbinauthz` per-package coverage: **90.9%** ✅ (≥85%)
- `internal/authz/casbin` per-package coverage: **85.6%** ✅ (≥85%)
- `golangci-lint run ./...`: **0 issues** ✅
- Confinement guard (`TestCasbinConfinement`): **PASS** ✅ — casbin absent from engine/model/runtime/persistence transitive deps (proven to bite: 25 violations when casbin injected into runtime, clean after revert)
- No ORM in go.mod: **CLEAN** ✅ (`gorm`/`go-pg`/`sqlx`/`ent` absent)
- casbin version: **v2.135.0** ✅ (pinned, not bumped)
- Opus whole-branch review: **Ready to merge: Yes** — no Critical/Important; all binding invariants (callback race-fix ordering, watcher leak/connection-release, no `RemoveFilteredPolicy` over-deletion, separate version table, façade type confinement, additive-only) verified.

**Coverage note (resolved):** `internal/authz/casbin` initially measured 73.1% because `db.go`
(`NewDBEnforcer`, the two closers) is exercised only from `casbinauthz/casbinauthz_db_test.go`
(coverage attributed to the caller). Closed by adding `internal/authz/casbin/db_test.go`
(black-box) that calls `NewDBEnforcer` directly (watcher-enabled / disabled / invalid-model
paths) plus three watcher error-branch tests (Update error, listen acquire/LISTEN failures via
fault injection) → **85.6%**. `NewDBEnforcer` itself sits at 66.7%; its three remaining error
branches (`NewSyncedEnforcer`/`SetWatcher`/`SetUpdateCallback` failing) are structurally
unreachable in black-box because the production watcher never returns an error.

### What shipped (by layer)

| Layer | What | Notes |
|---|---|---|
| `internal/authz/casbin/migrate.go` + `casbinauthz.MigrateCasbin` | `MigrateCasbin(ctx, pool)` runs goose migrations tracked in a **separate `casbin_goose_db_version` table** (independent of `wrkflw_goose_db_version`). Creates `casbin_rule(id, ptype, v0–v5, created_at)` with a unique constraint on `(ptype,v0–v5)`. Idempotent; safe to call multiple times. | ADR-0023; separate version table prevents version-number conflicts with the main `persistence.Migrate` |
| `internal/authz/casbin/pg_adapter.go` + exports | `pgAdapter` implements `casbin/persist.Adapter` over pgx/v5. `LoadPolicy` reads all rows and feeds the casbin model via `model.AddPolicy`. `SavePolicy` truncates then bulk-inserts (single-pass with padded 6-column rules). `AddPolicy`/`RemovePolicy`/`RemoveFilteredPolicy` are incremental mutations persisted immediately. Padding/trimming (`padRule`/`ruleFromCols`) ensures row format is correct regardless of policy arity. `NewPGAdapter` (exported via `export_test.go`) for black-box tests. | ADR-0023 §3 |
| `internal/authz/casbin/pg_watcher.go` | `pgWatcher` implements `casbin/persist.Watcher` via `pgconn.LISTEN`/`NOTIFY`. `Update(s)` sends a `NOTIFY` whose payload is `{nodeID}:{s}` (the colon-separated node identifier allows self-filtering). The background `listen` loop ignores notifications where the payload's node prefix matches this node's ID, so a node does not reload on its own writes. `Close` cancels the listen loop and closes the dedicated connection. `backoff` (unexported) gives a jitter-retry helper for reconnects. `SetUpdateCallback` wires the handler. | ADR-0023 §4 |
| `internal/authz/casbin/db.go` | `NewDBEnforcer(ctx, pool, DBConfig)` assembles a `*casbin.SyncedEnforcer` over `pgAdapter`. When `WatcherEnabled`, creates and wires a `pgWatcher`. **Critical race fix:** casbin's `SetWatcher` internally calls `w.SetUpdateCallback(func(string){ _ = e.LoadPolicy() })` where `e` is the BASE `*Enforcer` (not mutex-synchronized). We override the callback *after* `SetWatcher` to call `enforcer.LoadPolicy()` on the `*SyncedEnforcer` so the lock is held during reload. `DBConfig` carries `ModelText`, `WatcherEnabled`, `WatcherChannel`, `NodeID`. | ADR-0023 §5 |
| `casbinauthz/` façade additions | `MigrateCasbin(ctx, pool)` re-exported; `DBOption` functional options: `WithModel`, `WithoutWatcher`, `WithWatcherChannel`, `WithNodeID`; `defaultNodeID()` generates a per-process random node ID. `NewCasbinAuthorizerFromDB(ctx, pool, ...DBOption) (authz.Authorizer, io.Closer, error)` — builds enforcer via `NewDBEnforcer`, wraps via the existing `NewCasbinAuthorizer` single-wrapping path, returns the stable `authz.Authorizer` interface + `io.Closer`. | ADR-0023 §6 |

### Key design decision (ADR)

- **ADR-0023** — pgx-native DB casbin policy adapter + LISTEN/NOTIFY watcher, following the
  same façade/internal layer pattern as ADR-0008/ADR-0009/ADR-0010. `casbinauthz` and
  `internal/authz/casbin` are the only packages importing casbin (enforced by the
  `TestCasbinConfinement` guard). The `*SyncedEnforcer`-callback override is the critical
  fix preventing a data-race on policy reload in multi-node deployments. A separate
  `casbin_goose_db_version` table keeps the casbin migration version independent of the
  main persistence schema version.

### Deferred follow-ups

1. **`FilteredAdapter` / incremental `WatcherEx` updates for large policy sets** — `LoadPolicy`
   re-reads the entire `casbin_rule` table on every watcher-triggered reload. For large policy
   sets this is expensive. Implementing `casbin/persist.FilteredAdapter` (partial load) and the
   `casbin/persist.WatcherEx` interface (per-rule delta updates instead of full reload) would cut
   reload cost significantly. Deferred until policy-set sizes warrant it.
2. **Policy-admin REST/gRPC surface** — `NewCasbinAuthorizerFromDB` provides policy persistence
   but no API to add/remove/list rules at runtime (other than direct DB manipulation). A
   `casbinauthz.PolicyAdmin` interface with REST/gRPC endpoints (e.g. `POST /admin/policy`,
   `DELETE /admin/policy/{id}`, `GET /admin/policy`) is a follow-up; the persisted `pgAdapter`
   is the backend.
3. **Watcher reconnect-delay not tunable** — the `backoff` helper uses a fixed
   `watcherReconnectDelay` constant (1 s), no jitter. There is no `DBOption` to override it.
   A `WithWatcherReconnectBackoff(...)` option (and optional jitter) would make this configurable
   for consumers with stricter SLA requirements.
4. **Separate `casbin_goose_db_version` table note** — the casbin migration intentionally uses
   a separate `casbin_goose_db_version` version table (via `goose.WithTableName`) to avoid
   version-number conflicts with the persistence migration set's `goose_db_version`. Consumers
   calling both `persistence.Migrate` and `casbinauthz.MigrateCasbin` will see two goose version
   tables in their schema; this is expected and documented.
5. **`context.Background()` in adapter/watcher methods** — casbin's `persist.Adapter`/`Watcher`
   method signatures take no `context`, so the pgx calls inside them cannot propagate a caller
   deadline/cancellation. A context-aware adapter wrapper (storing a base context) is a follow-up.

---

## True async call activity — ✅ COMPLETE

Final "also outstanding" item (engine follow-up #3). Built on branch `feat/async-call-activity`
(2026-06-22; merge-base `4b8137e`). 9 SDD tasks + opus whole-branch review. Design: spec
`docs/specs/2026-06-22-async-call-activity-design.md`, plan `docs/plans/2026-06-22-async-call-activity.md`,
ADRs 0024 (durable async call activity) + 0025 (atomic call-link Store side-effects).

**The headline:** a call-activity child that PARKS (its own human task, timer, signal, or nested
call activity) now works. Previously `perform(StartSubInstance)` ran the child synchronously and
errored on a parking child ("the synchronous runner does not support parked children"). Now the
parent parks; the child runs independently across later `Deliver`s; when the child reaches terminal
status, the parent is resumed by `SubInstanceCompleted`/`SubInstanceFailed` delivered **durably and
crash-safely**, idempotently.

**Engine/model UNTOUCHED** (the load-bearing property): `git diff 4b8137e..HEAD -- engine model`
shows **zero** production-line changes. The `SubInstanceCompleted`/`SubInstanceFailed` triggers, the
`StartSubInstance` command, the parent token park (`AwaitCommand`), and the resume logic
(`engine/step.go:514–539`) already existed and are used as-is. This is a runtime + persistence change.

### What shipped (by layer)

| Layer | What |
|---|---|
| `runtime/` | `CallLink`/`CallOutcome`/`PendingNotify` value types; `CallLinkStore` port (`ClaimPending`/`MarkNotified`/`LookupChild`) + `MemCallLinkStore`; additive `AppliedStep.NewCallLink`/`CallOutcome` (nil for all existing callers; `MemStore` honors them via `NewMemStoreWithCallLinks`). Non-blocking `perform(StartSubInstance)` + `WithCallLinks` option (opt-in; absent it the synchronous behavior is preserved verbatim); `maxCallActivityDepth`→`maxCallDepth`; the `deliverLoop` child-terminal hook (sets `CallOutcome` on the terminal commit). `CallNotifier` (`DrainOnce`/`Run`) — claims terminal links, resolves the parent def, delivers `SubInstanceCompleted`/`SubInstanceFailed`, **idempotent** (`engine.ErrTokenNotFound` ⇒ treated as success); `CallDeliverFunc`. |
| `internal/persistence/postgres/` | `0004_call_links.sql` (`wrkflw_call_links` + partial pending index); `Store.Create`/`Commit` honor `NewCallLink`/`CallOutcome` IN-TX (the crash-safety seam — link created with the child's Create, flipped with its terminal Commit, atomically); Postgres `CallLinkStore`; crash-safety e2e (a FRESH notifier over a NEW pool resumes a parked parent purely from durable DB state). |
| `persistence/` (façade) | `NewCallLinkStore(pool) runtime.CallLinkStore`; `NewCallNotifier(pool, deliver, reg, clk, ...opts)` reusing `runtime.CallNotifier` over the Postgres store (one wrapping path, no logic duplication). |
| `engine/`, `model/` | **Nothing.** Zero production diff (proven). |

### Key design decisions (ADRs)
- **ADR-0024** — durable async call activity via a `wrkflw_call_links` correlation table + a relay-shaped
  notifier; correlation lives in persistence (NOT on the pure `InstanceState`); opt-in; idempotent
  parent resume; crash-safe.
- **ADR-0025** — atomic call-link side-effects on the transactional `Store` (additive `AppliedStep`
  fields), so the link's existence is tied to the child's existence and the link's terminal flip is
  tied to the child's terminal commit, each in one transaction.

### Gate (final, controller-verified)
`go test -race ./...` green (Postgres `-p 1`); coverage **runtime 91.0% / persistence 91.7% /
internal/persistence/postgres 86.2%** (all ≥85%); `golangci-lint run ./...` **0 issues**;
**engine/model production code unchanged** (zero-line diff over the branch).

### Deferred follow-ups
1. **`FOR UPDATE SKIP LOCKED` claim for strict multi-replica exclusivity** — `ClaimPending` is a plain
   SELECT; idempotency (`ErrTokenNotFound`) makes concurrent multi-replica notifiers SAFE but allows a
   duplicate delivery (wasted work). A tx-holding `DrainOnce` with `FOR UPDATE SKIP LOCKED` would make
   the claim exclusive.
2. **CallNotifier relay-shaping** — telemetry span (`wrkflw.callnotifier.batch`), per-row backoff, and
   an optional `LISTEN`/`NOTIFY` wakeup on `wrkflw_call_links` (the relay has these; the notifier reuse
   inherits per-row isolation + retry-via-poll but not these). Latency/observability, not correctness.
3. **Richer `SubInstanceFailed`→parent error text** — `SubInstanceFailed` does not create an `Incident`,
   so `terminalErr` falls back to a generic message (e.g. the depth-limit cause is lost in a deep
   runaway cascade). Populating an `Incident` would surface the cause.
4. **Cancellation propagation** parent→child (parent cancel → child terminate) and orphaned-child
   cleanup (when the parent is already terminal, the child result is dropped — the parent `Deliver`
   no-ops on `ErrTokenNotFound`).
5. **Cross-machine child execution** — this design makes the parent *notification* durable, not the
   child's execution distributed; the child is driven by whichever runtime delivers its triggers.
6. **Per-definition `maxCallDepth`** (global guard in v1); `MarkNotified` clock injection (uses
   `time.Now()` today).
