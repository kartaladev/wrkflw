## Purpose

Defines how a failed action is retried: which retry policy is in effect for a node, how its fields are validated, defaulted and turned into a backoff delay, and which failures end retrying at once.

## ADDED Requirements

### Requirement: An action error is retryable unless marked otherwise
`action.IsRetryable` MUST report a nil error and a plain error as retryable, and MUST report as not retryable any error whose chain contains an error wrapped by `action.NonRetryable`; `action.NonRetryable` MUST return nil for nil and otherwise an error that unwraps to, and prints exactly as, the error it wraps.

#### Scenario: Retry classification by error
```gherkin
Scenario Outline: Retry classification by error
  Given the error <err>
  When action.IsRetryable is called with it
  Then it reports <retryable>

  Examples:
    | err                                                          | retryable |
    | nil                                                          | true      |
    | errors.New("boom")                                           | true      |
    | action.NonRetryable(errors.New("4xx"))                       | false     |
    | errors.Join(errors.New("ctx"), action.NonRetryable(errors.New("4xx"))) | false |
```

#### Scenario: NonRetryable is transparent to inspection
```gherkin
Given a sentinel error "original"
When it is wrapped with action.NonRetryable
Then errors.Is reports the wrapper matches the sentinel
And action.NonRetryable(errors.New("boom")).Error() equals "boom"
And action.NonRetryable(nil) is nil
```

#### Scenario: A non-retryable action error reaches the engine as non-retryable
```gherkin
Scenario Outline: A non-retryable action error reaches the engine as non-retryable
  Given a service task with no retry policy whose action returns <err>
  When the instance is driven
  Then the recorded action failure has Retryable = <retryable>

  Examples:
    | err                                     | retryable |
    | action.NonRetryable(errors.New("4xx"))  | false     |
    | errors.New("boom")                      | true      |
```

### Requirement: The effective retry policy is chosen action first, then node, then runtime default
For a failing action the effective policy MUST be the action's own declared retry specs when present, else the node's retry policy, else the runtime default, else none; an action-declared policy MUST win on attempts and backoff while keeping the node's MaxElapsed and NonRetryableErrors.

#### Scenario: Precedence across the three tiers
```gherkin
Scenario Outline: Precedence across the three tiers
  Given a node policy <node>, an action-declared policy <action> and a runtime default <default>
  When the effective retry policy is resolved
  Then <outcome>

  Examples:
    | node              | action            | default           | outcome                                   |
    | MaxAttempts 7     | MaxAttempts 9     | MaxAttempts 3     | the effective MaxAttempts is 9            |
    | MaxAttempts 7     | none              | MaxAttempts 3     | the effective MaxAttempts is 7            |
    | none              | none              | MaxAttempts 3     | the effective MaxAttempts is 3            |
    | none              | MaxAttempts 9     | none              | the effective MaxAttempts is 9            |
    | none              | none              | none              | no retry policy is in effect              |
```

#### Scenario: The action tier keeps the node's safety fields
```gherkin
Given a node policy with MaxAttempts 7, BackoffCoef 2, MaxElapsed 5m and NonRetryableErrors ["permission denied", "not found"]
And an action-declared policy with MaxAttempts 9 and BackoffCoef 3
When the effective retry policy is resolved
Then its MaxAttempts is 9 and its BackoffCoef is 3
And its MaxElapsed is 5m and its NonRetryableErrors are ["permission denied", "not found"]
```

#### Scenario: Without a node policy the action tier carries no safety fields
```gherkin
Given a node with no retry policy
And an action-declared policy with MaxAttempts 9
When the effective retry policy is resolved
Then its MaxAttempts is 9, its MaxElapsed is zero and its NonRetryableErrors are empty
```

#### Scenario: An action's retry specs override a stricter node policy end to end
```gherkin
Given a service task whose node policy allows 1 attempt
And its action is wrapped with action.WithRetrySpecs allowing 5 attempts from a 1s interval
And the action fails twice and then succeeds
When the instance is driven and each retry timer fires
Then the instance completes after 3 invocations
```

#### Scenario: A runtime default enables retry for a node that declares none
```gherkin
Given a ProcessDriver built with WithDefaultRetryPolicy allowing retries from a 1s interval
And a service task with no retry policy whose action fails
When the instance is driven
Then a retry timer is scheduled for 1s later instead of the instance failing
```

