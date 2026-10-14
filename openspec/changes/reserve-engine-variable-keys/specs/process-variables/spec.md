## ADDED Requirements

### Requirement: No action input carries a fold-variant of an engine-stamped name
Every action input the engine builds from instance variables, and not only the primary service-task input, MUST omit every key that encoding/json would bind to `_idempotencyKey`, `_errorMessage` or `_errorAttempts` without being that exact name, while those exact names and every key that folds onto none of them MUST still arrive.

#### Scenario: A completion action sees the engine's error message, not a caller's variant
```gherkin
Scenario Outline: A completion action sees the engine's error message, not a caller's variant
  Given a service task "task" with a retry policy of 1 attempt and a recovery flow to <node> carrying a completion action "ca"
  And an instance started with the variables {<variant>: "SPOOF"}
  When the action of "task" fails with the message "ENGINE-ERR"
  And <node> is completed
  Then the input of "ca" does not carry the key <variant>
  And the input of "ca" carries "_errorMessage" = "ENGINE-ERR" and "_errorAttempts" = 1
  And a typed action over an In declaring "_errorMessage" decodes "ENGINE-ERR"

  Examples:
    | node                                   | variant                          |
    | a user task "u1"                       | "_errormessage"                  |
    | a user task "u1"                       | "_ERRORMESSAGE"                  |
    | a user task "u1"                       | "_errorMeſsage" (U+017F LONG S)  |
    | a user task "u1"                       | "_errorattempts"                 |
    | a receive task "r1" completed by a message | "_errormessage"              |
```

#### Scenario: Every non-primary action input withholds fold-variants
```gherkin
Scenario Outline: Every non-primary action input withholds fold-variants
  Given an instance "inst-42" whose variables hold "_idempotencykey" = "SPOOF", "_ERRORATTEMPTS" = 9 and "customerId" = "c-1"
  When <action> is invoked
  Then its input carries neither "_idempotencykey" nor "_ERRORATTEMPTS"
  And its input carries "customerId" = "c-1"

  Examples:
    | action                                                              |
    | a definition cancel action listed in CancelActions, on CancelInstance |
    | a node cancel action set with WithCancelAction, on CancelInstance   |
    | a deadline action set with WithDeadlineAction, on deadline breach   |
    | a reminder action set with WithWaitAction, on a reminder fire       |
    | an error boundary's action, when the boundary catches               |
    | a compensation action set with WithCompensateAction, on reverse     |
```

#### Scenario: Keys that fold onto no reserved name reach every action input
```gherkin
Scenario Outline: Keys that fold onto no reserved name reach every action input
  Given an instance whose variables include <key> = "v"
  When a completion action, a cancel action and a compensation action are invoked
  Then each input carries <key> = "v"

  Examples:
    | key               |
    | "_error"          |
    | "_traceId"        |
    | "errorMessage"    |
    | "_errorMessages"  |
    | ""                |
```

#### Scenario: A variant stored before the upgrade is withheld on replay
```gherkin
Given an instance snapshot written before this change whose variables hold "_errormessage" = "SPOOF" beside the engine's "_errorMessage" = "ENGINE-ERR"
And its token waits on a user task carrying a completion action "ca"
When the snapshot is loaded and the user task is completed
Then the input of "ca" does not carry "_errormessage"
And it carries "_errorMessage" = "ENGINE-ERR"
```

### Requirement: Externally supplied variables cannot name an engine-stamped variable
Start variables, a human completion's output, a message payload and a signal payload MUST be refused with `engine.ErrReservedVariable`, naming the key, when any key equals or folds onto `_idempotencyKey`, `_errorMessage` or `_errorAttempts`, and the refused trigger MUST leave the instance unchanged and emit no command. Pending design Q1 to Q3: this is the recommended option.

