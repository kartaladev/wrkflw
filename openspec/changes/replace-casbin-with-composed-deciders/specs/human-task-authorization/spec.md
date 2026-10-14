## MODIFIED Requirements

### Requirement: Task routes fail closed when no identity can be established
The HTTP task routes MUST refuse a request with 401 `unauthenticated` when no actor is resolved, and with 503 `identity_unavailable` when the resolver itself fails, never proceeding as the zero actor; a resolved actor that fails actor validation MUST be refused with 400 `bad_request` before authorization.

#### Scenario: Unauthenticated claim is refused identically on every adapter
```gherkin
Scenario Outline: No resolved identity yields 401
  Given the <adapter> routes are mounted with no request-actor resolver and no authenticating middleware
  When a client posts to /tasks/{token}/claim with <body>
  Then the response status is 401
  And the error code is "unauthenticated"

  Examples:
    | adapter | body                                  |
    | stdlib  | no body                               |
    | gin     | no body                               |
    | fiber   | no body                               |
    | stdlib  | a body naming an eligible actor       |
    | gin     | a body naming an eligible actor       |
    | fiber   | a body naming an eligible actor       |
```

#### Scenario: Resolver outcomes map to distinct refusals
```gherkin
Scenario Outline: The resolver's answer decides the refusal
  Given a request-actor resolver that <resolver behaviour>
  When the task route resolves the caller
  Then the result is <result>

  Examples:
    | resolver behaviour                                   | result                                        |
    | is not configured                                    | 401 unauthenticated                           |
    | returns httpcore.ErrUnauthenticated                  | 401 unauthenticated                           |
    | returns the zero actor with a nil error              | 401 unauthenticated                           |
    | returns any other error                              | 503 identity_unavailable, no message rendered |
    | is called with a cancelled context                   | 503 identity_unavailable                      |
    | returns an actor with an ID                          | the actor is used                             |
    | returns an actor with role "kiosk" and no ID         | 400 bad_request (pending Q1)                  |
    | returns an actor acting on behalf of itself          | 400 bad_request                               |
```

#### Scenario: An invalid resolved actor is refused identically on every adapter
```gherkin
Scenario Outline: Actor validation refusal parity
  Given the <adapter> routes are mounted with a resolver returning actor "a1" on behalf of a principal with no ID
  When the caller posts to /tasks/{token}/<operation>
  Then the response status is 400
  And the error code is "bad_request"
  And the task and the instance are unchanged

  Examples:
    | adapter | operation |
    | stdlib  | claim     |
    | gin     | claim     |
    | fiber   | claim     |
    | stdlib  | complete  |
    | gin     | complete  |
    | fiber   | complete  |
    | stdlib  | reassign  |
    | gin     | reassign  |
    | fiber   | reassign  |
```

## ADDED Requirements

### Requirement: An actor that fails validation is refused before authorization
A claim, completion, reassignment or candidate refresh MUST be refused with `authz.ErrInvalidActor`, leaving the task and instance unchanged, when the acting actor has an empty ID, or its on-behalf principal has an empty ID, equals the actor's ID, or itself names a principal. The refusal MUST precede authorization and SHALL apply whether the trigger came from the task service or was built by hand and applied to the driver.

#### Scenario: Actor validation rules
```gherkin
Scenario Outline: Validating the acting actor
  Given an open human task whose eligibility spec is empty
  When an actor <actor> attempts to <operation> the task
  Then the result is <result>

  Examples:
    | actor                                                        | operation          | result                                 |
    | with ID ""                                                   | claim              | authz.ErrInvalidActor                  |
    | with ID "" and role "kiosk"                                  | complete           | authz.ErrInvalidActor (pending Q1)     |
    | "a1" on behalf of a principal with ID ""                     | claim              | authz.ErrInvalidActor                  |
    | "a1" on behalf of "a1"                                       | complete           | authz.ErrInvalidActor                  |
    | "a1" on behalf of "m1" who is on behalf of "x1"              | reassign           | authz.ErrInvalidActor                  |
    | with ID ""                                                   | refresh candidates | authz.ErrInvalidActor                  |
    | "a1" with no role, no privileges and no principal            | claim              | not refused                            |
    | "a1" on behalf of "m1"                                       | complete           | not refused                            |
    | "a1" on behalf of "A1"                                       | claim              | not refused (IDs compare exactly)      |
```

