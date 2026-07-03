package persistence

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// relayBacklogConfig holds the threshold values for [RelayBacklogCheck].
type relayBacklogConfig struct {
	maxDead    int64
	maxPending int64
}

// RelayBacklogOption configures a [RelayBacklogCheck].
type RelayBacklogOption func(*relayBacklogConfig)

// WithMaxDead sets the maximum number of dead (quarantined) outbox rows before
// the probe reports unhealthy. A value of 0 (the default) disables the threshold.
func WithMaxDead(n int64) RelayBacklogOption {
	return func(c *relayBacklogConfig) {
		c.maxDead = n
	}
}

// WithMaxPending sets the maximum number of pending outbox rows before the probe
// reports unhealthy. A value of 0 (the default) disables the threshold.
func WithMaxPending(n int64) RelayBacklogOption {
	return func(c *relayBacklogConfig) {
		c.maxPending = n
	}
}

// RelayBacklogCheck is a readiness probe that queries the outbox table statistics
// and reports unhealthy when the dead or pending row counts exceed configured
// thresholds (ADR-0054). It structurally satisfies the rest.HealthCheck contract
// (Name + Check), so a consumer registers it with rest.NewHealthHandler:
//
//	handler := rest.NewHealthHandler(persistence.NewRelayBacklogCheck(relay))
//
// It is defined here (not in transport/rest) so the transport package has no
// import dependency on the persistence layer. Only the test asserts the
// interface satisfaction via var _ rest.HealthCheck = check.
type RelayBacklogCheck struct {
	reader kernel.OutboxStatsReader
	cfg    relayBacklogConfig
}

// NewRelayBacklogCheck returns a [RelayBacklogCheck] backed by r. Both thresholds
// default to 0, which means disabled — [RelayBacklogCheck.Check] will only return
// an error when r.OutboxStats itself errors or a non-zero threshold is breached.
func NewRelayBacklogCheck(r kernel.OutboxStatsReader, opts ...RelayBacklogOption) RelayBacklogCheck {
	cfg := relayBacklogConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return RelayBacklogCheck{reader: r, cfg: cfg}
}

// Name returns "relay-backlog", the probe's name as it appears in /readyz responses.
func (c RelayBacklogCheck) Name() string { return "relay-backlog" }

// Check reads the outbox statistics from the underlying [kernel.OutboxStatsReader]
// and returns a non-nil error when:
//   - the reader call fails (e.g. DB down, ctx cancelled), or
//   - maxDead > 0 and stats.Dead > maxDead, or
//   - maxPending > 0 and stats.Pending > maxPending.
//
// A threshold of 0 is never evaluated (disabled), so the default configuration
// always returns nil unless the read itself fails.
func (c RelayBacklogCheck) Check(ctx context.Context) error {
	stats, err := c.reader.OutboxStats(ctx)
	if err != nil {
		return fmt.Errorf("workflow-persistence: relay-backlog: read outbox stats: %w", err)
	}

	if c.cfg.maxDead > 0 && stats.Dead > c.cfg.maxDead {
		return fmt.Errorf(
			"workflow-persistence: relay-backlog: dead message count %d exceeds threshold %d",
			stats.Dead, c.cfg.maxDead,
		)
	}

	if c.cfg.maxPending > 0 && stats.Pending > c.cfg.maxPending {
		return fmt.Errorf(
			"workflow-persistence: relay-backlog: pending message count %d exceeds threshold %d",
			stats.Pending, c.cfg.maxPending,
		)
	}

	return nil
}
