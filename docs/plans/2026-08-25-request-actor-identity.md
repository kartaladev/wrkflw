# ADR-0189 — Request-Actor Identity Implementation Plan

## ▶ Progress — SHIPPED. Both review gates passed; merged to `main`.

**Merged as `7be335fb`** (`--no-ff`, from `feat/request-actor-identity`, base `b5fe7272`).
**State:** all 13 tasks implemented, both gates passed, merged `--no-ff` and pushed.

### Delivery Gate

| gate | result |
|---|---|
| `/code-review` (high) | **8 findings, NONE a false positive — all 8 fixed and folded** |
| `/security-review` | **0 findings.** One candidate raised and rejected by the false-positive filter at 2/10 |

**The eight `/code-review` findings, each reproduced before it was fixed:**

1. ⚠⚠ **A 41 MB Mach-O binary was committed at the repo root.** `.gitignore` carries an explicit
   warning about this exact mistake **with an audit recipe**, and I added `examples/authenticated_tasks`
   without running it. `git rm --cached` + the missing entry; the repo's own recipe now passes.
2. ⚠⚠ **`deepCopyBounded` was not a deep copy.** It switched on `map[string]any`/`[]any` only, so
   every other container fell through **by reference** — measured: `map[string]string` and
   `[]string` both `shared=true`. That falsified the guarantee written three lines above it, and
   the guarantee was the crash fix. Rewritten as a reflection walk; both now `false`.
3. The ADR still read *"Proposed … not an input to implementation"* in the commit shipping it.
4. The ADR declared an in-bundle obligation to update ADR-0185's banner — **not discharged**.
5. ⚠ **`Logger`'s doc comment orphaned onto `RequestActor`** — the exact hazard a subagent had
   warned me about, and my own orphan-check missed it because struct-field comments are
   tab-indented and my regex was anchored at column 0.
6. ⚠⚠ **Identity resolved AFTER the body read.** See below — this reversed an owner decision.
7. The `production_wiring` demo actor authenticated every caller as a manager; now gated behind
   `WRKFLW_DEMO_INSECURE_ACTOR=1`, so a copy-paste **fails closed**.
8. A CHANGELOG quantifier overstated the malformed-body change. Measured: an **authenticated**
   caller now gets **200** — a previously-400 request *succeeds* — which I had under-reported in
   three documents.

### F6 — the finding that reversed a decision, and the ripple it caused

Owner decision D-4 had been *"keep the shape, document the ordering honestly"*, on the stated
premise that the exposure was not a regression because those routes were unauthenticated anyway.
**That premise stopped holding the moment this record authenticated them**, and `/code-review`
refused the residual on the repo's own rule that a documented residual is still a shipped defect.

⇒ `httpcore.RequestActor` is exported and every adapter calls it **before** the decode. The
401/503 policy still lives in exactly one place — the nine sites duplicate a two-line *call*,
never the decision — and the endpoints keep a zero-actor guard as defence in depth.

⚠⚠ **The ordering inverted: `401 → 413 → 400 → 404`, from `413 → 400 → 401 → 404`.** Seven tests
broke, every one correctly, including a contract the fiber agent had deliberately pinned with
reasoning — *a test asserting an ordering must be revisited when the ordering is the thing you
change*. Both new contract tests are mutation-verified and **discriminating**: mutating one
handler flips only that handler's row, and the gin failure body is literally the parse-error
disclosure the contract forbids.

### Found by `/security-review`, fixed although not a vulnerability

`deepCopyBounded`'s Slice/Array branch built a **slice** for an array input, so copying
`map[string][3]int` panicked (`SetMapIndex: value of type []int is not assignable to [3]int`) —
unrecovered on the request path. Not attacker-reachable (attribute values come from the consumer's
resolver; JSON claims never yield a Go array type), but a walk claiming to be total must not panic
on a shape it accepts. Arrays now copy as arrays; both branches gained an assignability guard that
refuses rather than panics.

### ⚠ A table-test rule I broke, caught by a subagent refusing my brief

I briefed `map[string]struct{…}`; the project `table-test` skill's canonical shape is a slice, and
the agent followed the skill over my brief — correct precedence, and it said so. Checking that
surfaced a rule I had actually violated: **rule 3 mandates a `ctx` modifier and a cancelled-context
case for context-sensitive components**, and `resolveRequestActor` hands `ctx` to consumer code.
Added: a cancelled context must surface as **503 with `context.Canceled` preserved in the chain**.

### Verification — all four items, run 2026-08-26

| item | result |
|---|---|
| `go test -race -coverprofile=cover.out ./...` | **EXIT=0**, zero failures, repo-wide (Docker up) |
| `go test ./...` no-regressions | **EXIT=0** |
| `golangci-lint run ./...` **repo-wide** | **EXIT=0, 0 issues** |
| coverage, touched packages, generated files excluded | all ≥ 85% |

`authz` **100.0%** · `stdlib` 96.1% · `httpcore` 94.9% · `service` **93.5%** · `gin` 88.5% ·
`internal/persistence/store` 88.1% · `fiber` 87.7%.
⚠ `service` reads **53.9% raw**; the four `*_mock.go` generated files are the difference. Both
numbers are real and measure different things — do not report the raw one as a regression.
⚠ The repo-wide total is **75.3%**, which is NOT the figure the floor governs; the floor is
per touched package.

### Mutations observed (each RED confirmed, restored from a `cp` backup, `diff` clean)

