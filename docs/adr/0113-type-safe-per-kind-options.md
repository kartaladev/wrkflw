# 0113. Type-safe per-kind activity options; remove definition.Lint

Status: **Accepted — 2026-07-08.**

## Context

The activity option system already encodes per-kind validity through marker interfaces: each
activity constructor (`NewServiceTask`, `NewUserTask`, `NewReceiveTask`, …) accepts its own
typed option interface (`ServiceTaskOption`, `UserTaskOption`, `ReceiveTaskOption`, …), and a
concrete option type satisfies only the interfaces of the kinds it is genuinely applicable to.
Options valid on every activity kind use the internal `activityOnlyOption` helper, which
satisfies all marker interfaces at once.

One option was mis-scoped: `WithWaitReminder` wrapped `reminderOpt` using `activityOnlyOption`,
making it silently accepted by all activity constructors — ServiceTask, SendTask, BusinessRule,
SubProcess, CallActivity — even though only UserTask and ReceiveTask possess an engine strategy
that arms an in-wait reminder. The inconsistency was papered over at runtime by
`definition.Lint`, a post-build advisory pass that emitted `definition.Warning` values (rule
`"reminder-ignored"`) when a `WithWaitReminder` was found on an incompatible node. The driver
called `lintDefinition` before each process run and logged the warnings, but they were never
surfaced as errors and could easily go unnoticed.

[`lestrrat-go/option`](https://github.com/lestrrat-go/option) was evaluated as an alternative.
Its restriction technique is identical to the existing marker-interface pattern and adds runtime
boxing of option values with no extra compile-time safety beyond what Go's own interface
assignment already provides. Introducing the dependency would buy nothing.

## Decision

We will narrow `WithWaitReminder` so it returns
`interface { UserTaskOption; ReceiveTaskOption }` instead of the broad `activityOnlyOption`.
Passing it to any other activity constructor is now a compile-time error.

We will keep `activityOnlyOption` for all genuinely-universal options (`WithRetryPolicy`,
`WithCompensation`, `WithDeadline`, `WithRecoveryFlow`, `WithCancelHandler`).

We will remove `definition.Lint`, the `definition.Warning` type, and the driver's
`lintDefinition` hook entirely. There is no remaining runtime advisory that justifies keeping
the mechanism.

We will add a package-level convention comment above the option-interface block in
`definition/activity/options.go` stating: options scoped to a subset of activity kinds MUST
return a narrow anonymous interface; `activityOnlyOption` is reserved for options that are
genuinely valid on every kind; there is no runtime lint pass.

No new dependency is introduced.

## Consequences

- **Positive.** Mis-applying `WithWaitReminder` (or any future subset-scoped option) to an
  incompatible activity constructor is a compile-time error rather than a silently-ignored
  runtime warning. The constraint is enforced by the type checker and requires no test coverage
  of its own.
- **Positive.** The codebase is simpler: the `definition.Lint` function, the `Warning` type,
  the `lintDefinition` driver hook, and associated tests are gone.
- **Positive.** The convention is documented in-source; future contributors adding a new
  option have a clear pattern to follow.
- **Neutral (breaking).** The public `definition.Lint` and `definition.Warning` API is removed.
  The module is pre-1.0, so this is acceptable. Any caller using these APIs for custom advisory
  checks must migrate to a compile-time approach or implement their own advisory pass.
- **Neutral.** Future non-type-enforceable advisories (e.g. a semantic rule that cannot be
  expressed as a type mismatch) will need a fresh, purpose-built mechanism — the now-deleted
  `Lint` machinery must not be reinstated for a single rule.
- **Neutral.** Later phases (input validation: ADR-0110/0111/0112; completion-action:
  ADR-0114) add new options following this convention: they are born-narrow, returning the
  appropriate subset interface from the start.
