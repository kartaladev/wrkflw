# Tasks

Every Go task starts with the `cc-skills-golang:golang-how-to` skill, then runs red, green, refactor. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. Every command sets `GOTOOLCHAIN=go1.26.9`. Store rows (Postgres, MySQL, SQLite) need Docker. A whole-workspace run uses the `scripts/modules.sh` loop in CONTRIBUTING.md, because a bare `./...` covers only the root module.

## 1. Remove casbin (kartaladev/wrkflw#115 115b, #179; D1, D2). Holds the go.mod slot

- [ ] 1.1 Write the failing parity test for *The library mounts no policy administration routes* on stdlib, gin and fiber, including the negative-control rows. It goes red because `/admin/policies` answers 200 today. Verify with `go test -run TestAdminRoutes ./transport/http/...`, red before 1.2.
- [ ] 1.2 Delete `service/policyadmin.go`, `service/servicetest/policyadmin_mock.go`, the `Policies` field and its six routes in `transport/http/{stdlib,gin,fiber}/groups.go`, and the policy endpoints and DTOs in `httpcore/admin_endpoints.go`, `dto.go` and their tests. Make 1.1 green. Verify with `go test -race ./service/... ./transport/...`.
- [ ] 1.3 Delete `casbinauthz/`, `internal/authz/casbin/` and `runtime/casbin_privilege_e2e_test.go`. Rewrite `examples/scenarios/attribute_authz` on `authz.NewComposite()` with a translator that flattens privileges. Verify with `go build ./... && (cd examples && go build ./...)`.
- [ ] 1.4 Remove casbin from the at-rest guards: the `internal/authz/casbin/migrations` entry in `internal/atrest/discover.go` and the casbin prose in `schema.go`. Re-derive the counts (`discover_test.go:27-31` four → three directories; `:191` 87 → 79; `schema_test.go:452` and its 29-of-87 assertions, re-measured). Delete the `_CasbinRule` legs and exemption rows in `internal/persistence/store/atrest_crosscheck_test.go`. Verify with `go test -race ./internal/atrest/... ./internal/persistence/store/...` (Docker).
- [ ] 1.5 Remove the `casbinauthz/casbinauthz.go:DBOption` entry from `knownOpenInternalLeaks` in `runtime/monitor/internal_leak_test.go:272` in the same commit (the guard self-cleans). Keep `"casbin"` in `engine/purity_test.go`'s denylist. Verify with `go test -run 'TestInternalLeak|TestCorePurity' ./runtime/monitor/... ./engine/...`.
- [ ] 1.6 Run `go mod tidy` in the root and `examples` modules. Verify that `go mod graph | grep -E 'casbin|govaluate|doublestar'` prints nothing in both, that `go mod why -m github.com/jackc/pgx/v5 github.com/pressly/goose/v3` still resolves, and that `scripts/check-module-drift.sh` passes.
- [ ] 1.7 Remove casbin from the docs: the `doc.go` `casbinauthz` bullet (`:188-189`) and any casbin mention in `# Authorization` and `# Trust boundary`; the `authz` package doc; `runtime/shutdown.go:24,66`; `httpcore/errors.go:90`; `scripts/testdb.sh:64`; `docs/agents/eventually-waits.md:94`; and `CONTRIBUTING.md:123`, where it stays only as a denylisted vendor. Verify that `grep -rn -i casbin --include='*.go' --include='*.md' --include='*.sh' . | grep -v '^./openspec/'` prints only the purity denylist and CONTRIBUTING's denylist line, and that `scripts/check-doc-refs.sh` passes.

## 2. Integration

- [ ] 2.1 Run the full workspace suite with the `scripts/modules.sh` loop (Docker), `scripts/lint.sh`, `scripts/check-module-drift.sh`, `scripts/check-doc-refs.sh` and `scripts/check-test-timeout.sh`. Verify that all exit 0.
- [ ] 2.2 Add the release-note lines from design's Migration Plan to the `cut-v0-release` change's notes input, or record them in the PR body for it. Verify the PR body names `remove-casbin`.

## Workflow follow-up

- The PR's last commit runs `openspec archive remove-casbin`.
- After merge, comment on and close kartaladev/wrkflw#179, naming this change. Comment on kartaladev/wrkflw#115 that its casbin part landed here; it closes with `replace-casbin-with-composed-deciders`.
