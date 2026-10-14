## ADDED Requirements

### Requirement: end_behavior is refused outside its vocabulary
Both decoders SHALL refuse with `ErrInvalidEndBehavior`, at any nesting depth, an endEvent whose `end_behavior` is not `terminate`, `error` or empty, compared exactly, and SHALL decode `terminate`, `error` and an empty or absent value to the behavior each names.

#### Scenario: An out-of-vocabulary end_behavior is refused
```gherkin
Scenario Outline: A near-miss end_behavior fails instead of becoming a normal end
  Given a <format> definition whose endEvent "e" has end_behavior <value>
  When the definition is decoded
  Then decoding fails with ErrInvalidEndBehavior

  Examples:
    | format | value                         |
    | JSON   | "terminat"                    |
    | JSON   | "Terminate"                   |
    | JSON   | "ERROR"                       |
    | JSON   | "error "                      |
    | JSON   | " terminate"                  |
    | JSON   | "normal"                      |
    | JSON   | "terminаte" (Cyrillic а)      |
    | YAML   | "terminat"                    |
    | YAML   | "Terminate"                   |
    | YAML   | "error "                      |
    | YAML   | "normal"                      |
```

#### Scenario: The refusal locates the node and quotes the value
```gherkin
Scenario Outline: The diagnostic names the node, the value and the vocabulary
  Given a <format> definition whose endEvent "e" has end_behavior "terminat"
  When the definition is decoded
  Then decoding fails with ErrInvalidEndBehavior
  And the error names the node "e" and quotes "terminat" as authored
  And the error names the allowed values "terminate" and "error"

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: The vocabulary values decode to their own meaning
```gherkin
Scenario Outline: A legal end_behavior decodes and round-trips
  Given a <format> definition whose endEvent has end_behavior <value>
  When the definition is decoded and re-encoded to JSON
  Then decoding succeeds
  And the end event's behavior is <behavior>
  And the re-encoded node carries end_behavior <written>

  Examples:
    | format | value       | behavior     | written     |
    | JSON   | "terminate" | EndTerminate | "terminate" |
    | JSON   | "error"     | EndError     | "error"     |
    | JSON   | ""          | EndNormal    | no key      |
    | JSON   | absent      | EndNormal    | no key      |
    | YAML   | "terminate" | EndTerminate | "terminate" |
    | YAML   | "error"     | EndError     | "error"     |
    | YAML   | absent      | EndNormal    | no key      |
```

#### Scenario: The refusal reaches a nested subprocess
```gherkin
Scenario Outline: An endEvent inside a subprocess is checked like a top-level one
  Given a <format> definition whose subProcess, <depth>, contains an endEvent "i_end" with end_behavior <value>
  When the definition is decoded
  Then <outcome>

  Examples:
    | format | depth            | value       | outcome                                                  |
    | JSON   | one level down   | "terminat"  | decoding fails with ErrInvalidEndBehavior naming "i_end" |
    | JSON   | two levels down  | "terminat"  | decoding fails with ErrInvalidEndBehavior naming "i_end" |
    | YAML   | one level down   | "terminat"  | decoding fails with ErrInvalidEndBehavior naming "i_end" |
    | JSON   | two levels down  | "terminate" | decoding succeeds                                        |
```

#### Scenario: A stored row written out of band fails to load by name
```gherkin
Scenario Outline: A store load of an out-of-vocabulary end_behavior is refused, not normalised
  Given a <dialect> definition store holding a row written outside the library whose endEvent has end_behavior "terminat"
  When the definition is loaded with GetDefinition or Lookup
  Then the load fails with an error wrapping ErrInvalidEndBehavior
  And a row the library published with end_behavior "terminate" still loads

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: A trigger object's kind is refused outside its vocabulary
The JSON decoder SHALL refuse with `ErrInvalidTriggerKind`, at any nesting depth, a present `timer_trigger`, `deadline_trigger` or `wait_trigger` object on a node kind that reads it, whose `kind` is empty, missing, or not one of `onetime`, `duration`, `expr`, `everyExpr`, `cron`, `durationRand`, `daily`, `weekly` or `monthly`, compared exactly; a null trigger SHALL decode as absent.

