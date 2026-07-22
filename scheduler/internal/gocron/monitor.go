package gocron

import (
	"context"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/kartaladev/wrkflw/scheduler/internal/obs"
)

// Metric instrument names emitted by monitorStatus (ADR-0134 production item
// ① — production observability via gocron's native Monitor/EventListener
// hooks). Both are wired into the gocron engine at construction time via
// gocron.WithMonitorStatus (scheduler.go).
const (
	// jobRunsTotalMetric counts job fires by outcome. Attributes:
	//   - "status": one of gocron's JobStatus values (success|fail|skip|
	//     singleton_rescheduled — see statusString), or "unknown" when the
	//     calling gocron hook does not supply a status (RecordJobTiming).
	//   - "job_id": the timer/job identifier ScheduleJob was called with
	//     (job_schedule.go's gocron.WithName(id)) — i.e. the "name" string
	//     gocron's hooks pass, NOT gocron's own per-run uuid.UUID.
	//
	// Cardinality note: keying by job_id is intentionally high-cardinality —
	// one series per distinct timer/job id, not per timer kind. This is an
	// accepted trade-off (see the package doc on monitorStatus below): the
	// whole point of this metric is per-timer attribution, which a
	// lower-cardinality label would erase.
	jobRunsTotalMetric = "wrkflw_scheduler_job_runs_total"

	// jobDurationSecondsMetric records the wall-clock duration of each job
	// run. Same attribute set and cardinality note as jobRunsTotalMetric.
	jobDurationSecondsMetric = "wrkflw_scheduler_job_duration_seconds"

	// unknownStatus is the fallback "status" attribute value used wherever a
	// gocron hook does not supply (or supplies an unrecognized) JobStatus —
	// see IncrementJob is not that case (gocron does pass a real status
	// there); RecordJobTiming is the one hook with no status parameter at
	// all, and statusString falls back here for any future JobStatus value
	// this mapping doesn't yet know about.
	unknownStatus = "unknown"
)

// monitorStatus implements gocron.MonitorStatus (which embeds gocron.Monitor)
// backed by the Task-1 obs shim's meter (obs.Telemetry). It is wired into the
// gocron engine via gocron.WithMonitorStatus at construction (scheduler.go).
//
// gocron's executor drives monitorStatus from a single call site per job run
// — RecordJobTimingWithStatus (executor.go), invoked once per run for every
// outcome (success, error, and a recovered panic, which the executor reports
// as status=Fail with an error wrapping gocron.ErrPanicRecovered). That is
// the sole method this type needs for the engine's actual metrics; the plain
// Monitor methods (IncrementJob, RecordJobTiming) are implemented only for
// interface completeness — see their own doc comments.
//
// Cardinality: every instrument here is keyed by "job_id" = the caller-
// supplied timer/job id (see jobRunsTotalMetric's doc). This is a deliberate,
// accepted trade-off (ADR-0134): wrkflw's timers/jobs are a bounded,
// operationally meaningful set registered by the consumer, and per-timer
// attribution is the signal this metric exists to provide — collapsing
// job_id away would erase exactly what operators need to see.
type monitorStatus struct {
	runsTotal metric.Int64Counter
	duration  metric.Float64Histogram
}

// compile-time interface assertion: monitorStatus must satisfy
// gocron.MonitorStatus (Monitor embedded).
var _ gocron.MonitorStatus = (*monitorStatus)(nil)

// newMonitorStatus builds a monitorStatus backed by tel's meter.
func newMonitorStatus(tel obs.Telemetry) *monitorStatus {
	return &monitorStatus{
		runsTotal: tel.Int64Counter(jobRunsTotalMetric,
			"Count of gocron job runs by outcome status, keyed by job_id (ADR-0134)."),
		duration: tel.Float64Histogram(jobDurationSecondsMetric,
			"Duration in seconds of gocron job runs, keyed by status and job_id (ADR-0134)."),
	}
}

// IncrementJob implements gocron.Monitor (embedded in gocron.MonitorStatus).
// gocron only invokes a Monitor's IncrementJob when a plain gocron.Monitor is
// registered via gocron.WithMonitor — the engine registers monitorStatus
// solely via gocron.WithMonitorStatus (scheduler.go), so gocron never calls
// this method in the engine's actual configuration today. It is implemented
// for interface completeness (Monitor is embedded in MonitorStatus) and
// routes into the same runsTotal counter RecordJobTimingWithStatus drives,
// using the real status gocron does supply to this method.
func (m *monitorStatus) IncrementJob(_ uuid.UUID, name string, _ []string, status gocron.JobStatus) {
	m.runsTotal.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("status", statusString(status)),
		attribute.String("job_id", name),
	))
}

// RecordJobTiming implements gocron.Monitor (embedded in gocron.MonitorStatus).
// Like IncrementJob, gocron only calls this when a plain gocron.Monitor is
// registered via gocron.WithMonitor, which the engine does not do — it
// registers monitorStatus only via gocron.WithMonitorStatus (scheduler.go).
// Implemented for interface completeness; this method's signature carries no
// JobStatus, so the recorded point uses status="unknown".
func (m *monitorStatus) RecordJobTiming(startTime, endTime time.Time, _ uuid.UUID, name string, _ []string) {
	m.duration.Record(context.Background(), endTime.Sub(startTime).Seconds(), metric.WithAttributes(
		attribute.String("status", unknownStatus),
		attribute.String("job_id", name),
	))
}

// RecordJobTimingWithStatus implements gocron.MonitorStatus. This is the
// method gocron's executor actually calls (once per job run, for both
// success and failure — including a panic recovered via
// AfterJobRunsWithPanic, which the executor still reports through here as
// status=Fail with an error wrapping gocron.ErrPanicRecovered), so it is the
// single source of truth for both instruments: it increments runsTotal and
// records the run's duration together, attributed by the real status and the
// job's name (job_id).
func (m *monitorStatus) RecordJobTimingWithStatus(startTime, endTime time.Time, _ uuid.UUID, name string, _ []string, status gocron.JobStatus, _ error) {
	attrs := metric.WithAttributes(
		attribute.String("status", statusString(status)),
		attribute.String("job_id", name),
	)
	m.runsTotal.Add(context.Background(), 1, attrs)
	m.duration.Record(context.Background(), endTime.Sub(startTime).Seconds(), attrs)
}

// statusString normalizes a gocron.JobStatus to its metric attribute value,
// falling back to unknownStatus for anything outside gocron's four
// documented statuses (success|fail|skip|singleton_rescheduled). This guards
// against a future gocron release adding a status value this mapping doesn't
// yet know about, rather than emitting an arbitrary/blank attribute value.
func statusString(status gocron.JobStatus) string {
	switch status {
	case gocron.Success, gocron.Fail, gocron.Skip, gocron.SingletonRescheduled:
		return string(status)
	default:
		return unknownStatus
	}
}
