# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`. Verification runs per module as `CONTRIBUTING.md` describes, with `GOTOOLCHAIN=go1.26.9 go test -race`. This change starts only after `unify-http-routes-as-data` and `split-persistence-modules` are archived.

## 1. Module

- [ ] 1.1 Create `transport/http/fiberv2/go.mod` requiring `github.com/gofiber/fiber/v2 v2.52.15` and root at a pseudo-version of a pushed commit (no `replace`), add it to `go.work`, and raise `MIN_MODULES` in `scripts/modules.sh`. Note the consumer go.mod impact in the PR body. Verify: `go build ./...` in the new module, and `scripts/check-module-drift.sh` exits 0.
- [ ] 1.2 Add fiberv2 rows to the module-dependency guard that `split-persistence-modules` introduces: fiberv2 carries no gin, no Fiber v3 and no quic-go, and no other module carries Fiber v2. Verify: the guard test passes.

## 2. Fiber v2 binding

- [ ] 2.1 Write the failing test first. Middleware that sets an actor through `authz.ContextWithActor` and `SetUserContext` must reach the claim route, modelled on the v3 `TestTaskRoutes_ActorComesFromMiddlewareNotTheBody`. Then implement `transport/http/fiberv2` over `*fiber.Ctx` with `UserContext`/`SetUserContext` only, `c.Body()` bytes and `Group(prefix, ...Handler)` (design D1, D2). Verify: scenarios *Actor from middleware reaches the task route* and *Negative control - no middleware, no actor*.
- [ ] 2.2 Add a guard test that fails on any `.Context()` call in the package's non-test files (D1). Watch it go red against a deliberately planted call, then remove the call. Verify: `go test ./transport/http/fiberv2` in its module.

## 3. Parity

- [ ] 3.1 Add fiberv2 to the parity table, wherever `split-persistence-modules` placed `transport/http/parity`, plus its `BodyLimit` divergence test. Verify: scenarios *Parity with stdlib*, *The fasthttp BodyLimit divergence applies to fiberv2* and *Negative control - below the BodyLimit the cap answers 413*.

## 4. Integration

- [ ] 4.1 Run the whole suite per module with `-race`, including `examples`, plus `scripts/check-doc-refs.sh` and `golangci-lint run` in the new module. Verify: all exit 0.
- [ ] 4.2 Write the package doc: route-scoped `WithMiddleware`, `app.Use` for app-wide middleware, the `BodyLimit` divergence, and why the package never calls `.Context()`. Verify: re-read against shipped behavior.

## Workflow follow-up

- The PR names `add-fiber-v2-adapter`, and its last commit runs `openspec archive add-fiber-v2-adapter`.
- After merge, comment on and close kartaladev/wrkflw#40 if `unify-http-routes-as-data` has not already closed it.
