## MODIFIED Requirements

### Requirement: A condition that is not a valid boolean expression is an error
A condition that fails to compile MUST be refused with `engine.ErrInvalidExpression`, and a condition that yields a non-boolean result with `engine.ErrConditionNotBoolean`, each in an error naming the condition rather than a boolean.

#### Scenario: Invalid conditions are errors
```gherkin
Scenario Outline: Invalid conditions are errors
  Given the condition <code>
  When it is evaluated against the variables {"amount": 1}
  Then it is refused with <sentinel>
  And the error names the condition <code>
  And the error does not match <other>

  Examples:
    | code       | sentinel                      | other                         |
    | amount >   | engine.ErrInvalidExpression   | engine.ErrConditionNotBoolean |
    | amount + 1 | engine.ErrConditionNotBoolean | engine.ErrInvalidExpression   |
    | "approved" | engine.ErrConditionNotBoolean | engine.ErrInvalidExpression   |
```

#### Scenario: The sentinel reaches the caller of a gateway step
```gherkin
Scenario Outline: A gateway condition refusal surfaces through Step
  Given an exclusive gateway whose conditional flow carries the condition <code>
  When a token reaches the gateway with the variables {"amount": 1}
  Then the step fails with <sentinel>
  And the error names the gateway

  Examples:
    | code       | sentinel                      |
    | amount >   | engine.ErrInvalidExpression   |
    | amount + 1 | engine.ErrConditionNotBoolean |
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
A timer or deadline duration expression MUST yield a duration from a duration value as-is, from an integer of any width as whole seconds, from a float64 as fractional seconds, and from a string accepted by Go's duration parser; any other result or an unparseable string MUST be refused with `engine.ErrInvalidDuration`, and a syntax error with `engine.ErrInvalidExpression`, each in an error naming the expression.

#### Scenario: Duration by result form
```gherkin
Scenario Outline: Duration by result form
  Given the duration expression <code>
  When it is evaluated against the variables <vars>
  Then the outcome is <outcome>

  Examples:
    | code       | vars                      | outcome                                                                  |
    | "3h"       | {}                        | 3h                                                                       |
    | "90s"      | {}                        | 90s                                                                      |
    | 90         | {}                        | 90s                                                                      |
    | slaSeconds | {"slaSeconds": 30}        | 30s                                                                      |
    | v          | {"v": int8 5}             | 5s                                                                       |
    | v          | {"v": uint64 17}          | 17s                                                                      |
    | v          | {"v": 2h as a duration}   | 2h                                                                       |
    | 1.5        | {}                        | 1.5s                                                                     |
    | true       | {}                        | refused with engine.ErrInvalidDuration, in an error naming the expression   |
    | "xyz"      | {}                        | refused with engine.ErrInvalidDuration, in an error naming the expression   |
    | ""         | {}                        | refused with engine.ErrInvalidDuration, in an error naming the expression   |
    | duration > | {}                        | refused with engine.ErrInvalidExpression, in an error naming the expression |
```

#### Scenario: Negative control - an unparseable duration is not a syntax error
```gherkin
Scenario Outline: The two duration refusals stay distinct
  Given the duration expression <code>
  When it is evaluated against the variables {}
  Then the error does not match <other>

  Examples:
    | code       | other                       |
    | "xyz"      | engine.ErrInvalidExpression |
    | duration > | engine.ErrInvalidDuration   |
```

### Requirement: Correlation key expressions render to strings
A correlation key expression MUST yield its string result as-is, MUST render any other result in its default textual form, and MUST yield the empty string for an empty expression; a syntax error MUST be refused with `engine.ErrInvalidExpression`, in an error naming the expression.

#### Scenario: Correlation key by result form
```gherkin
Scenario Outline: Correlation key by result form
  Given the correlation key expression <code>
  When it is evaluated against the variables <vars>
  Then the outcome is <outcome>

  Examples:
    | code      | vars                    | outcome                                                                  |
    | (empty)   | {}                      | ""                                                                       |
    | "hello"   | {}                      | "hello"                                                                  |
    | orderId   | {"orderId": "ORD-42"}   | "ORD-42"                                                                 |
    | id        | {"id": 99}              | "99"                                                                     |
    | flag      | {"flag": true}          | "true"                                                                   |
    | orderId > | {"orderId": "x"}        | refused with engine.ErrInvalidExpression, in an error naming the expression |
```

### Requirement: The expr validation kind requires every predicate to hold
A validation built from the `expr` kind MUST accept an input only when every one of its predicates evaluates to true, and MUST refuse with `expr.ErrPredicateNotSatisfied`, in an error naming the failing predicate, an input on which any predicate is false or references an absent variable.

#### Scenario: Validation outcome by input
```gherkin
Scenario Outline: Validation outcome by input
  Given an expr validation with the predicates decision in ['approve','reject'] and amount > 0
  When it validates <input>
  Then <outcome>

  Examples:
    | input                                    | outcome                                                                                  |
    | {"decision": "approve", "amount": 5}     | the input is accepted                                                                    |
    | {"decision": "maybe", "amount": 5}       | it is refused with expr.ErrPredicateNotSatisfied, in an error naming decision in ['approve','reject'] |
    | {"decision": "approve"}                  | it is refused with expr.ErrPredicateNotSatisfied, in an error naming amount > 0              |
    | {"decision": "approve", "amount": 0}     | it is refused with expr.ErrPredicateNotSatisfied, in an error naming amount > 0              |
    | {"decision": "approve", "amount": 1}     | the input is accepted                                                                    |
```
