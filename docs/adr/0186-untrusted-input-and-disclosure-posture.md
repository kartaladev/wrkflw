# 186. The library's untrusted-input and disclosure posture

> ## ⚠ REVISED 2026-08-21 after its first standalone audit. NOT YET RE-AUDITED.
>
> Audit #1 (four Opus lenses — execution / failure-modes / counting / **interaction**):
> **63 findings, 33 Critical**. Three of the four lenses independently concluded the
> *decisions* were largely sound and the **plan** was where the bundle broke —
> *"six Criticals share one root cause: a decision stated in the ADR whose realisation
> lands in a package no phase assigns it to"*. Nothing needed a design increment.
> Adjudication: `docs/plans/sweep-evidence/audit-0186-adjudication.md`.
>
> **What this revision changed, in one line each:**
> 1. ⭐ **The element bound moved from EVALUATION to ADMISSION.** It is now a `service`
>    bound applied where a caller-supplied variable map enters, beside the byte bound —
>    not an `expreval`/`runtime` option. This one move closed **seven** findings
>    (I-2, I-3, I-9, I-14, E1, E2, F14) and made two more moot.
> 2. **D2 no longer touches `internal/expreval`, `runtime` or `engine` at all.** The
>    once-per-env mandate is deleted: it was unimplementable *and* unnecessary — the cost
>    figure that forced it compared a worst case against a typical case.
> 3. **D5's "value-free" 400 rendering was itself not value-free** and is replaced by a
>    schema-location rendering, executed.
> 4. **D3's `WithAllowedHosts` was unimplementable** in a `net.Dialer.Control` hook and
>    the SSRF deny-list was a sample presented as a class. Both replaced.
> 5. **Four enumerations were wrong** — decode sites (36/39, not 39/39), read paths (11,
>    not 8), plaintext columns (**12**, not 2 — and not the audit's "six" either),
>    sentinels in the banner (8, not 9).
>
> Evidence for every new claim: `docs/specs/2026-08-21-adr-0186-premise-evidence.md`.
> ⚠ **A bundle whose decisions changed has not been audited.** Not an input to implementation.

- Status: **Proposed** (revised; pending re-audit as a standalone delivery)
- Date: 2026-08-20, revised 2026-08-21 (draft → B3 revision → standalone re-cut → this fold)
- Relates to: ADR-0003 / ADR-0049 / ADR-0056 (evaluator purity, deterministic replay, the
  opt-in timeout — **not amended**, and this revision touches them even less than the draft
  did, see Decision 2), ADR-0081, ADR-0095, ADR-0144, ADR-0145/0147,
  **ADR-0146 / ADR-0152 / ADR-0183** (the three records whose actionable-400 rationale
  Decision 5 must not destroy — added in this revision, they were missing).
  ⚠ **ADR-0185 (identity) is a SEPARATE, LATER delivery** — this record must not depend on
  any symbol it introduces, and an audit lens confirmed no such symbol appears here.
- Backlog: 54 (partially — see D4), 65, 98, 99, 104, and the posture for 100 / 101

## Context

ADR-0185 will answer *who the actor is*; it is a **separate, later delivery** and nothing
here depends on it. This record answers the other half: **what the library accepts from a
caller, and what it hands back**. Six verified findings, plus two where the honest decision
is to decide a posture and defer the mechanism.

### 1. No body cap anywhere — and three of the sites cannot report one

Re-counted, non-test: `transport/http/stdlib` has **13** `json.NewDecoder` sites, `gin` **13**
`ShouldBindJSON`, `fiber` **13** `c.Bind().JSON`, `httpcore` **0** — **39 across three
idioms**, all in each package's `groups.go`. `grep -rnE "MaxBytesReader|BodyLimit" transport/`
exits 1.

⚠ **Note the bare `|`.** Two earlier rounds recorded this grep as `"MaxBytesReader\|BodyLimit"`
under `-E`, where `\|` is a **literal** pipe — a command that returns 0 for *any* repository,
i.e. evidence that could not falsify the claim it was offered for. Re-run correctly, the claim
holds.

⚠⚠ **But "every one of the 39 already wraps in `ErrBadInput`" is FALSE — 36 do; 3 discard the
decode error entirely.** `stdlib/groups.go:238`, `gin/groups.go:265`, `fiber/groups.go:255` are
`_ = <decode>(&in)`, all three on the same optional-body route
`POST /admin/instances/{id}/incidents/{incidentID}/resolve`. At those three there is **no
error path to convert**: a body cap installed there fails, the failure is assigned to `_`, the
handler proceeds with a zero-valued input and returns **2xx**. That is the worst outcome for a
security control — silently unenforced — and it is the exact hazard the "bare sentinel" mandate
below exists to prevent, one route over. Evidence §4.1.

Fiber's 4 MiB rejection is `fiber.DefaultBodyLimit` (`fiber/v3@v3.4.0/app.go:585`, applied in
`New()` at `:710`), i.e. the framework's, not ours. There is no process-variable size limit
either.

### 2. Expression cost is unbounded in its input

⚠ **The audit's `MaxNodes` fix is inverted, and this was executed** — the vendor says so in its
own godoc (`expr@v1.17.8/expr.go:221`: *"If MaxNodes is set to 0, the node budget check is
disabled"*). `expr.MaxNodes(0)` is what *disables* the check; never calling it leaves
`DefaultMaxNodes = 1e4` **active**, and a 20 000-node expression already fails to compile. The
unmetered axis is **caller-supplied array size**.

Measured with an 80-byte predicate against `vars.items` of *n* JSON integers:
25 ms → 98 ms → 391 ms → 1.563 s at n = 1 000 / 2 000 / 4 000 / 8 000. Clean O(n²), invisible to
any node cap. Two audit lenses reproduced the ladder independently and **measured n = 10 000
directly**: **2.458 s** (extrapolation predicted 2.442 s — 0.65 % error) and **1.92 s** on a
faster run of the same shape. ⚠ Both plain-mode; `-race` is several times slower.

⚠ Two evaluator **surfaces**, not one: `authz`'s is `expreval.New()`, i.e. `DefaultTimeout = 5 s`
**is** enabled; only the engine's gateway evaluator (`engine/conditions.go:43`,
`expreval.WithTimeout(0)`) is wall-clock unbounded, and that is a deliberate ADR-0003/0049/0056
trade, not an oversight. ⚠⚠ **Neither ABAC evaluator has an options seam** —
`authz/authz.go:23` is a package-level global and `internal/authz/casbin/authorizer.go:30` is
hard-coded in a constructor. Any mitigation attached to the *evaluator* therefore reaches one
surface of two. This is a decisive input to Decision 2.

### 3. `httpcall` is an SSRF primitive reachable from process variables

`WithURLExpr` (`action/httpcall/httpcall.go:125-134`) calls raw `expr.Compile`. The default
client is `&http.Client{Timeout: 30s}` with no `CheckRedirect` and no allowlist;
`grep -rnE "CheckRedirect|expreval" action/httpcall/` exits 1. The hazard **is** documented in
`WithURLExpr`'s godoc, whose last line reads *"…without an allowlist or a restricted
`*http.Client` transport (SSRF risk)"* — which makes this a posture question (*should the
library ship a safe default?*) rather than an oversight.

⚠ Two facts the draft missed, both load-bearing for Decision 3:
- `WithHTTPClient` (`httpcall.go:153`) assigns the **same** `h.client` field a restricted
  transport would, and `NewHTTPCall` applies options in registration order. A restriction
  written as an option is order-dependent and one ordering silently drops it.
