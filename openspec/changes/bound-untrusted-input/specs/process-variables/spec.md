## ADDED Requirements

### Requirement: A caller-supplied variables map is refused above the per-payload size limit
The ProcessDriver MUST refuse start variables, a signal or message payload, or a human-task completion output whose JSON encoding is longer than the configured per-payload limit with `runtime.ErrVariablesTooLarge`, leaving the instance unchanged and creating none on a start; a map exactly at the limit SHALL be accepted, the default limit MUST be finite, and a limit of zero or less SHALL disable the check.

#### Scenario: Per-payload size boundary on every ingress
```gherkin
Scenario Outline: Per-payload size boundary on every ingress
  Given a ProcessDriver configured with a per-payload limit of 64 bytes
  And a definition with a start event, a signal catch, a message catch and a user task in sequence
  When <ingress> is called with a variables map whose JSON encoding is <size>
  Then the outcome is <outcome>

  Examples:
    | ingress                            | size     | outcome                          |
    | StartInstance                      | 65 bytes | runtime.ErrVariablesTooLarge     |
    | StartInstance                      | 64 bytes | accepted                         |
    | StartInstance                      | 63 bytes | accepted                         |
    | StartInstance                      | a nil map | accepted                        |
    | StartInstance                      | {}       | accepted                         |
    | DeliverSignal                      | 65 bytes | runtime.ErrVariablesTooLarge     |
    | DeliverSignal                      | 64 bytes | accepted                         |
    | DeliverMessage                     | 65 bytes | runtime.ErrVariablesTooLarge     |
    | DeliverMessage                     | 64 bytes | accepted                         |
    | completing the user task           | 65 bytes | runtime.ErrVariablesTooLarge     |
    | completing the user task           | 64 bytes | accepted                         |
```

#### Scenario: A refused payload leaves no trace
```gherkin
Scenario Outline: A refused payload leaves no trace
  Given a ProcessDriver on <store> configured with a per-payload limit of 64 bytes
  And a running instance parked on a signal catch "approved"
  When signal "approved" is delivered with a payload whose JSON encoding is 65 bytes
  Then the error matches runtime.ErrVariablesTooLarge
  And the instance's variables, tokens and history are unchanged
  And the token is still waiting on "approved"

  Examples:
    | store      |
    | in-memory  |
    | Postgres   |
    | MySQL      |
    | SQLite     |
```

#### Scenario: Multi-byte and escaped content is measured as encoded
```gherkin
Scenario Outline: Multi-byte and escaped content is measured as encoded
  Given a ProcessDriver configured with a per-payload limit of <limit>
  When StartInstance is called with {"k": <value>}
  Then the outcome is <outcome>

  Examples:
    | limit    | value                                        | outcome                      |
    | 14 bytes | "ééé" (the map encodes to 14 bytes)           | accepted                     |
    | 13 bytes | "ééé"                                        | runtime.ErrVariablesTooLarge |
    | 14 bytes | "<<<" (each < encodes as <; 26 bytes)    | runtime.ErrVariablesTooLarge |
    | 26 bytes | "<<<"                                        | accepted                     |
```

#### Scenario: The default limit applies without configuration
```gherkin
Given a ProcessDriver built with no variables-limit option
When StartInstance is called with a variables map one byte over the default per-payload limit
Then the error matches runtime.ErrVariablesTooLarge
But a variables map exactly at the default per-payload limit is accepted
```

#### Scenario: A disabled limit admits a large payload
```gherkin
Scenario Outline: A disabled limit admits a large payload
  Given a ProcessDriver configured with a per-payload limit of <limit> and no total limit
  When StartInstance is called with a 32 MiB variables map
  Then the instance is started

  Examples:
    | limit |
    | 0     |
    | -1    |
```

### Requirement: A caller-supplied variables map is refused above the nesting-depth limit
The ProcessDriver MUST refuse start variables, a signal or message payload, or a human-task completion output nested deeper than the configured depth limit with `runtime.ErrVariablesTooDeep`, leaving the instance unchanged; a map nested exactly to the limit SHALL be accepted, and the default limit MUST be finite and below the depth at which stored variables could no longer be decoded.