| what was mutated | observed |
|---|---|
| `ContextWithActor`'s IN clone deleted | RED — *"top-level Roles must be cloned on the way IN"* |
| `ActorFromContext`'s OUT clone deleted | RED — *"each read must get its own copy"* |
| both `ClassifyError` arms moved below the 404 arm | RED — `expected: 503, actual: 404` |
| attribute depth 64 → 9999 against a real SQLite store | RED — `unmarshal claim_actor …: invalid character '{' exceeded max depth` |
| the `ResolveConfig` timeout composition removed | RED — *"the default bound must reach the consumer's resolver"* |
| `stdlib` 403 resolver removed (×2) | RED — `want 403 complete forbidden, got 401` · `want 403 reassign forbidden, got 401` |
| `fiber` middleware channel `SetContext` → `Locals` | 403 → **401** |
| `gin`/`fiber`/`stdlib` oversize arm dropped | 413 → 200 (and 413 → 401) |

⭐ **Round 1's labelled PREDICTION held.** The two `stdlib` pins ADR-0185 called vacuous fail
loudly in production code, exactly as predicted before any of it was written.

### Corrections implementation forced on the design (rule #11 — all folded into the ADR)

1. ⚠⚠ **`RequestActorTimeout` was INERT.** Configured, defaulted to 10 s, aliased three times, and
   **read by nothing** — all three endpoints passed a hardcoded `0`. Round 2's interaction lens had
   named this (F5) and the finding was accepted and **not folded**; two implementers then found it
   independently by source-verification. Fixed by composing the bound **into** the resolver in
   `ResolveConfig`, which keeps Decision 2's "one added argument, no branch" true instead of
   falsifying it. `resolveRequestActor` now takes no timeout parameter.
2. ⚠ **A deadline test whose failure mode was a HANG.** Mutating away the composition gave
   `go test` **EXIT=124**, not a failure — remove the code and CI stalls silently. The
   ctx-honouring fixture now gives up after a bounded wait and *succeeds*, so the assertion fails
   readably in ~2 s. **A test for a deadline must not fail by waiting.**
3. ⚠ **Line-based surgery on the ADR deleted a whole decision.** Rewriting Decision 3 dropped the
   entire resolver-timeout paragraph while leaving a reference to it 140 lines later. Restored.
4. **A false count in a shipped comment**, caught by the premise sweep: the example claimed it
   *"proves all six cells"*; the idiom table has **five** (stdlib has no broken idiom). Re-derived
   from the running program: 5 cells, 11 probes.

### ⚠⚠ Hazards hit during implementation — read before repeating this shape

- **A killed background command left the tree MID-MUTATION.** The composition block was deleted and
  the restore never ran. `go build` passed and the option was silently dead again. **Always check
  the tree against the `cp` backup after any interrupted run** — CLAUDE.md says an agent killed
  mid-mutation leaves a deliberate breakage behind, and it did.
- **An `Edit` anchored on `func X(` can insert between a doc comment and its `func`**, re-parenting
  the doc onto the new function. It compiles, tests green, and `gofmt` and the linter say nothing.
  The fiber agent hit this and caught it only when an ablation printed the file.
- **A mutation that panics or fails to compile is not a RED.** The stdlib agent's first 413 ablation
  produced only a nil-deref; it re-ran a clean one and got `413 → 200`.


> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development`
> to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

> ✅ **SHIPPED — merged as `7be335fb`.** The task list below is history; read the `▶ Progress`
> block above for what actually happened. It is kept because the audit trail cites it.
>
> ⛔ **This bundle FAILED THREE rule-#9 audits before the owner closed the audit phase.**
> Round 1 (`7fa756d0`, 2 decisions): 48 findings / **7 Criticals**.
> Round 2 (`37d77a34`, 9 decisions): 58 findings / **15 Criticals** — Criticals/lens **doubled**
> when scope widened, so the scope is cut back to ONE decision.
> Adjudications: `audit-0189-adjudication.md`, `audit2-0189-adjudication.md`.
> Author grids: `audit-0189-author-interaction-grid.md`, **`audit2-0189-removal-grid.md`**.
> **Split out to ADR-0190:** route-group authentication and the admin posture.

**Goal:** the HTTP transport stops deriving `authz.Actor` from the request body, reads it from a
consumer-supplied identity seam on the `context.Context`, and refuses rather than downgrades when
no identity is established — on the three human-task verbs.

**Architecture:** `authz` gains `ContextWithActor`/`ActorFromContext`. `httpcore` gains
`RequestActorFunc` on `CustomizeConfig`, defaulting to the context seam. The three task endpoints
take it as a parameter and resolve inside. The three DTO actor fields and `httpcore.Actor` are
removed. ⚠ `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` are **out of scope** — they stay
unauthenticated and are **ADR-0190's** subject.

**Spec:** `docs/specs/2026-08-25-request-actor-identity.md` — read it with this plan.
**ADR:** `docs/adr/0189-the-http-transport-does-not-accept-a-self-asserted-actor.md`.

## Global Constraints

- **TDD strict.** Test → **observe RED in a Bash call** → implement → observe GREEN. A `Write` of
  a test followed immediately by a `Write` of the implementation with no `go test` between them
  is a plan violation.
- **Table tests use the project `table-test` skill's form** — `map[string]struct{…}` with an
  `assert func(t *testing.T, …)` closure, never `want`/`wantErr` fields — and `t.Context()`.
- **Black-box (`package <pkg>_test`)** except where a test must reach an unexported symbol; the
  transport packages are all `_test` today. `head -1` before appending to any existing test file.
- **Judge by exit code**: `go test ... > /tmp/x.log 2>&1; echo "EXIT=$?"` then read the log. Always
  `-count=1`. `-run` on a nonexistent name **exits 0** — confirm a test RAN with
  `grep -q '^--- PASS: <Name>'`.
- **Restore a mutation from a `cp` backup**, never `git checkout <path>`.
- **Fan out by Go package only.** Two agents in one package break each other's compile.
- **One commit for the bundle**; per-task changes `--amend` into it. No micro-commits.
- ⛔ **Do not write, anywhere, that every route now has an "identified principal".** §3.3 removed
  the empty-ID rule, so a resolved-but-empty actor passes. The true sentence is *"carries a
  **resolved** actor"*. This is the bundle's most likely recap-overreach.
- **No Docker needed** for tasks 1–15 (`transport/http/...`, `authz`, `service` are
  container-free). Task 13's coverage run needs it — probe, and label a subset as partial.

## Task order

Tasks 1–4 and 7 are **additive**; each is independently green.
⚠ **Task 6 is NOT additive** — it changes claim-route decode behaviour, so it follows Task 5.
**Task 5 is the compile-breaking wave and runs INLINE in the controller** (rule #11).
Tasks 8–10 fan out — distinct packages, concurrent agents.

⚠ **Between Task 5 and Task 11 the adapter suites are RED by design** — the 23 runtime pins still
send `"actor"`/`"by"` and now get 401, and `parity` is in that range too. Planned red; do not
"fix" it by restoring the fields.
⚠ **Record the failing count at Task 5 Step 5 and check it against 23 + parity.** Round 2's
planned-red figure was wrong by ~10× because a decision added after the count was never
re-derived — see the self-review note at the end.

---

### Task 1: the identity seam in `authz`

**Files:** create `authz/context.go`, `authz/context_test.go` (`package authz_test`).
**Produces:** `authz.ContextWithActor`, `authz.ActorFromContext`.

- [ ] **Step 1 — failing test.** Round-trip; bare context ⇒ `ok == false`; and the honest
      clone-depth contract:

```go
// TestActorContextCloneDepth pins what the seam ACTUALLY guarantees. Actor.Clone
// clones Attributes ONE LEVEL DEEP by its own godoc, so a nested value stays
// shared. ADR-0189 §3.1 states this rather than claiming full isolation.
//
// FAILS WITHOUT THE CLONE: drop a.Clone() in ContextWithActor and the top-level
// Roles mutation becomes visible.
func TestActorContextCloneDepth(t *testing.T) {
	t.Parallel()

	roles := []string{"manager"}
	nested := map[string]any{"team": "finance"}
	ctx := authz.ContextWithActor(t.Context(), authz.Actor{
		ID: "alice", Roles: roles, Attributes: map[string]any{"profile": nested},
	})

	roles[0] = "admin"        // top level: MUST NOT be visible
	nested["team"] = "hacked" // nested:    IS visible, and that is the contract

	got, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, []string{"manager"}, got.Roles, "top-level Roles must be cloned")
	assert.Equal(t, "hacked", got.Attributes["profile"].(map[string]any)["team"],
		"nested attribute values are SHARED — one-level clone, per Actor.Clone's godoc")
}
```

- [ ] **Step 2 — RED.** `go test -count=1 ./authz/... > /tmp/t1.log 2>&1; echo "EXIT=$?"` ⇒
      `undefined: authz.ContextWithActor`.
- [ ] **Step 3 — implement** exactly the code in spec §3.1 (clone on the way in **and** out).
- [ ] **Step 4 — GREEN**, confirming both test names appear in `--- PASS` lines.
- [ ] **Step 4b — ⚠ MUTATION, MANDATORY — and the round-3 fixture STILL FAILED IT.**
      Round 2 mutation-proved the round-1 test vacuous; round 3 re-prescribed a fixture that
      **also** stayed GREEN when the OUT clone was deleted, and round 3's execution lens proved it
      again. **The reason: mutating the CALLER's slice can only ever exercise the IN clone.**
      The OUT clone needs a second read:

```go
// TestActorFromContextClonesOnTheWayOut — FAILS when ActorFromContext returns the
// stored actor verbatim. Mutating what you GOT must not change what the NEXT
// caller gets.
ctx := authz.ContextWithActor(t.Context(), authz.Actor{ID: "alice", Roles: []string{"manager"}})
first, ok := authz.ActorFromContext(ctx)
require.True(t, ok)
first.Roles[0] = "admin"                     // mutate what we were handed
second, _ := authz.ActorFromContext(ctx)
assert.Equal(t, []string{"manager"}, second.Roles, "each read must get its own copy")
```

      Then run BOTH mutations:
      `cp authz/context.go /tmp/context.go.bak`; delete the clone in `ContextWithActor`, observe
      RED; restore; delete the clone in `ActorFromContext`, **observe RED again**; restore; `diff`.
      **If either deletion leaves the suite GREEN, the test is vacuous — fix the test, not the
      implementation.** Record both observed failures in `▶ Progress`.
- [ ] **Step 5 — purity guard**: `go test -count=1 ./engine/...` ⇒ EXIT=0.
- [ ] **Step 6 — commit.**

---

### Task 2: the two sentinels, arms FIRST, and the co-match tests

**Files:** modify `transport/http/httpcore/errors.go`; append to `errors_test.go`.
**Produces:** `httpcore.ErrUnauthenticated` (401), `httpcore.ErrIdentityUnavailable` (503).

Read `errors.go`'s standing invariant comment before editing. `ErrIdentityUnavailable` wraps
**arbitrary consumer code**, so it can co-match *any* arm ⇒ **both new arms go first**.

- [ ] **Step 1 — failing test**, four ordering cases **plus the pair round 1 missed**:

```go
func TestClassifyError_IdentitySentinelsOutrankEveryOtherArm(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}{
		"bare unauthenticated → 401": {...http.StatusUnauthorized, "unauthenticated"...},
		"identity failure wrapping ErrNotAuthorized → 503 NOT 403": {
			err: fmt.Errorf("%w: %w", httpcore.ErrIdentityUnavailable, authz.ErrNotAuthorized),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusServiceUnavailable, status)
				assert.Empty(t, body.Message, "5xx must never carry the raw error")
			},
		},
		"identity failure wrapping a not-found sentinel → 503 NOT 404": {...kernel.ErrInstanceNotFound...},
		"identity failure wrapping ErrBadInput → 503 NOT 400":        {...httpcore.ErrBadInput...},
		// ⭐ THE PAIR ROUND 1 MISSED: the two NEW arms co-match each other.
		// An unauthenticated report wrapped by a resolver failure satisfies both.
		"identity failure wrapping ErrUnauthenticated → 401, the more specific": {
			err: fmt.Errorf("%w: %w", httpcore.ErrIdentityUnavailable, httpcore.ErrUnauthenticated),
			assert: func(t *testing.T, status int, _ httpcore.ErrorBody) {
				assert.Equal(t, http.StatusUnauthorized, status)
			},
		},
	}
	for name, tc := range tests { /* t.Parallel(); ClassifyError(tc.err); tc.assert(...) */ }
}
```

⚠ The last case fixes the arm order between the two: **401 before 503**, because "no credential"
is the more specific fact and a resolver reporting it is not an outage.

- [ ] **Step 2 — RED** (`undefined: httpcore.ErrUnauthenticated`).
- [ ] **Step 3 — implement** the sentinels + both arms at the TOP of the switch, carrying the
      position comment from spec §3.4 verbatim (it is the invariant's required statement).
- [ ] **Step 4 — GREEN**, all five subtests.
- [ ] **Step 5 — MUTATION, mandatory.** `cp errors.go /tmp/errors.go.bak`; move both arms below
      the 404 arm; expect **EXIT=1** with the not-found case reporting 404; `cp` back; `diff`.
      **Record the observed failure text in `▶ Progress`.**
- [ ] **Step 6 — commit (`--amend`).**

---

### Task 3: `RequestActorFunc`, the config field, the option, and the resolver timeout

**Files:** modify `transport/http/httpcore/seam.go`; append to `seam_test.go`.
**Produces:** `httpcore.RequestActorFunc`, `CustomizeConfig.RequestActor`,
`CustomizeConfig.RequestActorTimeout`, `httpcore.WithRequestActor[R]`,
`httpcore.WithRequestActorTimeout[R]`.

- [ ] **Step 1 — failing test:** default reads the seam · default with a bare context ⇒
      `ErrUnauthenticated` · `WithRequestActor` overrides · `WithRequestActor(nil)` **restores the
      fail-closed default** · a resolver that blocks past the timeout ⇒ `ErrIdentityUnavailable`.

⚠ The timeout case is not decoration. On fiber `c.Context()` is `context.Background()` when no
middleware set one, so without a bound the resolver has **no deadline and no cancellation**.
Default **10 s**, mirroring `WithCandidateResolveTimeout`; non-positive disables.

⚠⚠ **It bounds only a resolver that HONOURS ctx cancellation, and BOTH tests are required.**
Measured in round 2: a ctx-ignoring resolver ran **1.5 s against a 200 ms bound and returned
`err == nil`**. The precedent carries that caveat in its own godoc
(`runtime/task/service.go:154`) and round 2 **stripped the hedge when restating it**.
  - test A: a ctx-**honouring** resolver that blocks past the bound ⇒ `ErrIdentityUnavailable`;
  - test B: a ctx-**ignoring** resolver ⇒ pinned as **still succeeding**, documenting the narrowed
    guarantee. ⚠ Round 2 wrote only test A, which **cannot fail on the real hazard**.
`WithRequestActorTimeout`'s godoc must state which state it closes and which it does not.

- [ ] **Step 2 — RED.**
- [ ] **Step 3 — implement.** ⚠ The `RequestActor` default belongs in `ResolveConfig`'s
      **post-loop nil-guard block** (a func has a nil, so an explicit-0 ambiguity cannot arise);
      `RequestActorTimeout` belongs in the **struct literal** (a `time.Duration` has no nil, and
      non-positive must stay meaningful as "disabled") — the same rule `BodyReadTimeout` follows.
      ⚠ Round 1's ADR called the literal "where `MaxBodyBytes` and `BodyReadTimeout` live" as if
      only those two were there; `Wrap`, `InstanceMapper` and `Logger` are in it too. Word the
      comment around **nil-distinguishability**, not membership.
- [ ] **Step 4 — GREEN.** - [ ] **Step 5 — commit (`--amend`).**

---

### Task 4: `resolveRequestActor` — the zero-actor rule and the DEPTH-BOUNDED attribute guard

**Files:** modify `transport/http/httpcore/endpoints.go`; create
`transport/http/httpcore/resolve_actor_internal_test.go` (`package httpcore` — the helper is
unexported; this is the **only** in-package test file in httpcore, keep it that way).

- [ ] **Step 1 — failing test**, one case per rule:

| case | expected |
|---|---|
| nil resolver | `ErrUnauthenticated`, and a **zero** actor returned |
| resolver reports `ErrUnauthenticated` | passes through; **NOT** `ErrIdentityUnavailable` |
| resolver errors | `ErrIdentityUnavailable`, **cause preserved** via `errors.Is` |
| resolver blocks past the timeout | `ErrIdentityUnavailable` |
| ⚠ `Actor{}` — the **zero actor** | **`ErrUnauthenticated`** — regression guard vs round 2, which accepted it |
| ⚠ `{Roles: []string{""}}` — the `strings.Split("",",")` artifact | **`ErrUnauthenticated`** — an empty string is not a role |
| ⚠ `{Roles: []string{}}` | **`ErrUnauthenticated`** — treated alike with the row above (round 3 refused one and admitted the other) |
| ⚠ `Actor{ID:"", Roles:["kiosk"]}` | **NoError — it PASSES** — regression guard vs round 1, which refused it |
| `Attributes` containing `chan int` | `ErrIdentityUnavailable` (503, **not** 400 — the fault is the consumer's resolver) |
| ⭐ `Attributes` nested **65 deep** | `ErrIdentityUnavailable` — the depth bound is 64 |
| ⭐⭐ `Attributes` nested **9999 deep** | `ErrIdentityUnavailable` — ⚠ **BOTH earlier guards PASSED this**: `json.Marshal` alone (round 2) and an `Attributes`-only round trip (round 3, which admitted 9999 where the store admits 9998). This fixture is what proves the depth bound. |
| ⚠ a nested attribute map the caller mutates afterwards | the stored actor is **unaffected** — pins the deep copy |
| ⚠ `Attributes` with `int` and `time.Time` values | those types **survive** to the authorizer — pins that the copy is typed, not a JSON round trip |
| `Attributes` marshalling above the size bound | `ErrIdentityUnavailable` |
| a whole actor | returned whole, **Attributes included** |

⚠ **Two rows are regression guards in OPPOSITE directions** — round 1 refused any empty ID
(deleting the kiosk claimant), round 2 accepted everything (making the commonest middleware bug
fail-open). State both in comments so neither is "restored".
⚠ **The 9999-deep row is the load-bearing fixture, and it must be run against a REAL store.**
Round 2's guard used `json.Marshal` alone with only a `chan int` fixture — the arm that works.
Round 3 round-tripped `Attributes` alone and still admitted 9999 where the store admits 9998,
reproduced end-to-end on SQLite. ⇒ **the test asserts on `HumanTaskStore.Get` after an `Upsert`,
not on the guard's return value alone.** A guard tested only against itself is how this was missed
twice.

- [ ] **Step 2 — RED** (`undefined: resolveRequestActor`).
- [ ] **Step 3 — implement:**

```go
func resolveRequestActor(ctx context.Context, resolve RequestActorFunc, timeout time.Duration) (authz.Actor, error) {
	if resolve == nil {
		return authz.Actor{}, ErrUnauthenticated // a hand-built config fails CLOSED
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	a, err := resolve(ctx)
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return authz.Actor{}, err
	case err != nil:
		return authz.Actor{}, fmt.Errorf("%w: %w", ErrIdentityUnavailable, err)
	}
	// Refuse an actor carrying NO DIMENSION AT ALL. NOT an empty-ID check: the kiosk
	// claimant {ID:"", Roles:[...]} is deliberately legal (humantask/validate.go:24,
	// pinned at validate_test.go:45-47). What this rejects is the zero value, which is
	// what `actor, _ := authenticate(r)` produces on the error path.
	// Refuse the ZERO actor — and nothing else. This targets exactly one bug:
	// `actor, _ := authenticate(r)` yields Actor{} with a nil error, and the request
	// would proceed as though authenticated. It does NOT make the actor attributable
	// and does NOT close the attribute fail-open; see ADR-0189 Decision 3.
	// An empty string is not a role: strings.Split("", ",") returns [""], which is what
	// the canonical header middleware produces for a header-less request.
	if isZeroActor(a) {
		return authz.Actor{}, fmt.Errorf("%w: the resolver returned the zero actor", ErrUnauthenticated)
	}
	if len(a.Attributes) > 0 {
		// ⚠ DEPTH BOUND, not a round trip. encoding/json's ENCODER has no nesting
		// limit; its DECODER caps the WHOLE stored document at 10000 — and there is
		// no single stored shape (claim_actor marshals an Actor, candidates marshals
		// []Actor, the snapshot is deeper). Matching "the" shape is unfixable;
		// bounding depth is shape-independent and leaves 9936 levels of headroom.
		//
		// The same walk produces a TYPED deep copy. That is not decoration: Actor.Clone
		// is one level deep, so a consumer's nested map stays shared, and marshalling it
		// per request iterates a map they may be writing — measured as
		// "fatal error: concurrent map iteration and map write", which recover() does
		// NOT catch. Copying first means the marshal touches only our own copy.
		//
		// ⚠ The copy is typed rather than marshal/unmarshal because a JSON round trip
		// turns int into float64 and time.Time into string, changing what the expr
		// authorizer evaluates.
		safe, ok := deepCopyBounded(a.Attributes, maxActorAttributeDepth)
		if !ok {
			return authz.Actor{}, fmt.Errorf("%w: actor attributes nest deeper than %d",
				ErrIdentityUnavailable, maxActorAttributeDepth)
		}
		blob, mErr := json.Marshal(safe)
		if mErr != nil {
			return authz.Actor{}, fmt.Errorf("%w: actor attributes are not JSON-serialisable: %w",
				ErrIdentityUnavailable, mErr)
		}
		if len(blob) > maxActorAttributeBytes {
			return authz.Actor{}, fmt.Errorf("%w: actor attributes exceed %d bytes",
				ErrIdentityUnavailable, maxActorAttributeBytes)
		}
		a.Attributes = safe
	}
	return a, nil
}
```


- [ ] **Step 3b — the two helpers, same file:**

```go
const (
	// maxActorAttributeDepth bounds attribute nesting. encoding/json's decoder caps
	// the WHOLE stored document at 10000, and attributes are nested inside an Actor
	// inside a claim inside a task row — with THREE different stored shapes. Bounding
	// the attributes themselves is shape-independent; 64 leaves 9936 levels of
	// headroom, verified against wrapper depths of 1, 10, 100 and 1000.
	maxActorAttributeDepth = 64
	// maxActorAttributeBytes bounds the marshalled attributes. 16 KiB is generous for
	// a principal's profile and far below any body cap.
	maxActorAttributeBytes = 16 << 10
)

