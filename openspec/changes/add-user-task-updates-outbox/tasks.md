# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`, and every test goes red before the code that makes it pass. Tables follow the `table-test` skill. Dialect rows use `database.RunTestDatabase` (Docker required). All commands use `GOTOOLCHAIN=go1.26.9`. Run tests per module as CONTRIBUTING.md describes.

Q1 must be ruled before 3.1, and Q2 before 1.2. If Q2 rules opt-in, every test below runs with the option enabled, plus one off-by-default row.

## 1. Derivation

- [ ] 1.1 Write `runtime/outbox_test.go` rows first for `taskOutboxEvents` over hand-built pre- and post-step views: one row per transition, the implicit-claim ordering, and the no-transition controls. Watch them go red (the function does not exist). Scenarios: "The transition picks the topic", "One step, two transitions, in lifecycle order" and "No transition, no event". Verify with `go test ./runtime/ -run TestTaskOutboxEvents`.
- [ ] 1.2 Implement `taskOutboxEvents` (design D1) and capture the pre-step task view in `deliverLoop` beside `prevStatus`. Make 1.1 green. Add a driver-level test that a refused trigger writes no outbox row. Scenario: "The event exists only if the step commits". Verify with `go test -race ./runtime/...`.

## 2. Payload

- [ ] 2.1 Write the payload table first (exact keys and values per topic, empty actor for engine-driven transitions), the secret-marker test, and the on-behalf row (skip it until `replace-casbin-with-composed-deciders` is on main). Then implement the builder (design D2, D3). Scenarios: "The keys and their values", "No human, empty actor", "Values stay behind the disclosing read routes" and "The principal is the actor of record". Verify with `go test -race ./runtime/...`.

## 3. Dedup and atomicity

- [ ] 3.1 After Q1 is ruled, write the store test first: a step that writes the same topic for two tasks, on Postgres, MySQL and SQLite, asserting distinct keys with the instance-and-seq prefix. Add the relay-retry stable-ID row. Under option (a) these may pass immediately; prove they can fail by planting a constant key, then revert. Scenarios: "Two tasks, one topic, one step" and "Redelivery keeps the envelope id". Verify with the store package tests (Docker up).
- [ ] 3.2 Write the CAS-failure rows on the three dialects and the in-memory store. Scenario: "CAS failure". Verify with the per-module test loop (Docker up).

## 4. Docs

- [ ] 4.1 In `runtime/README.md`'s outbox section, add the topic table, the payload shape, the dedup rule, and the documented `instance.completed` line ("ships the whole variables map unprojected; task events carry references only"). Cite `openspec/specs/event-delivery/spec.md`. Verify with `scripts/check-doc-refs.sh` and a full per-module `go test -race` run.

## Workflow follow-up

- The PR's last commit runs `openspec archive add-user-task-updates-outbox`.
