# Process-Instance Chaining — Implementation Plan

**Spec:** `docs/specs/2026-06-24-process-instance-chaining-design.md`
**Reserved ADRs:** 0045 (chaining), 0046 (status-accurate terminal events)
**Branch:** `claude/process-instance-chaining-iazvza`
**Engine/model production diff:** MUST be ZERO (runtime + eventing + persistence only).

## Conventions (read before starting)

- **Strict TDD, visible RED→GREEN per symbol** (CLAUDE.md "TDD Operational
  Discipline"). Every new exported symbol: write the test, run `go test ./<pkg>/...`
  to see it FAIL (compile error counts), then implement to GREEN. No batching test+impl
  in one edit pass.
- **Skills:** load the always-on `cc-skills-golang:*` set; use project `table-test`,
  `use-mockgen`, `use-testcontainers` skills. Black-box tests (`<pkg>_test`).
- **Error prefix:** all production errors carry `workflow-` (assert with `errors.Is`).
- **No watermill in `runtime`/`engine`.** Watermill lives only in `eventing`.
- **No Go files at the module root.** Root type aliases are a separate user-owned task.
- **Gate after each phase:** `go test -race ./<touched>/...` green; lint clean.

## Phase 0 — ADRs + branch hygiene

- [ ] Branch `claude/process-instance-chaining-iazvza` is current and rebased on `main`.
- [ ] Write **ADR-0045** (`docs/adr/0045-process-instance-chaining.md`, Nygard) — the
      chaining decision (event-driven; callback policy; durable link; core/adapter split;
      idempotency model).
- [ ] Write **ADR-0046** (`docs/adr/0046-status-accurate-terminal-events.md`, Nygard) —
      status-driven terminal events; new `instance.terminated`; cancel/full-rollback fix;
      migration note for consumers.

## Phase 1 — Status-accurate terminal outbox events (ADR-0046) [`runtime`]

Goal: terminal events derived from `st.Status` at the `deliverLoop` edge, exactly one
per terminal status; add `instance.terminated`; fix cancel→failed + full-rollback gap.

- [ ] **RED** — In `runtime/outbox_test.go` (black-box), replace/extend the command-driven
      assertions with a table over `(prevStatus, status) → (topic, payload)`:
      - `(Running, Completed) → "instance.completed"`, payload == vars
      - `(Running, Failed) → "instance.failed"`, payload == `{"error":…}`
      - `(Running, Terminated) → "instance.terminated"`, payload == `{"error":…}`
      - non-terminal / terminal→terminal (no edge) → no event
      Target a new `terminalOutboxEvent(prevStatus, st)` (or `terminalEventsFor`).
      Run `go test ./runtime/...` → FAIL (`undefined`).
- [ ] **GREEN** — Add `terminalOutboxEvent(prevStatus engine.InstanceState/Status, st …)`
      in `runtime/outbox.go`; status-switch using existing `terminalErr(st)`; return the
      one event (or none when not a terminal edge). Keep `outboxEventsFor` only if other
      (non-terminal) commands ever produce events — today none do, so fold terminal
      derivation to status-driven and have `deliverLoop` call the new helper.
- [ ] **Wire** — In `runtime/runner.go` `deliverLoop`, replace
      `events := outboxEventsFor(res.Commands)` with the status-driven derivation at the
      terminal-edge branch (reuse the existing `isTerminal(st.Status) && !isTerminal(prevStatus)`
      computation; note `CallOutcome` already keys off it). Ensure events still land in
      `AppliedStep.Events` and commit in-tx.
- [ ] **Regression** — exhaustiveness/topic test: cancel (with and without compensation)
      → `instance.terminated`; admin full-rollback → `instance.terminated`; genuine
      `ActionFailed` unhandled → `instance.failed`.
- [ ] **Verify** — `go test -race ./runtime/...` green; engine/model diff still ZERO.

## Phase 2 — `ErrInstanceExists` typed duplicate-start [`runtime`]

- [ ] **RED** — `runtime/memstore_test.go`: calling `Create` twice for the same instance
      id returns `runtime.ErrInstanceExists` (today it overwrites). Run → FAIL.
- [ ] **GREEN** — Add `var ErrInstanceExists = errors.New("workflow-runtime: instance already exists")`;
      `MemStore.Create` returns it on duplicate id.
- [ ] **Postgres** — `internal/persistence/postgres` `Store.Create` maps a primary-key
      violation (23505) on the instance insert to `runtime.ErrInstanceExists`
      (integration test in Phase 6). Keep mem + pg behaviour aligned.
- [ ] **Verify** — `go test -race ./runtime/...` green.

## Phase 3 — Lineage: `ChainLink` + `ChainLinkStore` + `MemChainLinkStore` [`runtime`]

- [ ] **RED** — `runtime/chainlink_test.go`: `MemChainLinkStore.Record` then
      `LookupBySuccessor`/`ListByPredecessor` round-trip; a second `Record` for the same
      `(PredecessorID, Outcome)` returns `ErrChainLinkExists`. Run → FAIL (`undefined`).
- [ ] **GREEN** — `runtime/chainlink.go`: `Outcome` + constants, `ChainLink`,
      `ErrChainLinkExists`, `ChainLinkStore` interface, `MemChainLinkStore`
      (mutex + map keyed by `(PredecessorID, Outcome)`). Add `//go:generate mockgen`
      directive for `ChainLinkStore` (per `use-mockgen`).
- [ ] Generate the `ChainLinkStore` + `InstanceStarter` mocks (`go generate ./runtime/...`).
- [ ] **Verify** — `go test -race ./runtime/...` green; ≥85%.

## Phase 4 — Chaining core: `Chainer.Handle` [`runtime`]

- [ ] **RED** — `runtime/chainer_test.go` (black-box, mocked `InstanceStarter` +
      `ChainLinkStore`): table over —
      - policy returns `ok=false` → no Record, no Run, nil
      - happy path → Record + Run with id `<pred>-next-<outcome>` + mapped vars
      - `Record` → `ErrChainLinkExists` → no Run, nil (dup)
      - `Run` → `ErrInstanceExists` → nil (dup)
      - `Run` → transient err → error propagated
      - `links == nil` (no lineage) → Run still happens (deterministic-id only)
      Run → FAIL.
- [ ] **GREEN** — `runtime/chainer.go`: `Outcome`/`ChainEvent`/`SuccessorDecision`/
      `SuccessorPolicy`/`InstanceStarter`/`Chainer`/`NewChainer`/`ChainerOption`
      (`WithChainLinks`, `WithChainLogger`, `WithClock`) + `Handle` per spec §4.3.
      Confirm `*Runner` satisfies `InstanceStarter` (compile-time `var _`).
- [ ] **Observability** — span `wrkflw.chain.handle` + counter
      `wrkflw_chain_started_total{outcome}` (noop default; mirror runner obs).
- [ ] **Verify** — `go test -race ./runtime/...` green; ≥85%.

## Phase 5 — Watermill adapter: handler + `Chainer.Run` [`eventing`]

- [ ] **RED** — `eventing/chaining_test.go`: build a `*runtime.Chainer` over a real
      `Runner`+`MemStore`+`MemChainLinkStore`; publish an `instance.completed` message
      (payload vars, metadata `instance_id`) via `NewGoChannelPublisher`; assert the
      successor instance was started (via the store) and the link recorded. Also unit-test
      topic→Outcome projection + malformed-payload ack. Run → FAIL (`undefined`).
- [ ] **GREEN** — `eventing/chaining.go`: `NewChainHandler(core *runtime.Chainer)
      message.NoPublishHandlerFunc` (topic→Outcome, metadata→PredecessorID, body→Result);
      `NewChainerRunner` + `Chainer.Run(ctx, sub)` subscribing the three topics with
      ack/nack discipline (ack on success/no-op, nack on propagated error, ack+log on
      malformed). Keep all watermill imports in this file.
- [ ] **Verify** — `go test -race ./eventing/...` green; ≥85%.

## Phase 6 — Postgres `ChainLinkStore` + migration [`persistence`]

- [ ] Migration `internal/persistence/postgres/migrations/0008_chain_links.sql`:
      `wrkflw_chain_links` (cols per spec §4.5), `PRIMARY KEY (predecessor_instance_id,
      outcome)`, index on `successor_instance_id`.
- [ ] **RED** — `internal/persistence/postgres/chainlink_test.go` (testcontainers via
      `database.RunTestDatabase`): `Record` round-trip; duplicate `(pred, outcome)` →
      `runtime.ErrChainLinkExists` (23505 mapping); `LookupBySuccessor`/`ListByPredecessor`.
      Plus `Store.Create` duplicate-instance → `runtime.ErrInstanceExists`. Run → FAIL.
- [ ] **GREEN** — `postgres.ChainLinkStore` + 23505 mapping; `persistence.NewChainLinkStore(pool)
      runtime.ChainLinkStore` façade + compile-time assertion.
- [ ] **Verify** — `go test -race -p 1 ./...` green (incl. Postgres); ≥85% touched.

## Phase 7 — Example + docs

- [ ] Testable `Example` (in `eventing` or `runtime`) showing complete→successor wiring.
- [ ] `runtime/README.md` + `eventing` doc: a short "Process-instance chaining" section.
- [ ] (Optional) `examples/scenarios/instance_chaining/main.go` reference wiring.

## Phase 8 — Root type aliases (USER-CONFIRMED, separate)

- [ ] **Do not implement without explicit go-ahead.** List the intended root aliases for
      review: `Chainer`, `SuccessorPolicy`, `SuccessorDecision`, `ChainEvent`, `Outcome`
      (+ constants), `ChainLink`, `ChainLinkStore`, `ErrChainLinkExists`,
      `ErrInstanceExists` (and the `eventing` handler/runner if root-aliased). The user
      adds these in the module-root front-door package.

## Final gate (whole-track)

- [ ] `go test -race -p 1 ./...` green (incl. testcontainers Postgres).
- [ ] `go tool cover` ≥ 85% on `runtime`, `eventing`, touched `internal/persistence/postgres`,
      `persistence`.
- [ ] `golangci-lint run ./...` clean.
- [ ] Engine/model **production diff ZERO**; import-purity intact.
- [ ] ADR-0045 + ADR-0046 committed.
- [ ] Opus whole-branch review before merge.

## Verification checklist (acceptance)

1. Completing an instance starts the mapped successor exactly once (incl. under a
   duplicated terminal event).
2. A failed instance routes to its failure successor (or none) per policy.
3. A **cancelled** instance emits `instance.terminated` (NOT `instance.failed`) and
   routes accordingly; a full-rollback termination also emits `instance.terminated`.
4. `instance.completed` / `instance.failed` (genuine failure) topics + payloads
   unchanged for existing consumers.
5. Lineage is queryable: `LookupBySuccessor` returns the predecessor; `ListByPredecessor`
   returns fan-out hops.
6. No Go files added at the module root; engine/model untouched.
