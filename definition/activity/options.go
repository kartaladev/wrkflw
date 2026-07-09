package activity

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// Option-scoping convention: an option valid on only a subset of activity kinds
// MUST return a narrow anonymous interface embedding just those kinds' option
// interfaces (e.g. WithActionName returns interface { ServiceTaskOption;
// BusinessRuleOption }), so mis-applying it is a compile-time error. The broad
// activityOnlyOption type means "valid on EVERY activity kind" and is reserved
// for genuinely-universal options (WithRetryPolicy, WithCompensation,
// WithDeadline, WithRecoveryFlow, WithCancelHandler). There is no runtime lint
// pass; the type system is the guardrail.

// --- option interfaces ---

// ActivityOption is the functional-options type accepted by NewSubProcess and
// NewCallActivity (and satisfied by every shared activity option).
type ActivityOption interface {
	applyActivity(a *model.ActivityFields)
	applyName(b *model.Base)
}

// ServiceTaskOption configures a ServiceTask.
type ServiceTaskOption interface{ applyServiceTask(s *ServiceTask) }

// UserTaskOption configures a UserTask.
type UserTaskOption interface{ applyUserTask(u *UserTask) }

// ReceiveTaskOption configures a ReceiveTask.
type ReceiveTaskOption interface{ applyReceiveTask(r *ReceiveTask) }

// SendTaskOption configures a SendTask.
type SendTaskOption interface{ applySendTask(s *SendTask) }

// BusinessRuleOption configures a BusinessRuleTask.
type BusinessRuleOption interface{ applyBusinessRule(b *BusinessRuleTask) }

// applyActivityOpts applies ActivityOption values to a base + activity fields.
func applyActivityOpts(b *model.Base, a *model.ActivityFields, opts []ActivityOption) {
	for _, o := range opts {
		o.applyActivity(a)
		o.applyName(b)
	}
}

// --- WithName (accepted by every activity constructor) ---

type nameOpt struct{ name string }

func (o nameOpt) applyActivity(_ *model.ActivityFields) {}
func (o nameOpt) applyName(b *model.Base)               { b.SetName(o.name) }
func (o nameOpt) applyServiceTask(s *ServiceTask)       { s.SetName(o.name) }
func (o nameOpt) applyUserTask(u *UserTask)             { u.SetName(o.name) }
func (o nameOpt) applyReceiveTask(r *ReceiveTask)       { r.SetName(o.name) }
func (o nameOpt) applySendTask(s *SendTask)             { s.SetName(o.name) }
func (o nameOpt) applyBusinessRule(b *BusinessRuleTask) { b.SetName(o.name) }

// WithName sets the display name on any activity node.
func WithName(name string) nameOpt { return nameOpt{name} }

// --- action options (ServiceTask + BusinessRuleTask) ---

type actionNameOpt struct{ name string }

func (o actionNameOpt) applyServiceTask(s *ServiceTask)       { s.Action = o.name }
func (o actionNameOpt) applyBusinessRule(b *BusinessRuleTask) { b.Action = o.name }

// WithActionName sets the catalog action name. Resolved scoped→global at runtime.
// Mutually exclusive with WithAction/WithActionFunc (Build reports a conflict).
func WithActionName(name string) interface {
	ServiceTaskOption
	BusinessRuleOption
} {
	return actionNameOpt{name}
}

type inlineActionOpt struct{ a action.Action }

func (o inlineActionOpt) applyServiceTask(s *ServiceTask)       { s.Inline = o.a }
func (o inlineActionOpt) applyBusinessRule(b *BusinessRuleTask) { b.Inline = o.a }

// WithAction attaches a node-local inline action.Action available to this node
// only. Mutually exclusive with WithActionName (Build reports a conflict). Inline
// actions are never serialized; a persisted definition must re-attach them in code.
func WithAction(a action.Action) interface {
	ServiceTaskOption
	BusinessRuleOption
} {
	return inlineActionOpt{a}
}

// WithActionFunc is WithAction sugar wrapping a plain function as action.ActionFunc.
func WithActionFunc(fn func(context.Context, map[string]any) (map[string]any, error)) interface {
	ServiceTaskOption
	BusinessRuleOption
} {
	return inlineActionOpt{actionFunc(fn)}
}

// --- shared activity-field options (work on all activity constructors) ---

// activityOnlyOption wraps a function that mutates ActivityFields only. Its
// concrete type satisfies every activity option interface at once.
type activityOnlyOption struct {
	fn func(*model.ActivityFields)
}

func (o activityOnlyOption) applyActivity(a *model.ActivityFields) { o.fn(a) }
func (activityOnlyOption) applyName(_ *model.Base)                 {}
func (o activityOnlyOption) applyServiceTask(s *ServiceTask)       { o.fn(&s.ActivityFields) }
func (o activityOnlyOption) applyUserTask(u *UserTask)             { o.fn(&u.ActivityFields) }
func (o activityOnlyOption) applyReceiveTask(r *ReceiveTask)       { o.fn(&r.ActivityFields) }
func (o activityOnlyOption) applySendTask(s *SendTask)             { o.fn(&s.ActivityFields) }
func (o activityOnlyOption) applyBusinessRule(b *BusinessRuleTask) { o.fn(&b.ActivityFields) }

func withActivity(fn func(*model.ActivityFields)) activityOnlyOption {
	return activityOnlyOption{fn}
}

// WithRetryPolicy sets the per-node RetryPolicy (nil = use runtime default).
func WithRetryPolicy(p *model.RetryPolicy) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.RetryPolicy = p })
}

