## MODIFIED Requirements

### Requirement: A spec field no decider evaluates is refused as misconfiguration
A composite authorizer MUST refuse a request whose eligibility spec sets any field the spec declares, not a listed subset of them, that none of its installed deciders evaluates, with `authz.ErrSpecNotEvaluable` naming the field, and that error MUST NOT satisfy `authz.ErrNotAuthorized`. Every field of `authz.AuthzSpec` SHALL be named by exactly one `authz.SpecField`.

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

#### Scenario: Every spec field is required when set
```gherkin
Scenario Outline: A composite whose only decider reads no spec field
  Given a composite holding one custom decider that allows every request and <declares>
  And an eligibility spec that sets only <field> to <value>
  When the composite evaluates the request
  Then the result is <result>
  And when the result is an error, errors.Is(err, authz.ErrNotAuthorized) is false

  Examples:
    | declares                              | field      | value                    | result                                         |
    | declares that it reads no spec field  | roles      | ["manager"]              | authz.ErrSpecNotEvaluable naming "roles"       |
    | declares that it reads no spec field  | privileges | ["finance-task claim"]   | authz.ErrSpecNotEvaluable naming "privileges"  |
    | declares that it reads no spec field  | attribute  | actor.ID == "u1"         | authz.ErrSpecNotEvaluable naming "attribute"   |
    | declares that it reads no spec field  | attribute  | " " (one space)          | authz.ErrSpecNotEvaluable naming "attribute"   |
    | does not declare what it reads        | privileges | ["finance-task claim"]   | authz.ErrSpecNotEvaluable naming "privileges"  |
    | declares that it reads no spec field  | roles      | [] (empty, not absent)   | allowed                                        |
    | declares that it reads no spec field  | attribute  | "" (empty)               | allowed                                        |
    | declares that it reads no spec field  | nothing    | the zero spec            | allowed                                        |
```

#### Scenario: The spec-field vocabulary names every spec field
```gherkin
Scenario: AuthzSpec and SpecField agree
  Given the exported fields of authz.AuthzSpec
  And the authz.SpecField constants
  When the two sets are compared
  Then every AuthzSpec field is named by exactly one SpecField constant
  And every SpecField constant names an AuthzSpec field
  And a spec setting several fields with no covering decider reports every missing field, sorted, in one authz.ErrSpecNotEvaluable
```
