## MODIFIED Requirements

### Requirement: A predicate that cannot be evaluated is a failure, not a denial
The composite authorizer MUST refuse a request whose attribute predicate does not compile or does not yield a boolean with `authz.ErrPredicateNotEvaluable`, in an error naming the attribute predicate that does not satisfy `authz.ErrNotAuthorized`, and the HTTP adapters SHALL answer such a failure with 500 and no message, never 403 carrying the predicate text.

#### Scenario: Unevaluable predicates fail closed as errors
```gherkin
Scenario Outline: Broken predicate
  Given an eligibility spec whose attribute predicate is <predicate>
  When the default composite authorizer evaluates a request
  Then the request is refused with authz.ErrPredicateNotEvaluable
  And the error names the attribute predicate
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
  And the error matches authz.ErrPredicateNotEvaluable
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
  And the error does not match authz.ErrPredicateNotEvaluable
```
