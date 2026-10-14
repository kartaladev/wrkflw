## MODIFIED Requirements

### Requirement: Publish failures are returned to the caller
A publisher built with `eventing.NewPublisher` MUST refuse a payload that cannot be JSON-encoded with `eventing.ErrPayloadNotEncodable`, in an error naming the payload marshal failure, without calling the PublishFunc, and MUST return the PublishFunc's own error wrapped so that `errors.Is` still matches it, so an outbox relay leaves the row pending.

#### Scenario: Failure outcomes of a publish
```gherkin
Scenario Outline: A publish that cannot complete returns an error
  Given a publisher built with eventing.NewPublisher over a PublishFunc that returns <broker result>
  And an outbox event whose payload is <payload>
  When the event is published
  Then Publish returns <outcome>
  And the PublishFunc was called <calls> times

  Examples:
    | payload                   | broker result      | outcome                                                                                   | calls |
    | not JSON-encodable        | nil                | eventing.ErrPayloadNotEncodable, in an error naming the payload marshal failure            | 0     |
    | {"ok":true}               | a sentinel error   | an error that errors.Is matches the sentinel and that does not match eventing.ErrPayloadNotEncodable | 1     |
    | {"ok":true}               | nil                | nil                                                                                       | 1     |
```
