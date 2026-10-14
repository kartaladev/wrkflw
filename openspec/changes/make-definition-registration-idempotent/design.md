# Design

## Context

See proposal.md, Why. Current state, re-measured on this branch (go1.26.9) with a throwaway probe module outside the repo that imports the library through a `replace` directive:

- `MemDefinitionRegistry.Register` refuses any second registration of a pinned key, including the same pointer: `Register(d); Register(d)` returns `ErrDefinitionExists: "p1:1"` (`runtime/kernel/mem_definition_registry.go`, the `if _, exists := r.m[pinned]` branch).
- `RegisterDefinition` runs the message-start check before `Register`. Sequence `a` v1 on M, `a` v2 on N, `b` v1 on M, then `a` v1 again with identical content: returns `ErrDuplicateMessageStart: "M"`. The check compares the incoming definition with the latest version of each other ID, and `b`'s latest now holds M.
- A different-content re-registration whose message name collides with another ID's latest returns `ErrDuplicateMessageStart`, not `ErrDefinitionExists` (`d` v1 on Q while `c` holds Q).
- The durable store's identity is `normaliseDefinition` (`internal/persistence/store/definitions.go`): `json.Marshal`, `json.Unmarshal` into a `model.ProcessDefinition`, `json.Marshal` again, then `bytes.Equal`. It is applied to both sides because `scoped_actions` is marshal-only.
- Probe results under that normalisation: same scoped-action names with different implementations are identical; different scoped-action names are identical; a builder definition with scoped actions and a struct literal without them are identical; `CancelActions` nil and `[]string{}` are identical; a builder definition and the same definition loaded from YAML are identical; a definition and its own decoded JSON are identical.
- A definition whose user task carries a `callback` completion validation registers in memory, but `json.Marshal` fails with `model.ErrUnserializableValidation`. Such a definition can never be published, so the durable store has no precedent for comparing it.

Tests that pin the old refusal of identical content: `TestMemDefinitionRegistry_DuplicateVersionedKey` and `TestMemDefinitionRegistry_MustRegisterPanicsOnError` in `runtime/kernel/mem_definition_registry_test.go`, and the "duplicate versioned key" row of `TestMustRegisterDefinitionPanics` in `runtime/definition_registry_test.go`. Each registers `minimalValidDef`/`validDef` twice with identical content.

## Goals / Non-Goals

**Goals:**
- One meaning of "identical" for both doors, defined once in code.
- A no-op is a true no-op: no index write, no latest-key move, no warning log, no message-start check.

**Non-Goals:**
- Changing `PublishDefinition`'s outcomes or its error attribution (incoming side decoded first; stored-side decode detail never returned).
- Defining which error wins when different content also collides on a message-start name.
- Making an unencodable definition comparable.

## Decisions

### D1. Move the normalisation to a shared internal package

The decode/re-encode transform moves from `internal/persistence/store` to a new root-module internal package (working name `internal/defcontent`) that imports only `definition/model`. `runtime/kernel` and `internal/persistence/store` both import it. The store keeps its own `sameDefinitionContent`, which owns the error-attribution and disclosure rules, and calls the shared transform for each side.

Rejected:
- **Export the comparison from `runtime/kernel`.** It would add public API whose only consumer is this module, and would make "the scoped catalog takes no part" a public promise of a kernel function, not a property of registration.
- **A method on `model.ProcessDefinition`, such as `Equal`.** The same objection, more strongly: a public `Equal` that says two definitions with different live action implementations are equal invites misuse.
- **Copy the function into the kernel.** Parity is a contract, and two copies are two definitions that can drift. `TestDefinitionStorePublishImmutable` and the new kernel tests would each guard only their own copy.

Settled by the import graph: `go list -deps ./runtime/kernel` shows the kernel depends on `definition/model` but not on the store, and the store imports the kernel, so the store's unexported function cannot be reached from the kernel.

### D2. The same object is identical without encoding

