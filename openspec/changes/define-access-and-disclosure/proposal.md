# Proposal

## Why

wrkflw's default security posture has four gaps that no one has decided. An unconfigured `ProcessEngine` authorizes human tasks with `authz.AllowAll`. The disclosure projection hands out a principal's whole privilege list. Identity-varying bodies carry no cache directive. The ID generators have not been weighed against enumeration. Each gap was filed during an earlier review and ruled out of scope there. This change applies the decisions already recorded elsewhere, and specifies the recommended option for each remaining one, marked pending, so the maintainer rules once.

This change was split from a larger plan. The per-instance operation authorizer (kartaladev/wrkflw#108, #129) moved to `add-instance-access-hook`, and inbox privilege parity (kartaladev/wrkflw#200) moved to `surface-claimable-privileged-tasks`. Open-question numbers are kept from the pre-split plan, so this change holds Q1, Q2, Q4 and Q5.

Re-measured on `main` at `9c6dd2c1` (go1.26.9). The probes ran from the scratchpad through `go test -overlay`, so nothing was written to the repository.

| Premise | Measurement on main |
|---|---|
| #176: an unconfigured facade injects `authz.AllowAll{}` at `service/service.go:199-200` | **Holds.** The durable path does the same. `WithHumanTasks(store, nil)` silently keeps it, because nil options are ignored. The only signal is a DEBUG log label (`:316-317`). Probe: with the facade authorizer unset, "bob" (role `viewer`) claims a task whose eligibility is role `manager`, and `err=nil`. |
| #176 comment: the default is neither pinned by a test nor stated in root `doc.go` | **Holds.** Only `NewProcessEngine`'s godoc (`service/service.go:161`) says "an allow-all authorizer". The `# Trust boundary` section of `doc.go` does not. |
| #176, after `authorize-task-triggers-in-driver` (#106) lands | **Planned, not measured.** That change's design D4 moves the decision into the driver: the facade's own authorizer then reaches only `TaskService` and decides nothing. The default driver the facade builds (`service/service.go:223-242`) passes no `runtime.WithHumanTasks`, so a bare engine's human triggers would be refused with `runtime.ErrNoAuthorizer`. Q1 is re-read against that driver default. |
| #199: under `DiscloseActors` a principal's full privilege list crosses the projection, unscoped to the task (`runtime/view/public.go:149-152`) | **Holds, and is wider.** The function is now `projectTasks`, and the copy is at `:150-153`. Probe: `{}` → no privileges; `{actors}` → `payroll-export` present, and so are the actor's roles and attributes; `{notes}` → none. The eligibility spec stays behind `DisclosePolicy`. |
| #127: no `Cache-Control` or `Vary` anywhere in `transport/`, and "all five `InstanceRoutes` handlers" build a disclosure | **Zero occurrences holds. The count is stale and understated.** Eleven handlers per adapter build one: five instance routes, three task routes and three admin routes. |
| #128: "both shipped generators are time-ordered; neither is backed by a CSPRNG" | **Half stale.** `idgen.XID` is time- and counter-ordered, and neighbours are derivable. `idgen.UUIDv7` (google/uuid v1.6.0) does fill its 62 `rand_b` bits from `crypto/rand`, so its neighbours are not derivable. The engine's fallback IDs are `<instanceID>-<prefix><n>` (`engine/idgen.go:49`), as stated. |

## What Changes

Each item below is the recommended option of an open question in design. Each is **pending** until the maintainer rules. Two recorded decisions are applied as decided, not reopened: #21 call 7 (keep `xid` as the default ID), and kartaladev/wrkflw#114 item 5 with #119 Q4 (privileges are request-time only, and persisted actor copies carry none).

- **BREAKING, default security posture TIGHTENED (pending Q1).** An engine built without an authorizer, and without a consumer-supplied driver, authorizes human tasks with `authz.NewComposite()` on the driver the facade builds, instead of `authz.AllowAll{}` (today) or `runtime.ErrNoAuthorizer` on every human trigger (what `authorize-task-triggers-in-driver` alone would leave). A task with an empty eligibility spec stays open to everyone, so the getting-started path keeps working. A task that names roles, privileges or a predicate is now enforced. `authz.AllowAll{}` passed explicitly is the opt-out.
- **BREAKING, disclosure NARROWED (decided by #114 item 5, so Q2 is closed).** No instance route renders an actor's granted privileges to any caller, under any disclosure set, `DiscloseAll` and identified callers included. Roles, attributes and IDs keep their `DiscloseActors` gate. This change guarantees the read side. Stripping privileges at write time is owned by `replace-casbin-with-composed-deciders` (#115).
- **Cache directive (pending Q4).** Every response from a route that renders state through the disclosure decision carries `Cache-Control: private, no-store`, on stdlib, gin and fiber alike.
- **ID generation.** It is decided on #21 (call 7) that `idgen.XID` is the default generator, and that becomes a spec requirement. Pending Q5: no new generator. `idgen.UUIDv7` is documented as the option whose IDs a neighbour does not reveal, and its randomness is pinned.
- **Docs.** `doc.go` `# Trust boundary` names the facade default, the privilege rule and the cache directive.
- Persisted formats: **no change.** The privilege strip narrows what is rendered, not a stored row.
- Consumer go.mod: no impact. No new dependency.

Absorbed issues:
- kartaladev/wrkflw#176: absorbed. Its "#106 sentinel" framing was already withdrawn in its own comment. The posture question stands and reproduces. Where the default lives is moved by `authorize-task-triggers-in-driver` (its design D4), so Q1 is worded against the driver.
- kartaladev/wrkflw#199: absorbed. Its line numbers have drifted. Its open decision (a/b/c) is superseded by #114 item 5, which makes privileges request-time only and is option (a) extended to every caller. Its claim that the strip on the persisted path "was cut" is wrong about the decision itself: #114 stands, and #115 implements it.
- kartaladev/wrkflw#127: absorbed. Its handler count was found stale (eleven per adapter, not five).
- kartaladev/wrkflw#128: absorbed. Option 3 (change the default) is superseded by #21 call 7. "Neither is CSPRNG-backed" was found stale for UUIDv7.

Decisions the specs cannot be final without (design, Open Questions). No maintainer ruling is recorded on these threads beyond #21's call 7.
- **Q1** (#176) the default authorizer of an unconfigured engine, now the driver's. (Q2, on #199, is closed by #114 item 5; see design D2.)
- **Q4** (#127) cache directive: library, docs or option.
- **Q5** (#128) whether to ship a new generator.

Out of scope:
- Authorizing human-task triggers in the driver, the driver's nil-authorizer sentinel `runtime.ErrNoAuthorizer`, and whether the facade keeps an authorizer parameter at all: kartaladev/wrkflw#106, owned by `authorize-task-triggers-in-driver` (its Q1). This change depends on it (design D1).
- The per-instance operation authorizer, the `instance-access-control` capability, and 403 vs 404 on refused reads: kartaladev/wrkflw#108 and #129, owned by `add-instance-access-hook`.
- The inbox (`ClaimableBy`, `StaticActorResolver`) learning privileges: kartaladev/wrkflw#200, owned by `surface-claimable-privileged-tasks`.
- The SQL task store dropping `Actor.Privileges`, at-rest visibility, and the composite's field totality: kartaladev/wrkflw#198, #201 and #202, owned by `guard-authz-field-totality`.
- The actor model: kartaladev/wrkflw#115, owned by `replace-casbin-with-composed-deciders`; the casbin removal (#115 in part, and #179) is owned by `remove-casbin`.
- Engine-minted token, task and command IDs (`<instanceID>-t3`): unchanged. Per-instance control, not ID opacity, is the control (`add-instance-access-hook`).
- The read routes' variable content and reserved keys: `reserve-engine-variable-keys`.
- Route registration as data: kartaladev/wrkflw#40 and #205, owned by `unify-http-routes-as-data`. Landing order is in design D3.
- Release notes stating the posture: `cut-v0-release`.

**Coordination**
- Size: M
- Depends on: `establish-baseline-specs`, `authorize-task-triggers-in-driver`
- Blocks: `cut-v0-release`
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `instance-disclosure`:
  - ADDED: no instance route renders an actor's granted privileges (#199, decided by #114 item 5).
  - ADDED: identity-varying responses are marked uncacheable (#127, pending Q4).
- `human-task-authorization`:
  - ADDED: an engine with no configured authorizer enforces task eligibility (#176, pending Q1).
- `instance-id-generation`:
  - ADDED: the default generator is `idgen.XID` (decided, #21 call 7).
  - ADDED: UUIDv7 identifiers do not reveal their neighbours (#128, pending Q5).

## Impact

- `service/service.go`, `service/options.go`: the default authorizer the facade arms its own driver with (Q1), and the construction-summary log.
- `runtime/view/public.go`, `runtime/view/instance_actionable.go`, `transport/http/httpcore/view.go`: privileges are stripped from every rendered actor (#114 item 5).
- `transport/http/{stdlib,gin,fiber}/groups.go`, `transport/http/httpcore`: the cache directive. `groups.go` is shared with `unify-http-routes-as-data` and `add-instance-access-hook`.
- `runtime/idgen/idgen.go`: docs and the randomness test (Q5).
- `doc.go`, `authz/disclosure.go` godoc. `doc.go` is shared with `fix-doc-rot` and `add-instance-access-hook`.
- Tests that flip: any test or example that relies on the unconfigured facade admitting an ineligible actor (Q1), and `runtime/view` classification rows that expect privileges under `{actors}`.
