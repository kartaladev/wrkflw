# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`. Cases that share one call go in one table test (`table-test` skill). `T` below stands for `GOTOOLCHAIN=go1.26.9 go test -race`. Dialect rows need Docker (Postgres via `dbtest.RunTestDatabase`, MySQL via `dbtest.RunTestMySQL`). SQLite rows run without it. Run commands per module as CONTRIBUTING.md describes.

## 1. Composite coverage over every spec field (#202)

- [ ] 1.1 Red, proving the defect: in an `authz` internal test, add a guard that compares `AuthzSpec`'s fields with the `SpecField` constants ("The spec-field vocabulary names every spec field"), plus a test seam that runs the field derivation over a local copy of the spec type with an extra `Groups []string` field. Verify: `T ./authz/ -run '<new tests>'` fails on the seam, because a Groups-only spec yields no required fields and the composite allows. That turns #202's hypothesis into a measurement. Record the red output in the PR.
- [ ] 1.2 Add the rows of "Every spec field is required when set", including the empty-list, empty-string, one-space and non-`SpecReader` rows. Verify: green on today's code. These rows characterize today's "set" rules, so 1.4 cannot change them unnoticed.
- [ ] 1.3 Fix the allocation baseline: add a `testing.AllocsPerRun` test over the default composite with a two-field spec, and record today's count as its ceiling (design D4). Verify: `T ./authz/ -run '<alloc test>'` green on today's code.
- [ ] 1.4 Green: derive `requiredFields` from a precomputed reflective table. An unmapped set field is required under a synthetic name (design D4). Rewrite the `requiredFields` doc comment to name the table and the guard test by identifier. Verify: `T ./authz/...` green, including 1.1-1.3, the existing "Coverage is checked before deciding", "The default composite covers every field" and "Misconfigured composites" rows, and the allocation ceiling.

## 2. One storage rule for actors (#198, #114 item 5)

- [ ] 2.1 Red: in `authz`, add the actor classification totality test (design D3: `identity`, `persisted`, `request-time`, with stale-entry detection) and a table test for the storage copy operation. Cover the privileged actor, the ID-only actor, and an attribute literally named `privileges`. Verify: `T ./authz/ -run '<new tests>'` fails to compile or fails, because neither the table nor the operation exists yet.
- [ ] 2.2 Green: add the classification table and the exported storage copy operation (name per design D3). Clone first, then clear the request-time fields, so the receiver is untouched. Document it as the single strip that #115 item 7 will reuse. Verify: `T ./authz/...` green.

## 3. Task stores agree, and drop privileges (#198)

- [ ] 3.1 Red: in `processtest/taskstoreconformance.go`, add whole-actor round-trip cases covering "A privileged actor is stored without its privileges", "Negative control, actors without privileges round-trip whole", "Every listing applies the same rule as Get" and "The caller's actor is not mutated by the write". Include the reflective filler from design D3, which fails on an unsupported field kind. Verify: `T ./humantask/... ./internal/persistence/store/ -run 'TaskStoreConformance'` fails. Expected: on memory, privileges come back on every slot. On all three dialects, they come back on candidates, while the claim and completion rows already pass. Record which rows were red.
- [ ] 3.2 Add "The memory store and the SQL stores agree" as a cross-store test in `internal/persistence/store`. Verify: red on all three dialects before 3.3.
- [ ] 3.3 Green: apply the storage copy operation in `MemTaskStore.Upsert`, and on write and read of the SQL claim actor, completion actor and candidates. Keep the claim and completion remainder bytes identical for `roles` and `attributes` (design D3). Keep the nil-versus-empty distinction of `Candidates`. Rewrite the `htActorRemainder` doc comment to cite the classification table by identifier. Verify: every 3.1 and 3.2 row is green on memory, Postgres, MySQL and SQLite, and the existing "Claim invariant on write" and "Empty actor ID is not a wildcard" rows are unchanged.
- [ ] 3.4 Durability: write raw rows by SQL (bypassing `Upsert`) for "A legacy row carrying privileges" and "Negative control, a legacy row without privileges is unchanged". Verify: green on all three dialects. The stored `{}` and `{"roles":…,"attributes":…}` bytes decode as before.
- [ ] 3.5 Document: in `humantask/humantask.go`, change the `HumanTask`, `Claim` and `Completion` godoc from "the full `authz.Actor`" to the actor as stored, which carries no privileges. Cite `openspec/specs/human-task-lifecycle`. Verify: `scripts/check-doc-refs.sh` and `scripts/lint.sh` pass.

## 4. Integration

- [ ] 4.1 Run the full suite per module, with Docker. Verify: green, including `runtime/view` `TestClassification_IsTotal` (unchanged) and every example under `examples/` that reads a claim back.
- [ ] 4.2 Verify: `openspec validate guard-authz-field-totality --strict` and `scripts/check-openspec.sh` pass.

## Workflow follow-up

- The PR body names `guard-authz-field-totality` and states, for `replace-casbin-with-composed-deciders`, the journal trigger codec hand-off from design D5.
- The last commit runs `openspec archive guard-authz-field-totality`. Then comment on and close kartaladev/wrkflw#198, #201 (as stale, with the measurement) and #202.
