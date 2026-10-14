## ADDED Requirements

### Requirement: A decode refusal names its enclosing subprocess path
Both decoders SHALL name, in every node decode refusal raised inside a nested subprocess, the ID of each enclosing subProcess node from the outermost inward, and the refusal MUST still match its sentinel with `errors.Is`. A refusal on a top-level node SHALL keep its message unchanged.

#### Scenario: The refusal names every enclosing subprocess
```gherkin
Scenario Outline: A misplaced key at depth names its path
  Given a <format> definition whose endEvent "e" carrying action "charge" sits <where>
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind
  And the error names "e" and "action"
  And the error names <path> in that order

  Examples:
    | format | where                                                     | path                       |
    | JSON   | inside subProcess "sub"                                   | "sub"                      |
    | YAML   | inside subProcess "sub"                                   | "sub"                      |
    | JSON   | inside subProcess "inner" inside subProcess "outer"      | "outer", then "inner"      |
    | YAML   | inside subProcess "inner" inside subProcess "outer"      | "outer", then "inner"      |
    | JSON   | three subProcess levels down, "a" then "b" then "c"       | "a", then "b", then "c"    |
```

#### Scenario: A top-level refusal keeps its message
```gherkin
Scenario Outline: No path is added at the top level
  Given a <format> definition whose top-level endEvent "e" carries action "charge"
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind
  And the error message is exactly what it is before this change

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: Negative control - a legal key at depth still decodes
```gherkin
Scenario Outline: The path is only reported on a refusal
  Given a <format> definition whose endEvent "e" carrying end_behavior "terminate" sits two subProcess levels down
  When the definition is decoded
  Then decoding succeeds

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: A stored definition reports the path on load
```gherkin
Scenario Outline: The definition store load path carries the subprocess path
  Given a <dialect> definition store holding a row, written out of band, whose subProcess "sub" contains an endEvent "e" carrying action "charge"
  When the definition is loaded by ID and version
  Then loading fails with ErrKeyNotOnKind
  And the error names "sub", "e" and "action"

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Every node decode refusal is reported in one pass
Both decoders SHALL report every node decode refusal of a definition, across keys, nodes and subprocess levels, in one error, in document order, and every reported refusal MUST match its sentinel with `errors.Is`. The report SHALL name at most 20 refusals and then state how many more there were.

#### Scenario: Several refusals are reported together
```gherkin
Scenario Outline: One decode reports every refusal
  Given a <format> definition with <defects>
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind
  And the error names <named> in that order

  Examples:
    | format | defects                                                                              | named                                                     |
    | JSON   | endEvent "e" carrying action, signal_name and manual                                 | "action", "signal_name", "manual" on "e"                  |
    | YAML   | endEvent "e" carrying action, signal_name and manual                                 | "action", "signal_name", "manual" on "e"                  |
    | JSON   | endEvent "e1" carrying action, then endEvent "e2" carrying outcomes                   | "action" on "e1", then "outcomes" on "e2"                 |
    | YAML   | endEvent "e1" carrying action, then endEvent "e2" carrying outcomes                   | "action" on "e1", then "outcomes" on "e2"                 |
    | JSON   | subProcess "sub" carrying action whose endEvent "e" carries action                    | "action" on "sub", then "action" on "e" under "sub"       |
    | YAML   | subProcess "sub" carrying action whose endEvent "e" carries action                    | "action" on "sub", then "action" on "e" under "sub"       |
```

#### Scenario: Refusals from different gates are reported together
```gherkin
Scenario Outline: Each refusal keeps its own sentinel
  Given a <format> definition whose exclusiveGateway "g" carries action "charge" and whose terminate endEvent "t" carries termination_outcome "finish"
  When the definition is decoded
  Then the error matches ErrKeyNotOnKind
  And the error also matches ErrInvalidTerminationOutcome
  And the error names "g" before "t"

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: The report is capped
```gherkin
Scenario Outline: At most 20 refusals are named
  Given a JSON definition with <count> exclusiveGateway nodes that each carry action "charge"
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind
  And the error names the first <named> nodes in document order
  And the error <tail>

  Examples:
    | count | named | tail                                  |
    | 1     | 1     | states no further count               |
    | 19    | 19    | states no further count               |
    | 20    | 20    | states no further count               |
    | 21    | 20    | states that 1 more refusal was found  |
    | 1000  | 20    | states that 980 more refusals were found |
```

#### Scenario: The report is deterministic
```gherkin
Given a JSON definition with refused keys on three nodes at two subprocess levels
When it is decoded 50 times, 10 of them concurrently
Then every decode returns the same error message
```