// isZeroActor reports whether a carries no identity at all. An empty string is not
// a role: strings.Split("", ",") returns [""] for a header-less request.
func isZeroActor(a authz.Actor) bool {
	if a.ID != "" || len(a.Attributes) > 0 {
		return false
	}
	for _, r := range a.Roles {
		if r != "" {
			return false
		}
	}
	return true
}

// deepCopyBounded returns a typed deep copy of the JSON-ish container shapes,
// reporting false if nesting exceeds budget. Bailing at the budget also makes it
// terminate on a cyclic structure. Non-container values are copied by value and
// therefore shared if they are reference types — documented on RequestActorFunc.
func deepCopyBounded(v any, budget int) (any, bool) {
	if budget < 0 {
		return nil, false
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			c, ok := deepCopyBounded(e, budget-1)
			if !ok {
				return nil, false
			}
			out[k] = c
		}
		return out, true
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			c, ok := deepCopyBounded(e, budget-1)
			if !ok {
				return nil, false
			}
			out[i] = c
		}
		return out, true
	default:
		return v, true
	}
}
```

⚠ `deepCopyBounded` returns `any`; the call site asserts `map[string]any`. Keep the signature
generic so the recursion handles nested slices without a second function.

- [ ] **Step 4 — GREEN.** - [ ] **Step 5 — commit (`--amend`).**

---

### Task 5: ⚠ THE BREAKING WAVE — INLINE in the controller, never a subagent

**Files (the member set — spec §2.6 has the full table):**
- `httpcore/dto.go` — delete `Actor`, `ClaimInput.Actor`, `CompleteInput.Actor`, `ReassignInput.By`
- `httpcore/endpoints.go` — three signatures + three resolve calls
- **9 production call sites**: `stdlib/groups.go:140,154,168` · `gin/groups.go:172,192,212` ·
  `fiber/groups.go:151,168,185`
- **14 httpcore test lines**: `dto_test.go:47,62,73,84,153` ·
  `endpoints_test.go:405,422,436,466,485,499,531,560,575`
- **5 httpcore JSON fixtures**: `dto_test.go:57,68,79,151,161` ⚠ **different lines from the
  assertions above** — round 1 conflated the two nets and missed these.

**Produces:** the final three endpoint signatures, each `(…, mapper, actor RequestActorFunc)`.

- [ ] **Step 1 — write the failing endpoint tests first**, including the headline case:

```go
"unauthenticated → 401 even though the body claimed a manager": {
	actor: nil,
	assert: func(t *testing.T, _ int, _ any, err error) {
		assert.ErrorIs(t, err, httpcore.ErrUnauthenticated)
	},
},
```
  and in `dto_test.go`, replace the removed-field assertions with the new contract:
```go
// A body still carrying the pre-ADR-0189 keys DECODES CLEANLY and is not read.
var in httpcore.ClaimInput
require.NoError(t, json.Unmarshal([]byte(`{"actor":{"id":"alice"}}`), &in))
assert.Equal(t, httpcore.ClaimInput{}, in)
```
- [ ] **Step 2 — RED:** `go test -count=1 -gcflags=-e ./transport/... > /tmp/t5.log 2>&1` — record
      the text.
- [ ] **Step 3 — apply.** `ClaimInput` becomes `struct{}` **with the godoc explaining why it is
      kept** (a pre-ADR-0189 body must still decode to a no-op).
- [ ] **Step 4 — verify the tree compiles:** `go build ./...` ⇒ 0; `go vet ./...` ⇒ 0 (it compiles
      Docker-only test packages, the cheap proof no hidden consumer exists); `httpcore` green.
- [ ] **Step 5 — confirm the PLANNED red and record it.** Expect EXIT=1 in
      `stdlib,gin,fiber,parity`. ⭐ Specifically confirm `stdlib`'s
      `TestTaskRoutes_Complete_ServiceError` and `..._Reassign_ServiceError` report **401 where
      they want 403** — round 1's prediction, already confirmed by the audit's execution lens.
      If they PASS, stop: the fail-closed claim needs re-deriving.
- [ ] **Step 6 — commit (`--amend`).**

---

### Task 6: the claim route accepts an ABSENT body

**Files:** `stdlib/groups.go` (use the existing `decodeOptionalRequestBody`, `body.go:156`);
`gin/groups.go` + a new optional-decode helper; `fiber/groups.go` + the same.

⚠ **Scoped to the CLAIM route only.** `CompleteInput`/`ReassignInput` keep required content; a
group-wide helper would make `POST /instances` accept an empty body and fail later with a worse
error.

- [ ] **Step 1 — failing test, per adapter:** POST the claim route with **no body at all** ⇒ 200
      (with an authenticated resolver). **RED today: `400 {"error":"bad_request","message":
      "workflow-httpcore: bad input: EOF"}`** — measured, spec §2.7.
- [ ] **Step 2 — RED.** - [ ] **Step 3 — implement.** ⚠ Only `stdlib` has the helper; gin and
      fiber need an equivalent that treats an absent/empty body as the zero value but still
      honours the size cap.
- [ ] **Step 4 — GREEN.** - [ ] **Step 5 — commit (`--amend`).**

---

### Task 7: the six adapter option aliases

**Files:** `stdlib/options.go`, `gin/options.go`, `fiber/options.go`.
**Produces:** `WithRequestActor` and `WithRequestActorTimeout` in each — **six aliases, two per
adapter.** ⚠ Round 2's counting lens found the round-2 task titled "six" while producing nine,
because it also carried `WithAdminRoles`; that option left with the re-cut. Six is now correct —
verify by counting the funcs you actually add.

⚠ Aliases are **required, not cosmetic**: `R` appears only in the result type, so
`httpcore.WithRequestActor(fn)` does not compile.
⚠ **Do not infer the set from one file, and do not trust this sentence either — it has been wrong
in three consecutive rounds.** Run `grep -n "^func With" transport/http/{stdlib,gin,fiber}/options.go`
and read what is actually there before adding anything.
⚠ In **fiber's** godoc the middleware sentence must say `c.SetContext`, never `c.Locals`; in
**gin's**, `gc.Request = gc.Request.WithContext(...)`, never `gc.Set`.

- [ ] Write, `go build ./transport/...` ⇒ 0, commit (`--amend`).

---

### Tasks 8 / 9 / 10: per-adapter test migration — **DISPATCH IN PARALLEL**

#### Task 8: `stdlib` — 6 pins (5 runtime + 1 status change)
`errors_test.go:155,187` · `stdlib_test.go:471` · `coverage_test.go:92,126`
⚠ **plus `coverage_test.go:148`** — an erroring body reader on the claim route asserts **400** and
becomes **401** under Task 6's optional decode. Invisible to BOTH of §2.6's original nets (it names
no actor field and no `"actor"` literal); it is §2.6's *third net*. Rewrite it to assert 401, and
add a sibling asserting an **oversize** claim body still returns **413** — `stdlib`'s
`decodeOptionalRequestBody` preserves it and that must stay true.

- [ ] Delete the `"actor"`/`"by"` keys; mount with `stdlib.WithRequestActor(...)` where a specific
      identity is needed.
- [ ] ⭐ **Rewrite, do not recompile, the two 403 pins** (assertions at `:158`, `:190`). Each must
      install a resolver returning the `viewer` actor so the 403 is genuinely exercised.
- [ ] **MUTATION on each:** remove the resolver from one mount ⇒ expect
      `want 403 complete forbidden, got 401`; `cp` back; `diff`.
- [ ] Add the seam test: middleware authenticates a **viewer**, body claims a **manager**, expect
      **403** — the middleware's actor wins. Plus a bare-mount **401**.

#### Task 9: `gin` — 8 pins (7 runtime + 1 status change)
`gin_test.go:413,421,443,453` · `gin_coverage_test.go:192,218,244`
⚠ **plus `gin_coverage_test.go:184`** (`TestTaskRoutes_Claim_BadJSON`, body `not-json`) — asserts
**400**, becomes **401**. Same third net. Rewrite it, and add the 413-preserved sibling: gin's
optional-decode helper does not exist yet and must not swallow the oversize error.

⚠ `gin_coverage_test.go:244` asserts **404** on a nonexistent token. **gin has no 403 assertion at
all** — do not go hunting for one; ADR-0185 claimed there was one and was wrong.
⚠ **The 401 now precedes the task lookup**, so an unauthenticated request for a nonexistent task
returns **401, not 404**. Expect that assertion to move and pin the new behaviour deliberately.

- [ ] Delete the keys; mount with `gin.WithRequestActor(...)`.
- [ ] Seam test using gin's **working** idiom: `gc.Request = gc.Request.WithContext(...)`.
- [ ] ⭐ **Trap test:** middleware using **`gc.Set`** — gin's canonical idiom — ⇒ **401**.
      Measured: `gc.Set` does not reach `gc.Request.Context()`. Round 1 called gin "standard".

#### Task 10: `fiber` — 5 runtime pins
⚠ fiber has **no** bad-JSON claim test, so it contributes nothing to the third net — but it still
needs the 413-preserved test, since its optional-decode helper is also new.
`fiber_test.go:563,585,592,615,624`

- [ ] Delete the keys; mount with `fiber.WithRequestActor(...)`.
- [ ] Seam test using `c.SetContext`.
- [ ] ⭐ **Trap test:** middleware using **`c.Locals`** ⇒ **401**, pinning §2.8 as a contract.

Each: `go test -count=1 ./transport/http/<pkg>/... ⇒ EXIT=0`, report the diff, do not commit.

---

### Task 11: `parity`, the `service` comments, and ADR-0147

**Files:** `transport/http/parity/parity_test.go:518` · `service/instance_test.go:1090,1128` ·
`docs/adr/0147-humantask-audit-model.md`.

- [ ] Remove the `"actor"` key at `parity_test.go:518`; add a parity case asserting all three
      adapters answer an unauthenticated claim **identically** — 401, same
      `ErrorBody.Error == "unauthenticated"`.
- [ ] ⚠ **`service/instance_test.go:1090,1128`** — both comments say the fixture is built through
      the Go API *"because `httpcore.Actor` is `{id, roles}` only … (ADR-0147 amendment #5)"*.
      That type no longer exists and the limitation is gone. Correct both. **Invisible to both
      round-1 nets; found by the counting lens.**
- [ ] ⚠ **Amend ADR-0147 amendment #5's first caveat in place.** It asserts *"over HTTP those two
      slots can never carry attributes"* — falsified here. Annotate, do not delete.
- [ ] `go test -count=1 ./transport/http/parity/... ./service/... ⇒ EXIT=0`; commit (`--amend`).

---

### Task 12: examples

**Files:** create `examples/authenticated_tasks/main.go`; modify
`examples/{production,sqlite,mysql}_wiring/main.go`.

- [ ] **Step 1** — `authenticated_tasks`: middleware verifies a credential, calls
      `authz.ContextWithActor`, claims a task over HTTP, for **all three adapters**, with fiber
      using `c.SetContext` and gin using `gc.Request.WithContext` — each carrying a comment naming
      the canonical-but-broken idiom. ⚠ The credential check must be a real function of a real
      secret; an example named for authentication must not teach trusting a header.
- [ ] **Step 2** — the three wiring mains get the constant `demo-user` actor, commented DEMO ONLY.
      ⚠ `production_wiring` already passes `httpcore.WithMeterProvider[...]` — **append**.
      ⚠ These mains make the task routes answer **200** where they answer **403** today — strictly
      more open.
      ⚠ **Do NOT touch `production_wiring:273-275`'s `AdminRoutes.Customize(adminMux, …)`.** It
      mounts admin routes behind a fail-closed `requireAdminToken`, which is ADR-0095's
      admin-by-composition posture working as designed. Round 2's revision broke it by
      authenticating admin routes; that decision is gone. A round-1 plan sentence claimed the
      mains "must not mount `AdminRoutes`" — **that was false about existing code.**
- [ ] **Step 3** — `go build ./examples/...` ⇒ 0, then **run each main** and confirm a clean start;
      `curl` the claim route in `authenticated_tasks` for **200** and a bare mount for **401**.
      This closes spec §2.5's labelled `ASSUMPTION (unverified)` — replace it with the output.
- [ ] **Step 4** — commit (`--amend`).

---

### Task 13: documentation and the delivery gate

- [ ] **`SECURITY.md`** — the middleware pattern for all three frameworks, the 401/503 contract,
      the `c.SetContext`/`gc.Request.WithContext` warnings, and a "Scope notes for embedders"
      entry stating — ⛔ **in these terms, because the opposite sentence was prescribed while the
      removed decision existed** — that `InstanceRoutes`, `MessageRoutes` and `AdminRoutes`
      **authenticate NOTHING**: `POST /instances`, `/signals` and `/messages` are state-changing
      and open to any caller, and the consumer must put their own guard in front. Only the three
      human-task verbs are covered by this record. ⚠ For `AdminRoutes` say that ADR-0095's
      admin-by-composition posture stands and point at `examples/production_wiring`'s
      `requireAdminToken`.
- [ ] **`CHANGELOG.md`** — mirror ADR-0186's entry shape (see `CHANGELOG.md:20-38`): what broke,
      what to add, a code snippet for the fix.
- [ ] **`STABILITY.md`** — a subsection beside `### Request body caps (ADR-0186, pre-v0.1.0)`.
- [ ] ⚠ **`docs/plans/HANDOVER.md` and ADR-0185's banner** — both still route a fresh session into
      the deleted D1 design. Update in this bundle.
