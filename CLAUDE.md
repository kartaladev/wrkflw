# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`wrkflw` is a **Go workflow engine, shipped as a library** — not an executable backend. The
deliverable is an **importable Go module**; there is no daemon we own and run. It can be
embedded directly in a consumer's Go application or assembled by the consumer into a
standalone deployment (e.g. sidecar / container) reachable through the library's REST or gRPC
surfaces. Read the load-bearing properties below before any design work — they shape every
decision, and the Architecture section expands the rest:

- **Library-first, always**. The product is the **module-root public API** (the exported
  packages at the repo root — e.g. `engine/`, `model/`, `runtime/`; **no `pkg/` prefix**, see
  ADR-0004) that a consumer imports and embeds in *their* application. Every feature must be
  reachable and ergonomic through that API. When a design choice trades library ergonomics for
  server convenience, library ergonomics win.
- **Transports are library-provided, not shipped binaries**. The REST and gRPC surfaces
  exist so a consumer can *mount* them in their own server — expose them as
  constructors/handlers/`http.Handler` / gRPC `ServiceRegistrar` registrations from the public
  root packages, configured by the consumer's DI and lifecycle. "Standalone (sidecar / container)" is a
  deployment shape the **consumer** assembles from these pieces; we do not own a `main`.
  Any binaries in this repo are **example/reference wiring only**, never the product.
- Server/transport concerns must **never leak into the engine core** — the core depends on
  interfaces only and is consumable with no transport imported at all.
- **BPMN semantics**: model process definitions on BPMN2 concepts (tasks, gateways,
  events, sequence flows). Definitions load from BPMN2 XML, but **YAML and direct Go code
  are the preferred authoring forms**. A *process definition* is a template; a *process
  instance* is a running execution of it.
- **Token-based execution**: transitions between nodes are modeled by **tokens**. A token
  carries process-instance variables that downstream nodes read to make decisions (e.g. an
  exclusive gateway choosing a branch). Token movement is the engine's core state machine.
