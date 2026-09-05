# Contributing to wrkflw

Thanks for your interest in contributing. `wrkflw` is a **library-first** Go workflow engine: the
deliverable is the importable module-root API that consumers embed. Please keep that lens in mind —
when a change trades library ergonomics for server convenience, library ergonomics win.

## Prerequisites

- **Go 1.25** (the repo pins `go 1.25.x` in `go.mod`).
- A running **Docker daemon** — integration tests use [testcontainers-go](https://golang.testcontainers.org/)
  to provision real PostgreSQL / MySQL / MinIO / mailpit. They are not mocked.
- [`golangci-lint`](https://golangci-lint.run/) v2 (the config uses the v2 schema).

## Local workflow

```bash
go build ./...                                   # build everything
go test -race ./...                              # full suite (needs Docker)
go test ./<package>/...                          # one package, e.g. ./engine/...
golangci-lint run ./...                          # lint — must be clean before a PR
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
```

CI also runs three repo-specific checks. None needs Docker, so run them locally before pushing:

```bash
scripts/check-extraction.sh                      # internal/database stays extractable
scripts/check-test-timeout.sh                    # Eventually budgets fit go test -timeout
scripts/check-doc-refs.sh                        # no citations of deleted documents in *.go
```

The first needs the Go toolchain (`go list -deps`, which may hit the network on a cold module
cache). The other two are pure bash + git + grep.

## Expectations for a change

- **Test-driven.** Production code is written test-first (red → green → refactor). New exported
  symbols and behavioural changes must be preceded by a failing test. See `CLAUDE.md` for the full
  TDD discipline this repo follows.
- **Coverage.** Touched packages should stay at **≥ 85%** line coverage.
- **Lint clean.** `golangci-lint run ./...` must report zero issues.
- **Design decisions.** Record the rationale in the commit message and the PR body, and state the
  constraint it produced as a comment on the code it constrains — naming an identifier a reader can
  jump to (`ErrScopeLocalWithCompensateRef`), never a document. This repo keeps no ADR directory;
  `scripts/check-doc-refs.sh` fails the build on any `*.go` citation of the deleted one. Commit
  messages are deliberately outside its scope, so quoting history there is fine.
- **Engine purity.** The engine core (`engine/`, `model/`) must not import transport, storage-vendor,
  or event-bus packages — depend on the in-repo interfaces. Never import watermill, casbin, gocron, or
  clockwork directly from workflow/engine code.

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
