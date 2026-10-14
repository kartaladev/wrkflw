# token-routing-and-joins Specification

## Purpose
How the engine moves tokens along sequence flows: through exclusive, parallel and inclusive gateways, out of fired boundary events, and into force-termination end events that pick the instance's terminal status.

## Requirements

### Requirement: Exclusive gateway takes one branch or refuses the step
A diverging exclusive gateway SHALL route its token onto exactly one outgoing flow: the conditional flow whose condition holds, or else the default flow; when no condition holds and no default flow exists, the step MUST fail with `ErrNoMatchingFlow`.

#### Scenario: Exclusive gateway routing by condition and default
```gherkin
Scenario Outline: An exclusive gateway with one conditional flow "amount > 100" to "big"
  Given a definition start -> xor -> "big" whose second outgoing flow is <second flow>
  When an instance starts with amount = <amount>
  Then <outcome>

  Examples:
    | second flow                | amount | outcome                                                            |
    | a default flow to "small"  | 150    | exactly one token is parked on "big" and only "big" is invoked     |
    | a default flow to "small"  | 5      | exactly one token is parked on "small" and only "small" is invoked |
    | absent                     | 5      | the step fails with ErrNoMatchingFlow                              |
```

### Requirement: Parallel fork places a token on every outgoing flow
A diverging parallel gateway SHALL place one token on each of its outgoing flows in the same step, without evaluating conditions.

#### Scenario: Both branches of a parallel fork start together
```gherkin
Scenario: A parallel fork into two service tasks
  Given a definition start -> fork -> {a, b} where fork is a parallel gateway
  When an instance starts
  Then the actions "a" and "b" are both invoked in that step
  And exactly two tokens exist, one waiting on "a" and one waiting on "b"
```

### Requirement: Parallel join consumes one crossing token per incoming flow
For tokens that reach it by crossing one of its incoming sequence flows, a converging parallel gateway SHALL fire only once every incoming flow has been crossed, consuming one token per incoming flow; several tokens crossing the same incoming flow MUST NOT stand in for a flow that no token crossed, and any surplus token stays parked at the join.

#### Scenario: Two arrivals over one edge do not satisfy the join
```gherkin
Scenario: Two branches implicitly re-merge onto one incoming flow of the join
  Given a parallel fork whose two pass-through branches merge onto the join's flow "fj1"
  And the join's other incoming flow "fj2" comes from service task "c", which is still waiting
  When the instance starts
  Then two tokens are parked at the join
  And the end event after the join has not been reached
  And the instance status is StatusRunning
```

#### Scenario: The join fires on the step that delivers its last unsatisfied flow
```gherkin
Scenario: Completing the branch that feeds the untraversed flow
  Given the instance above with two tokens parked at the join over "fj1"
  When the action "c" completes, delivering a token over "fj2"
  Then the end event after the join is entered exactly once, at the time of that completion
  And one surplus token remains parked at the join
```

#### Scenario: A plain diamond completes when each flow delivers one token
```gherkin
Scenario: Negative control - one token per incoming flow
  Given a definition start -> fork -> {a, b} -> join -> end
  When the actions "a" and "b" both complete
  Then the instance status is StatusCompleted
  And no token remains
```

#### Scenario: A looping branch still contributes one token
```gherkin
Scenario: A branch that loops back before reaching the join
  Given a parallel fork into "a" and a branch "b" that loops back through an exclusive gateway
  When "b" completes once with again = 1 and then with again = 0, while "a" is still waiting
  Then exactly one token is parked at the join
  And the end event has not been reached
```

### Requirement: Parallel join tells incoming flows apart without relying on authored flow IDs
A converging parallel gateway SHALL distinguish its incoming sequence flows by their position in the definition, so blank or repeated authored flow IDs, or two flows with the same source and target, MUST NOT let a token crossing one flow satisfy another.

