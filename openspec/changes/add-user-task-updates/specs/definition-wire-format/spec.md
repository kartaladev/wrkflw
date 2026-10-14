## ADDED Requirements

### Requirement: A user task's updates round-trip in JSON and YAML
A `userTask` node's `updates` list SHALL decode from and encode to JSON and YAML with the keys `name`, `required`, `depends_on`, `validation`, `action` and `resubmit`, preserving declaration order; a node with no updates SHALL encode with no `updates` key.

#### Scenario: Round trip per format
```gherkin
Scenario Outline: Declaration order and every field survive
  Given a <format> definition whose user task declares, in order, update "assessment" with a json-schema validation and action "review.assess", and update "attachments" with required false, depends_on ["assessment"] and resubmit "refuse"
  When it is decoded and encoded again
  Then the encoded node lists "assessment" before "attachments"
  And every field of both updates is unchanged
  And decoding the encoded document yields an equal definition

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: Defaults are explicit after decoding
```gherkin
Scenario: required defaults to true and resubmit to replace
  Given a JSON definition whose user task declares update "a" with only a name
  When it is decoded
  Then update "a" is required with resubmit policy replace and no dependencies, validation or action
```

#### Scenario: Existing definitions encode unchanged
```gherkin
Scenario: No updates, no key
  Given the library's golden definitions, none of which declares updates
  When each is decoded and encoded with this build
  Then each encoding is byte-identical to the golden file
  And no encoding contains the key "updates"
```

### Requirement: Update declarations are refused outside user tasks and on unknown keys
Both decoders MUST refuse `updates` on any node kind other than `userTask` with `ErrKeyNotOnKind`, and MUST refuse an unknown key inside an update entry as they refuse an unknown node key.

#### Scenario: Misplaced and malformed update keys
```gherkin
Scenario Outline: Decoding refuses what no reader consumes
  Given a <format> definition with <shape>
  When the definition is decoded
  Then decoding fails with <error>

  Examples:
    | format | shape                                                       | error                                      |
    | JSON   | a serviceTask carrying a non-empty "updates"                | ErrKeyNotOnKind naming "updates"           |
    | YAML   | a receiveTask carrying a non-empty "updates"                | ErrKeyNotOnKind naming "updates"           |
    | JSON   | a userTask update entry carrying key "invalidates"          | the unknown-key error naming "invalidates" |
    | YAML   | a userTask update entry carrying key "Name"                 | the unknown-key error naming "Name"        |
```

#### Scenario: Negative control - updates on a user task decode
```gherkin
Scenario Outline: The legal placement is accepted
  Given a <format> definition whose userTask declares one update with every known key
  When the definition is decoded
  Then decoding succeeds

  Examples:
    | format |
    | JSON   |
    | YAML   |
```
