## Purpose

Defines what an action invoked by a user task receives: one task envelope, `humantask.Call`, carrying task context, while actions that concern instance data keep the flat variables map.

## ADDED Requirements

### Requirement: A user task's completion action receives the task envelope
When a user task declaring a completion action is completed, the action MUST receive a `humantask.Call` whose kind is `completion`, carrying the task, instance and node IDs, the completing actor, the submitted completion output as input and the instance variables after the completion merge.

#### Scenario: Envelope fields for a completion
```gherkin
Scenario: The completion action sees the task, the actor and the output
  Given a running instance "i1" parked on user task node "review" with task ID "t1" and completion action "review.done"
  And the instance variables are {"order": 7}
  When actor "alice" completes task "t1" with output {"approved": true}
  Then "review.done" is invoked with an input whose "kind" is "completion"
  And whose "task_id" is "t1", "instance_id" is "i1" and "node_id" is "review"
  And whose "actor" names "alice"
  And whose "input" is {"approved": true}
  And whose "variables" are {"order": 7, "approved": true}
  And whose "applied" is an empty object
  And the input has no "update" key and no "previous" key
```

#### Scenario: Output that is empty still yields an object
```gherkin
Scenario Outline: Completion input for empty and absent output
  Given a user task with completion action "review.done"
  When it is completed with output <output>
  Then "review.done" receives an envelope whose "input" is {}

  Examples:
    | output |
    | {}     |
    | absent |
```

#### Scenario: Negative control - a receive task's completion action keeps the flat map
```gherkin
Scenario: Receive task completion is instance data, not task context
  Given a running instance parked on a receive task with completion action "rx.done"
  And the instance variables are {"order": 7}
  When the awaited message arrives with payload {"paid": true}
  Then "rx.done" receives exactly the flat map {"order": 7, "paid": true}
  And the input has no "kind" key
```

### Requirement: A user task's deadline and reminder actions receive the task envelope
A deadline action or reminder action fired for a user task MUST receive a `humantask.Call` whose kind is `deadline` or `reminder`, carrying the task, instance and node IDs, the zero actor, an empty input object and the instance variables.

#### Scenario: Timer-fired task actions
```gherkin
Scenario Outline: The engine, not an actor, fires the action
  Given a running instance parked on user task "t1" at node "review" declaring <field> "<action>"
  And the instance variables are {"order": 7}
  When the task's <timer> timer fires
  Then "<action>" receives an envelope whose "kind" is "<kind>"
  And whose "task_id" is "t1" and "node_id" is "review"
  And whose "actor" is the zero actor
  And whose "input" is {} and "variables" are {"order": 7}

  Examples:
    | field           | action         | timer    | kind     |
    | deadline_action | review.late    | deadline | deadline |
    | wait_action     | review.nudge   | reminder | reminder |
```

#### Scenario: Negative control - other nodes' timer and boundary actions keep the flat map
```gherkin
Scenario Outline: Instance actions are not wrapped
  Given a running instance whose variables are {"order": 7}
  When <source> fires action "a1"
  Then "a1" receives exactly the flat map {"order": 7}

  Examples:
    | source                                                    |
    | the deadline of a receive task                            |
    | the reminder of an intermediate catch event               |
    | a non-interrupting timer boundary's boundary_action       |
    | an error boundary's boundary_action                       |
```

### Requirement: Instance actions keep the flat variables map
Service-task, cancel and compensation actions MUST keep receiving the flat variables map they receive today, including the primary service task's `_idempotencyKey`, and MUST NOT receive a `humantask.Call`.

#### Scenario: Instance action inputs are unchanged
```gherkin
Scenario Outline: Instance actions see instance data
  Given a running instance "i1" whose variables are {"order": 7}
  When <action> is invoked
  Then its input is <input>
  And its input has no "kind" key and no "task_id" key

  Examples:
    | action                                   | input                                              |
    | the primary action of service task "s1"  | {"order": 7, "_idempotencyKey": "i1:s1"}            |
    | a cancel action                          | {"order": 7}                                       |
    | a compensation action recorded with {"order": 7} | {"order": 7}                               |
```

### Requirement: Task actions decode the envelope strictly
An action built with `action.Typed` over `humantask.Call[In]` and `action.WithStrictInput()` MUST decode every envelope the engine sends for a user task, of every kind this capability defines, without `action.ErrDecodeInput`.

