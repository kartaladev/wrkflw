# Proposal

## Why

Once `add-user-task-updates` lands, a library consumer can submit and read updates. An HTTP client still cannot, and it has no way to read a single task at all: the adapters mount only `POST /tasks/{token}/claim`, `/complete` and `/reassign` (`transport/http/stdlib/groups.go:171,203`, and the fiber twin at `transport/http/fiber/groups.go:178,200,223`). The completability check that kartaladev/wrkflw#29 required ("`GET /tasks/{token}` (fields `completable`, `missing_updates`)") needs a read surface, and the audit table is reachable only through SQL until a history route exists (#29 round 2, Q7).

## What Changes

This is part 3 of 4 of kartaladev/wrkflw#41 ("Build the UserTask updates feature"): #41 section 4 (HTTP), plus the HTTP half of section 6's per-field disclosure tests. The design is kartaladev/wrkflw#29 (decided: Q7 (a), audit A5 (a) and A10, and the Decision comment's HTTP and disclosure paragraphs), applied and not reopened. Each route is one `Route` entry in the route table that `unify-http-routes-as-data` builds (kartaladev/wrkflw#23, decided), so stdlib, gin and fiber get it from one definition.

- **New `POST /tasks/{token}/updates`.** The body is `{"update": "<name>", "input": {...}}`. The answer is 202 with `{"seq": n}`. Identity is resolved before the body is read and fails closed exactly as on the other task routes, and the actor comes from the resolver only. The submission is authorized as `OpProgress` by the driver. One update per request (a list body was rejected on #29 because N actions have no clean partial failure).
- **New `GET /tasks/{token}`.** It returns the task projected through the deployment's `DisclosureSet`, read from the instance snapshot (the authority), plus `completable`, `missing_updates`, and `updates[]` with one entry per declared update: `{name, required, depends_on, submittable, pending, applied, validation}`.
- **New `GET /tasks/{token}/updates`.** It returns the task's update history in seq order.
- **Disclosure for unidentified callers** (#29 A10): `validation` under `DisclosePolicy`, `input` and `value` under `DiscloseVariables`, `actor` under `DiscloseActors`, and `reason` (consumer error text) under `DiscloseOperations` (pending Q1, below). Structural fields (`name`, `seq`, `status`, `submitted_seq`, `occurred_at`, `required`, `depends_on`, `submittable`, `completable`, `missing_updates`) are always shown, like the instance skeleton.
- **Error classification** for the core change's sentinels. `engine.ErrUnknownUpdate` answers 400 `bad_request`. `ErrUpdateInFlight`, `ErrUpdateDependencyMissing`, `ErrRequiredUpdatesMissing` and `ErrUpdateRefused` answer 422 `conflict_state` through their `ErrInvalidTransition` wrapping. A 422 for the two typed errors names the missing updates in the message. Each new arm states its position relative to the arms it can co-match, as `httpcore/errors.go:139-149` requires.
- **Route docs.** The noun `/updates` route sits beside the three verb routes. One line explains the divergence (#29 A5 (a)).

Re-measured on main (`b8576841`):
- **Holds:** three task routes per adapter, and no task `GET` on any adapter (`grep '"/tasks' transport/http` shows only claim, complete and reassign).
- **Holds:** disclosure applies only to callers the resolver does not identify. An identified caller sees every field (`openspec/specs/instance-disclosure`). The categories `DiscloseVariables`, `DiscloseActors`, `DiscloseOperations` and `DisclosePolicy` exist (`authz/disclosure.go:23,35,55,59`).
- **Conflict found:** `DiscloseOperations` today "restores incident IDs and the compensation cursor **without error text**" (instance-disclosure, "Each disclosure category widens only its own fields"). Incident error text "stays withheld unless `DiscloseVariables` is also set" (`authz/disclosure.go:52-54`), because it may embed process variables. #29 A10 places the rejection `reason`, which is the same kind of consumer error text, under `operations` alone. This is Q1.
- **Holds:** the classifier maps `ErrInvalidTransition` to 422 `conflict_state` and payload sentinels to explicit 400 arms (`transport/http/httpcore/errors.go:135-178`).

Impact flags:
- **BREAKING:** none. All three routes are new.
- **Persisted format:** none.
- **Default security posture: a new write route and two new read routes.** The write route fails closed on identity and authorizes at the transition. The read routes serve unidentified callers the structural projection only, the same posture as the instance routes. Q1 decides whether error text can reach an unidentified caller under `operations`.
- **Consumer go.mod:** none.

Out of scope:
- The trigger, the state machine, the table and the readers: `add-user-task-updates` (part 2).
- Task outbox events: `add-user-task-updates-outbox` (part 4).
- `Cache-Control: private, no-store` on disclosing routes: `define-access-and-disclosure` (kartaladev/wrkflw#127). Whichever of the two lands second applies the directive to these routes (design, coupling).
- A request-level idempotency key on human routes: off-map on #29 (A11), not filed.
- Refusing unknown request-body fields: recorded by `unify-http-routes-as-data` as a finding with no owner.

Decisions this change cannot be fully specified without (design, Open Questions):
- **Q1.** Under which set `reason` reaches an unidentified caller. The options are `operations` alone (#29 A10 as written), or the rule that already governs incident error text, where `operations` restores error text only together with `variables` (`authz/disclosure.go:52-54`). Recommended: the incident rule. The `reason` requirement is written for it and marked *pending Q1*.
- **Q2.** Paging of `GET /tasks/{token}/updates`. Recommended: the same `limit` and `cursor` parameters as the instance list routes, in ascending seq. Specs mark the paging rows *pending Q2*.
- Inherited from part 2: the response to a resubmission refused by policy (`add-user-task-updates` Q1). The 422 row is marked pending there.

**Coordination**
- Size: L
- Depends on: add-user-task-updates, unify-http-routes-as-data
- Blocks: cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `http-transport`: ADDED, the three task-update routes, their status codes and bodies, error classification, and parity across stdlib, gin and fiber.
- `instance-disclosure`: ADDED, what the task and history routes show an unidentified caller, per category.
- `human-task-authorization`: MODIFIED, "Identity is refused before the request body is read" covers the update route.

## Impact

- `transport/http/httpcore`: three `Route` entries, request and response DTOs, the task projection with update fields, history projection, and classifier arms in `errors.go`.
- `transport/http/{stdlib,gin,fiber}`: nothing beyond what the route table provides. Their route-group doc comments list the new routes.
- `transport/http/parity`: the table-driven parity check gains the three routes.
- `runtime/task` or the service facade: a read path from task token to instance snapshot and `TaskUpdateReader`.
- Docs: route docs, the noun-route line, and `transport/http` README if it lists routes.
