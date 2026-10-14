## ADDED Requirements

### Requirement: An entry action on an immediate manual task is refused
`model.Validate` SHALL refuse with `ErrEntryActionOnImmediate` a userTask that is manual, completes immediately and declares an entry action, and MUST accept an entry action on a wait-mode manual task and on a non-manual task.

#### Scenario: The immediate manual task cannot carry an entry action
```gherkin
Scenario Outline: A userTask that is <mode> with <entry>
  Given a userTask "review" that is <mode> with <entry>
  When the definition is built
  Then <outcome>

  Examples:
    | mode                          | entry                           | outcome                                         |
    | manual and immediate          | entry_action "review.prepare"   | Build fails with ErrEntryActionOnImmediate naming "review" |
    | manual and immediate          | no entry_action                 | Build succeeds                                  |
    | manual and waiting            | entry_action "review.prepare"   | Build succeeds                                  |
    | not manual                    | entry_action "review.prepare"   | Build succeeds                                  |
```

#### Scenario: The refusal reaches a nested subprocess
```gherkin
Given a subProcess whose nested definition holds a manual immediate userTask "inner" with entry_action "x.prepare"
When the definition is built
Then Build fails with ErrEntryActionOnImmediate naming "inner"
```

### Requirement: A failure policy without its action is refused
`model.Validate` SHALL refuse with `ErrOnFailureWithoutAction` a userTask whose entry or completion failure policy is `ignore` while the corresponding action is absent, and MUST accept the default `stop` policy with or without an action.

#### Scenario: ignore needs an action to ignore
```gherkin
Scenario Outline: A userTask built with <action> and <policy>
  Given a userTask "review" built with <action> and <policy>
  When the definition is built
  Then <outcome>

  Examples:
    | action                                 | policy                       | outcome                                                  |
    | no entry_action                        | entry policy ignore          | Build fails with ErrOnFailureWithoutAction naming "review" |
    | no completion_action                   | completion policy ignore     | Build fails with ErrOnFailureWithoutAction naming "review" |
    | entry_action "review.prepare"          | entry policy ignore          | Build succeeds                                           |
    | completion_action "review.finalize"    | completion policy ignore     | Build succeeds                                           |
    | no entry_action                        | no policy (stop)             | Build succeeds                                           |
    | entry_action "review.prepare"          | completion policy ignore     | Build fails with ErrOnFailureWithoutAction naming "review" |
```

### Requirement: on_failure on a ReceiveTask completion action is refused
PENDING Q2 (recommended option). `model.Validate` SHALL refuse with `ErrOnFailureUnsupportedKind` a receiveTask whose completion failure policy is `ignore`, and MUST accept a receiveTask completion action with the default `stop` policy, in either wire form.

#### Scenario: Only userTask completion actions take ignore
```gherkin
Scenario Outline: A <kind> whose completion_action is <value>
  Given a JSON definition with a <kind> "n1" whose completion_action is <value>
  When the definition is decoded and built
  Then <outcome>

  Examples:
    | kind        | value                                    | outcome                                                    |
    | receiveTask | {"name":"ack","on_failure":"ignore"}     | Build fails with ErrOnFailureUnsupportedKind naming "n1"   |
    | receiveTask | {"name":"ack","on_failure":"stop"}       | Build succeeds                                             |
    | receiveTask | "ack"                                    | Build succeeds                                             |
    | userTask    | {"name":"ack","on_failure":"ignore"}     | Build succeeds                                             |
```
