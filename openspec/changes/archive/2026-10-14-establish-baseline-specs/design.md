# Design

## Context

The specs were drafted from the 63 closed issues, the kartaladev/wrkflw#20 register and exported godoc. Then an adversarial audit by four independent reviewers checked them against HEAD, and the findings were applied. Every scenario row is mapped to a test in `evidence.md`. Each named test ran green on go1.26.8 (the `go.mod` version), with Docker for the Postgres, MySQL and SQLite rows. Rows that no test asserts are marked `NO TEST` and become characterization tests (tasks group 2).

The archived `adopt-openspec-workflow` design (D9) records why a bulk baseline is written at all, against OpenSpec's brownfield guidance: the migration changes need requirements to MODIFY.

## Goals / Non-Goals

**Goals:**
- Every requirement is a promise that is decided, shipped and demonstrated by a test at HEAD.
- Each promise has exactly one home capability, and capabilities are shaped so the migration changes extend them rather than inventing near-duplicates.
- The baseline is executable evidence: every scenario row names the test that proves it.

**Non-Goals:**
- Fixing anything. A promise that turns out false is removed and becomes its own change.
- Specifying behavior an open issue corrects or decides, or decided-but-unbuilt designs (the #22 driver split, #23 route table as data, #25 enum names, #29 user-task updates, #30 module topology, #31 GORM adapter, and #114's casbin removal and claimant-only tasks, which open #115 carries).
- Baselining the #20 cross-cutting lessons, CI, lint or test hygiene.

## Decisions

### D1. Admission rule: decided, shipped, demonstrated

A requirement is admitted when a closed issue, a #20 register entry or an **exported** godoc contract states it, the code at HEAD does it, and a test shows it. A true but untested row is kept and gets a characterization test (D4). Anything an open issue is about is left out, even the half that works: the requirement is narrowed to what survives the issue, or dropped. That way the issue's migration change can ADD or MODIFY it without contradicting the baseline. Examples applied in the audit:
- task authorization is stated at the boundary that survives #106 ("refused with `authz.ErrNotAuthorized`, state unchanged"), not as "the task service evaluates";
- the parallel join is specified only for tokens that crossed a flow (#216, #79);
- `MaxAttempts` 0 is left to #139;
- actor disclosure is limited to actor IDs (#199);
- the `rule` key keeps only refusals that #118 and #184 leave standing.

*Rejected:*
- **Baseline observed behavior too.** It would freeze defects as promises.
- **Admit internal godoc.** "A condition over an absent variable is not satisfied" had only internal godoc behind it, and it contradicts "silence is never correct"; it was dropped and is a D7 finding.

### D2. Twenty-one capabilities, one home per promise

The capabilities are shaped for the changes that follow:
- **`restart-recovery`**, not `command-recovery` or `timer-rehydration`. Timers are what ships; the changes for #181 (waiters) and #110/#167/#182 (commands) extend it.
- **`instance-store`**, not `persisted-state-format`. Format promises (#44, #138) live with the store that persists them: `instance-store`, `definition-store`, the task store in `human-task-lifecycle`, and the outbox in `event-delivery`.
- **`instance-chaining`** is split from `event-delivery`, because starting a successor instance is not delivery.
- **`instance-lifecycle`** is new. It holds shutdown drain, duplicate start, dropped cancel, ambiguous message start and the token/terminal distinction.
- **`instance-access-control`** is not created. Its one shipped promise, unauthenticated write as the default posture, lives in `http-transport`. #129 concerns reads and will MODIFY `instance-disclosure`; #108 creates `instance-access-control` for operation authorization.
- **Each duplicate was resolved to one home:**
  - `definition-wire-format`: the `termination_outcome` decode refusal;
  - `human-task-authorization`: every task-route identity requirement (401/503 and refusal before the body is read);
  - `operator-incidents`: the boundary-not-armed warning;
  - `typed-action-input`: a typed decode failure being terminal;
  - `definition-validation`: expr validation reconstruction.

Measured, not argued: unauthenticated signal delivery is part of the default posture, because the per-adapter bare-mount tests deliver a signal with no actor and get 200.

### D3. A refusal with no exported sentinel says "an error naming X", and is a finding

Some decided refusals return an error the code does not export as a sentinel. Examples: an unknown kind name, expression compile errors, the expression timeout, the httpcall hop limit and body validation, and the eventing payload marshal failure. The project rule now allows "refused with an error naming <thing>" in exactly that case (`openspec/config.yaml`, specs rules). Each such row is recorded in D7 as a sentinel to export.

*Rejected:* **Drop those rows.** That would remove decided promises such as "an unknown key is refused" (#47) from the baseline.

### D4. Missing evidence is closed by characterization tests, and a red one removes the row

Each `NO TEST` row gets a characterization test, expected green because the behavior was measured at HEAD by a probe or by reading code. A failing one is proof of a defect (CLAUDE.md, *Defects need proof*): the row is removed and the failing test seeds a new change. Nothing is fixed here.

### D5. Verify on the declared toolchain, with real dialects

Every verification runs on the toolchain `go.mod` declares: go1.26.8 when the baseline was measured, go1.26.9 after kartaladev/wrkflw#243, on which CI re-ran the whole suite before archive. On go1.27 two tests fail on error-message text, which open #235 covers, and that would otherwise read as baseline failures. Dialect rows run against Docker; a row that is false on one dialect is removed rather than archived.

### D6. `evidence.md` is the scenario-to-test map

`evidence.md` holds one table per capability. Each row has: requirement, scenario, row, test, and whether it ran green. Specs stay free of test names (they state behavior). The map is what a reviewer re-runs, and task 3.1 requires it to hold no `NO TEST` row before archive.

### D7. What the baseline found, grouped into follow-up changes

After this change is archived, each group becomes a change that depends on it (Workflow follow-up in tasks.md). They wait for archive because a proposal-only change fails `openspec validate --strict`, the MODIFIED deltas need the baseline specs to exist, and groups 2 and 3 need decisions first:
1. **Defect: JSON accepts case-variant keys.** `"ID"`, `"Name"` and `"Target"` decode without error, while YAML refuses them; no open issue covers it.
2. **Decide silent expression outcomes:**
   - a condition over an absent variable evaluates to false;
   - a timeout on a boundary error expression becomes a non-match instead of a failure;
   - the timeout error is internal.
3. **Align godoc with behavior.** The spec follows the tests in each case:
   - the `chain.WithChainLinks` and `Handle` godoc say a recorded link skips the start; it is retried;
   - the in-memory registry refuses an identical re-registration, while #111 decided publish is idempotent-if-identical (needs a decision);
   - the `ErrDuplicateMessageStart` and `RegisterDefinition` godoc say any collision; only each other definition's latest version is compared;
   - the `RetryPolicy.MaxInterval` godoc says ≤0 disables the cap, but normalization replaces it;
   - the `ErrCancelNotApplicable` godoc says it applies during a terminal walk, while a test pins nil there;
   - the `runtime.WithTimerStore` godoc describes the retired AppliedStep path;
   - a start-timer comment says each version arms independently, while the test asserts only the latest fires (unverified);
   - the httpcall "downgrade reported in preference to cross-host" rule is in an internal comment only.
4. **Export refusal sentinels for the D3 rows:**
   - reassignment by a non-claimant;
   - unresolvable boundary target;
   - unevaluable attribute predicate;
   - unknown and retired kind names;
   - expression compile, type, duration and correlation-key errors;
   - expression timeout;
   - expr validation predicate failure;
   - httpcall hop limit and body validation;
   - eventing payload marshal failure.
5. **Strengthen assertions that cannot fail.** These belong with the #227 class:
   - no-mutation checks that compare empty against empty through `Step`;
   - the `TestChainerHandle` transient row;
   - `TestXID` ignoring the second call's error;
   - the gin 5xx test;
   - the fabricated-error 500 test;
   - the asymmetry test that bypasses the decoders.
6. **Guard `#### Scenario Outline:` headers in `scripts/check-openspec.sh`.** OpenSpec does not parse them as scenarios and validation still passes. It happened once while drafting.

## Closed-issue dispositions

All 63 closed issues on kartaladev/wrkflw:
- **B:** baselined (shipped promise, in the named capability).
- **P:** partly baselined; the rest is owned by the named open issue.
- **U:** decided but unshipped; the named open issue builds it.
- **H:** hygiene, CI, test or doc work with no library behavior.

| Issue | Disposition | Where |
|---|---|---|
| kartaladev/wrkflw#21 | P | `http-transport` posture, `human-task-authorization`; rest #106, #176 |
| kartaladev/wrkflw#22 | U | #39 |
| kartaladev/wrkflw#23 | U | #40 |
| kartaladev/wrkflw#24 | B | `event-delivery` |
| kartaladev/wrkflw#25 | U | #44 |
| kartaladev/wrkflw#26 | B | `definition-validation`, `definition-wire-format` |
| kartaladev/wrkflw#27 | B | `definition-wire-format` |
| kartaladev/wrkflw#28 | U | #41 |
| kartaladev/wrkflw#29 | U | #41 |
| kartaladev/wrkflw#30 | U | #39, #48 |
| kartaladev/wrkflw#31 | U | #42 |
| kartaladev/wrkflw#32 | H | |
| kartaladev/wrkflw#33 | H | |
| kartaladev/wrkflw#34 | H | |
| kartaladev/wrkflw#35 | H | module-graph hygiene (no test doubles in production build) |
| kartaladev/wrkflw#36 | B | `outbound-http-calls` |
| kartaladev/wrkflw#38 | B | `operator-incidents`, `token-routing-and-joins` |
| kartaladev/wrkflw#43 | B | `http-transport`, `instance-disclosure` |
| kartaladev/wrkflw#45 | B | `event-delivery` |
| kartaladev/wrkflw#46 | B | `definition-validation` |
| kartaladev/wrkflw#47 | B | `definition-wire-format` |
| kartaladev/wrkflw#49 | H | |
| kartaladev/wrkflw#52 | H | |
| kartaladev/wrkflw#53 | B | `definition-validation` |
| kartaladev/wrkflw#54 | B | `operator-incidents` |
| kartaladev/wrkflw#61 | H | |
| kartaladev/wrkflw#65 | H | |
| kartaladev/wrkflw#66 | H | |
| kartaladev/wrkflw#67 | B | `outbound-http-calls` |
| kartaladev/wrkflw#69 | B | `http-transport` |
| kartaladev/wrkflw#71 | B | `http-transport` |
| kartaladev/wrkflw#74 | P | `http-transport` (once per request, gin scope); fiber scope #40 |
| kartaladev/wrkflw#78 | B | `operator-incidents` |
| kartaladev/wrkflw#80 | H | |
| kartaladev/wrkflw#84 | B | `outbound-http-calls` |
| kartaladev/wrkflw#86 | H | |
| kartaladev/wrkflw#90 | H | |
| kartaladev/wrkflw#92 | H | |
| kartaladev/wrkflw#94 | H | |
| kartaladev/wrkflw#99 | H | |
| kartaladev/wrkflw#107 | B | `human-task-authorization` |
| kartaladev/wrkflw#111 | B | `definition-store` |
| kartaladev/wrkflw#112 | P | `definition-store`, `definition-validation`; rest #151, #152, #154, #157 |
| kartaladev/wrkflw#113 | B | `definition-validation` (JSON Schema and expr kinds); guard #124 |
| kartaladev/wrkflw#114 | P | `human-task-authorization` (composed deciders); casbin removal and claimant-only #115 |
| kartaladev/wrkflw#117 | P | `typed-action-input`; rest #137, #142, #143, #144 |
| kartaladev/wrkflw#119 | U | #115, #121 |
| kartaladev/wrkflw#120 | P | `token-routing-and-joins`; relocated-token join #216 |
| kartaladev/wrkflw#132 | B | `event-delivery` |
| kartaladev/wrkflw#133 | B | `event-delivery` |
| kartaladev/wrkflw#134 | B | `instance-chaining` |
| kartaladev/wrkflw#141 | P | `expression-evaluation`; reserved names #187 |
| kartaladev/wrkflw#145 | H | measurement only |
| kartaladev/wrkflw#146 | H | |
| kartaladev/wrkflw#147 | B | `definition-wire-format` |
| kartaladev/wrkflw#148 | H | |
| kartaladev/wrkflw#150 | B | `process-variables`, `typed-action-input` |
| kartaladev/wrkflw#153 | H | 64-bit only, stated in CONTRIBUTING.md |
| kartaladev/wrkflw#156 | B | `outbound-http-calls` (pool sizing) |
| kartaladev/wrkflw#183 | B | `definition-wire-format` |
| kartaladev/wrkflw#209 | H | |
| kartaladev/wrkflw#212 | B | `token-routing-and-joins` |
| kartaladev/wrkflw#214 | B | `definition-wire-format`, `token-routing-and-joins` |

## Risks / Trade-offs

- **[A misread survives the audit]** → Every scenario row names a test in `evidence.md`, and task 1.1 re-runs them all before archive.
- **[The baseline goes stale as migration changes land]** → Each migration change MODIFIES the requirement it changes in the same PR, and archive syncs it.
- **[Over-exclusion leaves capabilities thin]** `transaction-seam` (3 requirements) and `instance-id-generation` (4) are small. → Accepted; the changes for #109 and #128 extend them.
- **[`evidence.md` drifts from the specs]** → Task 3.1 checks it, and it is archived with the change as the record of what was proven at baseline time; it is not maintained afterwards.

## Coupling

- Every migration change that MODIFIES a baseline requirement lists `establish-baseline-specs` in `Depends on`.
- `openspec/config.yaml` gains the D3 sentinel rule in this change; `adopt-openspec-workflow` (PR #242) owns the rest of that file.
- After archive, kartaladev/wrkflw#20 is commented with a pointer to `openspec/specs/` and closed.
