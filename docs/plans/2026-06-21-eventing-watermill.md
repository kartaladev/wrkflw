# Eventing (watermill) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a watermill-backed `runtime.Publisher` that the existing Postgres outbox `Relay` drains into a broker, keeping watermill out of engine/runtime/model.

**Architecture:** A concrete adapter in `internal/eventing/watermill/` wraps any watermill `message.Publisher`, mapping one `runtime.OutboxEvent` → one `*message.Message` (stable UUID = dedup_key, `instance_id` metadata partition key). A root façade `eventing/` exposes `NewPublisher` / `NewGoChannelPublisher` returning `runtime.Publisher`, plus slog + OpenTelemetry observability. A one-field extension to `runtime.OutboxEvent`, populated by the Postgres relay, supplies the dedup/partition keys.

**Tech Stack:** Go 1.25.7, `github.com/ThreeDotsLabs/watermill` (core: `message`, `pubsub/gochannel`), `go.opentelemetry.io/otel` (API), `log/slog`, pgx (existing), testcontainers (existing).

## Global Constraints

- Module path: `github.com/kartaladev/wrkflw`. Go 1.25.7.
- **watermill is NEVER imported from `engine`/`model`/`runtime` production code** — only from `eventing/`, `internal/eventing/watermill/`, and `_test.go` files. Same rule for the OTel SDK (API-only in library code).
- **TDD strict:** every new exported symbol gets a failing test FIRST with a visible RED (`go test` showing build-fail or assertion-fail) before the implementation, per CLAUDE.md "TDD Operational Discipline." A `Write` of `foo_test.go` immediately followed by `foo.go` with no `go test` between them is forbidden.
- **Tests:** black-box (`package <pkg>_test`); table-driven tests use the project `table-test` skill's **`assert` closure per case** (NOT `want`/`wantErr` fields); use `t.Context()` not `context.Background()`; pair each `foo.go` with `foo_test.go`; reserve `*_example_test.go` for genuine e2e/godoc examples.
- **Façade purity:** the `eventing` package returns the `runtime.Publisher` interface, never an internal-concrete type; a `var _ runtime.Publisher = (*watermillpub.Publisher)(nil)` compile-time guard lives in the façade.
- **Verify per task:** `go test -race ./<touched-pkg>/...` green; on completion ≥85% line coverage on touched packages and `golangci-lint run ./...` clean (v2 config).
- **Commits:** Conventional Commits scoped `eventing` (or `runtime`/`persistence` for Task 1), ending with the trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Commit per task.
- Branch is `feat/eventing-watermill` (already created; spec + ADR-0012 already committed there).

---

### Task 1: Extend `runtime.OutboxEvent` and populate it from the Postgres relay

Adds `DedupKey` + `InstanceID` to the event the relay hands the publisher, sourced from the outbox row. This is the only change to already-merged code. Requires a running Docker daemon (testcontainers).

**Files:**
- Modify: `runtime/ports.go:24-28` (add two fields to `OutboxEvent`)
- Modify: `internal/persistence/postgres/relay.go:97-129` (SELECT projection, Scan, event construction)
- Test: `internal/persistence/postgres/relay_test.go` (new test)

**Interfaces:**
- Produces: `runtime.OutboxEvent{Topic string; Payload map[string]any; DedupKey string; InstanceID string}` — consumed by every later task.

- [ ] **Step 1: Write the failing test**

Append to `internal/persistence/postgres/relay_test.go`:

