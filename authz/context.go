package authz

import "context"

// actorContextKey is the unexported key under which [ContextWithActor] stores the
// authenticated principal. An unexported struct type cannot collide with a key any
// other package defines, which is why it is not a string.
type actorContextKey struct{}

// ContextWithActor returns a copy of ctx carrying a as the AUTHENTICATED principal
// of the current request.
//
// Authentication middleware calls this; the HTTP transport reads it and reads the
// actor from nowhere else (ADR-0189). Before that record the transport built the
// actor from the request body, so any caller could choose their own roles.
//
// It is a plain function on purpose: any middleware in any framework can call it
// with no DI container and no interface to implement.
//
// ⚠ The actor is cloned via [Actor.Clone], which is ONE LEVEL DEEP: a nested map or
// slice inside an attribute value stays shared with the caller. Callers must not
// mutate such nested values concurrently — the transport seam takes its own deep
// copy before it serialises anything, but nothing protects a caller who keeps
// writing to a map it has handed over.
func ContextWithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, a.Clone())
}

// ActorFromContext reports the actor a middleware placed on ctx with
// [ContextWithActor].
//
// ok is false when nothing authenticated the request. Callers must treat that as a
// REFUSAL, never as a licence to proceed with the zero Actor: the transport turns it
// into 401 (ADR-0189).
//
// The returned actor is cloned on the way OUT as well as in, so one caller mutating
// what it received cannot change what the next caller reads. ⚠ The same one-level
// limit applies as in [ContextWithActor].
func ActorFromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorContextKey{}).(Actor)
	if !ok {
		return Actor{}, false
	}
	return a.Clone(), true
}
