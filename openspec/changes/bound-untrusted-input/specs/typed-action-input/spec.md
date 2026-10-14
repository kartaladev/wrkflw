## ADDED Requirements

### Requirement: A decode failure's text names keys but carries no input value
When a typed action cannot decode its input, the text of the error, which the runtime records as the action failure, MUST name the offending key and MUST NOT contain any value taken from the input or any Go type name of `In`. The decoder's own error MUST stay reachable with `errors.As` on the error, and the error MUST still match `action.ErrDecodeInput`.

#### Scenario: The decode-failure text omits the input value
```gherkin
Scenario Outline: The decode-failure text omits the input value
  Given an action built with action.Typed over an In declaring <field>
  When the action is invoked with the input <input>
  Then the error matches action.ErrDecodeInput
  And action.IsRetryable reports false for the error
  And the error text contains <key>
  And the error text does not contain <value>
  And the error text does not contain the Go type name of In

  Examples:
    | field                                | input                         | key      | value          |
    | "amount" as int                      | {"amount": 1.5}               | "amount" | "1.5"          |
    | "amount" as int                      | {"amount": 123456789.25}      | "amount" | "123456789.25" |
    | "small" as int8                      | {"small": 300}                | "small"  | "300"          |
    | "u" as uint                          | {"u": -7}                     | "u"      | "-7"           |
    | "f" as float32                       | {"f": 1e300}                  | "f"      | "1e+300"       |
    | "nested" as a struct declaring "x" as int | {"nested": {"x": 9.5}}   | "x"      | "9.5"          |
    | "name" as string                     | {"name": 42}                  | "name"   | "42"           |
    | "amount" as int                      | {"amount": "s3cr3t-value"}    | "amount" | "s3cr3t-value" |
    | "amount" as int                      | {"amount": {"k9": "v9"}}      | "amount" | "v9"           |
```

#### Scenario: The decoder's own error stays reachable in memory
```gherkin
Given an action built with action.Typed over an In declaring "amount" as int
When the action is invoked with the input {"amount": 1.5}
Then errors.As on the error yields the decoder's *json.UnmarshalTypeError
And that error's value description contains "1.5"
And the error text does not contain "1.5"
```

#### Scenario: The recorded action failure and every sink derived from it are value-free
```gherkin
Given a service task "charge" whose action is built with action.Typed over an In declaring "amount" as int
And the task has a recovery flow under a retry policy allowing 3 attempts
When an instance is started with the variables {"amount": 1.5}
And the instance is driven
Then the recorded action failure's text contains "amount" and does not contain "1.5"
And the instance variable "_errorMessage" contains "amount" and does not contain "1.5"
But an error boundary whose Go error check reads the failure's cause can still reach the *json.UnmarshalTypeError
```

#### Scenario: Negative control - a strict unknown-key refusal still names the key
```gherkin
Given an action built with action.Typed and action.WithStrictInput over an In declaring only "ref"
When the action is invoked with {"ref": "ana-3", "bogus": "s3cr3t-value"}
Then the error matches action.ErrDecodeInput
And the error text contains "bogus"
And the error text does not contain "s3cr3t-value"
```

#### Scenario: Negative control - a decodable input is unaffected
```gherkin
Given an action built with action.Typed over an In declaring "amount" as int
When the action is invoked with {"amount": 2}
Then no error is returned
```