#### Scenario: Depth boundary
```gherkin
Scenario Outline: Depth boundary
  Given a ProcessDriver configured with a depth limit of 8
  When <ingress> is called with a variables map nested <depth> objects or arrays deep
  Then the outcome is <outcome>

  Examples:
    | ingress                  | depth | outcome                       |
    | StartInstance            | 9     | runtime.ErrVariablesTooDeep   |
    | StartInstance            | 8     | accepted                      |
    | StartInstance            | 7     | accepted                      |
    | StartInstance            | 1     | accepted                      |
    | DeliverMessage           | 9 (arrays inside objects) | runtime.ErrVariablesTooDeep |
    | completing the user task | 9     | runtime.ErrVariablesTooDeep   |
```

#### Scenario: A variable deeper than the store codec can read is never committed
```gherkin
Scenario Outline: A variable deeper than the store codec can read is never committed
  Given a ProcessDriver on <store> built with no variables-limit option
  When StartInstance is called with a variables map nested 20000 objects deep
  Then the error matches runtime.ErrVariablesTooDeep
  And no instance is created

  Examples:
    | store     |
    | in-memory |
    | Postgres  |
    | MySQL     |
    | SQLite    |
```

### Requirement: A trigger may not grow the instance variables past the total limit
The ProcessDriver MUST refuse a signal, message or human-task completion whose merge would make the instance variables' JSON encoding longer than the configured total limit with `runtime.ErrVariablesTooLarge`, leaving the instance unchanged; an action completion that would do so MUST instead be recorded as a non-retryable action failure matching it. A merge to exactly the limit SHALL be accepted.

#### Scenario: Accumulation across payloads that are each within the per-payload limit
```gherkin
Scenario Outline: Accumulation across payloads that are each within the per-payload limit
  Given a ProcessDriver configured with a per-payload limit of 1 KiB and a total limit of <total>
  And a running instance whose variables encode to 1000 bytes, parked on a message catch
  When the message is delivered with a payload adding <added> bytes to the encoded variables
  Then the outcome is <outcome>

  Examples:
    | total      | added       | outcome                      |
    | 2000 bytes | 1001 bytes  | runtime.ErrVariablesTooLarge |
    | 2000 bytes | 1000 bytes  | accepted                     |
    | 2000 bytes | 999 bytes   | accepted                     |
```

#### Scenario: A payload that overwrites keys is measured after the merge
```gherkin
Given a ProcessDriver configured with a total limit of 2000 bytes
And a running instance whose variables encode to 1990 bytes, including a 500-byte variable "doc", parked on a signal catch
When the signal is delivered with the payload {"doc": "x"}
Then the signal is accepted
```

#### Scenario: An action output past the total fails the action non-retryably
```gherkin
Given a ProcessDriver configured with a total limit of 2000 bytes and a default retry policy allowing 3 attempts
And a service task whose action returns an output that would make the variables encode to 2001 bytes
When the instance is driven
Then the recorded action failure is not retryable and its cause matches runtime.ErrVariablesTooLarge
And no retry timer is armed
And the output is not merged into the instance variables
```

#### Scenario: Two concurrent deliveries cannot jointly exceed the total
```gherkin
Given a ProcessDriver configured with a total limit of 2000 bytes
And a running instance whose variables encode to 1000 bytes, with two tokens parked on message catches "a" and "b"
When messages "a" and "b" are delivered concurrently, each adding 600 bytes
Then exactly one delivery is applied
And the other is refused with runtime.ErrVariablesTooLarge
And the instance variables encode to at most 2000 bytes
```

#### Scenario: An instance already above a lowered total still loads and advances without growing
```gherkin
Given an instance whose stored variables encode to 3000 bytes
And the ProcessDriver is restarted with a total limit of 2000 bytes
When the instance is loaded and a signal with an empty payload is delivered to its parked token
Then the instance loads without error and the token advances
But a signal whose payload adds a new 10-byte variable is refused with runtime.ErrVariablesTooLarge
```