// WithRecoveryFlow sets the flow taken when retries are exhausted.
func WithRecoveryFlow(flowID string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.RecoveryFlow = flowID })
}

// WithCompensation sets the action.Action name invoked during rollback.
func WithCompensation(action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.CompensationAction = action })
}

// WithCancelHandler sets the action.Action run when the node is interrupted.
func WithCancelHandler(action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.CancelHandler = action })
}

// WithDeadline sets the DeadlineTimer (schedule.TriggerSpec), DeadlineFlow, and
// DeadlineAction on an activity node. Use schedule.AfterDuration, schedule.AfterExpr,
// or any other TriggerSpec constructor.
func WithDeadline(t schedule.TriggerSpec, flowID, action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.DeadlineTimer, a.DeadlineFlow, a.DeadlineAction = t, flowID, action })
}

// reminderOpt narrows WithWaitReminder to only the activity kinds whose engine
// strategy actually arms an in-wait reminder: UserTask and ReceiveTask.
// IntermediateCatchEvent uses the event-side event.WithCatchWaitReminder.
// Applying a reminder to any other activity kind (ServiceTask, SendTask,
// BusinessRuleTask, SubProcess, CallActivity) is a compile-time error.
type reminderOpt struct {
	every  schedule.TriggerSpec
	action string
}

func (o reminderOpt) applyUserTask(u *UserTask) {
	u.ReminderEvery, u.ReminderAction = o.every, o.action
}

func (o reminderOpt) applyReceiveTask(r *ReceiveTask) {
	r.ReminderEvery, r.ReminderAction = o.every, o.action
}

// WithWaitReminder sets the ReminderEvery (schedule.TriggerSpec) and ReminderAction
// on a UserTask or ReceiveTask — the only activity kinds whose engine strategy arms
// an in-wait reminder. Passing it to any other activity constructor is a compile
// error (see reminderOpt). Use schedule.Every, schedule.EveryExpr, or any other
// recurring TriggerSpec constructor. For IntermediateCatchEvent, use
// event.WithCatchWaitReminder instead.
func WithWaitReminder(t schedule.TriggerSpec, action string) interface {
	UserTaskOption
	ReceiveTaskOption
} {
	return reminderOpt{t, action}
}

// completionActionOpt narrows WithCompletionAction to only the activity kinds
// whose engine strategy runs a completion action: UserTask and ReceiveTask.
// Applying a completion action to any other activity kind is a compile-time error.
type completionActionOpt struct{ action string }

func (o completionActionOpt) applyUserTask(u *UserTask)       { u.CompletionAction = o.action }
func (o completionActionOpt) applyReceiveTask(r *ReceiveTask) { r.CompletionAction = o.action }

// WithCompletionAction attaches a catalog action run when a UserTask or
// ReceiveTask completion is triggered (human completion / message receive),
// after the completion input is merged. Its returned vars merge into the
// instance variables. Failure is governed by the node's WithRetryPolicy /
// error boundary (same machinery as a ServiceTask action). Distinct from
// WithCompletionValidation, which gates the completion input; this runs after it.
func WithCompletionAction(name string) interface {
	UserTaskOption
	ReceiveTaskOption
} {
	return completionActionOpt{name}
}

// --- UserTask-only options ---

type eligibilityExprOpt struct{ expr string }

func (o eligibilityExprOpt) applyUserTask(u *UserTask) { u.EligibilityExpr = o.expr }

// WithEligibilityExpr sets a UserTask attribute-eligibility predicate (expr).
// It may only be passed to NewUserTask.
func WithEligibilityExpr(expr string) UserTaskOption { return eligibilityExprOpt{expr} }

type eligibilityPrivilegesOpt struct{ privs []string }

func (o eligibilityPrivilegesOpt) applyUserTask(u *UserTask) {
	u.EligibilityPrivileges = append(u.EligibilityPrivileges, o.privs...)
}

// WithEligibilityPrivileges sets resource-privilege tokens on a UserTask. Each
// token is a space-separated "object action" pair. Multiple calls are additive.
// It may only be passed to NewUserTask.
func WithEligibilityPrivileges(privs ...string) UserTaskOption {
	return eligibilityPrivilegesOpt{privs: privs}
}

type completionValidationOpt struct{ s validate.ValidationStrategy }

func (o completionValidationOpt) applyUserTask(u *UserTask) { u.CompletionValidation = o.s }

// WithCompletionValidation validates a UserTask's completion output before it
// is applied. It may only be passed to NewUserTask.
func WithCompletionValidation(s validate.ValidationStrategy) UserTaskOption {
	return completionValidationOpt{s: s}
}

// --- Receive/Send options ---

type correlationKeyOpt struct{ key string }

func (o correlationKeyOpt) applyReceiveTask(r *ReceiveTask) { r.CorrelationKey = o.key }
func (o correlationKeyOpt) applySendTask(s *SendTask)       { s.CorrelationKey = o.key }

// WithCorrelationKey sets CorrelationKey on a ReceiveTask or SendTask. It may only
// be passed to NewReceiveTask or NewSendTask.
func WithCorrelationKey(key string) interface {
	ReceiveTaskOption
	SendTaskOption
} {
	return correlationKeyOpt{key}
}

type receivePayloadValidationOpt struct{ s validate.ValidationStrategy }

func (o receivePayloadValidationOpt) applyReceiveTask(r *ReceiveTask) { r.PayloadValidation = o.s }

// WithPayloadValidation validates a ReceiveTask's message payload before it is
// applied. It may only be passed to NewReceiveTask.
func WithPayloadValidation(s validate.ValidationStrategy) ReceiveTaskOption {
	return receivePayloadValidationOpt{s: s}
}
