## MODIFIED Requirements

### Requirement: Typed refuses an unusable action shape at construction
`action.Typed` MUST panic at construction when the function is nil, when `Out` is not a struct or a map (optionally behind a single pointer), when `In` is not a struct, a map or an interface with no methods (same single-pointer allowance), or when `action.WithStrictInput` is combined with an `In` that is not a struct (same single-pointer allowance).

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

#### Scenario: Lenient construction refuses an In that cannot hold a JSON object
```gherkin
Scenario Outline: Lenient construction refuses an In that cannot hold a JSON object
  Given a function whose In type is <in> and whose Out type is a struct
  When action.Typed is called with that function with no options
  Then construction <outcome>

  Examples:
    | in                             | outcome  |
    | a struct                       | succeeds |
    | a pointer to struct            | succeeds |
    | map[string]any                 | succeeds |
    | a pointer to map[string]any    | succeeds |
    | any                            | succeeds |
    | string                         | panics   |
    | int                            | panics   |
    | bool                           | panics   |
    | []any                          | panics   |
    | an array of struct             | panics   |
    | chan int                       | panics   |
    | a func type                    | panics   |
    | fmt.Stringer                   | panics   |
    | a pointer to pointer to struct | panics   |
```

#### Scenario: A lenient In that cannot hold an object no longer runs on a nil input
```gherkin
Given a function whose In type is string and that records every invocation
When action.Typed is called with that function with no options
Then construction panics
And the function is never invoked, so no nil input reaches it as an empty string
```

### Requirement: Strict input rejects any top-level key that is not exactly a declared JSON name
With `action.WithStrictInput`, a typed action MUST fail with `action.ErrDecodeInput`, not retryable, naming the offending keys within the bound on named keys, when a top-level input key is not byte-for-byte one of the JSON names `In` declares, including keys that only fold onto a declared name and the lower-cased name of an untagged field, and MUST accept input whose keys are all exact declared names.

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
    | "Untagged" from an untagged field         | "untagged"                             | "untagged"         |
    | "Untagged" from an untagged field         | "UNTAGGED"                             | "UNTAGGED"         |
    | "Untagged" from an untagged field         | " Untagged"                            | " Untagged"        |
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
    | "Untagged" from an untagged field               | "Untagged"              |
```

#### Scenario: Lenient binds the lower-cased name of an untagged field
```gherkin
Given an action built with action.Typed with no options over an In with an untagged string field Untagged
When the action is invoked with {"untagged": "x"}
Then no error is returned
And the function received "x" in Untagged
```

## ADDED Requirements

### Requirement: Strict input rejects a nested key that is not exactly a declared JSON name
With `action.WithStrictInput`, a typed action MUST fail with `action.ErrDecodeInput`, not retryable, naming the offending key, when a key of any object that decodes into a struct, at any depth, is not byte-for-byte one of the JSON names that struct declares, and MUST leave untouched a position whose type decodes arbitrary keys itself.

#### Scenario: Strict refuses a non-exact key at every nested struct position
```gherkin
Scenario Outline: Strict refuses a non-exact key at every nested struct position
  Given an action built with action.Typed and action.WithStrictInput over an In whose field "order" is <position> of a struct declaring "orderId" and "sKey"
  When the action is invoked with "order" carrying <value>
  Then the error matches action.ErrDecodeInput
  And action.IsRetryable reports false for the error
  And the error text contains <named>
  And the function is not invoked

  Examples:
    | position                 | value                                                  | named      |
    | the struct itself        | {"orderId": "A", "orderid": "B"}                       | "orderid"  |
    | the struct itself        | {"ORDERID": "A"}                                       | "ORDERID"  |
    | the struct itself        | {"ſKey": "A"} (U+017F LONG S)                          | "ſKey"     |
    | the struct itself        | {"orderId": "A", "bogus": 1}                           | "bogus"    |
    | the struct itself        | {"orderId ": "A"} (trailing space)                     | "orderId " |
    | a pointer to             | {"orderid": "B"}                                       | "orderid"  |
    | a slice of               | [{"orderId": "A"}, {"orderid": "B"}]                   | "orderid"  |
    | an array of 2            | [{"orderId": "A"}, {"orderid": "B"}]                   | "orderid"  |
    | a map[string] of         | {"k": {"orderid": "B"}}                                | "orderid"  |
    | the struct itself        | a map[string]string value {"orderid": "B"}             | "orderid"  |