#### Scenario: Blank incoming flow IDs on a join
```gherkin
Scenario Outline: Two tokens cross one incoming flow while the other is untraversed
  Given the implicit re-merge definition whose join's incoming flows carry IDs <fj1 id> and <fj2 id>
  When the instance starts and action "c" has not completed
  Then the end event after the join has not been reached

  Examples:
    | fj1 id | fj2 id |
    | ""     | ""     |
    | ""     | "fj2"  |
    | "fj1"  | ""     |
    | "fj1"  | "fj2"  |
```

#### Scenario: Two identical blank edges into one join
```gherkin
Scenario: A send task with two identical blank outgoing flows into a parallel join
  Given a definition reaching the engine in which two tokens leave the send task over the first of two identical blank flows into the join
  When the instance runs
  Then the join does not fire, because its second incoming flow was never traversed
```

### Requirement: Inclusive fork takes every true branch
A diverging inclusive gateway SHALL place a token on every outgoing flow whose condition holds and on every unconditional non-default flow; it SHALL take the default flow only when no other flow is taken, and MUST fail the step with `ErrNoMatchingFlow` when nothing is taken and no default exists.

#### Scenario: Inclusive fork branch selection
```gherkin
Scenario Outline: Inclusive fork with flows "a > 0" to "ta", "b > 0" to "tb" and a default to "tc"
  Given an inclusive fork with those three outgoing flows
  When an instance starts with a = <a> and b = <b>
  Then the invoked actions are <actions>

  Examples:
    | a | b | actions |
    | 1 | 1 | a, b    |
    | 1 | 0 | a       |
    | 0 | 0 | c       |
```

#### Scenario: An unconditional flow suppresses the default
```gherkin
Scenario: Inclusive fork with one empty-condition flow and one default flow
  Given an inclusive fork whose outgoing flows are one with an empty condition and one default flow
  When an instance starts
  Then only the unconditional branch is taken
  And the default branch is not taken
```

#### Scenario: Nothing taken and no default
```gherkin
Scenario: Inclusive fork whose only flow is conditional and false
  Given an inclusive fork with the single flow "a > 0" and no default
  When an instance starts with a = 0
  Then the step fails with ErrNoMatchingFlow
```

### Requirement: Inclusive join waits only for branches that can still reach it
A converging inclusive gateway SHALL fire exactly once, after every token that can still reach it has arrived, counting a branch executing inside a sub-process; it MUST NOT wait for a branch that was never taken or for a child scope that cannot reach it.

#### Scenario: A sibling branch is inside a sub-process
```gherkin
Scenario: One OR-split branch runs inside a sub-process
  Given an inclusive split into sub-process "sub" and service task "svcB", both flowing into inclusive join "orjoin"
  When "svcB" completes while "sub" is still running
  Then one token is parked at "orjoin"
  And the end event after the join has not been reached
```

#### Scenario: The join fires once when the sub-process drains
```gherkin
Scenario: The sub-process branch arrives
  Given the instance above
  When the sub-process's inner action completes
  Then the end event after the join is entered exactly once
  And the instance status is StatusCompleted
```

#### Scenario: An untaken branch does not hold the join
```gherkin
Scenario: Two of three conditional branches are taken
  Given an inclusive split with branches a, b, c into an inclusive join followed by service task "post"
  When the instance starts with a = 1, b = 1, c = 0 and both "a" and "b" complete
  Then "post" is invoked exactly once
```

#### Scenario: A child scope that cannot reach the join does not hold it
```gherkin
Scenario: Negative control - a side sub-process with no path to the join
  Given an inclusive split into "svcA", "svcB" and a sub-process "sideSub" that leads to its own end
  When "svcA" and "svcB" complete while "sideSub" is still running
  Then the end event after the join is reached
  And the instance status is StatusRunning
```

### Requirement: Boundary events are armed on hosts that wait
A timer boundary event SHALL be armed when its host is a serviceTask, businessRuleTask, receiveTask or userTask, and the definition validator SHALL accept that attachment; a message boundary SHALL be armed when its host is a userTask.