- [ ] **Premise sweep of the diff's own comments** — every *all/none/only/every/never/always* and
      every count added by this bundle, re-verified as if it stood alone. ⛔ Especially: nothing may
      say every route has an "identified principal".
- [ ] ⚠ **The doc sweep needs a wider net than `grep '"actor"'`.** Round 2's counting and
      failure-modes lenses both found live doc sites that net cannot reach — including
      `docs/adr/0146:12`'s `httpcore.CompleteInput{Actor, Output}` and the README's headline
      `stdlib.Mount(mux, svc)`. Run all of:
```bash
grep -rn '"actor"\|"by"' README.md docs/ examples/ SECURITY.md
grep -rn 'httpcore\.Actor\|ClaimInput{\|CompleteInput{\|ReassignInput{' README.md docs/ examples/
grep -rn 'ClaimTask(\|CompleteTask(\|ReassignTask(' README.md docs/ examples/
```
      and fix every hit that documents the removed field or the old signature.
- [ ] **Verification, in order:**

```bash
docker info > /dev/null 2>&1 && echo DOCKER_UP || echo DOCKER_DOWN
go test -race -coverprofile=cover.out ./... > /tmp/cov.log 2>&1; echo "EXIT=$?"
scripts/coverage.sh cover.out          # ⚠ reports only; its exit code proves nothing
go test ./... > /tmp/all.log 2>&1; echo "EXIT=$?"
command -v golangci-lint && golangci-lint run ./... > /tmp/lint.log 2>&1; echo "LINT_EXIT=$?"
```
      ⚠ Docker down ⇒ say so and label the container-free subset **partial**.
      ⚠ `golangci-lint run ./transport/...` is **not** `run ./...`.
