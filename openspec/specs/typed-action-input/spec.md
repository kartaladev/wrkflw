# typed-action-input Specification

## Purpose
Lets a consumer write an action against its own Go input and output types with `action.Typed`, while the engine, runtime and persistence keep seeing only the map envelope and the string action name; it fixes how input is decoded, when construction is refused, and what strict input decoding rejects.

## Requirements

### Requirement: Typed actions round-trip their envelope through JSON
An action built with `action.Typed` MUST decode its input map into the consumer's `In` type by JSON round trip, invoke the consumer's function, and return that function's `Out` encoded back to a map by the same round trip, so that registering it under a name changes nothing the engine or persistence sees.

#### Scenario: A typed action reads its declared fields and returns a map
```gherkin
Given an action built with action.Typed over a function taking {ref string, count int} and returning {approved bool}
When the action is invoked with the input map {"ref": "ana-3", "count": 2}
Then it returns the map {"approved": true}
And it returns no error
```

### Requirement: An input that cannot be decoded fails with ErrDecodeInput and is not retried
When the input map cannot be decoded into `In`, a typed action MUST fail with an error matching `action.ErrDecodeInput` that `action.IsRetryable` reports as not retryable, so that an engine retry policy never re-runs an invocation whose input cannot change.

#### Scenario: Undecodable input values are refused as ErrDecodeInput
```gherkin
Scenario Outline: Undecodable input values are refused as ErrDecodeInput
  Given an action built with action.Typed over a function whose In declares "count" as an integer
  When the action is invoked with the input <input>
  Then the error matches action.ErrDecodeInput
  And action.IsRetryable reports false for the error
  And the action returns no output map

  Examples:
    | input                                            |
    | {"ref": "ana-3", "count": 2.5}                   |
    | {"ref": "ana-3", "bad": <a value JSON cannot encode>} |
```

#### Scenario: A decodable neighbour is accepted
```gherkin
Given an action built with action.Typed over a function whose In declares "count" as an integer
When the action is invoked with the input {"ref": "ana-3", "count": 2}
Then no error is returned
```

#### Scenario: A decode failure under a retry policy arms no retry, while a plain failure does
```gherkin
Given a service task whose action is built with action.Typed and a default retry policy allowing 3 attempts
When the instance is driven with the variables {"ref": "ana-3", "count": 2.5}
Then the recorded action failure is not retryable and its cause matches action.ErrDecodeInput
And no retry timer is armed
But when the same task's action instead returns a plain error under the same policy
Then the recorded action failure is retryable and a retry timer is armed
```

### Requirement: The consumer function's own error passes through unchanged
A typed action MUST return the error produced by the consumer's function unchanged, without classifying it as `action.ErrDecodeInput` or altering its retry classification, so that the function keeps control of whether its failure is retried.

#### Scenario: A function error keeps its identity and stays retryable
```gherkin
Given an action built with action.Typed over a function that returns ctx.Err()
And a context that is already cancelled
When the action is invoked with a decodable input under that context
Then the error matches context.Canceled
And the error does not match action.ErrDecodeInput
And action.IsRetryable reports true for the error
```

### Requirement: Typed refuses an unusable action shape at construction
`action.Typed` MUST panic at construction when the function is nil, when `Out` is not a struct or a map (optionally behind a single pointer), or when `action.WithStrictInput` is combined with an `In` that is not a struct (same single-pointer allowance).

#### Scenario: Construction outcome by type shape
```gherkin
Scenario Outline: Construction outcome by type shape
  Given a function whose In type is <in> and whose Out type is <out>
  When action.Typed is called with that function <mode>
  Then construction <outcome>

  Examples:
    | in                | out                 | mode                        | outcome  |
    | a struct          | a struct            | with no options             | succeeds |
    | a struct          | map[string]any      | with no options             | succeeds |
    | a struct          | a pointer to struct | with no options             | succeeds |
    | a struct          | a pointer to map    | with no options             | succeeds |
    | a struct          | string              | with no options             | panics   |
    | a struct          | int                 | with no options             | panics   |
    | a struct          | any                 | with no options             | panics   |
    | a struct          | a slice             | with no options             | panics   |
    | a struct          | a pointer to pointer to struct | with no options  | panics   |
    | a struct          | a struct            | with action.WithStrictInput | succeeds |
    | a pointer to struct | a struct          | with action.WithStrictInput | succeeds |
    | map[string]any    | a struct            | with action.WithStrictInput | panics   |
    | any               | a struct            | with action.WithStrictInput | panics   |
```

#### Scenario: A nil function is refused at construction
```gherkin
Given a nil function value
When action.Typed is called with it
Then construction panics
```

### Requirement: Default decoding ignores keys that match no declared field
Without `action.WithStrictInput`, a typed action MUST decode successfully when the input carries keys that match none of `In`'s JSON names, including the engine's `_idempotencyKey` stamp when `In` does not declare it, and MUST ignore those keys.

