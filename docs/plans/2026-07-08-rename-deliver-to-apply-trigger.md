# Rename ProcessDriver.Deliver → ApplyTrigger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hard-rename the public method `ProcessDriver.Deliver` to `ApplyTrigger` (identical signature and behavior) across the whole module, its tracer span, godoc, prose, and tests, without touching the unrelated `processtest.Deliver` Decision constructor.

**Architecture:** Behavior-preserving refactor. No `gopls`/`gorename` available, so use two scripted, collision-safe text replacements: a whole-word replace everywhere EXCEPT the `processtest` package (where the only capital-`Deliver` token is this method or method-referring prose/strings), and a receiver-scoped `driver.Deliver(` replace inside `processtest` (which also defines the `Deliver` Decision constructor). The existing test suite is the safety net.

**Tech Stack:** Go 1.25, `perl -pi` for in-place edits, `go test -race`, `golangci-lint`.

## Global Constraints

- **NEVER whole-word replace `\bDeliver\b` inside `processtest/`.** That package defines and uses an unrelated `func Deliver(trigger engine.Trigger) Decision` (`processtest/drive.go:62`), called bare as `Deliver(...)` (handlers.go) and package-qualified as `processtest.Deliver(...)` (drive_test.go:60), and documented as `[Deliver]` (doc.go:27, drive.go:53). All of these MUST remain `Deliver`.
- The METHOD is only ever invoked on a `*ProcessDriver` receiver named `driver` or `runner` (possibly prefixed `h.`/`e.`/`e.h.`). Only those forms, the method definition, the span string, and method-referring prose change.
- Do NOT modify historical ADRs/plans markdown. ADR-0107 supersedes the naming.
- Behavior-preserving: identical signature `(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error)`. No new tests (TDD refactor rule: existing tests must pass before AND after).
- Verification gate for the whole plan: `go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...` all clean; runnable examples still execute.
- Commit trailers on every commit:
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf
  ```

---

### Task 1: Write ADR-0107 (independent — may run in PARALLEL with Task 2)

**Files:**
- Create: `docs/adr/0107-rename-deliver-to-apply-trigger.md`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing code-facing. Referenced by the HANDOVER update in Task 3.

- [ ] **Step 1: Write the ADR** (Nygard template — Status/Date, Context, Decision, Consequences)

Content must cover: Context (Deliver is the low-level primitive under DeliverMessage/BroadcastSignal; the name collides conceptually with DeliverMessage and misdescribes the "apply a trigger to the state machine" operation); Decision (hard-rename to `ApplyTrigger`, identical signature, no deprecated alias — consistent with the repo's `Run`→`Drive` and `NewCatch`→`NewIntermediateCatch` hard renames; span `wrkflw.runner.Deliver`→`wrkflw.runner.ApplyTrigger`); Consequences (positive: symmetric primitives `Drive`/`ApplyTrigger` + facade hierarchy is legible; neutral: trace dashboards keyed on the old span name need updating; the unrelated `processtest.Deliver` Decision is deliberately untouched). Reference [ADR-0105](0105-event-gateway-message-arm-delivery.md) and [ADR-0106](0106-broadcast-signal-driver-facade.md) as the facades built on this primitive.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0107-rename-deliver-to-apply-trigger.md
git commit -m "docs(adr): 0107 rename ProcessDriver.Deliver to ApplyTrigger" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>" \
  -m "Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf"
```

---

### Task 2: Execute the rename (scripted, atomic)

**Files (modified by the scripted replace):**
- Method def + span + godoc: `runtime/processdriver.go`
- Production callers: `runtime/processdriver_cancel.go`, `runtime/processdriver_message.go`, `runtime/processdriver_incident.go`, `runtime/timerops.go`, `service/service.go`
- Godoc snippets: `runtime/signal/signalbus.go`, `runtime/calllink/notifier.go`, `persistence/persistence.go`
- Examples: `examples/scenarios/{signal_broadcast,usertask_approval,inwait_reminder,attribute_authz,compensation_saga}/main.go`, `examples/{sqlite_wiring,mysql_wiring}/main.go`
- Tests: the 11 `_test.go` files referencing the method
- processtest (method calls only): `processtest/harness.go`, `processtest/drive.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func (driver *ProcessDriver) ApplyTrigger(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error)` — the renamed primitive that `DeliverMessage`/`BroadcastSignal`/cancel/incident/timer paths now call.

- [ ] **Step 1: Whole-word replace everywhere EXCEPT processtest**

Run (from repo root):
```bash
find . -name '*.go' -not -path './processtest/*' -print0 \
  | xargs -0 perl -pi -e 's/\bDeliver\b/ApplyTrigger/g'
```
This renames: method def (`) Deliver(`), all `driver.Deliver(`/`runner.Deliver(` calls, the span string `wrkflw.runner.Deliver`, method godoc/prose, and the `"...Deliver failed"` log strings together with the test that asserts on them. It does NOT match `DeliverMessage`, `DeliverFunc`, or lowercase `deliverLoop`/`delivered` (word-boundary + case-sensitive).

- [ ] **Step 2: Receiver-scoped replace INSIDE processtest**

