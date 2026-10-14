# definition-validation Specification

## Purpose
Defines the authoring gate a process definition must pass before it can be registered, published or run, and the declarative input-validation kinds a definition can carry and persist.

## Requirements

### Requirement: Building a definition runs structural validation
`Build` on a definition builder and on the loader returned by `ParseYAML` SHALL run `model.Validate` and SHALL return its error instead of a definition when the definition is structurally invalid.

#### Scenario: A builder definition with no start event is refused
```gherkin
Given a builder for definition "p" version 1 holding only a serviceTask
When Build is called
Then Build returns an error matching ErrNoStartEvent
And no definition is returned
```

#### Scenario: A YAML definition with no start event is refused at Build
```gherkin
Given a YAML definition whose only node is a serviceTask
When it is parsed with ParseYAML
Then parsing succeeds
And Build on the returned loader returns an error matching ErrNoStartEvent
```

#### Scenario: A well-formed definition builds
```gherkin
Given a builder for definition "p" version 1 with start "s", serviceTask "t" and end "e" connected s to t to e
When Build is called
Then Build succeeds
And the definition has 3 nodes and 2 flows
```

### Requirement: Registration enforces the authoring gate
`MemDefinitionRegistry.Register`, `RegisterDefinition` and `MustRegisterDefinition` SHALL refuse a definition that fails `model.Validate` with `ErrInvalidDefinition` wrapped together with every broken rule, and a refused definition SHALL claim neither its pinned nor its latest key.

#### Scenario: Invalid definitions are refused and claim no key
```gherkin
Scenario Outline: A structurally invalid definition is not registered
  Given an empty in-memory definition registry
  And definition "gated" that <defect>
  When the definition is registered
  Then registration fails with ErrInvalidDefinition
  And the error also matches <rule>
  And looking up Latest("gated") returns ErrDefinitionNotFound
  And looking up Version("gated", 1) returns ErrDefinitionNotFound

  Examples:
    | defect                                          | rule                        |
    | has no start event                              | ErrNoStartEvent             |
    | has version 0                                   | ErrInvalidVersion           |
    | has a flow targeting an unknown node            | ErrDanglingFlow             |
    | has an intermediate catch event with no trigger | ErrCatchEventMissingTrigger |
```

#### Scenario: A valid definition claims both keys
```gherkin
Given an empty in-memory definition registry
And a valid definition "gated" version 1
When the definition is registered
Then registration succeeds
And both Latest("gated") and Version("gated", 1) resolve
```

#### Scenario: The process-global registration path runs the same gate
```gherkin
Scenario Outline: RegisterDefinition refuses an invalid definition
  Given a definition that <defect>
  When it is passed to RegisterDefinition
  Then registration fails with ErrInvalidDefinition and <rule>
  And the default registry does not resolve its latest key

  Examples:
    | defect                        | rule                |
    | has no start event            | ErrNoStartEvent     |
    | has an end with outgoing flow | ErrEndHasOutgoing   |
```

#### Scenario: The panicking variants inherit the gate
```gherkin
Scenario Outline: A definition failing the gate panics on the Must path
  Given a definition with no start event
  When it is passed to <call>
  Then the call panics

  Examples:
    | call                                  |
    | MustRegister on an in-memory registry |
    | MustRegisterDefinition                |
```

#### Scenario: Nil and unnamed definitions are refused bare
```gherkin
Scenario Outline: A missing definition or ID is refused before validation
  Given <input>
  When it is registered in an in-memory registry
  Then registration fails with <error>

  Examples:
    | input                         | error                |
    | a nil definition              | ErrNilDefinition     |
    | a definition with an empty ID | ErrEmptyDefinitionID |
```

### Requirement: Identifier and version are bounded to the storable domain
`ValidateDefinition` SHALL refuse, wrapped with `ErrInvalidDefinition`, a definition ID longer than `MaxDefinitionIDRunes` runes, not valid UTF-8, or containing NUL, and a version above `MaxDefinitionVersion`; `PublishDefinition` SHALL apply the same bounds on every backend before writing.