#### Scenario: Undeclared keys are ignored by default
```gherkin
Given an action built with action.Typed with no options over a function whose In declares only "ref" and "count"
When the action is invoked with {"ref": "ana-3", "count": 2, "unrelated": "x", "_idempotencyKey": "idem-7"}
Then no error is returned
And the output is {"approved": true}
```

### Requirement: Strict input rejects any top-level key that is not exactly a declared JSON name
With `action.WithStrictInput`, a typed action MUST fail with `action.ErrDecodeInput`, not retryable, naming the offending key, when a top-level input key is not byte-for-byte one of the JSON names `In` declares, including keys that only fold onto a declared name, and MUST accept input whose keys are all exact declared names.

#### Scenario: Strict refuses keys that are not exact declared names
```gherkin
Scenario Outline: Strict refuses keys that are not exact declared names
  Given an action built with action.Typed and action.WithStrictInput over an In declaring <declared>
  When the action is invoked with the top-level keys <keys>
  Then the error matches action.ErrDecodeInput
  And action.IsRetryable reports false for the error
  And the error text contains <named>

  Examples:
    | declared                                  | keys                                   | named              |
    | "ref", "count"                            | "ref", "count", "unrelated"            | "unrelated"        |
    | "orderId"                                 | "orderId", "orderid"                   | "orderid"          |
    | "ref", "_idempotencyKey"                  | "ref", "_idempotencyKey", "_idempotencykey" | "_idempotencykey" |
    | "nested" flattened from an embedded pointer | "nested", "Nested"                   | "Nested"           |
    | "ref"; a field excluded by json:"-" named Skipped | "ref", "Skipped"                | "Skipped"          |
    | "ref"; an unexported field named secret   | "ref", "secret"                        | "secret"           |
    | "sKey"                                    | "sKey", "ſKey" (U+017F LONG S)          | "ſKey"             |
    | "kKey"                                    | "kKey", "KKey" (U+212A KELVIN SIGN)     | "KKey"             |
```

#### Scenario: Strict accepts exact declared names
```gherkin
Scenario Outline: Strict accepts exact declared names
  Given an action built with action.Typed and action.WithStrictInput over an In declaring <declared>
  When the action is invoked with the top-level keys <keys>
  Then no error is returned

  Examples:
    | declared                                        | keys                    |
    | "ref" promoted from an embedded struct, "count" | "ref", "count"          |
    | "nested" flattened from an embedded pointer, "label" | "nested", "label"  |
    | "ref", "_idempotencyKey"                        | "ref", "_idempotencyKey" |
    | "sKey", "kKey"                                  | "sKey", "kKey"          |
```

### Requirement: Strict input on a primary service task requires In to declare the idempotency stamp
Because the engine stamps `_idempotencyKey` into a primary service task's input, a strict typed action on such a task MUST fail with `action.ErrDecodeInput` naming `_idempotencyKey` unless `In` declares that exact name, and when `In` declares it the action MUST receive the engine's stamp.

#### Scenario: Strict without the declared stamp fails the service task by name
```gherkin
Given a service task "task" whose action is built with action.Typed and action.WithStrictInput over an In declaring only "ref" and "count"
And a default retry policy allowing 3 attempts
When the instance is driven with the variables {"ref": "ana-3", "count": 2}
Then the recorded action failure is not retryable
And its cause matches action.ErrDecodeInput
And its error text contains "_idempotencyKey"
And no retry timer is armed
```

#### Scenario: Strict with the declared stamp receives it
```gherkin
Given an action built with action.Typed and action.WithStrictInput over an In declaring "ref" and "_idempotencyKey"
When the action is invoked with {"ref": "ana-3", "_idempotencyKey": "inst-42:task"}
Then no error is returned
And the function received "inst-42:task" as its idempotency key
```

### Requirement: Strict input constrains which keys may appear, not which must
With `action.WithStrictInput`, a nil or empty input map MUST decode to the zero value of `In` without error, because strict decoding refuses unexpected keys and never requires a key to be present.

#### Scenario: An absent envelope decodes to the zero In
```gherkin
Scenario Outline: An absent envelope decodes to the zero In
  Given an action built with action.Typed and action.WithStrictInput over a function whose In declares "ref" and "count"
  When the action is invoked with <input>
  Then no error is returned
  And the function received the zero In

  Examples:
    | input     |
    | a nil map |
    | {}        |
```

### Requirement: A typed action composes with Wrap as the innermost bare action
An action built with `action.Typed` MUST accept a resiliency policy layered over it with `action.Wrap`, so that `action.ResolvePolicy` reports the wrapped policy, and `action.Unwrap` MUST return an action that declares no policy of its own and still performs the typed decode.

#### Scenario: Wrap, resolve and unwrap a typed action
```gherkin
Given an action built with action.Typed over a function taking {ref string, count int} and returning {approved bool}
When it is wrapped with action.Wrap and action.WithExecTimeout(3s)
Then action.ResolvePolicy of the wrapped action reports a 3s timeout
And action.ResolvePolicy of action.Unwrap of the wrapped action reports an empty policy
And invoking the unwrapped action with {"ref": "ana-3", "count": 2} returns {"approved": true} with no error
```