### Requirement: A retryable failure with budget left schedules a backoff retry
When a policy is in effect and the failure is not terminal, the engine MUST park the token on a one-shot retry timer whose delay is the policy's backoff for the attempt, scaled by the failure's jitter fraction only when that fraction is positive, MUST count the attempt, and MUST re-invoke the action when the timer fires.

#### Scenario: Retry delay by jitter fraction
```gherkin
Scenario Outline: Retry delay by jitter fraction
  Given a service task with MaxAttempts 5, InitialInterval 1s and BackoffCoef 2
  When its first invocation fails retryably with jitter fraction <jitter>
  Then a retry timer is scheduled with delay <delay>

  Examples:
    | jitter  | delay |
    | not set | 1s    |
    | 0       | 1s    |
    | 0.5     | 500ms |
```

#### Scenario: The first retry parks the token and counts the attempt
```gherkin
Scenario Outline: The first retry parks the token and counts the attempt
  Given a service task with MaxAttempts 3, InitialInterval 1s and BackoffCoef 2
  When its first invocation fails retryably with jitter fraction <jitter>
  Then the token is waiting on the scheduled retry timer with 1 retry attempt counted

  Examples:
    | jitter  |
    | not set |
    | 0.5     |
```

#### Scenario: The retry timer re-invokes the action
```gherkin
Given a service task with MaxAttempts 3 parked on a retry timer after a retryable failure
When the retry timer fires
Then the action is invoked again for the same node
```

#### Scenario: With no policy in effect a failure is not retried
```gherkin
Given a service task with no node policy and no runtime default
When its invocation fails retryably
Then no retry timer is scheduled
```

### Requirement: Retrying stops at the first terminal condition
A failure MUST be terminal, with no retry scheduled, when the error is not retryable, when its text contains one of the policy's non-empty NonRetryableErrors substrings, when the next attempt would reach a positive MaxAttempts, or when more than a positive MaxElapsed has passed since the first retry was scheduled.

#### Scenario: Terminal conditions end retrying
```gherkin
Scenario Outline: Terminal conditions end retrying
  Given a service task under the effective policy <policy>
  When its invocation fails with <failure>
  Then no retry timer is scheduled

  Examples:
    | policy                                         | failure                                         |
    | MaxAttempts 3                                  | a non-retryable error                           |
    | MaxAttempts 1                                  | a retryable error on the first attempt          |
    | MaxAttempts 5, NonRetryableErrors ["validation"] | a retryable error "input validation failed"   |
    | MaxAttempts 5, MaxElapsed 1m                   | a retryable error 199s after the first retry was scheduled |
```

#### Scenario: The legal neighbours of each terminal condition are retried
```gherkin
Scenario Outline: The legal neighbours of each terminal condition are retried
  Given a service task under the effective policy <policy>
  When its invocation fails with <failure>
  Then a retry timer is armed

  Examples:
    | policy                                           | failure                                          |
    | MaxAttempts 3 (runtime default)                  | a plain retryable error                          |
    | MaxAttempts 5, NonRetryableErrors ["validation"] | a retryable error "timeout"                      |
    | MaxAttempts 5, MaxElapsed 1m                     | a retryable error 29s after the first retry was scheduled |
    | MaxAttempts 5, MaxElapsed 1m                     | a retryable error exactly 60s after the first retry was scheduled |
```

#### Scenario: Substring matching of NonRetryableErrors
```gherkin
Scenario Outline: Substring matching of NonRetryableErrors
  Given a policy with NonRetryableErrors <list>
  When the error text <text> is checked
  Then it is <verdict>

  Examples:
    | list                       | text                      | verdict       |
    | ["validation", "not found"] | "input validation failed" | non-retryable |
    | ["validation", "not found"] | "timeout"                 | retryable     |
    | [""]                       | "timeout"                 | retryable     |
```

### Requirement: Backoff grows exponentially up to the cap
`RetryPolicy.Backoff(attempt)` MUST return InitialInterval × BackoffCoef^attempt for the zero-based retry number, capped at MaxInterval when MaxInterval is positive, and MUST return zero when InitialInterval is zero or negative.

