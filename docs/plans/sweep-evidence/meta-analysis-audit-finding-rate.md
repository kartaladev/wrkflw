# Meta-analysis: why does every design audit return ~58 findings?

**Date:** 2026-08-24 · **Author:** analysis subagent (read-only; no repo file outside this one was
modified) · **Corpus:** `docs/plans/sweep-evidence/` — 10 audit rounds across 4 delivery lineages,
plus the 119-item backlog sweep's triage/fix records.

> **What this document is.** The owner's question: *"why do our audits keep returning ~58 findings,
> round after round, at wildly different scopes? Is the cause architectural, a design-process flaw,
> or something about the audit itself?"* This is a counted answer, not a theory. Where a number is
> an estimate rather than a full count, it is labelled **(est.)**.

---

## 0. Method and sampling frame — read this before trusting any number below

Three distinct populations are counted here. They are **not** interchangeable and mixing them is the
easiest way to produce a wrong conclusion:

| population | what it is | size | how obtained |
|---|---|---|---|
| **P1 — headline totals** | the "N findings, M Critical" verdict line each adjudication publishes | 10 rounds | quoted verbatim from each `*-adjudication.md` |
| **P2 — enumerated accepted findings** | every finding the adjudication *names and accepts* (Criticals in full, Majors as listed bullets) | **193 items** (counted item-by-item in §3) | hand-classified, one pass, this document |
| **P3 — raw per-lens findings** | every finding in the lens reports, accepted or not | **554** | the sum of the ten headline totals, which are themselves per-lens sums; not individually classified |

**The bucket distribution in §3 is over P2, not P1.** P2 is a strict subset of P1: an adjudication
enumerates its Criticals exhaustively and its Majors selectively (several rounds end their Major
list with *"plus the Majors recorded in each lens report"*). P2 therefore over-represents Criticals
and under-represents Minors — which is the right bias for a root-cause question, and the wrong bias
for question 4 (what fraction is cosmetic). Question 4 is answered against P1 severity splits where
they exist, and the P2 bias is stated there.

**De-duplication.** Adjudications group findings by the decision they falsify and note when two or
three lenses found the same thing (`E22 + C2`). P2 counts the **de-duplicated** item once. This is
why P2 is far below P1 (554 total headline findings): the headline numbers are raw per-lens
sums including cross-lens duplicates.

**One number in the corpus I could not reconcile, and did not use.** Round 2's headline is
*"38 findings across three lenses; ~13 distinct Criticals … (16 raw, with three pairs found
independently by two lenses each)"* — the only round whose Critical count is given as an
approximation. It is carried as `~13` everywhere below and excluded from the tightest statistics.

**Bucket set.** The eight buckets in the brief (A–H) were used as given, with two changes, both
declared:

