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

CI also runs three repo-specific checks. Each is a plain script needing neither Docker nor a
network, so run them locally before pushing:

```bash
scripts/check-extraction.sh                      # internal/database stays extractable
scripts/check-test-timeout.sh                    # Eventually budgets fit go test -timeout
scripts/check-doc-refs.sh                        # no citations of deleted documents in *.go
```

## Expectations for a change

- **Test-driven.** Production code is written test-first (red → green → refactor). New exported
  symbols and behavioural changes must be preceded by a failing test. See `CLAUDE.md` for the full
  TDD discipline this repo follows.
- **Coverage.** Touched packages should stay at **≥ 85%** line coverage.
- **Lint clean.** `golangci-lint run ./...` must report zero issues.
- **Architecture Decision Records.** Non-trivial design decisions are recorded as ADRs under
  `docs/adr/NNNN-<slug>.md` using the Nygard template.
- **Engine purity.** The engine core (`engine/`, `model/`) must not import transport, storage-vendor,
  or event-bus packages — depend on the in-repo interfaces. Never import watermill, casbin, gocron, or
  clockwork directly from workflow/engine code.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/) scoped to the area, e.g.:

```
feat(action/httpcall): add response size cap
fix(persistence): guard relay loop on context deadline
docs(adr): record retryable-action error contract
```

Commit one logical change at a time.

## Reporting bugs / requesting features

Open a GitHub issue with a minimal reproduction (a failing test is ideal). For **security issues**,
do **not** open a public issue — contact the maintainers privately first.