- `action/httpcall.ErrBodyTooLarge` (`httpcall.go:94`) is **already exported**, bounds the
  *outbound response* body at a default **10 MiB**, and correctly classifies **500**. Decision 1
  must not mint a second sentinel with that name. Evidence §4.5.

### 4. The instance read path aliases, and discloses — on eleven paths, in five fields

`transport/http/httpcore/view.go:31` assigns `Variables: st.Variables` — an alias of the
caller's map, not a copy.

⚠ **The draft's consequence claim was withdrawn, and the withdrawal was half wrong.** It said
*"anything mutating the view mutates instance state"*; the withdrawal argued the read path hands
out a clone. **Executed: the clone is SHALLOW.** `engine/step_state.go:325` is
`copyVars = maps.Clone`, and `State.Clone()` → `cloneState` → `copyVars` is the whole chain, so
deleting a **nested** key from the clone deletes it from the source:

```
after nested delete on the CLONE, SOURCE applicant = map[string]interface{}{"name":"ada"}
top-level delete isolation: source still has 'tags' = true
```

⇒ the withdrawn claim is **false for top-level values and TRUE for nested ones**, and
`persistence/caching_instance_store.go:72`'s godoc — *"cloneInstanceEntry **deep-copies** an
entry"* — is a false comment in shipped code. Evidence §3.

**The read surface is larger than the draft's count in two dimensions.**

*Paths — eleven, not eight.* Six `mapInstance` call sites (`endpoints.go:42,52,94,124,140,155`),
**three admin endpoints that call `NewInstanceView` directly and take no mapper at all**
(`admin_endpoints.go:111` `ResolveIncident`, `:121` `CancelInstance`, `:514`
`ResolveCompensationStall`), and two mapper-less non-admin endpoints (`GetInstanceSnapshot`
`endpoints.go:60`, `GetActionableView` `:72`). `AdminListInstances` (`:81-95`) is genuinely
clean — and it is the *one* admin endpoint of four the draft checked before generalising.
Evidence §4.2.

*Fields — five, not one.* `GetInstanceSnapshot` returns the raw `service.ProcessInstance`, whose
`instanceJSON` projection (`service/instance.go:117-144`) carries `variables` (`:125`, assigned
`:344`), `tokens[].payload`, `incidents[].error` — the raw `err.Error()` of a failed action, the
very value `ClassifyError` blanks at 5xx — `tasks[]` (actor id/roles/attributes verbatim,
ADR-0147), and `definition`: the **whole template**, embedded per ADR-0144, i.e. every gateway
and flow condition **expression source** in the process. On a **non-admin** route.

⚠⚠ **And `GetActionableView` carries no task variables at all.** Executed:
`runtime/view.ActionableTask` declares six fields — `TaskID, NodeID, State, Claim, Candidates,
AllowedActions` — **no `Vars`**, and `NewActionableView` never reads `t.Vars` (it already clones,
`instance_actionable.go:88`). The draft's *"whose `HumanTask.Vars` is the per-task variable
snapshot"* was never executed and is false. What that route **does** disclose is
`allowed_actions[].condition` — the sequence-flow expression source, verbatim — and
`candidates[]`. Evidence §4.3.

There is no redaction hook anywhere on `CustomizeConfig`, and the `SECURITY:` caveat exists at
exactly **three** non-test sites (`stdlib/groups.go:189`, `gin:204`, `fiber:209`), all on the
**admin** group; the instance and task groups carry none.

### 5. Five 4xx classes echo `err.Error()` verbatim

`transport/http/httpcore/errors.go` at 404 (`:31`), 403 (`:33`), 409 (`:35`), 400 (`:50`),
422 (`:56`); 500 (`:58`) correctly blanks. The switch has exactly six arms and the set is closed.

Executed: a 403 produced by an ABAC evaluation error returns the predicate source **twice** —
once from `%q` in `internal/expreval/expreval.go:135`, once inside expr's own snippet — carrying
whatever process-variable and actor-attribute names the deployment's policy names. A **bare**
deny returns only `"workflow-authz: not authorized"` and leaks nothing, so the leak is confined
to the eval-error arm of 403.

**And 400 leaks too.** Executed against the repo's own jsonschema strategy with input
`{"ssn":"123-45-6789"}`: `- at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'` — the
submitted value, verbatim, for exactly the constraint used to shape national-ID / card / account
fields. A sibling `maxLength` leaf reports `got 11, want 3`, disclosing a length.

⚠ Three corrections this revision folds, each of which changes what Decision 5 can do:

- **The typed error does not reach the transport.** `runtime/validation/gate.go:45` is
  `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` — `%s`, so `errors.As(err,
  **jsonschema.ValidationError)` is **false** at `ClassifyError` (`true` before the gate, `false`
  after; reproduced by two lenses). The rendering must live in `runtime/validation`.
- **The prescribed replacement rendering was itself not value-free.** It named
  `InstanceLocation`, which is *instance*-derived: a card number submitted as an object **key**
  renders as `at '/4111-1111-1111-1111': violates type`. Executed. The replacement is in
  Decision 5 and was executed against the real in-repo strategy. Evidence §2.
- **`avro` leaks too**, and for a different reason than the bundle assumed. It carries no
  structured leaf (goavro exports no error type) — but it **echoes the submitted value verbatim**
  on the enum path and a **length** on the fixed path. The bundle routed it to static text for
  the right outcome and the wrong stated reason.

The 400 arm is far wider than one strategy. Re-derived: it matches **eight sentinels across five
`errors.Is` groups** (`kernel.ErrBadCursor`, `kernel.ErrBadArmedTimerCursor`, `ErrBadInput`,
`validation.ErrInvalidInput`, `engine.ErrInvalidOutcome`, `engine.ErrOutcomeRequired`,
`engine.ErrEmptyTriggerKey`, `engine.ErrEmptyReassignTarget`), all rendered by the single
`Message: err.Error()` at `:50`. ⚠ **Eight, not nine** — an earlier banner of this record said
nine, inherited from a re-audit summary and restated without checking. And
`validation.ErrInvalidInput` wraps **four** strategies — `jsonschema`, `expr`, `avro`,
`callback` — of which only `jsonschema` yields a structured leaf; `expr` echoes the predicate
source (`definition/model/validate/expr/expr.go:64,68`, `%q` on `v.source[i]`) and `callback`
emits whatever a consumer's validator writes.

⚠⚠ **What the 400 arm must STILL DO.** Four of the seven non-validation sentinels echo **no**
caller value at all, and the in-code rationale for three of them is written in the very switch
this record edits: `errors.go:38-41` (ADR-0146) — *"Without these arms they fall to the 500
default, which hides an actionable 4xx behind an empty body"*; `:43-46` (ADR-0152) — *"a
malformed request the caller can fix by supplying the id"*; `:47-49` (ADR-0183) — *"a required
field the caller omitted and can supply"*. And `ErrBadInput` is the **highest-volume 400 by a
wide margin** (36 decode wraps plus the whole `httpcore.Validate` DTO layer, i.e. every POST/PUT
body on all 26 routes). Executed: `httpcore.Validate`'s message is
`Key: 'DTO.name' Error:Field validation for 'name' failed on the 'max' tag` — **value-free, and
value-free even for a length constraint**. Evidence §1. Blanking it wholesale would be pure
information loss with zero disclosure benefit, and would silently retire three ADRs.

### 6. Nothing is protected at rest, and nothing is tamper-evident