#### Scenario: Validation outranks the eligibility refusal
```gherkin
Scenario: An invalid and ineligible actor sees the validation error
  Given an open human task whose eligibility spec requires role "approver"
  When an actor with ID "" and role "clerk" attempts to claim the task
  Then the error is authz.ErrInvalidActor
  And errors.Is(err, authz.ErrNotAuthorized) is false
```

#### Scenario: The driver refuses a hand-built trigger from an invalid actor
```gherkin
Scenario Outline: Direct ApplyTrigger with an invalid actor
  Given a running instance with an open human task at snapshot version 3
  When a <trigger> trigger whose actor is "a1" on behalf of "a1" is applied through ApplyTrigger
  Then the error is authz.ErrInvalidActor
  And the snapshot version is still 3
  And no journal row is appended

  Examples:
    | trigger         |
    | HumanClaimed    |
    | HumanCompleted  |
    | HumanReassigned |
    | HumanCandidatesResolved |
```

### Requirement: Role eligibility tests the principal's single active role
The default composite authorizer SHALL admit an actor against a roles-only spec only when the role of the actor's principal equals, by exact string comparison, one of the spec's roles; an empty role MUST NOT satisfy any spec.

#### Scenario: Role matching
```gherkin
Scenario Outline: Roles-only spec under the default composite authorizer
  Given an eligibility spec requiring roles ["approver", "clerk"]
  And an actor "u1" acting for itself whose role is <role>
  When the default composite authorizer evaluates the request
  Then the result is <result>

  Examples:
    | role              | result                 |
    | "approver"        | allowed                |
    | "clerk"           | allowed                |
    | ""                | authz.ErrNotAuthorized |
    | "Approver"        | authz.ErrNotAuthorized |
    | " approver"       | authz.ErrNotAuthorized |
    | "approver,clerk"  | authz.ErrNotAuthorized |
    | "аpprover"        | authz.ErrNotAuthorized |
```

#### Scenario: A privilege does not satisfy a role
```gherkin
Scenario: Granted privilege named like the role
  Given an eligibility spec requiring roles ["approver"]
  And an actor whose role is "" and whose privileges are ["approver"]
  When the default composite authorizer evaluates the request
  Then the request fails with authz.ErrNotAuthorized
```

### Requirement: The deciders evaluate the principal, not the caller
When an actor carries an on-behalf principal, the default composite authorizer MUST evaluate the principal's role, privileges and attributes and SHALL ignore the caller's own; when it carries none, the actor itself is the principal.

#### Scenario: Delegated eligibility
```gherkin
Scenario Outline: Assistant acting for a manager
  Given an eligibility spec <spec>
  And the actor is <actor>
  When the default composite authorizer evaluates the request
  Then the result is <result>

  Examples:
    | spec                          | actor                                                                  | result                 |
    | requiring roles ["approver"]  | "a1" role "assistant" on behalf of "m1" role "approver"                | allowed                |
    | requiring roles ["approver"]  | "a1" role "approver" on behalf of "m1" role "clerk"                    | authz.ErrNotAuthorized |
    | requiring roles ["approver"]  | "a1" role "approver" with no principal                                 | allowed                |
    | requiring privileges ["pay"]  | "a1" privileges ["pay"] on behalf of "m1" privileges []                | authz.ErrNotAuthorized |
    | requiring privileges ["pay"]  | "a1" privileges [] on behalf of "m1" privileges ["pay"]                | allowed                |
```

### Requirement: Attribute predicates bind the principal as actor and the caller as caller
An eligibility attribute predicate MUST see the principal under `actor` and the acting caller, principal included, under `caller`; a predicate naming the retired roles list MUST be refused with an error naming the attribute predicate that does not satisfy `authz.ErrNotAuthorized`.

