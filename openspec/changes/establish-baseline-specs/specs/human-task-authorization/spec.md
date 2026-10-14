## Purpose

Defines who may claim, complete or refresh a human task, how the HTTP task routes establish the acting actor, how the library's composite authorizer evaluates a task's eligibility spec, and how a spec or authorizer it cannot evaluate is refused rather than silently permitted.

## ADDED Requirements

### Requirement: Task operations are refused for an actor the task's eligibility does not admit
A claim, completion or candidate refresh by an actor the task's eligibility spec does not admit MUST be refused with `authz.ErrNotAuthorized`, and the refusal MUST leave the task and the instance unchanged.

#### Scenario: Ineligible actor is refused on each operation
```gherkin
Scenario Outline: An actor outside the eligibility spec is refused
  Given an open human task whose eligibility spec the actor "bob" does not satisfy
  When "bob" attempts to <operation> the task
  Then the attempt fails with an error wrapping authz.ErrNotAuthorized

  Examples:
    | operation           |
    | claim               |
    | complete            |
    | refresh candidates  |
```

#### Scenario: A refused operation changes nothing
```gherkin
Scenario Outline: The refusal leaves the task and the instance as they were
  Given an open human task whose eligibility spec the actor "bob" does not satisfy
  When "bob" attempts to <operation> the task and is refused
  Then the task's state, claim and candidates are unchanged
  And the instance's state is unchanged

  Examples:
    | operation           |
    | claim               |
    | complete            |
    | refresh candidates  |
```

#### Scenario: Eligible actor is not refused
```gherkin
Scenario Outline: An actor the spec admits proceeds
  Given an open human task whose eligibility spec the actor "alice" satisfies under the default composite authorizer
  When "alice" attempts to <operation> the task
  Then the attempt is not refused

  Examples:
    | operation           |
    | claim               |
    | complete            |
    | refresh candidates  |
```

### Requirement: Attribute predicates see the task's variable snapshot
An eligibility attribute predicate MUST be evaluated against the process variables snapshotted on the task when it was created, so a predicate reading `vars[...]` admits or refuses by those values.

#### Scenario: Predicate over a task variable decides the claim
```gherkin
Scenario Outline: vars["region"] gates the claim
  Given an open human task with eligibility predicate vars["region"] == "EU"
  And the task's variable snapshot has region = <region>
  When an actor claims the task
  Then the claim <outcome>

  Examples:
    | region | outcome                                 |
    | "EU"   | succeeds                                |
    | "US"   | fails with authz.ErrNotAuthorized       |
```

### Requirement: Task routes take the actor from the configured identity resolver only
The HTTP task routes MUST act as the actor returned by the configured request-actor resolver and SHALL ignore any actor carried in the request body, on the stdlib, gin and fiber adapters alike.

#### Scenario: A body-supplied actor cannot promote the caller
```gherkin
Scenario Outline: Forged body actor is ignored
  Given the <adapter> routes are mounted and the resolver authenticates "bob" holding no eligibility for the task
  And the claim request body names actor "alice" who is eligible
  When "bob" posts to /tasks/{token}/claim
  Then the response status is 403

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: The forged-actor refusal carries the forbidden code
```gherkin
Scenario Outline: 403 envelope code
  Given the <adapter> routes are mounted and the resolver authenticates "bob" holding no eligibility for the task
  And the claim request body names actor "alice" who is eligible
  When "bob" posts to /tasks/{token}/claim
  Then the error code is "forbidden"

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: An eligible resolved actor claims successfully
```gherkin
Scenario Outline: Eligible resolved actor is admitted
  Given the <adapter> routes are mounted and the resolver authenticates an eligible actor
  When the actor posts to /tasks/{token}/claim with no body
  Then the response status is 200

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

### Requirement: Task routes fail closed when no identity can be established
The HTTP task routes MUST refuse a request with 401 `unauthenticated` when no actor is resolved, and with 503 `identity_unavailable` when the resolver itself fails, never proceeding as the zero actor.

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
    | resolver behaviour                       | result                                        |
    | is not configured                        | 401 unauthenticated                           |
    | returns httpcore.ErrUnauthenticated      | 401 unauthenticated                           |
    | returns the zero actor with a nil error  | 401 unauthenticated                           |
    | returns any other error                  | 503 identity_unavailable, no message rendered |
    | is called with a cancelled context       | 503 identity_unavailable                      |
    | returns an actor with an ID              | the actor is used                             |
```

### Requirement: Identity is refused before the request body is read
On the claim, complete and reassign task routes an unauthenticated request MUST be refused with 401 before its body is read, so the body's size or validity SHALL NOT change the answer and an unauthenticated caller learns nothing about whether its body parsed.

