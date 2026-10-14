## Purpose

Defines what a businessRuleTask carrying a `rule` does at run time: it evaluates a rule through an opt-in rule engine instead of invoking an action, merges the decision output into process variables, and is refused by name, never run as a service task, when its rule cannot be compiled or resolved.

## ADDED Requirements

### Requirement: A businessRuleTask with a rule evaluates the rule instead of an action
When a token enters a businessRuleTask that carries a `rule`, the engine SHALL evaluate that rule against the instance's process variables and SHALL NOT invoke any action, including an action registered under the node's id; the token SHALL leave the node by its outgoing flow once the evaluation completes.

#### Scenario: A named or inline rule is evaluated and no action runs
```gherkin
Scenario Outline: The rule decides, the action catalog is not consulted
  Given a rule engine is registered
  And the action catalog holds an action named "score" that records each invocation
  And a definition whose businessRuleTask "score" carries <rule> and no action
  And <rule> yields {"tier":"gold"} for variables {"amount":250}
  When an instance starts with variables {"amount":250}
  Then the instance completes
  And its variables are {"amount":250,"tier":"gold"}
  And the action "score" was invoked 0 times

  Examples:
    | rule                                                   |
    | rule "pricing.v3" resolved by the rule catalog         |
    | an inline rule document compiled by the rule engine    |
```

#### Scenario: A duplicate completion of an evaluation is refused like an action's
```gherkin
Given a rule engine is registered
And an instance whose businessRuleTask "score" has completed its rule evaluation
When the same evaluation's completion is delivered again
Then the step fails with ErrTokenNotFound
And the instance's variables are unchanged
```

#### Scenario: A restart before the result is committed re-evaluates once
```gherkin
Given a rule engine is registered
And an instance whose businessRuleTask "score" was entered but whose evaluation result was never committed
When the driver restarts and recovers the instance
Then the rule is evaluated again against the same variables
And the decision output is merged exactly once
And the instance completes
```

### Requirement: A businessRuleTask without a rule keeps invoking its action
A businessRuleTask that carries no `rule` SHALL invoke its `action` exactly as a serviceTask does, the action name defaulting to the node id when `action` is unset, whether or not a rule engine is registered.

#### Scenario: The pre-rule behavior is unchanged
```gherkin
Scenario Outline: A rule-less businessRuleTask runs its action
  Given <engine>
  And a definition whose businessRuleTask "score" carries <config> and no rule
  When an instance starts
  Then the action <invoked> is invoked once
  And no rule is evaluated

  Examples:
    | engine                          | config              | invoked      |
    | a rule engine is registered     | action "risk-score" | "risk-score" |
    | a rule engine is registered     | neither             | "score"      |
    | no rule engine is registered    | action "risk-score" | "risk-score" |
    | no rule engine is registered    | neither             | "score"      |
```

### Requirement: Decision output is merged into process variables as action output is
The output of a rule evaluation SHALL be merged into the instance's process variables by the same rule that merges an action's output: each output key replaces the variable of that exact name, every other variable is kept, and an empty output leaves the variables unchanged.

#### Scenario: Output keys replace, other variables stay
```gherkin
Scenario Outline: The merge is key by key and case-exact
  Given a rule engine is registered
  And a businessRuleTask whose rule yields <output>
  When an instance starts with variables {"amount":100,"tier":"silver"}
  Then the instance's variables are <after>

  Examples:
    | output                       | after                                               |
    | {"tier":"gold"}              | {"amount":100,"tier":"gold"}                        |
    | {}                           | {"amount":100,"tier":"silver"}                      |
    | {"Tier":"gold"}              | {"amount":100,"tier":"silver","Tier":"gold"}        |
    | {"discount":0}               | {"amount":100,"tier":"silver","discount":0}         |
    | {"tier":null}                | {"amount":100,"tier":null}                          |
```

