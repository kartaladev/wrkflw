## Purpose

Defines how an instance's process variables are seeded, merged from triggers, handed to a primary service action alongside the engine's idempotency stamp, and restored by a reverse, so consumers can rely on what an action reads and what an instance carries.

## ADDED Requirements

### Requirement: An instance keeps an independent copy of its start variables
Starting an instance MUST set its variables to the supplied start variables and retain a separate copy of them as the instance's start variables, which later changes to the live variables MUST NOT alter.

#### Scenario: Start variables survive a later change to the live variables
```gherkin
Given a definition start → end
When an instance "i1" is started with the variables {"amount": 100}
Then the instance's start variables are {"amount": 100}
And when the live variable "amount" is then set to 999
Then the instance's start variables still hold "amount" = 100
```

### Requirement: Completion results are merged into the instance variables
The output of a completed action and the output of a completed human task MUST be merged into the instance variables, keeping existing variables the output does not name.

#### Scenario: Completion output becomes instance variables
```gherkin
Scenario Outline: Completion output becomes instance variables
  Given a running instance parked on <node>
  When <trigger> is delivered with the output <output>
  Then the instance variable <key> equals <value>

  Examples:
    | node                 | trigger                                          | output              | key        | value |
    | a service task       | an action completion for its command             | {"result": "ok"}    | "result"   | "ok"  |
    | a user task          | a human completion carrying CompletionInput.Output | {"approved": true} | "approved" | true  |
```

### Requirement: A trigger delivered to a terminal or unmatched target leaves variables unchanged
A message or signal delivered to a completed, failed or terminated instance, and a signal that matches no awaiting token, MUST NOT merge its payload into the instance variables.

#### Scenario: A dropped payload is not merged
```gherkin
Scenario Outline: A dropped payload is not merged
  Given <instance>
  When <trigger> is delivered with the payload {"x": 1}
  Then no error is returned and no command is emitted
  And the instance variables are unchanged

  Examples:
    | instance                                                       | trigger                          |
    | a failed instance that still holds a token awaiting signal "approved" | signal "approved"         |
    | a failed instance that still holds a token awaiting message "approved-msg" | message "approved-msg" |
    | a completed instance                                           | signal "approved"                |
    | a completed instance                                           | message "approved-msg"           |
    | a terminated instance                                          | signal "approved"                |
    | a terminated instance                                          | message "approved-msg"           |
    | a running instance whose only token awaits a different signal  | signal "no-match"                |
```

### Requirement: A primary service action receives the variables plus a stable idempotency key
The input of a node's primary service-task action MUST be a copy of the instance variables with `_idempotencyKey` set to `"<instance id>:<node id>"`, overwriting any caller value of that exact name, and a re-invocation of the same node after a retryable failure MUST carry the same key.

#### Scenario: The key is the same on the first attempt and on a retry
```gherkin
Given a definition start → service task "task" → end with a retry policy allowing 3 attempts
And an instance "p"
When the instance is started
Then the action input carries "_idempotencyKey" = "p:task"
And when that invocation fails with a retryable error and the retry timer fires
Then the re-invocation's input carries "_idempotencyKey" = "p:task"
```

#### Scenario: The stamp is added beside the instance variables
```gherkin
Scenario Outline: The stamp is added beside the instance variables
  Given an instance "inst-42" with the variables <vars> reaching service task "task"
  When the primary action input is built
  Then the input carries "_idempotencyKey" = "inst-42:task"
  And the input carries <kept>

  Examples:
    | vars                                     | kept                                        |
    | {"customerId": "c-1", "amount": 42}      | "customerId" = "c-1" and "amount" = 42      |
    | {"_idempotencyKey": "caller-chosen"}     | no "caller-chosen" value                    |
    | no variables                             | no other key                                |
```

### Requirement: A primary service action never receives a fold-variant of an engine-stamped name
The input of a node's primary service-task action MUST omit every key that encoding/json would bind to `_idempotencyKey`, `_errorMessage` or `_errorAttempts` without being that exact name, in either typed-input mode, while every key that folds onto none of those names MUST still reach the action.

#### Scenario: Fold-variants of reserved names are withheld from the action
```gherkin
Scenario Outline: Fold-variants of reserved names are withheld from the action
  Given an instance "inst-42" whose variables include the key <variant>
  When the primary action input for service task "task" is built
  Then the input does not carry the key <variant>
  And the input carries "_idempotencyKey" = "inst-42:task"

  Examples:
    | variant             |
    | "_idempotencykey"   |
    | "_IDEMPOTENCYKEY"   |
    | "_IdempotencyKey"   |
    | "_idempotencyKEY"   |
    | "_idempotencyKey" (U+212A KELVIN SIGN for the k) |
    | "_errormessage"     |
    | "_ERRORATTEMPTS"    |
    | "_errorMeſsage" (U+017F LONG S) |
```

#### Scenario: Keys that fold onto no reserved name still arrive
```gherkin
Scenario Outline: Keys that fold onto no reserved name still arrive
  Given an instance "inst-42" whose variables include <key> = <value>
  When the primary action input for service task "task" is built
  Then the input carries <key> = <value>

  Examples:
    | key              | value                 |
    | "_traceId"       | "t-1"                 |
    | "_error"         | "not a reserved stamp" |
    | "customerId"     | "c-1"                 |
```

#### Scenario: A lenient typed action sees the engine's stamp, not a caller's variant
```gherkin
Given a service task "task" whose action is built with action.Typed with no options over an In declaring "_idempotencyKey"
When instance "typed-1" is driven with the variables {"_idempotencykey": "SPOOFED-BY-ATTACKER"}
Then the action receives "typed-1:task" as its idempotency key
And the instance completes with no incident
```

### Requirement: Reversing an instance restores its variables
Reversing a running instance (`ProcessDriver.ReverseInstance`, or the engine's `NewReverseToStart` and `NewReverseToNode` triggers) MUST, on a full reverse, reset the instance variables to its start variables, and with `WithTargetNode` MUST restore them to the target node's start-of-visit snapshot, except that an empty snapshot MUST leave the current variables untouched.

#### Scenario: A full reverse resets the variables to the start variables
```gherkin
Given an instance started with {"amount": 100} whose compensable service task completed with {"amount": 500}
And the instance is running, parked on a later task
When the instance is reversed to its start and the compensation action completes
Then the instance is running
And the instance variable "amount" equals 100
```

#### Scenario: A target reverse restores the target's start-of-visit variables
```gherkin
Scenario Outline: A target reverse restores the target's start-of-visit variables
  Given an instance started with {"a": 1} whose compensable steps step1, step2 and step3 completed with {"a": 9, "b": 2}, {"c": 3} and {"d": 4}
  And the instance is running, parked on a later task
  When the instance is reversed to <target> and every compensation action completes
  Then the instance is running with its token at <target>
  And the instance variables equal <vars>

  Examples:
    | target  | vars                         |
    | "step1" | {"a": 1}                     |
    | "step3" | {"a": 9, "b": 2, "c": 3}     |
```

#### Scenario: An empty start-of-visit snapshot leaves the variables untouched
```gherkin
Given an instance started with no variables whose first recorded node is "step1"
And later steps have set the variable "a" = 2
When the instance is reversed to "step1" and the walk finishes
Then the instance variable "a" still equals 2
```