`grep -rniE "encrypt|redact"` over `persistence/`, `internal/persistence/` and `engine/`
(non-test) exits 1. `wrkflw_journal` is **6** columns — no hash, no prev-hash, no signature.
`engine.NodeVisit` carries no actor field, by ADR-0145 design; the actor's real homes are the
task record and the journal's `trigger` payload.

⚠ **The plaintext set is twelve columns across seven tables, in three dialects — not two, and
not the audit's "at least six" either.** Beyond `wrkflw_instances.snapshot` and
`wrkflw_journal.trigger`: `wrkflw_outbox.{payload,last_error}`,
`wrkflw_definitions.definition`, `wrkflw_human_task.{vars,candidates,eligibility,claim_actor,
completion_actor,note}`, `wrkflw_call_links.{output,error}`, `wrkflw_timers.trigger_payload`,
`wrkflw_chain_links.start_vars`. Evidence §4.4. This matters more than any other enumeration in
the record, because **D6's deliverable IS the enumeration**: a consumer who reads `SECURITY.md`,
encrypts the two named columns and leaves the human-task variable snapshot in the clear has been
harmed by our documentation.

---

The system-level shape matters: there is **no definition-deploy route** among the 26 HTTP routes
(9 non-admin + 15 admin + 2 health), so expression *source* (2, 3) is author-supplied, not
anonymous-caller-supplied. That is what keeps 2 and 3 serious rather than critical — and it is a
property of today's route table, not a guarantee.

## Decision

### 1. Request bodies are capped by default, and oversize has ONE status

- `httpcore.CustomizeConfig.MaxBodyBytes`, default **1 MiB**, `0` = unbounded (the explicit
  opt-out), honoured by every adapter through its own idiom: `http.MaxBytesReader` for stdlib;
  the same wrapper applied to `c.Request.Body` *before* `ShouldBindJSON` for gin; and a
  `len(c.BodyRaw())` pre-check for fiber, because `BodyLimit` is a `fiber.Config` field set on
  `fiber.New` — the **app**, which a mounted route group does not own.
- **`MaxBodyBytes` means WIRE bytes, in all three adapters.** ⚠ This is a folded correction, and
  the mechanism turns on it: `c.Body()` **decompresses** (`fiber/v3@v3.4.0/req.go:146`), so a
  63.7 KiB gzip expanding to 64 MiB makes `len(c.Body())` return **33** — the string
  `"body size exceeds the given limit"`, which fiber's own bounded gunzip wrote — and the
  prescribed pre-check passes it through to a **400**, not a 413, on precisely the amplification
  case it exists for. Executed. `c.BodyRaw()` (`req.go:92-96`) is the un-decompressed wire body
  with no response side effect, and it is what makes the three adapters agree: `net/http` does
  not auto-decompress request bodies, so stdlib and gin already see wire bytes.
  - A **second**, separately-named check on the decompressed size is deliberately **out of
    scope** and recorded as a residual: fiber's decompression ceiling is `app.config.BodyLimit`
    (default 4 MiB), which the mounted group does not own.
- ⚠ Conceded plainly: fiber's pre-check is a **rejection, not a prevention** — the body is
  already buffered by the time it runs.

**Oversize is a 413, and the mapping is named rather than assumed.** `http.MaxBytesReader` does
not produce a status — it makes the next `Read` fail, surfacing inside the decoder as an error
the 400 arm would classify. Executed: both stdlib and gin surface the **bare
`*http.MaxBytesError`** (`errors.As` is true through both `json.Decoder.Decode` and
`ShouldBindJSON`); fiber's pre-check produces a home-grown error. **Two shapes, not three** — the
draft's *"gin wraps it again"* was unexecuted and is false. Therefore:

- a new **`httpcore.ErrRequestBodyTooLarge`** sentinel. ⚠ **Not `ErrBodyTooLarge`** —
  `action/httpcall.ErrBodyTooLarge` already exists, means an *outbound response* exceeded
  10 MiB, and is correctly a **500**. Two identically-named exported sentinels with opposite
  meanings, minted in one commit across two packages two phases edit, is a support hazard; and
  `httpcall`'s chosen 10 MiB is unacknowledged prior art for the 1 MiB judgement call below.
- each adapter converts its own oversize signal into that sentinel **before** calling
  `ClassifyError` (`errors.As` for stdlib and gin; the pre-check for fiber);
- `ClassifyError` maps the sentinel → **413**;
- a test asserts `httpcall.ErrBodyTooLarge` still classifies **500**.

⚠⚠ **And the oversize error must NOT carry `ErrBadInput`, or it ships as 400.** Executed:
`ClassifyError` on an error wrapping both sentinels returns `400 {bad_request}`. Every one of
the **36 propagating** decode sites double-wraps —
`writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))` — and the switch is
**ordered**: 404 `:28`, 403 `:32`, 409 `:34`, **400 `:36-50`**, 422 `:51`, default 500.
Therefore:

- the adapter returns the **bare** `ErrRequestBodyTooLarge` on the oversize path; the
  `ErrBadInput` wrap is for **decode** failures only;
- the **413 arm is placed before the 400 arm**, with a comment saying why;
- ⚠ **the three discarding sites get the opposite instruction.** At `stdlib/groups.go:238`,
  `gin/groups.go:265` and `fiber/groups.go:255` there is no error to convert. Those sites must
  **gain** an error path that distinguishes *body absent / EOF* (keep ignoring — the body is
  genuinely optional) from *body present but oversize*:
  ```go
  if err := decode(&in); err != nil && errors.As(err, new(*http.MaxBytesError)) {
      writeErr(cfg, …, httpcore.ErrRequestBodyTooLarge)   // bare → 413
      return
  }
  // any other decode error stays ignored: the body is optional
  ```
  ⚠ These three are on an **admin** route, and ADR-0095 keeps admin routes out of `Mount`, so
  the parity suite structurally cannot see them. The per-adapter test must name the route.

**Migration and discoverability.** `MaxBodyBytes = 0` is the opt-out, and a
`wrkflw_rest_request_body_bytes` histogram joins the existing transport instrumentation so a
consumer can measure their real distribution *before* the cap bites. A separate observe-only
soft-limit option was considered and is **not** shipped — `0` plus the histogram covers the
same need at a fraction of the surface.

⚠ **Fiber above `fiber.DefaultBodyLimit` is a known divergence, not a gap.** Executed: at 8 MiB
the route group is **never reached** — the client receives fasthttp's `text/plain`
`Request Entity Too Large`, with no `ErrorBody`, no correlation id and no log line. So
`MaxBodyBytes > 4 MiB` is silently ineffective on fiber unless the consumer also raises
`fiber.Config.BodyLimit`, which belongs to `fiber.New` and which we cannot read from a
`fiber.Router`. Fiber's `Mount` therefore **logs a WARN at mount time** when
`MaxBodyBytes > fiber.DefaultBodyLimit`, and `SECURITY.md` records the divergence. We do not
refuse, because we cannot distinguish a consumer who already raised their app limit from one who
did not.

### 2. Variable maps are bounded at ADMISSION, in bytes AND elements. Nothing in the evaluator changes.

⭐ **This is the revision's central change, and it replaces both halves of the draft's Decision
2.** The draft bounded evaluation *input* by giving `internal/expreval` a
`WithMaxEnvElements(n)` option plumbed by `runtime.WithMaxEvalElements(n)`. That mechanism is
withdrawn entirely. What survives is the *number* and its derivation; what changes is **where the
check runs**.

**Why the evaluator is the wrong place — four executed reasons, not one:**

