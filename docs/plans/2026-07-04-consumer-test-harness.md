# Consumer test harness (`processtest`) — implementation plan

Spec: `docs/specs/2026-07-04-consumer-test-harness.md` · ADR: `docs/adr/0092-consumer-test-harness.md`
Branch: `feat/consumer-test-harness`

Strict TDD: each task writes a failing test first (visible red via `go test`),
then the minimum implementation (green), then refactor. Tasks are ordered so each
builds on green predecessors.

## Tasks

1. **Supporting seam — `MemScheduler.NextFireAt`** (`runtime/kernel`)
   - Test: schedule two timers, assert `NextFireAt` returns the earlier fireAt+true;
     empty scheduler returns false. Red: undefined. Green: implement.

3. **`FakeClock`** (`processtest/clock.go`)
   - Test: `Now`/`Advance`/`Set`; satisfies `clock.Clock`. Red→green.

4. **Fakes: `SpyCatalog`** (`processtest/spycatalog.go`)
   - Test: resolves+records invocation (Name/In/Out/Err) by wrapping an inner
     action; `Count`/`InvocationsOf`. Red→green.

5. **Fakes: `SpyAuthorizer`** (`processtest/spyauthz.go`)
   - Test: default allow; `Deny(err)`; records calls. Red→green.

6. **Fakes: `CaptureSender`** (`processtest/capturesender.go`)
   - Test: exposes an `email.SenderFunc` that records `SentEmail`; wire with real
     `email.NewEmail(email.WithSender(cap.SenderFunc()))` and assert capture. Red→green.

7. **Park model + classifier** (`processtest/park.go`)
   - Test (table): given constructed `engine.InstanceState`s (terminal, open task,
     await-signal, await-message, armed-timer, incident), `classify` returns the
     right `Reason` + populated fields; priority ordering. Red→green.
   - `Reason` + `String()`, `Park`, `isTerminal`.

8. **Decision + constructors** (`processtest/decision.go`)
   - Test: zero value is Pass; each constructor sets its kind; accessor used by loop.
     Red→green (unexported kind, exported constructors).

9. **Drive loop core + free `DriveToCompletion`** (`processtest/drive.go`)
   - Test: build a trivial 2-node auto definition, `Run` via a real driver, drive
     to `Completed`. Test unhandled park → `ErrUnhandledPark`; drive-limit →
     `ErrDriveLimitExceeded`; `Stop`/`Abort`. Red→green.

10. **`Harness` fixture** (`processtest/harness.go`)
    - Test: `New()` wires stack; `Start`+`DriveToCompletion` with `AutoTimers()`
      drives a timer definition (uses fixture clock+scheduler+NextFireAt) to
      terminal; accessors expose spies. Red→green.
    - Options: `WithActions/WithAction/WithAuthorizer/WithActorResolver/`
      `WithSignalBus/WithDefinitions/WithDriveLimit/WithClockStart`.

11. **Handler combinators** (`processtest/handlers.go`)
    - Test: `AutoTimers`, `CompleteTasks` (approval flow to Completed via fixture
      TaskService), `PublishSignal`, `DeliverMessage`, `Chain` precedence. Red→green.

12. **Godoc Examples + package doc** (`processtest/example_test.go`, `doc.go`)
    - `Example` for automatic flow, timer flow, approval flow, email capture.

13. **ADR/spec cross-check + coverage/lint gate.**

## Verification checklist

- [ ] `go build ./...` clean.
- [ ] `go test ./processtest/... ./runtime/kernel/...` green.
- [ ] `go test -race -coverprofile=cover.out ./processtest/... && go tool cover -func=cover.out | tail -1` ≥ 85%.
- [ ] `go test ./...` from repo root — no regressions.
- [ ] `golangci-lint run ./...` clean.
- [ ] `engine` + `definition/model` + `action/email` zero-diff (`git diff --stat` shows no changes there).
- [ ] Every new exported symbol had a visible red state before implementation.
- [ ] Godoc `Example`s compile and pass.
```
