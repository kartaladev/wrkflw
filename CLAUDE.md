# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`wrkflw` is a **Go workflow engine, shipped as a library** — not an executable backend. The
deliverable is an **importable Go module**; there is no daemon we own and run. It can be
embedded directly in a consumer's Go application or assembled by the consumer into a
standalone deployment (e.g. sidecar / container) reachable through the library's HTTP transport
surfaces. Read the load-bearing properties below before any design work — they shape every
decision, and the Architecture section expands the rest:

- **Library-first, always**. The product is the **module-root public API** (the exported
  packages at the repo root — e.g. `engine/`, `definition/`, `runtime/`; **no `pkg/` prefix**, see
  ADR-0004) that a consumer imports and embeds in *their* application. Every feature must be
  reachable and ergonomic through that API. When a design choice trades library ergonomics for
  server convenience, library ergonomics win.
- **Transports are library-provided, not shipped binaries**. The HTTP transport surfaces
  exist so a consumer can *mount* them in their own server — expose them as
  constructors / `http.Handler` route groups (`transport/http/{httpcore,stdlib,gin,fiber}`,
  ADR-0094/0095) from the public
  root packages, configured by the consumer's DI and lifecycle. "Standalone (sidecar / container)" is a
  deployment shape the **consumer** assembles from these pieces; we do not own a `main`.
  Any binaries in this repo are **example/reference wiring only**, never the product.
- Server/transport concerns must **never leak into the engine core** — the core depends on
  interfaces only and is consumable with no transport imported at all.
