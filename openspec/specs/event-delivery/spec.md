# event-delivery Specification

## Purpose
Defines how wrkflw hands engine events to a broker and back: the broker-agnostic envelope and publisher, the built-in in-process bus, the message handler, and the transactional outbox relay that drains committed events at least once. Chaining a successor instance off a terminal event is the instance-chaining capability.

## Requirements

### Requirement: Eventing names no third-party messaging library
The `eventing` package MUST NOT import any third-party module other than OpenTelemetry directly, and its transitive build graph MUST NOT contain watermill or any of the three modules it alone carried (oklog/ulid, lithammer/shortuuid, pkg/errors), so a consumer reaches a broker through a function it writes rather than a vendor adapter.

#### Scenario: Direct imports stay within the allow-list
```gherkin
Given the eventing package at HEAD
When its direct imports are listed
Then every import is a standard-library package, a wrkflw package, or an OpenTelemetry package
```

#### Scenario: No denied module appears transitively
```gherkin
Scenario Outline: A denied module is absent from the build graph
  Given the eventing package at HEAD
  When its transitive dependencies are listed
  Then no dependency equals or lies under "<module>"

  Examples:
    | module                              |
    | github.com/ThreeDotsLabs/watermill  |
    | github.com/oklog/ulid               |
    | github.com/lithammer/shortuuid      |
    | github.com/pkg/errors               |
```

### Requirement: Published envelopes carry the outbox wire contract
An envelope produced by `eventing.NewPublisher` SHALL carry the outbox event's dedup key as its ID, the event topic as its Topic, the metadata keys `topic`, `instance_id` and `definition_ref`, and the JSON encoding of the event payload as its Body.

#### Scenario: A fully populated outbox event maps to the golden envelope
```gherkin
Given a publisher built with eventing.NewPublisher over a recording PublishFunc
And an outbox event with topic "instance.completed", instance id "order-42", definition "order-flow" version 1, dedup key "order-42:3:0" and payload {"status":"completed"}
When the event is published
Then the PublishFunc receives exactly one envelope
And its ID is "order-42:3:0"
And its Topic is "instance.completed"
And its routing metadata is exactly topic="instance.completed", instance_id="order-42", definition_ref="order-flow:1"
And its Body is {"status":"completed"}
```

#### Scenario: An event without a dedup key still gets a unique envelope id
```gherkin
Given a publisher built with eventing.NewPublisher
And an outbox event with an empty dedup key and no definition reference
When the same event is published twice
Then each envelope has a non-empty ID
And the two IDs differ
And the definition_ref metadata key is present with an empty value
```

### Requirement: Publish failures are returned to the caller
A publisher built with `eventing.NewPublisher` MUST refuse a payload that cannot be JSON-encoded with an error naming the payload marshal failure, without calling the PublishFunc, and MUST return the PublishFunc's own error wrapped so that `errors.Is` still matches it, so an outbox relay leaves the row pending.

#### Scenario: Failure outcomes of a publish
```gherkin
Scenario Outline: A publish that cannot complete returns an error
  Given a publisher built with eventing.NewPublisher over a PublishFunc that returns <broker result>
  And an outbox event whose payload is <payload>
  When the event is published
  Then Publish returns <outcome>
  And the PublishFunc was called <calls> times

  Examples:
    | payload                   | broker result      | outcome                                        | calls |
    | not JSON-encodable        | nil                | an error naming the payload marshal failure    | 0     |
    | {"ok":true}               | a sentinel error   | an error that errors.Is matches the sentinel   | 1     |
    | {"ok":true}               | nil                | nil                                            | 1     |
```

### Requirement: Trace context crosses the broker in the envelope
A publisher MUST write W3C trace context naming its publish span into the envelope metadata by default, without relying on a globally configured propagator, and `eventing.WithPropagator` SHALL replace that default rather than compose with it.

#### Scenario: The default propagator writes traceparent
```gherkin
Given a publisher built with eventing.NewPublisher and no propagator option
And no global OpenTelemetry propagator is configured
When an event is published
Then the envelope metadata contains a "traceparent" entry
And the trace and span ids it carries are those of the publish span
```

#### Scenario: An explicit propagator replaces the default
```gherkin
Given a publisher built with eventing.WithPropagator set to a propagator writing only "stub-key"
When an event is published
Then the envelope metadata contains "stub-key"
And it does not contain "traceparent"
```

#### Scenario: A handler span parents onto the publish span
```gherkin
Given an eventing.NewInProcess bus with a live subscription whose handler starts a span
When an event is published on the subscribed topic
Then the handler's span is a child of the publish span
```

