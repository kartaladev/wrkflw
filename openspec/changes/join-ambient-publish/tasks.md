# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to` and is written test-first: write the test, watch it go red, then make it pass. Cases that share one call go in one table test (`table-test` skill). `T` below stands for `GOTOOLCHAIN=go1.26.9 go test -race -count=1`. Dialect rows need Docker (Postgres via `dbtest.RunTestDatabase`, MySQL via `dbtest.RunTestMySQL`). SQLite rows run without it. Scenario names refer to `specs/definition-store/spec.md`. Starting needs `add-lent-transaction-seam` archived. Tasks naming an Open Question wait for its answer, recorded in design.md first.

## 1. Reds that exist today, and measurements

- [ ] 1.1 Red: write "A single-connection SQLite pool does not deadlock a publish inside a unit" with a 5-second context deadline. This is a test, not a timing. Today the publish should return `context.DeadlineExceeded` instead of nil. Record the observed error in design.md (Context). Also record a before/after wall-clock on the implementing machine for the PR body, replacing the thread's 9m54s / 0.00s, which are not reused. Verify: `T ./internal/persistence/store/ -run Definition` fails for the stated reason. If it passes, record the hang premise as stale and keep the test as a guard.
- [ ] 1.2 Red: add the RunInTx rows of "The unit's outcome decides whether the Version exists" on all three dialects. Today the `rolls back` rows find the definition, because the publish committed on its own. Verify: `T ./internal/persistence/store/ -run Definition` fails on those rows only. The `commits` rows and "Negative control, a publish with no transaction commits on its own" pass.
- [ ] 1.3 Measure G6 (hypothesis, Open Question 3). On MySQL under REPEATABLE READ, open a transaction and take a snapshot. Another connection then publishes "g6" version 1 with different content and commits. Publish "g6" version 1 inside the first transaction. Repeat on Postgres with REPEATABLE READ. Record each outcome in design.md: `ErrConcurrentPublish`, `ErrDefinitionExists`, a serialization error, or success. Verify: the test and its recorded outcome. If either backend returns success for different content, stop and report a defect with that test as its red.

## 2. Publish and reads join the unit

- [ ] 2.1 Green: `PublishDefinition` takes one querier from the context's unit (the engine-owned handle or `add-lent-transaction-seam`'s external handle) when present, and from the pool otherwise. The insert and `readPublished` use that one querier. Verify: 1.1 and 1.2 green, and the existing `definition-store` publish conformance rows pass unchanged on all three dialects.
- [ ] 2.2 Red, then green: write "Publish then read in one unit" on all three dialects, then make `GetDefinition` and `Lookup` read through the unit's querier when present. Verify: `T ./internal/persistence/store/ -run Definition` green, including the republish and different-content rows, which guard against F0b coming back.
- [ ] 2.3 Write the lent-transaction rows of "The unit's outcome decides whether the Version exists" and "Nothing outside the unit sees the publish before it commits", through the `persistence` facade (`package persistence_test`). Verify: `T ./persistence/ -run Definition` green on all three dialects.
- [ ] 2.4 Red, then green: write "Publish a new Version after a refusal, in the same unit". It should go red if the publish marks the unit on a refused outcome. Make a refusal leave the unit unmarked; only a failed statement marks it (design Decision 3). Verify: green on all rows.
- [ ] 2.5 Write "A failed publish statement leaves a lent transaction as it was", injecting the insert failure through a test seam on the definition store's querier. Verify: green on all three dialects.
- [ ] 2.6 Replace the "issue #151" godoc on `PublishDefinition`, the `querier` note, and the `persistence.DefinitionStore` interface comment with the joined contract. Cover both kinds of unit, read-your-own-publish, refusals leaving the unit committable, and the different-database limit. Cite `openspec/specs/definition-store/spec.md`. Verify: `scripts/check-doc-refs.sh` passes, and `grep -n '#151' internal/persistence/store/definitions.go persistence/persistence.go` prints nothing.

## 3. Caching registry under a unit (part waits for Open Question 1)

- [ ] 3.1 Extend the kernel marker from `add-lent-transaction-seam` so `Store.RunInTx` sets it on the context it hands to the callback. Expose one kernel query for "this context carries a transaction" (design Decision 5). Verify: a table test in `runtime/kernel` over a plain context, a lent context and a `RunInTx` context. `runtime/monitor/internal_leak_test.go` stays green.
- [ ] 3.2 Red: write "A rolled-back publish is never served" with a fake clock, on all listed rows. Verify: `T ./persistence/ -run CachingDefinition` fails on every row today. The wrapper caches the in-unit lookup and serves it after rollback.
- [ ] 3.3 Red: write "Concurrent lookups across the transaction boundary do not share a result". Use a backing registry that holds each lookup on a channel until released, and wait on the held state per `docs/agents/eventually-waits.md`. Verify: fails today on at least the first row (shared flight).
- [ ] 3.4 Green: a transaction-carrying lookup never populates the cache and never joins or leads a shared flight. Hits are served per Open Question 1. Rewrite the pinned-key safety argument and the `NewCachingDefinitionRegistry` example godoc (design Decision 4). Verify: 3.2 and 3.3 green, "Negative control, a committed publish is cached as before" green, and the existing `CachingDefinitionRegistry` tests unchanged.
- [ ] 3.5 After Open Question 1 is answered: add the Latest-inside-the-unit row to the spec and its test, and run `openspec validate join-ambient-publish --strict`. Verify: validation passes and the row is green.

## 4. Concurrency contract (waits for Open Question 2)

- [ ] 4.1 Red: two goroutines share one transaction-carrying context and call `Lookup` and `PublishDefinition` on the definition store concurrently. Verify: `T ./internal/persistence/store/ -run Concurrent` reports a data race today, on Postgres and SQLite at least.
- [ ] 4.2 Add the ADDED requirement for the answered contract to `specs/definition-store/spec.md`. Include its negative control: sequential calls on one context succeed. Then implement it. Verify: 4.1 green under `-race`, the new scenario green, and `openspec validate join-ambient-publish --strict` passes.

## 5. Publish and start in one lent transaction

- [ ] 5.1 Write "Publish and start together" in `service` with the durable definition store as the registry. Verify: `T ./service/ -run Lent` green on all three dialects.
- [ ] 5.2 Apply the answer to Open Question 3 (from the 1.3 measurement): amend the `ErrConcurrentPublish` godoc, or add the chosen sentinel and its scenario. Verify: the 1.3 test asserts the decided outcome on MySQL and Postgres.

## 6. Integration

- [ ] 6.1 Run the whole suite per module as CONTRIBUTING.md describes (`scripts/modules.sh`), with Docker for dialect rows. Run `scripts/lint.sh` per module, `scripts/check-doc-refs.sh`, and `scripts/check-openspec.sh`. Verify: all exit 0.

## Workflow follow-up

- The PR names `join-ambient-publish` and carries the 1.1 before/after measurement in its body. Its last commit runs `openspec archive join-ambient-publish`.
- Comment on kartaladev/wrkflw#151 and close it. Tell kartaladev/worktrack#27 that publish-and-start in one lent transaction has landed.
