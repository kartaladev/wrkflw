# restart-recovery Specification

## Purpose
Defines how work that committed steps left pending is recovered when the process restarts. Today it covers armed timers, which a durable timer store lets a fresh scheduler re-arm, explicitly or by its own start-up rehydration; re-registering signal and message waiters and re-driving unperformed commands are not yet promised and are left to future changes.

## Requirements

### Requirement: Persisted timers are re-armed at their original fire time after a restart
`ProcessDriver.RehydrateTimers`, called on a fresh driver wired with a scheduler, the same timer store and the definitions, SHALL re-arm every persisted armed timer, and a non-recurring timer with a persisted next run MUST be re-armed at its original absolute instant rather than restarting its delay.

#### Scenario: A one-shot timer survives a crash on every SQL dialect
```gherkin
Scenario Outline: arm, crash, rehydrate, fire
  Given a ProcessDriver over a <dialect> instance store and timer store
  And instance "rh-1" parked on a one-shot timer due one hour after start
  And the driver and its scheduler are discarded
  And the clock is advanced five hours
  When a fresh driver over the same stores calls RehydrateTimers
  Then the timer is scheduled on the fresh scheduler at start plus one hour
  And one scheduler tick fires it
  And instance "rh-1" reaches StatusCompleted

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: Rehydrated timer resumes the instance on the in-memory stores
```gherkin
Given a ProcessDriver over an in-memory instance store and in-memory timer store
And instance "rh-1" parked on a one-hour timer
And the driver and its scheduler are discarded
When a fresh driver over the same stores calls RehydrateTimers
And the clock passes the fire time and the scheduler ticks
Then instance "rh-1" reaches StatusCompleted
```

### Requirement: A scheduler with a registered timer job store rehydrates on start
A scheduler built with `scheduler.WithJobStore` for the runtime's timer job kind, supplied by `runtime.NewJobStore`, SHALL re-arm every persisted armed timer when it starts, without an explicit `RehydrateTimers` call.

#### Scenario: Self-rehydration resumes a crashed instance
```gherkin
Given instance "rh-self-1" parked on a timer in a SQLite instance store and timer store
And the driver and scheduler that armed it are discarded
And a fresh scheduler registers runtime.NewJobStore for job kind "wrkflw.timer"
When the fresh scheduler starts
And the clock passes the fire time
Then instance "rh-self-1" reaches StatusCompleted
```

### Requirement: Timers whose definition cannot be resolved do not block the others
When some persisted timers reference definitions the registry cannot resolve, rehydration SHALL still re-arm every resolvable timer and SHALL report the skipped subset with an error wrapping `scheduler.ErrUnresolvedTimerDefinitions`; the scheduler's start-up self-rehydration MUST treat that error as non-fatal.

#### Scenario: Resolvable timers are returned alongside the sentinel
```gherkin
Scenario Outline: job store load with unresolvable definitions
  Given <resolvable> persisted timers whose definition is registered
  And <unresolvable> persisted timers whose definition is not registered
  When the runtime job store loads the armed timers
  Then <resolvable> jobs are returned
  And the error wraps scheduler.ErrUnresolvedTimerDefinitions

  Examples:
    | resolvable | unresolvable |
    | 0          | 1            |
    | 2          | 1            |
```

#### Scenario: RehydrateTimers re-arms resolvable timers and reports the rest
```gherkin
Given 2 persisted timers whose definition is registered
And 1 persisted timer whose definition is not registered
When a fresh driver calls RehydrateTimers
Then the 2 resolvable timers are scheduled
And RehydrateTimers returns an error wrapping scheduler.ErrUnresolvedTimerDefinitions
```

#### Scenario: Scheduler start is non-fatal on unresolved definitions
```gherkin
Given a persisted armed timer whose definition is not registered
And a scheduler with runtime.NewJobStore registered for job kind "wrkflw.timer"
When the scheduler starts
Then Start returns nil
```

#### Scenario: Negative control, all definitions resolvable
```gherkin
Given a persisted armed timer whose definition is registered
When a fresh driver calls RehydrateTimers
Then RehydrateTimers returns nil
And the timer is scheduled
```

### Requirement: A timer row that cannot be armed is skipped without failing the batch
Rehydration SHALL skip, without failing the batch, a persisted timer whose trigger has no next fire from the current clock, and MUST still re-arm every other row at a non-zero instant.

#### Scenario: Unarmable rows are dropped, healthy rows survive
```gherkin
Scenario Outline: job store load beside a healthy hourly row
  Given the clock at 2026-02-10 09:30 UTC
  And a healthy row recurring every hour
  And a row with trigger <trigger> and persisted next run <next_run>
  When the runtime job store loads the armed timers
  Then the healthy row is returned with a non-zero next run
  And the second row is <outcome>

  Examples:
    | trigger                         | next_run         | outcome                             |
    | every 12 months on day 31       | 2026-08-31 00:00 | skipped                             |
    | every 0                         | zero             | skipped                             |
    | every 1h                        | zero             | returned with a non-zero next run   |
    | weekly on Monday                | 2026-02-16       | returned with a non-zero next run   |
```

#### Scenario: RehydrateTimers returns promptly over an unarmable row
```gherkin
Given the clock at 2026-02-10 09:30 UTC
And a persisted row recurring every 12 months on day 31
When RehydrateTimers is called on a driver with a real scheduler
Then it returns nil within 10 seconds
```
