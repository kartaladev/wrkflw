## ADDED Requirements

### Requirement: A parallel join counts each token that crossed no flow toward at most one incoming flow
At a converging parallel gateway, a parked token that reached it without crossing a sequence flow SHALL satisfy at most one incoming flow, and only one that no crossing token satisfies. Crossing tokens are matched first, so such a token MUST NOT displace a crossing token. Several such tokens satisfy at most one flow each.

#### Scenario: Tokens with no crossed flow at a join
```gherkin
Scenario Outline: <empty> no-flow tokens and crossings over <crossed> at a join with <n> incoming flows
  Given a definition whose parallel join "join" has <n> incoming flows and whose fork also feeds an idle branch "z" that never reaches the join
  And an instance state parking at "join" <empty> tokens that crossed no flow, as decoded from a snapshot written before arrival flows were recorded
  And parking at "join" one token for each flow in <crossed>, each having crossed that flow
  When the action "z" completes, so the step reaches the drive loop without any token entering "join"
  Then the end event after "join" is <reached>
  And <parked> tokens remain parked at "join"

  Examples:
    | n | empty | crossed  | reached                 | parked |
    | 2 | 0     | fj1      | not reached             | 1      |
    | 2 | 1     | none     | not reached             | 1      |
    | 2 | 1     | fj1      | reached exactly once    | 0      |
    | 2 | 1     | fj2      | reached exactly once    | 0      |
    | 3 | 1     | fj1      | not reached             | 2      |
    | 3 | 1     | fj1, fj2 | reached exactly once    | 0      |
    | 3 | 2     | none     | not reached             | 2      |
```

#### Scenario: Pending Q3 - several no-flow tokens satisfy one flow each
```gherkin
Scenario Outline: <empty> no-flow tokens and crossings over <crossed> at a join with <n> incoming flows
  Given the definition and instance state of the scenario above
  When the action "z" completes
  Then the end event after "join" is <reached>
  And <parked> tokens remain parked at "join"

  Examples:
    | n | empty | crossed | reached              | parked |
    | 2 | 2     | none    | reached exactly once | 0      |
    | 3 | 2     | fj1     | reached exactly once | 0      |
    | 3 | 3     | none    | reached exactly once | 0      |
    | 2 | 3     | none    | reached exactly once | 1      |
```

#### Scenario: Two crossings over one flow are not rescued by a no-flow token for that same flow
```gherkin
Scenario: Negative control - the no-flow token covers only the flow nobody crossed
  Given a join with incoming flows "fj1" and "fj2"
  And two tokens parked at "join" that both crossed "fj1", and one parked no-flow token
  When the action "z" completes
  Then the end event after "join" is reached exactly once
  And one token that crossed "fj1" remains parked at "join"
```

### Requirement: A join that fires on a token that crossed no flow logs a warning
When a converging parallel gateway fires and at least one of the tokens it consumes crossed no sequence flow, the engine SHALL log a warning naming the instance, the join and how many such tokens were consumed, and MUST still fire; a firing that consumes only crossing tokens MUST NOT log that warning.

#### Scenario: Pending Q4 - the early fire is visible
```gherkin
Scenario Outline: The join fires consuming <empty> no-flow tokens
  Given a join with incoming flows "fj1" and "fj2", with tokens parked as in <parking>
  When the action "z" completes
  Then the end event after "join" is reached exactly once
  And a WARN log names the instance, "join" and the count <empty>

  Examples:
    | parking                                         | empty |
    | one crossed "fj1", one crossed no flow          | 1     |
    | two crossed no flow                             | 2     |
```

#### Scenario: Pending Q4 - a firing on crossing tokens alone is silent
```gherkin
Scenario: Negative control - each flow delivered one crossing token
  Given a definition start -> fork -> {a, b} -> join -> end
  When the actions "a" and "b" both complete
  Then the instance status is StatusCompleted
  And no warning about tokens that crossed no flow is logged
```

### Requirement: Join readiness is re-tested when the token set settles
When a step leaves no active token, a converging parallel or inclusive gateway whose wait has been discharged without a token entering it on that step SHALL fire then, exactly once. A join whose wait is not discharged MUST NOT fire, and the instance MUST stay `StatusRunning` with its tokens parked at the join.

#### Scenario: A discharged wait fires the join
```gherkin
Scenario Outline: <join> join, discharged by <event>
  Given <setup>
  When <event>
  Then the end event after the join is entered exactly once
  And no token remains parked at the join
  And the instance status is StatusCompleted

  Examples:
    | join      | setup                                                                                             | event                                                              |
    | inclusive | an inclusive split into sub-process "sub" and "svcB", "svcB" done and parked at "orjoin"           | the sub-process's branch leaves by its default flow away from "orjoin" and its scope closes |
    | parallel  | a fork into "svcA", "svcB" and idle "svcZ", with one crossing token parked at "join" for each of its flows | the action "z" completes                                            |
```

#### Scenario: An undischarged wait does not fire the join
```gherkin
Scenario Outline: Negative control - <join> join with <pending> still pending, settled by <event>
  Given <setup>
  When <event>
  Then the end event after the join is not reached
  And <parked> token is still parked at the join
  And the instance status is StatusRunning

  Examples:
    | join      | pending                          | setup                                                                                  | event                         | parked |
    | parallel  | the flow from "svcB"             | a fork into "svcA", "svcB" and idle "svcZ"; "svcA" done and parked at "join", "svcB" waiting | the action "z" completes      | one    |
    | inclusive | the sub-process "sub" branch     | an inclusive split into "sub", "svcB" and idle "svcZ"; "svcB" done and parked at "orjoin", "sub" running | the action "z" completes | one    |
```

#### Scenario: The pending branch then arrives
```gherkin
Scenario Outline: At-limit accept - the row above, after <arrival>
  Given the undischarged <join> instance of the scenario above, after "z" completed
  When <arrival>
  Then the end event after the join is entered exactly once
  And the instance status is StatusCompleted

  Examples:
    | join      | arrival                                         |
    | parallel  | the action "b" completes                        |
    | inclusive | the sub-process's inner action completes         |
```
