# Contributing to wrkflw

Thanks for your interest in contributing. `wrkflw` is a **library-first** Go workflow engine: the
deliverable is the importable module-root API that consumers embed. Please keep that lens in mind —
when a change trades library ergonomics for server convenience, library ergonomics win.

## Prerequisites

- **Go 1.26.8** (the version `go.mod` declares; CI installs it with `go-version-file: go.mod`).
- A running **Docker daemon** — integration tests use [testcontainers-go](https://golang.testcontainers.org/)
  to provision real PostgreSQL / MySQL / MinIO / mailpit. They are not mocked.
- [`golangci-lint`](https://golangci-lint.run/) v2 (the config uses the v2 schema).
- **A 64-bit target.** wrkflw is built, tested and linted on 64-bit platforms only. 32-bit builds
  (`GOOS=linux GOARCH=386`, `GOOS=linux GOARCH=arm` — the `GOOS` is required, or a macOS host reports
  a PIE/cgo linker error instead of the real result) are not run in CI and are not a supported
  configuration: a build or vet failure there is not a defect. The tree happens to vet clean on both
  today, but nothing keeps that true. This disclaims **CI coverage**, not the in-code invariants:
  `maxSchedulableInterval` is still chosen so its products stay inside a 32-bit `int`, and that bound
  must be re-derived if the constant is ever raised.

## Local workflow

```bash
go build ./...                                   # build everything
go test -race ./...                              # full suite (needs Docker)
go test ./<package>/...                          # one package, e.g. ./engine/...
scripts/lint.sh ./...                            # lint — must be clean before a PR
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
```

CI also runs three repo-specific checks. None needs Docker, so run them locally before pushing:

```bash
scripts/check-extraction.sh                      # internal/database stays extractable
scripts/check-test-timeout.sh                    # test wait budgets fit go test -timeout
scripts/check-doc-refs.sh                        # no citations of deleted documents in *.go
```

The first needs the Go toolchain (`go list -deps`, which may hit the network on a cold module
cache). The other two are pure bash + git + grep.

`scripts/lint.sh` is a local wrapper, not a fourth CI check. It runs `golangci-lint` under a
`GOLANGCI_LINT_CACHE` derived from this worktree's root, and fails if a finding is attributed to a
path outside the directory it was run from. golangci-lint's cache is keyed by content and shared by
every checkout on the machine, so without it a second worktree holding a byte-identical file is
handed the first worktree's cache entry and prints the first worktree's path — a phantom finding in
one direction, and, more expensively, a real finding here waved off as another branch's noise in the
other. CI is unaffected and stays on `golangci-lint-action`: each job is a fresh runner with a single
checkout, so it has no sibling worktrees to inherit from. Pass any `golangci-lint run` arguments
straight through. `scripts/lint.sh --self-test` runs on every invocation anyway; it checks that the
detector reports exactly the paths that do not belong to this checkout, and — by running the script
end to end against a stub `golangci-lint` — that golangci-lint's exit status is passed through, that a
misattributed path exits 9, and that the per-worktree cache reaches the tool. The shared-cache
reproduction it also attempts is best-effort: it warns and lets the lint proceed when it cannot reach
a verdict, because a self-test that refuses to lint is worse than the defect it guards.

## Expectations for a change

- **Test-driven.** Production code is written test-first (red → green → refactor). New exported
  symbols and behavioural changes must be preceded by a failing test. See `CLAUDE.md` for the full
  TDD discipline this repo follows.
- **Coverage.** Touched packages should stay at **≥ 85%** line coverage.
- **Lint clean.** `scripts/lint.sh ./...` must report zero issues.
- **Design decisions.** Record the rationale in the commit message and the PR body, and state the
  constraint it produced as a comment on the code it constrains — naming an identifier a reader can
  jump to (`ErrScopeLocalWithCompensateRef`), never a document. This repo keeps no ADR directory;
  `scripts/check-doc-refs.sh` fails the build on any `*.go` citation of the deleted one. Commit
  messages are deliberately outside its scope, so quoting history there is fine.
- **Engine purity.** The engine core (`engine/`, `model/`) must not import transport, storage-vendor,
  or event-bus packages — depend on the in-repo interfaces. Never import casbin, gocron, or clockwork
  directly from workflow/engine code. There is no event-bus vendor left to name: a broker is reached
  through `eventing.PublishFunc` and `eventing.Handler`, which a consumer implements over their own
  client.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/) scoped to the area, e.g.:

```
feat(action/httpcall): add response size cap
fix(persistence): guard relay loop on context deadline
docs(agents): record the Eventually wait rule
```

Commit one logical change at a time.

## Reporting bugs / requesting features

Open a GitHub issue with a minimal reproduction (a failing test is ideal). For **security issues**,
do **not** open a public issue — contact the maintainers privately first.
