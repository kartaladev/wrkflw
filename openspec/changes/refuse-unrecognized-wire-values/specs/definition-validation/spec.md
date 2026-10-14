## ADDED Requirements

### Requirement: A deadline flow must name a flow of its definition
`model.Validate` SHALL refuse with `ErrInvalidDeadlineFlow` a node whose non-empty deadline flow matches, compared exactly, the ID of no flow in the definition level that holds the node, and SHALL accept a node whose deadline flow names such a flow or is empty.

#### Scenario: A deadline flow that resolves to no flow is refused
```gherkin
Scenario Outline: A dangling deadline flow fails validation instead of failing at breach time
  Given a definition whose userTask "t" has a deadline timer and deadline flow "<ref>"
  And the definition level holding "t" has flows <flows>
  When the definition is validated
  Then validation fails with ErrInvalidDeadlineFlow
  And the error names the node "t" and quotes "<ref>"

  Examples:
    | ref    | flows                                       |
    | NOSUCH | "f1" and "f2"                               |
    | F2     | "f1" and "f2"                               |
    | f2     | "f1" and "f2 "                              |
    | f2     | "f1", with "f2" only in the outer definition |
```

#### Scenario: A deadline flow that resolves is accepted
```gherkin
Scenario Outline: The legal neighbours validate
  Given a definition whose userTask "t" has <deadline>
  When the definition is validated
  Then validation succeeds

  Examples:
    | deadline                                                     |
    | a deadline timer and deadline flow "f2" leaving "t"          |
    | a deadline timer, deadline action "esc" and no deadline flow |
    | no deadline at all                                           |
```

#### Scenario: Every authoring path reaches the rule
```gherkin
Scenario Outline: A dangling deadline flow is refused however the definition was authored
  Given a definition whose userTask has deadline flow "NOSUCH", authored <how>
  When it is <checked>
  Then it fails with an error wrapping ErrInvalidDeadlineFlow

  Examples:
    | how                                          | checked                                  |
    | in Go with activity.WithWaitDeadline         | built                                    |
    | as JSON                                      | decoded and validated                    |
    | as YAML                                      | parsed with ParseYAML and built          |
    | as JSON, one subprocess level down           | decoded and validated                    |
```

#### Scenario: Publishing refuses before any I/O on every store
```gherkin
Scenario Outline: A publish or registration of a dangling deadline flow writes nothing
  Given a definition whose userTask has deadline flow "NOSUCH"
  When it is <published>
  Then it fails with an error wrapping ErrInvalidDeadlineFlow
  And no definition row exists for it afterwards

  Examples:
    | published                                         |
    | registered with MemDefinitionRegistry.Register    |
    | published to a Postgres definition store          |
    | published to a MySQL definition store             |
    | published to a SQLite definition store            |
```
