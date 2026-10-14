# Design

## Context

See proposal.md (Why) for the motivation and the re-measurement table. Open-question numbers are kept from the pre-split plan: Q3 now lives in `surface-claimable-privileged-tasks`, and Q6 and Q7 in `add-instance-access-hook`. The constraints that shape this design are:

- **Recorded decisions, applied and not reopened.**
  - #21 call 3: a nil authorizer in the driver refuses, and `authz.AllowAll{}` is the opt-out.
  - #21 call 7: keep `xid` as the default ID.
  - kartaladev/wrkflw#114 item 5 and #119 Q4: privileges are request-time only, and persisted actor copies carry none.
  - #43: the posture is stated in the `# Trust boundary` section of `doc.go`.
- **The facade and the driver hold separate authorizers.** `service.WithHumanTasks` sets the one `TaskService` uses, which is `authz.AllowAll{}` when unset. `runtime.WithHumanTasks` sets the driver's. Today only the facade's is read. `authorize-task-triggers-in-driver` (#106, its design D4) moves the check into the driver: the facade's authorizer then feeds only `TaskService` and decides nothing, and a driver with no authorizer refuses human triggers with `runtime.ErrNoAuthorizer`. The default driver the facade builds (`service/service.go:223-242`) passes no `runtime.WithHumanTasks` today.
- **Eleven handlers per adapter build a `Disclosure`.** Five are instance routes, three task routes and three admin routes. Each adapter registers its routes through one wrapper (stdlib and gin `observe`, fiber `observed`), which is where nosniff is already set (`httpcore/headers.go`).

## Goals / Non-Goals

**Goals:**
- Close each posture gap with a decided behavior, or with a recommended behavior plainly marked pending.
- Keep the getting-started path (`service.NewProcessEngine()` with no options) working for a definition whose tasks declare no eligibility.
- Keep stdlib, gin and fiber in parity for every new behavior.

**Non-Goals:**
- No write-side privilege strip. That is owned by `replace-casbin-with-composed-deciders` (#115), and the SQL divergence by `guard-authz-field-totality` (#198).
- No per-instance access control (`add-instance-access-hook`) and no inbox change (`surface-claimable-privileged-tasks`).

## Decisions

### D1. The unconfigured engine's default is the driver's authorizer, and the facade and driver must agree (pending Q1)

After `authorize-task-triggers-in-driver` lands, the driver's authorizer is the only one that decides (its D4). The facade's own `c.authz` default stops mattering, so Q1 is about what the facade arms **the driver it builds** with when the consumer configured nothing. The recommended option, Q1 (d), arms it with the default composite. #21 call 3's nil-refuses sentinel then fires only for a driver built bare, or one the consumer supplies through `service.WithProcessDriver` without an authorizer. That second case is owned by #106 and is unchanged here.

- Rejected: **keep permit-all, documented loudly** (#176 option 1). Measured today: a role-gated task is claimable by any actor with no warning above DEBUG. After #106 it would also mean arming the default driver with `AllowAll` on purpose.
- Rejected: **refuse when unset** (option 2), which is what #106 alone leaves: every human trigger on a bare engine fails with `runtime.ErrNoAuthorizer`. It breaks `NewProcessEngine()` for every human task, including tasks with no eligibility, which the default composite already admits.

**Coupling:** this change depends on `authorize-task-triggers-in-driver`, which lists it under Blocks. Whether `service.WithHumanTasks` keeps its authorizer parameter is that change's Q1. If it drops it (its recommended (a)), the `service.WithHumanTasks(store, nil)` row of "Unconfigured engines refuse an actor the eligibility does not admit" goes, and the default lives only on the driver the facade builds. If it keeps it and passes it to the driver (its (c)), this change owns arming the default driver. `replace-casbin-with-composed-deciders` may change `NewComposite`'s decider list (#114, #119). The requirement names `authz.NewComposite()`, not its contents.

### D2. Privileges never render, for any caller (decided by #114 item 5)

#199 offered (a) never, (b) behind `DisclosePolicy`, or (c) scoped to the task. #114 item 5 already decided that persisted actor copies drop privileges, because they are request-time facts. A rendered privilege therefore shows a write-side strip that has not landed (#115) or a store that diverges (#198). The read side is made unconditional: every rendered actor (candidates, claim, completion, the actionable view) carries no privileges. This holds for identified callers and under `DiscloseAll` too.

- Rejected: **(b) gate on `DisclosePolicy`.** It still sends unscoped grants.
- Rejected: **(c) scope to the spec.** It needs the spec to interpret the grant, and under #114 the grant is not stored at all.
- Rejected: **strip only in the unidentified projection.** That is #199's own option (a). An identified caller would still read stale grants, which #114 says must not exist.

Evidence: a probe on main showed `payroll-export` and `admin-console` crossing under `{actors}`, and absent under `{}` and `{notes}`.

### D3. The cache directive is set where nosniff is set (pending Q4)

The directive is `Cache-Control: private, no-store`, set on every response of the eleven disclosing routes, in the route wrapper or in the shared disclosure path. `Vary` is not set, because the header the consumer's resolver reads is unknowable to the library (#127 reading 1). This follows the precedent of #71 (`headers.go`): the library sets headers on the responses its own handlers write. A consumer middleware that short-circuits is outside that scope, as it is for nosniff.

- Rejected: **document only** (#127 reading 2). A shared cache would invert the per-request disclosure decision. The library knows those bodies vary and the consumer may not.
- Rejected: **an option** (reading 3). It adds surface for a header no correct deployment wants off.

**Coupling:** `unify-http-routes-as-data` (#40, #205) rewrites route registration. Whichever lands second moves the directive into the route-as-data wrapper. The scenarios are adapter-level, so they survive the rewrite. `add-instance-access-hook` edits the same route wrappers; its 403 responses carry the directive too, because the directive does not depend on status.

### D5. The default ID stays xid, and UUIDv7 is the opacity option (default decided by #21 call 7, the rest pending Q5)

#128's premise "neither generator is CSPRNG-backed" is stale. google/uuid v1.6.0 `NewV7` starts from `NewRandom()` (`crypto/rand`) and overwrites only the 48-bit timestamp, the version and the 12-bit sequence, which leaves 62 random bits. A consumer who wants IDs that do not reveal their neighbours already has `idgen.UUIDv7()`. This change pins that property and documents it. It does not add a generator.

- Rejected: **a new `idgen.Random()`.** It duplicates UUIDv7's opacity and gives up index locality.
- Rejected: **change the default.** #21 call 7 decided against it.

(D4 and D6 of the pre-split plan moved to `add-instance-access-hook` and `surface-claimable-privileged-tasks`.)

## Risks / Trade-offs

- [The default flip (Q1) breaks consumers who relied on the permissive facade, including tests and examples that never configure an authorizer.] → The engine logs the effective authorizer at INFO, not DEBUG, and the `doc.go` and `NewProcessEngine` godoc name `authz.AllowAll{}` as the opt-out. Tests that flip are listed in tasks.
- [The privilege strip (D2) hides data a consumer relied on in audits.] → #114 already decided that the data must not be stored. A consumer who needs grants reads them from its own policy source.
- [`no-store` costs a client-side re-fetch.] → The responses are per-instance live state, and none was cacheable correctly before.

## Migration Plan

There is no persisted-format migration. Upgraders:
1. A consumer relying on the permissive facade passes `authz.AllowAll{}` explicitly, where `authorize-task-triggers-in-driver` Q1 leaves the parameter, if Q1 here lands as recommended.
2. Rollback is a code revert. No stored row changes shape.

## Open Questions

No maintainer ruling is recorded on these threads beyond the decisions listed in Context. Each question changes a pending-marked requirement, so `Readiness: human`, and apply waits for the rulings.

- **Q1 (#176): the default authorizer of an unconfigured engine, which after #106 is the authorizer of the driver the facade builds.**
  - (a) Keep permit-all: arm the default driver with `authz.AllowAll{}` and document it loudly.
  - (b) Refuse when unset: leave the default driver without an authorizer, so every human trigger fails with `runtime.ErrNoAuthorizer` (#21 call 3 applied to the facade). This is what `authorize-task-triggers-in-driver` alone produces.
  - (c) Refuse only the driver-authorized operations. After #106 every human-task operation is driver-authorized, so (c) collapses into (b).
  - (d) Arm the default driver with `authz.NewComposite()`.
  - **Recommend (d)**, which is fail-closed for declared eligibility and still works with no configuration. (b) is the literal reading of #21 call 3 if the maintainer prefers it.
  - Sub-question: `WithHumanTasks(store, nil)` is silently ignored today. Under (d) it is harmless. Under (b) it should be refused. If `authorize-task-triggers-in-driver` Q1 (a) removes the parameter, the sub-question is moot.
- **Q2 (#199):** closed by #114 item 5 (D2).
- **Q4 (#127): the cache directive.**
  - (1) The library sets `private, no-store`.
  - (2) Document only.
  - (3) An option.
  - **Recommend (1)** (D3).
- **Q5 (#128): a new generator.**
  - (1) Keep as is, and document UUIDv7 as the opacity option, pinned by test.
  - (2) Ship a new CSPRNG generator.
  - **Recommend (1)** (D5). Changing the default is closed by #21 call 7.