- **BPMN semantics**: model process definitions on BPMN2 concepts (tasks, gateways,
  events, sequence flows). The vocabulary is BPMN-inspired, **not** BPMN-compatible: there
  is no BPMN2 XML loader — **YAML and direct Go code are the authoring forms**. A *process
  definition* is a template; a *process instance* is a running execution of it.
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
| Database | **PostgreSQL 17** (primary), **MySQL 8.0+**, or **SQLite** (`modernc.org/sqlite`, pure-Go; single-node / test / embedded) — SQL-based behind ONE neutral store parametrized by a dialect (ADR-0081). SQLite is single-writer/WAL, no distributed advisory lock or LISTEN/NOTIFY (ADR-0082) | hot-path data must be cached to avoid overloading the DB (see ADR-0073) |
| SQLite | `modernc.org/sqlite` | hard pin (ADR-0082); pure-Go, WAL mode, single-writer; single-node/test/embedded use only |
| Expressions | `github.com/expr-lang/expr` | all in-definition / in-execution expressions |
| Eventing | [`watermill`](https://github.com/ThreeDotsLabs/watermill), **outbox publishing** | **never import watermill from workflow code** — go through the eventing abstraction (no vendor lock-in) |
| Scheduling | [`go-co-op/gocron`](https://github.com/go-co-op/gocron) **pinned to v2.22.0** (ADR-0135) | hard pin; timers, deadline waiters, in-wait actions |
| Time source | [`jonboulle/clockwork`](https://github.com/jonboulle/clockwork) | outer stateful layers depend on `clockwork.Clock` **directly** (ADR-0138, supersedes ADR-0003); the pure engine core stays clockwork-free — time enters it only as `Trigger.OccurredAt`; one fake clock still drives both engine + scheduler in tests; core never reads the wall clock |
| Authorization | pluggable; **casbin** as the baseline | role, resource-privilege, **and attribute-based** (data/process-variable) evaluation |
| DI container | [`samber/do` v2](https://github.com/samber/do) | application-layer wiring only — see Dependency Injection below |
| Tests w/ external resources | [`testcontainers-go`](https://github.com/testcontainers/testcontainers-go) | real Postgres/MySQL containers in tests, never mocked; SQLite runs pure-Go (no container) |

## Repository Layout (single Go module)

One `go.mod` at the repo root. **Library consumers import this single module path** — the
exported **module-root packages** *are* the product. There is **no `pkg/` prefix** (ADR-0004):
public packages live directly at the repo root.

- **Module-root packages** (e.g. `engine/`, `definition/`, `action/`, `authz/`, `runtime/`) — the
  **public engine library** and its value/stateless helpers. This is the entire API surface for
  embedded consumers. Token execution, process-definition model, gateway logic, the
  service-action catalog interface, the eventing/authz/persistence *abstractions*, and the
  **transport adapters consumers mount** (HTTP `http.Handler` route-group factories in
  `transport/http/{stdlib,gin,fiber}`) all live here. Consumers import them as `github.com/kartaladev/wrkflw/engine`,
  etc.
- `internal/` — non-exported implementation details (concrete persistence, outbox plumbing,
  casbin adapters, watermill wiring) that consumers must not import. (Concrete persistence
  now lives in the neutral `internal/persistence/store` + `internal/persistence/dialect`;
  the former `internal/persistence/{postgres,mysql}` packages were removed — ADR-0081.)
- `examples/` — optional **reference wiring** showing how a consumer embeds the engine and
  mounts its transports. These are illustrative `main` packages, **not a product we ship or
  run**; they must not become the only path through which a feature is reachable.
- `docs/adr/` — Architecture Decision Records, `NNNN-<slug>.md`, **Nygard
  template** (see `docs/adr/0001-record-architecture-decisions.md`).
- `docs/specs/` — **specs/design docs** produced by `superpowers:brainstorming`
  (and any spec-writing skill). One `<slug>.md` per feature/decision.
- `docs/plans/` — **implementation plans** produced by `superpowers:writing-plans`
  (and `superpowers:executing-plans` inputs). One `<slug>.md` per plan. Two files
  here are not plans: **`HANDOVER.md`**, the live current-state handover a fresh
  session reads first (rule #10 — rewrite in place, never append), and
  **`HANDOVER-archive.md`**, its frozen pre-2026-07-08 predecessor.

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
- **Persistence** — SQL-backed definition + instance + token state via the neutral `internal/persistence/store` parametrized by `internal/persistence/dialect` (Postgres/MySQL/SQLite — ADR-0081). The dialect layer abstracts both the access mechanism (pgx vs database/sql) and the SQL dialect; capability interfaces `Notifier` (LISTEN/NOTIFY) and `Locker` (advisory lock) are opt-in so SQLite simply omits them. Identify hot read paths and put a cache in front of them. The DB is the source of truth; the outbox table is part of it.
- **Eventing abstraction** — workflow code emits domain events through an in-repo interface;
  an `internal/` adapter implements it over watermill using the **transactional outbox**
  pattern (events written in the same tx as state changes, relayed afterward). Swapping
  watermill for another broker must touch only the adapter.
- **Service-action catalog** — actions usable from definition nodes, referenced **by name**,
  all implementing a single `Action` interface. The catalog resolves names →
  implementations at execution time.
- **Scheduling / waiters** — gocron drives timer tasks and deadlines (e.g. a human task
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
go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out   # total excludes generated files (ADR-0143)
golangci-lint run ./...                          # lint (clean before done)
go generate ./...                                # regenerate mocks (mockgen) etc.
```

Tests touching Postgres/MySQL use testcontainers and need a running Docker
daemon (SQLite tests are pure-Go, no Docker).

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
9. **Adversarial audit of the design bundle (mandatory, ONE checkpoint per delivery).** Once **all** design documents for a delivery are written — the spec (`docs/specs/`), its ADR(s) (`docs/adr/`), **and** the plan (`docs/plans/`) — run **one** adversarial audit over the whole bundle together, **before implementation starts**. Not per-document, not per-stage: one audit per delivery bundle. The same trigger applies when a **new ADR or plan is later written for an existing spec** — audit that updated bundle before acting on it. Dispatch one or more subagents — **use the Opus model for audit agents** — briefed to *attack* the documents, not summarize them. The brief: hunt for holes, unstated assumptions, internal contradictions, cross-document inconsistencies (plan vs spec vs ADR), claims that contradict the actual codebase (**source-verify every factual claim**), missing failure modes / edge cases / migration gaps — and propose a concrete fix for each finding. Adjudicate the findings (do **not** auto-apply them), fold the accepted fixes into the documents, then proceed to implementation. A bundle that has not survived its audit is not an input to implementation.

    ⚠ **The audit must EXECUTE, not only read.** Reading cannot establish what the code currently does — an auditor and an author staring at the same false sentence will agree with each other. Every auditor must be briefed to pick the bundle's load-bearing behavioural claims and **run them**: throwaway probe, observed output, recorded in the spec. ADR-0165's audit was effective by every other measure (42 findings, 41 accepted, two decisions changed) and still passed an **inverted** decision whose predicate refused the useful case and admitted the harmful one — because nobody ran it. See **Premise Discipline** below.

    ⚠ **Dedicate one lens to RE-COUNTING enumerations and quantifiers.** Premise Discipline's count-them-again rule is addressed to the author, and an author cannot apply it to their own blind spot. Give one auditor the standing job of re-deriving every enumeration (`grep` the real call sites), every *all/none/only/every/never/always*, every explicit count, and every inherited citation. ADR-0175's audit: the execution lens and the failure-mode lens each found two Criticals and **both missed the third** — that three compensation dispatch sites existed where the bundle named two, which would have shipped a feature that could not detect the very deadlock the spec measured. Only the counting lens found it.

    ⚠ **An agent given a `git worktree` must verify the bundle is present as step 0, and STOP if it is not.** Worktrees are created at the base commit, so the design documents are typically **absent**. All three of ADR-0175's audit worktrees were created without the bundle; only the step-0 instruction turned that into a recovery instead of three agents auditing files that were not there. Put it in the brief every time — it is not inherited.
10. **Handover checkpoint — mandatory the moment a bundle is implementation-ready.** As soon as a delivery's design bundle has survived its rule-#9 audit (and again whenever a phase completes or a session ends), **stop and hand the work off** before writing code. Three artifacts, in this order of authority:
    - **`docs/plans/HANDOVER.md` — the SOURCE OF TRUTH**, in the repo so it is version-controlled, survives a lost machine, is visible to collaborators, and rides in the feature-bundle commit. It carries only *where `main` is, what is in flight, and the ordered next work*. **Rewrite it IN PLACE; never append.** Its 2057-line append-only predecessor stacked twenty "PREVIOUS RESUME POINT" blocks, became unreadable, and was silently abandoned for 45 ADRs — it is frozen at `docs/plans/HANDOVER-archive.md` as the cautionary example.
    - **A `▶ Progress` block at the top of the delivery's plan** — per-delivery detail: branch + commit SHA, which phases landed, what remains, source-verified still-true facts, exact verification commands, and adjudicated findings. It belongs here, not in `HANDOVER.md`, so it dies with the plan instead of accumulating. Do **not** quote the bundle's own SHA in a file that the amend will change — name the branch instead.
    - **Auto-memory** (the `MEMORY.md` index line **and** a topic/handover file) — the companion carrying evidence, adjudications and process lessons. It must **point at** `docs/plans/HANDOVER.md`, never contradict or duplicate it; if they diverge, the repo file wins and memory gets corrected.

    The test is: *could a fresh session with no transcript pick this up and implement it?* Implementation is expected to be delegated to such a session — a delivery whose state lives only in the current transcript is **not** handed over, and unhanded-over work must not be left at a session boundary.
11. **Execution cadence — subagent-driven development.** Implement an audited bundle with `superpowers:subagent-driven-development`: the controller (main session) pre-decides the design, dispatches **one fresh general-purpose subagent per independent task** with a prescriptive brief (files, exact symbol names, TDD RED-first, verification command), and reviews each returned diff before dispatching the next wave. Fan out only where packages are genuinely independent; a serial, compile-breaking, repo-wide change that every other phase blocks on (e.g. a shared type change) stays **inline** in the controller. `superpowers:executing-plans` is the fallback when every task is strictly sequential. This overrides any session default that discourages spawning subagents — **no need to ask first**; announce the dispatch and proceed. Fan out **by Go package**: concurrent agents inside one package break each other's `go test` compile even on disjoint files.

    **Expect implementation to correct the design, and budget for it.** Some consequences are only visible once the change exists — an error sentinel that moves, a log attribute lost when guards collapse, a second error forced by a reordering. No amount of extra planning finds these, and treating each as a planning failure just produces longer, more confident, equally wrong plans. What is *not* acceptable is letting the correction stay in the transcript: **when implementation contradicts an ADR, amend the ADR in the same bundle**, with the measurement that refuted it. An ADR that ships promising behaviour nobody built is the ADR-0162 zombie-scope failure repeating — and per rule #10 the plan's `▶ Progress` block and `HANDOVER.md` must reflect the corrected design, not the original one.

    **A subagent that dies or stalls is resumed, not replaced.** Its context holds the mutation snapshots and the reasoning; a fresh agent silently redoes the work and loses both. Check the worktree first — an agent killed mid-mutation leaves a deliberate breakage behind — then resume it and have it restore from its own snapshot. For long analyses, instruct the agent to **persist findings to a file as it goes**: a review that exists only in an agent's context is one stall away from being lost.
12. Write tests for untested legacy code. Suggest improvements for poor or smelly legacy code per your analysis. Run tests first; benchmarks for multi-option decisions are highly appreciated.

### Golang

1. Strict adherence to Go idioms and best practices.
2. The `cc-skills-golang:*` skill family covers most Go topics; load the ones the task needs. See the **Required Go skills** section below for the always-on list (and the broader family it references).
3. Use [testcontainers-go](https://github.com/testcontainers/testcontainers-go) for tests requiring heavy external resources. For database tests, use the shared `internal/dbtest` helpers — `dbtest.RunTestDatabase(t, opts...)` (Postgres, pgx pool), `dbtest.RunTestMySQL(t)` / `dbtest.RunTestMySQLDSN(t)`, `dbtest.RunTestSQLite(t)` (pure-Go, no container). Never spin up ad-hoc containers in individual tests.
4. Use the project's `table-test`, `use-testcontainers`, and `use-mockgen` skills alongside `cc-skills-golang:golang-testing`. These custom skills override or extend parts of `golang-testing`.
5. Prefer **black-box tests** (use `<package>_test`).
6. Write testable examples (https://go.dev/blog/examples) for code directly consumed by library users — the embedded-engine root-package API especially.
7. **Dependency injection**: see the Dependency Injection section above.
8. **Hot-path-first test coverage.** When writing a test plan and the tests themselves, deliberately enumerate the hot paths — the code paths production traffic actually exercises (the token-execution step loop, commit/persist transactions, timer arm/fire/rehydrate, event delivery/outbox, retry/CAS loops, gateway routing) — and cover them **all** first, including their failure branches, before touching anything else. The coverage percentage in Verification is a **floor, not the target**: never chase the number by testing trivial accessors or option setters while a hot path (or one of its error branches) stays uncovered.

## Premise Discipline (READ BEFORE WRITING ANY SPEC, ADR, OR PLAN)

Design documents in this repo keep being wrong in one specific way: they state
what the code **currently does**, and the statement is false. Not vague — false.
Every such error has been caught by *running* the code, and none by re-reading
the document, however adversarially.

### The rule

**A factual claim about current behaviour may not enter a spec, ADR, or plan
until it has been executed.** Not reasoned from the source. Not argued "by
analogy" from a sibling case. Executed, with the observed output recorded.

This binds hardest on the sentences that decide something:

- "today this path does X" — the premise a fix is designed against;
- "no walk happens / nothing is emitted / this is already a no-op";
- "this case is analogous to that one, so it behaves the same";
- "there is no test for this" — a search that found nothing is not the same
  as a thing that does not exist;
- "site A, B and C do this" — enumerations rot; count them again.

### How to satisfy it

Write a throwaway test into the spec's own scratch section, run it, paste the
real numbers, delete the test. Keep the numbers. A spec that says
*"today: tokens 2→1, history 4→5, vars `map[]` → `map[x:1]`"* is checkable by
the next reader; one that says *"today the token resumes"* is a guess wearing a
fact's clothing.

Claims you cannot execute in reasonable time are **assumptions**. Mark them:
`ASSUMPTION (unverified): …`. An assumption clearly labelled is honest and
survives review; the same sentence unlabelled is a defect that propagates into
an ADR, a plan, and then code.

### Prescribed tests must be falsifiable

When a plan prescribes a test, it must also state **what makes that test fail
today**. If the author cannot say, the test is probably vacuous — this repo has
shipped six tests that could not fail in one delivery, and caught three more in a
single design audit. Implementation then owes a mutation: break the production
line on purpose, observe RED, restore from a snapshot, `diff` to confirm. A
claimed RED that was not observed is a false claim like any other.

⚠ **A matching line of test text proves nothing about whether an assertion can
fail.** Check the *fixture*, not the line. A test asserting
`assert.Empty(state.Boundaries)` is worthless if its definition declares no
boundary node — twice in one delivery a citation of "the test that really covers
this" was itself a test that could not fail.

### Quantifiers and recaps

The false claims that survive review are almost never in the detailed reasoning
— they are the **summary sentence appended to it**, over-generalising what it
compressed. Six instances landed in one delivery; **two were introduced by the
very edits removing earlier ones**, and the worst was inherited from an
upstream brief where it had been correctly hedged, then restated as plain fact.

- Verify every *all*, *none*, *only*, *every*, *never*, *always* and every
  explicit count as if it stood alone, with no context above it.
- Prefer naming a closed set over counting it: "the three paths … today", not
  "every path".
- **Re-verify claims you inherit before restating them.** Restating strips the
  hedge; the sentence stops looking contingent and nobody checks it again.

## TDD Operational Discipline (READ BEFORE EVERY NEW SYMBOL)

This section is the non-negotiable interpretation of rule #6. It is written
to be **impossible to skim past or "batch through"**. The user audits the
conversation transcript and verifies that every implementation was
preceded by a visible red state.

### The Mandatory Cycle

For **every** new exported symbol (function, method, type with behaviour,
constructor, HTTP handler, DI provider, etc.) and for **every** behavioural
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
   go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out
   ```
   `scripts/coverage.sh` reports the total **excluding generated files** (the `// Code generated … DO NOT EDIT.` mockgen `*_mock.go` doubles), so the 85% floor is measured over hand-written code only (ADR-0143, mirrors `.golangci.yml` `generated: lax`). The 85% is a floor, not the goal — hot paths and their failure branches must ALL be covered first (Golang rule #8); a package can fail review at 95% if a hot path is untested.

   **Docker for this run: do NOT ask — just check the daemon and go.** The coverage run and the repo-wide no-regressions run (item 2) sweep packages whose tests need testcontainers, so they need a running Docker daemon. **Standing permission is granted for these two runs specifically**: probe the daemon first (e.g. `docker info` / `docker ps`), and if it answers, run them without asking.

   If the daemon is **not** available — not running, socket missing, `docker` absent, or the probe errors — do **not** attempt the run and do **not** silently substitute a narrower one. Say so plainly and give the owner the choice: start the Docker daemon, or explicitly skip the coverage step. Then **report what was actually verified**: a container-free subset (`engine`, `runtime/{calllink,signal,task}`, `service`, `processtest`, `transport/http`) is a partial result and must be labelled as one — never presented as the Verification item passing. ⚠ `scripts/coverage.sh` only **reports**; its exit code proves nothing.

   This carve-out is scoped to Verification items 1–2. It is **not** a general licence to spin containers: ad-hoc containers in individual tests remain forbidden (Golang rule #3), and a subagent that needs Docker still needs it stated explicitly in its brief.
2. `go test ./...` from the repo root passes — no regressions elsewhere.
3. `golangci-lint run ./...` is clean. Use the `cc-skills-golang:golang-lint` skill if configuration is needed.

   **Binary for this run: do NOT ask — probe and go.** Check `golangci-lint` is on `PATH` (e.g. `command -v golangci-lint`) and, if it is, run it without asking. If it is **absent**, do not skip the step silently and do not substitute `go vet` as if it were equivalent (`go vet` is a compile-and-vet check, not the lint gate). Say it is missing and offer the choice: install it — either the agent installs it, or the owner does — or explicitly skip linting for this delivery. Whichever is chosen, **report which one happened**; "lint clean" must never be claimed for a run that did not execute.

   ⚠ **`golangci-lint run ./engine/...` is not `golangci-lint run ./...`.** A package-scoped run is a partial result; label it as such until the repo-wide run has passed.
4. **Before delivery** (merging to `main` or pushing a PR branch): run `/code-review` **and** `/security-review` on the pending change and fix **all** findings — see the **Delivery Gate** under Git Discipline. Review-driven fixes are folded into the feature commit via `--amend`, never stacked as new commits.

## Common Pitfalls

1. Don't ignore pre-existing errors in packages you aren't working on. Never excuse them as "not caused by this session." Queue them as follow-up tasks and address by priority.
2. Stick to skills explicitly listed under "Rule of Thumbs". If a skill outside that list seems applicable, ask before using it.
3. Never import watermill, casbin, or gocron directly from workflow/engine code — go through the in-repo abstraction (the eventing interface, the `Authorizer`, the scheduler port) so vendors stay swappable. The engine *core* additionally must not import `clockwork` at all — enforced by `engine/purity_test.go`.
4. **Judge a test run by its exit code, never by a pipeline.** `go test ./pkg/... > /tmp/out.log 2>&1; echo "EXIT=$?"`, then read the log. A `go test … | grep | head` tail once reported green here while 14 tests were failing — `head` closes the pipe and the failures never render.
5. **`go test -run` on a name that does not exist exits 0** ("no tests to run"). So a name-filtered run can never certify "this test is unreached", and renaming a test silently disarms every filtered invocation that named it. Verify absence on the whole package, and confirm a test *ran* with `-v` rather than inferring it from a green exit.

## Git Discipline

Use Conventional Commits scoped to the area:

- `feat(<scope>): <description>`     — new functionality
- `fix(<scope>): <description>`      — bug fix
- `chore(<scope>): <description>`    — tooling / cleanup
- `refactor(<scope>): <description>` — behavior-preserving restructure
- `docs(<scope>): <description>`     — documentation

### Commit granularity — feature bundles, NO micro-commits

- **Do not micro-commit.** One meaningful, deliverable feature = **one commit**. The
  commit bundles everything that ships the feature: implementation, tests, **and its
  documents (ADR(s), spec, plan)** — so `git log` reads as a sequence of complete,
  self-contained feature bundles, and each ADR lands with the code that realizes it.
- **Fold, don't stack.** Fixes arising from `/code-review` / `/security-review` (or any
  pre-delivery rework) are folded into the feature commit with `git commit --amend` —
  never appended as separate `fix:`/fixup commits. This is safe because the feature
  commit stays local (unpushed, on its feature branch) until the Delivery Gate passes.
  **Never amend a commit that has already been pushed/delivered** — after delivery,
  follow-ups are new feature bundles of their own.
- Updates to an already-committed spec/plan/ADR likewise `--amend` the commit that
  introduced the document (keep history clean; one bundle per feature).

### Delivery Gate (before merge to `main` or pushing a PR)

A feature is deliverable only when **all** of the following pass, in order:

1. The Verification section above (tests + ≥ 85% coverage, no cross-repo regressions, clean lint).
2. **Documents describe what shipped.** Re-read the bundle's ADR(s), spec and plan against the built code and correct every divergence — most importantly any behaviour the ADR *promises* that implementation changed or dropped. Also sweep the diff's own comments for unexecuted claims and over-reaching quantifiers (**Premise Discipline** above); false claims in committed comments have reached this gate repeatedly, and they are cheapest to kill here.
3. `/code-review` on the pending change — **fix all findings** (fold via `--amend`).
4. `/security-review` — **fix all findings** (fold via `--amend`).

Only then merge (`--no-ff`) to `main` / push the PR branch. Findings you adjudicate as
false-positive or out-of-scope must be stated explicitly with the reason — silence is
not an adjudication.

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

- `samber/cc-skills-golang@golang-database` — Postgres 17 / MySQL / SQLite, transactions, hot-path caching, the outbox table.
- `samber/cc-skills-golang@golang-concurrency` — token execution, gocron waiters, background relayers.
- `samber/cc-skills-golang@golang-context` — cancellation/deadline propagation through the engine.
- `samber/cc-skills-golang@golang-structs-interfaces` — the eventing/authz/action/persistence abstractions.
- `samber/cc-skills-golang@golang-observability` — required metrics, traces, and `slog` logging.
- `samber/cc-skills-golang@golang-dependency-injection` — DI decision/concepts.
- `samber/cc-skills-golang@golang-samber-do` — the locked `samber/do` v2 container for internal/example wiring.