1. **There is no carrier for a per-env count.** `engine.ConditionEvaluator`'s three methods take
   `(code string, env map[string]any)` and nothing else, and the draft froze those signatures.
   A Go map is not comparable, so it cannot key a memo table; the engine **mutates the live map
   in place** between evaluations (`engine/step_triggers.go:515,:517` inject `_errorMessage` /
   `_errorAttempts`), so any cache is stale by construction. The draft's *"computed when the
   variable map changes and carried alongside it"* had nowhere to land.
2. **The only remaining identity handle is unsound.** Keying on
   `reflect.ValueOf(env).Pointer()` was measured: 200 000 distinct maps produced **82 473**
   addresses — **59 % collided** — and a memoised `count=2` was observed backing a map of real
   bounded count **50 001**. The bound would fail **open**, admitting exactly the env it exists
   to refuse.
3. **The cost premise that forced the mandate was an invalid comparison, and it was the
   controller's own analytical error.** *"Counting per evaluation would be 20–60× worse than the
   cost the decision refused"* compared a **worst-case bound cost** (10 000 elements) against a
   **typical-case ctx cost** (3 scalars). Two lenses measured it like-for-like and independently:

   | | execution lens | failure-modes lens |
   |---|---|---|
   | typical env, counting | +74 ns/op, **0 allocs** | 64 ns |
   | the `ctx` it refused | 866 ns, +6 allocs | 820 ns |
   | verdict | **~12× cheaper** | **~13× cheaper** |
   | crossover | ~500 elements | — |
   | worst case in context | 16.5 µs replaces a **2.458 s** evaluation | 0.0009 % of a 1.92 s evaluation |

   So the mandate was unnecessary as well as unimplementable, and the ADR compared the cost of
   *rejecting* a hostile env against the cost of *evaluating* a benign one.
4. ⭐ **An evaluator-side bound reaches one of the two surfaces this record's own Context §2
   enumerates.** Neither ABAC evaluator has an options seam. An **admission** bound reaches
   both, plus `action/httpcall`'s URL expression and `action/transform`'s — because all four
   read the same admitted variable map. The evaluator-side design could not have reached any of
   the last three at all: an `Action` receives `(ctx, in)` and holds no reference to the driver.

**The decision:**

- `service.WithMaxVariableBytes(n int64)`, default **256 KiB**, and
  `service.WithMaxVariableElements(n int)`, default **10 000**, are enforced **together, at the
  same seam, at the same moment**: where a caller-supplied variable map is admitted. That seam is
  the closed set of four request fields (Evidence §4.6):
  `StartInstanceRequest.Vars`, `DeliverSignalRequest.Payload`, `DeliverMessageRequest.Payload`,
  `CompleteTaskRequest.Output`.
- Both are refused with a single new `service.ErrVariablesTooLarge`, whose message names which
  bound tripped. **Decision 5 routes it → 413.**
- `0` = unbounded for either.
- The element count walks the map with an **early exit at `n+1`**, so it is `O(min(elements, n))`
  and can never cost more than the bound it enforces. It runs **once per request**, not per
  evaluation — which is what the draft wanted and could not express at the evaluator.
- ⚠⚠ **The bound is on the INCOMING caller-supplied map, NOT on the resulting merged map.**
  This is a deliberate choice between two failure modes and it is the author's own interaction
  finding, written before the re-audit. Bounding the *merged* result would give the stronger
  property — no persisted map ever exceeds the bound — but it converts the unbounded runtime
  growth below into an **unrecoverable wedge**: once a service action's output has pushed the map
  past 256 KiB, every subsequent `CompleteTask` would be refused **413 forever**, and the human
  task could never be completed. Bounding the incoming map cannot wedge anything: the refusal
  happens before persist, with the caller present, and the caller can retry with less.
  ⇒ **What this bound guarantees is per-request on the caller axis.** It does **not** bound the
  aggregate map. Stated plainly because the alternative is an over-claim of exactly the kind this
  record keeps having to withdraw.

**Why enforcing them together is the whole point.** The draft's two bounds ran at different
times, in different packages, in different units, with no cross-check — and the *looser* one ran
first. Measured at the draft's defaults: 256 KiB of JSON integers admits **45 540** elements
against an element cap of 10 000 — **4.55×**. So the window 10 001 … 45 540 was admitted,
validated and **durably persisted**, then refused at every subsequent evaluation, forever, with
no repair verb (no route mutates process variables; the only exit is
`POST /admin/instances/{id}/cancel`). 45 540 small integers is ~223 KiB — an ordinary "list of
ids" payload, so the window was the common case, not the corner.

⇒ **What the co-location closes is the WINDOW**: there is no longer a request a byte bound admits
and an element bound later refuses, and the 256 KiB / 10 000 pair no longer has to be mutually
consistent to be safe. ⚠ It does **not** close the aggregate question — see the bullet above and
the scope statement below. An earlier draft of this fold claimed *"nothing is ever persisted that
cannot be evaluated"*; that is **false** once runtime growth is out of scope, and it is withdrawn.

**The 10 000 default is derived, and now measured rather than extrapolated.** From the O(n²)
ladder: 2 000 ≈ 100 ms (a tight bound for latency-sensitive deployments); **10 000 = 2.458 s
measured** (Apple M4 Pro, plain mode; a second lens measured 1.92 s on a faster run; the
extrapolation said 2.442 s, a 0.65 % error); 43 000 ≈ 45 s; 50 000 ≈ 61 s. ⚠ The audit's own
suggested replacements (*"5 000 ≈ 40 ms, 10 000 ≈ 150 ms"*) are wrong by ~15×: 5 000 ≈ 610 ms.

**256 KiB is a judgement call and is labelled as one.** Nothing derives it. It is a
**payload/storage** bound with no CPU claim attached; the CPU bound is element cardinality.
⚠ One datapoint argues it is *low*: first-party `action/httpcall` writes its response body into
`vars["httpBody"]` with a default cap of **10 MiB** — **40×** the variable byte bound. Two
first-party defaults that disagree out of the box. See the scope statement below.

**What this decision does NOT bound, stated rather than implied:**

- ⚠⚠ **Runtime growth.** The variable map is also grown by `mergeVars` from three non-request
  sources — a service action's output (`engine/step_triggers.go:161`), human-task completion
  output (`:936`) and the message/callback mirror (`:1208`) — plus the engine's own
  `_errorMessage`/`_errorAttempts` writes. **None of these is bounded by this decision, and that
  is deliberate.** Bounding them means refusing a persist *after* the side effect has already
  happened (the HTTP POST fired, the card was charged), which wedges the instance with no repair
  verb, or failing the step into an incident — a disposition design in `engine` that this
  delivery does not have and must not invent at the end of a fold. The untrusted axis this record
  exists to close is **caller-supplied** input; action output is author-configured. Opened as a
  backlog item, and `SECURITY.md` says so.
- **Predicate complexity.** The curve is for one measured quadratic predicate over a bounded
  input. A higher-degree *predicate* is still expensive, and on the engine's gateway path there
  is no wall-clock backstop by ADR-0056's deliberate trade. The predicate axis is author-supplied
  and there is no definition-deploy route.
- **The ABAC goroutine residual.** `expreval`'s 5 s `DefaultTimeout` **abandons** the running
  goroutine (`expreval.go:74-100`: the select returns on the timer, the goroutine keeps burning
  a core). Under this decision the env it evaluates was admitted under the bound, so the burn is
  bounded — but the mechanism is worth recording, and it is why a wall-clock guard was never the
  mitigation.