### Requirement: The in-process bus fans out to every subscription on the topic
`eventing.NewInProcess` SHALL deliver each published envelope to every live subscription on the envelope's topic and to no subscription on any other topic.

#### Scenario: Every subscriber on the topic receives the envelope
```gherkin
Given an in-process bus with several live subscriptions on "instance.completed"
When an envelope is published to "instance.completed"
Then Publish returns nil
And every subscription receives the envelope
```

#### Scenario: Each subscription receives its own topic's envelopes
```gherkin
Given an in-process bus with a live subscription on "instance.completed" and one on "instance.failed"
When envelope "p1" is published to "instance.completed" and then envelope "sentinel" to "instance.failed"
Then the "instance.completed" subscription receives "p1" and holds only "instance.completed" envelopes
And the first envelope the "instance.failed" subscription receives is "sentinel"
And it holds only "instance.failed" envelopes
```

### Requirement: Each in-process subscription delivers in publish order
Each subscription of an `eventing.NewInProcess` bus SHALL deliver the envelopes queued for it in the order they were published.

#### Scenario: A queued backlog drains first-in first-out
```gherkin
Given an in-process bus with one live subscription whose handler is held inside its first delivery
And further envelopes are published to the topic while the handler is held
When the handler is released
Then the subscription delivers the queued envelopes in the order they were published
```

### Requirement: A handler's return value is its ack or nack
For a subscription on the in-process bus, a `Handler` returning nil MUST acknowledge the envelope, and a `Handler` returning an error MUST cause the same envelope to be handed back to that handler after the redelivery backoff until it acknowledges or the subscription ends.

#### Scenario: A nacked envelope is redelivered after the backoff
```gherkin
Given an in-process bus built with eventing.WithRedeliveryBackoff of 20 milliseconds
And a handler that returns an error on its first two attempts and nil on the third
When one envelope is published
Then the handler receives the same envelope id three times
And consecutive attempts are at least the backoff apart
```

### Requirement: Every in-process delivery attempt receives its own copy
The in-process bus MUST hand each subscription, and each redelivery attempt to one subscription, an independent copy of the envelope's metadata and body, so no handler can observe another handler's mutations or its own previous attempt's.

#### Scenario: Sibling subscriptions are isolated
```gherkin
Given an in-process bus with two live subscriptions on one topic
And the first handler overwrites a metadata key and a byte of the body
When an envelope is published
Then the second handler sees the original metadata and body
```

#### Scenario: A redelivery is isolated from the previous attempt
```gherkin
Given an in-process bus with one subscription whose handler mutates the metadata and body and nacks once
When an envelope is published
Then the redelivered attempt sees the original metadata and body
```

### Requirement: Publishing to a topic whose subscriptions have all ended is reported
The in-process bus SHALL return `eventing.ErrNoSubscription` from Publish when the envelope's topic has had a subscription registered on this bus and currently has none, and MUST NOT deliver that envelope.

#### Scenario: Publish after the last subscription stopped
```gherkin
Given an in-process bus
And a subscription on "instance.completed" started with Start and then stopped
When an envelope is published to "instance.completed"
Then Publish returns eventing.ErrNoSubscription
And the stopped subscription's handler is not called
```

#### Scenario: Negative control - a topic never subscribed stays silent
```gherkin
Given an in-process bus built without eventing.WithRequireSubscription
And a live subscription on "instance.completed" only
When an envelope is published to "instance.terminated", which has never been subscribed on this bus
Then Publish returns nil
```

#### Scenario: Negative control - a live subscription is not refused
```gherkin
Given an in-process bus with a live subscription on "instance.completed"
When an envelope is published to "instance.completed"
Then Publish returns nil
And the subscription receives the envelope
```

### Requirement: Closing the in-process bus ends subscriptions and refuses later publishes
`InProcess.Close` SHALL end every live subscription, including one waiting out a redelivery backoff, SHALL cancel the contexts of subscriptions created by Start, MUST make later publishes return `eventing.ErrBusClosed`, and MUST be idempotent.

#### Scenario: Close ends idle subscriptions and Subscribe returns nil
```gherkin
Given an in-process bus with three live Subscribe calls that have drained to idle
When Close is called
Then every Subscribe call returns nil
```

#### Scenario: Close ends a subscription parked in the redelivery backoff
```gherkin
Given an in-process bus built with a 30 second redelivery backoff
And a subscription whose handler always nacks and has nacked at least once
When Close is called
Then the Subscribe call returns nil without waiting for the backoff
```

#### Scenario: Close cancels the context of a Start subscription
```gherkin
Given a subscription created with InProcess.Start whose handler captured its context
When Close is called
Then the captured context is cancelled with context.Canceled
```

