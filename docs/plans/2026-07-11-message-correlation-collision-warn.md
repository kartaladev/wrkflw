# Message-correlation collision WARN — implementation plan

- Spec: `docs/specs/2026-07-11-message-correlation-collision-warn-design.md`
- ADR: `docs/adr/0125-message-correlation-collision-warn.md`
- Branch: `fix/message-correlation-collision-warn`

## Goal

Emit a WARN in `syncMsgWaiters` when re-registering a `(name, correlationKey)` key
already owned by a **different** running instance. Delivery semantics unchanged.

## TDD steps

1. **RED** — Add `runtime/message_collision_warn_test.go` (white-box, `package
   runtime`). Inject a custom `*slog.Logger` writing to a `bytes.Buffer` (JSON
   handler) via `WithLogger`. Register one definition with a keyed `ReceiveTask`
   await, drive **two** instances that park awaiting the same `(name, key)`. Assert
   the buffer contains the WARN with both instance IDs and the `(name, key)`, and
   that a single `DeliverMessage` completes **exactly one** instance (behavior
   unchanged). Run `go test ./runtime/...` → must FAIL (WARN not emitted yet).

2. **GREEN** — In `runtime/processdriver_waiters.go`, in the re-register loop of
   `syncMsgWaiters` (after the ADR-0124 terminal guard), read the existing owner of
   each key; if present and `!= st.InstanceID`, `LogAttrs` a WARN
   (`context.Background()`, `slog.LevelWarn`, message + correlation_key +
   incumbent_instance + joining_instance), then proceed with the existing overwrite.
   Update the `msgWaiters` field doc in `runtime/processdriver.go` to note the 1:1
   violation is WARN-logged. Run `go test ./runtime/...` → PASS.

3. **REFACTOR** — If a second test case is added, fold into a `table-test` closure
   form. Re-run tests.

## Verification checklist

- [ ] `go build ./...` clean
- [ ] `go test -race ./...` — 0 failures
- [ ] `runtime` coverage ≥ 85% (`go test -coverprofile=cover.out ./runtime/ && go tool cover -func=cover.out | tail -1`)
- [ ] `golangci-lint run ./...` clean
- [ ] ADR-0125 written (Nygard template)
- [ ] `/code-review high` findings adjudicated + fixed
- [ ] `/security-review` findings adjudicated (expected none)
- [ ] No message-delivery-semantics change; no new public API
- [ ] ADR-0124 terminal guard / ADR-0123 accessors untouched except the added WARN

## Non-goals

- No multi-value map, no fan-out, no multi-instance delivery.
- No new public API.
