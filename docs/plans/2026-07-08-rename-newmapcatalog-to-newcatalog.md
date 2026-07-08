# Rename action.NewMapCatalog → action.NewCatalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hard-rename the constructor `action.NewMapCatalog` → `action.NewCatalog` (identical signature and behavior) across the module.

**Architecture:** Behavior-preserving refactor. `NewMapCatalog` is a unique token with no collision (`action.NewCatalog` does not pre-exist), so one safe global whole-word replace covers every site. The existing test suite is the safety net.

**Tech Stack:** Go 1.25, `perl -pi`, `go test -race`, `golangci-lint`.

## Global Constraints

- Rename ONLY the constructor token `NewMapCatalog` → `NewCatalog`. The **type** `MapCatalog` (no `New` prefix) stays — the `\b` word boundary protects it.
- Do NOT change signatures, behavior, or the return type (`NewCatalog` still returns concrete `MapCatalog`). `Registry`, `NewRegistry`, `DefaultCatalog`, `Catalog` are untouched.
- Do NOT edit markdown under `docs/` (the `find` is restricted to `*.go`). ADR-0108 is committed separately.
- Behavior-preserving: no new tests (existing suite must be green before AND after).
- `go test -race ./...` needs Docker for the PG/MySQL testcontainer suites; if unavailable, run `go test ./...` and report that as a concern.
- Commit trailers on every commit:
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf
  ```

---

### Task 1: Execute the rename (scripted, atomic)

**Files:** every `*.go` containing `NewMapCatalog` (~111 occurrences: `action/catalog.go` definition + `action/doc.go` prose + call sites across runtime/engine/service/transport/examples/processtest/tests).

**Interfaces:**
- Produces: `func NewCatalog(m map[string]Action) MapCatalog` in `action/catalog.go` (was `NewMapCatalog`).

- [ ] **Step 1: Global whole-word replace**

Run from repo root:
```bash
find . -name '*.go' -print0 | xargs -0 perl -pi -e 's/\bNewMapCatalog\b/NewCatalog/g'
```

- [ ] **Step 2: Audit**

```bash
echo "--- remaining NewMapCatalog (expect NONE) ---"; grep -rn "NewMapCatalog" --include='*.go' .
echo "--- NewCatalog now defined (expect 1 func def) ---"; grep -rn "func NewCatalog" --include='*.go' action/
echo "--- MapCatalog type intact (expect type/method/var lines) ---"; grep -rn "type MapCatalog\|MapCatalog)" --include='*.go' action/catalog.go
```
Expected: first grep empty; second shows `func NewCatalog(m map[string]Action) MapCatalog`; third shows the type still named `MapCatalog`.

- [ ] **Step 3: Build + vet**

```bash
go build ./... && go vet ./...
```
Expected: clean, no output.

- [ ] **Step 4: Full race suite**

```bash
go test -race ./... 2>&1 | grep -vE '^ok |no test files'; echo "EXIT=${PIPESTATUS[0]}"
```
Expected: `EXIT=0`, no `FAIL`/`panic`/`DATA RACE`.

- [ ] **Step 5: Lint**

```bash
golangci-lint run ./...
```
Expected: `0 issues.`

- [ ] **Step 6: Run touched examples**

```bash
for ex in signal_broadcast usertask_approval parallel_fork_join; do
  echo "== $ex =="; go run ./examples/scenarios/$ex/ >/dev/null && echo OK || echo FAIL
done
go run ./examples/readme_quickstart/ >/dev/null && echo "quickstart OK"
```
Expected: each prints `OK`/`quickstart OK`.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor(action): rename NewMapCatalog to NewCatalog" \
  -m "The map-backed Catalog is the default impl; Registry (the only other Catalog) already owns NewRegistry, so the map catalog takes the unqualified NewCatalog. Constructor-only rename; MapCatalog type and concrete return kept. ADR-0108." \
  -m "Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>" \
  -m "Claude-Session: https://claude.ai/code/session_01RVxKQ8g7m5haiTbnXjDbEf"
```

---

### Task 2 (controller): merge, HANDOVER, push

- [ ] Merge `refactor/rename-newmapcatalog-to-newcatalog` into main (--no-ff).
- [ ] Refresh `docs/plans/HANDOVER.md` CURRENT RESUME POINT (state hash, next free ADR → 0109, ADR-0108 shipped bullet, mark harness task #1 done).
- [ ] Re-run `go build ./...` + `go test -race ./...` gate, then `git push origin main`.

---

## Self-Review

- **Spec coverage:** constructor def ✓ (T1 S1), all call sites ✓ (T1 S1 global find), prose ✓ (T1 S1), ADR-0108 ✓ (separate commit), type-untouched ✓ (constraint + `\b`), verification ✓ (T1 S2–S6), merge/push/HANDOVER ✓ (T2).
- **Placeholder scan:** none.
- **Type consistency:** produced signature `func NewCatalog(m map[string]Action) MapCatalog` matches the spec; single new identifier `NewCatalog`.