#### Scenario: Bounds and their accepting neighbours
```gherkin
Scenario Outline: Each bound refuses just outside and accepts just inside
  Given a valid definition with ID <id> and version <version>
  When it passes through ValidateDefinition
  Then the outcome is <outcome>

  Examples:
    | id                                  | version                  | outcome                                                                          |
    | 255 ASCII runes                     | 1                        | accepted                                                                         |
    | 256 ASCII runes                     | 1                        | ErrInvalidDefinition and ErrDefinitionIDTooLong                                  |
    | 255 two-byte runes                  | 1                        | accepted                                                                         |
    | 256 two-byte runes                  | 1                        | ErrInvalidDefinition and ErrDefinitionIDTooLong                                  |
    | "short-\xff\xfe-id" (invalid UTF-8) | 1                        | ErrInvalidDefinition and ErrDefinitionIDNotUTF8                                  |
    | "héllo-wörld-日本語"                | 1                        | accepted                                                                         |
    | "nul\x00inside"                     | 1                        | ErrInvalidDefinition and ErrDefinitionIDContainsNUL, not ErrDefinitionIDNotUTF8  |
    | "ver-ok"                            | MaxDefinitionVersion     | accepted                                                                         |
    | "ver-big"                           | MaxDefinitionVersion + 1 | ErrInvalidDefinition and ErrDefinitionVersionTooLarge                            |
```

#### Scenario: The durable store applies the same bounds on every backend
```gherkin
Scenario Outline: Publishing outside the storable domain writes nothing
  Given a definition store on <backend>
  When a definition with <input> is published
  Then the publish <outcome>
  And no row is stored under the ID or any truncation of it

  Examples:
    | backend  | input                            | outcome                                                          |
    | postgres | a 256-rune ID                    | fails with ErrInvalidDefinition and ErrDefinitionIDTooLong       |
    | mysql    | a 256-rune ID                    | fails with ErrInvalidDefinition and ErrDefinitionIDTooLong       |
    | sqlite   | a 256-rune ID                    | fails with ErrInvalidDefinition and ErrDefinitionIDTooLong       |
    | postgres | an invalid UTF-8 ID              | fails with ErrInvalidDefinition and ErrDefinitionIDNotUTF8       |
    | mysql    | an invalid UTF-8 ID              | fails with ErrInvalidDefinition and ErrDefinitionIDNotUTF8       |
    | sqlite   | an invalid UTF-8 ID              | fails with ErrInvalidDefinition and ErrDefinitionIDNotUTF8       |
    | postgres | an ID containing NUL             | fails with ErrInvalidDefinition and ErrDefinitionIDContainsNUL   |
    | mysql    | an ID containing NUL             | fails with ErrInvalidDefinition and ErrDefinitionIDContainsNUL   |
    | sqlite   | an ID containing NUL             | fails with ErrInvalidDefinition and ErrDefinitionIDContainsNUL   |
    | postgres | version MaxDefinitionVersion + 1 | fails with ErrInvalidDefinition and ErrDefinitionVersionTooLarge |
    | mysql    | version MaxDefinitionVersion + 1 | fails with ErrInvalidDefinition and ErrDefinitionVersionTooLarge |
    | sqlite   | version MaxDefinitionVersion + 1 | fails with ErrInvalidDefinition and ErrDefinitionVersionTooLarge |
```

#### Scenario: Values at the bound are stored faithfully on every backend
```gherkin
Scenario Outline: An at-limit ID or version publishes and reads back unchanged
  Given a definition store on <backend>
  When a definition with <input> is published
  Then the publish succeeds
  And reading it back returns the same ID and version

  Examples:
    | backend  | input                                 |
    | postgres | a 255-rune ID                         |
    | mysql    | a 255-rune ID                         |
    | sqlite   | a 255-rune ID                         |
    | postgres | the ID "utf8-good-héllo-wörld-日本語" |
    | mysql    | the ID "utf8-good-héllo-wörld-日本語" |
    | sqlite   | the ID "utf8-good-héllo-wörld-日本語" |
    | postgres | version MaxDefinitionVersion          |
    | mysql    | version MaxDefinitionVersion          |
    | sqlite   | version MaxDefinitionVersion          |
```

### Requirement: Structural authoring defects are refused
`model.Validate` SHALL refuse a non-end node with no outgoing flow with `ErrDeadEnd`, a flow whose source or target names no node with `ErrDanglingFlow`, two nodes of one definition sharing an ID, blank IDs included, with `ErrDuplicateNodeID`, and a subProcess with no nested definition with `ErrMissingSubprocess`.