#### Scenario: An out-of-vocabulary trigger kind is refused at every trigger site
```gherkin
Scenario Outline: A trigger whose kind is unrecognised fails instead of vanishing
  Given a JSON definition with a <node> node "n" carrying <key> whose kind is <value>
  And the node carries <companion>
  When the definition is decoded
  Then decoding fails with ErrInvalidTriggerKind
  And the error names the node "n" and the key "<key>" and quotes the kind as authored

  Examples:
    | node                   | key              | value         | companion                 |
    | userTask               | wait_trigger     | "duratoin"    | wait_action "rem"         |
    | userTask               | wait_trigger     | "Duration"    | wait_action "rem"         |
    | userTask               | wait_trigger     | "duration "   | wait_action "rem"         |
    | userTask               | wait_trigger     | ""            | wait_action "rem"         |
    | userTask               | wait_trigger     | missing       | wait_action "rem"         |
    | userTask               | wait_trigger     | "duratiön"    | wait_action "rem"         |
    | userTask               | deadline_trigger | "onetme"      | deadline_flow to a flow   |
    | userTask               | deadline_trigger | "duratoin"    | deadline_action "esc"     |
    | intermediateCatchEvent | timer_trigger    | "duratoin"    | signal_name "go"          |
    | intermediateCatchEvent | timer_trigger    | "duratoin"    | nothing else              |
    | startEvent             | timer_trigger    | "CRON"        | nothing else              |
    | boundaryEvent          | timer_trigger    | "duratoin"    | attached_to a userTask    |
```

#### Scenario: Every trigger kind name is accepted and round-trips
```gherkin
Scenario Outline: A well-formed trigger of each kind decodes and re-encodes unchanged
  Given a JSON definition with a userTask whose wait_trigger is a well-formed trigger of kind "<kind>"
  When the definition is decoded and re-encoded
  Then decoding succeeds
  And the re-encoded wait_trigger has kind "<kind>"

  Examples:
    | kind         |
    | onetime      |
    | duration     |
    | expr         |
    | everyExpr    |
    | cron         |
    | durationRand |
    | daily        |
    | weekly       |
    | monthly      |
```

#### Scenario: A null or absent trigger is not refused
```gherkin
Scenario Outline: An unauthored trigger decodes as no trigger
  Given a JSON definition with a userTask whose wait_trigger is <value> and no wait_action
  When the definition is decoded
  Then decoding succeeds
  And the node has no wait trigger

  Examples:
    | value  |
    | null   |
    | absent |
```

#### Scenario: The kind gate still answers first for a key the kind never reads
```gherkin
Given a JSON definition with an exclusiveGateway "g" carrying wait_trigger whose kind is "duratoin"
When the definition is decoded
Then decoding fails with ErrKeyNotOnKind
And the error is not ErrInvalidTriggerKind
```

#### Scenario: YAML has no trigger objects
```gherkin
Given a YAML definition with a userTask carrying a wait_trigger mapping
When it is parsed with ParseYAML
Then parsing fails with an error naming "wait_trigger"
And the error is not ErrInvalidTriggerKind
```

#### Scenario: The refusal reaches a nested subprocess
```gherkin
Scenario Outline: A trigger inside a subprocess is checked like a top-level one
  Given a JSON definition whose subProcess, <depth>, contains a userTask "i_t" with wait_trigger kind <value>
  When the definition is decoded
  Then <outcome>

  Examples:
    | depth           | value      | outcome                                                  |
    | one level down  | "duratoin" | decoding fails with ErrInvalidTriggerKind naming "i_t"   |
    | two levels down | "duratoin" | decoding fails with ErrInvalidTriggerKind naming "i_t"   |
    | two levels down | "duration" | decoding succeeds                                        |
```