#### Scenario: Strict typed decoding per kind
```gherkin
Scenario Outline: The envelope's key set is closed
  Given an action built with action.Typed over humantask.Call[map[string]any] and action.WithStrictInput()
  And it is registered as a user task's <field>
  When the engine invokes it through <event>
  Then the action runs and does not fail with action.ErrDecodeInput

  Examples:
    | field             | event                 |
    | completion_action | a completion          |
    | deadline_action   | a deadline firing     |
    | wait_action       | a reminder firing     |
```

#### Scenario: A flat-map action under strict input sees the breaking change
```gherkin
Scenario: An action still written against the flat map is refused, not silently fed zero values
  Given an action built with action.Typed over a struct declaring only "approved" and action.WithStrictInput()
  And it is registered as a user task's completion_action
  When the task is completed with output {"approved": true}
  Then the action fails with action.ErrDecodeInput naming the key "kind"
  And the failure is not retried
```

### Requirement: Call kinds are encoded by name and an unknown kind is refused
A `humantask.CallKind` MUST encode as its name, and decoding an envelope whose kind is not a known name MUST fail with `humantask.ErrUnknownCallKind` rather than decode to a zero kind.

#### Scenario: Kind round trip and refusal
```gherkin
Scenario Outline: Decoding the kind field
  Given an envelope JSON document whose "kind" is <kind>
  When it is decoded into humantask.Call[map[string]any]
  Then the result is <result>

  Examples:
    | kind           | result                                  |
    | "completion"   | kind completion                         |
    | "deadline"     | kind deadline                           |
    | "reminder"     | kind reminder                           |
    | "Completion"   | humantask.ErrUnknownCallKind            |
    | " completion"  | humantask.ErrUnknownCallKind            |
    | "complétion"   | humantask.ErrUnknownCallKind            |
    | ""             | humantask.ErrUnknownCallKind            |
    | 0              | humantask.ErrUnknownCallKind            |
```

### Requirement: The envelope's idempotency key is stable per invocation and distinct across invocations
The `idempotency_key` of a task envelope MUST be identical every time the same invocation is delivered, including a re-drive after a restart, and MUST differ between two distinct invocations on the same task.

#### Scenario: Key format per kind (pending Q1)
```gherkin
Scenario Outline: The key names the task and the invocation
  Given user task "t1" with a completion, a deadline and a reminder action
  When <event>
  Then the action's envelope has "idempotency_key" "<key>"

  Examples:
    | event                          | key                |
    | the task is completed          | t1:completion      |
    | the deadline fires             | t1:deadline        |
    | the reminder fires the 1st time | t1:reminder:1     |
    | the reminder fires the 2nd time | t1:reminder:2     |
```

#### Scenario: Two firings never share a key
```gherkin
Scenario: Repeated reminders are distinguishable
  Given user task "t1" with a reminder action
  When the reminder fires three times
  Then the three envelopes carry three distinct idempotency keys
  And each key begins with "t1:"
```

#### Scenario: Re-delivery of the same invocation keeps its key
```gherkin
Scenario: The same command re-sent carries the same envelope
  Given user task "t1" was completed and its completion action's command committed
  When that command is performed twice, as an at-least-once re-drive does
  Then both deliveries carry byte-identical envelopes
  And both carry the same idempotency key
```

### Requirement: Task action failure handling is unchanged by the envelope
The envelope MUST NOT change how a task action's failure is handled: a completion action's terminal failure SHALL follow the node's retry policy, recovery flow, error boundary and incident chain as before, and deadline and reminder actions SHALL stay fire-and-forget.

#### Scenario: Failure paths per kind
```gherkin
Scenario Outline: The same failure chain as before the envelope
  Given a user task whose <field> always fails with a retryable error
  And the node declares a retry policy and no recovery flow or error boundary
  When <event>
  Then <outcome>

  Examples:
    | field             | event               | outcome                                                                 |
    | completion_action | the task completes  | the retry policy is applied, then an incident is raised on the instance  |
    | deadline_action   | the deadline fires  | the token still moves to the deadline flow and no incident is raised      |
    | wait_action       | the reminder fires  | the token stays on the task and no incident is raised                     |
```