`Register` checks pointer equality before encoding. It is correct by definition, it saves encoding on the most common re-run (an init that registers a package-level variable twice), and it is the only way an unencodable definition can be shown identical. Measured: the same-pointer re-registration returns `ErrDefinitionExists` today, so this is part of the behavior change, not a pre-existing shortcut.

### D3. An unencodable copy stays refused, with both errors

When either side fails to encode, `Register` returns an error wrapping `ErrDefinitionExists` and the encode error (`model.ErrUnserializableValidation` for the callback case), so a caller can match either. The first registration is kept.

Rejected:
- **Treat an encode failure as identical.** Fails open: two copies with different callback validators would silently resolve to the first.
- **Return only the encode error.** It hides that the key is taken, which is the refusal the caller has to act on.

The incoming definition is the caller's own, so naming its encode error discloses nothing. The stored definition's encode error can also be returned: both are in-process objects supplied by the same program, unlike the durable store's rows.

### D4. Identical re-registration short-circuits before the message-start check

`RegisterDefinition` and `MustRegisterDefinition` hold `registerMu`, look up the pinned key, and when it holds identical content return nil (or do not panic) before `checkMessageStartUnique`, `Register`, and the force-termination and mixed-subprocess-start warnings. A no-op subscribes nothing and claims no name, so it cannot collide; the warnings were already logged at the first registration.

When the pinned key holds different content, the existing order is kept (message-start check, then `Register`, which refuses with `ErrDefinitionExists`). Rejected: refusing different content with `ErrDefinitionExists` before the message-start check. It is defensible, but it changes which error a measured case returns and the maintainer did not decide it (see proposal, Out of scope).

### D5. The comparison runs under the registry's write lock, only on collision

`Register` already holds `r.mu` for the existence check, so it compares there: a pointer check, then encoding of both sides only when the pinned key exists and the pointers differ. Registration of a new key is unchanged and does no encoding. Holding the lock while encoding blocks concurrent `Lookup` for the duration of two marshals of one definition, on a registration path that is init-time in practice. Rejected: compare under a read lock and re-check under the write lock. It is more code for a path that is not hot, and the registry is not a hot write path.

## Risks / Trade-offs

- [A consumer relied on `ErrDefinitionExists` or a panic to detect a repeated identical registration] → BREAKING note in the proposal; v0.x line.
- [Two definitions differing only in scoped-action implementations now silently resolve to the first catalog in memory, where the catalog is executable, unlike the durable store where it is never stored] → This follows the maintainer's decision to use the durable definition. It is pinned by the scoped-catalog scenario so it cannot change unnoticed, and documented on `Register`.
- [A separately built copy of a cyclic subprocess graph aborts the process inside `encoding/json`, where today it is refused with `ErrDefinitionExists`] → Reachable only by in-process Go construction, never from YAML or JSON, and it already aborts `PublishDefinition` and `json.Marshal`; tracked as a follow-up in the `ProcessDefinition.MarshalJSON` comment. The same-object path (D2) does not encode and is unaffected. Not fixed here; noted on `Register`.
- [Encoding under the write lock delays concurrent lookups] → Only on the collision path; see D5.

## Cross-change coupling

- `align-godoc-with-behavior` (skip_specs, so no spec overlap) rewrites the `ErrDuplicateMessageStart` bullet of the `RegisterDefinition` godoc and the `checkMessageStartUnique` comment in `runtime/definition_registry.go`, and leaves the `ErrDefinitionExists` bullet to this change. This change edits that bullet, the identical-re-registration short-circuit in the same function, and the `MustRegisterDefinition` and default-registry godoc. No landing order is required; whichever lands second rebases the shared godoc block. Any later change that MODIFIES "Message-start names are unique across globally registered definitions" must carry this change's added sentence and scenario.
- The shared normalisation package is new and owned here. kartaladev/wrkflw#151 (publish joins an ambient transaction) changes `PublishDefinition`'s querier, not its comparison, so it is not coupled beyond touching the same file.

## Migration Plan

None: in-memory state only, nothing persisted. Rollback is a revert.