#### Scenario: Each structural defect is refused by its sentinel
```gherkin
Scenario Outline: A structurally broken definition fails validation
  Given a definition that <defect>
  When the definition is validated
  Then validation fails with <error>

  Examples:
    | defect                                                       | error                |
    | has a serviceTask with no outgoing flow                      | ErrDeadEnd           |
    | has a flow whose target names no node                        | ErrDanglingFlow      |
    | has a flow whose source names no node                        | ErrDanglingFlow      |
    | has two serviceTasks with ID "charge"                        | ErrDuplicateNodeID   |
    | has two nodes with ID "dup" inside a subprocess               | ErrDuplicateNodeID   |
    | has two nodes whose ID is blank                              | ErrDuplicateNodeID   |
    | has a subProcess node with no nested definition              | ErrMissingSubprocess |
```

#### Scenario: The legal neighbours validate
```gherkin
Scenario Outline: A structurally sound definition passes
  Given a definition that <shape>
  When the definition is validated
  Then validation succeeds

  Examples:
    | shape                                                                  |
    | runs start to serviceTask to end                                       |
    | reuses the outer definition's node IDs inside a nested subprocess      |
    | has a subProcess carrying a valid nested definition                    |
```

### Requirement: Intermediate events must declare what they wait for or emit
`model.Validate` SHALL refuse an intermediateCatchEvent that declares no timer, signal or message trigger with `ErrCatchEventMissingTrigger`, and an intermediateThrowEvent that declares no signal name with `ErrThrowEventMissingTrigger`.

#### Scenario: Trigger-less intermediate events are refused
```gherkin
Scenario Outline: An intermediate event with nothing to wait for or emit fails validation
  Given a definition whose <node>
  When the definition is validated
  Then validation fails with <error>

  Examples:
    | node                                                           | error                       |
    | catch event declares no trigger                                | ErrCatchEventMissingTrigger |
    | catch event declares only a correlation key                    | ErrCatchEventMissingTrigger |
    | trigger-less catch event sits behind an event-based gateway    | ErrCatchEventMissingTrigger |
    | throw event declares no signal name                            | ErrThrowEventMissingTrigger |
    | throw event declares a display name but no signal name         | ErrThrowEventMissingTrigger |
```

#### Scenario: Events that declare a trigger validate
```gherkin
Scenario Outline: An intermediate event with a trigger passes
  Given a definition whose <node>
  When the definition is validated
  Then validation succeeds

  Examples:
    | node                                       |
    | catch event waits on a timer               |
    | catch event waits on a signal              |
    | catch event waits on a message             |
    | throw event emits a signal                 |
    | compensation throw event carries no signal |
```

### Requirement: Trigger boundaries attach only to hosts that arm them
`model.Validate` SHALL refuse with `ErrBoundaryTriggerHost` a timer, signal or message boundary event attached to a sendTask, subProcess or callActivity, and SHALL accept such a boundary on a serviceTask, businessRuleTask, receiveTask or userTask, and an error boundary on a subProcess or callActivity.

#### Scenario: A trigger boundary on an unarmed host is refused
```gherkin
Scenario Outline: The boundary attachment fails validation
  Given a definition with a <trigger> boundary attached to a <host>
  When the definition is validated
  Then validation fails with ErrBoundaryTriggerHost

  Examples:
    | trigger | host         |
    | timer   | sendTask     |
    | signal  | subProcess   |
    | message | callActivity |
```

#### Scenario: Armed hosts and error boundaries stay valid
```gherkin
Scenario Outline: The boundary attachment validates
  Given a definition with a <trigger> boundary attached to a <host>
  When the definition is validated
  Then validation succeeds

  Examples:
    | trigger | host             |
    | timer   | serviceTask      |
    | timer   | businessRuleTask |
    | signal  | receiveTask      |
    | message | userTask         |
    | error   | subProcess       |
    | error   | callActivity     |
```

### Requirement: A node of a foreign type is refused
`model.Validate` and `Build` SHALL refuse with `ErrForeignNodeType`, at any subprocess depth, a node whose concrete type is not the type registered for the kind it reports, and SHALL NOT refuse a node produced by that kind's own constructor or decoder.

