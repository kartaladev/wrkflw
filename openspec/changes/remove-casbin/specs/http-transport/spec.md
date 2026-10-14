## ADDED Requirements

### Requirement: The library mounts no policy administration routes
`AdminRoutes` MUST NOT register any route that reads or edits authorization policy or role bindings on any adapter, because the library stores no policy; such a request SHALL answer 404 while the other admin routes stay mounted.

#### Scenario: Policy routes are absent
```gherkin
Scenario Outline: Retired policy routes answer 404
  Given AdminRoutes is mounted on the <adapter> adapter with every optional dependency set
  When a caller sends <method> <path>
  Then the response status is 404

  Examples:
    | adapter | method | path                  |
    | stdlib  | GET    | /admin/policies       |
    | stdlib  | POST   | /admin/policies       |
    | stdlib  | DELETE | /admin/policies       |
    | stdlib  | GET    | /admin/role-bindings  |
    | stdlib  | POST   | /admin/role-bindings  |
    | stdlib  | DELETE | /admin/role-bindings  |
    | gin     | GET    | /admin/policies       |
    | gin     | DELETE | /admin/role-bindings  |
    | fiber   | GET    | /admin/policies       |
    | fiber   | DELETE | /admin/role-bindings  |
```

#### Scenario: Negative control - the remaining admin routes answer
```gherkin
Scenario Outline: Admin group still mounted
  Given AdminRoutes is mounted on the <adapter> adapter with every optional dependency set
  When a caller sends <method> <path>
  Then the response status is <status>

  Examples:
    | adapter | method | path                 | status |
    | stdlib  | GET    | /admin/instances     | 200    |
    | gin     | GET    | /admin/dead-letters  | 200    |
    | fiber   | GET    | /admin/timers        | 200    |
```
