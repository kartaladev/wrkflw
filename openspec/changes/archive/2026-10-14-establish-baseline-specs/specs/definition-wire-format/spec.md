## Purpose

Defines how a process definition is encoded to and decoded from JSON and YAML: how node kinds are named, which keys each node kind accepts, how retired keys and the termination_outcome vocabulary are refused, and what the library promises about its own encoded output.

## ADDED Requirements

### Requirement: Node kinds are encoded by stable name
A node's kind SHALL be encoded as its stable lowerCamelCase name string, never as a number, and decoding SHALL refuse with an error naming the value any kind name that is not a registered kind, including the retired names `terminateEndEvent` and `errorEndEvent`.

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
  Given a JSON kind value "<name>"
  When it is decoded as a node kind
  Then decoding fails with an error
  And the error names "<name>"

  Examples:
    | name              |
    | notANodeKind      |
    | terminateEndEvent |
    | errorEndEvent     |
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
Given a YAML definition with a node whose kind is "notANodeKind"
When it is parsed with ParseYAML
Then parsing fails with an error naming "notANodeKind"
```

### Requirement: Unknown keys are refused at decode
Decoding a definition SHALL refuse, with an error naming the key, any key that matches no key the wire format defines: in YAML compared exactly, in JSON matching no defined key even case-insensitively. A JSON key differing from a defined key only in letter case is outside this requirement.

#### Scenario: An unknown key is refused in either format
```gherkin
Scenario Outline: An undefined key fails the decode and is named
  Given a valid definition in <format>
  And an extra key "<key>" added <where>
  When the definition is decoded
  Then decoding fails
  And the error names "<key>"

  Examples:
    | format | where                       | key             |
    | JSON   | at the top level            | bogus_top       |
    | JSON   | on a node                   | bogus_node_key  |
    | JSON   | on a node, as a misspelling | eligable_roles  |
    | YAML   | at the top level            | bogus_top_level |
    | YAML   | on a node, as a misspelling | eligable_roles  |
    | YAML   | on a flow                   | bogus_flow_key  |
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

### Requirement: The retired label key is refused with a pointer to name
Both decoders SHALL refuse a node carrying the retired `label` key with `ErrRetiredLabelKey`, whatever its value and at any nesting depth, and `name` SHALL be the node's display string; the encoder SHALL never emit `label`.

#### Scenario: Any label key is refused
```gherkin
Scenario Outline: A node carrying label fails with ErrRetiredLabelKey
  Given a <format> definition with a userTask node carrying <keys>
  When the definition is decoded
  Then decoding fails with ErrRetiredLabelKey
  And the error names both "label" and "name"

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
  Then decoding fails with an error naming "caption"
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

### Requirement: A key the node kind never reads is refused
Both decoders SHALL refuse with `ErrKeyNotOnKind`, at any nesting depth, a node carrying a known key that its kind never reads under any combination, unless the value is a scalar zero (empty string, false or null), which is accepted as absent; an empty list or object under such a key SHALL be refused.

#### Scenario: A misplaced key is refused in both formats
```gherkin
Scenario Outline: A key the kind cannot carry fails with ErrKeyNotOnKind
  Given a <format> definition with a "<kind>" node "n1" carrying "<key>" with a value that is not a scalar zero
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind
  And the error names "<key>", "<kind>" and "n1"

  Examples:
    | format | kind             | key                    |
    | JSON   | exclusiveGateway | action                 |
    | JSON   | serviceTask      | rule                   |
    | JSON   | exclusiveGateway | rule                   |
    | JSON   | parallelGateway  | outcomes               |
    | JSON   | serviceTask      | signal_name            |
    | JSON   | userTask         | error_code             |
    | JSON   | userTask         | timer_duration         |
    | JSON   | serviceTask      | compensate_ref         |
    | JSON   | serviceTask      | compensate_scope_local |
    | JSON   | serviceTask      | validation             |
    | YAML   | exclusiveGateway | action                 |
    | YAML   | serviceTask      | rule                   |
    | YAML   | parallelGateway  | outcomes               |
    | YAML   | userTask         | error_code             |
    | YAML   | serviceTask      | validation             |