#### Scenario: A timer boundary is armed on every waiting host kind
```gherkin
Scenario Outline: A timer boundary attached to a <host>
  Given a definition with a timer boundary attached to a <host>
  When an instance is driven to the host
  Then the boundary is armed
  And model.Validate accepts the definition

  Examples:
    | host              |
    | serviceTask       |
    | businessRuleTask  |
    | receiveTask       |
    | userTask          |
```

#### Scenario: A message boundary is armed on a user task
```gherkin
Scenario: A user task with a message boundary
  Given a definition with a message boundary attached to user task "work"
  When an instance is driven to "work"
  Then exactly one boundary is armed
```

### Requirement: A fired boundary routes to its own outgoing flow
When an armed message boundary event fires, the engine SHALL route the token to the target of the boundary's own outgoing flow, and MUST NOT take another flow that merely shares its authored flow ID, including a blank one.

#### Scenario: Boundary routing with a decoy flow sharing its ID
```gherkin
Scenario Outline: A message boundary fires while an earlier-declared flow shares its ID
  Given a user task "work" with a message boundary whose outgoing flow has ID <boundary flow id> and targets "realTarget"
  And an unrelated flow declared earlier with ID <decoy id> targeting "decoyTarget"
  When the boundary's message is received
  Then the action of "realTarget" is invoked and a token is parked on "realTarget"
  And no token reaches "decoyTarget" and its action is not invoked

  Examples:
    | decoy id  | boundary flow id |
    | ""        | ""               |
    | "f-decoy" | "f-bnd"          |
    | ""        | "f-bnd"          |
```

### Requirement: A boundary whose target names no node fails the step
When a fired message boundary's outgoing flow targets a node the definition does not declare, the step SHALL fail with an error naming the boundary; a declared node whose ID is blank MUST remain a valid target.

#### Scenario: Unresolvable and blank-but-declared boundary targets
```gherkin
Scenario Outline: An armed message boundary fires toward <target>
  Given a definition reaching the engine whose boundary "bnd" has its outgoing flow toward <target>
  When the boundary's message is received
  Then <outcome>

  Examples:
    | target                          | outcome                                                       |
    | an undeclared blank node ID     | the step is refused with an error naming boundary "bnd"       |
    | an undeclared node "ghost"      | the step is refused with an error naming boundary "bnd"       |
    | a declared node whose ID is ""  | the step succeeds and the target node's action is invoked     |
```

### Requirement: A force-termination end selects the terminal status by its outcome
An end event whose end behavior is terminate SHALL cancel all remaining parallel work of the instance and end it at `StatusTerminated` emitting `FailInstance` with the termination reason when its outcome is abort, or at `StatusCompleted` emitting `CompleteInstance` with the instance variables when its outcome is complete or not authored.

#### Scenario: Outcome selects the terminal status
```gherkin
Scenario Outline: An end event with end_behavior "terminate", termination_reason "fraud" and termination_outcome <outcome>
  Given the definition is decoded from JSON and passes model.Validate
  When an instance starts and reaches the end event
  Then the instance status is <status>
  And the step emits <command> and not the other terminal command

  Examples:
    | outcome     | status           | command                                          |
    | "abort"     | StatusTerminated | FailInstance carrying "fraud"                    |
    | "complete"  | StatusCompleted  | CompleteInstance carrying the instance variables |
    | (omitted)   | StatusCompleted  | CompleteInstance carrying the instance variables |
    | ""          | StatusCompleted  | CompleteInstance carrying the instance variables |
```

#### Scenario: Parallel work is swept
```gherkin
Scenario Outline: A fork whose first branch opens a user task and whose second branch force-terminates
  Given the end event's outcome is <outcome>
  When an instance starts
  Then the open human task is Cancelled and one UpdateTask command cancels it
  And no token, timer, armed event or boundary arm remains
  And the instance has an end time

  Examples:
    | outcome  |
    | abort    |
    | complete |
```
