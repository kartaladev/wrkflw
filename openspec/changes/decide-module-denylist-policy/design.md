# Design

## Context

See proposal.md (Why, and the re-measurement table). Three mechanisms keep a module out today, and each sees a different part of the graph:

| Guard | What it reads | Match | Covers |
|---|---|---|---|
| `deniedEngineImports` (`engine/purity_test.go:161`) | non-test import statements of `engine/` | substring | layering: transport, persistence, runtime, and vendors (`watermill`, `gocron`, `clockwork`, `casbin`, otel) |
| `TestEventingDependencyGraphIsMessagingVendorFree` (`eventing/vendorfree_test.go:76`) | `go list -deps` of `eventing` | equals or prefix + `/` | watermill, oklog/ulid, lithammer/shortuuid, pkg/errors. Backs the baseline `event-delivery` requirement "Eventing names no third-party messaging library" |
| `TestServiceDependencyGraphIsVendorFree` (`service/vendorfree_test.go:15`) | `go list -deps` of `service` | exact package | DB drivers, `database/sql`, persistence packages |

None of the three reads the **module graph** (`go list -m all`). goavro's earlier regrowth happened there, in a package nothing imported (#20 register, #113 entry). A build-graph check scoped to one package would not have seen it.

Measured mapping: `engine` is in `go list -deps ./eventing`, so the eventing ban already rejects a watermill import from `engine`.

## Goals / Non-Goals

**Goals:**
- One written rule for what happens to a guard entry when its dependency leaves `go.mod`. It is applied to watermill now and to casbin's three modules after `remove-casbin`.
- A deliberate yes or no on a regrowth guard for removed modules. If yes, the guard must be able to go red, and each entry carries its reason.
- Every list says what it covers, so two lists naming the same module can be told apart.

**Non-Goals:**
- Changing which modules wrkflw depends on. Removals belong to the changes that perform them.
- A general "allowed modules" list. `eventing/vendorfree_test.go` already explains why an allow-list over `-deps` can only pass by being too broad to mean anything.
- Guarding modules that were never dependencies.

## Decisions

These are recommendations. All five are open (see Open Questions), and the tasks are written for the recommended options, pending the maintainer's answers.

