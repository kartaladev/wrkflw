## ADDED Requirements

### Requirement: An invalid actor classifies as a bad request
The HTTP error classifier SHALL render an error matching `authz.ErrInvalidActor` as status 400 with code `bad_request` and the error's text, identically on the stdlib, gin and fiber adapters, and MUST NOT classify it as 401 or 403.

#### Scenario: Classification of the actor validation error
```gherkin
Scenario Outline: ErrInvalidActor maps to 400
  Given an error matching <error>
  When it is classified for an HTTP response
  Then the status is <status>
  And the error code is <code>

  Examples:
    | error                                    | status | code         |
    | authz.ErrInvalidActor                    | 400    | bad_request  |
    | authz.ErrInvalidActor wrapped with %w    | 400    | bad_request  |
    | authz.ErrNotAuthorized                   | 403    | forbidden    |
```