#### Scenario: Body problems do not pre-empt the 401
```gherkin
Scenario Outline: unauthenticated request with a bad body
  Given the <adapter> adapter mounted with no identity configured
  When an unauthenticated caller sends POST /tasks/{token}/<operation> with <body>
  Then the response status is 401

  Examples:
    | adapter | operation | body                                  |
    | stdlib  | claim     | a body that fails on the first read   |
    | stdlib  | complete  | a body that fails on the first read   |
    | stdlib  | reassign  | a body that fails on the first read   |
    | gin     | claim     | a JSON string where an object belongs |
    | gin     | complete  | a JSON string where an object belongs |
    | gin     | reassign  | a JSON string where an object belongs |
    | fiber   | claim     | a body larger than the configured cap |
    | fiber   | complete  | a body larger than the configured cap |
    | fiber   | reassign  | a body larger than the configured cap |
```

#### Scenario: The pre-empting 401 carries the unauthenticated code
```gherkin
Scenario Outline: 401 envelope code with a bad body
  Given the <adapter> adapter mounted with no identity configured
  When an unauthenticated caller sends POST /tasks/{token}/claim with <body>
  Then the error code is "unauthenticated"

  Examples:
    | adapter | body                                  |
    | stdlib  | a body that fails on the first read   |
    | gin     | a JSON string where an object belongs |
    | fiber   | a body larger than the configured cap |
```

#### Scenario: Negative control - an authenticated oversize body is still 413
```gherkin
Scenario: Identity resolved, body over the cap
  Given the stdlib adapter with a body cap and an authenticated caller
  When the caller claims a task with a body larger than the cap
  Then the response status is 413
```

### Requirement: Privilege eligibility matches by exact string
The default composite authorizer (`authz.NewComposite`) SHALL admit an actor against a privileges-only spec only when one of the spec's privileges equals, by exact string comparison, one of the actor's granted privileges; it MUST NOT expand wildcards or hierarchies, and a role MUST NOT satisfy a privilege.

#### Scenario: Privilege matching
```gherkin
Scenario Outline: Privileges-only spec under the default composite authorizer
  Given an eligibility spec requiring privileges ["finance-task claim"]
  And an actor granted privileges <granted> whose role is <role>
  When the default composite authorizer evaluates the request
  Then the result is <result>

  Examples:
    | granted                                  | role                 | result                     |
    | ["finance-task claim"]                   | none                 | allowed                    |
    | ["other", "finance-task claim"]          | none                 | allowed                    |
    | []                                       | none                 | authz.ErrNotAuthorized     |
    | ["finance-task *"]                       | none                 | authz.ErrNotAuthorized     |
    | []                                       | "finance-task claim" | authz.ErrNotAuthorized     |
```

#### Scenario: No prefix hierarchy
```gherkin
Scenario: A shorter privilege does not imply a longer one
  Given an eligibility spec requiring privileges ["a b"]
  And an actor granted privileges ["a"]
  When the default composite authorizer evaluates the request
  Then the request fails with authz.ErrNotAuthorized
```

### Requirement: An empty eligibility spec admits any actor
The default composite authorizer SHALL allow every actor when the eligibility spec sets no roles, no privileges and no attribute predicate.

#### Scenario: Empty spec allows
```gherkin
Scenario: No eligibility fields set
  Given an eligibility spec with no roles, no privileges and no attribute predicate
  When the default composite authorizer evaluates a request from actor "u1"
  Then the request is allowed
```

#### Scenario: A non-empty spec is still evaluated
```gherkin
Scenario: Setting a field removes the allow-all
  Given an eligibility spec requiring privilege "finance-task claim"
  When the default composite authorizer evaluates a request from actor "u1" holding no privileges
  Then the request fails with authz.ErrNotAuthorized
```

### Requirement: The attribute predicate constrains every identity decision
When an eligibility spec carries an attribute predicate, the default composite authorizer MUST refuse with `authz.ErrNotAuthorized` whenever the predicate evaluates false, even if the actor satisfies the spec's identity field.

#### Scenario: Identity and constraint combine
```gherkin
Scenario Outline: Predicate applied after identity
  Given an eligibility spec whose identity field the actor satisfies
  And the spec carries attribute predicate <predicate>
  When the default composite authorizer evaluates the request
  Then the result is <result>

  Examples:
    | predicate                           | result                 |
    | actor.ID == "someone-else"          | authz.ErrNotAuthorized |
    | vars["amount"] < 1000 (amount = 10) | allowed                |
```

### Requirement: A denial carries no policy detail
A refusal by the composite authorizer MUST be exactly `authz.ErrNotAuthorized` with nothing wrapped around it, so the 403 response body names no role, privilege, predicate or decider.

#### Scenario: Refusal is the bare sentinel
```gherkin
Scenario: Denied request returns the bare sentinel
  Given an eligibility spec requiring privilege "finance-task claim"
  And an actor holding only privilege "other"
  When the default composite authorizer evaluates the request
  Then the returned error is identical to authz.ErrNotAuthorized
```

#### Scenario: An allowed request returns no error
```gherkin
Scenario: Matching privilege is not refused
  Given an eligibility spec requiring privilege "finance-task claim"
  And an actor holding privilege "finance-task claim"
  When the default composite authorizer evaluates the request
  Then no error is returned
```