**D1. Engine-import entries guard only present dependencies (recommended answer to Q1).** When a dependency leaves `go.mod`, the change that removes it also moves its `deniedEngineImports` entry to the banned-module list (D2). For watermill, which is already gone, this change makes the move.
- Rejected: *delete with no replacement* (#125 reading 1). This discards the reintroduction guard that #113 showed is needed.
- Rejected: *keep the entry indefinitely* (#125 reading 2). This is the legibility cost the #131 review measured: a reader cannot tell whether an entry guards a live dependency or an absent one.
- Evidence that settles watermill: `engine` ∈ `go list -deps ./eventing` (measured), so removing `"watermill"` from the engine list leaves no gap in watermill's module path.

**D2. One general banned-module list, as a Go test over the workspace module graph (recommended answer to Q2).** A root-module test runs `go list -m all` in workspace mode, which covers both `go.work` modules, and fails on any module that equals a banned path or lies under it. Each entry is a struct holding the module path, the issue or change that removed it, and a one-line reason. That answers #124's requirement that a list "needs somewhere for the rationale of each entry to live".
- Rejected: *no guard* (#124's counter-argument). The regrowth has already happened once, and the guard costs one test with no new CI job.
- Rejected: *`scripts/check-no-goavro.sh` in its own CI job*. It covers one module, it is a fourth permanent job (the tax #124 names), and it needs the wiring meta-guards that `harden-ci-guard-wiring` is building.
- Rejected: *build graph only (`go list -deps` over `scripts/modules.sh` patterns)*. It misses a required-but-unimported module, which is the exact #113 shape.
- Non-vacuity: the test fails if `go list -m all` returns fewer than two `github.com/kartaladev/wrkflw` modules. A test that sees nothing must not pass. A planted-entry control proves the matcher can go red: a test-local list naming a module that is present, such as `github.com/expr-lang/expr`, must report it.

**D3. The eventing ban stays, and each list names its scope (recommended answer to Q3).** `eventing/vendorfree_test.go` verifies a baseline spec requirement, so it is not deleted. The new list's doc comment says it covers the whole workspace module graph and that the eventing test is the spec-backed, eventing-scoped check. The eventing test gets a matching comment. watermill appears in both, by design, with different stated scopes.

**D4. Ban by evidence of sole carriage (recommended answer to Q4).** A module goes on the list when the removed dependency was its only reason to be in the graph. The measurement is `go mod why -m` before removal:
- `github.com/linkedin/goavro` (#113): the only module asked for in #124.
- `github.com/ThreeDotsLabs/watermill` (#45 / #131).
- `github.com/casbin/casbin`, `github.com/casbin/govaluate`, `github.com/bmatcuk/doublestar`, after `remove-casbin`. `go mod why -m` reaches govaluate and doublestar only through casbin (measured).
- **Not** `golang/snappy`, `oklog/ulid`, `lithammer/shortuuid` or `pkg/errors` in the module-graph list. These are general-purpose libraries that an unrelated future dependency could legitimately require. A module-graph ban on them would fail a legitimate upgrade. The last three stay banned from eventing's build graph, where the baseline spec puts them.

**D5. No spec delta now (recommended answer to Q5).** See proposal.md, Capabilities.

## Cross-change coupling

- `remove-casbin` (landing order: before this change). Its tasks 1.5 and 1.7 keep `"casbin"` in `deniedEngineImports` and in `CONTRIBUTING.md:123`. Under D1 this change then moves the entry and strikes casbin from the sentence. No file is edited by both changes at once.
- `split-persistence-modules` adds modules to `go.work`. With `go list -m all` in workspace mode, the D2 test covers new modules automatically, and its two-module floor still holds. It also creates `module-dependencies`, the future home if Q5 is answered "promote".
- `cut-v0-release` adds a post-tag `GOWORK=off` job. Outside workspace mode, `go list -m all` sees only the root module. That is still correct for the root (the consumer's view) but narrower. The test must not misread a non-workspace run as vacuous: it skips the floor when `go env GOWORK` is `off` and says so in its log line.
- `harden-ci-guard-wiring` is coupled only if Q2 picks a script.

## Risks / Trade-offs

- [A future dependency legitimately requires a banned module] → D4 limits the module-graph list to modules whose only reason to exist was the removed feature. A conflict therefore means the feature is returning, which is the moment the list should force a decision. The failure message names the entry's reason and its removal issue.
- [`go list -m all` is slower than `-deps` and needs the module cache] → It runs once per `go test ./...` in the root module, as the existing `go list` tests already do.
- [Two lists still name watermill] → Accepted under D3. Each list states its scope. Q3 records the alternative of dropping watermill from the new list.

## Open Questions

Readiness is human until these are answered. No thread records an outcome for any of them. The #125 thread decides only that watermill and casbin are handled separately.

1. **Q1, an engine-import entry for an absent module.**
   - (a) Delete it.
   - (b) Keep it as a reintroduction guard.
   - (c) Move it to a module-graph ban.
   - **Recommendation: (c)**, as D1.
2. **Q2, a regrowth guard.**
   - (a) None.
   - (b) A goavro-specific `scripts/check-no-goavro.sh` in its own job.
   - (c) A general list as a Go test over the module graph.
   - (d) A general list over the build graph.
   - **Recommendation: (c)**, as D2.
3. **Q3, the new list and the eventing ban.**
   - (a) Both keep watermill, with stated scopes.
   - (b) The new list omits modules already covered by a spec-backed ban.
   - (c) The eventing test reads the shared list. This is rejected for the recommendation because the spec names its four modules explicitly.
   - **Recommendation: (a)**, as D3.
4. **Q4, entries.** Should casbin, govaluate and doublestar go on the list after `remove-casbin`, and should snappy, which left with goavro?
   - **Recommendation:** the first three yes, snappy no, as D4.
5. **Q5, spec or guard.**
   - (a) A test-only guard now, with `skip_specs` kept.
   - (b) After `split-persistence-modules` archives, a later change adds a `module-dependencies` requirement: no wrkflw module requires a banned module.
   - **Recommendation: (a)** now. (b) remains available and is out of scope here.
