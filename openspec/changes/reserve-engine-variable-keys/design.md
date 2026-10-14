# Design

## Context

See proposal.md (Why) for the measurements. They were taken on `main` at `9c6dd2c1` with scratch probes run through `go test -overlay`. The constraints that shape the approach are below.

- **The engine writes three names.**
  - `_idempotencyKey` is stamped into a copy, the primary action input only, and never into the instance variables.
  - `_errorMessage` and `_errorAttempts` are written into the instance variables on the retry-exhaustion catch-flow (`engine/step_triggers.go:562-564`).
  - `_error` is injected into a clone for a boundary error expression and never persisted.
- **One filter exists today, and it covers one site.** `foldsOntoReservedName` withholds variants from the primary service-task input only (`engine/step_state.go`). Five other sites build an action input from the instance variables or from a snapshot of them:
  - the completion action, `parkOnCompletionAction`, reached from human completion and from message receipt;
  - the definition cancel actions and the node cancel actions, both in `handleCancelRequested`;
  - `emitFireOnceAction`, which serves deadline, reminder, armed-boundary (timer, signal, message) and error-boundary actions;
  - `compensationInvoke`, which takes a snapshot recorded when the activity completed.
- **`mergeVars` is the single write path, and its callers are mixed.** All eight callers are in `step_triggers.go`.
  - Five pass maps from outside the engine: start variables, human completion output, signal payload, and the message payload on two paths: one matched by an armed event, boundary or event-subprocess arm, and one matched by a waiting receive task.
  - Three pass maps the engine or consumer code originates: an action's completion output, a child instance's whole variable map (`runtime/processdriver_action.go:667` passes `childSt.Variables`), and `applyOutcomeExposure`, whose key comes from the definition.
- **A call activity passes the parent's whole variable map to the child** (`step_nodes.go:1149`). After a recovered failure, that map holds the exact `_errorMessage`. If a child start refused reserved keys, every call activity downstream of a recovery would fail to start its child.
- **The program cache masks the guard meant to catch its loss.** Mutation measured: pass the env to `compile` and add `expr.Env(env)`. `TestBuiltinsAreNotShadowableByEnvKeys` then passes, because it evaluates the base env first and the cache serves that program to the planted env. With the cache also disabled, it reports 6 shadowable identifiers. Under the plain mutation, the only failure in `internal/expreval`, `engine`, `runtime` and `authz` is one subtest of `TestBoundaryRoutingUnderCallerChosenKeyNames`. `TestEvalBoolMemoizes` passes under the mutation.

## Goals / Non-Goals

**Goals:**
- No action receives a caller's fold-variant in place of an engine-written value, including for instances stored before the upgrade.
- After Q1 to Q3 are answered, a caller cannot write an engine-stamped variable, and is told so by name.
- The no-shadowing barrier and cache soundness each have a guard that fails on the mutation it exists for.

**Non-Goals:**
- Rewriting stored snapshots or journal rows that already hold reserved keys.
- Reserving `_error`, or reserving the whole `_` prefix (unless Q2 chooses it).
- Type-checking expressions.

## Decisions

**D1. One filter, applied at every action-input site. It stays after the ingress refusal lands.**
The fold-variant predicate becomes the single helper used by all six action-input builders. That covers the five sites above plus the primary input. For compensation, the filter runs when the action is invoked, on the recorded snapshot, so records written before the upgrade are covered too. The exact names pass through, because the recovery action reads them.
- *Rejected: rely on the ingress refusal alone.* A snapshot written before the upgrade can already hold `_errormessage` (spec scenario "A variant stored before the upgrade is withheld on replay"). The ingress never sees it again, so only a copy-site filter closes it. This also settles the question left in the `foldsOntoReservedName` comment: the filter stays, as the guard for stored data, not as unexplained residue.
- *Rejected: filter inside `copyVars`.* `copyVars` also builds `StartVariables`, the `CompleteInstance` result, the send-task payload, the task eligibility snapshot and state clones. Filtering there would silently remove state, which the project rules forbid.
- *Rejected: six inline copies of the loop.* That is the shape #187 calls the mechanism of the defect.

**D2. Pending Q1 (recommended option): refuse in the trigger handlers, before anything changes.**
Each handler that merges an external map checks it before its first mutation and returns `ErrReservedVariable` wrapped with the key. There are four such handlers: start, human completion, message receipt on both paths, and signal receipt. `Step` therefore returns the error with the input state untouched and no commands. The check is not placed inside `mergeVars`, because three of its callers originate their maps inside the engine or in consumer code (Q3). A trigger whose map is clean is accepted even when the stored variables already hold a reserved key, so instances stored before the upgrade keep running.
- *Rejected (Q1 option): strip reserved keys at `mergeVars`.* A caller's key would vanish silently, which the project rules forbid and #187 warns against.
- *Rejected as recommended (Q1 option): namespace the engine's keys.* Moving `_errorMessage` and `_errorAttempts` into a separate state field, or a prefix callers cannot write, changes the snapshot format and every definition that routes on `_errorMessage`. Both are expensive to reverse.