```

#### Scenario: A key the kind reads is accepted
```gherkin
Scenario Outline: A legal key on its kind decodes
  Given a <format> definition with a "<kind>" node carrying <keys>
  When the definition is decoded
  Then decoding succeeds

  Examples:
    | format | kind                   | keys                                                              |
    | JSON   | serviceTask            | action                                                            |
    | JSON   | businessRuleTask       | rule                                                              |
    | JSON   | userTask               | outcomes                                                          |
    | JSON   | userTask               | validation                                                        |
    | JSON   | intermediateCatchEvent | signal_name                                                       |
    | JSON   | intermediateCatchEvent | timer_trigger                                                     |
    | JSON   | compensationThrowEvent | compensate_ref and compensate_scope_local                         |
    | JSON   | endEvent               | end_behavior "terminate", termination_reason, termination_outcome |
    | JSON   | endEvent               | end_behavior "error" and error_code                               |
    | JSON   | boundaryEvent          | attached_to and error_code                                        |
    | YAML   | serviceTask            | action                                                            |
    | YAML   | userTask               | outcomes                                                          |
    | YAML   | endEvent               | end_behavior "terminate", termination_reason, termination_outcome |
```

#### Scenario: A zero value under a misplaced key is asymmetric
```gherkin
Scenario Outline: A scalar zero is accepted as absent, an empty composite is refused
  Given an exclusiveGateway node "g" carrying <key> with <value>
  When the node passes the decode gate
  Then <outcome>

  Examples:
    | key          | value                       | outcome                          |
    | action       | the empty string            | the node is accepted             |
    | manual       | false                       | the node is accepted             |
    | rule         | null                        | the node is accepted             |
    | action       | "charge"                    | the node fails with ErrKeyNotOnKind |
    | outcomes     | an empty list               | the node fails with ErrKeyNotOnKind |
    | retry_policy | an empty object             | the node fails with ErrKeyNotOnKind |
```

#### Scenario: The legacy flat timer form is accepted and normalised
```gherkin
Given a JSON definition with an intermediateCatchEvent carrying timer_duration "PT1H"
When the definition is decoded and re-encoded
Then decoding succeeds
And the encoding carries "timer_trigger" and not "timer_duration"
And the re-encoded definition decodes again without error
```

#### Scenario: The refusal reaches a nested subprocess
```gherkin
Scenario Outline: A key on a node inside a subprocess is gated like a top-level one
  Given a <format> definition whose subProcess contains an endEvent "i_end" carrying <extra>
  When the definition is decoded
  Then <outcome>

  Examples:
    | format | extra                    | outcome                                                         |
    | JSON   | end_behavior "terminate" | decoding succeeds                                               |
    | JSON   | action "charge"          | decoding fails with ErrKeyNotOnKind naming "i_end" and "action" |
    | YAML   | action "charge"          | decoding fails with ErrKeyNotOnKind naming "i_end"              |
```

### Requirement: termination_outcome on a terminate end is refused outside its vocabulary
On an endEvent whose `end_behavior` is `terminate`, both decoders SHALL refuse with `ErrInvalidTerminationOutcome` a `termination_outcome` other than `complete`, `abort` or empty, compared exactly, and SHALL decode `complete` and `abort` to the outcome they name.

#### Scenario: Out-of-vocabulary values are refused
```gherkin
Scenario Outline: A near-miss outcome fails instead of completing
  Given a <format> definition whose endEvent has end_behavior "terminate" and termination_outcome <value>
  When the definition is decoded
  Then decoding fails with ErrInvalidTerminationOutcome

  Examples:
    | format | value      |
    | JSON   | "Abort"    |
    | JSON   | "COMPLETE" |
    | JSON   | "abrot"    |
    | JSON   | "abort "   |
    | YAML   | "Abort"    |
    | YAML   | "COMPLETE" |
    | YAML   | "abrot"    |
    | YAML   | "abort "   |
```

#### Scenario: The refusal locates the node and quotes the value
```gherkin
Scenario Outline: The diagnostic names the node, the value and the precondition
  Given a <format> definition whose endEvent "e" has end_behavior "terminate" and termination_outcome "abbort"
  When the definition is decoded
  Then decoding fails with ErrInvalidTerminationOutcome
  And the error names the node "e" and quotes "abbort" as authored
  And the error names the allowed values "abort" and "complete"
  And the error names end_behavior "terminate"

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: The vocabulary values are accepted as themselves
```gherkin
Scenario Outline: A legal outcome decodes to its own meaning
  Given a <format> definition whose endEvent has end_behavior "terminate" and termination_outcome "<value>"
  When the definition is decoded
  Then decoding succeeds
  And the end event's behavior is terminate
  And its outcome is <outcome>

  Examples:
    | format | value    | outcome         |
    | JSON   | abort    | OutcomeAbort    |
    | JSON   | complete | OutcomeComplete |
    | JSON   |          | OutcomeComplete |
    | YAML   | abort    | OutcomeAbort    |
    | YAML   | complete | OutcomeComplete |
    | YAML   |          | OutcomeComplete |
```