#### Scenario: A reserved key in an external map is refused
```gherkin
Scenario Outline: A reserved key in an external map is refused
  Given <setup>
  When <trigger> is stepped with a map holding "amount" = 1 and <key> = "x"
  Then the step fails with engine.ErrReservedVariable
  And the error text contains <key>
  And the instance state equals its state before the step and no command is emitted

  Examples:
    | setup                                         | trigger                     | key                 |
    | a new instance                                | StartInstance               | "_errorMessage"     |
    | a new instance                                | StartInstance               | "_errormessage"     |
    | a new instance                                | StartInstance               | "_idempotencyKey"   |
    | a new instance                                | StartInstance               | "_idempotencyKey" (U+212A KELVIN SIGN for the k) |
    | a new instance                                | StartInstance               | "_errorAttempts"    |
    | an instance parked on user task "u1"          | HumanCompleted output       | "_errorMessage"     |
    | an instance parked on user task "u1"          | HumanCompleted output       | "_ERRORATTEMPTS"    |
    | an instance whose token awaits message "m"    | MessageReceived payload     | "_errorMessage"     |
    | an instance whose token awaits signal "s"     | SignalReceived payload      | "_errorMeſsage" (U+017F LONG S) |
```

#### Scenario: Keys near a reserved name are accepted
```gherkin
Scenario Outline: Keys near a reserved name are accepted
  Given a new instance
  When StartInstance is stepped with the variables {<key>: "x"}
  Then the step succeeds
  And the instance variable <key> equals "x"

  Examples:
    | key                |
    | "_error"           |
    | "errorMessage"     |
    | "_errorMessages"   |
    | "_errorMessage "   |
    | " _errorMessage"   |
    | "_idempotency"     |
    | "_traceId"         |
    | ""                 |
```

#### Scenario: A caller can no longer suppress the engine's error message
```gherkin
Given a service task "task" with a retry policy of 1 attempt whose recovery flow leads to user task "u1" and then an exclusive gateway routing _errorMessage contains "fatal" to "escalate"
And the action of "task" failed with the message "fatal: disk"
When "u1" is completed with the output {"_errorMessage": "all good"}
Then the step fails with engine.ErrReservedVariable
And the instance variable "_errorMessage" still equals "fatal: disk"
And when "u1" is completed with the output {"note": "ok"}
Then the token takes the flow to "escalate"
```

#### Scenario: A caller can no longer fabricate the engine's error message
```gherkin
Given a definition start → exclusive gateway routing _errorMessage contains "fatal" to "escalate", default to "log"
When an instance is started with the variables {"_errorMessage": "fatal: invented"}
Then the step fails with engine.ErrReservedVariable and no instance state is produced
And when an instance is started with no variables
Then the token takes the default flow to "log"
```

#### Scenario: An instance that reached a call activity after a recovered failure still starts its child
```gherkin
Given a service task "task" with a retry policy of 1 attempt whose recovery flow leads to a call activity "call"
When the action of "task" fails and the instance is driven by a ProcessDriver
Then the child instance of "call" is started with no error
And the parent instance is running with its token at "call"
```

#### Scenario: An instance holding a reserved key from before the upgrade keeps running
```gherkin
Given an instance snapshot written before this change whose variables hold "_errorMessage" = "set by a caller"
When it is loaded and a message carrying the payload {"note": "ok"} is delivered to its waiting token
Then the step succeeds and the token advances
And the instance variable "_errorMessage" still equals "set by a caller"
```

#### Scenario: The refusal reaches a caller identically through every adapter
```gherkin
Scenario Outline: The refusal reaches a caller identically through every adapter
  Given the <adapter> adapter mounted over a service holding a registered definition
  When a caller sends POST /instances with variables {"_errormessage": "x"}
  Then the response status is 400 with the error code "bad_request"
  And the message contains "_errormessage"
  And no instance is created

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: The refusal surfaces through the ProcessDriver
```gherkin
Scenario Outline: The refusal surfaces through the ProcessDriver
  Given a ProcessDriver over <store> holding a registered definition
  When ProcessDriver.Start is called with the variables {"_idempotencyKey": "chosen"}
  Then the call fails with engine.ErrReservedVariable
  And the store holds no instance for that ID

  Examples:
    | store       |
    | Postgres    |
    | MySQL       |
    | SQLite      |
```
