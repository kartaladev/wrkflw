# Proposal

## Why

Closed kartaladev/wrkflw#111 decided that a published `(ID, Version)` is immutable and that re-publishing identical content is a no-op, because strict refusal "would punish every re-run deploy". `PublishDefinition` implements that, but in-memory registration still refuses an identical re-registration with `ErrDefinitionExists`, so the two doors that share one authoring gate disagree on the same input. The baseline (`establish-baseline-specs`, design D7 group 3) recorded this as needing a decision; the maintainer has now decided it.

## What Changes

- **BREAKING** `kernel.MemDefinitionRegistry.Register`, `MustRegister`, `runtime.RegisterDefinition` and `runtime.MustRegisterDefinition` return nil (the Must variants do not panic) when an already-registered `(ID, Version)` is registered again with identical content. The call changes nothing: the first registration keeps both its pinned and its latest key, and its scoped-action catalog. Today each of these calls returns `ErrDefinitionExists` or panics (measured at HEAD with a probe; pinned by three existing tests that this change rewrites).
- Different content under a registered `(ID, Version)` is still refused with `ErrDefinitionExists`, and the first registration is kept. Unchanged.
- "Identical" means what it means for `PublishDefinition`: both definitions, encoded and then put through the same decode/re-encode normalisation, give equal bytes. The scoped-action catalog is never serialised, so two definitions that differ only in their scoped actions (names or implementations) are identical, and the first catalog is the one kept. The same object registered twice is identical without encoding.
- A definition that cannot be encoded (a Go-authored callback validation strategy, `model.ErrUnserializableValidation`) cannot be shown identical, so a separately built copy is still refused with `ErrDefinitionExists`, wrapped together with the encode error. This is the behavior today and is only made explicit.
- An identical re-registration through `RegisterDefinition` / `MustRegisterDefinition` is not a message-start collision. Today, re-registering a superseded version whose message-start name was later taken by another definition ID fails with `ErrDuplicateMessageStart` (measured at HEAD); after this change it returns nil. A new or different definition claiming a held name is still refused.
- The comparison is shared between the durable store and the in-memory registry, not copied into both, so the two definitions of "identical" cannot drift.
- Godoc that says "first-registration-wins" without qualification is aligned: `ErrDefinitionExists`, `MemDefinitionRegistry.Register` and `MustRegister`, `RegisterDefinition`, `MustRegisterDefinition`, and the default-registry test-isolation note.

Persisted formats: none affected (the in-memory registry persists nothing; the durable store's encoding and comparison are unchanged). Default security posture: unchanged. Consumer `go.mod`: unchanged.

Issues: closed kartaladev/wrkflw#111 is the decision this applies, not absorbed (it is already closed). Checked for overlap and found none to absorb: kartaladev/wrkflw#151 (publish joins an ambient transaction) is durable-store only; kartaladev/wrkflw#152 (MySQL `def_id` collation) is about key equality on one dialect, not content equality; kartaladev/wrkflw#157 (`ErrConcurrentPublish` and pgx wrapping) is the durable read-back path. This change does not touch any of their code paths except the shared normalisation, whose behavior is unchanged.

Out of scope:
- Which error wins when a different-content re-registration also collides on a message-start name. Today `ErrDuplicateMessageStart` wins; this change keeps that order and specifies no precedence.
- The `ErrDuplicateMessageStart` / `RegisterDefinition` godoc claim that any collision is refused, when only each other definition's latest version is compared. The change `align-godoc-with-behavior` owns it.
- `MapDefinitionRegistry`, which is built from a fixed set and has no registration call.
- Marshalling a cyclic subprocess graph, which aborts the process inside `encoding/json`; already tracked as a follow-up in the `ProcessDefinition.MarshalJSON` comment.

Decisions this change needs, all resolved:
- Identical re-registration is a no-op success; different content is refused keeping the first (maintainer decision, applying kartaladev/wrkflw#111).
- Identical is defined as `PublishDefinition` defines it (maintainer decision).
- Identical re-registration must not trip `ErrDuplicateMessageStart` (maintainer decision).
- An unencodable definition stays refused unless it is the same object (follows from the previous two: identity cannot be shown, and silence is never correct).

**Coordination**
- Size: S
- Depends on: establish-baseline-specs
- Blocks: nothing
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `definition-store`: "In-memory registration keeps the first registration of a version" gains the identical-is-a-no-op rule, the shared meaning of identical, and the concurrency, latest-key and scoped-catalog cases; "Message-start names are unique across globally registered definitions" states that an identical re-registration is not a collision.

## Impact

- `runtime/kernel` (`MemDefinitionRegistry.Register`, `MustRegister`, `ErrDefinitionExists` godoc).
- `runtime` (`RegisterDefinition`, `MustRegisterDefinition`, message-start check, default-registry godoc).
- `internal/persistence/store` (the normalisation moves to a shared internal package; `PublishDefinition` behavior unchanged).
- Tests that pin the old refusal of identical content: `runtime/kernel` `TestMemDefinitionRegistry_DuplicateVersionedKey`, `TestMemDefinitionRegistry_MustRegisterPanicsOnError`, and `runtime` `TestMustRegisterDefinitionPanics` ("duplicate versioned key"). Each is rewritten to register different content.
- Consumers that relied on `ErrDefinitionExists` or a panic to detect a repeated identical registration get nil instead.
