# ADR-0189 revision — the AUTHOR's pairwise interaction grid

**Written BEFORE the revision, per CLAUDE.md rule #9's corollary:** *"if a revision touches more
than one decision, write down the pairwise consequences yourself and mark the ones you could not
resolve. An unwritten interaction is the cheapest possible Critical."*

This exists because ADR-0185's second audit found that **five of nine Criticals were holes the
revision's own fixes opened in each other**. Every fix below was correct alone.

## The changed decisions

| id | change | source |
|---|---|---|
| **A** | `authz.Actor.Attributes` **keeps** flowing; a marshalability pre-check is added at the seam | owner D-1 = Option B |
| **B** | the empty-`Actor.ID` refusal is **REMOVED** | owner D-2 |
| **C** | **every route group except `HealthRoutes`** refuses an unresolved actor (401) | owner D-3 |
| **D** | `AdminRoutes` gains a consumer-declared required-role gate, **opt-in**; undeclared it inherits C's 401 only | owner D-3, corrected |
| **E** | the endpoint-parameter shape is kept; auth-behind-body-decode documented, not fixed | owner D-4 |
| **F** | the claim route must accept an **absent** body | audit A3 |
| **G** | blast radius corrected to **37 lines / 6 packages**; counting method changed to member-set | audit A2 |
| **H** | the gin `gc.Set` trap is documented and pinned by test | audit B1 |
| **I** | a co-match test for the two new arms against **each other** | audit B7 |
| **J** | `Clone` depth stated honestly; `ActorFromContext` clones on the way **out** | audit B8 |
| **K** | `CHANGELOG` + `STABILITY.md` entries | audit B4 |

## The grid

Cells marked ⚠ are live interactions and are resolved below. `·` = derived, no interaction.

|      | A | B | C | D | E | F |
|------|---|---|---|---|---|---|
| **A** | — | · | ⚠**1** | · | ⚠**2** | · |
| **B** |   | — | ⚠**3** | ⚠**4** | · | · |
| **C** |   |   | — | ⚠**5** | ⚠**6** | ⚠**7** |
| **D** |   |   |   | — | · | · |
| **E** |   |   |   |   | — | ⚠**8** |

G, H, I, J, K are documentation/test changes with no decision surface; each was checked against
A–F and none interacts. That check is itself recorded so a re-auditor need not redo it.

---

### ⚠1 — A × C: **C closes A's exposure leg.** ⭐ POSITIVE, and it rewrites a Consequence

The audit's CRITICAL (failure-modes F2) was that flowing `Attributes` puts them on
`GET /instances/{id}/actionable` and `/snapshot`, which render `Claim.Actor` verbatim to
**anonymous** callers. **C authenticates `InstanceRoutes`**, so those routes stop being anonymous.

⇒ the exposure leg is closed by C, *including the pre-existing `candidates` exposure this bundle
did not cause*. **Resolved.**

⚠ **State the limit precisely, do not overclaim:** this turns *"anyone can read any instance"*
into *"any authenticated caller can read any instance."* Per-instance ownership is **backlog 62**
and stays open. Writing "the exposure is closed" without that clause would be a new false claim.

### ⚠2 — A × E: the marshalability pre-check runs AFTER the body read

E leaves task-route authentication behind the adapter's capped body read. A's new pre-check lives
at the same seam, so it too runs post-decode. That is fine — it guards a *write*, not an entry —
but the ADR must not describe the pre-check as an input gate. **Resolved: wording.**

### ⚠3 — B × C: **B removes C's backstop.** This is the sharpest interaction in the revision

With the empty-ID rule gone, `ActorFromContext` returns `ok == true` for
`ContextWithActor(ctx, authz.Actor{})`. A consumer's buggy middleware that authenticates and then
stores a **zero actor** now satisfies C's refusal on **every** route group.

⇒ C's promise is *"the request carries an actor"*, **not** *"the request carries an identified
principal."* Before D-2 those were the same sentence; they no longer are.

**Resolved by wording, deliberately:** the ADR says C refuses an **unresolved** actor and states
explicitly that a resolved-but-empty actor passes, by ADR-0148's design. ⛔ The ADR may **not**
say "every route now has an identified principal" — that is now false, and it is exactly the kind
of recap sentence that has shipped false three times in this repo.

### ⚠4 — B × D: an empty-ID actor can pass the admin role gate

D checks `actor.Roles`; B allows `Actor{ID: "", Roles: ["platform-admin"]}`. Such an actor clears
the admin gate with no identity in any log or audit record.

**Assessed and accepted, with a stated limit.** It is coherent with D-2 (roles are what the gate
evaluates) and the consumer's middleware is what mints the actor. But admin operations have **no
audit record at all** today (`admin_endpoints.go` has zero `authz.` references), so nothing is
lost that existed. **Recorded as a residual, and filed as a backlog item**: admin operations
should carry an actor in an audit trail. ⚠ Not fixed here — fixing it means the service-layer
change D-3 option (c) explicitly declined.

