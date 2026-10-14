## Purpose

Defines what instance and task data the HTTP instance routes show a caller the transport could not identify, and how a deployment's disclosure set widens that projection category by category.

## ADDED Requirements

### Requirement: Unidentified callers receive a structural projection of instance state
Under the default empty disclosure set, every instance route that renders instance state MUST withhold process variables and every snapshot of them, actor identities, completion notes, eligibility specs, incidents and the compensation cursor from an unidentified caller, while keeping the instance ID, status, timestamps, history, and token and task identity and state.

#### Scenario: No route leaks a planted secret
```gherkin
Scenario Outline: Unidentified read of an instance carrying a secret
  Given the <adapter> routes are mounted with no request-actor resolver
  And an instance whose variables hold "111-22-3333" and whose task was claimed by an actor with ID "111-22-3333"
  When an unauthenticated client calls <route>
  Then the status is below 500
  And the response body does not contain "111-22-3333"
  And a 200 response body still contains the instance ID

  Examples:
    | adapter | route                              |
    | stdlib  | GET /instances/{id}                |
    | stdlib  | GET /instances/{id}/snapshot       |
    | stdlib  | GET /instances/{id}/actionable     |
    | stdlib  | POST /instances/{id}/signals       |
    | gin     | GET /instances/{id}                |
    | gin     | GET /instances/{id}/snapshot       |
    | gin     | GET /instances/{id}/actionable     |
    | gin     | POST /instances/{id}/signals       |
    | fiber   | GET /instances/{id}                |
    | fiber   | GET /instances/{id}/snapshot       |
    | fiber   | GET /instances/{id}/actionable     |
    | fiber   | POST /instances/{id}/signals       |
```

#### Scenario: Structural fields survive the projection
```gherkin
Scenario: Projection keeps the skeleton
  Given an instance state with variables, a claimed and completed task, incidents and compensation records
  When it is projected under the empty disclosure set
  Then the instance ID, definition ID and status are unchanged
  And the token and task counts are unchanged
  And each task keeps its task ID and state
  And no variable value, actor, note or incident error appears in the projection
```

### Requirement: A caller the resolver does not identify reads the projection
The disclosure decision MUST treat a caller as unidentified, and project the state, whenever the configured request-actor resolver is absent, fails, reports no credential, or returns the zero actor, and it MUST NOT turn any of these into a refusal.

#### Scenario: Every unidentified shape is projected
```gherkin
Scenario Outline: Resolver outcomes that leave the caller unidentified
  Given a request-actor resolver that <resolver behaviour>
  And the empty disclosure set
  When the instance view is rendered for the request
  Then the rendered view carries no variables
  And the instance ID is still present

  Examples:
    | resolver behaviour                                   |
    | is not configured                                    |
    | returns httpcore.ErrUnauthenticated                  |
    | returns any other error                              |
    | returns the zero actor with a nil error              |
```

#### Scenario: The projection is real, not vacuous
```gherkin
Scenario: A widened set restores what the projection removed
  Given a resolver that returns httpcore.ErrUnauthenticated
  And the disclosure set {variables}
  When the instance view is rendered for the request
  Then the rendered variables include "ssn" = "111-22-3333"
```

### Requirement: Each disclosure category widens only its own fields
A disclosure set SHALL restore to unidentified callers exactly the fields of the categories it names: variables restores every variable snapshot, actors restores the actor IDs on claims and candidates without the note, notes restores the completion note without its actor, operations restores incident IDs and the compensation cursor without error text or record inputs, and policy restores eligibility without variables.

#### Scenario: Categories are independent
```gherkin
Scenario Outline: Projecting under one or two categories
  Given an instance state carrying the secret "111-22-3333" in variables, the task snapshot, the claim actor, the candidate actors, the completion note, the incident error and compensation inputs
  When it is projected under the disclosure set <set>
  Then <restored>
  And <withheld>

  Examples:
    | set               | restored                                                      | withheld                                                                       |
    | {actors}          | the claim actor ID is restored                                | the completion note is empty                                                   |
    | {actors}          | each candidate actor ID is restored                           | the completion note is empty                                                   |
    | {actors, notes}   | the completion note is restored                               | nothing named is withheld                                                      |
    | {variables}       | instance variables and the task variable snapshot are restored | the task claim is absent                                                       |
    | {notes}           | the completion note is restored                               | the completing actor ID is empty and the claim is absent                       |
    | {operations}      | incident ID "inc1" and the active compensation command ID are restored | incident error text, compensation record inputs, final error text and variables are empty |
    | {policy}          | the task eligibility spec is restored                         | variables are absent                                                           |
```

#### Scenario: The empty set discloses nothing beyond the skeleton
```gherkin
Scenario: Zero-value and explicitly empty sets are the same closed posture
  Given the zero-value disclosure set and a set built from no categories
  When each is asked whether any category may be disclosed
  Then every answer is no
```

### Requirement: The embedded definition is withheld from unidentified callers unless policy is disclosed
The instance routes MUST withhold the embedded process definition from an unidentified caller unless the disclosure set includes policy.

#### Scenario: Definition embed
```gherkin
Scenario Outline: Whether the definition is withheld
  Given a resolver that returns httpcore.ErrUnauthenticated
  And the disclosure set <set>
  When the transport decides whether to withhold the definition
  Then the definition is <decision>

  Examples:
    | set      | decision |
    | {}       | withheld |
    | {policy} | included |
```

### Requirement: Identity is resolved once per request
Each instance read SHALL invoke the configured request-actor resolver exactly once per request, so the state projection and the definition embed are decided from the same answer.

#### Scenario: One resolution per read
```gherkin
Scenario Outline: Counting resolver calls
  Given the stdlib routes are mounted with a resolver that counts its calls
  When a client calls <route>
  Then the status is 200
  And the resolver was called exactly once

  Examples:
    | route                          |
    | GET /instances/{id}            |
    | GET /instances/{id}/snapshot   |
    | GET /instances/{id}/actionable |
```

### Requirement: Projection never alters the source state
Projecting an instance state for disclosure MUST NOT mutate the state it was given, whatever categories are disclosed.

#### Scenario: Source state is untouched
```gherkin
Scenario Outline: Projecting leaves the input intact
  Given an instance state carrying variables, a task variable snapshot, a claim and a completion note
  When it is projected under <set>
  Then the input's variables, task snapshot, claim actor and completion note are unchanged

  Examples:
    | set                  |
    | {}                   |
    | {actors}             |
    | {actors, variables}  |
```