- **Expression evaluation**: use [`expr-lang/expr`](https://github.com/expr-lang/expr)
  wherever a definition or execution needs to evaluate an expression (gateway conditions,
  data/attribute predicates, timer durations). Do not hand-roll an expression language.

## Tech Stack (locked — changing any of these requires an ADR)

| Concern | Choice | Notes |
|---|---|---|
| Language | **Go 1.25** | hard requirement |
| Database | **PostgreSQL 17** (primary) or **MySQL 8.0+**, SQL-based | hot-path data must be cached to avoid overloading the DB (see ADR-0073) |
| Expressions | `github.com/expr-lang/expr` | all in-definition / in-execution expressions |
| Eventing | [`watermill`](https://github.com/ThreeDotsLabs/watermill), **outbox publishing** | **never import watermill from workflow code** — go through the eventing abstraction (no vendor lock-in) |
| Scheduling | [`go-co-op/gocron`](https://github.com/go-co-op/gocron) **pinned to v2.21.2** | hard pin; timers, SLA waiters, in-wait actions |
| Time source | [`jonboulle/clockwork`](https://github.com/jonboulle/clockwork) | implements the in-repo `clock.Clock` interface (ADR-0003) — **never import clockwork from engine/workflow code**, depend on `clock.Clock`; shared with gocron so a fake clock drives both engine + scheduler in tests; core never reads the wall clock |
| Authorization | pluggable; **casbin** as the baseline | role, resource-privilege, **and attribute-based** (data/process-variable) evaluation |
| DI container | [`samber/do` v2](https://github.com/samber/do) | application-layer wiring only — see Dependency Injection below |
| Tests w/ external resources | [`testcontainers-go`](https://github.com/testcontainers/testcontainers-go) | real Postgres/MinIO/SNS in tests, never mocked |

## Repository Layout (single Go module)

One `go.mod` at the repo root. **Library consumers import this single module path** — the
exported **module-root packages** *are* the product. There is **no `pkg/` prefix** (ADR-0004):
public packages live directly at the repo root.

- **Module-root packages** (e.g. `engine/`, `model/`, `action/`, `authz/`, `runtime/`) — the
  **public engine library** and its value/stateless helpers. This is the entire API surface for
  embedded consumers. Token execution, process-definition model, gateway logic, the
  service-action catalog interface, the eventing/authz/persistence *abstractions*, and the
  **transport adapters consumers mount** (REST `http.Handler` factories, gRPC service
  registrations) all live here. Consumers import them as `github.com/zakyalvan/krtlwrkflw/engine`,
  etc.
- `internal/` — non-exported implementation details (concrete persistence, outbox plumbing,
  casbin adapters, watermill wiring) that consumers must not import.
- `examples/` — optional **reference wiring** showing how a consumer embeds the engine and
  mounts its transports. These are illustrative `main` packages, **not a product we ship or
  run**; they must not become the only path through which a feature is reachable.
- `docs/adr/` — Architecture Decision Records, `NNNN-<slug>.md`, **Nygard
  template** (see `docs/adr/0001-record-architecture-decisions.md`).
- `docs/specs/` — **specs/design docs** produced by `superpowers:brainstorming`
  (and any spec-writing skill). One `<slug>.md` per feature/decision.
- `docs/plans/` — **implementation plans** produced by `superpowers:writing-plans`
  (and `superpowers:executing-plans` inputs). One `<slug>.md` per plan.

Paths must **never** contain the word `superpowers`: specs go in `docs/specs/`,
plans go in `docs/plans/` — regardless of where a skill's defaults would place
them.

There is no `cmd/` of owned daemons. If a reference binary is genuinely useful, it lives in
`examples/` and stays thin — all real behaviour belongs in the public root packages so it is
testable and reusable by consumers.

## Architecture (the big picture)

These are the seams to understand before touching code; they span multiple packages.

- **Engine core (token state machine)** — drives a process instance by moving tokens across
  nodes per the definition's sequence flows. Gateways (exclusive/parallel/etc.) read token
  variables (via `expr`) to decide routing. Keep this **pure of transport, storage vendor,
  and event-bus specifics** — it depends on interfaces only.
- **Persistence** — SQL/Postgres-backed definition + instance + token state. Identify hot
  read paths and put a cache in front of them. The DB is the source of truth; the outbox
  table is part of it.
- **Eventing abstraction** — workflow code emits domain events through an in-repo interface;
  an `internal/` adapter implements it over watermill using the **transactional outbox**
  pattern (events written in the same tx as state changes, relayed afterward). Swapping
  watermill for another broker must touch only the adapter.
- **Service-action catalog** — actions usable from definition nodes, referenced **by name**,
  all implementing a single `ServiceAction` interface. The catalog resolves names →
  implementations at execution time.
- **Scheduling / waiters** — gocron drives timer tasks and SLA deadlines (e.g. a human task
  due in 3 working days → on breach, run alternative action(s) then take an alternative
  path). Support **in-wait actions** (e.g. reminder emails) executed *during* a wait period,
  not only on expiry.
- **Authorization** — pluggable authz evaluated at (at least) human-task nodes. Must support
  role-based, resource-privilege-based, **and attribute-based** rules over process/data
  variables. casbin is the default engine behind the abstraction.
- **Compensation / rollback** — each node may carry **optional, pluggable compensation
  action(s)** so a process can be rolled back to a previous node (error recovery or
  debugging).
- **Resilience** — process errors must be **retryable**; design for the other resilience
  concerns (idempotency, backoff, poison handling) deliberately.
- **Observability** — expose process **metrics**, enable **traces**, and log via the
  standard library **`slog`**.
- **API response customization** — the API surface must allow customizing the
  `ProcessInstance` response shape (a v1 engine already exists; minimize migration effort).
- **Admin/superuser monitoring** — a way for admins to monitor all processes, likely
  implemented as middleware and/or a set of HTTP handlers.

**Before designing any of these for the first time**, run `superpowers:brainstorming`,
do comprehensive research about workflow-management best practices (a standing project
requirement), and record the decision as an ADR.

## Common Commands

```bash
go build ./...                                   # build everything
go test ./...                                    # all tests (workspace-wide)
go test ./<package>/...                          # one package (root-level, e.g. ./engine/...)
go test -run '^TestName$' ./<package>/...        # a single test
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...                          # lint (clean before done)
go generate ./...                                # regenerate mocks (mockgen) etc.
```

The repo is pre-`go.mod`: run `go mod init <module-path>` before any of the
above work (this is the Go-module counterpart to the `git init` step in Git
Discipline). Tests touching Postgres/MinIO/SNS use testcontainers and need a
running Docker daemon.

## Dependency Injection

Because this is a library, **DI is a consumer choice, not something we impose**. The public
root-package API must be fully usable with plain constructors and interface parameters — never
require a consumer to adopt `samber/do` to use the engine.

`samber/do` v2 is used **internally and in `examples/` reference wiring** to compose the
engine's own stateful collaborators (services, repositories, orchestrators, background
workers). Where used, register providers via `do.Provide` / `do.ProvideNamed` and resolve
via `do.MustInvoke[T](injector)`. Prefer **interface-typed providers** so tests swap
implementations through a child injector (`do.New(parent)`). Always also offer a plain
constructor so a consumer who doesn't use a container can wire the same component by hand.

**Do not** force DI on pure value-types or stateless packages — those have no behaviour to
inject. The seam is: *anything that holds state, owns I/O, or depends on configuration.*

## Rule of Thumbs

### General

When working, you must always:

1. Analyze the codebase first; scan the Go package you're working on and its dependencies before changing anything.
2. Ask when in doubt — don't deliver assumptions. The codebase may already answer the question.
3. Proactively present options with context and trade-offs at every complex decision fork.
4. Provide code snippets for multi-line changes so the user can make informed decisions.
5. Write architecture decision records (ADR) for decisions made. ADRs **must** follow the **Nygard template** (Status/Date, Context, Decision, Consequences). Store them under `docs/adr/NNNN-<slug>.md`, using `docs/adr/0001-record-architecture-decisions.md` — itself written in that template — as the canonical example.
6. **TDD strict** (see "TDD Operational Discipline" below — **read it before each new symbol**): no production code before a failing test. Use `superpowers:test-driven-development` as the workflow and `cc-skills-golang:golang-testing` as the Go baseline; the project's `table-test` and `use-mockgen` skills override its table-test closure style and mock-generation steps. Do not exit red-green-refactor before all tests are green.
7. Use `superpowers:brainstorming` before implementing anything new — state the problem, present 2–3 options with trade-offs, then write the plan. Persist the resulting spec/design doc under `docs/specs/<slug>.md`.
8. Create an explicit execution plan with a `verification checklist` for any task spanning 3+ steps. Persist plans (e.g. from `superpowers:writing-plans`) under `docs/plans/<slug>.md`.
9. Write tests for untested legacy code. Suggest improvements for poor or smelly legacy code per your analysis. Run tests first; benchmarks for multi-option decisions are highly appreciated.

### Golang

1. Strict adherence to Go idioms and best practices.
2. The `cc-skills-golang:*` skill family covers most Go topics; load the ones the task needs. See the **Required Go skills** section below for the always-on list (and the broader family it references).
3. Use [testcontainers-go](https://github.com/testcontainers/testcontainers-go) for tests requiring heavy external resources (database, MinIO, SNS). For database tests, prefer the shared `database.RunTestDatabase(t, opts...)` helper once it exists.
4. Use the project's `table-test`, `use-testcontainers`, and `use-mockgen` skills alongside `cc-skills-golang:golang-testing`. These custom skills override or extend parts of `golang-testing`.
5. Prefer **black-box tests** (use `<package>_test`).
6. Write testable examples (https://go.dev/blog/examples) for code directly consumed by library users — the embedded-engine root-package API especially.
7. **Dependency injection**: see the Dependency Injection section above.

## TDD Operational Discipline (READ BEFORE EVERY NEW SYMBOL)

This section is the non-negotiable interpretation of rule #6. It is written
to be **impossible to skim past or "batch through"**. The user audits the
conversation transcript and verifies that every implementation was
preceded by a visible red state.

### The Mandatory Cycle

For **every** new exported symbol (function, method, type with behaviour,
constructor, HTTP/gRPC handler, DI provider, etc.) and for **every** behavioural
change to an existing symbol:

1. **Red** — Write the test file (or extend the existing one) with the new
   assertion. Save it.
2. **Red verification** — Run `go test ./<package>/...` (root-level, e.g.
   `./engine/...`) in a Bash tool call. **The build must fail or the
   assertion must fail.** The failure
   itself is the evidence that step 1 happened before step 3. A compile
   error like `undefined: NewThing` is a valid red state.
3. **Green** — Write the minimum implementation that makes the test pass.
   Save it.
4. **Green verification** — Run the same `go test` invocation. It must pass.
5. **Refactor** — Optional, but if you do, run the tests again.

### Forbidden Patterns

These patterns silently bypass the cycle. Do not use them:

- **Forbidden**: a `Write` tool call creating `foo_test.go` followed
  immediately by a `Write` tool call creating `foo.go`, with no `Bash`
  call running `go test` in between. The red state is not observable from
  the transcript.
- **Forbidden**: a single `Write` tool call that creates both the test and
  the implementation in any form.
- **Forbidden**: writing the implementation first "to figure out the
  shape," intending to add tests after. The shape is supposed to emerge
  from the test.
- **Forbidden**: batching multiple symbols' worth of tests + impls in one
  edit pass, even if each pair would individually be fine.

### Why This Strictness

A previous session shipped a phase across five packages without observable red
states. The code still passed lint and coverage, but the discipline broke
because the audit trail was missing. The intent of TDD is the cycle, not
just the final coverage number. Treat the cycle as a deliverable in its
own right.

### What Counts as "New Behaviour"

- Adding a method to an interface ⇒ test first.
- Adding a parameter to an existing method ⇒ test for the new parameter first.
- Bug fix ⇒ regression test that reproduces the bug first.
- Adding error handling that returns a new sentinel error ⇒ test for the new error case first.
- Pure refactor with no behavioural change ⇒ no new test needed, but existing tests must still pass before AND after.

### Self-Audit Before Committing

Before staging any commit, ask: *"Could a reviewer reading this
conversation see the red state for every new symbol?"* If no, the work is
not done — go back and add the missing red verifications by checking out
the symbol and writing the test now (it will still fail; the impl is
already there — that's fine for retroactive verification, but discloses
the lapse).

## Verification

On completion of any change, verify:

1. All tests for the touched package pass with ≥ 85% line coverage:
   ```bash
   go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
   ```
2. `go test ./...` from the repo root passes — no regressions elsewhere.
3. `golangci-lint run ./...` is clean. Use the `cc-skills-golang:golang-lint` skill if configuration is needed.

## Common Pitfalls

1. Don't ignore pre-existing errors in packages you aren't working on. Never excuse them as "not caused by this session." Queue them as follow-up tasks and address by priority.
2. Stick to skills explicitly listed under "Rule of Thumbs". If a skill outside that list seems applicable, ask before using it.
3. Never import watermill, casbin, gocron, or clockwork directly from workflow/engine code — go through the in-repo abstraction (the eventing interface, the `Authorizer`, the scheduler port, the `clock.Clock` interface) so vendors stay swappable.

## Git Discipline

Always commit per logical change. Ask before committing. Use Conventional Commits scoped to the area:

- `feat(<scope>): <description>`     — new functionality
- `fix(<scope>): <description>`      — bug fix
- `chore(<scope>): <description>`    — tooling / cleanup
- `refactor(<scope>): <description>` — behavior-preserving restructure
- `docs(<scope>): <description>`     — documentation

This repository is **not yet a git repo** — initialize one (`git init`) before the first commit.

## Required Go skills

The following Go skills from `samber/cc-skills-golang` MUST always be applied when working on this project. Load them at the start of every Go-related task, regardless of whether the user explicitly mentions them.

Core:

- `samber/cc-skills-golang@golang-code-style`
- `samber/cc-skills-golang@golang-data-structures`
- `samber/cc-skills-golang@golang-design-patterns`
- `samber/cc-skills-golang@golang-documentation`
- `samber/cc-skills-golang@golang-error-handling`
- `samber/cc-skills-golang@golang-modernize`
- `samber/cc-skills-golang@golang-naming`
- `samber/cc-skills-golang@golang-safety`
- `samber/cc-skills-golang@golang-security`
- `samber/cc-skills-golang@golang-testing`
- `samber/cc-skills-golang@golang-troubleshooting`

Domain (this engine specifically):

- `samber/cc-skills-golang@golang-database` — Postgres 17, transactions, hot-path caching, the outbox table.
- `samber/cc-skills-golang@golang-concurrency` — token execution, gocron waiters, background relayers.
- `samber/cc-skills-golang@golang-context` — cancellation/deadline propagation through the engine.
- `samber/cc-skills-golang@golang-structs-interfaces` — the eventing/authz/action/persistence abstractions.
- `samber/cc-skills-golang@golang-observability` — required metrics, traces, and `slog` logging.
- `samber/cc-skills-golang@golang-dependency-injection` — DI decision/concepts.
- `samber/cc-skills-golang@golang-samber-do` — the locked `samber/do` v2 container for internal/example wiring.
