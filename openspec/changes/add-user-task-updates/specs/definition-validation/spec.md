## ADDED Requirements

### Requirement: Malformed user-task update declarations are refused at Build
Building a definition MUST refuse updates on a manual user task with `model.ErrManualTaskUpdates`, two updates sharing a name with `model.ErrDuplicateUpdateName`, a dependency naming no update of the same task with `model.ErrUpdateDependencyUnknown`, a dependency cycle with `model.ErrUpdateDependencyCycle`, and a resubmit policy other than `replace` or `refuse` with `model.ErrUpdateResubmitPolicy`.

#### Scenario: Each defect has its sentinel
```gherkin
Scenario Outline: A broken update declaration fails Build
  Given a definition whose user task "review" declares <declaration>
  When the definition is built
  Then Build fails with <error>
  And the error names node "review" and <named>

  Examples:
    | declaration                                                         | error                            | named                  |
    | manual: true and update "a"                                         | model.ErrManualTaskUpdates       | update "a"             |
    | updates "a" and "a"                                                 | model.ErrDuplicateUpdateName     | update "a"             |
    | update "b" depending on ["z"]                                       | model.ErrUpdateDependencyUnknown | updates "b" and "z"    |
    | update "a" depending on ["a"]                                       | model.ErrUpdateDependencyCycle   | update "a"             |
    | "a" depending on ["b"] and "b" depending on ["a"]                   | model.ErrUpdateDependencyCycle   | updates "a" and "b"    |
    | "a" -> ["b"], "b" -> ["c"], "c" -> ["a"]                            | model.ErrUpdateDependencyCycle   | updates "a", "b", "c"  |
    | update "a" with resubmit "Replace"                                  | model.ErrUpdateResubmitPolicy    | "Replace"              |
    | update "a" with resubmit "merge"                                    | model.ErrUpdateResubmitPolicy    | "merge"                |
    | update "a" with resubmit " refuse"                                  | model.ErrUpdateResubmitPolicy    | " refuse"              |
```

#### Scenario: A blank update name is refused
```gherkin
Scenario Outline: An update must be addressable
  Given a definition whose user task "review" declares an update named <name>
  When the definition is built
  Then Build fails with an error naming node "review" and the blank update name

  Examples:
    | name  |
    | ""    |
    | "   " |
```

#### Scenario: Negative control - legal declarations build
```gherkin
Scenario Outline: A sound update declaration passes Build
  Given a definition whose user task "review" declares <declaration>
  When the definition is built
  Then Build succeeds

  Examples:
    | declaration                                                                 |
    | no updates                                                                  |
    | one update "a" with no validation, no action and no resubmit                |
    | "a", "b" depending on ["a"], "c" depending on ["a", "b"]                    |
    | update "a" with resubmit "replace" and update "b" with resubmit "refuse"   |
    | update "a" with required false and a json-schema validation                 |
    | manual: false with updates, alongside a completion validation               |
```

#### Scenario: Unregistered validation kinds on an update fail Build as on the completion slot
```gherkin
Scenario: The update slot uses the same validation registry
  Given a definition whose user task declares update "a" with a validation descriptor of an unregistered kind
  When the definition is built
  Then Build fails as it does for the same descriptor in the task's completion validation slot
```

#### Scenario: A large dependency graph is checked without exponential cost
```gherkin
Scenario: Cycle detection is linear in the declaration
  Given a user task declaring 1000 updates, each depending on the one before it
  When the definition is built
  Then Build succeeds
  And adding a dependency from the first update to the last makes Build fail with model.ErrUpdateDependencyCycle
```
