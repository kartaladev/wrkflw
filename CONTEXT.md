# wrkflw

wrkflw is an importable Go library — not a daemon — that a consumer embeds to define
and run workflows. This is the project's glossary: one line per term, so agents and
contributors stop drifting to synonyms.

Where a term was fixed by a decision, the issue that fixed it is cited. A term with no
citation is descriptive of code that predates the decision log; it is not a decision,
and nobody should cite one for it. Terms marked _decided, not yet in code_ have a
ruling but no symbol to jump to yet — do not use them to describe today's behaviour.

## Language

### Process shape

**Definition**:
The reusable template a process instance executes (`model.ProcessDefinition`): nodes,
sequence flows, and the validation over them. Pure data; it runs nothing.
_Avoid_: workflow, process template, schema

**Version**:
The integer edition of a definition. A published version is immutable — a write to an
existing (id, version) is refused rather than upserted, so a stored definition can
never change under a running instance. A `model.Qualifier` names the pair, with
version 0 meaning latest. (#111)
_Avoid_: revision, generation

**Node kind**:
The discriminator of a node (`model.NodeKind`), carried on the wire as a stable
lowerCamelCase name. The set is closed: leaf packages register kinds through a
capability token, and a node whose dynamic type is not its kind's registered type is
refused. (#26, #27)
_Avoid_: node type, element type, activity type

**Flow**:
A directed sequence flow between two nodes (`flow.SequenceFlow`), optionally
conditional. Flows reference nodes by ID, never by name.
_Avoid_: edge, transition, arrow

**Rule**:
The reserved field on a `businessRuleTask` (`model.RuleSpec`): either a catalog name
or an inline rule document, interpreted by nothing in wrkflw today. A non-empty rule
is refused at build time until the rule-engine adapter exists, and a rule and an
action together are refused outright. (#27)
_Avoid_: decision, DMN, policy

### Execution

**Instance**:
One running execution of a definition (`engine.InstanceState`), carrying its tokens,
scopes and status. The library has no owner or tenant concept on an instance.
_Avoid_: run, execution, case, job

**Token**:
The unit of execution position inside an instance (`engine.Token`) — what sits on a
node, moves along a flow, and waits. Parallelism is counted in tokens, not in flows.
_Avoid_: thread, marker, pointer

**Scope**:
A nested execution region within an instance (`engine.Scope`), the unit compensation
and cancellation walk. Scopes form a tree by parent.
_Avoid_: frame, context, region

**Deadline**:
A time limit on a waiting node. On breach the token is rerouted to the node's
`DeadlineFlow` and any deadline action runs. There is no escalation event and no
escalation boundary — see [BPMN](#bpmn). (#27)
_Avoid_: escalation, timeout, SLA

**Cancel**:
The administrative teardown of an entire instance (`CancelRequested`), optionally
running the definition's cancel actions. Distinct from the `Cancelled` task state, and
unrelated to Go context cancellation. There are no transactions and no cancel events —
see [BPMN](#bpmn). (#27)
_Avoid_: abort, terminate, rollback

**Compensation**:
The undo walk over completed work. A handler is a catalog action name, not an
activity, and there is no compensation boundary. A throw unwinds the whole instance by
default; `ScopeLocal` opts a single scope out. (#27)
_Avoid_: rollback, undo, reversal

**Incident**:
A recorded failure that parks a token and demands operator attention
(`engine.Incident`) rather than failing silently. A token that stops and reports
nothing is indistinguishable from one legitimately waiting, which is why silent parks
raise one. (#54)
_Avoid_: error, fault, alert

### Work and actors

**Action**:
A named unit of consumer-supplied behaviour (`action.Action`). Identity is the string
name; no engine, runtime, persistence or wire format references a Go type.
_Avoid_: handler, task function, job

**Catalog**:
The registry that resolves an action name to an action (`action.Catalog`). A
definition may carry a scoped catalog that falls back to the global one.
_Avoid_: registry, container, action map

**Actor**:
A principal that can act on human tasks (`authz.Actor`: ID, roles, attributes). The
human-task guard refuses only the wholly zero actor, so an actor carrying roles but no
ID may claim and complete; the disclosure decision is stricter and requires a
non-empty `Actor.ID`. **An actor can act without being able to see.** (#21, #114)
_Avoid_: user, principal, subject

**Eligibility spec**:
The per-task rule for who may act (`authz.AuthzSpec`: any-of roles, any-of privileges,
optional attribute predicate; empty means allow-all). It is derived from the
definition node and persisted **on the task**, not read from the definition at
decision time. (#21, #114)
_Avoid_: permission, ACL, policy

**Candidates**:
The resolved projection of who is currently eligible for a task. **Not an
access-control list**: authorization is evaluated live against the eligibility spec,
so an actor who becomes eligible can act on a task whose candidate list is stale.
Refreshing keeps the list current; it grants nothing.
_Avoid_: assignees, permitted users, ACL

**Claimant**:
The actor recorded in a task's claim (`humantask.Claim`), as resolved at claim time. A
task carries at most one claim; reassignment overwrites it, so there is no claim
history. Distinct from a candidate.
_Avoid_: assignee, owner, holder — the repo declares no `Assignee` identifier, though
"assignee" survives in prose around reassignment

**Task states**:
The four-state lifecycle of a human task (`humantask.TaskState`): `Unclaimed`,
`Claimed`, `Completed`, `Cancelled`. `IsOpen()` is `Unclaimed` or `Claimed`. A fifth
state `Progressed` is decided but is not in the enum today — _decided, not yet in
code_. (#29)
_Avoid_: status, phase

**Authorizer**:
The authorization port consulted for human-task claim, complete, reassign and
candidate refresh. Nothing else in the library authorizes. See the `# Authorization`
section of root `doc.go`. (#114)
_Avoid_: guard, policy engine, enforcer

**Disclosure set**:
What a caller may **see** (`authz.DisclosureSet` over the `authz.Disclose*`
categories). It governs reads only, never what a caller may do. (#21, #114)
_Avoid_: visibility, permissions, scopes

**Trust boundary**:
The router group the consumer mounts wrkflw's routes onto. **wrkflw authenticates
nobody**: every route runs with whatever identity the surrounding application already
established. See the `# Trust boundary` section of root `doc.go`. (#21)
_Avoid_: perimeter, auth layer

**Update**:
A declared, re-submittable unit of progress on a human task — _decided, not yet in
code_. The existing `engine.UpdateTask` command is a different thing: it writes the
task row. (#29)
_Avoid_: progress, partial completion

**Call**:
The typed envelope carrying a human task's inputs — _decided, not yet in code_. Do not
confuse it with `activity.CallActivity`, the BPMN call activity, or with
`kernel.CallLink`. (#29)
_Avoid_: payload, form, request

### Storage and delivery

**Journal**:
The append-only record of applied steps for an instance, read back through
`kernel.JournalReader`. It is the instance's history, not its current state.
_Avoid_: log, audit trail, event store

**Outbox**:
The transactional buffer domain events are written into alongside the state change,
then relayed at-least-once (`kernel.OutboxPublisher`). A delivery failure is an outbox
concern the instance never observes.
_Avoid_: queue, event bus, publisher

**Lent transaction**:
A transaction opened by the consumer and handed to wrkflw to join rather than replace
— _decided, not yet in code_. (#22)
_Avoid_: ambient transaction, shared transaction

**Continuation**:
The handle by which a caller resumes work across a persistence boundary — _decided,
not yet in code_. (#22)
_Avoid_: callback, resume token

## BPMN

wrkflw is inspired by BPMN 2.0 and does not aim to comply with it. The divergences —
`businessRuleTask`, `sendTask`, `endEvent`, deadline, cancel, compensation and
boundary hosts — are listed in the **`# BPMN 2.0` section of root `doc.go`**, together
with the kinds that do match BPMN.

That table is the single copy, deliberately: it lives in `doc.go` because pkg.go.dev
is where a consumer meets the API, and this glossary points at it rather than forking
it. A doc test pins it, so a node kind added without being classified fails the build.
(#27)
