package runtime

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
)

const runnerInstrumentationName = "github.com/zakyalvan/krtlwrkflw/runtime"

// runnerObs bundles the runner's telemetry and pre-built process instruments.
// It is always non-nil after [NewRunner] (defaults to noop providers + slog.Default()).
type runnerObs struct {
	tel observability.Telemetry

	instStarted       metric.Int64Counter
	instCompleted     metric.Int64Counter
	instActive        metric.Int64UpDownCounter
	stepDuration      metric.Float64Histogram
	actionDuration    metric.Float64Histogram
	actionRetries     metric.Int64Counter
	incidentsRaised   metric.Int64Counter
	incidentsResolved metric.Int64Counter
	humanTasks        metric.Int64Counter
}

// newRunnerObs constructs a runnerObs from the given observability options.
// Nil options (unset signal options) are silently dropped so [observability.New]
// only sees real, non-nil options.
func newRunnerObs(opts ...observability.Option) *runnerObs {
	// Filter out nil options (fields that were never set by a With* option).
	var real []observability.Option
	for _, o := range opts {
		if o != nil {
			real = append(real, o)
		}
	}
	tel := observability.New(runnerInstrumentationName, real...)
	return &runnerObs{
		tel:               tel,
		instStarted:       tel.Int64Counter("wrkflw_instances_started_total", "Process instances started."),
		instCompleted:     tel.Int64Counter("wrkflw_instances_completed_total", "Process instances that reached a terminal state."),
		instActive:        tel.Int64UpDownCounter("wrkflw_instances_active", "Currently live (non-terminal) process instances."),
		stepDuration:      tel.Float64Histogram("wrkflw_step_duration_seconds", "Duration of a single engine.Step call."),
		actionDuration:    tel.Float64Histogram("wrkflw_action_duration_seconds", "Duration of a service-action invocation."),
		actionRetries:     tel.Int64Counter("wrkflw_action_retries_total", "Service-action retries scheduled."),
		incidentsRaised:   tel.Int64Counter("wrkflw_incidents_raised_total", "Incidents raised."),
		incidentsResolved: tel.Int64Counter("wrkflw_incidents_resolved_total", "Incidents resolved."),
		humanTasks:        tel.Int64Counter("wrkflw_human_tasks_total", "Human-task lifecycle transitions."),
	}
}

// tracer returns the OTel tracer scoped to the runner's instrumentation name.
func (o *runnerObs) tracer() trace.Tracer {
	return o.tel.Tracer
}

// isTerminal reports whether s is a terminal process-instance status
// (completed, failed, or terminated).
func isTerminal(s engine.Status) bool {
	return s == engine.StatusCompleted || s == engine.StatusFailed || s == engine.StatusTerminated
}

// statusName maps a process-instance [engine.Status] to a stable, lowercase
// label string suitable for use as a metric attribute.
func statusName(s engine.Status) string {
	switch s {
	case engine.StatusCompleted:
		return "completed"
	case engine.StatusFailed:
		return "failed"
	case engine.StatusTerminated:
		return "terminated"
	case engine.StatusCompensating:
		return "compensating"
	default:
		return "running"
	}
}

// triggerName returns a stable, low-cardinality label for a trigger type.
// It strips the "engine." package prefix from the concrete Go type name so
// the label reads as, e.g., "StartInstance" rather than "engine.StartInstance".
func triggerName(t engine.Trigger) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", t), "engine.")
}

