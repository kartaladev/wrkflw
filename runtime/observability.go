package runtime

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/observability"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// driverObs bundles the driver's telemetry and pre-built process instruments.
// It is always non-nil after [NewProcessDriver] (defaults to noop providers + slog.Default()).
type driverObs struct {
	tel observability.Telemetry

	instStarted       metric.Int64Counter
	instCompleted     metric.Int64Counter
	instActive        metric.Int64UpDownCounter
	stepDuration      metric.Float64Histogram
	actionDuration    metric.Float64Histogram
	actionRetries     metric.Int64Counter
	actionFailures    metric.Int64Counter
	timerFired        metric.Int64Counter
	timerArmsRefused  metric.Int64Counter
	incidentsRaised   metric.Int64Counter
	incidentsResolved metric.Int64Counter
	humanTasks        metric.Int64Counter
}

// newDriverObs constructs a driverObs from the given observability options.
// Nil options (unset signal options) are silently dropped so [observability.New]
// only sees real, non-nil options.
func newDriverObs(opts ...observability.Option) *driverObs {
	// Filter out nil options (fields that were never set by a With* option).
	var real []observability.Option
	for _, o := range opts {
		if o != nil {
			real = append(real, o)
		}
	}
	tel := observability.New(kernel.InstrumentationScope, real...)
	return &driverObs{
		tel:               tel,
		instStarted:       tel.Int64Counter("wrkflw_instances_started_total", "Process instances started."),
		instCompleted:     tel.Int64Counter("wrkflw_instances_completed_total", "Process instances that reached a terminal state."),
		instActive:        tel.Int64UpDownCounter("wrkflw_instances_active", "Currently live (non-terminal) process instances."),
		stepDuration:      tel.Float64Histogram("wrkflw_step_duration_seconds", "Duration of a single engine.Step call."),
		actionDuration:    tel.Float64Histogram("wrkflw_action_duration_seconds", "Duration of a service-action invocation."),
		actionRetries:     tel.Int64Counter("wrkflw_action_retries_total", "Service-action retries scheduled."),
		actionFailures:    tel.Int64Counter("wrkflw_action_failures_total", "Service-action invocations that returned an error."),
		timerFired:        tel.Int64Counter("wrkflw_timer_fired_total", "Timer callbacks that fired and delivered a TimerFired trigger."),
		timerArmsRefused:  tel.Int64Counter("wrkflw_timer_arms_refused_total", "Timer arms refused because the scheduler could not run them (ADR-0176)."),
		incidentsRaised:   tel.Int64Counter("wrkflw_incidents_raised_total", "Incidents raised."),
		incidentsResolved: tel.Int64Counter("wrkflw_incidents_resolved_total", "Incidents resolved."),
		humanTasks:        tel.Int64Counter("wrkflw_human_tasks_total", "Human-task lifecycle transitions."),
	}
}

// tracer returns the OTel tracer scoped to the runner's instrumentation name.
func (o *driverObs) tracer() trace.Tracer {
	return o.tel.Tracer
}

// triggerName returns a stable, low-cardinality label for a trigger type.
// It strips the "engine." package prefix from the concrete Go type name so
// the label reads as, e.g., "StartInstance" rather than "engine.StartInstance".
func triggerName(t engine.Trigger) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", t), "engine.")
}
