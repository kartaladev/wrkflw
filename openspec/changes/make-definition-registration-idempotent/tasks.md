# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`, and cases that share one call go in one table test (`table-test` skill). `T` below stands for `GOTOOLCHAIN=go1.26.9 go test -race`.

## 1. Share the normalisation (refactor, behavior unchanged)

- [ ] 1.1 Move the decode/re-encode transform out of `internal/persistence/store` into a new root-module internal package (design D1), with a unit table test: nil vs empty `CancelActions`, a builder definition vs its YAML-loaded twin, scoped-action names present vs absent, and different cancel-action order. Point the store's `sameDefinitionContent` at it, keeping its error attribution unchanged. Verify: `T ./internal/...` green, including every `TestDefinitionStorePublishImmutable`, `TestDefinitionStoreScopedRepublishIsIdempotent` and `TestBaselineScopedRepublishReadsBackUnchanged` row on all three dialects (Docker), and `scripts/check-extraction.sh` passes.

## 2. In-memory registry: identical is a no-op

- [ ] 2.1 Red: add kernel table tests for "An identical re-registration is a no-op" (in-memory rows: same object, separately built, YAML-loaded, MustRegister), "Equality is judged on normalised content", "Definitions differing only in scoped actions are identical and the first catalog is kept", "A definition that cannot be encoded is identical only to itself", "An identical re-registration of an older version does not move latest", and the in-memory rows of "Concurrent registrations of one version resolve to a single registration". Verify: `T ./runtime/kernel/ -run '<new tests>'` fails on the identical, scoped, YAML, older-version and identical-concurrency rows, and passes on the unencodable-copy and distinct-content rows (which pin today's refusal).
- [ ] 2.2 Rewrite `TestMemDefinitionRegistry_DuplicateVersionedKey` and `TestMemDefinitionRegistry_MustRegisterPanicsOnError` to register different content, and fold them with `TestBaselineMemRegistryRefusesDifferentContentAndKeepsTheFirst` into the in-memory rows of "A duplicate version with different content is refused" (including the reordered and case-variant cancel-action rows), plus the rows of "A different version of the same ID is accepted". Verify: these pass before and after 2.3.
- [ ] 2.3 Green: in `MemDefinitionRegistry.Register`, on an existing pinned key, return nil for the same pointer or identical normalised content, and otherwise `ErrDefinitionExists` (joined with the encode error when either side cannot be encoded), without touching either key (design D2, D3, D5). Verify: `T ./runtime/kernel/...` green, including every 2.1 and 2.2 row.
- [ ] 2.4 Refactor and document: update the godoc of `ErrDefinitionExists`, `Register` and `MustRegister`, and the comment on the `>=` latest-key line, to state identical-is-a-no-op, the scoped-catalog consequence and the cyclic-graph risk, citing `openspec/specs/definition-store`. Verify: `scripts/check-doc-refs.sh` and `scripts/lint.sh` pass; `T ./runtime/kernel/...` still green.

## 3. Global registration: identical is not a collision

- [ ] 3.1 Red: add runtime table tests for the process-global rows of "An identical re-registration is a no-op", "Concurrent registrations of one version resolve to a single registration", and every row of "An identical re-registration is not a message-start collision", using unique IDs and message names per case. Verify: `T ./runtime/ -run '<new tests>'` fails on the identical re-registration of `a` v1 after `b` took M (`ErrDuplicateMessageStart`) and on the other identical rows; the different-content, new-ID and unclaimed-name rows pass.
- [ ] 3.2 Rewrite the "duplicate versioned key" row of `TestMustRegisterDefinitionPanics` to register different content, and add the global rows of "A duplicate version with different content is refused". Verify: these pass before and after 3.3.
- [ ] 3.3 Green: in `RegisterDefinition` and `MustRegisterDefinition`, under `registerMu`, return nil (and do not panic) when the pinned key already holds identical content, before the message-start check, the registry write and the warnings; keep today's order for different content (design D4). Verify: `T ./runtime/...` green, including every 3.1 and 3.2 row and the existing `TestRegisterDefinitionRejectsDuplicateMessageStart` rows.
- [ ] 3.4 Refactor and document: update the `RegisterDefinition`, `MustRegisterDefinition` and default-registry godoc ("first-registration-wins") to state identical-is-a-no-op and that it is not a message-start collision. Verify: `scripts/check-doc-refs.sh` and `scripts/lint.sh` pass; consider `/simplify` on the diff.

## 4. Integration

- [ ] 4.1 Run the full suite for every module as CONTRIBUTING.md describes (`for pat in $(scripts/modules.sh ...)`; Docker for the store) and confirm no other test relied on the old refusal. Verify: all modules green on go1.26.9, and `openspec validate make-definition-registration-idempotent --strict` and `scripts/check-openspec.sh` pass.

## Workflow follow-up

- Open the PR naming this change; its last commit runs `openspec archive make-definition-registration-idempotent`.
- No GitHub intake issue to close: kartaladev/wrkflw#111 is already closed. Optionally comment on it with this change's name.
