## ADDED Requirements

### Requirement: Each task lifecycle transition writes one task event with the step that made it
A step that creates, claims, reassigns, progresses, rejects an update on, completes or cancels a human task MUST commit, with that step, one outbox event per transition and task on the matching topic among `task.created`, `task.claimed`, `task.reassigned`, `task.progressed`, `task.update_rejected`, `task.completed` and `task.cancelled` (pending Q2).

#### Scenario: Topic per transition
```gherkin
Scenario Outline: The transition picks the topic
  Given a running instance with user task "t1" at node "review"
  When <transition>
  Then the committed step's outbox events include exactly one "<topic>" event for "t1"

  Examples:
    | transition                                                    | topic                 |
    | the token enters "review" and task "t1" is created            | task.created          |
    | "alice" claims "t1"                                           | task.claimed          |
    | "t1" is reassigned from "alice" to "bob"                      | task.reassigned       |
    | an action-less update "a" is applied on "t1"                  | task.progressed       |
    | the action of update "a" on "t1" succeeds                    | task.progressed       |
    | the action of update "a" on "t1" fails terminally             | task.update_rejected  |
    | a resubmission of refuse-policy update "a" is rejected        | task.update_rejected  |
    | "t1" is completed                                             | task.completed        |
    | the deadline of "t1" fires                                    | task.cancelled        |
    | the instance is cancelled while "t1" is open                  | task.cancelled        |
```

#### Scenario: An implicit claim is a claim
```gherkin
Scenario Outline: One step, two transitions, in lifecycle order
  Given an unclaimed user task "t1"
  When "alice" <operation> "t1" in one step
  Then the step's task events for "t1" are <topics> in that order

  Examples:
    | operation                                   | topics                                 |
    | applies an action-less update "a" to        | task.claimed, task.progressed          |
    | submits an action-backed update "a" to      | task.claimed                           |
```

#### Scenario: Negative control - steps that change no task write no task event
```gherkin
Scenario Outline: No transition, no event
  Given a running instance with open task "t1"
  When <step> is committed
  Then the step's outbox events contain no topic starting with "task."

  Examples:
    | step                                                        |
    | a timer on another node fires                               |
    | an action-backed update "a" on "t1" is submitted while "t1" is already claimed by the submitter |
    | a candidate refresh replaces the candidates of "t1"         |
    | a service task elsewhere in the instance completes          |
```

#### Scenario: A refused trigger writes nothing
```gherkin
Scenario Outline: The event exists only if the step commits
  Given open task "t1"
  When <trigger> is refused with <error>
  Then no outbox row is written

  Examples:
    | trigger                                    | error                           |
    | a completion missing a required update     | engine.ErrRequiredUpdatesMissing |
    | an update while another is in flight       | engine.ErrUpdateInFlight         |
    | a claim by an ineligible actor             | authz.ErrNotAuthorized           |
```

### Requirement: Task event payloads carry references only
A task event's payload MUST consist of exactly `task_id`, `instance_id`, `node_id`, `state`, `update` (an object of `name` and `seq`, or null) and `actor_id`, and MUST NOT carry any update input or value, process variable, rejection reason, completion note or eligibility.

#### Scenario: Payload shape per topic
```gherkin
Scenario Outline: The keys and their values
  Given instance "i1" with task "t1" at node "review"
  When <transition> by actor "alice"
  Then the "<topic>" payload is {"task_id": "t1", "instance_id": "i1", "node_id": "review", "state": "<state>", "update": <update>, "actor_id": <actor>}

  Examples:
    | transition                                  | topic                | state      | update                          | actor     |
    | "t1" is claimed                             | task.claimed         | claimed    | null                            | "alice"   |
    | update "a" is applied at seq 2              | task.progressed      | progressed | {"name": "a", "seq": 2}         | "alice"   |
    | update "a" is rejected at seq 4             | task.update_rejected | claimed    | {"name": "a", "seq": 4}         | "alice"   |
    | "t1" is completed                           | task.completed       | completed  | null                            | "alice"   |
```

#### Scenario: Engine-driven transitions carry no actor
```gherkin
Scenario Outline: No human, empty actor
  When <transition>
  Then the "<topic>" payload's "actor_id" is ""

  Examples:
    | transition                      | topic          |
    | task "t1" is created            | task.created   |
    | the deadline of "t1" fires      | task.cancelled |
```

#### Scenario: Secrets never reach the outbox through a task event
```gherkin
Scenario: Values stay behind the disclosing read routes
  Given task "t1" whose update "a" is applied with input and value {"ssn": "111-22-3333"} and later rejected with reason "ssn 111-22-3333 locked"
  And the completion carries the note "ssn 111-22-3333"
  When every task event of "t1" is read from the outbox
  Then no payload contains "111-22-3333"
```

#### Scenario: Acting on behalf of a principal
```gherkin
Scenario: The principal is the actor of record
  Given actor "assistant" acting on behalf of principal "alice"
  When it claims task "t1"
  Then the "task.claimed" payload's "actor_id" is "alice"
```

### Requirement: Task events within one step have distinct dedup keys
Every outbox event committed by one step, task events included, MUST carry a dedup key distinct from every other event's key, including when one step writes the same topic for several tasks (pending Q1).

#### Scenario: Repeated topic in one step
```gherkin
Scenario Outline: Two tasks, one topic, one step
  Given a <store> instance store
  When one step <transition>
  Then the step writes two "<topic>" rows with different dedup keys
  And each dedup key begins with the instance ID and the step's journal seq

  Examples:
    | store    | transition                                                | topic          |
    | Postgres | passes a parallel split into two user tasks               | task.created   |
    | MySQL    | passes a parallel split into two user tasks               | task.created   |
    | SQLite   | passes a parallel split into two user tasks               | task.created   |
    | SQLite   | cancels an instance holding two open user tasks           | task.cancelled |
```

#### Scenario: A relayed task event is delivered at least once with a stable id
```gherkin
Scenario: Redelivery keeps the envelope id
  Given a committed "task.progressed" row whose first publish fails
  When the relay retries it and the publish succeeds
  Then both publish attempts carried the same envelope ID
```

### Requirement: Task events commit atomically with their step on every store
Task events SHALL be written in the same transaction as the step's snapshot, journal and task update rows, so a step that does not commit writes no task event, on Postgres, MySQL, SQLite and the in-memory store alike.

#### Scenario: Stale commit leaves no event
```gherkin
Scenario Outline: CAS failure
  Given a <store> instance store
  When a step that would write "task.claimed" is committed against a stale version
  Then the commit fails with kernel.ErrConcurrentUpdate
  And no "task.claimed" row exists for that attempt

  Examples:
    | store     |
    | Postgres  |
    | MySQL     |
    | SQLite    |
    | in-memory |
```