#### Scenario: Merged output survives a snapshot round trip on every store
```gherkin
Scenario Outline: The merged variables reload unchanged
  Given a rule engine is registered
  And a <backend> instance store
  And a businessRuleTask followed by a userTask, whose rule yields {"tier":"gold","limit":5000}
  When an instance starts and parks on the userTask
  And the instance is reloaded from the store
  Then its variables contain "tier" = "gold" and "limit" = 5000

  Examples:
    | backend  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: A decimal in decision output is stored as its exact decimal string
A decimal value in a rule's output SHALL enter process variables as a string holding its exact decimal representation, so it survives a snapshot reload without precision loss. Pending: design OQ5 decides this representation.

#### Scenario: Decimal values keep every digit
```gherkin
Scenario Outline: A decimal output is its exact string
  Given a rule engine is registered
  And a businessRuleTask whose rule yields "fee" as the decimal <value>
  When an instance starts and its variables are reloaded from the store
  Then the variable "fee" is the string <stored>

  Examples:
    | value                        | stored                         |
    | 18125.00                     | "18125.00"                     |
    | 0.1                          | "0.1"                          |
    | 0                            | "0"                            |
    | -0.0725                      | "-0.0725"                      |
    | 123456789012345678901234.5   | "123456789012345678901234.5"   |
```

### Requirement: A failed evaluation is an action failure
When a rule evaluation returns an error, the token SHALL fail at the businessRuleTask exactly as on a non-retryable action failure: an error boundary on the node catches it, and without one the instance raises an incident; the recorded failure MUST name the node and the rule. Pending: design OQ6 decides the retry classification.

#### Scenario: An error boundary catches a failed evaluation
```gherkin
Scenario Outline: The failure routes like an action failure
  Given a rule engine is registered
  And a businessRuleTask "score" whose rule fails on variables {"amount":-1}
  And <boundary>
  When an instance starts with variables {"amount":-1}
  Then <outcome>
  And the rule was evaluated exactly once

  Examples:
    | boundary                                   | outcome                                                   |
    | an error boundary on "score"               | the token leaves by the boundary's flow                   |
    | no boundary on "score"                     | the instance raises an incident naming "score"            |
    | a retry policy of 3 attempts on "score"    | the instance raises an incident naming "score"            |
```

#### Scenario: A successful evaluation does not route through the boundary
```gherkin
Given a rule engine is registered
And a businessRuleTask "score" with an error boundary, whose rule yields {"tier":"gold"} on variables {"amount":5}
When an instance starts with variables {"amount":5}
Then the token leaves "score" by its outgoing flow
And the boundary's flow is not taken
```

### Requirement: A rule name the catalog cannot resolve fails the token by name
When a businessRuleTask's rule names an entry the rule catalog does not resolve, evaluation SHALL fail the token non-retryably with `rule.ErrUnknownRule` naming the rule, catchable by an error boundary; names SHALL be compared exactly. Pending: design OQ3 decides this rule and the sentinel.

#### Scenario: Unknown and near-miss names fail
```gherkin
Scenario Outline: Only an exact name resolves
  Given a rule engine is registered
  And the rule catalog holds only "pricing.v3"
  And a businessRuleTask "score" whose rule is <name>, with an error boundary
  When an instance starts
  Then <outcome>

  Examples:
    | name           | outcome                                                                  |
    | "pricing.v3"   | the rule is evaluated and the token leaves by its outgoing flow          |
    | "pricing.v4"   | the token leaves by the boundary, the failure matching rule.ErrUnknownRule |
    | "Pricing.v3"   | the token leaves by the boundary, the failure matching rule.ErrUnknownRule |
    | "pricing.v3 "  | the token leaves by the boundary, the failure matching rule.ErrUnknownRule |
```

### Requirement: Build checks rule names against a rule catalog it is given
When the definition loader or builder is given a rule catalog, `Build` SHALL refuse a definition whose businessRuleTask names a rule that catalog does not resolve, with `rule.ErrUnknownRule` naming the node and the rule; without a rule catalog, `Build` SHALL NOT check names. Pending: design OQ3 decides this rule.

#### Scenario: A given catalog makes Build check names
```gherkin
Scenario Outline: Names are checked only against a catalog Build was given
  Given a rule engine is registered
  And a definition whose businessRuleTask "score" names rule <name>
  And the loader is given <catalog>
  When Build is called
  Then <outcome>

  Examples:
    | name          | catalog                              | outcome                                                    |
    | "pricing.v3"  | a rule catalog holding "pricing.v3"  | Build succeeds                                             |
    | "pricing.v4"  | a rule catalog holding "pricing.v3"  | Build fails with rule.ErrUnknownRule naming "score"        |
    | "pricing.v4"  | no rule catalog                      | Build succeeds                                             |
