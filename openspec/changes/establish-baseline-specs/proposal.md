# Proposal

## Why

`openspec/specs/` is empty, so the promises wrkflw already keeps live only in 63 closed GitHub issues, the kartaladev/wrkflw#20 "Decisions so far" register and godoc, and the coming issue migration has no requirement to MODIFY. This change records those promises, and only those, as baseline capability specs.

## What Changes

- **Record shipped promises as capability specs.** A promise is baselined when it is decided (a closed issue, a #20 register entry, or an exported godoc contract), true at HEAD, and demonstrated by a test. It is measured against the code, not taken from issue text.
- **Leave defects and unshipped decisions out.** Behavior an open issue exists to correct or decide is not specified, even the half that works; the requirement is narrowed to what survives the issue, or dropped. Decided-but-unbuilt designs belong to the change that builds them: kartaladev/wrkflw#22, #23, #25, #29, #30, #31, and #114's casbin removal and claimant-only tasks (carried by open #115).
- **Map every scenario to its test.** `evidence.md` names the test behind every scenario row, and each named test ran green on go1.26.8 with real Postgres, MySQL and SQLite. Rows no test asserts are marked `NO TEST`; implementation adds a characterization test for each. A red one proves a defect: the row is removed and seeds a follow-up change, and nothing is fixed here.
- **Allow a refusal with no exported sentinel to be stated as "an error naming X".** The specs rule in `openspec/config.yaml` gains this exception, and each such row is recorded as a sentinel to export (design D3).
- **Record what the baseline found.** One defect (JSON accepts case-variant keys), two undecided silent behaviors in expressions, eight godoc and behavior disagreements, the sentinels to export, assertions that cannot fail, and one OpenSpec tooling gap. Each group becomes a follow-up change (design D7).
- **Close the #20 register.** After archive, kartaladev/wrkflw#20 is commented with a pointer to `openspec/specs/` and closed. Its cross-cutting process lessons are not library behavior and are not specified.

Every one of the 63 closed issues has a disposition in design.md: baselined, partly baselined with the rest owned by an open issue, decided-but-unshipped, or hygiene.

**Not affected:** nothing here is BREAKING. No persisted format, default security posture or consumer `go.mod` changes; only tests and specs are added.

**Out of scope**
- Migrating the 85 open issues. Each becomes or joins a change that cites it, in step 3 of the move to OpenSpec.
- CI, lint, test-hygiene and doc-rot issues among the closed 63 (for example kartaladev/wrkflw#34, #49, #61, #65, #66, #80, #86, #90, #92, #94, #99, #146, #148, #153, #209); they change no library behavior.
- Fixing any defect or godoc disagreement the baseline found; those are the D7 follow-up changes.

**Coordination**
- Size: XL.
- Depends on: `adopt-openspec-workflow`.
- Blocks: nothing.
- Readiness: agent.

## Capabilities

### New Capabilities

- `definition-wire-format`: how a process definition is encoded and decoded as JSON and YAML, which keys each node kind accepts, and the refused vocabularies.
- `definition-validation`: the authoring gate a definition passes before it can run, structural refusals, and the declarative validation kinds.
- `definition-store`: publishing, versioning and looking up definitions, including immutability and what "latest" means.
- `token-routing-and-joins`: how tokens move along flows, through gateways and joins, boundary events and force-termination ends.
- `operator-incidents`: when a stuck or failed instance raises an incident or a warning, and what operators may do about it.
- `instance-lifecycle`: starting, cancelling and draining instances: shutdown drain, duplicate start, dropped cancel, ambiguous message start.
- `instance-chaining`: starting a successor instance when an instance ends.
- `process-variables`: how process variables are captured, merged, reserved and restored.
- `expression-evaluation`: how expressions in definitions are evaluated, what they can reach, and the opt-in timeout.
- `typed-action-input`: typed action input and output, and strict versus lenient input decoding.
- `retry-policy`: how failed actions are classified, retried and stopped.
- `human-task-authorization`: who may claim, complete or refresh a human task, how identity is established on task routes, and how misconfiguration fails closed.
- `human-task-lifecycle`: the states a human task moves through and the operations that move it.
- `instance-disclosure`: what instance and task data each caller is shown.
- `http-transport`: the HTTP adapters' observable contract: default posture, routes, error bodies, headers, body caps, middleware and health.
- `outbound-http-calls`: the HTTP client behavior of actions that call out, including redirect and credential handling.
- `event-delivery`: publishing, subscribing and the transactional outbox relay.
- `instance-store`: what a durable instance store keeps, optimistic concurrency, history capping and schema migration.
- `transaction-seam`: how a step's writes commit or roll back together.
- `restart-recovery`: what a committed step's side effects recover after a restart; armed timers today.
- `instance-id-generation`: how instance identifiers are generated.

`instance-access-control` is not created: its one shipped promise lives in `http-transport` (design D2).

### Modified Capabilities

None. No capability spec exists yet.

## Impact

- **Specs:** creates the 21 capability specs above under `openspec/specs/` at archive.
- **Evidence:** `evidence.md`, archived with the change.
- **Config:** one rule change in `openspec/config.yaml` (design D3).
- **Tests:** characterization tests for the `NO TEST` rows only. No production code changes.
- **GitHub:** kartaladev/wrkflw#20 is commented and closed after archive.
