## ADDED Requirements

<!-- PENDING: the first and third requirements below state the RECOMMENDED option of design Open Question Q1 (keep the unbounded history default). They are rewritten if the maintainer chooses a default cap. The second requirement holds under either option. -->

### Requirement: Without a history cap the stored snapshot keeps the full inline history
On a SQL instance store opened without a history cap, or with a history cap of zero or less, the stored instance snapshot SHALL keep every node visit of the instance, open and closed, in its original order.

#### Scenario: Unset and non-positive caps keep every visit
```gherkin
Scenario Outline: history cap <setting>
  Given a SQL instance store <setting>
  And an instance whose history is closed c1, closed c2, closed c3, closed c4, open o1 in order
  When a step for that instance is committed and the instance is loaded
  Then the loaded history is <kept> in order

  Examples:
    | setting                          | kept                   |
    | opened without a history cap     | c1, c2, c3, c4, o1     |
    | configured with a history cap of 0  | c1, c2, c3, c4, o1  |
    | configured with a history cap of -1 | c1, c2, c3, c4, o1  |
    | configured with a history cap of 5  | c1, c2, c3, c4, o1  |
    | configured with a history cap of 4  | c1, c2, c3, c4, o1  |
    | configured with a history cap of 3  | c2, c3, c4, o1      |
    | configured with a history cap of 1  | c4, o1              |
```

#### Scenario: A long uncapped history survives reopening the store on each dialect
```gherkin
Scenario Outline: reload after reopen
  Given a <dialect> instance store opened without a history cap
  And an instance that has committed 500 steps, each closing one node visit
  When the store is closed, opened again on the same database, and the instance is loaded
  Then the loaded history holds all 500 closed visits in their original order

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: A history cap never truncates the instance's journal
A SQL instance store SHALL record every applied step in the instance's journal whatever history cap it is configured with, so `Entries` returns one trigger per successful `Create` and `Commit` even when the stored snapshot's history has been capped.

#### Scenario: Journal length is independent of the cap
```gherkin
Scenario Outline: Entries on <dialect> with <setting>
  Given a <dialect> instance store <setting>
  When instance "i1" is created and 5 steps are committed, each closing one node visit
  Then the loaded history of "i1" holds <closed> closed visits
  And Entries for "i1" returns 6 triggers

  Examples:
    | dialect  | setting                             | closed |
    | Postgres | configured with a history cap of 1  | 1      |
    | MySQL    | configured with a history cap of 1  | 1      |
    | SQLite   | configured with a history cap of 1  | 1      |
    | Postgres | opened without a history cap        | 5      |
    | MySQL    | opened without a history cap        | 5      |
    | SQLite   | opened without a history cap        | 5      |
```

### Requirement: An unset history cap is reported at start-up
`persistence.WarnUnsafeConfig` SHALL log `persistence.WarnMsgHistoryCap` exactly once at warning level when the `DeploymentProfile` it is given reports that no history cap is set, and SHALL NOT log it when the profile reports that one is set.

#### Scenario: Warning follows the declared profile
```gherkin
Scenario Outline: HistoryCapSet is <set>
  Given a logger that records every entry
  When WarnUnsafeConfig is called with that logger and a DeploymentProfile whose HistoryCapSet is <set>
  Then the logger holds <count> warning-level entries whose message is WarnMsgHistoryCap

  Examples:
    | set   | count |
    | false | 1     |
    | true  | 0     |
```

#### Scenario: A nil logger still reports the unset cap
```gherkin
Scenario: nil logger falls back to the default logger
  Given the default slog logger records every entry
  When WarnUnsafeConfig is called with a nil logger and a DeploymentProfile whose HistoryCapSet is false
  Then the call does not panic
  And the default logger holds 1 warning-level entry whose message is WarnMsgHistoryCap
```