```go
// capturingPub records the full OutboxEvents it receives (not just topics).
type capturingPub struct {
	mu     sync.Mutex
	events []runtime.OutboxEvent
}

func (p *capturingPub) Publish(_ context.Context, ev runtime.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, ev)
	return nil
}

// TestRelayPopulatesDedupAndInstanceID verifies the relay maps the outbox row's
// instance_id and dedup_key columns onto the OutboxEvent it publishes, so a
// watermill adapter can set a stable message UUID + partition key.
func TestRelayPopulatesDedupAndInstanceID(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	_, err := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, $5)`,
		"inst-42", "instance.completed", `{"ok":true}`, "inst-42:7:0", time.Now().UTC(),
	)
	require.NoError(t, err)

	pub := &capturingPub{}
	relay := pg.NewRelay(pool, pub)
	n, err := relay.DrainOnce(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, pub.events, 1)
	require.Equal(t, "instance.completed", pub.events[0].Topic)
	require.Equal(t, "inst-42", pub.events[0].InstanceID)
	require.Equal(t, "inst-42:7:0", pub.events[0].DedupKey)
	require.Equal(t, map[string]any{"ok": true}, pub.events[0].Payload)
}
```

- [ ] **Step 2: Run test to verify it fails (RED)**

Run: `go test -run '^TestRelayPopulatesDedupAndInstanceID$' ./internal/persistence/postgres/...`
Expected: BUILD FAILURE — `ev.InstanceID`/`ev.DedupKey` undefined (the fields don't exist yet). This is a valid red.

- [ ] **Step 3: Add the fields to `OutboxEvent`**

In `runtime/ports.go`, replace the struct:

```go
// OutboxEvent is one domain event to relay. DedupKey and InstanceID are
// populated when the event is read back from a persisted outbox row; they let a
// publisher set a stable message identity (DedupKey) and a per-instance
// partition/ordering key (InstanceID). They are empty for events not sourced
// from a persisted row.
type OutboxEvent struct {
	Topic      string
	Payload    map[string]any
	DedupKey   string
	InstanceID string
}
```

- [ ] **Step 4: Run test to verify it now compiles but fails on the assertions (RED)**

Run: `go test -run '^TestRelayPopulatesDedupAndInstanceID$' ./internal/persistence/postgres/...`
Expected: FAIL — `InstanceID`/`DedupKey` assertions fail (empty strings) because the relay doesn't read those columns yet.

- [ ] **Step 5: Populate the fields in the relay**

In `internal/persistence/postgres/relay.go`, change the `DrainOnce` query and scan:

```go
	rows, err := tx.Query(ctx,
		`SELECT id, topic, payload, instance_id, dedup_key
		   FROM wrkflw_outbox
		  WHERE published_at IS NULL
		  ORDER BY id
		    FOR UPDATE SKIP LOCKED
		  LIMIT $1`,
		r.batch,
	)
```

and in the row loop:

```go
	var claims []claim
	for rows.Next() {
		var id int64
		var topic string
		var rawPayload []byte
		var instanceID, dedupKey string
		if err := rows.Scan(&id, &topic, &rawPayload, &instanceID, &dedupKey); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: relay: scan: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(rawPayload, &payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: relay: unmarshal payload id=%d: %w", id, err)
		}
		claims = append(claims, claim{id: id, event: runtime.OutboxEvent{
			Topic:      topic,
			Payload:    payload,
			DedupKey:   dedupKey,
			InstanceID: instanceID,
		}})
	}
```

- [ ] **Step 6: Run tests to verify they pass (GREEN)**

Run: `go test -race ./internal/persistence/postgres/... ./runtime/...`
Expected: PASS (the new test plus all existing relay/store tests — they don't inspect the new fields, so they still pass).

- [ ] **Step 7: Commit**

```bash
git add runtime/ports.go internal/persistence/postgres/relay.go internal/persistence/postgres/relay_test.go
git commit -m "$(printf 'feat(runtime): carry dedup_key and instance_id on OutboxEvent\n\nThe Postgres relay now maps the outbox row instance_id and dedup_key onto\nthe OutboxEvent it publishes, so a publisher can set a stable message UUID\n(idempotency) and a per-instance partition/ordering key.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 2: watermill adapter — `Publish` mapping + slog (no OTel yet)

The concrete adapter wrapping a watermill `message.Publisher`, plus a slog-backed `watermill.LoggerAdapter` for bridging watermill's internal logs.

**Files:**
- Modify: `go.mod`/`go.sum` (add watermill)
- Create: `internal/eventing/watermill/publisher.go`
- Create: `internal/eventing/watermill/options.go`
- Create: `internal/eventing/watermill/logger.go`
- Test: `internal/eventing/watermill/publisher_test.go`
- Test: `internal/eventing/watermill/logger_test.go`

**Interfaces:**
- Consumes: `runtime.OutboxEvent` (Task 1), watermill `message.Publisher`.
- Produces:
  - `watermillpub.NewPublisher(pub message.Publisher, opts ...Option) *Publisher`
  - `(*Publisher).Publish(ctx context.Context, ev runtime.OutboxEvent) error`
  - `watermillpub.WithLogger(l *slog.Logger) Option`
  - `watermillpub.NewWatermillLogger(l *slog.Logger) watermill.LoggerAdapter`
  - (package import alias used throughout the plan: `watermillpub "github.com/kartaladev/wrkflw/internal/eventing/watermill"`)