```

#### Scenario: Strict refuses a non-exact key two levels down
```gherkin
Given an action built with action.Typed and action.WithStrictInput over an In whose field "order" is a struct whose field "line" is a struct declaring "sku"
When the action is invoked with {"order": {"line": {"sku": "A", "SKU": "B"}}}
Then the error matches action.ErrDecodeInput
And the error text contains "SKU"
```

#### Scenario: A shadowing nested twin can no longer win
```gherkin
Given an action built with action.Typed and action.WithStrictInput over an In whose field "order" is a struct declaring "orderId"
When the action is invoked with {"order": {"orderId": "ORD-TRUSTED", "orderid": "ORD-ATTACKER"}}
Then the error matches action.ErrDecodeInput
And the function is not invoked, so it never receives "ORD-ATTACKER"
```

#### Scenario: Negative control - exact nested keys are accepted at every position
```gherkin
Scenario Outline: Negative control - exact nested keys are accepted at every position
  Given an action built with action.Typed and action.WithStrictInput over an In whose field "order" is <position> of a struct declaring "orderId" and "sKey"
  When the action is invoked with "order" carrying <value>
  Then no error is returned

  Examples:
    | position          | value                                   |
    | the struct itself | {"orderId": "A", "sKey": "s"}           |
    | the struct itself | {}                                      |
    | the struct itself | null                                    |
    | a pointer to      | {"orderId": "A"}                        |
    | a slice of        | []                                      |
    | a slice of        | [{"orderId": "A"}, {"sKey": "s"}]       |
    | a map[string] of  | {"K": {"orderId": "A"}, "k": {}}        |
```

#### Scenario: Positions that decode arbitrary keys stay open
```gherkin
Scenario Outline: Positions that decode arbitrary keys stay open
  Given an action built with action.Typed and action.WithStrictInput over an In whose field "extra" has type <type>
  When the action is invoked with {"extra": {"anything": 1, "ANYTHING": 2}}
  Then no error is returned

  Examples:
    | type                                         |
    | any                                          |
    | map[string]any                               |
    | json.RawMessage                              |
    | a struct type with its own UnmarshalJSON     |
```

#### Scenario: A self-referential In terminates at any input depth
```gherkin
Scenario Outline: A self-referential In terminates at any input depth
  Given an action built with action.Typed and action.WithStrictInput over an In declaring "name" and "children" as a slice of In
  When the action is invoked with a chain of <depth> nested "children" objects whose deepest object carries <key>
  Then <outcome>

  Examples:
    | depth | key      | outcome                                                          |
    | 1     | "name"   | no error is returned                                             |
    | 64    | "name"   | no error is returned                                             |
    | 64    | "NAME"   | the error matches action.ErrDecodeInput and its text contains "NAME" |
```

#### Scenario: Concurrent invocations of one strict action are independent
```gherkin
Given one action built with action.Typed and action.WithStrictInput over an In whose field "order" is a struct declaring "orderId"
When 64 goroutines invoke it at once, half with {"order": {"orderId": "A"}} and half with {"order": {"orderid": "B"}}
Then every exact invocation returns no error
And every variant invocation fails with action.ErrDecodeInput
And the race detector reports nothing
```

### Requirement: A strict refusal names at most ten offending keys and counts the rest
When strict input refuses more than ten keys, the error MUST name exactly the first ten offending keys in byte order and state how many more were refused, and MUST name every offending key when there are ten or fewer, so that the recorded failure text is bounded in the number of keys and identical for identical input.

#### Scenario: Named keys by offending-key count
```gherkin
Scenario Outline: Named keys by offending-key count
  Given an action built with action.Typed and action.WithStrictInput over an In declaring only "ref"
  When the action is invoked with "ref" plus <count> unknown keys "k00000" upwards
  Then the error matches action.ErrDecodeInput
  And action.IsRetryable reports false for the error
  And the error text names <named> keys
  And the error text <remainder>

  Examples:
    | count | named | remainder                    |
    | 1     | 1     | states no remaining count    |
    | 9     | 9     | states no remaining count    |
    | 10    | 10    | states no remaining count    |
    | 11    | 10    | states that 1 more was refused |
    | 20000 | 10    | states that 19990 more were refused |
```

#### Scenario: The named keys are the first in byte order
```gherkin
Given an action built with action.Typed and action.WithStrictInput over an In declaring only "ref"
When the action is invoked with "ref" plus the unknown keys "b", "a", "B", "_x", "ſ", "c", "d", "e", "f", "g", "h", "i"
Then the error text names "B", "_x", "a", "b", "c", "d", "e", "f", "g", "h" in that order
And the error text does not name "i" or "ſ"
And the error text states that 2 more were refused
```

#### Scenario: The refusal text is identical across invocations
```gherkin
Given an action built with action.Typed and action.WithStrictInput over an In declaring only "ref"
And an input carrying "ref" plus 500 unknown keys
When the action is invoked with that input 20 times
Then all 20 error texts are byte-identical
```

#### Scenario: The cap covers nested and top-level offenders together
```gherkin
Given an action built with action.Typed and action.WithStrictInput over an In declaring "ref" and a field "order" that is a struct declaring "orderId"
When the action is invoked with 8 unknown top-level keys and "order" carrying 8 unknown keys
Then the error matches action.ErrDecodeInput
And the error text names 10 keys
And the error text states that 6 more were refused
```
