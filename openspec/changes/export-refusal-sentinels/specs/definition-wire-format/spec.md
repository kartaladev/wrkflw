## MODIFIED Requirements

### Requirement: Node kinds are encoded by stable name
A node's kind SHALL be encoded as its stable lowerCamelCase name string, never as a number, and decoding SHALL refuse with `ErrUnknownNodeKind`, in an error naming the value, any kind name that is not a registered kind, including the retired names `terminateEndEvent` and `errorEndEvent`.

#### Scenario: Every kind encodes as its name and round-trips
```gherkin
Scenario Outline: A kind encodes as a JSON string and decodes back to itself
  Given the node kind <kind>
  When it is encoded to JSON
  Then the encoding is the JSON string "<name>"
  And decoding that string yields <kind>

  Examples:
    | kind                       | name                   |
    | KindStartEvent             | startEvent             |
    | KindEndEvent               | endEvent               |
    | KindServiceTask            | serviceTask            |
    | KindUserTask               | userTask               |
    | KindBusinessRuleTask       | businessRuleTask       |
    | KindSubProcess             | subProcess             |
    | KindIntermediateCatchEvent | intermediateCatchEvent |
    | KindEventBasedGateway      | eventBasedGateway      |
```

#### Scenario: An unknown or retired kind name is refused
```gherkin
Scenario Outline: Decoding a kind name outside the registered set fails
  Given the JSON kind value <value>
  When it is decoded as a node kind
  Then decoding fails with ErrUnknownNodeKind
  And the error names the decoded string
  And the error is not ErrKindNotRegistered

  Examples:
    | value               | why                                 |
    | "notANodeKind"      | never a kind                        |
    | "terminateEndEvent" | retired                             |
    | "errorEndEvent"     | retired                             |
    | "StartEvent"        | case variant of a registered name   |
    | "startEvent "       | trailing whitespace                 |
    | ""                  | empty                               |
```

#### Scenario: A kind inside a definition is written as a name
```gherkin
Given a definition with one start event named "Order Received"
When the definition is encoded to JSON
Then the encoding contains the string "startEvent"
And decoding it yields a node of kind KindStartEvent whose name is "Order Received"
```

#### Scenario: YAML refuses an unknown kind name
```gherkin
Scenario Outline: A YAML node kind outside the registered set fails
  Given a YAML definition with a node whose kind is "<name>"
  When it is parsed with ParseYAML
  Then parsing fails with ErrUnknownNodeKind
  And the error names "<name>"

  Examples:
    | name              |
    | notANodeKind      |
    | terminateEndEvent |
    | errorEndEvent     |
```

#### Scenario: Negative control - a registered kind name is not refused
```gherkin
Scenario Outline: A registered kind name decodes in either format
  Given a valid <format> definition whose start node's kind is "startEvent"
  When the definition is decoded
  Then decoding succeeds
  And no error matches ErrUnknownNodeKind

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

### Requirement: Unknown keys are refused at decode
Decoding a definition SHALL refuse with `ErrUnknownKey`, in an error naming the key, any key that matches no key the wire format defines: in YAML compared exactly, in JSON matching no defined key even case-insensitively. A JSON key differing from a defined key only in letter case is outside this requirement.

#### Scenario: An unknown key is refused in either format
```gherkin
Scenario Outline: An undefined key fails the decode and is named
  Given a valid definition in <format>
  And an extra key "<key>" added <where>
  When the definition is decoded
  Then decoding fails with ErrUnknownKey
  And the error names "<key>"

  Examples:
    | format | where                              | key             |
    | JSON   | at the top level                   | bogus_top       |
    | JSON   | on a node                          | bogus_node_key  |
    | JSON   | on a node, as a misspelling        | eligable_roles  |
    | JSON   | on a flow                          | bogus_flow_key  |
    | JSON   | on a node inside a subprocess      | bogus_sub_key   |
    | YAML   | at the top level                   | bogus_top_level |
    | YAML   | on a node, as a misspelling        | eligable_roles  |
    | YAML   | on a flow                          | bogus_flow_key  |
    | YAML   | on a node inside a subprocess      | bogus_sub_key   |
```

#### Scenario: The sentinel survives the bound on reported YAML field errors
```gherkin
Scenario Outline: Many unknown YAML keys still match the sentinel
  Given a valid YAML definition with <count> distinct unknown top-level keys added
  When the definition is decoded
  Then decoding fails with ErrUnknownKey

  Examples:
    | count |
    | 1     |
    | 20    |
    | 21    |
```

#### Scenario: The unmodified definition still decodes
```gherkin
Scenario Outline: A definition with only defined keys decodes
  Given a valid definition in <format> carrying only defined keys
  When the definition is decoded
  Then decoding succeeds

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: Negative control - a defined key the kind never reads is not an unknown key
```gherkin
Scenario Outline: A misplaced defined key is ErrKeyNotOnKind, not ErrUnknownKey
  Given a valid <format> definition with an "exclusiveGateway" node carrying "action" with a value that is not a scalar zero
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind
  And the error is not ErrUnknownKey

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

### Requirement: The retired label key is refused with a pointer to name
Both decoders SHALL refuse a node carrying the retired `label` key with `ErrRetiredLabelKey`, whatever its value and at any nesting depth, and `name` SHALL be the node's display string; the encoder SHALL never emit `label`.

#### Scenario: Any label key is refused
```gherkin
Scenario Outline: A node carrying label fails with ErrRetiredLabelKey
  Given a <format> definition with a userTask node carrying <keys>
  When the definition is decoded
  Then decoding fails with ErrRetiredLabelKey
  And the error names both "label" and "name"
  And the error is not ErrUnknownKey

  Examples:
    | format | keys                                         |
    | JSON   | name "sem" and label "Human"                 |
    | JSON   | label "Human" and no name                    |
    | JSON   | name "sem" and an empty label                |
    | JSON   | name "sem" and a null label                  |
    | JSON   | label "Human" on a node inside a subprocess  |
    | YAML   | name "semantic-name" and label "Human"       |
    | YAML   | name "semantic-name" and an empty label      |
    | YAML   | name "semantic-name" and a null label        |
```

#### Scenario: Name alone is accepted as the display string
```gherkin
Scenario Outline: A node with name and no label decodes
  Given a <format> definition with a userTask node whose name is "<name>" and no label
  When the definition is decoded
  Then decoding succeeds
  And the node's Name is "<name>"

  Examples:
    | format | name                |
    | JSON   | Approve the request |
    | YAML   | semantic-name       |
```

#### Scenario: An unrelated unknown key is not reported as label
```gherkin
Scenario Outline: A non-label unknown key names itself
  Given a <format> definition with a userTask node carrying the key "caption"
  When the definition is decoded
  Then decoding fails with ErrUnknownKey
  And the error names "caption"
  And the error is not ErrRetiredLabelKey

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: Encoding never writes label
```gherkin
Given a decoded definition with a userTask node named "Approve"
When it is encoded to JSON
Then the encoding contains "name":"Approve"
And the encoding does not contain the key "label"
```