- [ ] **Step 1: Add the watermill dependency**

Run: `go get github.com/ThreeDotsLabs/watermill@latest`
Expected: `go.mod` gains `github.com/ThreeDotsLabs/watermill vX.Y.Z` (latest stable, 1.4+).

- [ ] **Step 2: Write the failing mapping test**

Create `internal/eventing/watermill/publisher_test.go`:

```go
package watermill_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/require"
	watermillpub "github.com/kartaladev/wrkflw/internal/eventing/watermill"
	"github.com/kartaladev/wrkflw/runtime"
)

// fakePub captures the topic and messages of the last Publish call.
type fakePub struct {
	topic string
	msgs  []*message.Message
	err   error
}

func (f *fakePub) Publish(topic string, msgs ...*message.Message) error {
	if f.err != nil {
		return f.err
	}
	f.topic = topic
	f.msgs = msgs
	return nil
}

func (f *fakePub) Close() error { return nil }

func TestPublishMapsEventToMessage(t *testing.T) {
	tests := map[string]struct {
		event  runtime.OutboxEvent
		assert func(t *testing.T, fp *fakePub, err error)
	}{
		"dedup key becomes the message UUID and payload is JSON": {
			event: runtime.OutboxEvent{
				Topic:      "instance.completed",
				Payload:    map[string]any{"ok": true},
				DedupKey:   "inst-1:3:0",
				InstanceID: "inst-1",
			},
			assert: func(t *testing.T, fp *fakePub, err error) {
				require.NoError(t, err)
				require.Equal(t, "instance.completed", fp.topic)
				require.Len(t, fp.msgs, 1)
				require.Equal(t, "inst-1:3:0", fp.msgs[0].UUID)
				require.JSONEq(t, `{"ok":true}`, string(fp.msgs[0].Payload))
				require.Equal(t, "inst-1", fp.msgs[0].Metadata.Get("instance_id"))
				require.Equal(t, "instance.completed", fp.msgs[0].Metadata.Get("topic"))
			},
		},
		"empty dedup key gets a generated non-empty UUID": {
			event: runtime.OutboxEvent{Topic: "instance.failed", Payload: map[string]any{"error": "boom"}},
			assert: func(t *testing.T, fp *fakePub, err error) {
				require.NoError(t, err)
				require.NotEmpty(t, fp.msgs[0].UUID)
			},
		},
		"publisher error is wrapped and returned": {
			event: runtime.OutboxEvent{Topic: "instance.completed", Payload: map[string]any{}},
			assert: func(t *testing.T, _ *fakePub, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "instance.completed")
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fp := &fakePub{}
			if name == "publisher error is wrapped and returned" {
				fp.err = errors.New("broker down")
			}
			pub := watermillpub.NewPublisher(fp)
			err := pub.Publish(context.Background(), tc.event)
			tc.assert(t, fp, err)
		})
	}
}

func TestPublisherImplementsRuntimePublisher(t *testing.T) {
	var _ runtime.Publisher = (*watermillpub.Publisher)(nil)
}
```

- [ ] **Step 3: Run test to verify it fails (RED)**

Run: `go test ./internal/eventing/watermill/...`
Expected: BUILD FAILURE — package/`NewPublisher`/`Publisher` undefined.

- [ ] **Step 4: Implement the adapter and options**

Create `internal/eventing/watermill/options.go`:

```go
// Package watermill adapts a watermill message.Publisher to the
// runtime.Publisher port. It is the only package besides eventing/ that imports
// watermill; engine/model/runtime never do.
package watermill

import "log/slog"

// Option configures a Publisher.
type Option func(*config)

type config struct {
	logger *slog.Logger
}

// WithLogger sets the structured logger (default slog.Default()). A nil logger
// is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}
```

Create `internal/eventing/watermill/publisher.go`:

```go
package watermill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/kartaladev/wrkflw/runtime"
)

// Publisher adapts a watermill message.Publisher to runtime.Publisher. It maps
// one OutboxEvent to one watermill message: the message UUID is the event's
// DedupKey (or a fresh UUID when empty) so redeliveries are deduplicable, and
// the instance id is set as metadata for per-instance partitioning/ordering.
type Publisher struct {
	pub    message.Publisher
	logger *slog.Logger
}

// Compile-time check.
var _ runtime.Publisher = (*Publisher)(nil)

// NewPublisher wraps a watermill message.Publisher.
func NewPublisher(pub message.Publisher, opts ...Option) *Publisher {
	cfg := config{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	return &Publisher{pub: pub, logger: cfg.logger}
}

// Publish maps ev to a watermill message and publishes it to ev.Topic.
func (p *Publisher) Publish(ctx context.Context, ev runtime.OutboxEvent) error {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		p.logger.ErrorContext(ctx, "eventing: marshal payload failed",
			slog.String("topic", ev.Topic), slog.Any("error", err))
		return fmt.Errorf("eventing: marshal payload: %w", err)
	}

	id := ev.DedupKey
	if id == "" {
		id = watermill.NewUUID()
	}
	msg := message.NewMessage(id, payload)
	msg.Metadata.Set("topic", ev.Topic)
	msg.Metadata.Set("instance_id", ev.InstanceID)
	msg.SetContext(ctx)

	if err := p.pub.Publish(ev.Topic, msg); err != nil {
		p.logger.ErrorContext(ctx, "eventing: publish failed",
			slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
			slog.Any("error", err))
		return fmt.Errorf("eventing: publish topic=%q: %w", ev.Topic, err)
	}

	p.logger.DebugContext(ctx, "eventing: published",
		slog.String("topic", ev.Topic), slog.String("instance_id", ev.InstanceID),
		slog.String("dedup_key", ev.DedupKey))
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes (GREEN)**

Run: `go test ./internal/eventing/watermill/...`
Expected: PASS.

- [ ] **Step 6: Write the failing logger-bridge test**

Create `internal/eventing/watermill/logger_test.go`:

```go
package watermill_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/stretchr/testify/require"
	watermillpub "github.com/kartaladev/wrkflw/internal/eventing/watermill"
)

func TestNewWatermillLoggerForwardsToSlog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var wl watermill.LoggerAdapter = watermillpub.NewWatermillLogger(logger)
	wl.Info("subscriber started", watermill.LogFields{"topic": "instance.completed"})

	out := buf.String()
	require.Contains(t, out, "subscriber started")
	require.Contains(t, out, "instance.completed")
	require.True(t, strings.Contains(out, "topic"))
}
```

- [ ] **Step 7: Run test to verify it fails (RED)**

Run: `go test -run '^TestNewWatermillLoggerForwardsToSlog$' ./internal/eventing/watermill/...`
Expected: BUILD FAILURE — `NewWatermillLogger` undefined.

- [ ] **Step 8: Implement the logger bridge**

Create `internal/eventing/watermill/logger.go`:

```go
package watermill

import (
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
)

// NewWatermillLogger returns a watermill.LoggerAdapter that forwards to l.
// Use it to unify watermill's internal logs (e.g. GoChannel) with the app's
// slog output.
func NewWatermillLogger(l *slog.Logger) watermill.LoggerAdapter {
	return &slogLogger{logger: l}
}

type slogLogger struct {
	logger *slog.Logger
}

func (s *slogLogger) Error(msg string, err error, fields watermill.LogFields) {
	s.logger.Error(msg, append(fieldsToArgs(fields), slog.Any("error", err))...)
}

func (s *slogLogger) Info(msg string, fields watermill.LogFields) {
	s.logger.Info(msg, fieldsToArgs(fields)...)
}

func (s *slogLogger) Debug(msg string, fields watermill.LogFields) {
	s.logger.Debug(msg, fieldsToArgs(fields)...)
}

func (s *slogLogger) Trace(msg string, fields watermill.LogFields) {
	s.logger.Debug(msg, fieldsToArgs(fields)...)
}

func (s *slogLogger) With(fields watermill.LogFields) watermill.LoggerAdapter {
	return &slogLogger{logger: s.logger.With(fieldsToArgs(fields)...)}
}

