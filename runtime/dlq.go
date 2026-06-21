package runtime

import "time"

// DeadLetter is a quarantined outbox row surfaced by the relay's DLQ admin API.
// A row becomes dead after exhausting MaxDeliveryAttempts consecutive publish
// failures. Operators can inspect dead rows via the relay's ListDeadLettered
// method and re-queue them for re-delivery via Redrive.
type DeadLetter struct {
	// ID is the wrkflw_outbox primary key.
	ID int64
	// InstanceID is the process instance that produced this event.
	InstanceID string
	// Topic is the event topic (e.g. "instance.completed").
	Topic string
	// RetryCount is the number of failed publish attempts recorded when the row
	// was quarantined.
	RetryCount int
	// LastError is the error string from the final failed publish attempt.
	LastError string
	// CreatedAt is the timestamp at which the outbox row was first inserted.
	CreatedAt time.Time
}
