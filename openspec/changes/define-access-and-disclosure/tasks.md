# Tasks

Apply waits for the maintainer's rulings on Q1, Q4 and Q5 (design, Open Questions), and group 1 waits for `authorize-task-triggers-in-driver` to merge. Groups 2 and 3.1 do not depend on any open question and can start first. Every Go task starts with `cc-skills-golang:golang-how-to`. Run commands per module, as CONTRIBUTING.md describes.

## 1. Default authorizer of an unconfigured engine (#176, pending Q1)

- [ ] 1.1 Write the failing test for "An engine with no configured authorizer enforces task eligibility", scenario "Unconfigured engines refuse an actor the eligibility does not admit". It covers all four wiring rows, in-memory and durable; drop the `service.WithHumanTasks(store, nil)` row if `authorize-task-triggers-in-driver` removed that parameter (design D1). Watch it go red: once #106 has merged, "bob" is refused with `runtime.ErrNoAuthorizer`, not `authz.ErrNotAuthorized` (before it, he claims with `err=nil`). Verify with `GOTOOLCHAIN=go1.26.9 go test ./service/ -run TestUnconfiguredAuthorizer`.
- [ ] 1.2 Arm the driver the facade builds with `authz.NewComposite()` when no authorizer is configured, and raise the construction-summary authorizer log from DEBUG to INFO. Verify that 1.1 goes green, and that the scenarios "Negative control, the default still admits eligible actors and empty specs", "AllowAll is the explicit opt-out" and "A predicate the default cannot evaluate fails closed on the facade" pass with `GOTOOLCHAIN=go1.26.9 go test ./service/ ./authz/`.
- [ ] 1.3 Fix every test and example that relied on the permissive default, by passing `authz.AllowAll{}` explicitly or an eligible actor. Verify with `GOTOOLCHAIN=go1.26.9 go test ./...` in each module, and `go build ./examples/...`.
- [ ] 1.4 Update the `NewProcessEngine` godoc, the `service.WithHumanTasks` godoc (if the parameter survives) and the `doc.go` `# Trust boundary` section to name the default and the opt-out. Check consistency with `authorize-task-triggers-in-driver` D4 (design D1). Verify with `scripts/check-doc-refs.sh` and `go vet ./...`.

## 2. Privileges never render (#199, decided by #114 item 5)

- [ ] 2.1 Write the failing tests for "No instance route renders an actor's granted privileges":
  - "Privileges stay withheld under every category", in `runtime/view`;
  - "Identified callers and DiscloseAll do not receive privileges either", on stdlib, gin and fiber.
  Watch them go red on the `{actors}`, identified and `{all}` rows. Verify with `GOTOOLCHAIN=go1.26.9 go test ./runtime/view/ ./transport/http/...`.
- [ ] 2.2 Strip `Privileges` from every rendered actor (candidates, claim, completion, the actionable view), on the projected and the passthrough paths alike, on a clone. Verify that 2.1 goes green, and that the scenarios "Negative control, actor identity still crosses under actors" and "The strip leaves the source state intact" pass. Update the `runtime/view/classification_test.go` rows so `Privileges` is classified as never rendered.
- [ ] 2.3 Rewrite the `authz.DiscloseActors` godoc warning so it states that privileges never cross. Verify with `go vet ./authz/` and `scripts/check-doc-refs.sh`.

## 3. Default ID generator and UUIDv7 opacity (#128)

- [ ] 3.1 Add the test for "The default generator is idgen.XID", covering scenarios "Unconfigured entry points mint xid-shaped IDs" (all four rows) and "Negative control, a configured generator still wins". It is expected green, because the requirement is the #21 call 7 decision. Verify with `GOTOOLCHAIN=go1.26.9 go test ./service/ ./runtime/ -run DefaultIDGenerator`.
- [ ] 3.2 (Pending Q5.) Add the tests for "UUIDv7 identifiers do not reveal their neighbours": scenarios "Same-millisecond IDs differ in their random bits" and "Negative control, xid neighbours are derivable by design". Document `idgen.UUIDv7` as the opacity option in the `idgen` and `doc.go` godoc. Verify with `GOTOOLCHAIN=go1.26.9 go test ./runtime/idgen/`.

## 4. Cache directive (#127, pending Q4)

- [ ] 4.1 Write the failing adapter tests for "Identity-varying responses are marked uncacheable", scenarios "Every disclosing route marks its response" and "Error and unidentified responses are marked too". Watch them go red: there is no `Cache-Control` in `transport/` on main. Verify with `GOTOOLCHAIN=go1.26.9 go test ./transport/http/...`.
- [ ] 4.2 Set `Cache-Control: private, no-store` on the eleven disclosing routes of each adapter (design D3). Verify that 4.1 goes green and "Negative control, the directive does not change the disclosure decision" passes. Document the directive in `httpcore/headers.go` and the `doc.go` `# Trust boundary` section.

## 5. Integration

- [ ] 5.1 Run the full suite per module, with Docker for the dialect rows, and `golangci-lint run`. Verify that both exit 0.
- [ ] 5.2 Run `openspec validate define-access-and-disclosure --strict` and `scripts/check-openspec.sh`. Verify that both pass.

## Workflow follow-up

- The PR's last commit runs `openspec archive define-access-and-disclosure --yes`.
