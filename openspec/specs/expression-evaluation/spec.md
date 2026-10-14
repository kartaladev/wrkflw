# expression-evaluation Specification

## Purpose
Defines how the expression strings a process definition carries (flow conditions, boundary error expressions, timer and deadline durations, correlation keys and `expr` validation predicates) are evaluated against instance data, and what that data can and cannot do to them.

## Requirements

### Requirement: Instance data is bound as data, never evaluated as expression source
An expression MUST be taken only from the process definition, and instance data, including variable values, variable key names and error text derived from them, MUST reach it only as values in its environment, so that text shaped like expression syntax never changes what the expression computes.

#### Scenario: Expression-shaped error text does not satisfy a boundary equality
```gherkin
Scenario Outline: Expression-shaped error text does not satisfy a boundary equality
  Given a service task with an error boundary whose error expression is _error == "PAY_ERR"
  And its action fails with an error whose message embeds the caller-chosen key name <key>
  When the failure is delivered
  Then the boundary does not catch and the error propagates

  Examples:
    | key                  |
    | " or true or "       |
    | == 'x' or true or    |
    | PAY_ERR              |
```

#### Scenario: Expression-shaped variable text does not satisfy a gateway condition
```gherkin
Given a service task whose terminal failure routes down its recovery flow into an exclusive gateway
And the gateway's conditional flow is _errorMessage == "boom" with a default flow to "log"
And the failure's message embeds the caller-chosen key name " or true or "
When the failure is delivered
Then the token takes the default flow to "log"
```

#### Scenario: The same boundary still matches the data it asks for
```gherkin
Given a service task whose action fails with an error whose message embeds the caller-chosen key name PAY_ERR
And an error boundary whose error expression is _error contains S, where S is a fixed substring of that failure message other than the key name
When the failure is delivered
Then the boundary catches the failure
```

### Requirement: A condition that is not a valid boolean expression is an error
A condition that fails to compile, or that yields a non-boolean result, MUST produce an error naming the condition rather than a boolean.

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

### Requirement: Duration expressions accept durations, numbers of seconds and duration strings
A timer or deadline duration expression MUST yield a duration from a duration value as-is, from an integer of any width as whole seconds, from a float64 as fractional seconds, and from a string accepted by Go's duration parser; any other result, an unparseable string or a syntax error MUST be refused with an error naming the expression.

#### Scenario: Duration by result form
```gherkin
Scenario Outline: Duration by result form
  Given the duration expression <code>
  When it is evaluated against the variables <vars>
  Then the outcome is <outcome>

  Examples:
    | code       | vars                      | outcome                                        |
    | "3h"       | {}                        | 3h                                             |
    | "90s"      | {}                        | 90s                                            |
    | 90         | {}                        | 90s                                            |
    | slaSeconds | {"slaSeconds": 30}        | 30s                                            |
    | v          | {"v": int8 5}             | 5s                                             |
    | v          | {"v": uint64 17}          | 17s                                            |
    | v          | {"v": 2h as a duration}   | 2h                                             |
    | 1.5        | {}                        | 1.5s                                           |
    | true       | {}                        | refused with an error naming the expression    |
    | "xyz"      | {}                        | refused with an error naming the expression    |
    | duration > | {}                        | refused with an error naming the expression    |
```

### Requirement: Correlation key expressions render to strings
A correlation key expression MUST yield its string result as-is, MUST render any other result in its default textual form, and MUST yield the empty string for an empty expression; a syntax error MUST be refused with an error naming the expression.

#### Scenario: Correlation key by result form
```gherkin
Scenario Outline: Correlation key by result form
  Given the correlation key expression <code>
  When it is evaluated against the variables <vars>
  Then the outcome is <outcome>

  Examples:
    | code      | vars                    | outcome                                     |
    | (empty)   | {}                      | ""                                          |
    | "hello"   | {}                      | "hello"                                     |
    | orderId   | {"orderId": "ORD-42"}   | "ORD-42"                                    |
    | id        | {"id": 99}              | "99"                                        |
    | flag      | {"flag": true}          | "true"                                      |
    | orderId > | {"orderId": "x"}        | refused with an error naming the expression |
```

### Requirement: Expression evaluation is bounded on opt-in
When built with `WithExpressionTimeout(d)` for a positive d, a `ProcessDriver` MUST abandon any single evaluation of a gateway condition, a timer or deadline duration, or a correlation key that exceeds d, and fail the call with an error naming the expression-evaluation timeout instead of blocking.

#### Scenario: A runaway expression is abandoned under the opt-in timeout
```gherkin
Scenario Outline: A runaway expression is abandoned under the opt-in timeout
  Given a ProcessDriver built with WithExpressionTimeout(50ms)
  And a definition whose <expression> calls a variable function that never returns
  When the instance is driven
  Then the call fails with an error naming the expression-evaluation timeout
  And it returns well before 2 seconds have elapsed

  Examples:
    | expression                   |
    | exclusive gateway condition  |
    | timer duration               |
    | message correlation key      |
```

#### Scenario: A normal condition evaluates under the default driver
```gherkin
Given a ProcessDriver built with no expression option
And a definition whose exclusive gateway routes amount > 100 to "big"
When the instance is driven with the variables {"amount": 150}
Then the instance completes
```

### Requirement: The expr validation kind requires every predicate to hold
A validation built from the `expr` kind MUST accept an input only when every one of its predicates evaluates to true, and MUST refuse, with an error naming the failing predicate, an input on which any predicate is false or references an absent variable.

#### Scenario: Validation outcome by input
```gherkin
Scenario Outline: Validation outcome by input
  Given an expr validation with the predicates decision in ['approve','reject'] and amount > 0
  When it validates <input>
  Then <outcome>

  Examples:
    | input                                    | outcome                                                         |
    | {"decision": "approve", "amount": 5}     | the input is accepted                                           |
    | {"decision": "maybe", "amount": 5}       | it is refused with an error naming decision in ['approve','reject'] |
    | {"decision": "approve"}                  | it is refused with an error naming amount > 0                   |
```
