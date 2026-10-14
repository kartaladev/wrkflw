## ADDED Requirements

### Requirement: Submitting an update is authorized as progress under the ownership rule
An update submission MUST be authorized at the transition as operation `authz.OpProgress` against the task's eligibility exactly as a completion is, including the claimant-only rule; a refusal MUST fail with `authz.ErrNotAuthorized` and change nothing, and an admitted actor on an unclaimed task SHALL become its claimant.

#### Scenario: Who may submit
```gherkin
Scenario Outline: Progress follows completion's authorization
  Given an open user task "t1" whose eligibility admits "alice" and "bob" but not "eve", <claimant_only>, in state <state>
  When <actor> submits update "u"
  Then the result is <result>

  Examples:
    | claimant_only        | state                     | actor   | result                                            |
    | not claimant-only    | unclaimed                 | "alice" | accepted, "alice" becomes the claimant            |
    | not claimant-only    | unclaimed                 | "eve"   | authz.ErrNotAuthorized, task unchanged            |
    | not claimant-only    | claimed by "alice"        | "bob"   | accepted, the claim stays with "alice"            |
    | claimant-only        | unclaimed                 | "alice" | accepted, "alice" becomes the claimant            |
    | claimant-only        | claimed by "alice"        | "alice" | accepted                                          |
    | claimant-only        | claimed by "alice"        | "bob"   | authz.ErrNotAuthorized, task unchanged            |
    | claimant-only        | progressed, claimed by "alice" | "bob" | authz.ErrNotAuthorized, task unchanged         |
```

#### Scenario: The operation the authorizer sees
```gherkin
Scenario: A spy authorizer records the request
  Given a driver whose authorizer is a recording spy that allows everything
  When "alice" submits update "u" on task "t1"
  Then the spy received exactly one request, with operation "progress", actor "alice" and the eligibility of "t1" from the instance snapshot
```

#### Scenario: A hand-built HumanProgressed is gated like the other human triggers
```gherkin
Scenario: No back door around the task service
  Given a driver with the default composite authorizer and an open task "t1" whose eligibility "eve" fails
  When an engine.HumanProgressed naming "eve" is built by hand and passed to ProcessDriver.ApplyTrigger
  Then ApplyTrigger fails with an error wrapping authz.ErrNotAuthorized
  And no update row is written and the instance version is unchanged
```

#### Scenario: Two eligible actors race on an unclaimed claimant-only task
```gherkin
Scenario: The instance version admits one implicit claim
  Given an unclaimed claimant-only task "t1" admitting "alice" and "bob"
  When "alice" and "bob" submit updates concurrently on the same loaded version
  Then exactly one submission commits and its actor is the claimant
  And the other, re-applied by the driver's concurrency retry, fails with authz.ErrNotAuthorized
  And the losing actor never appears as the claimant
```