func fieldsToArgs(fields watermill.LogFields) []any {
	args := make([]any, 0, len(fields))
	for k, v := range fields {
		args = append(args, slog.Any(k, v))
	}
	return args
}
```

- [ ] **Step 9: Run tests to verify they pass (GREEN)**

Run: `go test -race ./internal/eventing/watermill/...`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add go.mod go.sum internal/eventing/watermill/
git commit -m "$(printf 'feat(eventing): watermill Publisher adapter with slog bridge\n\nMap one OutboxEvent to one watermill message (UUID=dedup_key, instance_id\nmetadata, JSON payload) and provide a slog-backed watermill.LoggerAdapter.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: Observability — OTel span + published counter

Extend the adapter to emit one span per publish and a `wrkflw_eventing_published_total{status}` counter.

**Files:**
- Modify: `internal/eventing/watermill/options.go` (add provider options)
- Modify: `internal/eventing/watermill/publisher.go` (span + counter)
- Test: `internal/eventing/watermill/observability_test.go`

**Interfaces:**
- Produces: `watermillpub.WithTracerProvider(tp trace.TracerProvider) Option`, `watermillpub.WithMeterProvider(mp metric.MeterProvider) Option`.

- [ ] **Step 1: Write the failing observability test**

Create `internal/eventing/watermill/observability_test.go`:

```go
package watermill_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	watermillpub "github.com/kartaladev/wrkflw/internal/eventing/watermill"
	"github.com/kartaladev/wrkflw/runtime"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestPublishRecordsSpanAndCounter(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))

	pub := watermillpub.NewPublisher(&fakePub{},
		watermillpub.WithTracerProvider(tp),
		watermillpub.WithMeterProvider(mp))

	err := pub.Publish(context.Background(), runtime.OutboxEvent{
		Topic: "instance.completed", Payload: map[string]any{"ok": true}, InstanceID: "inst-9",
	})
	require.NoError(t, err)

	spans := sr.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "eventing.publish", spans[0].Name())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "wrkflw_eventing_published_total" {
				found = true
			}
		}
	}
	require.True(t, found, "published counter must be recorded")
}
```

- [ ] **Step 2: Run test to verify it fails (RED)**

Run: `go test -run '^TestPublishRecordsSpanAndCounter$' ./internal/eventing/watermill/...`
Expected: BUILD FAILURE — `WithTracerProvider`/`WithMeterProvider` undefined (and the SDK test deps download).

- [ ] **Step 3: Add provider options**

Append to `internal/eventing/watermill/options.go` (and add imports `"go.opentelemetry.io/otel/metric"` and `"go.opentelemetry.io/otel/trace"`, extend `config`):

```go
// config gains the provider fields:
//   tp trace.TracerProvider
//   mp metric.MeterProvider

// WithTracerProvider sets the tracer provider (default: otel global).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) {
		if tp != nil {
			c.tp = tp
		}
	}
}

// WithMeterProvider sets the meter provider (default: otel global).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) {
		if mp != nil {
			c.mp = mp
		}
	}
}
```

Resulting `config`:

```go
type config struct {
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
}
```

- [ ] **Step 4: Wire span + counter into the adapter**

Update `internal/eventing/watermill/publisher.go`. Add fields and build them in `NewPublisher`; wrap `Publish`. Imports add `"go.opentelemetry.io/otel"`, `"go.opentelemetry.io/otel/attribute"`, `"go.opentelemetry.io/otel/codes"`, `"go.opentelemetry.io/otel/metric"`, `"go.opentelemetry.io/otel/trace"`.

```go
const instrumentationName = "github.com/kartaladev/wrkflw/eventing"

type Publisher struct {
	pub       message.Publisher
	logger    *slog.Logger
	tracer    trace.Tracer
	published metric.Int64Counter
}