#### Scenario: Backoff by attempt
```gherkin
Scenario Outline: Backoff by attempt
  Given a policy with InitialInterval <initial>, BackoffCoef 2 and MaxInterval 10s
  When Backoff is computed for attempt <attempt>
  Then it returns <delay>

  Examples:
    | initial | attempt | delay |
    | 1s      | 0       | 1s    |
    | 1s      | 1       | 2s    |
    | 1s      | 2       | 4s    |
    | 1s      | 10      | 10s   |
    | 0       | 0       | 0     |
```

### Requirement: The default retry policy carries Temporal-style values
`DefaultRetryPolicy` MUST return MaxAttempts 3, InitialInterval 1s, BackoffCoef 2.0 and MaxInterval 100s, and `RetryPolicy.Normalize` MUST replace a non-positive InitialInterval or MaxInterval and a BackoffCoef below 1.0 with those defaults while keeping each of those three fields already set to a usable value.

#### Scenario: Default policy values
```gherkin
When DefaultRetryPolicy is called
Then it returns MaxAttempts 3, InitialInterval 1s, BackoffCoef 2.0 and MaxInterval 100s
```

#### Scenario: Normalize fills unset fields and keeps set ones
```gherkin
Scenario Outline: Normalize fills unset fields and keeps set ones
  Given the policy <policy>
  When it is normalized
  Then <field> is <value>

  Examples:
    | policy                                              | field           | value |
    | MaxAttempts 5                                       | InitialInterval | 1s    |
    | MaxAttempts 5                                       | BackoffCoef     | 2.0   |
    | MaxAttempts 5                                       | MaxInterval     | 100s  |
    | MaxAttempts 5, BackoffCoef 0.99                     | BackoffCoef     | 2.0   |
    | MaxAttempts 5, InitialInterval 5s, BackoffCoef 1.0, MaxInterval 1m | InitialInterval | 5s |
    | MaxAttempts 5, InitialInterval 5s, BackoffCoef 1.0, MaxInterval 1m | BackoffCoef     | 1.0 |
    | MaxAttempts 5, InitialInterval 5s, BackoffCoef 1.0, MaxInterval 1m | MaxInterval     | 1m  |
```

### Requirement: Validate refuses a retry policy outside its documented ranges
`model.Validate` MUST refuse a definition whose node retry policy has a negative MaxAttempts, InitialInterval or MaxInterval, or a BackoffCoef below 1.0 together with a positive InitialInterval, with `model.ErrInvalidRetryPolicy`, and MUST accept a policy within those ranges.

#### Scenario: Retry policy validation by field values
```gherkin
Scenario Outline: Retry policy validation by field values
  Given a definition start → service task → end whose service task carries the retry policy <policy>
  When the definition is validated
  Then the outcome is <outcome>

  Examples:
    | policy                                      | outcome                       |
    | InitialInterval 1s, BackoffCoef -1          | model.ErrInvalidRetryPolicy   |
    | InitialInterval 1s, BackoffCoef 0.99        | model.ErrInvalidRetryPolicy   |
    | MaxAttempts -1                              | model.ErrInvalidRetryPolicy   |
    | InitialInterval -1s                         | model.ErrInvalidRetryPolicy   |
    | MaxInterval -1s                             | model.ErrInvalidRetryPolicy   |
    | MaxAttempts 3, InitialInterval 1s, BackoffCoef 1.0 | accepted               |
    | MaxAttempts 3, InitialInterval 0, BackoffCoef 0.5  | accepted               |
    | MaxAttempts 3, InitialInterval 1s, BackoffCoef 2, MaxInterval 1m | accepted |
```

### Requirement: A retry declaration on an action is not an in-process retry
Attaching retry specs to an action with `action.Wrap` and `action.WithRetrySpecs` MUST only declare a policy for the runtime's durable retry mechanism; invoking the wrapped action directly MUST run it exactly once per call.

#### Scenario: A wrapped action fails once per direct call
```gherkin
Given an action that always fails, wrapped with action.WithRetrySpecs allowing 5 attempts
When the wrapped action is invoked directly once
Then the underlying action ran exactly once
And the failure is returned to the caller
```
