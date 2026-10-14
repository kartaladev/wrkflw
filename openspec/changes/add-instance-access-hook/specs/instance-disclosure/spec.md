## MODIFIED Requirements

### Requirement: A caller the resolver does not identify reads the projection
The disclosure decision MUST treat a caller as unidentified, and project the state, whenever the configured request-actor resolver is absent, fails, reports no credential, or returns the zero actor, and it MUST NOT turn any of these into a refusal. A read SHALL be refused only by a configured operation authorizer, never by the disclosure decision.

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

#### Scenario: Only the operation authorizer refuses a read
```gherkin
Scenario Outline: Who may refuse GET /instances/{id} (operation authorizer pending Q6)
  Given the stdlib routes are mounted with a resolver that <resolver behaviour>
  And the engine is configured with <authorizer>
  When a client calls GET /instances/{id} for an existing instance
  Then the response status is <status>

  Examples:
    | resolver behaviour                  | authorizer                                       | status |
    | returns httpcore.ErrUnauthenticated | no operation authorizer                          | 200    |
    | returns any other error             | no operation authorizer                          | 200    |
    | returns an actor with ID "u1"       | no operation authorizer                          | 200    |
    | returns httpcore.ErrUnauthenticated | an operation authorizer refusing instance.read   | 403    |
    | returns an actor with ID "u1"       | an operation authorizer refusing instance.read   | 403    |
    | returns an actor with ID "u1"       | an operation authorizer allowing instance.read   | 200    |
```
