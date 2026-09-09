---
name: use-mockgen
description: Generate, regenerate, and place uber-go/mock (`mockgen`) test doubles for Go interfaces in this repository. Use whenever a test needs to mock an interface, when a mocked interface is added, modified, or removed, or when a new dependency must be substituted in tests. Covers `//go:generate` directives, the `--typed` flag, where mocks should live (alongside the interface, in the producer package), and the source-vs-reflect mode decision. Overrides the mock-generation steps in `cc-skills-golang:golang-testing` and `cc-skills-golang:golang-stretchr-testify`; when the two conflict, prefer this one.
---

# Generating Go Mocks with `mockgen`

This repo standardizes on [`uber-go/mock`](https://github.com/uber-go/mock) (the maintained fork of `golang/mock`). All interface test doubles for our own code are generated, never hand-written.

When this skill conflicts with `cc-skills-golang:golang-testing` or `cc-skills-golang:golang-stretchr-testify` (which lean toward `testify/mock`), this skill wins for interfaces *we* own. `testify/mock` is fine for ad-hoc doubles inside a single test file, but anything reused across tests must be generated.

## Prerequisite

`mockgen` v0.6.0 or later must be on `$PATH`. v0.6.0 is the floor because earlier versions emit subtly different code for typed mocks (`--typed`), and our generated files assume the v0.6.x shape — mixing versions across the workspace causes spurious diffs every time someone regenerates.

```shell
mockgen --version
```

Install or upgrade with:

```shell
go install go.uber.org/mock/mockgen@latest
```

## Where mocks live

**The mock's consumers decide.** Answer one question before generating: whose
tests will import this mock?

| The consumers are… | Generate the mock… |
|---|---|
| the owning package's own black-box tests | in the same package, beside the interface |
| another package's tests | in a dedicated test-double package |

### Consumers inside the package → same package

The default, and it earns its keep here: our tests are black-box
(`package <name>_test`), so they already import the producer by its real path
and pick the mock up from an import they have.

```
<pkg>/
├── repository.go        // declares the interface
└── repository_mock.go   // generated mock, package <pkg>
```

- The interface and its mock evolve together; co-located files surface drift in code review.
- No second import for what is conceptually one symbol.
- The rename refactor stays trivial: move the interface, the mock moves with it.

### Consumers outside the package → a test-double package

As soon as every consumer is a different package, co-location buys nothing —
each consumer adds an import either way — while the exported `Mock*` surface
costs something real: it ships to consumers of the **production** package, who
never asked for test doubles.

`service/servicetest` is the worked example: the `service` admin ports are
mocked into a sibling test-double package because every consumer of those mocks
is a different package's tests. `service/servicetest/doc.go` carries the
reasoning at the source.

```
service/
├── opsadmin.go              // declares the interfaces + the //go:generate directive
└── servicetest/
    └── opsadmin_mock.go     // generated mock, package servicetest
```

`service/no_test_doubles_test.go` enforces the result: an exported `Mock*` type
in `service`, or a `//go:generate` directive whose destination lands back in
`service`, fails `go test ./service/`.

**When you cannot answer the question yet**, generate in the same package. Moving
a mock out later is a package rename; pulling an exported `Mock*` back out of a
published API is not.

## Invocation: prefer `//go:generate` directives

The default way to generate is a `//go:generate` directive at the top of the file declaring the interface, so the whole workspace can be regenerated with one command.

```go
// Package contract holds the repository contracts.
//
//go:generate mockgen -source=repository.go -package=contract -destination=repository_mock.go -typed
package contract

type Repository interface { /* ... */ }
```

Then regenerate with:

```shell
go generate ./...        # whole module
go generate ./service/...             # one package
```

Use a direct CLI invocation only when you do not own the source file (e.g., generating a mock for a third-party interface), or when scripting a one-off.

```shell
mockgen --source service/lineage.go \
        --package servicetest \
        --destination service/servicetest/lineage_mock.go \
        --typed
```

## Required flags

- `--typed` — always. Typed mocks give compile-time-checked expectations (`mockRepo.EXPECT().Find(ctx, id).Return(...)`) instead of `interface{}`-based ones; mistakes show up at `go build` time rather than at test runtime.
- `--source` — preferred over reflect mode (positional package + interface name) for interfaces in this repo. Source mode is hermetic (does not require the target package to build first), produces stable output across Go versions, and matches what the `//go:generate` directives use. Reserve reflect mode for cases where you cannot point `mockgen` at the source file — typically third-party interfaces in vendor or module cache.
- `--mock_names` — required only when a single source file declares multiple interfaces whose default mock names (`Mock<Name>`) would collide with something already in the destination package, or when you want a clearer naming scheme. Example: `--mock_names=Repository=MockUserRepo,Cache=MockUserCache`.

## Rules

- Generate a mock for an interface the first time a test needs it; do not hand-roll one.
- Regenerate whenever the source interface changes. Treat a stale mock as a test failure — never edit the generated file by hand to "fix" the diff.
- Delete the mock file when the interface is removed. A leftover mock is a silent maintenance trap.
- Keep the `//go:generate` directive in sync with the destination path if you move the file.

## Verification

After generating or regenerating, confirm all of the following before considering the task done:

1. `go build ./...` from the module root compiles — catches missing imports in the freshly generated file.
2. `go generate ./... && git status` shows no further diff — proves the committed mock matches what the directive produces.
3. Every interface declared in the source file has a corresponding `Mock<Name>` (or its `--mock_names` alias) in the generated file.
4. No mock files exist for interfaces that no longer appear in the source — search for orphans with `grep -l 'MockGen' **/*_mock.go` and reconcile.
5. Tests that consume the mock compile and pass under `go test -race ./...`.