```

### Requirement: A rule-bearing definition is refused when no rule engine is registered
`Build`, registration and `PublishDefinition` SHALL refuse a definition carrying a non-empty `rule` with `model.ErrNoRuleEngine`, naming the node, when no rule engine is registered, and SHALL NOT write a row or claim a key; a definition with no rule SHALL be unaffected. Pending: design OQ2 decides this rule and the sentinel.

#### Scenario: No engine, no rule-bearing definition
```gherkin
Scenario Outline: The refusal reaches every authoring door
  Given no rule engine is registered
  And a definition whose businessRuleTask "score" carries rule "pricing.v3" and no action
  When the definition goes through <door>
  Then it fails with model.ErrNoRuleEngine naming "score"
  And nothing is stored or registered

  Examples:
    | door                                  |
    | Build on the builder                  |
    | Build on a ParseYAML loader           |
    | MemDefinitionRegistry.Register        |
    | PublishDefinition on Postgres         |
    | PublishDefinition on MySQL            |
    | PublishDefinition on SQLite           |
```

#### Scenario: Without a rule, no engine is needed
```gherkin
Given no rule engine is registered
And a definition whose only business rule task carries action "risk-score" and no rule
When Build is called
Then Build succeeds
```

### Requirement: An inline rule that does not compile is refused at Build
`Build` and `PublishDefinition` SHALL refuse a definition whose inline rule document the registered rule engine cannot compile, with `rule.ErrRuleCompile` naming the node and wrapping the engine's own error, and SHALL NOT write a row. Pending: design OQ2 decides where compilation happens and the sentinel.

#### Scenario: A broken inline document is refused, a sound one builds
```gherkin
Scenario Outline: Compilation is the gate for inline rules
  Given a rule engine is registered
  And a definition whose businessRuleTask "score" carries the inline rule <document>
  When Build is called
  Then <outcome>

  Examples:
    | document                                              | outcome                                              |
    | a well-formed single-stage pipeline                   | Build succeeds                                       |
    | a pipeline whose expression does not parse            | Build fails with rule.ErrRuleCompile naming "score"  |
    | a pipeline whose stages depend on each other in a cycle | Build fails with rule.ErrRuleCompile naming "score" |
```

### Requirement: An inline rule is compiled once per definition version
An inline rule document SHALL be compiled at most once per process for each published definition id and version and reused by every instance of that version, including instances started concurrently; a different version SHALL be compiled separately.

#### Scenario: Instances of one version share one compilation
```gherkin
Scenario Outline: The compile count follows versions, not instances
  Given a rule engine that counts its compilations is registered
  And definition "p" whose businessRuleTask carries an inline rule
  When <instances>
  Then the engine has compiled <count> rule documents

  Examples:
    | instances                                                 | count |
    | 1 instance of version 1 runs                              | 1     |
    | 50 instances of version 1 start concurrently              | 1     |
    | 50 instances of version 1 and 1 of version 2 run          | 2     |
```

#### Scenario: A recovered instance evaluates its own version's rule
```gherkin
Given a rule engine is registered
And definition "p" version 1 whose inline rule yields {"tier":"silver"} and version 2 whose inline rule yields {"tier":"gold"}
And an instance of version 1 parked before its businessRuleTask
When the driver restarts and the instance proceeds
Then its variable "tier" is "silver"
```

### Requirement: The root module never requires the rule engine
The non-test module graph of wrkflw's root module SHALL NOT contain the rule engine or any module only the rule engine requires; only a consumer that imports the rule-engine adapter module SHALL receive them.

#### Scenario: The engine arrives only through the adapter module
```gherkin
Scenario Outline: The rule engine is opt-in
  Given a consumer module that imports <packages>
  When its non-test module graph is listed
  Then the graph <presence> github.com/kartaladev/rlng

  Examples:
    | packages                                              | presence         |
    | the root module's engine, runtime and definition      | does not contain |
    | the root module's rule port package                   | does not contain |
    | the rule-engine adapter module                        | contains         |
```

### Requirement: An rlng pipeline can be registered as an action
The rule-engine adapter module SHALL offer a compiled rlng pipeline as an `action.Action` that works with `action.Typed`, so a serviceTask, or a businessRuleTask without a rule, can invoke it by name with its output merged as action output.

#### Scenario: A pipeline runs as a catalog action
```gherkin
Scenario Outline: The action adapter evaluates the pipeline
  Given a pipeline that yields {"tier":"gold"} for {"amount":250} is registered as action "pricing.decide"
  And a definition whose <kind> "price" carries action "pricing.decide"
  When an instance starts with variables {"amount":250}
  Then its variables are {"amount":250,"tier":"gold"}

  Examples:
    | kind             |
    | serviceTask      |
    | businessRuleTask |
```
