## ADDED Requirements

### Requirement: A variable's name never changes which function a condition calls
A name in call position in a flow condition or a boundary error expression MUST resolve to the expression language's builtin of that name whatever the instance variables hold, so a variable named after a builtin MUST NOT change what the expression computes.

#### Scenario: A variable named after a builtin does not shadow it
```gherkin
Scenario Outline: A variable named after a builtin does not shadow it
  Given an exclusive gateway whose conditional flow to "hit" is <condition>, with a default flow to "miss"
  When an instance is driven with the variables <vars> plus <planted>
  Then the token takes the flow to "hit"

  Examples:
    | condition             | vars                        | planted                  |
    | len(xs) == 3          | {"xs": ["a", "bb", "ccc"]}  | nothing                  |
    | len(xs) == 3          | {"xs": ["a", "bb", "ccc"]}  | "len" = "PWNED"          |
    | upper(name) == "ANA"  | {"name": "ana"}             | "upper" = "PWNED"        |
    | trim(name) == "ana"   | {"name": " ana "}           | "trim" = "PWNED"         |
    | max(a, b) == 2        | {"a": 1, "b": 2}            | "max" = 0                |
    | abs(n) == 1           | {"n": -1}                   | "abs" = "PWNED"          |
```

#### Scenario: No builtin is shadowable in call or operator position
```gherkin
Given every builtin the pinned expression language version defines
When each is evaluated in call position and in operator position, once without and once with a variable of the builtin's name set to "PWNED"
Then each pair yields the same outcome, counting a value, a compile error and a run error as outcomes
And this holds when the planted evaluation runs first and when it runs second
```

#### Scenario: The condition still reads the variables it names
```gherkin
Scenario Outline: The condition still reads the variables it names
  Given an exclusive gateway whose conditional flow to "hit" is len(xs) == 3, with a default flow to "miss"
  When an instance is driven with the variables <vars>
  Then the token takes the flow to <target>

  Examples:
    | vars                           | target |
    | {"xs": ["a", "bb", "ccc"]}     | "hit"  |
    | {"xs": ["a", "bb"]}            | "miss" |
    | {"xs": []}                     | "miss" |
```

### Requirement: A condition's result depends only on the variables of that evaluation
Evaluating the same expression text MUST yield the result that its current variables determine, regardless of which variables, key sets or value types earlier evaluations of the same text received in the same process, under the default evaluator and under `WithExpressionTimeout`.

#### Scenario: Evaluation order between instances does not change routing
```gherkin
Scenario Outline: Evaluation order between instances does not change routing
  Given a ProcessDriver built with <option>
  And an exclusive gateway whose conditional flow to "hit" is <condition>, with a default flow to "miss"
  When instance "a" is driven with <vars-a> and then instance "b" with <vars-b>
  And in a fresh ProcessDriver instance "b" is driven first and then instance "a"
  Then in both runs "a" takes <target-a> and "b" takes <target-b>

  Examples:
    | option                        | condition    | vars-a                     | vars-b                                       | target-a | target-b |
    | no expression option          | x == 1       | {"x": 1}                   | {"x": "1"}                                   | "hit"    | "miss"   |
    | no expression option          | x == 1       | {"x": 2, "extra": true}    | {"x": 1}                                     | "miss"   | "hit"    |
    | no expression option          | len(xs) == 3 | {"xs": ["a", "bb", "ccc"]} | {"xs": ["a", "bb", "ccc"], "len": "PWNED"}   | "hit"    | "hit"    |
    | WithExpressionTimeout(1s)     | x == 1       | {"x": 1}                   | {"x": 2}                                     | "hit"    | "miss"   |
```
