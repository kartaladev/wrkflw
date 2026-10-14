# Tasks

Apply waits for the maintainer's ruling on Q3 (design, Open Questions). Every Go task starts with `cc-skills-golang:golang-how-to`. Run commands per module, as CONTRIBUTING.md describes.

## 1. Inbox privilege parity (#200, pending Q3)

- [ ] 1.1 Write the failing test for "The claimable list honours eligibility privileges on every task store", scenario "Privilege matching in the claimable list", memory rows first. Watch it go red: on main a privileges-only task lists to no one. Verify with `GOTOOLCHAIN=go1.26.9 go test ./humantask/`.
- [ ] 1.2 Add the exact-string privilege overlap to `MemTaskStore.ClaimableBy` and to the SQL store's Go filter. Verify that 1.1 goes green. Then make the postgres, mysql and sqlite rows pass with Docker running: `GOTOOLCHAIN=go1.26.9 go test ./internal/persistence/store/ -run ClaimableBy`, plus the scenarios "Only unclaimed tasks are listed", "Negative control, role and candidate matches are unchanged" and "The listing agrees with the default composite authorizer".
- [ ] 1.3 Write the failing test for "The static resolver returns actors granted a spec privilege", scenario "Privilege candidates from the static map". Then teach `StaticActorResolver.Candidates` the rule. Verify that it goes green, and that "Negative control, role candidates are unchanged" passes, with `GOTOOLCHAIN=go1.26.9 go test ./humantask/`.
- [ ] 1.4 Update the `ClaimableBy` doc comments on both stores and `CachingTaskStore`, so they name three eligibility routes. Verify with `go vet ./humantask/ ./persistence/ ./internal/persistence/...`.

## 2. Integration

- [ ] 2.1 Run the full suite per module, with Docker for the dialect rows, and `golangci-lint run`. Verify that both exit 0.
- [ ] 2.2 Run `openspec validate surface-claimable-privileged-tasks --strict` and `scripts/check-openspec.sh`. Verify that both pass.

## Workflow follow-up

- The PR's last commit runs `openspec archive surface-claimable-privileged-tasks --yes`.