#### Scenario: A counterfeit node is refused at both ingresses
```gherkin
Scenario Outline: A node that claims a kind it is not fails both gates
  Given a definition containing a counterfeit node reporting <kind> <where>
  When the definition is validated with model.Validate
  Then validation fails with ErrForeignNodeType
  And when the same nodes are added to a builder, Build fails with ErrForeignNodeType

  Examples:
    | kind                   | where                       |
    | userTask               | at the top level            |
    | startEvent             | inside a subprocess         |
    | startEvent             | two subprocess levels down  |
    | receiveTask            | at the top level            |
    | intermediateCatchEvent | at the top level            |
```

#### Scenario: Every genuine kind passes the type check
```gherkin
Given the node kinds bundle is imported
When a node of each registered kind is decoded from its own wire form and validated
Then no validation error matches ErrForeignNodeType
And a counterfeit node reporting that same kind does match ErrForeignNodeType
```

### Requirement: Node kinds are closed to consumers
A package outside this module SHALL NOT be able to register a node kind: a consumer build that imports the registration capability, or calls `model.RegisterKind` with a capability value it constructed itself, SHALL fail to compile, while the module's own kind packages register every shipped kind.

#### Scenario: A consumer cannot reach kind registration
```gherkin
Scenario Outline: Registering a kind from a consumer module fails to build
  Given a consumer module outside this one that <attempt>
  When it is compiled
  Then compilation fails with <diagnostic>

  Examples:
    | attempt                                                     | diagnostic                                                     |
    | imports the package that grants the registration capability | a refusal to use an internal package, naming that package      |
    | passes a self-built capability value to model.RegisterKind  | a refusal of the argument's type, naming the capability type   |
```

#### Scenario: The shipped kind packages do register every kind
```gherkin
Given the definition/kinds package is imported
When each node kind from KindStartEvent through KindCompensationThrowEvent is encoded
Then each encodes to a registered name
```

### Requirement: A businessRuleTask cannot carry both a rule and an action
`model.Validate` SHALL refuse a businessRuleTask carrying both a `rule` and an `action` with `ErrRuleAndAction`, and SHALL accept one carrying an action and no rule, or neither.

#### Scenario: Rule and action together are refused
```gherkin
Given a definition whose businessRuleTask "score" carries rule "pricing.v3" and action "risk-score"
When the definition is validated
Then validation fails with ErrRuleAndAction
```

#### Scenario: A businessRuleTask without a rule validates
```gherkin
Scenario Outline: A businessRuleTask without a rule passes
  Given a definition whose businessRuleTask "score" carries <config>
  When the definition is validated
  Then validation succeeds

  Examples:
    | config                    |
    | action "risk-score"       |
    | neither rule nor action   |
```

### Requirement: Declarative validation kinds reconstruct, unregistered kinds fail Build
A node's declarative validation block SHALL be reconstructed into a runnable validator from the adapter registered for its kind, at `Build` and on a JSON decode, and `Build` SHALL fail with `validate.ErrUnknownKind` when no adapter is registered for that kind.

#### Scenario: An expr validation block reconstructs at Build
```gherkin
Scenario Outline: A YAML start event's expr validation becomes a live validator
  Given a YAML definition whose start event has a validation block of kind "expr" and schema "amount > 0"
  And the loader is given <registry>
  When Build is called
  Then Build succeeds
  And the start event's validator rejects amount -1 and accepts amount 5

  Examples:
    | registry                               |
    | a registry holding the expr adapter    |
    | no registry, so the default registry   |
```

#### Scenario: An unregistered kind fails Build
```gherkin
Given a YAML definition whose start event has a validation block of kind "expr"
And the loader is given an empty validation registry
When Build is called
Then Build fails with validate.ErrUnknownKind
```

#### Scenario: A persisted expr validation reconstructs on decode
```gherkin
Given a definition whose start event carries an expr validation "amount > 0", encoded to JSON
When the JSON is decoded
Then the start event's validator rejects amount -1 and accepts amount 5
```

#### Scenario: A json-schema strategy round-trips through its descriptor
```gherkin
Given a json-schema validation strategy built from a schema
When a new strategy is built from its descriptor's schema with the json-schema factory
Then the rebuilt validator accepts and rejects the same inputs as the original
```