#### Scenario: Predicate bindings
```gherkin
Scenario Outline: actor and caller in a predicate
  Given an eligibility spec with attribute predicate <predicate>
  And the actor is <actor>
  When the default composite authorizer evaluates the request
  Then the result is <result>

  Examples:
    | predicate                     | actor                          | result                                                             |
    | actor.ID == "m1"              | "a1" on behalf of "m1"         | allowed                                                            |
    | caller.ID == "a1"             | "a1" on behalf of "m1"         | allowed                                                            |
    | actor.ID == "a1"              | "a1" on behalf of "m1"         | authz.ErrNotAuthorized                                             |
    | caller.ID == actor.ID         | "u1" with no principal         | allowed                                                            |
    | actor.Role == "approver"      | "u1" role "approver"           | allowed                                                            |
    | "approver" in actor.Roles     | "u1" role "approver"           | an error naming the attribute predicate, not authz.ErrNotAuthorized |
```

### Requirement: Only the claimant may complete a claimant-only task
On a user task declaring `claimant_only`, a completion MUST be refused with `authz.ErrNotAuthorized`, leaving task and instance unchanged, unless the acting principal's ID equals the claimant's ID exactly or the task is unclaimed; a task without the rule SHALL keep the advisory claim, and the rule SHALL NOT restrict claiming.

#### Scenario: Who may complete
```gherkin
Scenario Outline: Completion under the ownership rule
  Given an eligible open user task with claimant_only <rule> that is <claim state>
  When <completer> completes the task
  Then the result is <result>

  Examples:
    | rule  | claim state                         | completer                           | result                 |
    | true  | claimed by "alice"                  | "alice"                             | completed              |
    | true  | claimed by "alice"                  | "bob", who is eligible              | authz.ErrNotAuthorized |
    | true  | claimed by "alice"                  | "Alice", who is eligible            | authz.ErrNotAuthorized |
    | true  | claimed by "m1" through "a1"        | "m1"                                | completed              |
    | true  | claimed by "m1" through "a1"        | "b1" on behalf of "m1"              | completed              |
    | true  | claimed by "n1"                     | "a1" on behalf of "m1"              | authz.ErrNotAuthorized |
    | true  | unclaimed                           | "bob", who is eligible              | completed              |
    | false | claimed by "alice"                  | "bob", who is eligible              | completed              |
```

#### Scenario: The rule does not gate claiming
```gherkin
Scenario: An eligible actor claims a claimant-only task
  Given an unclaimed open user task with claimant_only true
  When an eligible actor "bob" claims it
  Then the task is claimed by "bob"
```

#### Scenario: Eligibility still applies under the rule
```gherkin
Scenario: An ineligible actor cannot implicitly claim
  Given an unclaimed open user task with claimant_only true whose eligibility "bob" does not satisfy
  When "bob" completes the task
  Then the error is authz.ErrNotAuthorized
  And the task is still unclaimed and open
```

### Requirement: Reassignment of a claimant-only task needs the claimant or reassign eligibility
On a user task declaring `claimant_only`, a reassignment MUST be refused with `authz.ErrNotAuthorized` unless the acting principal is the current claimant or satisfies the task's reassign eligibility; without the rule, reassignment SHALL be authorized by the task's eligibility as today. The reassign eligibility shape is pending Q2.

#### Scenario: Who may reassign
```gherkin
Scenario Outline: Reassign authority
  Given an open user task claimed by "alice" with claimant_only <rule> and reassign eligibility <reassign>
  And the task's eligibility requires role "approver"
  When <actor> reassigns the task from "alice" to "dave"
  Then the result is <result>

  Examples:
    | rule  | reassign                        | actor                                    | result                 |
    | true  | none                            | "alice"                                  | reassigned to "dave"   |
    | true  | none                            | "bob" with role "approver"               | authz.ErrNotAuthorized |
    | true  | requiring roles ["supervisor"]  | "carol" with role "supervisor"           | reassigned to "dave"   |
    | true  | requiring roles ["supervisor"]  | "bob" with role "approver"               | authz.ErrNotAuthorized |
    | true  | requiring roles ["supervisor"]  | "alice"                                  | reassigned to "dave"   |
    | true  | none                            | "x1" on behalf of "alice"                | reassigned to "dave"   |
    | false | none                            | "bob" with role "approver"               | reassigned to "dave"   |
    | false | none                            | "eve" with role "clerk"                  | authz.ErrNotAuthorized |
```