### Requirement: The reserved rule key refuses scalars that name nothing
On a businessRuleTask, both decoders SHALL refuse with `ErrInvalidRule` a `rule` value that is a number, a boolean, or an empty or blank string, SHALL accept a non-blank string as a catalog name, and SHALL treat an explicit null as absent.

#### Scenario: Malformed scalar rule values are refused
```gherkin
Scenario Outline: A rule scalar that names nothing fails with ErrInvalidRule
  Given a businessRuleTask whose rule is <value> in <format>
  When the rule is decoded
  Then decoding fails with ErrInvalidRule

  Examples:
    | format | value            |
    | JSON   | the number 42    |
    | JSON   | the boolean true |
    | JSON   | the empty string |
    | JSON   | a blank string   |
    | YAML   | the integer 42   |
    | YAML   | the boolean true |
    | YAML   | a blank string   |
```

#### Scenario: A non-blank name is accepted
```gherkin
Scenario Outline: A string rule decodes as a catalog name
  Given a businessRuleTask whose rule is <value> in <format>
  When the rule is decoded
  Then decoding succeeds
  And the rule's catalog name is <name>

  Examples:
    | format | value              | name         |
    | JSON   | "pricing.v3"       | "pricing.v3" |
    | YAML   | pricing.v3         | "pricing.v3" |
    | YAML   | the quoted "true"  | "true"       |
```

#### Scenario: An explicit null rule means no rule
```gherkin
Scenario Outline: A null rule decodes as absent
  Given a <format> definition with a businessRuleTask whose rule is null
  When the definition is decoded
  Then decoding succeeds
  And the node carries no rule

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

### Requirement: A kind with no registered node type fails loudly
Decoding a node whose kind has no registered node type, typically because the kind packages were not imported, SHALL fail with `ErrKindNotRegistered` rather than produce a zero-valued node.

#### Scenario: An unregistered kind is refused
```gherkin
Given a node wire form whose kind has no registered node type
When it is decoded into a node
Then decoding fails with ErrKindNotRegistered
```

#### Scenario: Every shipped kind is registered once the kinds bundle is imported
```gherkin
Given the definition/kinds package is imported
When each node kind from KindStartEvent through KindCompensationThrowEvent is encoded
Then each encodes to a registered name
```

### Requirement: Encoding refuses a definition that could not round-trip
`ProcessDefinition.MarshalJSON` SHALL return `ErrForeignNodeType` for a node whose concrete type is not its kind's registered type, at any subprocess depth, and `ErrUnserializableValidation` for a node carrying a non-declarative validation strategy, instead of writing output.

#### Scenario: A foreign node is refused at any depth
```gherkin
Scenario Outline: Marshalling a counterfeit node returns ErrForeignNodeType and no bytes
  Given a definition with a counterfeit userTask node <where>
  When the definition is encoded to JSON
  Then encoding fails with ErrForeignNodeType
  And no output is returned

  Examples:
    | where                          |
    | at the top level               |
    | one subprocess level down      |
    | two subprocess levels down     |
```

#### Scenario: The genuine node type still encodes at depth
```gherkin
Given a definition whose genuine leaf node sits two subprocess levels down
When the definition is encoded to JSON
Then encoding succeeds and returns output
```

#### Scenario: A callback validation strategy is refused
```gherkin
Given a definition whose userTask carries a callback completion-validation strategy
When the definition is encoded to JSON
Then encoding fails with ErrUnserializableValidation
```

#### Scenario: A declarative validation strategy encodes
```gherkin
Given a definition whose start event carries an expr input-validation strategy "amount > 0"
When the definition is encoded to JSON
Then encoding succeeds
And the output contains "kind":"expr"
```

### Requirement: Library-encoded definitions decode back unchanged
A definition the library encodes SHALL decode again through the strict JSON decoder without refusal by any decode gate, and re-encoding the decoded definition SHALL reproduce the original bytes.

#### Scenario: The golden definition round-trips byte for byte
```gherkin
Given the golden definition file covering every option-bearing node kind
When it is decoded and re-encoded with the same indentation
Then the output is byte-identical to the file
```

#### Scenario: No library output is refused by the key gate
```gherkin
Scenario Outline: Encoded nodes of every registered kind decode again
  Given a node of every registered kind built <how>
  When it is encoded and the encoding is decoded
  Then no node is refused with ErrKeyNotOnKind

  Examples:
    | how                                                    |
    | from the widest wire form its kind reads               |
    | in Go with every settable exported field non-zero      |
```

#### Scenario: A definition loaded from YAML survives a store round trip
```gherkin
Given a definition parsed from the all-fields YAML fixture that registers a definition-scoped action
When it is encoded to JSON and decoded back with the strict decoder
Then decoding succeeds
And every node keeps its ID and kind, and the flows are unchanged
```
