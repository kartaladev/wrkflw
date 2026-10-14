# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`. Verification runs in the root module: `GOTOOLCHAIN=go1.26.9 go test -race ./transport/http/...` unless a narrower package is named. Groups 3 to 6 follow the migration order of design D9: one adapter at a time, never all three at once. Tasks tagged with OQ1 to OQ3 wait for that ruling and implement the option it picks; if the ruling differs from the recommendation, update the pending requirement first.

## 1. Failing tests for the defects and divergences

- [ ] 1.1 In `transport/http/parity`, add a table test over stdlib, gin and fiber with an instance mapper whose `MarshalJSON` fails. Assert 500, the body `{"error":"internal_error"}`, no error text in the body, and one ERROR log record. Watch it go red: today stdlib and gin answer 201 with an empty body, and fiber answers 500 `text/plain` with the error text (scenario *Marshal failure on every adapter*; OQ1). Verify: `go test -run TestParity_MarshalFailure ./transport/http/parity` fails on all three rows.
- [ ] 1.2 Add raw-byte parity rows (status, raw body and `Content-Type`) for the requests in scenario *Byte parity across adapters*. Watch the stdlib newline and the gin/fiber charset rows go red.
- [ ] 1.3 Turn the trailing-bytes row of `TestParity_MaxBodyBytes_UnderCap` into the outline of scenario *Trailing content after the value*, with every row expecting the specified status. Watch stdlib and gin go red on the trailing rows.
- [ ] 1.4 Add scenario *Malformed bodies* as a byte-parity test, and watch fiber's "bind from body" prefix go red.
- [ ] 1.5 Rewrite `TestParity_WithMiddleware_ScopeAndMultiplicity` to scenarios *Exactly one execution per request on every route group*, *gin leaves the consumer's own routes alone*, *Consumer routes and unrouted paths are left alone* and *Concurrent requests each run the middleware once*, with stdlib rows. Watch fiber's consumer-route and unrouted rows go red; stdlib rows fail to compile until 3.4.
- [ ] 1.6 Add scenarios *limit and total values*, *Every listing route parses limit the same way* and the status negative control (OQ2), and watch the malformed-value rows go red.
- [ ] 1.7 Add scenarios *Optional bodies on the incident-resolve route* and *The optional claim body* (OQ3), and watch the malformed rows go red.

## 2. The httpcore route table, binding and serve path

- [ ] 2.1 Add `Route`, `BodyMode`, the six-method `Binding`, and `Serve` to `httpcore` (design D1). Cover `Serve` with unit tests against a fake binding: identity before `ReadBody` (D5), disclosure, instrumentation and error classification. Verify: `go test ./transport/http/httpcore`.
- [ ] 2.2 Implement the single encoder (D2, D3). Encode into a buffer, write `application/json` with no newline, and handle a marshal failure per OQ1. A write failure after commit is logged once. Unit tests cover both cases; 1.1's rows pass once an adapter uses `Serve`.
- [ ] 2.3 Implement the single decoder (D4): one value, then EOF; `BodyOptional` per OQ3; one error wrap. Unit-test every row of *Trailing content after the value*, *Malformed bodies* and the optional-body scenarios against a fake binding.
- [ ] 2.4 Implement query parsing for `limit`, `cursor`, `status` and `total` per OQ2. Unit-test every row of *limit and total values*.
- [ ] 2.5 Build the route groups as `[]Route` with closures over their dependencies: `InstanceRoutes(svc)`, `TaskRoutes(svc)`, `MessageRoutes(svc)`, `AdminRoutes(deps)` with optional routes absent while their dependency is nil, and `HealthRoutes(checks)`. Fix the route count in the `headers.go` comment, measured from the table. Verify: a table test asserts the method and path set.
- [ ] 2.6 Resolve OQ4: remove `CustomizeConfig.Wrap` and `WithRouterFunc`, or document their contract. Update `seam.go` godoc either way. Verify: `go vet ./...` and `go build ./...` in the root and `examples` modules.

## 3. stdlib onto the table