**Consequences for the locked invariants — stronger than the draft's.** `internal/expreval`,
`runtime` and `engine` are **not touched at all**. `ConditionEvaluator` keeps its signature; the
engine default stays synchronous at ~99 ns/op; `WithExpressionTimeout` and
`WithConditionEvaluator` keep their **documented** last-writer-wins contract untouched (⚠ the
draft called that collision *"silent"* — it is stated verbatim in both godocs,
`runtime/processdriver_options.go:196-197` and `:215-216`, and the draft's premise for demanding
a change was wrong); no third writer of `driver.conditionEval` is created; the process-wide
shared compile cache and the `slog.Bool("conditionEval", …)` startup diagnostic keep their
meanings; and ADR-0003/0049/0056 need no amendment. **Nor is any already-persisted instance
re-checked** — the bound is an admission control, so an instance carrying 20 000 elements from
before the upgrade keeps evaluating exactly as it did, and ADR-0049's replay guarantee is
untouched. The draft's evaluator-side bound would have broken all four of these things.

⚠ **Do NOT implement the `MaxNodes` fix** — Context §2 shows it is inverted and the check is
already in force.

### 3. Expression-derived URLs are restricted by default; author-typed URLs are not

- **`WithURLExpr` keeps its own `expr.Compile`.** ⚠ The draft routed it through
  `internal/expreval` on a "single vendor wrapper" rationale. That is withdrawn, for three
  executed reasons: (i) the rationale is false — re-derived **by import line**, four non-test
  files import the vendor and **three** are violators (`action/httpcall`, `action/transform`,
  `definition/model/validate/expr`), so routing one leaves the "only wrapper" claim false on the
  day it ships; (ii) it is **not semantics-preserving** — `httpcall.go:239-242` *rejects* a
  non-string URL-expr result with a non-retryable error, while `expreval.EvalString` **coerces**
  (`nil` → `"<nil>"`, `1+1` → `"2"`, `{"a":1}` → `"map[a:1]"`), so in the decision whose purpose
  is *stop being an SSRF primitive* the refactor would turn "refuse" into "coerce and dial";
  (iii) it moves compilation from option time to a per-call mutex-guarded cache lookup and, with
  the `expreval.New()` idiom, onto the timeout goroutine path. The vendor-wrapper consolidation
  question is real and becomes its own backlog item.
- **When a URL is expression-derived** (`urlExprProg != nil`, decidable at construction), the
  action installs a restricted client. Two checks, each where its data exists:
  - **IP deny-list, in `net.Dialer.Control`.** ⚠ Executed: `Control` receives only the resolved
    `network, address` — `http://localhost:…` arrives as `[::1]:…` / `127.0.0.1:…`, the hostname
    is gone. That makes it the right place for an **IP** rule (it sees every resolved address, so
    DNS rebinding cannot bypass it) and an impossible place for a **host** rule.
    The rule is stated as a **property**, not a list of five categories: refuse any resolved
    address that is not global unicast — `IsLoopback()`, `IsLinkLocalUnicast()`,
    `IsLinkLocalMulticast()`, `IsInterfaceLocalMulticast()`, `IsUnspecified()`, `IsPrivate()`
    (RFC1918 + ULA `fc00::/7`) — plus the ranges Go's helpers do not cover: `100.64.0.0/10`
    (CGNAT), `192.0.0.0/24`, `198.18.0.0/15`. Evaluated **after `ip.To4()`**, so IPv4-mapped IPv6
    (`::ffff:127.0.0.1`) is normalised.
    ⚠ `169.254.169.254` needs **no** separate rule: it is link-local. The draft listed "cloud
    metadata addresses" as a fifth category — it is inside a range the same sentence already
    named, and the phrasing disguised the gaps: `0.0.0.0`, `::ffff:127.0.0.1` and `100.64.0.1`
    all walked through an implementation that satisfied every one of the draft's four prescribed
    tests.
  - **Host allow-list, on the request URL and on each redirect hop.** `WithAllowedHosts([]string)`
    is a *positive* filter on the hostname, checked before the dial and re-checked in
    `CheckRedirect`. ⚠ **It does not override the IP deny-list** — an allow-listed host that
    resolves to `169.254.169.254` is still refused, or the option becomes a rebinding bypass.
  - `WithAllowedCIDRs([]string)` is the exemption that makes the escape hatch usable: it opts
    specific **networks** out of the IP deny-list, so the consumer whose `httpcall` node
    legitimately targets one internal `10.x` service has an answer short of turning the whole
    protection off. ⚠ Without it, `WithAllowedHosts` cannot express that case at all (the hook
    never sees a hostname) and `WithUnrestrictedTransport()` — disable everything — is the only
    lever. That is the "guard refuses the useful case" shape, and it is why this option exists.
  - ⚠ **The two gates are independent and BOTH must pass.** `AllowedHosts` (when configured)
    filters the hostname; the IP rule filters every resolved address. `AllowedCIDRs` exempts from
    the **IP** gate only — it does not admit a host the host gate rejected, and the host gate does
    not admit an address the IP gate rejected unless a CIDR says so. Stated explicitly because
    two opt-in lists over two different gates is precisely the shape a reader collapses into one.
  - **`CheckRedirect` with no allow-list configured (the default) FOLLOWS redirects**, and every
    hop is subject to the IP deny-list at dial time. With an allow-list configured, a hop to a
    host outside it is refused. ⚠ The draft's *"refuses a redirect whose host leaves the
    allowlist"* read literally means the empty default refuses **every** redirect, breaking
    http→https upgrades and trailing-slash normalisation for every existing user.
- **`WithUnrestrictedTransport()`** makes the current permissive behaviour explicit.
- ⚠ **`WithHTTPClient` collides, and the collision is refused, not resolved silently.**
  `NewHTTPCall` applies options in registration order over one `h.client` field, so
  `NewHTTPCall(WithURLExpr(e), WithHTTPClient(c))` and the reverse ordering give different
  results and one silently drops the security control. And "wrap their transport" is not
  generally possible — `otelhttp.NewTransport(...)` is an opaque `RoundTripper`, not an
  `*http.Transport` whose `DialContext` can be reached. Therefore: setting **both**
  `WithURLExpr` and `WithHTTPClient` **without** `WithUnrestrictedTransport()` is a construction
  error, surfaced as a non-retryable error from `Do` (the existing `urlExprErr` pattern,
  `httpcall.go:128-130`), naming both options and the escape hatch. ⚠ This is the same
  "compose or refuse, never overwrite" rule the draft applied to `runtime` and missed here —
  where the casualty is a security control, not a knob.
- **`WithBaseURL` is unchanged**: a URL the definition author typed is not attacker-controlled,
  and default-denying it would break every existing user for no gain.
- ⚠ **Scope statement.** `RedactVariables` (Decision 4) is a *display* control; this is a
  *destination* control. `httpcall.Do` and `transform.Do` receive the **unredacted** variable map
  (`in = copyVars(s.Variables)`), so a definition author can write
  `WithURLExpr('https://reports.example.com/?q=' + vars.ssn)` and, to an allowed host, this
  decision permits it. Neither control constrains what a definition-authored action does with the
  variables. `SECURITY.md` says so in one sentence, because a reader will otherwise compose the
  two into a guarantee neither makes.

### 4. Redaction runs at the `ProcessInstance` → response boundary, and the copy is DEEP

