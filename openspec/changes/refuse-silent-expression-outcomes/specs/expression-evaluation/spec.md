## MODIFIED Requirements

### Requirement: A condition that is not a valid boolean expression is an error
A condition that fails to compile, or that yields a non-boolean result, MUST produce an error naming the condition rather than a boolean. A condition that reads a process variable the instance does not hold MUST be refused with `engine.ErrAbsentVariable`, naming that variable, and MUST NOT evaluate to false.

#### Scenario: Invalid conditions are errors
```gherkin
Scenario Outline: Invalid conditions are errors
  Given the condition <code>
  When it is evaluated against the variables {"amount": 1}
  Then it is refused with an error naming the condition <code>

  Examples:
    | code       |
    | amount >   |
    | amount + 1 |
```

#### Scenario: A valid boolean condition evaluates
```gherkin
Scenario Outline: A valid boolean condition evaluates
  Given the condition amount > 100
  When it is evaluated against the variables <vars>
  Then the result is <result> with no error

  Examples:
    | vars            | result |
    | {"amount": 150} | true   |
    | {"amount": 50}  | false  |
```

#### Scenario: A condition over an absent variable is refused
```gherkin
Scenario Outline: A condition over an absent variable is refused
  Given the condition <code>
  When it is evaluated against the variables <vars>
  Then it is refused with ErrAbsentVariable naming the variable <absent>
  And no boolean result is produced

  Examples:
    | code                          | vars                     | absent |
    | amount > 100                  | {}                       | amount |
    | amount > 100                  | {"Amount": 150}          | amount |
    | amount > 100                  | {"amount ": 150}         | amount |
    | status == "x"                 | {"amount": 1}            | status |
    | status != "x"                 | {"amount": 1}            | status |
    | amount > 100 && missing > 1   | {"amount": 150}          | missing |
    | région == "eu"                | {"region": "eu"}         | région |
```

#### Scenario: Negative control - the same conditions evaluate once the variable is present
```gherkin
Scenario Outline: The same conditions evaluate once the variable is present
  Given the condition <code>
  When it is evaluated against the variables <vars>
  Then the result is <result> with no error

  Examples:
    | code                          | vars                                | result |
    | amount > 100                  | {"amount": 150}                     | true   |
    | status == "x"                 | {"status": "x"}                     | true   |
    | status != "x"                 | {"status": "x"}                     | false  |
    | amount > 100 && missing > 1   | {"amount": 150, "missing": 2}       | true   |
    | région == "eu"                | {"région": "eu"}                    | true   |
```

### Requirement: Expression evaluation is bounded on opt-in
When built with `WithExpressionTimeout(d)` for a positive d, a `ProcessDriver` MUST abandon any single evaluation of a gateway condition, a boundary error expression, a timer or deadline duration, or a correlation key that exceeds d, and fail the call with an error that matches `engine.ErrExpressionTimeout` under `errors.Is`, instead of blocking.

#### Scenario: A runaway expression is abandoned under the opt-in timeout
```gherkin
Scenario Outline: A runaway expression is abandoned under the opt-in timeout
  Given a ProcessDriver built with WithExpressionTimeout(50ms)
  And a definition whose <expression> calls a variable function that never returns
  When the instance is driven
  Then the call fails with an error matching engine.ErrExpressionTimeout
  And it returns well before 2 seconds have elapsed

  Examples:
    | expression                                         |
    | exclusive gateway condition                        |
    | timer duration                                     |
    | message correlation key                            |
    | error boundary expression on a failing service task |
```

#### Scenario: A normal condition evaluates under the default driver
```gherkin
Given a ProcessDriver built with no expression option
And a definition whose exclusive gateway routes amount > 100 to "big"
When the instance is driven with the variables {"amount": 150}
Then the instance completes
```

#### Scenario: Negative control - a prompt boundary error expression still matches under the opt-in timeout
```gherkin
Given a ProcessDriver built with WithExpressionTimeout(50ms)
And a service task whose action fails with error code "PAY_ERR"
And an error boundary on it whose error expression is _error == "PAY_ERR"
When the instance is driven
Then the boundary catches the failure
And the call returns no error
```

## ADDED Requirements

### Requirement: A boundary error expression that cannot be evaluated is skipped unless it timed out
When evaluating a boundary error expression fails, the engine SHALL treat that boundary as a non-match and go on to the next candidate boundary; when the failure is `engine.ErrExpressionTimeout`, the step MUST instead fail with it, and no further boundary, in this scope or an enclosing one, SHALL be considered.

#### Scenario: Boundary outcome by evaluation failure
```gherkin
Scenario Outline: Boundary outcome by evaluation failure
  Given a ProcessDriver built with WithExpressionTimeout(50ms)
  And a <host> that fails with error code "PAY_ERR"
  And a first error boundary on it whose error expression is <expression>
  And a second, catch-all error boundary on it that routes to "fallback"
  When the failure is delivered
  Then <outcome>

  Examples:
    | host                               | expression                        | outcome                                                                  |
    | service task                       | _error ==                         | the catch-all boundary catches and the token is routed to "fallback"     |
    | service task                       | _error + 1                        | the catch-all boundary catches and the token is routed to "fallback"     |
    | service task                       | _error == "PAY_ERR" && tier > 1   | the catch-all boundary catches and the token is routed to "fallback"     |
    | service task                       | block()                           | the call fails with engine.ErrExpressionTimeout and "fallback" is not entered |
    | sub-process whose inner task fails | block()                           | the call fails with engine.ErrExpressionTimeout and "fallback" is not entered |
    | call activity whose child fails    | block()                           | the call fails with engine.ErrExpressionTimeout and "fallback" is not entered |
```

#### Scenario: A timed-out boundary does not fall through to an enclosing scope
```gherkin
Given a ProcessDriver built with WithExpressionTimeout(50ms)
And a service task inside sub-process "sub" that fails with error code "PAY_ERR"
And an error boundary on the service task whose error expression calls block(), which never returns
And a catch-all error boundary on "sub" that routes to "outerFallback"
When the failure is delivered
Then the call fails with engine.ErrExpressionTimeout
And "outerFallback" is not entered
And the instance is not failed
```

#### Scenario: Negative control - a boundary error expression that evaluates is decided by its result
```gherkin
Scenario Outline: A boundary error expression that evaluates is decided by its result
  Given a service task that fails with error code "PAY_ERR"
  And a first error boundary on it whose error expression is <expression>, routing to "handler"
  And a second, catch-all error boundary on it that routes to "fallback"
  When the failure is delivered
  Then the token is routed to <target>

  Examples:
    | expression            | target     |
    | _error == "PAY_ERR"   | "handler"  |
    | _error == "OTHER"     | "fallback" |
```
