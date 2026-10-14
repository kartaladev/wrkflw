# Spec Delta

## ADDED Requirements

### Requirement: Armed timer rows store their kinds by name
A SQL store SHALL write an armed timer's kind and its trigger kind as their stable names, never as ordinals, and listing the armed timers SHALL return the kind it wrote, identically on Postgres, MySQL and SQLite.

#### Scenario: Armed timer rows
```gherkin
Scenario Outline: a <timer_kind> timer with a <trigger> trigger on <dialect>
  Given a <dialect> store opened after migration
  When a step arms a <timer_kind> timer whose trigger is <trigger>
  Then the raw kind column of the timer row holds the text "<timer_kind>"
  And the raw trigger_kind column holds the text "<trigger_name>"
  And listing the armed timers returns that timer with kind <timer_kind>

  Examples:
    | dialect  | timer_kind             | trigger                    | trigger_name |
    | Postgres | TimerIntermediate      | a one-time duration of 1h  | onetime      |
    | MySQL    | TimerIntermediate      | a one-time duration of 1h  | onetime      |
    | SQLite   | TimerIntermediate      | a one-time duration of 1h  | onetime      |
    | Postgres | TimerDeadline          | cron "0 9 * * *"           | cron         |
    | MySQL    | TimerDeadline          | cron "0 9 * * *"           | cron         |
    | SQLite   | TimerDeadline          | cron "0 9 * * *"           | cron         |
    | Postgres | TimerCompensationRetry | a one-time duration of 30s | onetime      |
    | MySQL    | TimerCompensationRetry | a one-time duration of 30s | onetime      |
    | SQLite   | TimerCompensationRetry | a one-time duration of 30s | onetime      |
```

### Requirement: An armed timer row whose kind is outside the vocabulary is refused
Listing the armed timers MUST fail with the timer-kind unknown-value sentinel when a stored row's kind is not one of the timer-kind names, including an integer written by an earlier build, and MUST NOT substitute a default kind.

#### Scenario: Out-of-band timer rows
```gherkin
Scenario Outline: <read> over a row whose <field> holds <stored> on <dialect>
  Given a <dialect> store holding a row written by the library
  And the row's <field> is overwritten out of band with <stored>
  When <read> is called for that row
  Then the result is <result>

  Examples:
    | dialect  | read                     | field                          | stored          | result                                                                  |
    | Postgres | listing the armed timers | timer kind column              | "TimerBogus"    | an error matching engine.ErrUnknownTimerKind (pending Q1)               |
    | MySQL    | listing the armed timers | timer kind column              | "2"             | an error matching engine.ErrUnknownTimerKind (pending Q1)               |
    | SQLite   | listing the armed timers | timer kind column              | "TimerDeadline" | the timer, with kind TimerDeadline                                      |
    | SQLite   | listing the armed timers | timer kind column              | "timerdeadline" | an error matching engine.ErrUnknownTimerKind (pending Q1)               |
    | Postgres | listing the armed timers | timer kind column              | "TimerRetry"    | the timer, with kind TimerRetry                                         |
    | MySQL    | listing the armed timers | timer kind column              | "TimerInWait"   | the timer, with kind TimerInWait                                        |
```

### Requirement: Timer pruning selects rows by trigger kind name
`Pruner.PruneTimers` SHALL delete only expired rows whose trigger kind is unset, one-time or a one-shot expression, excluding compensation-retry rows, and `ReclaimNeverDueTimers` SHALL delete only sub-epoch rows whose trigger kind is recurring, on every SQL dialect.

#### Scenario: Which rows each sweep removes
```gherkin
Scenario Outline: a <timer_kind> row with trigger <trigger> and next run <next_run> on <dialect>
  Given a <dialect> pruner over a store holding that row
  When PruneTimers runs with cutoff 2026-01-01 and then ReclaimNeverDueTimers runs
  Then the row is <outcome>

  Examples:
    | dialect  | timer_kind             | trigger         | next_run   | outcome                           |
    | Postgres | TimerIntermediate      | onetime         | 2025-06-01 | deleted by PruneTimers            |
    | MySQL    | TimerDeadline          | expr            | 2025-06-01 | deleted by PruneTimers            |
    | SQLite   | TimerRetry             | unset           | 2025-06-01 | deleted by PruneTimers            |
    | SQLite   | TimerCompensationRetry | unset           | 2025-06-01 | kept                              |
    | Postgres | TimerIntermediate      | onetime         | 2026-01-01 | kept                              |
    | MySQL    | TimerCompensationRetry | onetime         | 2025-06-01 | kept                              |
    | SQLite   | TimerIntermediate      | cron            | 2025-06-01 | kept                              |
    | Postgres | TimerInWait            | duration        | 1969-12-31 | deleted by ReclaimNeverDueTimers  |
    | MySQL    | TimerIntermediate      | everyExpr       | 1969-12-31 | deleted by ReclaimNeverDueTimers  |
    | SQLite   | TimerIntermediate      | durationRand    | 1969-12-31 | deleted by ReclaimNeverDueTimers  |
    | Postgres | TimerIntermediate      | onetime         | 1969-12-31 | deleted by PruneTimers            |
```
