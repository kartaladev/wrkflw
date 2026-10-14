## ADDED Requirements

### Requirement: A retried or resolved task action re-invokes the action that failed
When a userTask's entry or completion action is re-invoked, by a retry timer firing or by `ResolveIncident` on its `IncidentAction`, the engine SHALL dispatch the same action that failed, under the same idempotency key, and MUST NOT dispatch an action named after the node or any other action of the node.

#### Scenario: The re-invocation names the failed action
```gherkin
Scenario Outline: The <action> of userTask "review" is re-invoked by <route>
  Given a userTask "review" with entry_action "review.prepare", completion_action "review.finalize" and a retry policy of 3 attempts
  And the <action> failed <failure>
  When <route> re-invokes it
  Then exactly one InvokeAction is dispatched and it names "<name>"
  And its idempotency key equals the key of the failed attempt
  And no InvokeAction names "review"

  Examples:
    | action            | failure                                      | route                      | name            |
    | entry action      | retryably on attempt 1                       | the retry timer firing     | review.prepare  |
    | completion action | retryably on attempt 1                       | the retry timer firing     | review.finalize |
    | entry action      | retryably on attempts 1 to 3, raising an IncidentAction | ResolveIncident | review.prepare  |
    | completion action | retryably on attempts 1 to 3, raising an IncidentAction | ResolveIncident | review.finalize |
```

#### Scenario: A service task's retry still re-invokes its own action
```gherkin
Given a serviceTask "charge" with action "payments.charge" and a retry policy of 3 attempts
And its action failed retryably on attempt 1
When the retry timer fires
Then exactly one InvokeAction is dispatched and it names "payments.charge"
```

#### Scenario: A duplicate retry fire dispatches nothing
```gherkin
Given the entry action of userTask "review" failed retryably and its retry timer fired once
When the same retry timer fires again
Then no InvokeAction is dispatched
And the instance state is unchanged
```
