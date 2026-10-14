## ADDED Requirements

### Requirement: An engine with no configured authorizer enforces task eligibility
(Pending Q1.) A ProcessEngine built without an authorizer and without `service.WithProcessDriver` MUST authorize human-task operations with the default composite authorizer (`authz.NewComposite()`) on the driver it builds, on the in-memory and durable graphs alike. Only `authz.AllowAll{}` passed explicitly SHALL admit every actor.

#### Scenario: Unconfigured engines refuse an actor the eligibility does not admit
```gherkin
Scenario Outline: Default authorizer on the facade
  Given a ProcessEngine built <wiring>
  And an open human task whose eligibility is role "manager"
  When actor "bob" with role "viewer" attempts to <operation> the task
  Then the attempt fails with an error wrapping authz.ErrNotAuthorized
  And the task's state and claim are unchanged

  Examples:
    | wiring                                                   | operation |
    | with no authorizer option                                | claim     |
    | with no authorizer option                                | complete  |
    | with service.WithHumanTasks(store, nil)                  | claim     |
    | with service.WithDurableStore and no authorizer option   | claim     |
```

#### Scenario: Negative control, the default still admits eligible actors and empty specs
```gherkin
Scenario Outline: What the default composite admits
  Given a ProcessEngine built with no authorizer option
  And an open human task whose eligibility is <spec>
  When actor <actor> claims the task
  Then the claim succeeds

  Examples:
    | spec                                          | actor                                  |
    | role "manager"                                | "alice" with role "manager"            |
    | privileges ["finance-task claim"]             | "carol" granted "finance-task claim"   |
    | empty (no roles, no privileges, no predicate) | "bob" with role "viewer"               |
    | empty (no roles, no privileges, no predicate) | "kiosk" with no ID and role "kiosk"    |
```

#### Scenario: AllowAll is the explicit opt-out
```gherkin
Scenario: An engine opted into allow-all admits the ineligible actor
  Given a ProcessEngine built with service.WithHumanTasks(store, authz.AllowAll{})
  And an open human task whose eligibility is role "manager"
  When actor "bob" with role "viewer" claims the task
  Then the claim succeeds
```

#### Scenario: A predicate the default cannot evaluate fails closed on the facade
```gherkin
Scenario: Unconfigured engine with a broken eligibility predicate
  Given a ProcessEngine built with no authorizer option
  And an open human task whose eligibility predicate does not compile
  When actor "alice" claims the task
  Then the claim is refused with an error naming the attribute predicate
  And errors.Is(err, authz.ErrNotAuthorized) is false
```
