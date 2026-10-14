# Design

## Context

See proposal.md, *Why*, for the measured premises. The design depends on four facts measured on `main` at `9c6dd2c1`:

- `requiredFields` (`authz/composite.go:194-208`) decides which fields are "set" with a different rule per field. A slice counts as set when its length is above zero, so an empty non-nil `Roles` is unset. A string counts as set when it is not `""`, so `" "` is set. Any derivation has to keep both rules. `reflect.Value.IsZero` alone would not, because it reports an empty non-nil slice as non-zero.
- The SQL store writes an actor in two parts: the ID goes to the `claimed_by` / `completed_by` column, and a JSON remainder goes to `claim_actor` / `completion_actor`. Candidates are written as `json.Marshal([]authz.Actor)`, which goes through `Actor`'s own JSON tags and so carries `privileges`.
- `MemTaskStore` stores `HumanTask.Clone()`, so it keeps whatever it is given.
- `authz.Actor.ID` is tagged `json:"id"` **without** `omitempty`.

## Goals / Non-Goals

**Goals:**
- An `AuthzSpec` field that nobody mapped fails closed, at runtime and in CI.
- An `Actor` field that nobody classified as persisted or stripped fails CI.
- Task stores agree with each other on stored actors, and apply #114 item 5 (decided).