#### Scenario: A stored row written out of band fails to load by name
```gherkin
Scenario Outline: A store load of an unrecognised trigger kind is refused
  Given a <dialect> definition store holding a row written outside the library whose userTask has wait_trigger kind "duratoin"
  When the definition is loaded with GetDefinition or Lookup
  Then the load fails with an error wrapping ErrInvalidTriggerKind

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: An endEvent key is refused unless its end_behavior reads it
Both decoders SHALL refuse with `ErrKeyNotOnEndBehavior`, at any nesting depth, an endEvent carrying `termination_reason` or `termination_outcome` unless its `end_behavior` is `terminate`, or `error_code` unless it is `error`; an empty string under such a key SHALL be accepted as absent.

#### Scenario: A key its end_behavior does not read is refused
```gherkin
Scenario Outline: A conditional key outside its combination fails instead of being dropped
  Given a <format> definition whose endEvent "e" has end_behavior <behavior> and carries <key> <value>
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnEndBehavior
  And the error names the node "e", the key "<key>" and the end_behavior that reads it

  Examples:
    | format | behavior    | key                 | value      |
    | JSON   | absent      | error_code          | "PAY"      |
    | JSON   | absent      | termination_reason  | "why"      |
    | JSON   | absent      | termination_outcome | "abort"    |
    | JSON   | "terminate" | error_code          | "PAY"      |
    | JSON   | "error"     | termination_reason  | "why"      |
    | JSON   | "error"     | termination_outcome | "complete" |
    | YAML   | absent      | error_code          | "PAY"      |
    | YAML   | absent      | termination_outcome | "abort"    |
    | YAML   | "terminate" | error_code          | "PAY"      |
    | YAML   | "error"     | termination_reason  | "why"      |
```

#### Scenario: The gates answer in a fixed order
```gherkin
Scenario Outline: The diagnostic names the first problem a reader would fix
  Given a JSON definition whose <node> node carries <keys>
  When the definition is decoded
  Then decoding fails with <error>

  Examples:
    | node      | keys                                                | error                  |
    | userTask  | error_code "PAY"                                    | ErrKeyNotOnKind        |
    | endEvent  | end_behavior "terminat" and termination_reason "r"  | ErrInvalidEndBehavior  |
    | endEvent  | termination_outcome "Abort" and no end_behavior     | ErrKeyNotOnEndBehavior |
    | endEvent  | end_behavior "terminate" and termination_outcome "Abort" | ErrInvalidTerminationOutcome |
```

#### Scenario: Legal combinations and empty values are accepted
```gherkin
Scenario Outline: A key under the end_behavior that reads it decodes and round-trips
  Given a <format> definition whose endEvent has end_behavior <behavior> and carries <keys>
  When the definition is decoded and re-encoded to JSON
  Then decoding succeeds
  And the re-encoded node carries <written>

  Examples:
    | format | behavior    | keys                                                  | written                                     |
    | JSON   | "terminate" | termination_reason "r" and termination_outcome "abort" | termination_reason "r", termination_outcome "abort" |
    | JSON   | "error"     | error_code "PAY"                                      | error_code "PAY"                            |
    | JSON   | absent      | error_code ""                                         | no error_code                               |
    | JSON   | "error"     | termination_reason ""                                 | no termination_reason                       |
    | YAML   | "terminate" | termination_reason "r"                                | termination_reason "r"                      |
    | YAML   | "error"     | error_code "PAY"                                      | error_code "PAY"                            |
```

#### Scenario: error_code on a boundary event is not an endEvent key
```gherkin
Given a JSON definition with a boundaryEvent attached to a serviceTask and carrying error_code "PAY"
When the definition is decoded
Then decoding succeeds
And the boundary event's error code is "PAY"
```

#### Scenario: The refusal reaches a nested subprocess
```gherkin
Scenario Outline: An endEvent inside a subprocess is checked like a top-level one
  Given a <format> definition whose subProcess contains an endEvent "i_end" with no end_behavior and error_code "PAY"
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnEndBehavior naming "i_end"

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: Every library-encoded endEvent decodes again
```gherkin
Scenario Outline: An endEvent built in Go encodes to keys its end_behavior reads
  Given an endEvent built in Go with <options>
  When it is encoded to JSON and the encoding is decoded
  Then decoding succeeds
  And no node is refused with ErrKeyNotOnEndBehavior

  Examples:
    | options                                           |
    | no options                                        |
    | terminate with a reason and the abort outcome     |
    | an error code                                     |
```

## MODIFIED Requirements

### Requirement: termination_outcome on a terminate end is refused outside its vocabulary
On an endEvent whose `end_behavior` is `terminate`, both decoders SHALL refuse with `ErrInvalidTerminationOutcome` a `termination_outcome` other than `complete`, `abort` or empty, compared exactly, and SHALL decode `complete` and `abort` to the outcome they name. The diagnostic SHALL NOT state that the key is accepted on any other end.

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
  And the error does not state that termination_outcome is accepted or dropped on any other end

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
