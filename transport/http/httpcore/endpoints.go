package httpcore

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/view"
	"github.com/kartaladev/wrkflw/service"
)

// mapInstance applies mapper to st, defaulting to NewInstanceView when mapper is nil.
func mapInstance(mapper func(engine.InstanceState) any, st engine.InstanceState) any {
	if mapper == nil {
		return NewInstanceView(st)
	}
	return mapper(st)
}

// StartInstance starts a new process instance and returns (201, mappedBody, nil) on
// success. Validates in via Validate (&in) first; returns (0, nil, ErrBadInput-wrapping
// error) on validation failure without calling svc.
func StartInstance(ctx context.Context, svc service.Service, in StartInput, mapper func(engine.InstanceState) any) (int, any, error) {
	if err := Validate(&in); err != nil {
		return 0, nil, err
	}
	// validate:"required" does not reject a zero Qualifier struct (it is not a
	// zero scalar), so def_ref presence is enforced explicitly to preserve the
	// "missing def_ref → 400" behavior.
	if in.DefRef.ID == "" {
		return 0, nil, fmt.Errorf("%w: def_ref is required", ErrBadInput)
	}
	pi, err := svc.StartInstance(ctx, service.StartInstanceRequest{
		DefRef: in.DefRef,
		Vars:   in.Vars,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, mapInstance(mapper, pi.State()), nil
}

// GetInstance loads the current state of an existing instance and returns (200,
// mappedBody, nil) on success.
func GetInstance(ctx context.Context, svc service.Service, id string, mapper func(engine.InstanceState) any) (int, any, error) {
	pi, err := svc.GetInstance(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, mapInstance(mapper, pi.State()), nil
}

// GetInstanceSnapshot returns the process instance's self-serializing
// snapshot projection (200) — a consumer-safe view that omits internal engine
// bookkeeping (timers, armed events, scopes, etc.). The returned
// service.ProcessInstance marshals directly to that projection, so no
// transport-side view construction is needed.
//
// proj, when non-nil, projects the state before it is marshalled; withholdDef additionally
// drops the embedded definition (ADR-0190). Both come from the adapter's per-request
// disclosure decision — see [DisclosingProjection] and [WithholdDefinition].
//
// ⚠ The projected instance is built by [service.ProjectFor], NOT by
// service.NewProcessInstance. The latter always embeds the definition, so rebuilding
// through it silently RE-EMBEDDED a template that WithoutEmbeddedDefinition had suppressed
// — widening disclosure on the one route this narrows.
func GetInstanceSnapshot(
	ctx context.Context,
	svc service.Service,
	id string,
	proj func(engine.InstanceState) engine.InstanceState,
	withholdDef bool,
) (int, any, error) {
	pi, err := svc.GetInstance(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	if proj == nil && !withholdDef {
		return http.StatusOK, pi, nil
	}
	return http.StatusOK, service.ProjectFor(pi, proj, withholdDef), nil
}

// GetActionableView returns an ActionableView DTO (200) — a curated projection
// listing open human tasks and the allowed next-step actions from the
// definition. State and definition are sourced from the returned
// service.ProcessInstance (definition may be nil if unresolved).
//
// ⚠ It needs BOTH knobs. proj governs the state; withholdDef governs the DEFINITION, and
// the definition is where allowed_actions[].condition comes from — an expr predicate that
// discloses business policy. A projection of the state alone leaves those conditions on the
// wire, and because the leak is a routing expression rather than a value, a test grepping
// for a secret string never sees it.
func GetActionableView(
	ctx context.Context,
	svc service.Service,
	id string,
	proj func(engine.InstanceState) engine.InstanceState,
	withholdDef bool,
) (int, any, error) {
	pi, err := svc.GetInstance(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	st := pi.State()
	if proj != nil {
		st = proj(st)
	}
	def := pi.Definition()
	if withholdDef {
		def = nil
	}
	return http.StatusOK, view.NewActionableView(st, def), nil
}

// DeliverSignal delivers a signal to an existing process instance and returns
// (200, mappedBody, nil) on success. Validates in before calling svc.
func DeliverSignal(ctx context.Context, svc service.Service, id string, in SignalInput, mapper func(engine.InstanceState) any) (int, any, error) {
	if err := Validate(&in); err != nil {
		return 0, nil, err
	}
	pi, err := svc.DeliverSignal(ctx, service.DeliverSignalRequest{
		InstanceID: id,
		Signal:     in.Signal,
		Payload:    in.Payload,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, mapInstance(mapper, pi.State()), nil
}

// DeliverMessage routes a message to a waiting instance and returns (202, nil, nil)
// on success. Delivery is fire-and-forget: a 202 does not guarantee a waiting
// instance was matched. Validates in before calling svc.
func DeliverMessage(ctx context.Context, svc service.Service, in MessageInput) (int, any, error) {
	if err := Validate(&in); err != nil {
		return 0, nil, err
	}
	if err := svc.DeliverMessage(ctx, service.DeliverMessageRequest{
		Name:           in.Name,
		CorrelationKey: in.CorrelationKey,
		Payload:        in.Payload,
	}); err != nil {
		return 0, nil, err
	}
	return http.StatusAccepted, nil, nil
}

// ClaimTask authorizes the actor and claims a human task, returning (200,
// mappedBody, nil) on success.
func ClaimTask(ctx context.Context, svc service.Service, token string, in ClaimInput, mapper func(engine.InstanceState) any, actor authz.Actor) (int, any, error) {
	// ⚠ The actor arrives already RESOLVED — the adapter calls [RequestActor] before it
	// reads the body, so an unauthenticated caller is refused without one being read.
	//
	// This guard is defence in depth for a consumer-written adapter that skips that
	// call: a zero actor can never mean "authenticated". It performs no I/O.
	if isZeroActor(actor) {
		return 0, nil, fmt.Errorf("%w: the endpoint received the zero actor", ErrUnauthenticated)
	}
	a := actor
	pi, err := svc.ClaimTask(ctx, service.ClaimTaskRequest{
		TaskID: token,
		Actor:  a,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, mapInstance(mapper, pi.State()), nil
}

// CompleteTask authorizes the actor and completes a human task, returning (200,
// mappedBody, nil) on success.
func CompleteTask(ctx context.Context, svc service.Service, token string, in CompleteInput, mapper func(engine.InstanceState) any, actor authz.Actor) (int, any, error) {
	// ⚠ The actor arrives already RESOLVED — the adapter calls [RequestActor] before it
	// reads the body, so an unauthenticated caller is refused without one being read.
	//
	// This guard is defence in depth for a consumer-written adapter that skips that
	// call: a zero actor can never mean "authenticated". It performs no I/O.
	if isZeroActor(actor) {
		return 0, nil, fmt.Errorf("%w: the endpoint received the zero actor", ErrUnauthenticated)
	}
	a := actor
	pi, err := svc.CompleteTask(ctx, service.CompleteTaskRequest{
		TaskID:  token,
		Actor:   a,
		Outcome: in.Outcome,
		Note:    in.Note,
		Output:  in.Output,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, mapInstance(mapper, pi.State()), nil
}

// ReassignTask authorizes the reassigner and reassigns a human task, returning
// (200, mappedBody, nil) on success.
func ReassignTask(ctx context.Context, svc service.Service, token string, in ReassignInput, mapper func(engine.InstanceState) any, actor authz.Actor) (int, any, error) {
	// ⚠ The actor arrives already RESOLVED — the adapter calls [RequestActor] before it
	// reads the body, so an unauthenticated caller is refused without one being read.
	//
	// This guard is defence in depth for a consumer-written adapter that skips that
	// call: a zero actor can never mean "authenticated". It performs no I/O.
	if isZeroActor(actor) {
		return 0, nil, fmt.Errorf("%w: the endpoint received the zero actor", ErrUnauthenticated)
	}
	a := actor
	pi, err := svc.ReassignTask(ctx, service.ReassignTaskRequest{
		TaskID: token,
		From:   in.From,
		To:     in.To,
		By:     a,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, mapInstance(mapper, pi.State()), nil
}