- `httpcore.CustomizeConfig.RedactVariables func(ctx context.Context, scope RedactionScope, vars map[string]any) map[string]any`,
  where `RedactionScope` carries at least `InstanceID`, `DefinitionRef` and a `Kind`.
  ⚠ The draft's `func(map[string]any) map[string]any` is instance-blind and scope-blind: it
  cannot express *"redact `ssn` only for definition `kyc-v3`"* or *"task vars but not instance
  vars"*, which makes it strictly weaker than `InstanceMapper func(engine.InstanceState) any` —
  the seam it is supposed to sit above. Widening costs nothing now and is a breaking change after
  v0.1.0.
- ⚠ **It runs above `InstanceMapper`, which bypasses it wholesale.** The draft placed it inside
  `NewInstanceView`, where `CustomizeConfig.InstanceMapper` replaces the default mapper and
  receives the raw `engine.InstanceState`. The seam CLAUDE.md lists as a product feature would
  silently disable the security control the same ADR adds.
- ⚠⚠ **The covered set is the ELEVEN paths named in Context §4**, applied in a helper each one
  calls — not in `mapInstance`, which reaches only six of them, and not only in the two
  mapper-less non-admin endpoints the draft added. The three direct-`NewInstanceView` admin
  endpoints (`ResolveIncident`, `CancelInstance`, `ResolveCompensationStall`) are in the set.
  **Phase 4 asserts the count as a machine-checked invariant**, because this enumeration has now
  rotted twice.
- ⚠⚠ **The map handed to `RedactVariables` is a JSON-shaped DEEP copy** (recursively over
  `map[string]any` / `[]any`), not `maps.Clone`'s result. Executed (Evidence §3): with a shallow
  copy, the obvious hook for a nested secret —
  `delete(m["applicant"].(map[string]any), "ssn")` — **deletes it from the live cached instance
  entry**, so a security control becomes silent data loss on the next read *and the next
  persist*; and a consumer who instead rebuilds only the top level leaves the nested secret in
  the response. Both natural implementations are wrong, in opposite directions. A shallow copy
  was sufficient for the *aliasing* defect and is insufficient for the *hook*.
- ⚠ **The deep copy is taken ONLY when a hook is configured.** The default path — no
  `RedactVariables` — takes the shallow copy, which is all the aliasing defect needs. This
  matters because the read path is a hot path and a recursive copy of every response's variable
  map is a real cost that nothing in this bundle has measured. Stated as an author interaction
  finding: the deep copy is D4's *hook* requirement, not D4's *aliasing* requirement, and
  charging every consumer for it would be a regression introduced by a fix.
- The **default** (no hook configured) is therefore the shallow copy, which fixes the aliasing
  defect at `view.go:31` whether or not a consumer redacts anything and restores the repo's
  clone-on-escape convention.
- **Fix the false godoc.** `persistence/caching_instance_store.go:72` claims
  `cloneInstanceEntry` *"deep-copies"*. It does not. Correct it in this bundle — it is reachable
  from this diff and the Delivery Gate requires killing it.
- The `SECURITY:` caveat is added to the instance and task route groups in all three adapters, so
  the three admin-only occurrences stop implying the others are safe by omission.

⚠⚠ **What this covers, stated as a covered set rather than as closure — because the draft
claimed closure twice and was wrong both times.**

**Covered:** `InstanceState.Variables`, on all eleven response paths.

**NOT covered, and each is a new backlog item rather than a silent gap:**
`tokens[].payload`; `incidents[].error` (the raw `err.Error()` of a failed action — for an
`httpcall` node, the target URL and query string, i.e. the same value `ClassifyError` blanks at
5xx); `tasks[].{candidates,claim,completion}` (actor attribute maps and a free-text note); the
embedded `definition` (every gateway and flow condition expression source — `service`'s existing
`WithoutEmbeddedDefinition` is the only lever today, and it is a construction-time engine option,
not a per-response control); and `ActionableView`'s `allowed_actions[].condition` and
`candidates[]`. Four of these live inside `service.instanceJSON`, which is **unexported** with a
custom `MarshalJSON` — so covering them is work in `service`, not a `httpcore` helper, and
bundling it here multiplies exactly the interaction surface that failed this delivery's lineage
twice. ⇒ **Backlog 54 closes for `variables`** (aliasing + redaction hook + the eleven paths);
the rest opens as new items.

⚠ **And the flow-condition disclosure is a decision, not an omission.** `GET …/actionable`
publishes `"condition":"vars.internalApprovalLimit > 5000"` on a **non-admin** 200 route, and
`GET …/snapshot` embeds the whole definition. Decision 5 spends two sections removing predicate
source from the 403 and 400 arms while these success paths serve the same class of string.
**Position, stated:** routing conditions and the embedded definition are **author-supplied
process metadata a caller acting on the process is entitled to see** — the actionable view exists
to convey them — whereas the 403 leak is *policy* source the caller has no role in. A deployment
that treats condition source as sensitive has no lever today; that is the backlog item, and
`SECURITY.md` names which strings a non-admin caller can read.

### 5. A 403 says nothing, 400 says everything except the value, 5xx unchanged

`ClassifyError` gains a per-class message policy:

| status | message |
|---|---|
| 403 | static `"not authorized"` — raw error logged at the transport seam |
| **400** | **value-free rendering** — see below |
| 404, 409, 422 | unchanged |
| **413** (new) | static `"request too large"`, via `ErrRequestBodyTooLarge` **and** `service.ErrVariablesTooLarge` (Decisions 1 and 2) |
| 5xx | unchanged (already blank) |

⚠ **Both new sentinels are routed here, in this arm, by name.** `ClassifyError`'s `default` is a
**500 with an empty body**, so *any* sentinel this bundle mints and does not route becomes an
internal error — which would render the one thing a caller can act on (*your variable payload is
too large*) as a server fault. Phase ordering must let the classifier see `service`'s sentinel;
see the plan's phase table.

⚠ **`ClassifyError`'s arms are order-dependent by construction.** Any future arm — including
ADR-0185's 401 and 503 — must state its position relative to the existing arms and carry a test
asserting that an error matching two arms resolves to the intended one. This sentence exists so
the lesson outlives the bundle that learned it.

**400 is deny-by-default over an OPEN set, with an enumerated exception list — not a blanket
blanking.** ⚠⚠ This is the revision's second substantive change, and it exists because the
draft's blanket static text **destroyed the actionable messages three prior ADRs deliberately
added** (Context §5). The rule:

> The 400 arm renders `err.Error()` only for sources whose message is **provably value-free by
> construction**, enumerated below and pinned by a test. Every other source — including any
> sentinel added to the arm in future — renders static text plus the correlation id.

| 400 source | rendering | why |
|---|---|---|
| `kernel.ErrBadCursor`, `kernel.ErrBadArmedTimerCursor` | `err.Error()` | messages are `": not an instance cursor"` / `": cursor carries no start time"` — no caller value |
| `engine.ErrEmptyTriggerKey`, `engine.ErrEmptyReassignTarget` | `err.Error()` | `"%w: %T.%s"` — names the **field the caller must fill**. ADR-0152/0183 rationale, in the switch being edited |
| `httpcore.ErrBadInput` | `err.Error()` | **executed**: `httpcore.Validate` renders `Key: 'DTO.name' Error:Field validation for 'name' failed on the 'max' tag` — field + tag, no value, **not even a length**. This is the highest-volume 400 on all 26 routes |
| `engine.ErrInvalidOutcome` | **reshaped**: `node %q: outcome not declared` | it is the one non-validation sentinel echoing a caller value (`"%w: node %q outcome %q"`) |
| `engine.ErrOutcomeRequired` | `err.Error()` | `"%w: node %q"` — a definition node id, ADR-0146 rationale |
| `validation.ErrInvalidInput` | what `runtime/validation` rendered (below) | the typed error never reaches here — `gate.go:45` flattens it |
| anything else in the arm | static `"invalid input"` | deny-by-default for the open set |