- [ ] 3.1 Implement the stdlib binding and reduce `stdlib/groups.go`, `write.go` and `body.go` to it plus thin wrappers that keep every exported signature. Verify: the stdlib suite passes unchanged except rows tied to a removed divergence, and `transport/http/parity` passes against hand-written gin and fiber, with stdlib rows of 1.1 to 1.4, 1.6 and 1.7 green.
- [ ] 3.2 Add `stdlib.WithMiddleware(...func(http.Handler) http.Handler)`, applied per route ahead of identity (D7). Update the package doc that says stdlib has no `WithMiddleware`. Verify: the stdlib rows of 1.5 and of scenarios *An actor set by middleware is the route's actor*, *A short-circuiting middleware stops the route* and *Negative control - without the middleware the same claim is refused*.
- [ ] 3.3 Run `goleak` on the stdlib body read-deadline path. Verify: `go test -run Deadline ./transport/http/stdlib`.

## 4. gin onto the table

- [ ] 4.1 Implement the gin binding and reduce `gin/groups.go`, `write.go` and `bodycap.go` to it plus wrappers. No `ShouldBindJSON` remains. Verify: the gin suite passes, with assertions changed only where a divergence is removed; parity passes against stdlib (table) and fiber (hand-written); and the gin rows of 1.1 to 1.7 and of the middleware-order scenarios are green.
- [ ] 4.2 Apply gin `WithMiddleware` per route instead of through `Group` (D7). Verify: *gin leaves the consumer's own routes alone* and the gin concurrency row.
- [ ] 4.3 Run `goleak` on the gin body read-deadline path. Verify: `go test -run Deadline ./transport/http/gin`.

## 5. fiber (v3) onto the table

- [ ] 5.1 Implement the fiber binding and reduce `fiber/groups.go`, `write.go` and `bodylimit.go` to it plus wrappers. No `Bind().JSON` and no `c.JSON` remain. Verify: the fiber suite passes; *The 413 body is the fixed envelope on every adapter* and `TestFiberDivergence_AboveFiberConfigBodyLimit` stay green; and every fiber row of groups 1 and 3 is green.
- [ ] 5.2 Apply fiber `WithMiddleware` per route, and delete the memo and its map from `fiber/options.go` (#40 coordinator note). Update its godoc: route-scoped, with `app.Use` for app-wide middleware. Verify: the fiber rows of 1.5, including consumer route 0 and unrouted path 0.
- [ ] 5.3 Add `fiber.WithBodyReadTimeout`, documented as inert, with one WARN at mount (D8). Verify: scenarios *The option is accepted and reported* and *Negative control - the option is absent*.

## 6. Parity package as one table

- [ ] 6.1 Replace the hand-written per-route parity rows with one table-driven test that drives every `Route` through every binding and compares status, headers the library sets and raw bytes. Keep only the two fasthttp divergence tests. Delete the `noBodyParity` row, coordinating with `improve-wire-gate-diagnostics` (design, Cross-change coupling). Verify: `go test ./transport/http/parity`.
- [ ] 6.2 Add a benchmark of `POST /instances` per adapter. Record allocs/op before (on main) and after in the PR body, and confirm no adapter regresses by more than one allocation per request. Verify: `go test -bench PostInstances -benchmem -run ^$ ./transport/http/parity`.

## 7. Integration

- [ ] 7.1 Run the whole suite per module as `CONTRIBUTING.md` describes, with `-race`, including `examples`. Also run `scripts/check-doc-refs.sh` and `golangci-lint run`. Verify: all exit 0.
- [ ] 7.2 Re-read every godoc this change touched (`httpcore/seam.go`, `headers.go`, the stdlib package doc, `WithMiddleware` on every adapter, and `write.go` if it survives) against the shipped behavior. Verify: no comment still describes a removed divergence (`grep -rn "RECORDED DIVERGENCE\|no WithMiddleware\|silently swallowed" transport/http` returns nothing).

## Workflow follow-up

- The PR names `unify-http-routes-as-data`, and its last commit runs `openspec archive unify-http-routes-as-data`.
- After merge, comment on and close kartaladev/wrkflw#205 if the planning PR has not already converted it. Comment on kartaladev/wrkflw#40 that part 1 has landed; `add-fiber-v2-adapter` closes it.
