# Tasks

Apply waits for the maintainer's rulings on Q6 and Q7 (design, Open Questions). Every Go task starts with `cc-skills-golang:golang-how-to`. Run commands per module, as CONTRIBUTING.md describes.

## 1. Operation authorizer hook (#108, #129, pending Q6 and Q7)

- [ ] 1.1 Add `authz` operation-authorizer types and `service.WithOperationAuthorizer`. Write the tests first for "Without the option no instance operation is authorized", scenarios "The default keeps unauthenticated write" and "A nil authorizer is a construction error". Verify with `GOTOOLCHAIN=go1.26.9 go test ./service/ ./transport/http/...`.
- [ ] 1.2 Write the failing tests for "A configured operation authorizer is consulted once before each instance operation":
  - "Each operation names itself and its target";
  - "Engine-originated starts are not caller operations";
  - "Human-task operations keep their own authorizer".
  Then wire the hook into each `ProcessEngine` instance method. Verify with `GOTOOLCHAIN=go1.26.9 go test ./service/`.
- [ ] 1.3 Write the failing tests for "A refused instance operation returns the refusal and changes nothing":
  - every row;
  - "Negative control, an allowing authorizer changes nothing about the outcome";
  - "An authorizer failure that is not a denial still blocks";
  - "Concurrent refused and allowed calls do not interfere", with `-race`.
  Make them pass. Verify with `GOTOOLCHAIN=go1.26.9 go test -race ./service/`.
- [ ] 1.4 Write the tests for "The unidentified caller reaches the authorizer as the zero actor", scenario "The hook decides for the anonymous caller", and make them pass. Verify with `GOTOOLCHAIN=go1.26.9 go test ./service/`.
- [ ] 1.5 Put the resolved actor into the request context on hooked routes, reusing the single resolution per request. Write the tests first for "HTTP routes answer an operation refusal without rendering state":
  - "Refused routes on every adapter";
  - "The route hands the resolved actor to the authorizer";
  - "Negative control, an allowing authorizer keeps 200".
  Also cover the modified `instance-disclosure` scenario "Only the operation authorizer refuses a read". Verify with `GOTOOLCHAIN=go1.26.9 go test ./transport/http/...`, and confirm that "One resolution per read" still passes.
- [ ] 1.6 Document the hook in the `doc.go` `# Trust boundary` section, replacing "There is no per-instance access control" with how to add it. Add the sentence to the demo resolver comments in `examples/production_wiring`, `sqlite_wiring` and `mysql_wiring` saying that `WRKFLW_DEMO_INSECURE_ACTOR` also lifts redaction on instance reads (#129 comment). Verify with `scripts/check-doc-refs.sh` and `go build ./examples/...`.

## 2. Integration

- [ ] 2.1 Run the full suite per module, with Docker for the dialect rows, and `golangci-lint run`. Verify that both exit 0.
- [ ] 2.2 Run `openspec validate add-instance-access-hook --strict` and `scripts/check-openspec.sh`. Verify that both pass.

## Workflow follow-up

- The PR's last commit runs `openspec archive add-instance-access-hook --yes`.
