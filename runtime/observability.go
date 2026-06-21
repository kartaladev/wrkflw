package runtime

import (
	"go.opentelemetry.io/otel/metric"

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