#### Scenario: Calls after Close
```gherkin
Scenario Outline: The bus refuses work after Close
  Given an in-process bus that has been closed
  When <call> is made
  Then it returns <result>

  Examples:
    | call                 | result               |
    | Publish              | eventing.ErrBusClosed |
    | a second Close       | nil                  |
    | Subscribe            | eventing.ErrBusClosed |
    | Start                | eventing.ErrBusClosed |
```

#### Scenario: Negative control - an open bus accepts a publish
```gherkin
Given an in-process bus that has not been closed and has a live subscription on the topic
When an envelope is published
Then Publish returns nil
```

### Requirement: Ending a subscription stops at the next envelope boundary
When an in-process subscription's context is cancelled, the subscription MUST stop after the envelope in flight instead of draining the rest of its queued backlog, and a Subscribe ended by cancellation SHALL return the context's error.

#### Scenario: A cancelled subscription abandons its backlog
```gherkin
Given an in-process subscription whose handler is held inside the delivery of envelope "delivered"
And envelope "abandoned" is queued behind it
When the subscription's context is cancelled and the handler is released
Then the handler has received only "delivered"
```

#### Scenario: Subscribe returns the cancellation error
```gherkin
Given a live Subscribe call on an in-process bus
When its context is cancelled
Then Subscribe returns an error matching context.Canceled
```

### Requirement: InProcess.Start returns only once the subscription is live
`InProcess.Start` SHALL return only after its subscription is registered, so a publish issued after it returns is delivered; its stop function MUST end the subscription and wait for the delivery loop to finish, and MUST be safe to call more than once and concurrently.

#### Scenario: A publish immediately after Start is delivered
```gherkin
Given an in-process bus
When a subscription is created with Start and an envelope is published once immediately after it returns
Then the handler receives the envelope
```

#### Scenario: stop waits for an in-flight handler
```gherkin
Given a subscription created with Start whose handler is held inside a delivery
When stop is called
Then stop does not return until the handler has returned
```

#### Scenario: stop is safe twice and concurrently
```gherkin
Given a subscription created with Start whose handler is held inside a delivery
When four callers invoke stop concurrently and the handler is then released
Then every caller returns
And none returns while the handler is still running
And calling stop again afterwards does not panic
```

### Requirement: The message handler routes decodable messages and acks poison
A handler built with `eventing.NewMessageHandler` SHALL decode the message name, correlation key and variables from the envelope body and pass them to the deliver function, MUST acknowledge without delivering a body that is empty, malformed or names no message, and MUST return a deliver failure so the envelope is redelivered.

#### Scenario: Message handler decisions
```gherkin
Scenario Outline: One message envelope, one decision
  Given a message handler over a deliver function that <deliver behaviour>
  When an envelope with body <body> is handled
  Then the handler returns <result>
  And deliver was called <calls> times

  Examples:
    | body                                                                            | deliver behaviour | result                         | calls |
    | {"messageName":"OrderPlaced","correlationKey":"ord-7","variables":{"amount":10}} | succeeds          | nil                            | 1     |
    | {not json                                                                       | succeeds          | nil                            | 0     |
    | {"correlationKey":"ord-7"}                                                      | succeeds          | nil                            | 0     |
    | (empty)                                                                         | succeeds          | nil                            | 0     |
    | {"messageName":"OrderPlaced","correlationKey":"ord-7"}                           | fails             | the deliver error (errors.Is)  | 1     |
```

#### Scenario: The routed values reach deliver unchanged
```gherkin
Given a message handler over a recording deliver function
When an envelope whose body names message "OrderPlaced", correlation key "ord-7" and variables {"amount":10} is handled
Then deliver receives name "OrderPlaced", correlation key "ord-7" and payload {"amount":10}
```

### Requirement: The relay publishes pending outbox rows and marks them published
The outbox relay the `persistence` package builds for each SQL dialect SHALL hand every due pending outbox row to its publisher, MUST mark a row published only after its Publish call returned nil, and DrainOnce SHALL return the number of rows it published. The Postgres, MySQL and SQLite relays all delegate to one shared relay implementation.

#### Scenario: A drain publishes every pending row on every dialect
```gherkin
Scenario Outline: DrainOnce on <dialect>
  Given a <dialect> outbox holding <rows> pending rows
  And the <dialect> relay over a publisher that always succeeds
  When DrainOnce is called
  Then it returns <rows> and nil
  And the publisher received <rows> events
  And no row remains pending
  And a second DrainOnce returns 0

  Examples:
    | dialect  | rows |
    | Postgres | 3    |
    | MySQL    | 3    |
    | SQLite   | 3    |
    | Postgres | 0    |
    | MySQL    | 0    |
    | SQLite   | 0    |
```