### Requirement: A predicate that cannot be evaluated is a failure, not a denial
The composite authorizer MUST refuse a request whose attribute predicate does not compile or does not yield a boolean with an error naming the attribute predicate that does not satisfy `authz.ErrNotAuthorized`, and the HTTP adapters SHALL answer such a failure with 500 and no message, never 403 carrying the predicate text.

#### Scenario: Unevaluable predicates fail closed as errors
```gherkin
Scenario Outline: Broken predicate
  Given an eligibility spec whose attribute predicate is <predicate>
  When the default composite authorizer evaluates a request
  Then the request is refused with an error naming the attribute predicate
  And errors.Is(err, authz.ErrNotAuthorized) is false

  Examples:
    | predicate                |
    | actor.ID =====           |
    | a non-boolean expression |
```

#### Scenario: The HTTP response hides the predicate
```gherkin
Scenario: Unevaluable predicate classifies 500
  Given an authorizer error from an unevaluable predicate whose text embeds the predicate source
  When the HTTP error classifier renders it
  Then the status is 500
  And the response message does not contain the predicate source
```

#### Scenario: A predicate evaluating false is still a denial
```gherkin
Scenario: Evaluable false predicate
  Given an eligibility spec whose attribute predicate is actor.ID == "someone-else"
  When the default composite authorizer evaluates a request from actor "u1"
  Then the error is authz.ErrNotAuthorized
```

### Requirement: A spec field no decider evaluates is refused as misconfiguration
A composite authorizer MUST refuse a request whose eligibility spec sets a field that none of its installed deciders evaluates, with `authz.ErrSpecNotEvaluable` naming the field, and that error MUST NOT satisfy `authz.ErrNotAuthorized`.

#### Scenario: Coverage is checked before deciding
```gherkin
Scenario Outline: Composite missing the decider for a set field
  Given a composite authorizer holding <installed>
  And an eligibility spec that sets <field>
  And an actor satisfying every field an installed decider evaluates
  When the composite evaluates the request
  Then the result is <result>

  Examples:
    | installed                          | field                 | result                                           |
    | only an attribute decider          | privileges            | authz.ErrSpecNotEvaluable naming "privileges"    |
    | only a privilege decider           | privileges            | allowed                                          |
    | only privilege and role deciders   | roles and attribute   | authz.ErrSpecNotEvaluable naming "attribute"     |
    | the default composite              | roles and attribute   | allowed                                          |
    | only a privilege decider           | roles                 | authz.ErrSpecNotEvaluable naming "roles"         |
    | only privilege and role deciders   | roles                 | allowed                                          |
    | only a privilege decider           | nothing               | allowed                                          |
```

#### Scenario: The default composite covers every field
```gherkin
Scenario: Default composite never reports a coverage gap
  Given the default composite authorizer
  And an eligibility spec setting privileges and an attribute predicate
  When the composite evaluates the request
  Then the error, if any, is not authz.ErrSpecNotEvaluable
```

### Requirement: An empty or broken composite fails closed as misconfiguration
A composite authorizer holding no deciders MUST refuse every request with `authz.ErrNoDeciders`, and one holding a nil decider MUST refuse with `authz.ErrNilDecider`; neither error satisfies `authz.ErrNotAuthorized`.

#### Scenario: Misconfigured composites
```gherkin
Scenario Outline: Configuration errors are not denials
  Given <composite>
  And an eligibility spec that sets <spec>
  When the composite evaluates the request
  Then the error is <error>
  And errors.Is(err, authz.ErrNotAuthorized) is false

  Examples:
    | composite                               | spec            | error                |
    | the zero composite                      | nothing         | authz.ErrNoDeciders  |
    | a composite with a nil decider element  | roles ["admin"] | authz.ErrNilDecider  |
    | a composite with a nil decider element  | nothing         | authz.ErrNilDecider  |
```

#### Scenario: A constraint-only composite is legitimate for a constraint-only spec
```gherkin
Scenario: Partial composite that covers the spec
  Given a composite holding only an attribute decider
  And an eligibility spec with attribute predicate actor.ID == "u1"
  When actor "u1" is evaluated
  Then the request is allowed
```

### Requirement: A user task may not declare both eligible roles and eligible privileges
Definition validation MUST refuse a user task that declares both eligible roles and eligible privileges with `model.ErrRolesAndPrivileges`, naming the node, and SHALL accept either field alone or alongside an eligibility predicate.

#### Scenario: Identity fields on a user task
```gherkin
Scenario Outline: Validating eligibility declarations
  Given a user task "approve" declaring <declaration>
  When the definition is validated
  Then the result is <result>

  Examples:
    | declaration                                      | result                                                  |
    | eligible roles and eligible privileges            | model.ErrRolesAndPrivileges, message names "approve"   |
    | eligible roles only                               | valid                                                   |
    | eligible privileges only                          | valid                                                   |
    | eligible privileges and an eligibility predicate  | valid                                                   |
    | no eligibility at all                             | valid                                                   |
```