Run:
```bash
perl -pi -e 's/driver\.Deliver\(/driver.ApplyTrigger(/g' processtest/*.go
```
This renames only the method calls (`h.driver.Deliver(`, `e.h.driver.Deliver(`, `e.driver.Deliver(`). It leaves the `Deliver` Decision constructor, its bare `Deliver(...)` calls, the `processtest.Deliver(...)` qualified call, and the `[Deliver]` docs untouched.

- [ ] **Step 3: Audit — confirm no stray method references remain and the Decision is intact**

Run:
```bash
echo "--- remaining driver./runner. Deliver calls (expect NONE) ---"
grep -rnE "(driver|runner)\.Deliver\(" --include='*.go' .
echo "--- old span string (expect NONE) ---"
grep -rn '"wrkflw.runner.Deliver"' --include='*.go' .
echo "--- whole-word Deliver remaining (expect ONLY processtest Decision + DeliverMessage/DeliverFunc) ---"
grep -rnw Deliver --include='*.go' . | grep -vE 'processtest/'
```
Expected: first two greps print nothing; the third prints nothing (all remaining whole-word `Deliver` live in `processtest/` as the Decision). If anything unexpected appears, fix it by hand before proceeding.

- [ ] **Step 4: Build + vet**

Run:
```bash
go build ./... && go vet ./...
```
Expected: clean, no output. A build error here means a reference was missed or a false positive was introduced — inspect and fix.

- [ ] **Step 5: Full race test suite**

Run:
```bash
go test -race ./... 2>&1 | grep -vE '^ok |no test files'; echo "EXIT=${PIPESTATUS[0]}"
```
Expected: `EXIT=0`, no `FAIL`/`panic`/`DATA RACE`. (Needs Docker for the PG/MySQL testcontainer suites.)

- [ ] **Step 6: Lint**

Run:
```bash
golangci-lint run ./...
```
Expected: `0 issues.`

- [ ] **Step 7: Run the touched examples**

Run:
```bash
for ex in signal_broadcast usertask_approval inwait_reminder attribute_authz compensation_saga; do
  echo "== $ex =="; go run ./examples/scenarios/$ex/ >/dev/null && echo OK || echo FAIL
done
go run ./examples/sqlite_wiring/ >/dev/null && echo "sqlite OK"
```
Expected: each prints `OK`/`sqlite OK` (mysql_wiring needs a DB — skip).

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(runtime): rename ProcessDriver.Deliver to ApplyTrigger" \
  -m "Behavior-preserving hard rename of the low-level trigger-apply primitive; DeliverMessage/BroadcastSignal/cancel/incident/timer paths call it. Span wrkflw.runner.Deliver -> wrkflw.runner.ApplyTrigger. processtest.Deliver Decision constructor deliberately untouched. ADR-0107." \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>" \
  -m "Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf"
```

---

### Task 3: Merge to main, refresh HANDOVER, push

**Files:**
- Modify: `docs/plans/HANDOVER.md` (CURRENT RESUME POINT block)

**Interfaces:**
- Consumes: the merge commit hash from this task's merge step.
- Produces: nothing.

- [ ] **Step 1: Merge the branch into main (no-ff)**

```bash
git checkout main
git merge --no-ff refactor/rename-deliver-to-apply-trigger \
  -m "Merge: rename ProcessDriver.Deliver -> ApplyTrigger (ADR-0107)" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>" \
  -m "Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf"
MERGE=$(git rev-parse --short HEAD); echo "merge=$MERGE"
```

- [ ] **Step 2: Update HANDOVER CURRENT RESUME POINT**

Set the state line to `origin/main == main == <MERGE>`, bump "Next free ADR" to 0108, add a shipped bullet for ADR-0107 (Deliver→ApplyTrigger), and remove the "OPEN follow-up: driver.Deliver rename under discussion" note added under the af128e5 bullet (now done). Add a note that task #1 `NewMapCatalog`→`NewCatalog` remains queued.

- [ ] **Step 3: Commit HANDOVER + re-run the full gate once more**

```bash
go build ./... && go test -race ./... >/dev/null 2>&1 && echo "gate green"
git add docs/plans/HANDOVER.md
git commit -m "docs(handover): resume point after Deliver->ApplyTrigger (ADR-0107)" \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>" \
  -m "Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf"
```

- [ ] **Step 4: Push main**

```bash
git push origin main
```
Expected: `main -> main` update line.

---

## Self-Review

- **Spec coverage:** method def ✓ (T2 S1), span ✓ (T2 S1), production callers ✓ (T2 S1), processtest method calls ✓ (T2 S2), examples ✓ (T2 S1), godoc ✓ (T2 S1), tests ✓ (T2 S1), ADR-0107 ✓ (T1), historical-docs-untouched ✓ (constraint + find excludes nothing but only edits .go), collision-safety ✓ (T2 S1 excludes processtest, S2 receiver-scoped, S3 audit), verification ✓ (T2 S4–S7), merge/push/HANDOVER ✓ (T3).
- **Placeholder scan:** none — all steps carry exact commands.
- **Type consistency:** the produced signature in Task 2 matches the spec verbatim; `ApplyTrigger` is the single new identifier used throughout.
- **Parallelization:** Task 1 (ADR, new .md) and Task 2 (rename, .go) touch disjoint files and may run concurrently; Task 3 gates on both.
