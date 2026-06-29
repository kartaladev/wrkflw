# 0074. Retryable-action error contract

Status: **Accepted — 2026-06-29.**
Relates to: ADR-0063 (definition-scoped action catalog), ADR-0075 (built-in service-action catalog).

## Context

The `runtime/runner.go` executor returned `ActionFailed` with `Retryable=true` for every action error, regardless of the error's nature. Built-in actions (e.g., HTTP 4xx status, malformed config or template) must be able to signal that a failure is permanent and should not be retried. Without a contract for distinguishing transient from permanent errors, the engine retries every action error until backoff exhaustion, wasting time and resources on requests that will never succeed.

The engine's `ActionFailed` trigger (`engine/trigger.go`) already carries a `Retryable` boolean field to represent this intent. The missing piece is a way for action implementations (both consumer-written and library-provided) to communicate the retryability of their errors.

## Decision

Define a **public error contract** in the `action` package:

- **`Retryabler` interface:** An error that implements `Retryabler` (with method `Retryable() bool`) can communicate whether its failure is transient.
- **`NonRetryable(err error) error` constructor:** Wraps an error to mark it as non-retryable; implements `Retryabler` with `Retryable()` returning `false`.
- **`IsRetryable(err error) bool` helper:** Inspects an error via `errors.As` to find a `Retryabler`. If found, returns its `Retryable()` value; otherwise defaults to `true` (conservative: assume transient).

The `runtime/runner.go` executor now calls `action.IsRetryable(err)` to determine the `Retryable` field of the `ActionFailed` trigger, replacing the hardcoded `true`. The `engine` and `model` packages require no changes (zero-diff).

Default behavior is unchanged: plain errors and `nil` default to retryable. Only errors explicitly implementing `Retryabler` or wrapped with `NonRetryable` override this default.

## Consequences

- **Backward compatible:** Existing actions that return plain errors continue to retry by default. No consumer code breaks.
- **Actionable non-retryable failures:** Built-in actions (HTTP, email, transform, log) and consumer-written actions can now mark specific errors as permanent, improving observability and reducing wasted retry effort.
- **Minimal engine surface change:** Only one literal in `runtime/runner.go` changes; no new fields, interfaces, or triggers. The retryability decision remains in the action's hands.
- **Composable with error wrapping:** `NonRetryable` composes cleanly with `fmt.Errorf("%w", ...)` and other error wrapping, preserving the `Retryabler` interface through the stack.