- **F was widened** from "guard/test from a prior delivery" to **"collision with any existing repo
  artifact — guard, test, exported symbol, or shipped feature — that the new design breaks, is
  blocked by, or is defeated by, and that the bundle never mentions."** Three findings
  (`WithActorResolver` colliding with `service.WithActorResolver`; `RedactVariables` bypassed by the
  documented `CustomizeConfig.InstanceMapper`; `WithHTTPClient` colliding with D3's transport) are
  the same defect as a guard collision with the artifact type changed, and splitting them off would
  have created a 3-item bucket.
- **One bucket was added: `I` — unhandled reachable case.** The design is silent about a state that
  occurs in production; there is no contradiction (E), no refuted premise (C), and the mechanism
  works as specified (not H) — it simply was never asked about this input. Examples: "nothing put an
  actor in the context"; variables growing during execution with no HTTP caller present; the
  default-ON cap wedging an instance with no verb to shrink it. This bucket holds **8 of 193** items
  (4.1 %) — small, but each is a reachable production state, so folding them into a neighbouring
  bucket would have misattributed them.

**Classification rule where two buckets fit** (applied consistently; the pairs that recur):

- *Count wrong* → **D**, even when the wrong count caused a design error. (Round 5's C9 is the type
  case: a false call-site count is the entire basis for a decision. It is still D.)
- *Claim about existing code, refuted by execution* → **C**. *Claim about the thing being built,
  refuted by execution* → **H**.
- *"No phase assigns this decision's realisation to any package"* → **D**. The brief's own D wording
  covers it: a list derived over the frame the author was editing, asserted over the repo.
- *A guard/mechanism that works but introduces a hazard the design does not mention* → **H**
  (the mechanism does not behave as the author assumed).

---

## 1. The ten rounds, as recorded

Every cell is quoted from the round's own `*-adjudication.md` header. Lens counts and report sizes
are re-derived from the files on disk (`wc -l` over each round's non-adjudication reports) and
**match the adjudications' own stated line totals in all 10 rounds** — the one enumeration in this
corpus that never rotted.

| # | round | file prefix | bundle scope | lenses | findings | Critical | lens-report lines |
|---|---|---|---|---|---|---|---|
| 1 | B3 audit | `audit-b3` | B3: 12 backlog items / 5 decisions (ADR-0185 + ADR-0186 together) | 3 | 58 | 12 | 2,291 |
| 2 | B3 re-audit | `reaudit-b3` | B3 revised, same 5 decisions | 3 | 38 | ~13 | 2,631 |
| 3 | 0186 audit | `audit-0186` | ADR-0186 standalone, 6 decisions | 4 | 63 | 33 | 4,020 |
| 4 | 0186 re-audit | `reaudit-0186` | the fold of round 3, 6 decisions | 4 | 56 | 28 | 3,717 |
| 5 | 0186 audit 3 | `audit3-0186` | re-cut to **3** decisions | 4 | 65 | 20 | 2,295 |
| 6 | 0186 audit 4 | `audit4-0186` | re-cut to **1** decision | 4 | 61 | 24 | 2,956 |
| 7 | 0186 audit 5 | `audit5-0186` | 1 decision **stripped to a minimum** | 4 | 57 | 14 | 2,305 |
| 8 | 0187 audit | `audit-0187` | ADR-0187 §AT-REST, 10 decisions | 4 | 64 | 17 | 3,612 |
| 9 | 0187 re-audit | `reaudit-0187` | 0187 revised — **owner scoped it to 2 lenses** | 2 | 34 | 11 | 1,594 |
| 10 | 0185-core audit | `audit-0185core` | ADR-0185-core, 3 decisions | 4 | 58 | 22 | 3,473 |
| | | | **totals** | **36** | **554** | **194** | **28,894** |

---

## 2. Question 6 first, because it reframes everything else: the count tracks AUDITOR EFFORT

This is the strongest result in the analysis and it is a full count, not an estimate. Normalise each
round's finding total by the number of lenses dispatched:

| # | round | lenses | findings | **findings / lens** | Critical | Crit / lens | lens-report lines / lens |
|---|---|---|---|---|---|---|---|
| 1 | audit-b3 | 3 | 58 | **19.33** | 12 | 4.00 | 764 |
| 2 | reaudit-b3 | 3 | 38 | **12.67** | ~13 | 4.33 | 877 |
| 3 | audit-0186 | 4 | 63 | **15.75** | 33 | 8.25 | 1,005 |
| 4 | reaudit-0186 | 4 | 56 | **14.00** | 28 | 7.00 | 929 |
| 5 | audit3-0186 | 4 | 65 | **16.25** | 20 | 5.00 | 574 |
| 6 | audit4-0186 | 4 | 61 | **15.25** | 24 | 6.00 | 739 |
| 7 | audit5-0186 | 4 | 57 | **14.25** | 14 | 3.50 | 576 |
| 8 | audit-0187 | 4 | 64 | **16.00** | 17 | 4.25 | 903 |
| 9 | reaudit-0187 | 2 | 34 | **17.00** | 11 | 5.50 | 797 |
| 10 | audit-0185core | 4 | 58 | **14.50** | 22 | 5.50 | 868 |

**Pearson correlation, findings vs lens count, all 10 rounds: r = 0.855 (r² = 0.73).**
Lens count alone explains **73 %** of the variance in the headline number the owner is worried about.

Restrict to the seven **4-lens** rounds — the only apples-to-apples comparison, and the ones spanning
the entire 12× scope cut from "6 decisions across 6 packages" to "one option, one sentinel, one
status":

- raw findings: 63, 56, 65, 61, 57, 64, 58 — **mean 60.6, sd 3.33, CV 5.5 %**
- per lens: 15.75, 14.00, 16.25, 15.25, 14.25, 16.00, 14.50 — **mean 15.14, sd 0.83, CV 5.5 %**

A four-agent audit of this repo returns **15.1 ± 0.8 findings per agent**, whatever it is pointed at.
That is a tighter distribution than most of the measurements these audits themselves argue about.

### 2.1 The two off-trend rounds are both explained by dispatch, not by artifact quality

- **Round 9 (34 findings)** is the corpus's natural experiment. Its own header: *"Scope chosen by the
  owner: **two lenses, not four** … Execution and failure-modes were skipped."* Two lenses,
  17.0 findings each — **above** the four-lens mean. The bundle had just been revised to fix six
  decisions; the count halved because half the agents were not dispatched.
- **Round 2 (38 findings, 12.67/lens)** is the one genuine low outlier. It is also the only round
  whose adjudication reports a *de-duplicated* Critical count (`~13 distinct … 16 raw`), i.e. the
  only round that visibly collapsed duplicates before publishing its headline. Its lens reports are
  the **second-largest per lens in the corpus** (877 lines/lens) — more work, fewer published
  findings, consistent with de-duplication rather than with a better bundle.

### 2.2 Each individual lens also converges on ~16, whatever it is pointed at

Five rounds publish a per-lens breakdown. Reading down the columns rather than across the rows:

| lens | R5 | R6 | R7 | R8 | R9 | mean |
|---|---|---|---|---|---|---|
| execution | 16 | 19 | 14 | 10 | — | 14.8 |
| failure-modes | 16 | 20 | 12 | 18 | — | 16.5 |
| counting | 13 | 8 | 15 | 17 | 17 | 14.0 |
| interaction | 20 | 14 | 16 | 19 | 17 | 17.2 |

Four differently-briefed agents, attacking bundles that differ 12× in scope, all land between 14 and
17 findings on average. **The output is a property of "one Opus agent, one adversarial brief, one
bundle-sized reading budget", not of how much there is to find.**

### 2.3 What effort does NOT predict: severity

`Critical count vs lens count` is a much weaker r = 0.66, and Criticals fell 33 → 28 → 20 → 24 → 14
across the ADR-0186 scope cut while the total held. The **report-lines vs Criticals** correlation is
r = 0.81 — i.e. rounds where agents wrote more also *escalated* more, which is a second effort
artifact, not a quality signal.

⇒ **Severity, not count, is the metric that responded to the scope cut.** Criticals per lens fell
from 8.25 (round 3) to 3.50 (round 7) — a 2.4× improvement — inside a total that never moved.
The flat 58 is an artifact of the measuring instrument; the falling Critical rate underneath it is a
real signal that was hidden by watching the wrong number.

---

## 3. Bucket classification, round by round (population P2)

Every item below is a finding the adjudication **names and accepts**. Each is glossed (CLAUDE.md
rule 13: no bare labels) and assigned exactly one bucket. Where the adjudication grouped several
lens findings into one adjudicated item (`E22 + C2`), it is **one** item here.

### Round 1 — `audit-b3` (B3, 5 decisions; 58 findings / 12 Critical) — 19 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | `has(vars,"k")` — the escape hatch ADR-0185 D4 prescribes — is not a function in expr v1.17.8; it compiles to nil and denies everyone | H |
| 2 | `Reassign` → `Complete` bypasses the claimant guard; the ADR names `Reassign` as the mitigation and it is the escalation | E |
| 3 | Eligibility is a **stored** field frozen into the task row; `AuthzSpec` has no json tags ⇒ upgrade strands every in-flight human task | **B** |
| 4 | The gate ships in 2 of **4** `expreval.New()` instances; casbin builds its own and stays fail-open | **A** |
| 5 | ADR-0186 D2 justified against the import rule (`purity_test`) when the locked invariant is deterministic replay; measured 99 → 965 ns/op | C |
| 6 | Pin count is 29, not 23 — `ReassignInput.By` is tagged `"by"`, so the grep net missed six reassign pins | D |
| 7 | Every `step_triggers.go` citation is 10 lines stale at the bundle's own commit (conclusion held, measurement wrong) | G |
| 8 | Decision 3's blast radius never counted: 274 `NewUserTask` sites / 128 without eligibility / 5 reaching `model.Validate`, across packages no phase covers | D |
| 9 | The preserved 400 arm echoes submitted values verbatim via jsonschema `pattern` — refutes the spec's own `ASSUMPTION (unverified)` | C |
| 10 | `WithActorResolver` collides with the existing `service.WithActorResolver` (opposite data-flow meaning) | F |
| 11 | No decision for "nothing put an actor in the context": anonymous claim, `Actor{ID:""}` in the audit record, guard degenerates to `"" == ""` | I |
| 12 | `RedactVariables` is bypassed wholesale by the documented `CustomizeConfig.InstanceMapper` | F |
| 13 | Plan phases 3 and 4 are circular (phase 3 writes a field phase 4 creates) | E |
| 14 | Zombie scope: D2's "same knob" is two unconnected knobs in different packages and units, unplumbed to `engine/conditions.go:43` | **A** |
| 15 | The 256 KiB default is refuted by the bundle's own O(n²) table (~45–60 s of unpreemptible CPU) | E |
| 16 | Static reference extraction is depth-1; "five predicate forms" describes an unbounded class as closed | H |
| 17 | `runtime`'s two new options, `ErrorBody`'s correlation id and the 413 status mapping have no phase and no owner | D |
| 18 | ADR-0117 Decision 3 is reversed too; only Decision 1 is annotated | G |
| 19 | 12 `examples/scenarios` mains, not 13 | D |

**A 2 · B 1 · C 2 · D 4 · E 3 · F 2 · G 2 · H 2 · I 1**

### Round 2 — `reaudit-b3` (B3 revised; 38 findings / ~13 Critical) — 17 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | D4's dominance rule still admits deny-list predicates — three executed forms return `true` on empty `vars`, one matching the ADR's own wording | H |
| 2 | The dominance rule also **denies** a correct predicate: `and` is left-associative, so the guard is not the enclosing node's left operand | H |
| 3 | The zero-reference rule is disarmed by any single ordinary reference | H |
| 4 | The `actor` axis gets zero protection — `Attributes` is a struct field that always exists, so depth-1 extraction always reports it present | H |
| 5 | D4's runtime rule **re-introduces** the upgrade-stranding D3 exists to fix, through `Attribute` instead of `Open` | E |
| 6 | A `reassign` privilege in the single shared spec bricks all four verbs — casbin applies `Privileges` unconditionally per verb | H |
| 7 | The hoisted `CheckSpecStated` is authorizer-blind but enforces an authorizer-dependent rule; the ADR contradicts itself and two prescribed tests cannot both pass | E |
| 8 | `AuthzSpec` is durable in **two** places and the migration targets the one the four `Authorize` sites do not read | **B** |
| 9 | `Open *bool` makes the zero value of the **public** `authz.AuthzSpec` fail-OPEN | I |
| 10 | `errors.As` cannot reach the jsonschema error at `ClassifyError` — `gate.go:45` flattens it with `%s`; the bundle's probe called the vendor directly | C |
| 11 | The 400 arm carries 9 sentinels and 4 strategies; the fix and its one test cover one | D |
| 12 | The hoisted gate does not close the chain: `ProcessDriver.ApplyTrigger` and `engine.NewHumanCompleted` bypass `runtime/task` | D |
| 13 | "Only 5 `NewUserTask` sites reach `model.Validate`" is one of **three** authoring forms; re-derived ≥13 in 6 files | D |
| 14 | The 274/128/5 triple was inherited verbatim in a section captioned "re-derived rather than inherited"; all three wrong | D |
| 15 | The evidence file's `??` measurement is false as labelled — the probe ran with `vars` empty under a heading declaring `vars = {"tier":"gold"}` | C |
| 16 | D2's replacement bound costs ~19 µs — more than the 866 ns ctx cost it refused — and the ADR does not disclose it | H |
| 17 | `authz/authz.go`'s own three godocs are falsified by D3 and prescribed nowhere | G |

**A 0 · B 1 · C 2 · D 4 · E 2 · F 0 · G 1 · H 6 · I 1**

### Round 3 — `audit-0186` (ADR-0186, 6 decisions; 63 findings / 33 Critical) — 26 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | D2's "20–60× worse" premise compared a worst-case bound cost against a typical-case ctx cost; two lenses measured it ~12–13× **cheaper** | C |
| 2 | The once-per-env mandate is unimplementable — `ConditionEvaluator` passes a bare `map[string]any` and D2 refuses to change the signature | H |
| 3 | `reflect.ValueOf(env).Pointer()` is unsound: 200,000 maps → 82,473 addresses, 59 % collided; the bound fails **open** | H |
| 4 | The correlation id cannot be produced by `ClassifyError` (no ctx, no config), and no phase builds the log half of the join | H |
| 5 | The 400 allow-list's **deny** half is built by no phase; all eight sentinels still render through one `err.Error()` | D |
| 6 | Phase 7's "sentinel classified 413" is built by no phase and is unschedulable as drawn | D |
| 7 | D2 bounds one of the two evaluator surfaces it itself enumerates | D |
| 8 | The static-400 default destroys actionable messages ADR-0146/0152/0183 deliberately added, with in-code rationale demanding they stay | F |
| 9 | `WithAllowedHosts` is unimplementable in `net.Dialer.Control` — the hook receives only the resolved `IP:port` | H |
| 10 | D3 silently collides with the existing `WithHTTPClient`; option order decides which is dropped | F |
| 11 | The default-ON caps can wedge an instance permanently — `httpcall` writes up to 10 MiB into `vars`, no verb shrinks variables | I |
| 12 | "Every one of the 39 decode sites wraps in `ErrBadInput`" — 36 do; 3 discard the error and return 2xx | D |
| 13 | `TestActionableViewRedactsTaskVars`, billed as the control deciding D4's placement, cannot fail — `ActionableView` has no `Vars` field | C |
| 14 | The read-path enumeration is 6 + 2 = 8; source has 6 + 2 + **3** = 11 | D |
| 15 | Redaction covers `variables` only; the snapshot also emits token payloads, incident strings, actor attributes and the definition | D |
| 16 | `SECURITY.md` is prescribed to name "the two" plaintext columns; there are at least six | D |
| 17 | The "value-free" 400 rendering is not — `InstanceLocation` renders a card number submitted as an object key | H |
| 18 | "Three options writing one field is last-writer-wins, silently" is false: both existing options document it | C |
| 19 | The spec's header and its §6 state contradictory splits of which inherited evidence holds | G |
| 20 | Above fiber's 4 MiB app limit the adapter is never reached; `MaxBodyBytes > 4 MiB` is silently ignored | H |
| 21 | fiber's `c.Body()` decompresses, so the pre-check returns 400, not 413, on the amplification case | H |
| 22 | D5 moves submitted values off the wire and onto `slog.Default()` — a sink D4 cannot redact and D6 excludes | H |
| 23 | Variables grow during execution, so D1's cap fires with no HTTP caller present and D5's static message is false there | I |
| 24 | D4's redaction hook plus its prescribed **shallow** copy mutates shared cached instance state | H |
| 25 | The D2×D3 open question resolves NO and is unwireable: the one attacker-influenced expression surface the bound cannot reach | E |
| 26 | The oversize→413 chain is unreachable at three decode sites and the three adapters diverge there | D |

**A 0 · B 0 · C 3 · D 8 · E 1 · F 2 · G 1 · H 9 · I 2**

### Round 4 — `reaudit-0186` (the fold; 56 findings / 28 Critical) — 26 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | Per-request is not per-caller: 5 admitted signal deliveries → 49,995 elements / 789 KiB, ≈61 s per evaluation | D |
| 2 | The byte bound has no affordable mechanism in `service` — `json.Marshal` costs 948 µs/op vs the element walk's 19 µs | H |
| 3 | The admission seam is not closed: `runtime.ProcessDriver` exports four more entry points; `BroadcastSignal` has no `service` equivalent at all | D |
| 4 | D2 names "three non-request sources" of `mergeVars`; there are **eight**, and one named is admission site #4 — plan tests 1 and 6 assert opposite outcomes on the same line | D |
| 5 | `authz.Actor.Attributes` is a second unbounded caller-supplied map in the ABAC env, cost-identical on the O(n²) axis | D |
| 6 | "Value-free by construction" is false: `ErrBadCursor` reflects caller strings verbatim; Evidence §1 probed `httpcore.Validate` and generalised to a different producer | C |
| 7 | Two prescribed tests are mutually exclusive — no `fmt.Errorf` form both `%w`-preserves and hides the text | E |
| 8 | The pin invariant cannot fail on its own scenario: executed mutation, pin PASSED, shipped `"rejected value 4111-1111-1111-1111"` | E |
| 9 | The static 413 destroys the message D2 mandates, and 413 has no row in the widened logging table | E |
| 10 | `callback` — a **consumer-authored** message — is blanked with no opt-in | I |
| 11 | No proxy decision anywhere; `Transport.Proxy` is non-nil by default and `Dialer.Control` sees the proxy's address (169.254.169.254 fetched 200 OK) | H |
| 12 | The IP rule fails OPEN for every IPv6 address (`To4()` nil), and the stated property and the helper list deny different sets in both directions | H |
| 13 | The escape hatch is per-network while its justification is per-service: `WithAllowedCIDRs(["10.0.0.0/8"])` admits `kubernetes.default` | H |
| 14 | The refusal's return path leaks internal IPs via `incidents[].error` on a non-admin route, mints no sentinel, and is retryable | H |
| 15 | Two phase-5 tests cannot pass (`httptest` binds loopback, which the IP rule refuses) — the plan diagnoses this for test 2 then repeats it | E |
| 16 | Refusing `WithURLExpr` + `WithHTTPClient` refuses the option's own documented use; `otelhttp.NewTransport` composes the other way | C |
| 17 | The conditional copy does not fix the aliasing defect — executed with no hook, a consumer mutated the live cached entry; the ADR asserts the opposite three times, contradicting its own Evidence §3 | E |
| 18 | Covered set is 11 read paths, the break list threads 8 functions; three direct `NewInstanceView` admin endpoints have no channel | D |
| 19 | Redacting `GetInstanceSnapshot` re-embeds the definition for `WithoutEmbeddedDefinition` consumers — the fix defeats the only existing lever | F |
| 20 | The invariant prescribed to stop the read-path count rotting a third time is blind to both mapper-less endpoints — the two the last rot added | E |
| 21 | 7 of 9 phase references are wrong; three name phases 8 and 9, which no longer exist | G |
| 22 | Phase 3's hoisted `go build ./examples/...` cannot pass — every call site is in phase 4 | E |
| 23 | The body-size histogram is mapped to `httpcore`, which never sees a body, and is truncated at the cap by `MaxBytesReader` | H |
| 24 | "No agent needs Docker" is false — D6's invariant test lands in `internal/persistence/store` (≥5 container-bound files) | C |
| 25 | Spec §5 claims "all 15 D×D pairs"; it is 13, missing D1×D4 and D4×D6 | D |
| 26 | The at-rest plaintext set is not "12 columns across 7 tables" but **18 across 8** — the sentence counted its own markdown rows; wrong in nine places; third consecutive rot of this exact enumeration | D |

**A 0 · B 0 · C 3 · D 7 · E 7 · F 1 · G 1 · H 6 · I 1**

### Round 5 — `audit3-0186` (re-cut to 3 decisions; 65 findings / 20 Critical) — 23 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | `httpcore.Validate` is called "the DTO validator, every POST/PUT on all 26 routes"; it has **3** call sites and 3 of 11 DTOs carry a `validate:` tag — that false count is the whole basis for the opt-in, and `engine` appears in no package list, no phase, no fan-out plan | D |
| 2 | The 404/409/422 "bounded residual" is not bounded: 6 of its 7 sentinels echo caller-controlled bytes | D |
| 3 | `*json.UnmarshalTypeError` is in the vouched set and embeds the caller's numeric literal (live on two routes today) | C |
| 4 | Phase 2's mechanism does not describe the code: `callback` has no `Kind`, 3 registered kinds not 4, and the literal is `"json-schema"` not `"jsonschema"` | C |
| 5 | "403 stops leaking the deployment's own policy source" is false — `eligible_expr` is marshalled into the definition and shipped on a non-admin read route | C |
| 6 | The new "outermost `ClientSafeMessage` wins" rule makes the celebrated `callback` consumer opt-in unreachable | E |
| 7 | Both prescribed "does not import transport" tests cannot fail (the import would be a compile-time cycle), and three implementations would agree only by method-name coincidence | E |
| 8 | D2's own logging table contradicts itself on 403 (row 1 logs the raw error, row 3 gates it behind a default-off option) | E |
| 9 | `keywordLocation`-only renders a missing required field as `at '/required'`, discarding a safe author-derived name | H |
| 10 | `MaxBodyBytes = 0`, the documented "unbounded" opt-out, rejects every non-empty body — and `0` is the mode the migration story mandates | H |
| 11 | "39 sites, one policy, one status" is false: a complete JSON value plus 3 MiB of trailing bytes → `err == nil`, **2xx** on stdlib/gin, 413 on fiber | H |
| 12 | Plan phase 4 test 6's stated falsifier is inverted — the gzip bomb makes the wrong implementation behave like the right one | E |
| 13 | Oversize-but-malformed is 400 on stdlib/gin and 413 on fiber | H |
| 14 | The 413 log row demands an "observed size" that does not exist — `http.MaxBytesError` carries only `Limit` | C |
| 15 | The histogram sits at `json.Decoder`, which measures what it consumed, not the body | H |
| 16 | The correlation id makes `TestParity_ErrorEnvelopes`'s byte-for-byte guarantee impossible and the plan never names it | F |
| 17 | fiber's mount WARN sits in a function the documented admin path never calls, and compares against a constant rather than the app's limit | H |
| 18 | Widened 4xx logging has no off switch ⇒ attacker-driven log volume | I |
| 19 | The at-rest enumeration walks three migration directories; there is a **fourth** — `internal/authz/casbin/migrations`, a tenth table holding the deployment's casbin policy | D |
| 20 | "48 free-form columns" is a Postgres number; SQLite has 67 (79 schema-wide) | D |
| 21 | No second column-**name** divergence exists (open question closed in the bundle's favour) but a systematic **type** divergence does, and the prescribed invariant is names-only | D |
| 22 | `SECURITY.md` "cannot disagree" with the classification, but no generator, command or drift check exists anywhere in the plan | E |
| 23 | Spec §5's "this table is complete at three, and that is the re-cut's main safety property" is false — 3 survivors **plus** 3 removals ⇒ 3 + 9 pairs, of which the bundle derives one; the controller's own brief repeated the false claim | D |

**A 0 · B 0 · C 4 · D 6 · E 5 · F 1 · G 0 · H 6 · I 1**

### Round 6 — `audit4-0186` (ONE decision; 61 findings / 24 Critical) — 16 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | "Negative `MaxBodyBytes` → a construction error at mount" has **no return channel**: `ResolveConfig`, all 15 `Customize` methods and all 6 `Mount`s return nothing, and adding one contradicts the bundle's own "no new exported interface" (4 lenses) | E |
| 2 | The histogram and rejection counter have no home and no phase; `httpcore`'s instrument fields are unexported and the ADR excludes it by name, arguing from observation sites about a declaration boundary (4 lenses) | D |
| 3 | "Unmarshal from the resulting buffer" is unspecified and the two readings disagree on under-cap trailing bytes — the bundle's own evidence validated the lenient form while the ADR prescribes the strict one (3 lenses) | E |
| 4 | "The read's own error distinguishes absent/EOF from oversize" is false — an over-declared `Content-Length` yields `unexpected EOF`, so every truncated upload ships as 413; the plan deletes the only discriminator (3 lenses) | C |
| 5 | The mount boundary is **five** functions per adapter, not one, and `AdminRoutes` is excluded from `Mount` by ADR-0095 design | D |
| 6 | `httpcore.MountGroups` — the documented consumer extension seam — has no `CustomizeOption` parameter at all, so those consumers get the 1 MiB cap with no way to raise it | D |
| 7 | `action/httpcall` already ships this exact mechanism with an incompatible convention (plain `int64`, `max <= 0` disables, default in the constructor, documented in six places) | F |
| 8 | Spec §5's `ASSUMPTION (unverified)` that `fiber.Config.BodyLimit` is unreachable is refuted — `(*fiber.App).Config()` is exported | C |
| 9 | Read-before-parse replaces return-on-first-value with wait-for-EOF and introduces an unbounded **wait**; the ADR bounds space, never time, and no `examples/` sets `ReadTimeout` | H |
| 10 | The migration procedure cannot produce its own measurement: the unbounded path has no body read, `0` is not unbounded on fiber, and 2 of 3 examples have no `MeterProvider` | E |
| 11 | The prescribed compressed-body parity case cannot pass (2xx on fiber, 400 on stdlib/gin) | E |
| 12 | The "buffering is a cost" consequence is measurably wrong — buffered is 2 % faster and allocates 37 % fewer bytes; the real cost (peak memory × in-flight) goes unstated | H |
| 13 | "The unbounded-body surface closes on all 39 sites" is false — a per-request wire cap does not bound per-instance accumulation | D |
| 14 | Phase 1 test 2's `nil`-row falsifier is vacuous: the post-loop idiom named as the mutation is correct over `*int64` | E |
| 15 | Spec §4 discharges onto ADR text that does not exist (`grep` exits 1), and the carry-forward says "slice 4" three times where its own table says slice 6 | G |
| 16 | Spec §4's coupling grid counts **removals, not couplings**: three cells hold more than one coupling and four couplings are omitted | D |

**A 0 · B 0 · C 2 · D 5 · E 5 · F 1 · G 1 · H 2 · I 0**

### Round 7 — `audit5-0186` (1 decision, stripped; 57 findings / 14 Critical) — 17 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | The cap is completely bypassed on fiber by `Content-Encoding: gzip` — `bind.go:309` calls the **decompressed** `Body()`; a 3,121-byte request parsed 3,145,761 bytes and returned 200 | H |
| 2 | `Mount` reaches only 6 of 13 decode sites per adapter; `WithMaxBodyBytes(0)` — the migration lever named in five places — leaves 21 of 39 sites at the default | D |
| 3 | `WithMaxBodyBytes(0)` does not compile as written: `CustomizeOption[R]` has `R` only in the result type, so Go cannot infer it; the repo's remedy is three new per-adapter aliases with no phase | H |
| 4 | `SECURITY.md` contains neither residual the ADR discharges onto, and one of those sentences is tagged "Verified from source" | G |
| 5 | "The parity suite structurally cannot see the admin routes" is false — `parity_test.go:663,670,677` mounts `AdminRoutes` by hand today; asserted in four places, inherited from ADR-0095 | C |
| 6 | `WithMaxBodyBytes(0)` is broken on fiber twice: the "`n <= 0` ⇒ no wrapper" rule names stdlib and gin only, and the obvious fiber pre-check returns 413 on an ordinary 1 MiB body | H |
| 7 | The slowloris hang is **created** by this delivery, and the residual that "states" it used the one fixture where old and new behave identically (0/50 handlers return; goroutines +150) | H |
| 8 | "Peak memory is `MaxBodyBytes` × in-flight" is false for a third of the surface (fiber: `BodyLimit`, 4× larger; stdlib/gin: 2.12× the cap from `io.ReadAll` doubling) | H |
| 9 | The bundle never specifies the 413's `ErrorBody` and its two documents contradict each other; `ClassifyError` sets `Message` on every arm, so a 413 does ship text | E |
| 10 | "A consumer cannot measure" is false — `wrkflw_rest_requests_total{http.status_code}` already counts every 413 with no new code (third time this lineage claimed a gap the repo had filled) | C |
| 11 | Plan test 8's falsifier is inverted **in the very sentence** saying an earlier revision had it backwards, and spec §2's rule names a fixture gzip cannot produce | E |
| 12 | Plan test 7 — the designated discharge for the bundle's one live `ASSUMPTION (unverified)` — cannot be written: the repo has zero `binding:` tags | C |
| 13 | The `httpcall.ErrBodyTooLarge` test passes today unchanged; `errors.Is` compares identity, so no naming choice can make it fail | E |
| 14 | Phase 3's prescribed fiber-divergence case is unwritable — `hitFiber` `t.Fatalf`s on exactly that condition | E |
| 15 | The strip's headline ("the four 3–4-lens findings were all ancillary, not the cap") is false both ways: two are the cap's core mechanism, and two have no multi-lens attribution at all | D |
| 16 | The carry-forward's provenance commits are unreachable from every ref (fold-don't-stack orphaning), in the one file whose job is "nothing was dropped" | G |
| 17 | The "slice 4" vs "slice 6" error round 6 found is still not fixed (3 occurrences) — a fix recorded as made and not made | G |

**A 0 · B 0 · C 3 · D 2 · E 4 · F 0 · G 3 · H 5 · I 0**

### Round 8 — `audit-0187` (ADR-0187 §AT-REST, 10 decisions; 64 findings / 17 Critical) — 18 items

One further finding (**A10**, "the ADR has nine decisions") was **rejected** — refuted by execution,
the ADR has ten — and is excluded from P2.

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | A1 — the published "every `freeform`/`policy` column is index-free" safety sentence is false: `casbin_rule_ptype_idx` exists, and `RemovePolicy` makes all seven policy columns equality predicates; `keyed` was derived over 79 columns and restated over 87 | D |
| 2 | A2 — the discovery glob finds 1 of 4 migration files (`**` is not supported by Go's `filepath.Glob`); implemented literally the census is 8 columns, not 87 | H |
| 3 | A3 — MySQL names the journal column `trigger_`, so the cross-dialect key union is 88 against an 87-key classification and the completeness guard fails permanently; the repo already solved this in `normalizeMySQLTriggerColumn` | **A** |
| 4 | A4 — the dialect-invariance guard is vacuous **by construction**: `ClassDivergences`'s `cls` argument has no dialect term (fuzzed over 200,000 inputs, zero non-empty results) | E |
| 5 | A5 — the parser is `CREATE TABLE`-only and fails **open**, so the headline consequence ("adding a column fails the build until classified") is false for `ALTER TABLE`, the most likely future shape | H |
| 6 | A6 — `Render` is nondeterministic (Go map iteration): 995 of 1000 renders differed, so the generator script can never print its success line | H |
| 7 | A7 — "`dbtest` skips when unavailable" is false; the production helpers `require.NoError` on the shared error (finding's stated mechanism corrected, conclusion accepted) | C |
| 8 | A8 — `keyed` is stated for Postgres only; the real per-dialect values are 29 / 28 / 28 | D |
| 9 | A9 — spec and plan disagree on the package and test name, and `go test -run <nonexistent>` exits 0 | G |
| 10 | A11 — the falsifiability recap is wrong in both halves ("three of five are real RED"; the table has four, and the fifth is the pin) | D |
| 11 | A12 — the spec numbers D1–D9 but its interaction pass says "ten decisions" and cites `D10` | D |
| 12 | A13 — the empty `keyed` cell cannot distinguish "column absent in this dialect" from "present but unindexed" | H |
| 13 | A14 — a reclassification breaks **two** tasks, not one (Task 5's `byClass` map also breaks) | D |
| 14 | A15 — goose stores no checksum, so an in-place migration edit never re-applies and the classification describes a *fresh* database, not a deployed one | I |
| 15 | A16 — D7's cross-check is column-names-only while all three parser traps are in the **index** path, so it does not check what justified it | E |
| 16 | A17 — Task 4's mutation exercises the wrong guard; simulated `unclassified=1, stale=0`, so even the stated expectation is wrong | E |
| 17 | A18 — Task 7 hand-rolls SQLite setup that `dbtest.RunTestSQLite(t)` already provides (Golang rule #3) and omits the driver import | F |
| 18 | A19 — the spec's own "cheap fix" for the uncross-checked-table hole appears nowhere in the plan | G |

**A 1 · B 0 · C 1 · D 5 · E 3 · F 1 · G 2 · H 4 · I 1**

### Round 9 — `reaudit-0187` (revised, **2 lenses**; 34 findings / 11 Critical) — 14 items

`B15` (MySQL's `-- +goose Down` drops 8 tables where Postgres and SQLite drop 9, leaving
`wrkflw_outbox` behind) is a **pre-existing repo defect found in passing**, filed as backlog 140,
and is excluded from P2 — it is not a defect of the bundle.

⚠ Five of these fourteen are marked **G²** — a stale copy of a corrected value that *would have
shipped*, as distinct from a cosmetic G. See §4.1.

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | B1 — the withdrawn false safety sentence survives in **four** places, including as a code instruction to the generator ("would have shipped hard-coded into `render.go` and published to consumers") | G² |
| 2 | B2 — the round-1 key-set invariant had **no true reading**: unscoped the sets are 87/79/79 and identity fails forever; scoped to `wrkflw_*` coverage fails against an 87-key classification | E |
| 3 | B3 — D2b × D7: the Docker cross-check could never go green, because the parse normalizes `trigger_` → `trigger` and live MySQL introspection returns `trigger_` | E |
| 4 | B4 — deleted symbols (`ClassDivergences`) are still prescribed in Task 4's liveness guard as a RED step, in the same file whose closing paragraph says they were deleted | G² |
| 5 | B5 — Task 5's falsifiability prose still states the withdrawn census 50 lines under code asserting the new one, and its documented route to green is restoring the `casbin_rule` skip that caused the round-1 Critical | G² |
| 6 | B6 — the pairwise recount round 1 ordered was never done: 13 decisions ⇒ 78 pairs, and no pair involves D2b, D11 or D12 | D |
| 7 | B7 — "six changed and two added" undercounted: three were added, and the omitted one is the hub the others depend on | D |
| 8 | B8 — D12's "995 of 1000" was one stochastic sample restated as a constant in three documents; re-measured 949–995 over 87 distinct render orders ⇒ a ~1-in-87 flaky green, worse than a deterministic red | C |
| 9 | B9 — "there is no pin left: all five are real RED" is refuted by its own table two rows below (third consecutive wrong quantifier in that paragraph, the second introduced by the fix to the first) | D |
| 10 | B10 — the normalization must be an exact `(table, column)` match: a `HasPrefix("trigger_")` rule would mangle `wrkflw_timers.trigger_kind` and `trigger_payload`, and no prescribed test would catch it | H |
| 11 | B11 — `wrkflw_outbox.dedup_key` is keyed solely by an inline `UNIQUE` in all three dialects, and Task 1's trap list omitted that shape — the one parser shape carrying the counts | D |
| 12 | B12 — the round-1 adjudication referenced 34 of 64 findings; **30 were never mentioned**, and one of them was still unfixed | D |
| 13 | B13 — three round-1 accepted fixes reached only one document or none | G² |
| 14 | B14 — the plan banner named the wrong set of changed tasks and the type roster kept two deleted symbols while omitting their replacement | G² |

**A 0 · B 0 · C 1 · D 5 · E 2 · F 0 · G 5 (all G²) · H 1 · I 0**

### Round 10 — `audit-0185core` (ADR-0185-core, 3 decisions; 58 raw / 22 raw Critical) — 17 items

| # | finding (glossed) | bucket |
|---|---|---|
| 1 | C1 — `checkSpecStated` fails the plan's own case 5; `EvaluatesDimension` is consulted only inside the per-dimension loop and is **structurally incapable** of rescuing the states-nothing leg, falsifying three sentences | E |
| 2 | C2a — the three durable copies of the authorization spec **do not share a shape**: two hold a marshalled `AuthzSpec` (key `Open`), the third holds `NodeWire`'s flat `eligible_*` keys and no `AuthzSpec` at all; the bundle spells the field three ways | **A** |
| 3 | C2b — the prescribed `json_set(definition,'$.nodes[#]…')` silently appends a phantom node and succeeds, after which the definition does not decode | H |
| 4 | C2c — one malformed row aborts the entire backfill on SQLite (plain `TEXT` where Postgres/MySQL are `JSONB`/`JSON`): 0 rows migrated | H |
| 5 | C2d — `TestMigrations_OneFilePerDialect` forbids a `0002` file at all; all three subtests fail and the bundle never mentions the guard | F |
| 6 | F10 — the copy-priority model is backwards for **writes**: `handleHumanClaimed` takes the task from the snapshot-derived state and `Upsert`s it wholesale over the task row, so a missed snapshot **reverts the repair** — unrecoverable loss of in-flight human work | **B** |
| 7 | F18 — ADR-0118's no-eligibility manual task dies at `go run` (EXIT=1); only ADR-0117 is amended | F |
| 8 | F3 — `definition/model/yaml.go` declares `nodeYAML`, a **second** wire struct with its own `yaml:"eligible_*"` tags, appearing zero times in the bundle ⇒ an open user task is unauthorable in YAML | **A** |
| 9 | C4 — "when human tasks are configured" is not a state that exists: a bare `service.NewProcessEngine()` already defaults a `MemTaskStore` and builds a `TaskService` unconditionally | C |
| 10 | F5 — `WithAnonymousActorAllowed()` and the empty-`Actor.ID` rejection void each other; the three demo mains cannot claim | E |
| 11 | F8 — a consumer decorator silently downgrades casbin to roles-only, undetected at wiring time | I |
| 12 | C3 — adding `EligibleOpen` breaks ADR-0187's `TestDefinitionEligibilityFieldsAreTheDeclaredSet`, which pins the `Eligible*` set to exactly three by reflection | F |
| 13 | F20 — the spec's second "vacuous 403" pin is misattributed (gin asserts 404 and has no 403 assertion; both vacuous pins are in `stdlib`) | C |
| 14 | F10ex — Task 3 Step 4's prescribed compile-breakage list is **empty**, which means no test anywhere pins the durable eligibility shape | C |
| 15 | F5c — `CustomizeConfig` has **eight** fields at `seam.go:20-80`, not the six inherited from a pre-ADR-0186 draft | D |
| 16 | C5 — the authoring gate's blast radius is misstated in **both** directions | D |
| 17 | F3/F1/F2 + interaction F5 — the enumerations were derived over the packages the author was editing and asserted over the repo | D |

**A 2 · B 1 · C 3 · D 3 · E 2 · F 3 · G 0 · H 2 · I 1**

---

## 4. Question 1 — the aggregate bucket table

**Population P2 = 193 enumerated accepted findings** across 10 rounds. Every row sums; the totals
were re-derived by script from the ten per-round lines above rather than by hand.

| round | scope | A par.repr | B dup.persist | C false premise | D enum/scope | E intra-bundle | F guard/artifact collision | G doc-only | H mechanism | I unhandled case | **n** | **A+B+F** |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| R1 `audit-b3` | B3, 5 dec | 2 | 1 | 2 | 4 | 3 | 2 | 2 | 2 | 1 | 19 | **26.3 %** |
| R2 `reaudit-b3` | B3 revised | 0 | 1 | 2 | 4 | 2 | 0 | 1 | 6 | 1 | 17 | 5.9 % |
| R3 `audit-0186` | 6 dec | 0 | 0 | 3 | 8 | 1 | 2 | 1 | 9 | 2 | 26 | 7.7 % |
| R4 `reaudit-0186` | 6 dec | 0 | 0 | 3 | 7 | 7 | 1 | 1 | 6 | 1 | 26 | 3.8 % |
| R5 `audit3-0186` | 3 dec | 0 | 0 | 4 | 6 | 5 | 1 | 0 | 6 | 1 | 23 | 4.3 % |
| R6 `audit4-0186` | 1 dec | 0 | 0 | 2 | 5 | 5 | 1 | 1 | 2 | 0 | 16 | 6.2 % |
| R7 `audit5-0186` | 1 dec stripped | 0 | 0 | 3 | 2 | 4 | 0 | 3 | 5 | 0 | 17 | 0.0 % |
| R8 `audit-0187` | 10 dec | 1 | 0 | 1 | 5 | 3 | 1 | 2 | 4 | 1 | 18 | 11.1 % |
| R9 `reaudit-0187` | revised, 2 lenses | 0 | 0 | 1 | 5 | 2 | 0 | 5 | 1 | 0 | 14 | 0.0 % |
| R10 `audit-0185core` | 3 dec | 2 | 1 | 3 | 3 | 2 | 3 | 0 | 2 | 1 | 17 | **35.3 %** |
| **TOTAL** | | **5** | **3** | **24** | **49** | **34** | **11** | **16** | **43** | **8** | **193** | **9.8 %** |
| **share** | | 2.6 % | 1.6 % | 12.4 % | 25.4 % | 17.6 % | 5.7 % | 8.3 % | 22.3 % | 4.1 % | | |

**Three groupings matter more than the nine buckets:**

| grouping | buckets | n | share | what it means |
|---|---|---|---|---|
| **Architectural** | A + B + F | 19 | **9.8 %** | the code's own shape generates these; they recur until the code changes |
| **Design-process** | C + D + E + H + I | 158 | **81.9 %** | the author asserted, enumerated, or reasoned faster than they executed |
| **Cosmetic** | G | 16 | **8.3 %** | no behavioural consequence |

### 4.1 The G split: 5 of the 16 "cosmetic" findings would have shipped

Bucket G is not homogeneous. Five findings (all in round 9, marked **G²** above) are *stale copies of
a value the same revision had already corrected somewhere else* — including one that
*"would have shipped hard-coded into `render.go` and published to consumers"* (B1) and one whose
*"documented route to making the stale version pass is restoring the `casbin_rule` skip that caused
the round-1 Critical"* (B5). Three further round-7 findings (the absent `SECURITY.md` residuals, the
unreachable provenance commits, the "slice 4"/"slice 6" error recorded as fixed and not fixed) are
arguably the same class; classified conservatively as plain G.

⇒ **Purely-cosmetic findings are 11 of 193 (5.7 %) at the strict reading, 16 of 193 (8.3 %) at the
loose one.** See §8 for what this does to question 4.

---

## 5. Question 2 — is the distribution stable, and where do the architectural buckets live?

**The distribution is NOT stable, and the instability is the most diagnostic thing in the corpus.**
The *total* is flat (§2); the *mix* moves by a factor of nine.

**A + B + F (architectural) by delivery lineage:**

| lineage | rounds | A+B+F | n | share |
|---|---|---|---|---|
| **ADR-0185 identity / authz** | R1, R2, R10 | **12** | 53 | **22.6 %** |
| ADR-0186 untrusted input | R3–R7 | 5 | 108 | 4.6 % |
| ADR-0187 at-rest posture | R8, R9 | 2 | 32 | 6.3 % |

Every one of the corpus's five **A (parallel representation)** findings and all three **B
(duplicated persistence)** findings are in the ADR-0185 lineage, except one A in R8. Concretely,
**one concept — the authorization spec — accounts for the entire architectural signal:**

- R1 item 3: eligibility is a **stored** field frozen into the task row, and `AuthzSpec` has no json
  tags ⇒ a new binary reads pre-upgrade rows as `Open == false`. (B)
- R2 item 8: *"`AuthzSpec` is durable in **two** places and the migration targets the wrong one."* (B)
- R10 item 2: the durable copies are **three**, and they **do not share a shape** — two marshal an
  `AuthzSpec` (key `Open`), the third holds `NodeWire`'s flat `eligible_*` keys and no `AuthzSpec`
  at all; the bundle spells the field three ways in its own documents. (A)
- R10 item 8: `definition/model/yaml.go` declares `nodeYAML`, a **second** wire struct with its own
  `yaml:"eligible_*"` tags, which appears **zero times** in the bundle ⇒ an open user task would be
  unauthorable in YAML, one of only two authoring forms. (A)
- R10 item 6: the write-back **inverts** the copy priority the spec assumes, so a snapshot the
  migration misses does not stay stale — it **reverts the repair**. (B)

⚠ **The count of that one architectural fact rotted three times across three rounds: 1 → 2 → 3
durable copies**, each round "correcting" the previous one and each correction wrong. `HANDOVER.md`
records the third correction as arriving from ADR-0187's `/code-review` gate, not from any of the
ten audits. That is the single sharpest illustration in the corpus of an architectural defect
manufacturing process findings: the code has three unreconciled representations, so every document
written about it contains a false count, and every audit finds one.

**The other two lineages are architecturally clean and process-noisy.** ADR-0186's five rounds
produced 108 accepted findings and **zero** A or B. ADR-0187 produced one A (R8 item 3, MySQL's
`trigger_` reserved-word alias — a genuine parallel representation of one logical column across
three dialect schemas, which the repo had *already* solved in `normalizeMySQLTriggerColumn` and the
bundle did not know).

⇒ **Answer:** architectural root causes dominate exactly one delivery lineage (ADR-0185, where they
are 22.6 % and rising to 35.3 % in the most recent round) and are near-absent from the other two.
For the repo as a whole they are **9.8 %** of accepted findings. **The ~58 is not architectural.**

---

## 6. Question 3 — does finding count correlate with bundle scope? No. Does the MIX? Yes.

**The 12× scope cut is real and is verified from the files.** ADR-0186 went, in five audited steps:
6 decisions across 6 packages (R3) → the same 6 folded (R4) → 3 decisions (R5) → **1** decision (R6)
→ 1 decision *stripped to a minimum* (R7), described by its own adjudication as
*"one option, one sentinel, one status."*

| round | decisions | findings | Critical | D enum/scope | E intra-bundle | H mechanism | A+B+F |
|---|---|---|---|---|---|---|---|
| R3 | 6 | 63 | 33 | 8 (31 %) | 1 (4 %) | 9 (35 %) | 2 |
| R4 | 6 | 56 | 28 | 7 (27 %) | 7 (27 %) | 6 (23 %) | 1 |
| R5 | 3 | 65 | 20 | 6 (26 %) | 5 (22 %) | 6 (26 %) | 1 |
| R6 | 1 | 61 | 24 | 5 (31 %) | 5 (31 %) | 2 (12 %) | 1 |
| R7 | 1, stripped | 57 | 14 | **2 (12 %)** | 4 (24 %) | 5 (29 %) | **0** |

- **Findings vs decisions: no correlation.** 63 → 56 → 65 → 61 → 57 while decisions went 6 → 1.
- **Criticals vs decisions: a real 2.4× improvement.** 33 → 14, monotone apart from R6's 24.
- **The mix moved, in three ways the total hides:**
  1. **D (enumeration/scope) fell 8 → 7 → 6 → 5 → 2** — monotone, and the only bucket that tracked
     scope cleanly. Fewer packages in frame ⇒ fewer frames to mis-derive over.
  2. **F (collision with an existing artifact) fell 2 → 1 → 1 → 1 → 0.**
  3. **E (intra-bundle logic error) rose 1 → 7 and then held at 4–5.** As the design shrank, the
     residue became *the bundle contradicting itself and prescribing tests that cannot fail* —
     R7's four E findings are all prescribed tests that are inverted, unwritable, or pass unchanged.

⚠ **A discrepancy I must state.** R6's and R7's adjudications both conclude *"every Critical is a
SCOPE-BOUNDARY failure"*, yet bucket D falls to 5 and then 2 in exactly those rounds. This is not a
contradiction — it is a definitional gap. The auditors use "scope" for *any* boundary asserted at one
level and not re-derived at the next (a mount seam, a compression layer, a config sentinel value);
bucket D as briefed is narrower — *a count or list* derived over one frame. The wider notion is
distributed here across D + H. **D + H combined is 17 (65 %) in R3 and 7 (41 %) in R7** — falling,
but far less than D alone suggests. Read the auditors' "scope" claim against **D + H**, not D.

⇒ **Answer:** the count is scope-independent; the Critical rate and the bucket mix are not. The
project's own conclusion — *"the finding rate is a property of the PROCESS, not of the bundle"*
(R7 adjudication) — is right about the total and, per §2, understates the cause: it is a property of
the **audit** process specifically, not of the design process.

---

## 7. Question 4 — how much of "58 findings" is noise? Less than you fear, but the answer is layered

Three independent readings, all pointing the same way:

**(a) In the classified population (P2, Critical-biased):** cosmetic findings are **16 / 193 =
8.3 %**, or **11 / 193 = 5.7 %** once the five round-9 stale-copies that would have shipped are moved
out of "cosmetic". So of the enumerated accepted findings, **~92 % carry a behavioural or design
consequence.**

**(b) In the raw per-lens populations (P1), by the auditors' own severity labels** — only two rounds
publish a full three-way split:

| round | findings | Critical | Major | Minor | Minor share |
|---|---|---|---|---|---|
| R5 `audit3-0186` | 65 | 20 (31 %) | 28 (43 %) | 14 (22 %) | 22 % |
| R6 `audit4-0186` | 61 | 24 (39 %) | 26 (43 %) | 11 (18 %) | 18 % |

⚠ **R5's own table does not sum**: 20 + 28 + 14 = 62 against a stated 65, because its counting-lens
row is `13 (+3 clean)` with only 10 severity-labelled entries. A three-finding discrepancy inside an
adjudication that exists to police enumerations. It does not change the conclusion, and it is
recorded because leaving it unremarked in *this* document would repeat the corpus's signature defect.

**(c) Rejection rate — the sharpest number, and it argues against the noise hypothesis.** The
adjudications rarely reject anything:

- R1: *"all 12 accepted; none rejected"* · R2: *"All accepted; none rejected"*
- R8: 1 rejected of 17 Criticals (A10, refuted by execution) · R10: *"21 of 22 raw Criticals are
  accepted"*, the 22nd accepted in substance and corrected in detail

⇒ **Across the four rounds that state it, 65 of 67 raw Criticals were accepted (97.0 %)** — R1 12/12,
R2 16/16 raw, R8 16/17, R10 21/22. (On R2's *de-duplicated* count of ~13 it is 62 of 64, 96.9 %; I
mis-stated this as "62 of 63" on the first pass and caught it re-deriving the sum, which is the
corpus's own recurring defect committed inside its meta-analysis.) These are
not nitpicks that a tired controller waved through — each was independently re-derived by the
controller before acceptance (R8's header states this explicitly, and R10's convergence table shows a
controller check per row).

**⇒ Answer:** "58 findings" is **not** 15 defects and 43 nitpicks. It is closer to **~50 real
findings and ~8 cosmetic ones per round**, of which roughly **20 (est., extrapolating the P2
Critical share) are decision-changing**. The reason the count feels like noise is not that the
findings are false — it is that **they are inexhaustible**: the process generates ~15 per agent from
any artifact, so no amount of fixing lowers the next round's total.

**⚠ Contrast data point — the same repo audited at the CODE level.** The 119-item backlog sweep's
four triage records classify **122 items** (S small / D design / A adjudicated-away; the 122 vs 119
gap is the triage files' own overlap and is not reconciled here — treat 122 as the count of tier
verdicts, not of distinct items). **17 of 122 (13.9 %) were adjudicated "not a defect / closed /
duplicate / trap"** — a rejection rate **~9× higher** than the design audits'. Findings against
shipped code are *more* likely to be wrong than findings against design documents, because the code
can answer back. That is the inverse of the intuition that design audits produce noise.

---

## 8. Question 5 — recurrence: the same six root causes, delivery after delivery

**They are the same, and the corpus says so in its own voice.** Six recur in three or more rounds:

**R1 — The wrong grep net (bucket D). Non-zero in ALL TEN rounds (min 2, max 8); named explicitly as
a recurring net failure in seven of them.** Not bad arithmetic — every adjudication that
comments says the arithmetic was right; *"the arithmetic was right for the SEVENTH consecutive
round"* (R7). The defect is always the **net** or the **frame**:
`ReassignInput.By` tagged `"by"` hid six pins (R1) → `NewUserTask(` is one of three authoring forms
(R2) → *"the net was wrong again — third consecutive round"* (R3, 36 of 39 decode sites) → *"the
third consecutive rot of this exact enumeration, inside the paragraph warning about it"* (R4, at-rest
columns 2 → 6 → 12 → 18) → a fourth migration directory the glob never saw (R5) → `keyed` derived
over 79 columns and asserted over 87 (R8) → *"the enumerations were derived over the packages the
author was editing and asserted over the repo"* (R10).

**R2 — A prescribed test that cannot fail (bucket E). 7 of 10 rounds.** R3 (`ActionableView` has no
`Vars` field, in the test billed as *"the control that decides D4's placement"*) → R4 (the pin
invariant passed over its own scenario under executed mutation; **and** the invariant written to stop
the read-path count rotting was blind to the two endpoints the last rot added) → R5 (both
"does not import transport" tests are compile-time-impossible) → R6 (the `nil`-row falsifier is
vacuous) → R7 (**four in one round**: inverted, unwritable, passes-unchanged, unwritable) → R8 (a
guard vacuous *by construction*, fuzzed over 200,000 inputs) → R10 (Task 3's prescribed
compile-breakage list is empty). The repo already records this as
`[[mutation-verify-load-bearing-tests]]`; it has not stopped.

**R3 — The stand-in probe: measure one producer, generalise to another (bucket C). 4 rounds.**
R2 (`§6`'s jsonschema probe called the **vendor**, not the repo's `gate.go` wrapper — and the
`store_core.go` evidence was against the copy the `Authorize` sites do not read) → R4 (Evidence §1
probed `httpcore.Validate` and generalised to the 36 decode wrap sites, *"the stand-in failure the
same evidence file documents as this repo's signature defect, committed one layer from where it was
documented"*) → R6/R7 (the narrow-fixture form: *"the bundle's probes are narrow in a consistent
direction: toward the fixture that demonstrates the fix"*).

**R4 — Claiming a gap the repo had already filled (bucket F). 5 rounds, and the corpus counts them
itself.** R5 (fourth migration directory) → R6 (`action/httpcall` already ships the exact cap
mechanism with an incompatible convention) → R7 (*"third time this lineage asserted a gap the repo
had already filled"* — `wrkflw_rest_requests_total` already counts every 413) → R8 (*"fifth instance
in this lineage"* — `normalizeMySQLTriggerColumn` already exists in the very file cited as the
convention being reused) → R10 (`dbtest.RunTestSQLite` hand-rolled; `TestMigrations_OneFilePerDialect`
unmentioned).

**R5 — Corrected where defined, left where consumed (bucket G²). Named in R9, visible in R7.** R9's
headline: *"seven of counting's seventeen and five of interaction's seven Criticals are one defect: a
number or decision was fixed, and the sentence describing it was not — always within 60 lines."*
R7 found the same shape one round earlier (`"slice 4"` vs `"slice 6"`, *"a fix recorded as made and
not made"*). The remedy the corpus adopted — *after writing a corrected value, `grep` for the old
one* — would have caught all seven.

**R6 — The celebratory sentence (buckets C and E).** R4's diagnosis, in the interaction lens's own
words: *"The revision minted absolute claims to celebrate its own fixes, and wrote them against
premises its other fixes had already changed."* It recurs verbatim: R5's I18 (*"this table is
complete at three"* — false, and the controller's own audit brief repeated it as fact), R7's M1 (the
strip's headline false in both directions), R9's B9 (*"all five are real RED"* refuted by its own
table two rows below — *"the second [wrong quantifier] introduced by the fix to the first"*).

⇒ **Answer:** six recurring root causes, not a shifting set. Five of the six are **process** classes
(C, D, E, F, G²) that the code cannot fix. Only one — the authorization spec's three unreconciled
durable representations (§5) — is architectural, and it is confined to one lineage.

---

## 9. Question 6 — recap of the effort finding (full detail in §2)

Findings vs lens count: **r = 0.855, r² = 0.73** over 10 rounds. Seven 4-lens rounds spanning a 12×
scope cut return **15.14 ± 0.83 findings per lens (CV 5.5 %)**. The two off-trend rounds are both
dispatch artifacts: round 9 ran **two lenses by owner decision** and returned half the findings at an
*above*-average per-lens rate; round 2 is the only round that visibly de-duplicated before publishing
its headline. Per-lens-type means over the five rounds that publish a breakdown: execution 14.8,
failure-modes 16.5, counting 14.0, interaction 17.2 — four differently-briefed agents, one narrow
band.

**No token budget or wall-clock duration is recorded anywhere in the corpus**, so lens-report length
is the only available effort proxy beyond lens count; it correlates with **Criticals** (r = 0.81)
more strongly than with findings (r = 0.54). Rounds where agents wrote more escalated more. That is a
second effort artifact, and it means the *severity* signal in §6 should be read with the caveat that
part of the 33 → 14 Critical decline coincides with a 1,005 → 576 lines-per-lens decline.

---

## 10. The conclusion, in one paragraph

**The ~58 is an instrument reading, not a defect count.** A four-lens adversarial pass over any
design bundle in this repo returns 60.6 ± 3.3 findings because each Opus lens returns ~15, and it
will keep returning ~58 no matter how small the bundle gets — round 6 (one decision) and round 7
(one decision stripped to a minimum) were the control experiments and the count did not move. So:
**stop reading the total.** Underneath it, two signals were real and were hidden by watching the
wrong number: Criticals per lens fell **8.25 → 3.50** across the ADR-0186 scope cut (a genuine 2.4×
quality improvement that the flat total concealed), and the bucket mix shifted decisively — the
enumeration/scope class fell 8 → 2 while the self-contradiction class rose and held. On the owner's
three-way question the answer is **~10 % architectural, ~82 % design-process, ~8 % cosmetic**, and
the architectural tenth is not diffuse: it is one concept — the authorization spec, durable in three
unreconciled shapes with no machine-checked correspondence — which by itself produced every A and B
finding in the corpus and whose own copy-count rotted 1 → 2 → 3 across three consecutive rounds. Two
actions follow directly from the numbers and nothing else does: **(1)** fix the *one* architectural
defect (a single canonical representation of `AuthzSpec` with a generated/pinned correspondence
across the task row, the instance snapshot and `NodeWire`) — that is the only change that removes
findings permanently rather than deferring them; **(2)** convert bucket D, the largest single class
at 25.4 %, from prose into machine-checked derivations (the corpus already names the working
pattern — `engine/terminal_sites_test.go`'s `go/parser` walk — and R4's adjudication already
prescribed it and it was not adopted). Everything else on the list — including auditing harder,
splitting further, or expecting convergence — has now been tried and measured, and the measurement
says it does not work.
