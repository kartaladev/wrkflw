## MODIFIED Requirements

### Requirement: Exclusive gateway takes one branch or refuses the step
A diverging exclusive gateway SHALL route its token onto exactly one outgoing flow: the conditional flow whose condition holds, or else the default flow; when no condition holds and no default flow exists, the step MUST fail with `ErrNoMatchingFlow`. A condition it evaluates that reads a variable the instance does not hold MUST fail the step with `ErrAbsentVariable`, never falling through to the default flow.

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

#### Scenario: An absent variable refuses the step instead of taking the default
```gherkin
Scenario Outline: An exclusive gateway whose condition reads an absent variable
  Given a definition start -> xor with a conditional flow "amount > 100" to "big" and <second flow>
  When an instance starts with the variables <vars>
  Then the step fails with ErrAbsentVariable naming amount
  And neither "big" nor "small" is invoked

  Examples:
    | second flow               | vars             |
    | a default flow to "small" | {}               |
    | a default flow to "small" | {"Amount": 150}  |
    | no other flow             | {}               |
```

#### Scenario: Negative control - a condition the gateway never evaluates is not refused
```gherkin
Scenario: The first conditional flow holds before an absent-variable condition is reached
  Given an exclusive gateway whose outgoing flows, in definition order, are "amount > 100" to "big", "missing > 1" to "other" and a default flow to "small"
  When an instance starts with amount = 150 and no variable named missing
  Then exactly one token is parked on "big" and only "big" is invoked
```

### Requirement: Inclusive fork takes every true branch
A diverging inclusive gateway SHALL place a token on every outgoing flow whose condition holds and on every unconditional non-default flow; it SHALL take the default flow only when no other flow is taken, and MUST fail the step with `ErrNoMatchingFlow` when nothing is taken and no default exists. A condition that reads a variable the instance does not hold MUST fail the step with `ErrAbsentVariable`, and no branch is taken.

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

#### Scenario: An absent variable refuses the inclusive fork
```gherkin
Scenario Outline: Inclusive fork with flows "a > 0" to "ta", "b > 0" to "tb" and a default to "tc"
  Given an inclusive fork with those three outgoing flows
  When an instance starts with the variables <vars>
  Then the step fails with ErrAbsentVariable naming <absent>
  And no action is invoked

  Examples:
    | vars       | absent |
    | {"a": 1}   | b      |
    | {"b": 1}   | a      |
    | {}         | a      |
```
