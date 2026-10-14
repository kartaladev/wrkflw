## ADDED Requirements

### Requirement: The on-behalf principal and the ownership rule follow their categories
Projection MUST withhold the on-behalf principal of every recorded claim and completion actor from an unidentified caller unless the disclosure set includes actors, and MUST withhold a task's claimant-only rule and reassign eligibility unless it includes policy; neither category SHALL restore the other's fields.

#### Scenario: Principal and ownership rule under each category
```gherkin
Scenario Outline: Projecting a delegated claim on a claimant-only task
  Given an instance whose claimant-only task with reassign eligibility roles ["supervisor"] was claimed by "a1" on behalf of "m1"
  When it is projected for an unidentified caller under the disclosure set <set>
  Then the claim's on-behalf principal is <principal>
  And the task's claimant-only rule and reassign eligibility are <rule>

  Examples:
    | set               | principal         | rule        |
    | {}                | absent            | absent      |
    | {actors}          | "m1"              | absent      |
    | {policy}          | absent            | restored    |
    | {actors, policy}  | "m1"              | restored    |
    | {notes}           | absent            | absent      |
```

#### Scenario: Negative control - an identified caller sees both
```gherkin
Scenario: Full fidelity for an identified caller
  Given the same instance and a resolver that identifies the caller as "u1"
  When the instance is read
  Then the claim's on-behalf principal is "m1"
  And the task's reassign eligibility roles are ["supervisor"]
```