- [ ] **Delivery Gate** — hand to the owner for `/code-review` and `/security-review` (agents
      cannot invoke them). Fold findings via `--amend`. ⚠ **A review finding is a claim needing
      execution** — reproduce before fixing; if one is a false positive, say so with the
      measurement rather than skipping it silently.
- [ ] Update this plan's `▶ Progress` block and the auto-memory topic file; merge `--no-ff`, push,
      delete the branch.

---

## Self-review against the spec

| spec | task |
|---|---|
| §3.1 seam + one-level clone honesty | 1 |
| §3.2 DTO removal, signatures, Attributes flow | 5 |
| §3.3 refusal rules; **empty ID PASSES** | 4 |
| §3.3 resolver timeout | 3 |
| §3.4 arms first + **arms co-matching each other** | 2 |

| §3.6 group authentication, Health exempt, placement asymmetry | 8, 9–11 |
| §3.7 opt-in admin role gate | 8, 9–11 |
| §3.3 the ZERO-actor rule + resolver timeout | 3, 4 |
| §3.5 depth bound, size bound, deep copy | 4 |
| §3.6 optional claim body | 6 |
| §3.6 ordering residual documented | 13 (SECURITY.md) |
| §3.7 examples, docs, ADR-0147, HANDOVER, ADR-0185 banner | 11, 12, 13 |
| §2.6 member set — 23 compile + 23 runtime + 2 comments + **2 status changes** = **50** | 5 (28) · 8–10 (**19**) · 11 (3) |
| §5 rows 1–17 | 1, 2, 3, 4, 5, 6, 8–10, 11 |
| survivor×survivor S1–S6 (`audit2-0189-removal-grid.md`) | 3, 4, 6 |

**Gaps found and closed during this self-review:** §5 row 7 (attributes reach
`service.ClaimTaskRequest`) is asserted in Task 4 **on the object the endpoint builds**, not on a
view — round 1 asserted it on the wrong object and its self-review wrongly claimed it closed.
§2.6's five `dto_test.go` **fixture** lines had no owning task in round 1; they are now explicit
in Task 5.

**Changed by the round-2 re-cut:** Tasks 8–11 of the round-2 plan (route-group authentication and
the admin role gate) are **deleted**, not deferred within this plan — they become **ADR-0190**
with its own audit. ⚠ Round 2's counting lens showed those tasks would have added ~186 failing
assertions across 13 test files, **7 of which no bundle document named**; the re-cut removes all
of them, and §2.6's member set reverts to the 48 it was derived for. ⛔ **That reversion is a
claim and Task 5 Step 4 must re-execute it** — assuming it is precisely what produced round 2's
Critical.