**D3. The sentinel and its HTTP mapping.**
`engine.ErrReservedVariable` is declared in `engine/errors.go`. The error text names the key exactly as supplied, quoted with `%q` so that variants made of invisible characters stay legible. `transport/http/httpcore` maps it to 400 `bad_request`, because the caller can fix the request. It cannot co-match the 413 arm or the identity arms. Per the standing invariant in `ClassifyError`, the new arm states its position and gets a test.

**D4. Pending Q3 (recommended): maps that originate inside the engine.**
- A child's start input withholds the three reserved names. The child's catch-flow writes its own values.
- A child's completion output is merged without the reserved names, so the parent keeps its own values. The child's variables began as a copy of the parent's, so a reserved name in its output either echoes the parent's value or describes the child's failure, which is not the parent's.
- An action's completion output carrying a reserved name fails that action as non-retryable, naming `ErrReservedVariable`. The failure then follows the node's existing failure routing instead of a step error, because a step error would leave the token waiting on a command that has already completed.
- `model.Validate` refuses an `OutcomeVariable` that names a reserved key. This is a new validation sentinel, owned here.
None of these is specified until Q3 is answered. The spec only requires that a call activity after a recovered failure still starts its child.

**D5. (#186) Make both guards fire. The compile path stays free of `expr.Env`, pending Q4.**
- The shadowing sweep uses a fresh evaluator for each evaluation, so the cache cannot hand one env's program to another.
- A new cache-order test evaluates one code string under environments that differ in key set and value type, in both orders, against one shared evaluator. It must go red under the `expr.Env` mutation measured above. Task 4.3 records that run.
- A comment at `compile` names both guards and cites `openspec/specs/expression-evaluation/spec.md`.
- *Rejected: rely on `TestBoundaryRoutingUnderCallerChosenKeyNames`.* It fails today only because of the order in which its subtests evaluate. That is incidental, not a guard.

**Cross-change coupling.**
- `refuse-silent-expression-outcomes` edits `internal/expreval/expreval.go`: it caches free identifiers beside each program (its D1), and that design assumes no `expr.Env`. This change keeps that assumption true and touches only tests plus a comment in that file. Either change can land first. If Q4 is answered "key the cache by environment shape", the free-identifier cache must be keyed the same way. That change owns `ErrAbsentVariable`, and the spec rows here avoid absent variables so the two do not interact.
- `refuse-silent-expression-outcomes` and `export-refusal-sentinels` both edit `engine/errors.go`. The additions are independent, so the merge is additive.
- `persist-pending-commands`: a `StartSubInstance` persisted before the upgrade may still carry the parent's reserved names. Under D2 the child start comes from the engine, not from a caller, so it is not refused. This holds whichever change lands first.
- `harden-typed-action-input` owns the strict-mode decode behaviour these inputs feed into. It is not changed here.
- `cut-v0-release` is blocked by this change, because kartaladev/wrkflw#187 blocks kartaladev/wrkflw#48.

## Risks / Trade-offs

- [Consumers that start instances or complete tasks with `_errorMessage` or `_idempotencyKey`, for example to replay a failed run, start failing after the upgrade.] → BREAKING, named in the proposal. The error names the key. The migration is to rename the variable.
- [A message or signal arriving from a bus with a reserved key is refused on every redelivery.] → The bus sees the same named error each time, the same as any other refused trigger. Dead-lettering is the bus consumer's existing path. Payload ceilings belong to `bound-untrusted-input`.
- [`TestErrorMessageIsCallerWritable` flips from pinning the hazard to pinning the refusal.] → Task 3.1 rewrites it as the red test for the refusal. The test is not deleted.
- [The fold check costs a fold comparison per key on every external merge.] → Three names, with an early check on length and a leading `_`. Benchmarked in task 3.4.

## Migration Plan

- No persisted format change, so rollback is a revert.
- Stored instances are not rewritten. Variants in them are withheld from action inputs (D1), and exact reserved keys a caller wrote before the upgrade stay until the engine overwrites them.

## Open Questions

These need the maintainer before apply. No decision is recorded on either issue thread. The specs carry the recommended option for Q1 and Q2 and nothing that depends on Q3 or Q4.

- **Q1. What does "reserved" mean? (#187)**
  - (a) Refuse the write with `ErrReservedVariable`. This is recommended: it adds no format change and drops nothing silently.
  - (b) Namespace the engine's keys outside caller-writable variables. This changes the persisted format and every `_errorMessage` condition.
  - (c) Strip on merge. This violates the project's rule against silence.
- **Q2. Which keys? (#187)**
  - (a) The three names exactly, plus their fold-variants. This is recommended; the exact names are the fabrication and suppression attack.
  - (b) Fold-variants only. This leaves fabrication and suppression open.
  - (c) Every `_`-prefixed key. This is broader and breaks `_traceId`-style variables.
- **Q3. Maps that originate inside the engine or in consumer code.** These are the action completion output, the child start input, the child completion output, and `OutcomeVariable`. D4 gives the recommendation for each. Each choice adds spec rows and tasks.
- **Q4. `expr.Env` at the evaluator seam. (#186)**
  - (a) Refuse it structurally, with the reason stated at `compile` and both D5 guards. This is recommended: it keeps the barrier and the cache sound, and `refuse-silent-expression-outcomes` already depends on it. It gives up expr's type checking.
  - (b) Allow it, keying the cache by environment shape. This regains type checking but deletes the shadowing barrier, which would then need a different mechanism.
  The specs hold under either answer.