### ⚠5 — C × D: ordering — 401 before 403, and a fail-closed default that BREAKS EXISTING CONSUMERS

Admin routes must evaluate C's refusal (401) **before** D's role gate (403); the reverse leaks
whether a role would have sufficed to an unauthenticated caller.

⚠⚠ **This interaction was RESOLVED BY CHANGING D, after a fact the first draft of this grid did
not have.** `stdlib.Mount` **excludes** `AdminRoutes`, and its godoc says admin routes are
*"intentionally excluded so consumers can choose whether and where to mount them — typically on a
separate, access-controlled mux."* So a fail-closed default would have returned 403 to the
consumer who followed the documented advice and already secured that mux.

⇒ **D is opt-in.** `WithAdminRoles(...)` enables the 403 gate; undeclared, `AdminRoutes` inherits
C's 401 refusal and nothing more. No existing correct wiring breaks, and the admin surface is
still strictly better than today.

⚠ **Consequence for the audit finding this came from:** failure-modes F7's *"after this ships,
`POST /admin/role-bindings` still grants roles with no identity"* is true **only for a consumer
who mounts `AdminRoutes` on their public mux, against the godoc's advice.** The finding is
**accepted with that scope correction**, not as filed.

### ⚠6 — C × E: does C make E's residual worse? **No — because C is new code**

E's residual is that authentication resolves *behind* the adapter's body decode. If C were
implemented the same way, that residual would widen from 3 task routes to every route.

**Resolved by design, and this is a decision the revision makes:** the newly-authenticated groups
(`InstanceRoutes`, `MessageRoutes`, `AdminRoutes`) call the refusal check in the route handler
**BEFORE** decoding the body. Nothing constrains them to the endpoint-parameter shape — that shape
exists only because the three *task* endpoints need the actor as a **value**, and D-4 chose not to
re-plumb them a third time. The others need only the **refusal**.

⇒ E's residual stays exactly three routes and does not grow.
⚠ **Cost, stated:** the transport now has two resolution placements. That asymmetry is
deliberate and must be justified in the ADR *at the seam*, or the next reader will "fix" it into
one shape and silently re-widen the window.
⚠ **Explicitly NOT done:** adding a pre-decode check to the task routes as well. It would resolve
the consumer's (possibly I/O-performing) resolver **twice per request**. Rejected on that ground.

### ⚠7 — C × F: F's "absent body is OK" must not become "absent body is OK everywhere"

F makes the claim route tolerate a missing body. C adds refusals to routes whose bodies are
**required** (`StartInput.DefRef` is `validate:"required"`; `MessageInput.Name` likewise).

**Resolved:** F is scoped to the **claim** route alone, because `ClaimInput` is the only DTO that
becomes zero-field. `CompleteInput` and `ReassignInput` keep required content and keep a required
body. ⚠ The plan must say this per-route, not per-adapter — a helper applied group-wide would make
`POST /instances` accept an empty body and fail later with a worse error.

### ⚠8 — E × F: the 400-vs-401 ordering on a bodyless claim from an ANONYMOUS caller

Under E the body is decoded first. Under F an absent body is now legal on the claim route. So an
anonymous, bodyless claim decodes cleanly and *then* 401s — correct. But an anonymous claim with
**malformed** JSON still returns **400 before 401**, disclosing parse validity to an
unauthenticated caller.

**Assessed: accepted, and it is not a regression** — that is today's behaviour on every route and
follows directly from D-4. **Recorded in the same residual as E**, not as a separate one.

---

## Interactions I could NOT fully resolve — flagged, not hidden

1. **D's required-role gate invents policy the library has no other example of.** (Still open
   after D was made opt-in — being opt-in narrows the blast radius, not the design objection.) Every other
   authorization decision in `wrkflw` goes through the `Authorizer` abstraction; this one is a
   direct `actor.Roles` membership test in the transport. It is defensible (admin routes have no
   `AuthzSpec` to evaluate and no engine-side model) but it is a **second, parallel authorization
   mechanism**, and CLAUDE.md's pluggable-authz requirement points the other way.
   **Left open for the re-audit to attack.** The alternative — routing admin verbs through
   `Authorizer` with a synthesized spec — was not designed here and would be a service-layer
   change.
2. ~~**Whether `MountGroups` can still mount a working API** (interaction F9).~~
   **RESOLVED, and F9 is largely REFUTED.** `MountGroups` passes no options, so each group runs
   `ResolveConfig()` with none — which installs the **default** `RequestActor`,
   `authz.ActorFromContext`. A consumer who authenticates with middleware and
   `authz.ContextWithActor` therefore gets a fully working task API through `MountGroups` with
   zero options, which is the documented path. What `MountGroups` cannot deliver is a **custom**
   resolver — but it cannot deliver *any* option, by its own design (*"no extra opts"*). That is a
   pre-existing property of `MountGroups`, not a defect this bundle introduces.
   **Downgraded to MINOR:** its godoc should say that groups mounted this way rely on the context
   seam.
