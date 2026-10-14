## MODIFIED Requirements

### Requirement: A businessRuleTask cannot carry both a rule and an action
`model.Validate` SHALL refuse a businessRuleTask carrying both a `rule` and an `action` with `ErrRuleAndAction`, and SHALL accept one carrying an action and no rule, a well-shaped rule and no action, or neither. A well-shaped rule SHALL NOT be refused merely for being present.

#### Scenario: Rule and action together are refused
```gherkin
Scenario Outline: Rule and action on one node fail with ErrRuleAndAction
  Given a rule engine is registered
  And a definition whose businessRuleTask "score" carries <rule> and action "risk-score"
  When the definition is validated
  Then validation fails with ErrRuleAndAction
  And the error names the node "score"

  Examples:
    | rule                                   |
    | rule "pricing.v3"                      |
    | an inline rule document {"stages":[]}  |
```

#### Scenario: A businessRuleTask without a rule validates
```gherkin
Scenario Outline: A businessRuleTask without a rule passes
  Given a definition whose businessRuleTask "score" carries <config>
  When the definition is validated
  Then validation succeeds

  Examples:
    | config                    |
    | action "risk-score"       |
    | neither rule nor action   |
```

#### Scenario: A businessRuleTask with a rule and no action validates
```gherkin
Scenario Outline: A well-shaped rule alone passes once a rule engine is registered
  Given a rule engine is registered
  And a definition whose businessRuleTask "score" carries <rule> and no action
  When the definition is validated
  Then validation succeeds

  Examples:
    | rule                                   |
    | rule "pricing.v3"                      |
    | rule "a"                               |
    | rule "定价.v3"                          |
    | an inline rule document {"stages":[]}  |
    | an inline rule document {}             |
```

#### Scenario: A malformed rule is reported as malformed, not as a conflict
```gherkin
Scenario Outline: The shape check precedes the rule-and-action check
  Given a rule engine is registered
  And a definition whose businessRuleTask "score" carries <rule> set through the Go builder and <action>
  When the definition is validated
  Then validation fails with ErrInvalidRule
  And the error does not match ErrRuleAndAction

  Examples:
    | rule                                    | action              |
    | a blank catalog name "   "              | action "risk-score" |
    | an inline document that is an array     | action "risk-score" |
    | an inline document that is an array     | no action           |
```
