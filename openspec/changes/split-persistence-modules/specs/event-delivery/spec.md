## MODIFIED Requirements

### Requirement: Postgres can wake the relay by notification; other dialects poll
With `persistence.WithOutboxNotify` on a Postgres store and `pgx.WithListenNotify` on a relay built from a `pgx.FromPool` connection, the relay SHALL drain a newly committed outbox row before its poll interval elapses; relays on MySQL, SQLite and Postgres over `database/sql` MUST drain by polling.

#### Scenario: Postgres drains on notification before the poll interval
```gherkin
Given a Postgres store from a pgx.FromPool connection with outbox notification enabled
And a relay on that connection listening for notifications with a 30 second poll interval
When a step commits an outbox row
Then the relay publishes it well before 30 seconds have elapsed
```

#### Scenario: MySQL and SQLite still drain by polling
```gherkin
Scenario Outline: Poll-only drain on <dialect>
  Given a <dialect> outbox holding 1 pending row
  And the <dialect> relay with a short poll interval
  When Run is started
  Then the row is published

  Examples:
    | dialect |
    | MySQL   |
    | SQLite  |
```

#### Scenario: Postgres over database/sql drains by polling
```gherkin
Given a Postgres store from a sql.FromDB connection with outbox notification enabled
And a relay on that connection with a short poll interval
When a step commits an outbox row
Then the relay publishes it by polling
```

#### Scenario: Negative control, a pgx relay without listening still drains by polling
```gherkin
Given a Postgres store from a pgx.FromPool connection with outbox notification enabled
And a relay on that connection with a short poll interval and without pgx.WithListenNotify
When a step commits an outbox row
Then the relay publishes it by polling
```
