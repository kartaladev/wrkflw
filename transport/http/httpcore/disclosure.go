package httpcore

import (
	"context"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/view"
)

// identified reports whether the request carries a REAL principal (ADR-0190).
//
// ⚠ It resolves through the CONFIGURED resolver, not [authz.ActorFromContext]. Nothing in
// transport/http ever calls authz.ContextWithActor — ADR-0189 hands the actor to the
// endpoints as an ARGUMENT — so a context-keyed check is always false, and every
// authenticated caller would be projected. That is the "everyone blind" outcome this whole
// posture exists to avoid, and it was measured: a claim returned 200 while the context
// carried no actor at all.
//
// ⚠ It requires a non-empty Actor.ID — a STRICTER test than ADR-0189's isZeroActor, and
// deliberately so. That guard answers "may this actor ACT", and it blesses the kiosk
// claimant {ID:"", Roles:["kiosk"]} on purpose (humantask/validate.go). This one answers
// "may this caller SEE EVERYTHING", and an actor with no ID is unattributable: there is
// nobody to hold responsible for the disclosure. A kiosk may therefore complete a task
// while still receiving the public projection.
//
// The two predicates are not interchangeable, and reusing isZeroActor here was wrong: it
// admits {Roles:["kiosk"]} as identified, which handed full process variables to an
// anonymous caller. Caught by the kiosk case in TestDisclosingMapper_ProjectsWhenUnidentified.
//
// ⚠ It never surfaces an error. A failed or absent identity means "project", never 401:
// ADR-0190 Decision 1 keeps these routes reachable, and turning an unidentified read into a
// refusal would be the authentication this library declines to implement.
func identified(ctx context.Context, resolve RequestActorFunc) bool {
	if resolve == nil {
		return false
	}
	a, err := resolve(ctx)
	return err == nil && a.ID != ""
}

// DisclosingMapper returns an instance mapper that projects the state for a caller the
// transport could not identify, and passes it through untouched for one it could.
//
// It is the single decision point for ADR-0190's disclosure posture. Adapters build one per
// request and pass it wherever they would have passed cfg.InstanceMapper, so the six
// mapper-taking endpoints need no signature change and cannot each decide differently.
//
// inner is the consumer's own [CustomizeConfig.InstanceMapper]; nil means the default
// [NewInstanceView].
//
// ⚠ The projection is applied to the STATE, before inner sees it — never to inner's output.
// A consumer-supplied mapper may render any field, so filtering what it returns cannot work;
// it must never be handed the withheld data at all.
func DisclosingMapper(
	ctx context.Context,
	resolve RequestActorFunc,
	d authz.DisclosureSet,
	inner func(engine.InstanceState) any,
) func(engine.InstanceState) any {
	if inner == nil {
		inner = func(st engine.InstanceState) any { return NewInstanceView(st) }
	}
	if identified(ctx, resolve) || d.Has(authz.DiscloseAll) {
		return inner
	}
	return func(st engine.InstanceState) any {
		return inner(view.PublicState(st, d))
	}
}

// DisclosingProjection returns the state projection to apply for this request, or nil when
// the caller is identified and the state passes through whole.
//
// It serves the render paths that do not go through an instance mapper: the self-marshalling
// snapshot, which hands it to [service.ProjectFor], and the actionable view.
func DisclosingProjection(
	ctx context.Context,
	resolve RequestActorFunc,
	d authz.DisclosureSet,
) func(engine.InstanceState) engine.InstanceState {
	if identified(ctx, resolve) || d.Has(authz.DiscloseAll) {
		return nil
	}
	return func(st engine.InstanceState) engine.InstanceState {
		return view.PublicState(st, d)
	}
}

// WithholdDefinition reports whether the embedded process definition must be withheld from
// this request.
//
// The definition carries every node's eligibility spec and every flow condition, so it is
// policy in the sense [authz.DisclosePolicy] governs — and it reaches the wire by two routes
// the state projection cannot touch: the `definition` embed on the snapshot document, and
// the flow conditions rendered as a task's allowed actions.
func WithholdDefinition(ctx context.Context, resolve RequestActorFunc, d authz.DisclosureSet) bool {
	return !identified(ctx, resolve) &&
		!d.Has(authz.DisclosePolicy) && !d.Has(authz.DiscloseAll)
}