func NewPublisher(pub message.Publisher, opts ...Option) *Publisher {
	cfg := config{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	tp := cfg.tp
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	mp := cfg.mp
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	counter, err := mp.Meter(instrumentationName).Int64Counter(
		"wrkflw_eventing_published_total",
		metric.WithDescription("Count of outbox events published to the broker."),
	)
	if err != nil {
		// Never fail construction over a metric; fall back to a no-op counter.
		counter, _ = noop.NewMeterProvider().Meter(instrumentationName).Int64Counter("wrkflw_eventing_published_total")
		cfg.logger.Warn("eventing: counter init failed; using no-op", slog.Any("error", err))
	}
	return &Publisher{
		pub:       pub,
		logger:    cfg.logger,
		tracer:    tp.Tracer(instrumentationName),
		published: counter,
	}
}
```

(Add import `"go.opentelemetry.io/otel/metric/noop"` for the fallback.) Wrap `Publish` body:

```go
func (p *Publisher) Publish(ctx context.Context, ev runtime.OutboxEvent) error {
	ctx, span := p.tracer.Start(ctx, "eventing.publish", trace.WithAttributes(
		attribute.String("messaging.destination", ev.Topic),
		attribute.String("wrkflw.instance_id", ev.InstanceID),
	))
	defer span.End()

	err := p.publish(ctx, ev) // existing marshal+publish logic moved into publish()
	status := "ok"
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	p.published.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
	return err
}
```

Rename the existing marshal+publish+log body to an unexported `func (p *Publisher) publish(ctx context.Context, ev runtime.OutboxEvent) error` (same body as Task 2, minus the span — keep the slog calls). The `msg.SetContext(ctx)` now carries the active span context.

- [ ] **Step 5: Run tests to verify they pass (GREEN)**

Run: `go test -race ./internal/eventing/watermill/...`
Expected: PASS (the mapping/logger tests from Task 2 still pass; the new span+counter test passes).

- [ ] **Step 6: Commit**

```bash
git add internal/eventing/watermill/ go.mod go.sum
git commit -m "$(printf 'feat(eventing): trace span and published counter on publish\n\nEmit an eventing.publish span and a wrkflw_eventing_published_total{status}\ncounter per Publish, defaulting to the OTel global providers.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 4: Root façade `eventing.NewPublisher` + options

The consumer-facing package that wraps any watermill publisher and returns `runtime.Publisher`.

**Files:**
- Create: `eventing/eventing.go`
- Test: `eventing/eventing_test.go`

**Interfaces:**
- Consumes: `watermillpub.NewPublisher`, `watermillpub.With*` (Tasks 2–3).
- Produces:
  - `eventing.NewPublisher(pub message.Publisher, opts ...Option) runtime.Publisher`
  - `eventing.WithLogger(*slog.Logger) Option`, `eventing.WithTracerProvider(trace.TracerProvider) Option`, `eventing.WithMeterProvider(metric.MeterProvider) Option`

- [ ] **Step 1: Write the failing façade test**

Create `eventing/eventing_test.go`:

```go
package eventing_test

import (
	"context"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/require"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime"
)

type fakePub struct{ published int }

func (f *fakePub) Publish(_ string, _ ...*message.Message) error { f.published++; return nil }
func (f *fakePub) Close() error                                  { return nil }

func TestNewPublisherReturnsWorkingRuntimePublisher(t *testing.T) {
	fp := &fakePub{}
	var pub runtime.Publisher = eventing.NewPublisher(fp)
	err := pub.Publish(context.Background(), runtime.OutboxEvent{
		Topic: "instance.completed", Payload: map[string]any{"ok": true}, DedupKey: "i:1:0",
	})
	require.NoError(t, err)
	require.Equal(t, 1, fp.published)
}
```

- [ ] **Step 2: Run test to verify it fails (RED)**

Run: `go test ./eventing/...`
Expected: BUILD FAILURE — package `eventing` / `NewPublisher` undefined.

- [ ] **Step 3: Implement the façade**

Create `eventing/eventing.go`:

```go
// Package eventing is the consumer-facing façade for publishing wrkflw domain
// events to a broker via watermill. Wrap any watermill message.Publisher with
// NewPublisher and hand the result to persistence.NewRelay. watermill is
// confined to this package and internal/eventing/watermill; engine/model/runtime
// never import it.
package eventing

import (
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	watermillpub "github.com/kartaladev/wrkflw/internal/eventing/watermill"
	"github.com/kartaladev/wrkflw/runtime"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Compile-time guard: the internal adapter satisfies the public port.
var _ runtime.Publisher = (*watermillpub.Publisher)(nil)

// Option configures a publisher.
type Option func(*options)

type options struct {
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
}

// WithLogger sets the structured logger (default slog.Default()).
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithTracerProvider sets the tracer provider (default: otel global).
func WithTracerProvider(tp trace.TracerProvider) Option { return func(o *options) { o.tp = tp } }

// WithMeterProvider sets the meter provider (default: otel global).
func WithMeterProvider(mp metric.MeterProvider) Option { return func(o *options) { o.mp = mp } }

// NewPublisher wraps a watermill message.Publisher as a runtime.Publisher,
// mapping each OutboxEvent to a watermill message.
func NewPublisher(pub message.Publisher, opts ...Option) runtime.Publisher {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	return watermillpub.NewPublisher(pub, toInternal(o)...)
}

func toInternal(o options) []watermillpub.Option {
	var out []watermillpub.Option
	if o.logger != nil {
		out = append(out, watermillpub.WithLogger(o.logger))
	}
	if o.tp != nil {
		out = append(out, watermillpub.WithTracerProvider(o.tp))
	}
	if o.mp != nil {
		out = append(out, watermillpub.WithMeterProvider(o.mp))
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes (GREEN)**

Run: `go test -race ./eventing/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add eventing/eventing.go eventing/eventing_test.go
git commit -m "$(printf 'feat(eventing): root facade NewPublisher over watermill adapter\n\nConsumer-facing eventing.NewPublisher returns runtime.Publisher and keeps\nwatermill behind the internal adapter; options forward logger and OTel\nproviders.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: GoChannel convenience + e2e round-trip Example

In-process pub/sub helper plus a testable Example proving the full publish→receive path.

**Files:**
- Modify: `eventing/eventing.go` (add `NewGoChannelPublisher`)
- Test: `eventing/gochannel_test.go`
- Test: `eventing/eventing_example_test.go`

**Interfaces:**
- Produces: `eventing.NewGoChannelPublisher(opts ...Option) (runtime.Publisher, message.Subscriber, io.Closer)`

- [ ] **Step 1: Write the failing round-trip test**

Create `eventing/gochannel_test.go`:

```go
package eventing_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime"
)

func TestGoChannelPublisherRoundTrip(t *testing.T) {
	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer func() { require.NoError(t, closer.Close()) }()

	msgs, err := sub.Subscribe(t.Context(), "instance.completed")
	require.NoError(t, err)

	require.NoError(t, pub.Publish(t.Context(), runtime.OutboxEvent{
		Topic:      "instance.completed",
		Payload:    map[string]any{"order": "A-1"},
		DedupKey:   "inst-1:1:0",
		InstanceID: "inst-1",
	}))

	msg := <-msgs
	require.Equal(t, "inst-1:1:0", msg.UUID)
	require.Equal(t, "inst-1", msg.Metadata.Get("instance_id"))
	require.JSONEq(t, `{"order":"A-1"}`, string(msg.Payload))
	msg.Ack()
}
```

- [ ] **Step 2: Run test to verify it fails (RED)**

Run: `go test -run '^TestGoChannelPublisherRoundTrip$' ./eventing/...`
Expected: BUILD FAILURE — `NewGoChannelPublisher` undefined.

- [ ] **Step 3: Implement `NewGoChannelPublisher`**

Append to `eventing/eventing.go` (add imports `"io"`, `"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"`):

```go
// NewGoChannelPublisher builds an in-process GoChannel pub/sub and returns a
// runtime.Publisher over it, the matching Subscriber (for in-process consumers
// or tests), and an io.Closer to release it. No external broker is required.
// GoChannel ships in watermill core, so this adds no broker dependency.
func NewGoChannelPublisher(opts ...Option) (runtime.Publisher, message.Subscriber, io.Closer) {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	gc := gochannel.NewGoChannel(gochannel.Config{}, watermillpub.NewWatermillLogger(logger))
	return NewPublisher(gc, opts...), gc, gc
}
```

- [ ] **Step 4: Run test to verify it passes (GREEN)**

Run: `go test -race ./eventing/...`
Expected: PASS.

- [ ] **Step 5: Write the testable Example (godoc)**

Create `eventing/eventing_example_test.go`:

```go
package eventing_test

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/eventing"
	"github.com/kartaladev/wrkflw/runtime"
)

// ExampleNewGoChannelPublisher shows publishing an outbox event in-process and
// receiving it on the subscriber side.
func ExampleNewGoChannelPublisher() {
	pub, sub, closer := eventing.NewGoChannelPublisher()
	defer closer.Close()

	ctx := context.Background()
	msgs, _ := sub.Subscribe(ctx, "instance.completed")

	_ = pub.Publish(ctx, runtime.OutboxEvent{
		Topic:      "instance.completed",
		Payload:    map[string]any{"order": "A-1"},
		DedupKey:   "inst-1:1:0",
		InstanceID: "inst-1",
	})

	msg := <-msgs
	fmt.Println(msg.Metadata.Get("instance_id"), string(msg.Payload))
	msg.Ack()
	// Output: inst-1 {"order":"A-1"}
}
```

- [ ] **Step 6: Run the example to verify output (GREEN)**

Run: `go test -race -run '^ExampleNewGoChannelPublisher$' ./eventing/...`
Expected: PASS (output matches).

- [ ] **Step 7: Commit**

```bash
git add eventing/eventing.go eventing/gochannel_test.go eventing/eventing_example_test.go
git commit -m "$(printf 'feat(eventing): in-process GoChannel publisher + e2e example\n\nNewGoChannelPublisher returns a Publisher, Subscriber, and Closer for\nin-process eventing and tests; a testable Example documents the full\npublish to receive round-trip.\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 6: Verification gate + HANDOVER update

Run the whole-repo gate, prove the vendor-isolation guard, and record the sub-project in HANDOVER.

**Files:**
- Modify: `docs/plans/HANDOVER.md` (add Eventing section; flip the "What's next" bullet)

- [ ] **Step 1: Full race test suite**

Run: `go test -race ./...`
Expected: all green (requires Docker for the Postgres relay test). If any failure, fix before proceeding.

- [ ] **Step 2: Coverage on touched packages**

Run: `go test -coverprofile=cover.out ./eventing/... ./internal/eventing/... && go tool cover -func=cover.out | tail -1`
Expected: ≥ 85% total for the touched packages. If below, add table cases to `publisher_test.go` (e.g. marshal-error path with a payload containing a non-serializable value such as a channel, asserting the wrapped "marshal payload" error).

- [ ] **Step 3: Lint**

Run: `golangci-lint run ./...`
Expected: 0 issues.

- [ ] **Step 4: Vendor-isolation guard**

Run: `grep -RIl "ThreeDotsLabs/watermill" --include='*.go' engine model runtime || echo "CLEAN: no watermill in engine/model/runtime"`
Expected: prints `CLEAN: ...` (no matches). If any file is listed, the import leaked — move it behind the façade.

- [ ] **Step 5: Update HANDOVER.md**

Add an "Eventing (watermill) sub-project — ✅ COMPLETE" section to `docs/plans/HANDOVER.md` mirroring the Scheduling/Authz sections (what shipped table, ADR-0012, deferred follow-ups: broker-specific constructors, richer event envelope/topic-mapping, retry/DLQ poison isolation, optional LISTEN/NOTIFY relay trigger). Flip the `What's next` "Eventing" bullet to point at the new section.

- [ ] **Step 6: Commit**

```bash
git add docs/plans/HANDOVER.md
git commit -m "$(printf 'docs(eventing): record watermill sub-project completion in HANDOVER\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

## Self-review notes (author)

- **Spec coverage:** §3 → Task 1; §5 `Publish` mapping → Task 2; §7 observability → Tasks 2 (slog) + 3 (OTel); §6 façade → Task 4; §1/§4 GoChannel + §8 e2e Example → Task 5; §11 gate + §10 dep → Tasks 2/6. All spec sections map to a task.
- **Type consistency:** `runtime.OutboxEvent{Topic,Payload,DedupKey,InstanceID}` used identically across Tasks 1–5; `watermillpub.NewPublisher`/`WithLogger`/`WithTracerProvider`/`WithMeterProvider`/`NewWatermillLogger` defined in Tasks 2–3 and consumed in Tasks 4–5 with matching signatures; façade `NewPublisher`/`NewGoChannelPublisher` signatures match their tests.
- **Watermill API anchors:** `message.NewMessage(uuid string, payload []byte)`, `msg.UUID`, `msg.Metadata.Set/Get`, `msg.SetContext`, `message.Publisher{Publish(topic, ...*Message) error; Close() error}`, `message.Subscriber.Subscribe(ctx, topic) (<-chan *Message, error)`, `watermill.NewUUID()`, `watermill.LoggerAdapter`, `gochannel.NewGoChannel(Config, LoggerAdapter)`. Verify these against the resolved watermill version during Task 2; if the minor API differs, trust the compiler/tests over this listing.