#### Scenario: Negative control - legal keys on every node decode
```gherkin
Scenario Outline: A definition with no refusal is not reported
  Given a <format> definition whose every node carries only keys its kind reads, at three subprocess levels
  When the definition is decoded
  Then decoding succeeds

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

### Requirement: JSON and YAML report the same decode refusals
For the same definition authored in JSON and in YAML, both decoders SHALL report the same set of node decode refusals in the same order, and a YAML inline subprocess MUST NOT be structurally validated while any node decode refusal exists anywhere in the definition.

#### Scenario: A decode refusal outranks a nested structural defect
```gherkin
Scenario Outline: The outer refusal is reported, not the nested defect
  Given a <format> definition whose subProcess "sub" carries action "charge" and whose inline subprocess has no start event
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind naming "sub" and "action"
  And the error does not match ErrNoStartEvent

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: Negative control - a nested structural defect is still reported in YAML
```gherkin
Given a YAML definition whose subProcess "sub" carries only keys its kind reads and whose inline subprocess has no start event
When the definition is parsed
Then parsing fails with ErrNoStartEvent
And the error names "sub"
```

### Requirement: Encoding refuses a cyclic subprocess graph
`ProcessDefinition.MarshalJSON` SHALL return `ErrCyclicSubprocess` and no output, without aborting the process, when a subprocess definition contains, at any depth, a definition that encloses it; a subprocess definition reached twice without a cycle MUST still encode. Pending: design OQ1 decides the sentinel and this rule.

#### Scenario: A cycle is refused at any length
```gherkin
Scenario Outline: Encoding a cyclic graph returns ErrCyclicSubprocess
  Given a definition "d" built in Go whose <cycle>
  When "d" is encoded to JSON
  Then encoding fails with ErrCyclicSubprocess
  And no output is returned
  And the process keeps running

  Examples:
    | cycle                                                                      |
    | subProcess node's definition is "d" itself                                 |
    | subProcess definition "a" contains a subProcess whose definition is "d"   |
    | cycle closes three subprocess levels below "d"                             |
    | nested definition "a" contains a subProcess whose definition is "a"       |
```

#### Scenario: Negative control - a shared subprocess definition encodes
```gherkin
Scenario Outline: A repeated but acyclic definition encodes
  Given a definition with <shape>
  When it is encoded to JSON
  Then encoding succeeds
  And the output decodes again without error

  Examples:
    | shape                                                                  |
    | two subProcess nodes that share one subprocess definition              |
    | one subprocess definition shared at depth 1 and at depth 2             |
    | a chain of 32 distinct nested subprocess definitions                   |
```

#### Scenario: Publishing a cyclic graph does not abort the process
```gherkin
Scenario Outline: The durable publish path refuses the cycle by name
  Given a <dialect> definition store
  And a definition whose subProcess node's definition is the definition itself
  When the definition is published
  Then publishing fails with ErrCyclicSubprocess
  And the process keeps running
  And no row is written for the definition

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: An inline rule document refuses non-string keys below its top level
A YAML inline rule document SHALL be refused with `ErrInvalidRule` when any mapping key below its top level, including inside a list, is not a string, whatever Go toolchain the library is built with, and the refused rule MUST be left zero. Pending: design OQ2 decides this rule.

#### Scenario: A nested non-string key is refused
```gherkin
Scenario Outline: The refusal does not depend on encoding/json
  Given a businessRuleTask whose YAML inline rule is <document>
  When the rule is decoded
  Then decoding fails with ErrInvalidRule
  And the rule is zero

  Examples:
    | document                          |
    | stages: {1: review}               |
    | stages: {true: review}            |
    | stages: {1.5: review}             |
    | stages: {~: review}               |
    | stages: [{1: review}]             |
    | a: {b: {c: {7: deep}}}            |
```

#### Scenario: Negative control - string keys are accepted
```gherkin
Scenario Outline: A nested string key decodes
  Given a businessRuleTask whose YAML inline rule is <document>
  When the rule is decoded
  Then decoding succeeds
  And the inline document is <json>

  Examples:
    | document                    | json                         |
    | stages: {a: review}         | {"stages":{"a":"review"}}    |
    | stages: {"1": review}       | {"stages":{"1":"review"}}    |
    | stages: {"é": review}       | {"stages":{"é":"review"}}    |
    | stages: {" a ": review}     | {"stages":{" a ":"review"}}  |
```

#### Scenario: A top-level integer key keeps its documented coercion
```gherkin
Given a businessRuleTask whose YAML inline rule is "1: review"
When the rule is decoded
Then decoding succeeds
And the inline document is {"1":"review"}
```

#### Scenario: A JSON inline rule is unaffected
```gherkin
Given a businessRuleTask whose JSON inline rule is {"stages":{"1":"review"}}
When the definition is decoded
Then decoding succeeds
And the inline document is {"stages":{"1":"review"}}
```
