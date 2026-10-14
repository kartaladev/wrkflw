## Purpose

Defines how a terminal process instance starts its successor: the broker-agnostic chaining core that applies a successor policy idempotently under at-least-once delivery, the chain handler that maps terminal envelopes onto it, and the turnkey runner that subscribes the three terminal topics.

## ADDED Requirements

### Requirement: Chainer.Start returns only once all three terminal topics are live
`Chainer.Start` (from `eventing.NewChainerRunner`) SHALL return only after subscriptions to `instance.completed`, `instance.failed` and `instance.terminated` are all live on the given Starter; on a failure it MUST stop every subscription it already started and return a nil stop with the error.

#### Scenario: A terminal event published once after Start chains its successor
```gherkin
Given a Chainer runner over a chaining core whose policy always starts a successor
And an in-process bus
When Chainer.Start returns successfully and one "instance.completed" event is published exactly once
Then the successor instance is started
```

#### Scenario: A partial failure strands nothing
```gherkin
Scenario Outline: Start unwinds what it brought up
  Given a Starter that fails when asked to start "<failing topic>"
  When Chainer.Start is called
  Then it returns a nil stop and an error that matches the Starter's error under errors.Is and names "<failing topic>"
  And every topic in <started> was stopped before Start returned

  Examples:
    | failing topic        | started                                   |
    | instance.completed   | none                                      |
    | instance.failed      | instance.completed                        |
    | instance.terminated  | instance.completed, instance.failed       |
```

#### Scenario: stop ends all three subscriptions and is idempotent
```gherkin
Given a Chainer started with Chainer.Start over a Starter that records starts and stops
When stop is called twice
Then all three terminal topics were stopped by the first call
And the second call does not panic and stops nothing again
```

#### Scenario: stop waits for an in-flight chain handler
```gherkin
Given a Chainer started with Chainer.Start over an in-process bus
And a chain handler held inside the delivery of an "instance.completed" envelope
When stop is called
Then stop does not return until the handler has returned
```

### Requirement: The chain handler acks or nacks by outcome
A handler built with `eventing.NewChainHandler` SHALL project the three terminal topics to the completed, failed and terminated outcomes and drive the chaining core, MUST acknowledge an envelope on any other topic or with a malformed JSON body without chaining, and MUST return the error of a transient chaining failure so the envelope is redelivered.

#### Scenario: Ack discipline per envelope
```gherkin
Scenario Outline: The chain handler's decision per envelope
  Given a chain handler over a core whose policy starts a successor
  And a starter that succeeds
  When an envelope on topic "<topic>" with body <body> for instance "p1" is handled
  Then the handler returns nil
  And the projected outcome is <outcome>

  Examples:
    | topic               | body               | outcome    |
    | instance.completed  | {"orderID":"o-7"}  | completed  |
    | instance.failed     | {"error":"x"}      | failed     |
    | instance.terminated | {"error":"x"}      | terminated |
    | something.else      | {}                 | none       |
    | instance.completed  | {not json          | none       |
```

#### Scenario: A transient start failure nacks the envelope
```gherkin
Given a chain handler over a core whose policy starts a successor
And a starter that fails transiently
When an "instance.completed" envelope with body {} for instance "p1" is handled
Then the handler returns an error
And the successor start was attempted
```

### Requirement: Chaining is idempotent under at-least-once delivery
`chain.Chainer.Handle` SHALL start the successor under an instance ID fixed by the predecessor and outcome, MUST treat `kernel.ErrInstanceExists` from the starter and a policy that declines as successful no-ops, and MUST return a transient start failure (matching under `errors.Is`) so a redelivery re-attempts the start.

#### Scenario: Handle outcomes
```gherkin
Scenario Outline: One terminal event, one decision
  Given a chaining core with <link store>
  And a policy that <policy>
  And a starter that <starter behaviour>
  When Handle is called for predecessor "p1" with outcome completed
  Then Handle returns <result>
  And the starter was called <starts> times

  Examples:
    | link store    | policy                   | starter behaviour                    | result                               | starts |
    | a link store  | declines (ok=false)      | succeeds                             | nil                                  | 0      |
    | a link store  | returns a nil definition | succeeds                             | nil                                  | 0      |
    | a link store  | decides a successor      | succeeds                             | nil                                  | 1      |
    | a link store  | decides a successor      | returns kernel.ErrInstanceExists     | nil                                  | 1      |
    | a link store  | decides a successor      | fails transiently                    | the starter's error (errors.Is)      | 1      |
    | no link store | decides a successor      | succeeds                             | nil                                  | 1      |
```

#### Scenario: The successor ID is fixed by predecessor and outcome
```gherkin
Scenario Outline: Deterministic successor ID
  Given a chaining core with <link store> and a policy that decides a successor
  When Handle is called for predecessor "p1" with outcome completed
  Then the starter is asked to start instance "p1-next-completed"

  Examples:
    | link store    |
    | a link store  |
    | no link store |
```

#### Scenario: A redelivery after a failed start re-attempts the start
```gherkin
Given a chaining core with a link store and a starter that fails once then succeeds
When Handle is called and returns an error
And Handle is called again with the same event
Then the second call returns nil
And the starter was called twice
```

### Requirement: A chaining core needs a starter and a policy
`chain.NewChainer` MUST return `kernel.ErrNilDependency` and a nil Chainer when its starter or its successor policy is nil, and SHALL return a usable Chainer when both are present.

#### Scenario: Constructor guards
```gherkin
Scenario Outline: NewChainer argument checks
  Given a starter that is <starter> and a policy that is <policy>
  When chain.NewChainer is called
  Then it returns <result>

  Examples:
    | starter | policy  | result                                  |
    | nil     | present | a nil Chainer and kernel.ErrNilDependency |
    | present | nil     | a nil Chainer and kernel.ErrNilDependency |
    | present | present | a non-nil Chainer and nil               |
```