⚠ Two `ErrBadInput` **wrap sites** embed a caller value and are edited rather than blanked:
`admin_endpoints.go:30` (`unknown status %q`) and `dto.go:174` (`got %q`). Both name the allowed
set instead of the rejected input.

⚠ **The pin test is a machine-checked invariant, not a list in prose.** It asserts that the set
of sentinels matching the 400 arm equals the set enumerated above — a new sentinel added without
a row fails the test rather than silently inheriting `err.Error()`.

**Inside `validation.ErrInvalidInput`, the rendering lives in `runtime/validation` and is
value-free BY CONSTRUCTION.**

⚠⚠ **It cannot be done at `ClassifyError`, because the typed error never gets there.**
`runtime/validation/gate.go:45` is `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` — `%s`,
not `%w` — so `errors.As` is `true` before the gate and `false` after. The gate must preserve the
strategy's error (`%w`) **and render the client-safe message itself**, where the type is still
available.

⚠⚠ **And the draft's replacement rendering was itself not value-free.** It named
`InstanceLocation`, which is *instance*-derived. Executed against the real in-repo strategy
(Evidence §2):

| column | verdict |
|---|---|
| `keywordLocation` | **value-free in every shape tried** — `/properties/ssn/pattern`, `/additionalProperties/type`, `/propertyNames/maxLength`, `/properties/items/items/type`. It is a JSON pointer into the **schema**, which is author-supplied |
| `instanceLocation` | **leaks** — a card number submitted as an object key renders as `/4111-1111-1111-1111` |
| the vendor's `Error.String()` | **leaks twice** — the value (`'123-45-6789'`) *and* lengths (`maxLength: got 9, want 3`) |

⇒ the rendering is: one line per leaf of `ValidationError.BasicOutput().Errors`, carrying
**`keywordLocation` and nothing else**. Not the instance location, not the vendor's text, and
**nothing derived from the value** — no lengths, no counts, no enumerated allowed values.

- ⚠ Use `BasicOutput()`, not the root error. The object `errors.As` yields is the **root**
  `*jsonschema.ValidationError`, whose `ErrorKind.KeywordPath()` is **empty** — a literal
  rendering of the draft's prescription produces `at '/': violates ` with a trailing blank, and
  every prescribed assertion is satisfied by that useless message. The usable leaves are in
  `.Causes`, recursively.
- ⚠ Do **not** call `ErrorKind.LocalizedString`. Executed: it **panics** on a nil printer —
  turning a malformed client request into a server panic inside the 400 path — and a real printer
  promotes `golang.org/x/text` from indirect to a new direct dependency.
- ⚠ **Ergonomics cost, measured not asserted.** For a closed-`properties` schema
  `keywordLocation` still names the field *and* the constraint. For an **array** it loses the
  index: `/properties/items/items/type` does not say *which* item failed, where the leaking
  `instanceLocation` said `/items/1`. Accepted, and recorded as the price of value-freedom by
  construction.
- `expr` (`expr.go:64,68`) stops `%q`-ing `v.source[i]` on the runtime path and renders static
  text. ⚠ Phase 2 must **not** also re-route that package through `expreval` — it is a separate
  question (Decision 3) and changing validation semantics mid-phase is out of scope.
- `avro` and `callback` render static text. ⚠ For avro the *stated reason* is corrected: not
  merely "no structured leaf" but that it **echoes the submitted value verbatim** on the enum
  path (`"4111-1111-1111-1111"`) and a length on the fixed path (`11 != 4`).
- ⚠ The strategy set is **open** — `validate.Register(kind, factory)` is exported and `callback`
  takes an arbitrary function — so the allow-list keys on **kind**, and an unknown kind renders
  static. "The other three strategies" is closed-set phrasing over an open set.

**Every error body gains a correlation id — minted in `writeErr`, not in `ClassifyError`.**
⚠ Folded correction: `ClassifyError(err error) (int, ErrorBody)` takes **no context and no
config**, and the OTel span id is reachable only from the request `ctx` — *not* from
`cfg.TracerProvider`, which creates spans and cannot expose one already on a request. Changing
that signature breaks an exported function `doc.go:66` advertises as a consumer seam. So each
adapter's `writeErr` — which already holds a `ctx` — mints the id (the recording span's id, else
a random hex) and assigns it onto the returned `ErrorBody`. `ClassifyError` keeps its signature
and its pure-function discipline.