#### Scenario: The relay built through the persistence package drains
```gherkin
Scenario Outline: DrainOnce through the public <dialect> relay constructor
  Given a <dialect> outbox holding <rows> pending rows
  And the <dialect> relay built by the persistence package over a publisher that always succeeds
  When DrainOnce is called
  Then it returns <rows> and nil
  And the publisher received <rows> events

  Examples:
    | dialect  | rows |
    | Postgres | 1    |
    | MySQL    | 3    |
    | SQLite   | 3    |
```

### Requirement: A failing outbox row is retried, isolated and finally dead-lettered
When Publish fails for an outbox row, the relay MUST leave that row pending with its retry count incremented and not claim it again until its backoff has elapsed, MUST still publish healthy rows in the same batch, and MUST quarantine the row as dead once it reaches the configured maximum delivery attempts, after which it is no longer claimed.

#### Scenario: Poison isolation and quarantine on every dialect
```gherkin
Scenario Outline: A poison row on <dialect>
  Given a <dialect> outbox holding a "poison" row and a "healthy" row
  And a relay with a maximum of 3 delivery attempts whose publisher fails only for "poison"
  When DrainOnce is called once
  Then it returns 1
  And "healthy" is published
  And "poison" is pending with retry count 1
  When the clock passes the backoff and DrainOnce is repeated until "poison" has 3 attempts
  Then "poison" is dead
  And a further DrainOnce publishes nothing
  And "healthy" was published exactly once

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: A failed row is not claimed before its backoff elapses
```gherkin
Scenario Outline: Backoff deferral on <dialect>
  Given a <dialect> outbox holding one row whose first Publish failed
  When DrainOnce is called again before the row's backoff has elapsed
  Then the publisher is not called for that row
  And its retry count is still 1

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: A publish failure is absorbed, not propagated
```gherkin
Scenario Outline: DrainOnce with a failing publisher on <dialect>
  Given a <dialect> outbox holding 1 pending row
  And a relay over a publisher that always fails
  When DrainOnce is called
  Then it returns 0 and nil
  And the row is still pending

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Relay.Run keeps polling through publish failures and stops on cancellation
`Relay.Run` SHALL keep draining on each poll tick while individual publishes fail, and MUST return the context's error when its context is cancelled.

#### Scenario: Run absorbs failures and exits on cancel on every dialect
```gherkin
Scenario Outline: Run absorbs failures and exits on cancel on <dialect>
  Given a <dialect> outbox holding 1 pending row
  And a relay over a publisher that always fails, polling every 10 milliseconds
  When Run is started
  Then the row's retry count reaches at least 1 while Run keeps running
  When the context is cancelled
  Then Run returns an error matching context.Canceled

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Dead-lettered rows can be listed and redriven
`Relay.ListDeadLettered` SHALL return only dead outbox rows with their instance, topic, retry count and last error, and `Relay.Redrive` SHALL return only dead rows to pending, report how many it re-queued, ignore rows that are not dead, and return 0 with no error when given no ids.

#### Scenario: Listing and redriving on every dialect
```gherkin
Scenario Outline: Dead-letter administration on <dialect>
  Given a <dialect> outbox holding one dead row with last error "boom" and one pending row
  When ListDeadLettered is called with limit 10
  Then it returns exactly the dead row with its topic, instance id, retry count and last error
  When Redrive is called with no ids
  Then it returns 0 and nil
  When Redrive is called with the pending row's id
  Then it returns 0 and nil
  When Redrive is called with the dead row's id
  Then it returns 1 and nil
  And the row is pending
  And the next DrainOnce publishes it

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: The relay reports outbox backlog statistics
`Relay.OutboxStats` SHALL report the number of pending rows, the number of dead rows, and the age of the oldest pending row.

#### Scenario: Stats on every dialect
```gherkin
Scenario Outline: OutboxStats on <dialect>
  Given a <dialect> outbox holding two pending rows created 5 seconds ago and one dead row
  When OutboxStats is called
  Then Pending is 2
  And Dead is 1
  And the oldest pending age is greater than zero

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Postgres can wake the relay by notification; other dialects poll
With `persistence.WithOutboxNotify` on the Postgres store and `persistence.WithListenNotify` on the Postgres relay, the relay SHALL drain a newly committed outbox row before its poll interval elapses; the MySQL and SQLite relays MUST continue to drain by polling.

#### Scenario: Postgres drains on notification before the poll interval
```gherkin
Given a Postgres store with outbox notification enabled
And a Postgres relay listening for notifications with a 30 second poll interval
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