**Non-Goals:**
- Stripping privileges from the snapshot, the journal or the engine handlers. That is #115 item 7, in `replace-casbin-with-composed-deciders`.
- Rejecting unknown keys in a stored actor remainder. A JSON decode that ignores unknown keys is the rolling-upgrade question #158 records, and it is not reopened here.
- Any at-rest sensitivity classification (retired by #52).

## Decisions

### D1. Task stores drop privileges at their boundary (applies #114 item 5, decided)

#198 asked whether the target is "both persist" or "both drop". #114 item 5 decided it: persisted actor copies (claim, completion, candidates) drop privileges, because they are matched by ID and role, a stored privilege list goes stale on the first policy change, and the snapshot must not grow for a nicety. The #119 amendment recorded on #115 extends this to both levels of the actor.

Every `TaskStore` implementation applies the strip on write. The SQL store also applies it on read (D2). The strip works on a copy, so the caller's request-time actor keeps its privileges.

Rejected:
- **Both persist** (add `privileges` to the SQL remainder). This contradicts #114 item 5.
- **Faithful stores, with the strip only in the engine** (#115 item 7). `humantask.TaskStore` is a public interface, and a consumer may upsert into it directly. The parity contract in `openspec/config.yaml` says the stores behave identically whoever writes. Item 7 has also not landed. The measured state shows why a single upstream strip is not enough on its own: the SQL store already disagrees with itself (claims dropped, candidates kept), and no test noticed. The store strip complements item 7 and does not replace it.

### D2. Legacy rows are stripped on read, not migrated

Rows in the `candidates` column written before this change carry privileges. A read-side strip in the SQL store covers them, and covers any hand-written `claim_actor` row that carries a `privileges` key.

Rejected: a data migration that rewrites every `candidates` row. It is a persisted-format operation with three dialect variants, and it gives nothing a read strip does not. The rows would still hold the tokens until rewritten. That residual is stated under Risks.

### D3. One strip, owned by `authz`, with a classification table and a reflective round trip

`authz` gains one exported operation that returns the copy of an actor that may be stored. A candidate name is `Actor.ForStorage()`, and the name is the implementer's call. It clones the actor and clears every field classified as request-time. A classification table in `authz` assigns every exported `Actor` field to `identity`, `persisted` or `request-time`. Two tests guard it:

1. **Totality**, modeled on `TestClassification_IsTotal` (`runtime/view/classification_test.go`). It fails on an unclassified field, and on a stale entry for a field that no longer exists.
2. **Reflective round trip**, in the task-store conformance suite. It sets every `Actor` field to a non-zero value by reflection, writes the actor through each slot (claim, completion, candidate) of each store, and asserts that the read equals `ForStorage()` of the input. This test proves the SQL remainder's marshal and decode actually honour the table. The table alone cannot prove that, because the remainder's encoding is still written by hand.

`replace-casbin-with-composed-deciders` will reuse the same operation for item 7 in the engine handlers. When it adds `OnBehalf`, it extends the operation to strip both levels. When it adds `Role`, it classifies the field. Both totality tests go red until it does.

Rejected:
- **Derive the remainder from `Actor`'s JSON tags** (marshal the actor with ID and privileges zeroed). `id` has no `omitempty`, so every new row would gain `"id":""`, which changes stored bytes for today's fields. It would also make a new field persisted by default, with no decision recorded. `Privileges` is exactly a field that should not have been persisted by default.
- **A classification table in the store package.** Item 7's engine strip would then need its own list, which makes a third hand-written list.

The remainder struct stays hand-written. Its bytes for `roles` and `attributes` stay identical, so `claim_actor` and `completion_actor` need no migration. Its doc comment is rewritten to cite the classification table by identifier, so the comment cannot drift the way the current one did.

### D4. `requiredFields` derives from `AuthzSpec` by reflection, and an unmapped field fails closed

A package-level table, computed once with `reflect.TypeFor[AuthzSpec]()`, maps each field index to its `SpecField`. A field with no mapping is assigned a synthetic `SpecField` built from its Go name. No decider can declare that name, so a spec that sets the field is refused with `ErrSpecNotEvaluable` naming it. "Set" keeps today's rules: length above zero for slices and maps, not `""` for strings, and `!IsZero()` for anything else. A totality test asserts that the table and the `SpecField` constants are a bijection with `AuthzSpec`'s fields. This is the observable invariant in the modified requirement.

Rejected:
- **Keep the three `if`s and add only a test.** #202's acceptance asks for runtime fail-closed as well as a CI signal. A test alone leaves the runtime fail-open whenever the test is skipped by `-run`. The register entry for #209 recorded that hazard.
- **Struct tags on `AuthzSpec`** (`authz:"roles"`). This works, but it adds a tag vocabulary for one table's sake, and a mistyped tag is a silent mismatch the bijection test would still have to catch.
- **Panic at `init` on an unmapped field.** A library must not panic at import time. The synthetic name fails closed per request instead.

`Composite.Authorize` is on the per-request path. The reflected field indices are precomputed. The task fixes the allocation baseline of today's `checkCoverage` first, with `testing.AllocsPerRun` over the default composite and a two-field spec, and pins the new implementation at or below it.

### D5. Landing order and coupling

- **`replace-casbin-with-composed-deciders`** (#115; #179 moved to `remove-casbin`) shares `authz/authz.go`, the `Actor` struct and the strip operation. Either order works, so this is not a `Blocks` edge. Landing this change first is preferred, because then both totality guards fail on that change's `Role` and `OnBehalf` edits and force a recorded classification. If that change lands first, this change classifies the final shape, and its round-trip test gains an `OnBehalf` row asserting privileges are stripped at both levels (#119). **Hand-off finding for that change:** item 7 names the engine handlers and `resolveHumanCandidates`, but the journal's trigger codec (`internal/persistence/store/trigger_codec.go:67-71`) also persists `actor`, `by` and `candidates` with privileges. Whether journal trigger rows are "persisted actor copies" under #114 item 5 is that change's call.
- **`surface-claimable-privileged-tasks`** (#200) edits `ClaimableBy` in the same two files (`humantask/memory.go` and `internal/persistence/store/humantask_store.go`). The edits are textually independent. Whichever lands second rebases. Its read-side strip of privileges in the view projection and this change's store strip are complementary layers of one decision (#114 item 5).

## Risks / Trade-offs

- [A consumer reads privileges back from `MemTaskStore` or from SQL candidates] → BREAKING note in the proposal, and the `HumanTask`, `Claim` and `Completion` godoc stops promising "the full `authz.Actor`". Privileges were decided as request-time only.
- [Legacy `candidates` rows keep privilege tokens at rest until rewritten] → They never surface through the store API (D2). Any later upsert of the task rewrites the row without them. The snapshot and journal copies remain until item 7 lands, and the proposal names this so a green run here is not read as "no privileges at rest".
- [Reflection gets "set" wrong for an empty non-nil slice] → A spec row pins `roles: []` as unset, and the bijection test runs over every field kind present.
- [The round-trip test fills fields reflectively and misses a kind added later, such as a pointer to `Actor`] → The filler fails the test on an unsupported kind instead of skipping it.

## Migration Plan

There is no schema migration. New rows stop writing `privileges` into `candidates`. `claim_actor` and `completion_actor` bytes are unchanged for today's fields. Rollback is safe: an older binary reads rows without `privileges` as actors with no privileges, which is what it produced for claims and completions anyway.
