# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`, and every test goes red before the code that makes it pass. Tables follow the `table-test` skill. All commands use `GOTOOLCHAIN=go1.26.9`. Run tests per module as CONTRIBUTING.md describes; the `gin` and `fiber` bindings may be their own modules after `split-persistence-modules`.

Q1 must be ruled before 3.2, and Q2 before 2.3. `add-user-task-updates` Q1 must be ruled before 1.3.

## 1. POST /tasks/{token}/updates

- [ ] 1.1 Write the parity rows first in `transport/http/parity`: the accepted submission on stdlib, gin and fiber (202 and `{"seq": 1}`), the body-actor row, the malformed-body table and the absent-input control. Watch them go red (404 today). Scenarios: "202 with the seq", "A body-supplied actor is ignored", "Malformed submissions" and "input may be omitted for an update with no descriptor". Verify with `go test ./transport/http/...`.
- [ ] 1.2 Add the `Route` entry and request DTO (design D3), resolving identity before the body. Call `TaskService.Progress` and `ApplyTrigger`, and answer the seq. Make 1.1 green. Add the 401-before-body rows for the update route. Scenarios: "unauthenticated request with a bad body" (updates rows) and "401 envelope code on the update route". Verify with `go test ./transport/http/...`.
- [ ] 1.3 Write the classification table first, including a co-match test for an error wrapping both `ErrUnknownUpdate` and `ErrInvalidTransition`. Then add the 400 arm (design D4). Cover the completion-missing-updates rows, and the refuse row per the core Q1 ruling. Scenarios: "Engine refusal to HTTP", "The 422 message is actionable", "POST complete on an incomplete task" and "refuse policy surfaces as a conflict carrying the seq". Verify with `go test ./transport/http/...`.

## 2. GET routes

- [ ] 2.1 Write `humantask` tests first for the `submittable` predicate (design D2), one row per situation. Then implement it next to `Completable`. Scenario: "When an update is submittable". Verify with `go test ./humantask/...`.
- [ ] 2.2 Write the `GET /tasks/{token}` rows first (the mid-way form view, no declared updates, 404 and three-adapter byte equality). Then add the route reading the snapshot (design D1). Scenarios: "The read surface for a form", "Completable with an empty list" and "404 and identical bodies". Verify with `go test ./transport/http/...`.
- [ ] 2.3 Write the history rows first (paired rows, empty history and 404). After Q2 is ruled, add the paging rows. Then add the route over `TaskUpdateReader.History`. Scenarios: "submitted then applied", "Empty history is not an error" and "limit and cursor". Verify with `go test ./transport/http/...`.

## 3. Disclosure

- [ ] 3.1 Write the per-field disclosure table first for both routes (`{}`, `{policy}`, `{variables}`, `{actors}`, `{all}`), the identified-caller rows, and the task-field parity with `GET /instances/{id}`. Then implement the projection (design D5). Scenarios: "One category, one field", "Disclosure narrows only unidentified callers" and "Same rules as the instance routes for the task's own fields". Verify with `go test ./transport/http/...`.
- [ ] 3.2 After Q1 is ruled, write the `reason` table and the identified-caller control, then gate `reason`. Scenarios: "Error text and the unidentified caller" and "The actor who can retry sees why". Verify with `go test ./transport/http/...`.
- [ ] 3.3 If `define-access-and-disclosure` is already on main, add `Cache-Control: private, no-store` to both GET routes, with its parity row. Otherwise note in the PR that the other change must add it. Verify with `go test ./transport/http/parity/...`.

## 4. Docs

- [ ] 4.1 Update the route-group docs and `transport/http` README to list the three routes. Add the one line on the noun `/updates` route beside the verb routes, and cite `openspec/specs/http-transport/spec.md`. Verify with `scripts/check-doc-refs.sh` and a full per-module `go test -race` run.

## Workflow follow-up

- The PR's last commit runs `openspec archive add-user-task-updates-http`.
