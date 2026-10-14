## ADDED Requirements

### Requirement: No instance route renders an actor's granted privileges
Every route that renders instance or task state MUST omit the granted privileges of every actor it renders, on candidates, claims and completions, for every caller and under every disclosure set including `authz.DiscloseAll`, while the categories that restore an actor SHALL still restore its ID, roles and attributes.

#### Scenario: Privileges stay withheld under every category
```gherkin
Scenario Outline: Projecting a task whose actors hold privileges
  Given an instance with a claimed and completed task whose claim actor, completion actor and candidate "u1" each hold roles ["fin"] and privileges ["payroll-export", "admin-console"]
  When it is projected under the disclosure set <set>
  Then the projected tasks contain neither "payroll-export" nor "admin-console"

  Examples:
    | set                                                  |
    | {}                                                   |
    | {actors}                                             |
    | {actors, notes}                                      |
    | {actors, policy}                                     |
    | {variables, actors, notes, operations, policy}       |
```

#### Scenario: Identified callers and DiscloseAll do not receive privileges either
```gherkin
Scenario Outline: No route leaks a privilege to any caller
  Given the <adapter> routes are mounted with <disclosure>
  And an instance whose claimed task's actor and candidate hold privilege "payroll-export" in the stored state
  When <caller> calls <route>
  Then the response status is 200
  And the response body does not contain "payroll-export"

  Examples:
    | adapter | disclosure                                  | caller                        | route                          |
    | stdlib  | the disclosure set {actors} and no resolver | an unauthenticated client     | GET /instances/{id}            |
    | stdlib  | a resolver returning actor "u1"             | the identified client "u1"    | GET /instances/{id}            |
    | stdlib  | a resolver returning actor "u1"             | the identified client "u1"    | GET /instances/{id}/snapshot   |
    | stdlib  | a resolver returning actor "u1"             | the identified client "u1"    | GET /instances/{id}/actionable |
    | stdlib  | the disclosure set {all}                    | an unauthenticated client     | GET /instances/{id}            |
    | gin     | a resolver returning actor "u1"             | the identified client "u1"    | GET /instances/{id}            |
    | gin     | the disclosure set {actors} and no resolver | an unauthenticated client     | GET /instances/{id}/actionable |
    | fiber   | a resolver returning actor "u1"             | the identified client "u1"    | GET /instances/{id}            |
    | fiber   | the disclosure set {actors} and no resolver | an unauthenticated client     | GET /instances/{id}/snapshot   |
```

#### Scenario: Negative control, actor identity still crosses under actors
```gherkin
Scenario: The privilege strip does not withhold the rest of the actor
  Given an instance with a claimed task whose claim actor "u1" holds roles ["fin"], attribute "email" = "u1@example.test" and privileges ["payroll-export"]
  When it is projected under the disclosure set {actors}
  Then the projected claim actor has ID "u1", roles ["fin"] and attribute "email" = "u1@example.test"
  And the projected claim actor has no privileges
```

#### Scenario: The strip leaves the source state intact
```gherkin
Scenario: Rendering does not mutate the input's privileges
  Given an instance state whose claim actor holds privileges ["payroll-export"]
  When it is rendered for an identified caller
  Then the input's claim actor still holds privileges ["payroll-export"]
```

### Requirement: Identity-varying responses are marked uncacheable
(Pending Q4.) Every response written by a route that renders instance state through the disclosure decision MUST carry `Cache-Control: private, no-store`, whatever the status and whether or not the caller was identified, identically on the stdlib, gin and fiber adapters.

#### Scenario: Every disclosing route marks its response
```gherkin
Scenario Outline: Cache directive on identity-varying routes
  Given the <adapter> routes are mounted with the instance, task and admin route groups
  And a resolver that returns an actor with ID "u1" eligible for the task
  When the client calls <route> for an existing instance
  Then the response header "Cache-Control" is "private, no-store"

  Examples:
    | adapter | route                                                      |
    | stdlib  | POST /instances                                            |
    | stdlib  | GET /instances/{id}                                        |
    | stdlib  | GET /instances/{id}/snapshot                               |
    | stdlib  | GET /instances/{id}/actionable                             |
    | stdlib  | POST /instances/{id}/signals                               |
    | stdlib  | POST /tasks/{token}/claim                                  |
    | stdlib  | POST /tasks/{token}/complete                               |
    | stdlib  | POST /tasks/{token}/reassign                               |
    | stdlib  | POST /admin/instances/{id}/incidents/{incidentID}/resolve  |
    | stdlib  | POST /admin/instances/{id}/compensation/resolve-stall      |
    | stdlib  | POST /admin/instances/{id}/cancel                          |
    | gin     | GET /instances/{id}                                        |
    | gin     | POST /tasks/{token}/claim                                  |
    | gin     | POST /admin/instances/{id}/cancel                          |
    | fiber   | GET /instances/{id}                                        |
    | fiber   | POST /tasks/{token}/claim                                  |
    | fiber   | POST /admin/instances/{id}/cancel                          |
```

#### Scenario: Error and unidentified responses are marked too
```gherkin
Scenario Outline: The directive does not depend on outcome or identity
  Given the <adapter> routes are mounted with a resolver that <resolver behaviour>
  And an instance with ID "i-1" exists and no instance "missing" exists
  When the client calls GET /instances/<id>
  Then the response status is <status>
  And the response header "Cache-Control" is "private, no-store"

  Examples:
    | adapter | resolver behaviour                  | id      | status |
    | stdlib  | returns httpcore.ErrUnauthenticated | i-1     | 200    |
    | stdlib  | returns an actor with ID "u1"       | missing | 404    |
    | gin     | returns httpcore.ErrUnauthenticated | missing | 404    |
    | fiber   | returns httpcore.ErrUnauthenticated | missing | 404    |
```

#### Scenario: Negative control, the directive does not change the disclosure decision
```gherkin
Scenario Outline: Bodies are what they were before the directive
  Given the stdlib routes are mounted with a resolver that <resolver behaviour> and the empty disclosure set
  And an instance whose variables hold "ssn" = "111-22-3333"
  When the client calls GET /instances/{id}
  Then the response status is 200
  And the response body <contains> "111-22-3333"

  Examples:
    | resolver behaviour                  | contains         |
    | returns an actor with ID "u1"       | contains         |
    | returns httpcore.ErrUnauthenticated | does not contain |
```
