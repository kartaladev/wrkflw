## ADDED Requirements

### Requirement: Task action references accept a name or a name with a failure policy
On a userTask, both decoders SHALL accept `entry_action` and `completion_action` as a non-blank string naming the action with policy `stop`, or as an object `{name, on_failure}`, and the encoder MUST write the bare string when the policy is `stop` and the object form only for `ignore`, so a definition stored without a policy re-encodes byte for byte.

#### Scenario: Both forms decode to a name and a policy
```gherkin
Scenario Outline: <key> written as <value> in <format>
  Given a <format> definition with a userTask carrying <key> <value>
  When the definition is decoded
  Then decoding succeeds
  And the node's <key> names "<name>" with policy <policy>

  Examples:
    | format | key               | value                                         | name            | policy |
    | JSON   | completion_action | "review.finalize"                             | review.finalize | stop   |
    | JSON   | completion_action | {"name":"review.finalize"}                    | review.finalize | stop   |
    | JSON   | completion_action | {"name":"review.finalize","on_failure":"stop"} | review.finalize | stop   |
    | JSON   | completion_action | {"name":"review.finalize","on_failure":"ignore"} | review.finalize | ignore |
    | JSON   | entry_action      | "review.prepare"                              | review.prepare  | stop   |
    | JSON   | entry_action      | {"name":"review.prepare","on_failure":"ignore"} | review.prepare  | ignore |
    | YAML   | completion_action | review.finalize                               | review.finalize | stop   |
    | YAML   | completion_action | {name: review.finalize, on_failure: ignore}   | review.finalize | ignore |
    | YAML   | entry_action      | review.prepare                                | review.prepare  | stop   |
    | YAML   | entry_action      | {name: "review.prepare", on_failure: stop}    | review.prepare  | stop   |
    | JSON   | entry_action      | "révision.préparer"                           | révision.préparer | stop |
```

#### Scenario: The encoder writes the shortest form that round-trips
```gherkin
Scenario Outline: Encoding a userTask whose <key> is "<name>" with policy <policy>
  Given a userTask built with <key> "<name>" and policy <policy>
  When the definition is encoded to <format> and decoded back
  Then the encoding carries <form>
  And the decoded node equals the built node

  Examples:
    | format | key               | name            | policy | form                                              |
    | JSON   | completion_action | review.finalize | stop   | the bare string "review.finalize"                 |
    | JSON   | completion_action | review.finalize | ignore | {"name":"review.finalize","on_failure":"ignore"}  |
    | JSON   | entry_action      | review.prepare  | stop   | the bare string "review.prepare"                  |
    | YAML   | entry_action      | review.prepare  | ignore | a mapping with name and on_failure "ignore"       |
```

#### Scenario: A definition stored before this change re-encodes unchanged
```gherkin
Given the definition wire golden file as it was before this change
When each definition in it is decoded and re-encoded
Then every re-encoding is byte-identical to its input
```

#### Scenario: entry_action on any other kind is refused
```gherkin
Scenario Outline: entry_action on a <kind> in <format>
  Given a <format> definition with a "<kind>" node "n1" carrying entry_action "x.prepare"
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind naming "entry_action", "<kind>" and "n1"

  Examples:
    | format | kind        |
    | JSON   | serviceTask |
    | JSON   | receiveTask |
    | JSON   | subProcess  |
    | YAML   | serviceTask |
    | YAML   | receiveTask |
```

### Requirement: on_failure is refused outside its vocabulary
Both decoders SHALL refuse with `ErrInvalidOnFailure` an `on_failure` value other than `stop` or `ignore`, compared exactly and without trimming, and SHALL treat an absent `on_failure` as `stop`.

#### Scenario: Out-of-vocabulary policies are refused
```gherkin
Scenario Outline: on_failure <value> in <format>
  Given a <format> userTask whose completion_action is an object with name "review.finalize" and on_failure <value>
  When the definition is decoded
  Then decoding fails with ErrInvalidOnFailure naming the node

  Examples:
    | format | value              |
    | JSON   | "Ignore"           |
    | JSON   | "IGNORE"           |
    | JSON   | " ignore"          |
    | JSON   | "ignore "          |
    | JSON   | "skip"             |
    | JSON   | "continue"         |
    | JSON   | the empty string   |
    | JSON   | the number 1       |
    | JSON   | "ıgnore" (dotless) |
    | YAML   | Stop               |
    | YAML   | the boolean true   |
```

#### Scenario: The two words of the vocabulary are accepted
```gherkin
Scenario Outline: on_failure <value> on <key>
  Given a JSON userTask whose <key> is an object with name "a.b" and on_failure <value>
  When the definition is decoded
  Then decoding succeeds

  Examples:
    | key               | value    |
    | entry_action      | "stop"   |
    | entry_action      | "ignore" |
    | completion_action | "stop"   |
    | completion_action | "ignore" |
```

### Requirement: A malformed task action reference is refused
Both decoders SHALL refuse with `ErrInvalidActionRef` an `entry_action` or `completion_action` value that is a number, a boolean, a list, a blank string, or an object whose `name` is absent, blank or not a string, or that carries a key other than `name` and `on_failure`; an explicit null or an empty string MUST be treated as absent.

#### Scenario: Malformed references are refused
```gherkin
Scenario Outline: <key> written as <value> in <format>
  Given a <format> userTask carrying <key> <value>
  When the definition is decoded
  Then decoding fails with ErrInvalidActionRef naming the node and "<key>"

  Examples:
    | format | key               | value                                      |
    | JSON   | completion_action | the number 42                              |
    | JSON   | completion_action | the boolean true                           |
    | JSON   | completion_action | ["a.b"]                                    |
    | JSON   | completion_action | a blank string "   "                       |
    | JSON   | completion_action | {}                                         |
    | JSON   | completion_action | {"on_failure":"ignore"}                    |
    | JSON   | completion_action | {"name":"","on_failure":"ignore"}          |
    | JSON   | completion_action | {"name":7}                                 |
    | JSON   | entry_action      | {"name":"a.b","retry":3}                   |
    | JSON   | entry_action      | {"Name":"a.b"}                             |
    | YAML   | entry_action      | the integer 42                             |
    | YAML   | entry_action      | {on_failure: ignore}                       |
```

#### Scenario: Absent references stay absent
```gherkin
Scenario Outline: <key> written as <value> in <format>
  Given a <format> userTask carrying <key> <value>
  When the definition is decoded
  Then decoding succeeds
  And the node declares no <key>

  Examples:
    | format | key               | value            |
    | JSON   | entry_action      | null             |
    | JSON   | entry_action      | the empty string |
    | JSON   | completion_action | null             |
    | YAML   | entry_action      | null             |
```
