## MODIFIED Requirements

### Requirement: The default retry policy carries Temporal-style values
`DefaultRetryPolicy` MUST return MaxAttempts 3, InitialInterval 1s, BackoffCoef 2.0 and MaxInterval 100s. `RetryPolicy.Normalize` MUST replace a MaxAttempts that is zero or negative other than `model.UnlimitedAttempts`, a non-positive InitialInterval or MaxInterval, and a BackoffCoef below 1.0 with those defaults, and MUST keep `model.UnlimitedAttempts` and every field already set to a usable value.

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
    | InitialInterval 1s (MaxAttempts omitted)            | MaxAttempts     | 3     |
    | MaxAttempts 0                                       | MaxAttempts     | 3     |
    | MaxAttempts -2                                      | MaxAttempts     | 3     |
    | MaxAttempts model.UnlimitedAttempts                 | MaxAttempts     | model.UnlimitedAttempts |
    | MaxAttempts 1                                       | MaxAttempts     | 1     |
```

### Requirement: Validate refuses a retry policy outside its documented ranges
`model.Validate` MUST refuse with `model.ErrInvalidRetryPolicy` a definition whose node retry policy has a MaxAttempts below `model.UnlimitedAttempts`, a negative InitialInterval or MaxInterval, or a BackoffCoef below 1.0 together with a positive InitialInterval, and MUST accept a policy within those ranges, including one whose MaxAttempts is `model.UnlimitedAttempts` or zero.

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
    | MaxAttempts -2                              | model.ErrInvalidRetryPolicy   |
    | InitialInterval -1s                         | model.ErrInvalidRetryPolicy   |
    | MaxInterval -1s                             | model.ErrInvalidRetryPolicy   |
    | MaxAttempts 3, InitialInterval 1s, BackoffCoef 1.0 | accepted               |
    | MaxAttempts 3, InitialInterval 0, BackoffCoef 0.5  | accepted               |
    | MaxAttempts 3, InitialInterval 1s, BackoffCoef 2, MaxInterval 1m | accepted |
    | MaxAttempts model.UnlimitedAttempts, InitialInterval 1s | accepted          |
    | MaxAttempts 0, InitialInterval 1s           | accepted                      |
```

## ADDED Requirements

### Requirement: A retry policy that does not set MaxAttempts is bounded
A retry policy whose MaxAttempts is zero, at any tier (node, action, runtime default or compensation), MUST end retrying at the default of 3 attempts, and only a policy whose MaxAttempts is `model.UnlimitedAttempts` SHALL retry without an attempt bound; both forms MUST mean the same after a JSON or YAML round trip.

#### Scenario: Attempt bound by declared MaxAttempts
```gherkin
Scenario Outline: Attempt bound by declared MaxAttempts
  Given a service task whose effective policy comes from <tier> with <max_attempts> and InitialInterval 1s
  And its action fails retryably on every invocation
  When the instance is driven and each retry timer fires
  Then after the <attempt> failure <outcome>

  Examples:
    | tier                                  | max_attempts                  | attempt | outcome                     |
    | the node policy                       | MaxAttempts omitted           | 2nd     | a retry timer is armed      |
    | the node policy                       | MaxAttempts omitted           | 3rd     | no retry timer is armed     |
    | an action's RetrySpecs                | MaxAttempts 0                 | 3rd     | no retry timer is armed     |
    | WithDefaultRetryPolicy                | MaxAttempts 0                 | 3rd     | no retry timer is armed     |
    | the node policy                       | MaxAttempts 1                 | 1st     | no retry timer is armed     |
    | the node policy                       | MaxAttempts model.UnlimitedAttempts | 10th | a retry timer is armed   |
```

#### Scenario: A compensation policy without MaxAttempts is bounded
```gherkin
Given a ProcessDriver built with WithCompensationRetryPolicy whose MaxAttempts is 0 and InitialInterval is 1s
And a compensation action that fails retryably on every invocation
When the compensation walk runs and each retry timer fires
Then the action is invoked 3 times
And the walk skips the record and raises a compensation-failed incident
```

#### Scenario: The unlimited spelling survives the wire in both formats
```gherkin
Scenario Outline: The unlimited spelling survives the wire in both formats
  Given a definition whose service task retry policy is authored in <format> with <authored>
  When it is decoded, encoded as JSON and decoded again
  Then the node's MaxAttempts is <value>

  Examples:
    | format | authored                     | value                   |
    | JSON   | "max_attempts": -1           | model.UnlimitedAttempts |
    | YAML   | max_attempts: -1             | model.UnlimitedAttempts |
    | JSON   | no max_attempts key          | 0, normalized to 3      |
    | YAML   | no max_attempts key          | 0, normalized to 3      |
```