**And the log half of the join is built, per class, because today it does not exist.**
⚠ All three `writeErr`s log only `if status >= 500`, and `httpcore` never logs at all — so a 403
or 400 produces **no log record today** and the entire justification for blanking 403 ("an
operator can join the two") rested on a join nobody built. The guard widens, and *what* is logged
differs by class because the data differs in kind:

| class | logged by default | level |
|---|---|---|
| 403 | the **raw** error + correlation id. The leaked string is the deployment's **own policy predicate source** — it belongs in the operator's log | `WarnContext` |
| 400 | the **rendered, client-safe** message + correlation id + the sentinel class. The raw error may contain **submitted values** | `WarnContext` |
| 400/403 raw error | only under `httpcore.WithVerboseErrorLogging(true)`, default **off** | `WarnContext` |
| 5xx | unchanged (raw error) | `ErrorContext` |

⚠⚠ **This is a folded Critical, and the reason is the shape of the fix.** Widening the sink
unconditionally would move the submitted value the delivery exists to stop disclosing *off the
wire and onto `slog.Default()`* — a sink `RedactVariables` cannot reach, that Decision 6's
at-rest enumeration would then be wrong about, and that in a typical deployment has longer
retention and a wider audience than the API response ever had. The headline outcome would be a
**relocation**, not a removal. `WarnContext` rather than `ErrorContext` for 4xx is deliberate
too: routine 400s at `Error` level flood an operator's error stream.
`CustomizeConfig.Logger`'s godoc — *"receives 5xx raw error details (never sent to clients)"* —
is corrected in place.

⚠ Fiber above `fiber.DefaultBodyLimit` is the stated exception to *"every error body"*: the route
group is never reached, so there is no `ErrorBody`, no id and no log line. Decision 1 covers it.

### 6. At rest: the posture is documented, the mechanism is deferred — deliberately

`wrkflw` **does not** encrypt process variables at rest and **does not** claim a tamper-evident
audit trail. `SECURITY.md` says so explicitly, **names the plaintext locations as a derived
list**, and states what the consumer owns (database-level encryption, grants, backup handling).

⚠⚠ **Twelve columns across seven tables, in all three dialects** —
`wrkflw_instances.snapshot`, `wrkflw_journal.trigger`, `wrkflw_outbox.{payload,last_error}`,
`wrkflw_definitions.definition`,
`wrkflw_human_task.{vars,candidates,eligibility,claim_actor,completion_actor,note}`,
`wrkflw_call_links.{output,error}`, `wrkflw_timers.trigger_payload`,
`wrkflw_chain_links.start_vars`. Evidence §4.4. The draft named **two**, and an audit lens
raised it to "at least six" and was itself short by three tables.

⇒ **Phase 9 derives the list from `internal/persistence/store/migrations/{postgres,mysql,sqlite}`
at implementation time rather than copying it from this record, and the invariant is a test**:
any new column in those tables is either listed or explicitly justified. This is the one decision
in the bundle whose **deliverable is the enumeration**, and an incomplete list presented as
exhaustive is strictly worse than the silence D6 rejects — it converts a consumer's own audit
into a false negative.

⚠ `SECURITY.md` must also record the **two sinks this bundle itself creates or widens**: with
`WithVerboseErrorLogging(true)`, rejected request payloads reach the configured `slog.Logger`;
and the caps bound, but do not protect, what is already at rest.

The mechanisms are deferred to their own future ADR, and the deferral is the decision, not an
omission:

- A `persistence.VariableCodec` without a **key-rotation and key-loss** story is worse than none:
  a consumer who rotates a key makes every stored instance unreadable, and the library must not
  own key management.
- A hash-chained `wrkflw_journal` whose chain head lives in the same database the attacker
  already writes to is security theatre. Tamper-*evidence* requires externalising the head, a
  deployment contract the library cannot impose. `engine.NodeVisit` is explicitly **not** the
  place for it (ADR-0145).

Recording "we do not do this, and here is why doing it badly is worse" is a decision a consumer
can act on. Silence is not.

## Consequences

### Positive

- The unbounded-input surface closes on both axes that were measured: body size (39 sites, one
  policy, one status, **and the three that could not report it named**) and variable payload
  (bytes **and** elements) — the second **at admission**, so the two bounds can no longer
  disagree about the same request and **no already-persisted instance is re-checked**.
  ⚠ Scoped, not closure: the bound is per-request on the caller axis; the aggregate map is not
  bounded, because bounding it at a request boundary would wedge instances that grew at runtime.
- ⭐ **The bound acts on the MAP, not on an evaluator, so every expression surface that reads
  process variables inherits it for the caller-supplied contribution** — both ABAC evaluators,
  the engine's gateway path, `action/httpcall`'s URL expression and `action/transform`. ⚠ *For
  the caller-supplied contribution* is the whole quantifier: a map grown past the bound by
  runtime `mergeVars` is read unbounded by all four. Even so, three of those four were **not
  reachable at all** by the draft's evaluator-side design — neither ABAC evaluator has an options
  seam, and an `Action` receives `(ctx, in)` and holds no reference to the driver.
- `internal/expreval`, `runtime` and `engine` are **untouched**. `ConditionEvaluator` keeps its
  signature, the engine default stays synchronous at ~99 ns/op, the two existing
  `driver.conditionEval` writers keep their documented last-wins contract, the shared compile
  cache and the startup diagnostic keep their meanings, and ADR-0003/0049/0056 need no amendment.
- `httpcall` stops being an SSRF primitive on its untrusted axis while staying ergonomic on its
  trusted one, **and the escape hatch works** — `WithAllowedCIDRs` gives the one-internal-service
  consumer an answer short of disabling the protection.
- Both 4xx arms proven to leak stop leaking, **and the fix does not relocate the leak**: 403
  becomes static with the raw predicate source in the operator's log; 400 renders a schema
  location that is value-free by construction; and the raw 400 error reaches the log only under
  an explicit opt-in.
- **The actionable 400 messages ADR-0146, ADR-0152 and ADR-0183 added survive.** Five of the
  eight sentinels — including `ErrBadInput`, every DTO on all 26 routes — keep their message,
  because it was executed and shown value-free rather than assumed leaky.
- Redaction covers `InstanceState.Variables` on **eleven** response paths, with a deep copy the
  hook can safely mutate, and the covered set is **named rather than claimed closed**.
- The at-rest posture becomes a written statement a consumer can hold the library to, over a
  **derived** enumeration rather than a sample.

### Negative / costs

- **BREAKING (wire)**: `ErrorBody` changes in five ways, not two — the 403 message becomes
  static; the 400 message changes shape; a correlation-id field appears (breaking any consumer
  decoding with `DisallowUnknownFields`); a **new 413 status** appears on routes that previously
  returned 400 or 500 (breaking clients with an exhaustive status switch); and `Logger` starts
  receiving 400/403 records, changing log volume.
- **BREAKING (source)**: the eight exported `httpcore` endpoint functions that project instance
  state gain the response-policy parameter Decision 4 needs — `GetInstanceSnapshot`,
  `GetActionableView`, and the six taking `mapper func(engine.InstanceState) any`. All are called
  from the three adapters' `groups.go`, i.e. by any consumer who assembled their own route group.
  Threaded in **one** edit as a single parameter, not eight ad-hoc ones.
  ⚠ `ClassifyError`'s signature is deliberately **not** changed; that is why the correlation id
  is minted in `writeErr`.
- Default-on caps will reject payloads that work today. `MaxBodyBytes = 0` and the element/byte
  bounds' `0` are the opt-outs, and the body-size histogram lets a consumer measure first.
  **1 MiB and 256 KiB remain judgement calls, explicitly `ASSUMPTION (unverified)`**; 10 000 is
  derived and now measured.
- ⚠ **Two first-party defaults disagree**: `action/httpcall` writes up to **10 MiB** into
  `vars["httpBody"]`, 40× the 256 KiB variable bound. Because runtime growth is deliberately
  unbounded (Decision 2), this does not wedge an instance — but a consumer using `httpcall` with
  a large `WithMaxResponseSize` should size `WithMaxVariableBytes` accordingly, and
  `SECURITY.md` says so.
- **Runtime variable growth is not bounded**, by decision. A `mergeVars` from a service action's
  output can carry the map past either bound with no caller present. Backlog item; the
  alternative was inventing an incident-disposition design in `engine` at the end of a fold.
- A consumer whose `httpcall` node legitimately targets an internal `10.x` address from a
  variable-derived URL must now say so explicitly, via `WithAllowedCIDRs`.
- Fiber diverges above `fiber.DefaultBodyLimit`: framework plain-text 413, no `ErrorBody`, no
  correlation id, no log. Documented, warned about at mount time, and not fixable from a mounted
  route group.
- 400 loses the array index in validation messages (the schema location does not carry it).
- The engine's gateway evaluation remains wall-clock unbounded for a pathological **predicate**.
  That is ADR-0056's standing trade, restated rather than quietly reversed.
- 100 and 101 stay **open**. A consumer with a regulatory at-rest requirement gets a documented
  "no", not a solution.

### Neutral / follow-ups opened

- **Backlog 54 closes for `variables` only.** New items: redaction (or a documented position) for
  `tokens[].payload`, `incidents[].error`, `tasks[].{candidates,claim,completion}`, the embedded
  `definition`, and `ActionableView`'s `allowed_actions[].condition` + `candidates[]`.
- **New item: bound runtime variable growth** (`mergeVars` from action/task/message output),
  which needs an incident-disposition design in `engine`.
- **New item: the vendor-wrapper consolidation question.** By import line, three non-test files
  import `expr-lang/expr` directly — `action/httpcall`, `action/transform`,
  `definition/model/validate/expr` — and this delivery routes **none** of them through
  `internal/expreval`. `action/transform` is a second unbounded expression surface over process
  variables and is currently invisible in every document.
- The three adapters now share a per-request policy (body cap, correlation id, 4xx logging) with
  no shared route table: backlog **96**'s blind parity suite becomes more expensive to live
  without, not less.
- Backlog **60**/**91** (trace and schema envelopes on the outbox) share the journal column a
  future integrity chain would use; design them together, not as three migrations of one table.
- Whether the engine's gateway path should get a *deterministic* cost bound (an instruction
  budget rather than a wall clock) is now the open question ADR-0056's trade leaves behind. It is
  not decided here.
