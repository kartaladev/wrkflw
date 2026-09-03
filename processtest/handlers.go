package processtest

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/task"
)

// AutoTimers returns a [ParkHandler] that resolves a timer park by advancing the
// harness clock to the next due timer and ticking the scheduler. It acts only when
// the park's primary [Reason] is [ReasonTimer], so it composes safely under [Chain]
// in front of task/signal handlers: a park that merely has a *secondary* armed
// timer (e.g. a user task carrying a deadline) is left for the task handler rather
// than fired to its timeout. Effective only under a [Harness] (the free-function
// drive cannot advance timers).
func AutoTimers() ParkHandler {
	return func(_ context.Context, p Park) (Decision, error) {
		if p.Reason == ReasonTimer {
			return AdvanceTimers(), nil
		}
		return Pass(), nil
	}
}

// DecideTaskFunc decides what to do with an open human task: the actor to act as,
// the completion output, and whether to act at all (false = leave it, yielding
// [Pass]). It is called once to claim and again to complete, so it must be
// idempotent for a given task.
type DecideTaskFunc func(t humantask.HumanTask) (actor authz.Actor, output map[string]any, ok bool)

// CompleteTasksWith returns a [ParkHandler] that claims and completes open human
// tasks using svc and the decision from decide. It acts on the first open task
// decide accepts (skipping any it declines, so a later actionable task is not
// stranded behind a declined one). Claiming and completing are separate driver
// deliveries, so the handler returns the claim trigger on one step and the
// completion trigger on the next (the drive loop re-invokes it).
//
// decide is invoked at most once per task id; the decision (actor, output,
// accept) is memoized and reused for that token's claim and completion, so the
// completion always uses the same actor that claimed — the handler does not rely
// on decide being idempotent.
func CompleteTasksWith(svc *task.TaskService, decide DecideTaskFunc) ParkHandler {
	type verdict struct {
		actor  authz.Actor
		output map[string]any
		accept bool
	}
	// memo is mutex-guarded: one handler value may be shared across concurrent
	// drives (the harness documents that as supported), and an unguarded map is a
	// data race there — reported by -race on two instances sharing this handler.
	//
	// The lock covers the map only, never the call to decide. decide is CONSUMER
	// code and may block or coordinate between instances; holding the lock across
	// it would serialise, or deadlock, two drives sharing this handler value.
	var mu sync.Mutex
	memo := make(map[string]verdict)

	return func(ctx context.Context, p Park) (Decision, error) {
		for _, tsk := range p.OpenTasks {
			mu.Lock()
			v, seen := memo[tsk.TaskID]
			mu.Unlock()
			if !seen {
				actor, output, ok := decide(tsk)
				candidate := verdict{actor: actor, output: output, accept: ok}
				mu.Lock()
				// Re-check: a task id is per instance, so concurrent drives never
				// contend for one, but the first stored verdict wins regardless.
				if existing, raced := memo[tsk.TaskID]; raced {
					v = existing
				} else {
					memo[tsk.TaskID] = candidate
					v = candidate
				}
				mu.Unlock()
			}
			if !v.accept {
				continue // decide declined this task; try the next open one
			}

			switch tsk.State {
			case humantask.Unclaimed:
				trg, err := svc.Claim(ctx, tsk.TaskID, v.actor)
				if err != nil {
					return Decision{}, fmt.Errorf("processtest: claim task %q: %w", tsk.TaskID, err)
				}
				return Deliver(trg), nil
			case humantask.Claimed:
				trg, err := svc.Complete(ctx, tsk.TaskID, v.actor, engine.CompletionInput{Output: v.output})
				if err != nil {
					return Decision{}, fmt.Errorf("processtest: complete task %q: %w", tsk.TaskID, err)
				}
				return Deliver(trg), nil
			}
		}
		return Pass(), nil
	}
}

// armFireLog records, per instance, the node ids a handler has already delivered
// its name against, so an ARM-derived delivery repeats only where it has not
// already fired.
//
// It keys on WHICH nodes are parked, not on how many waiters exist. An arm's true
// identity is unreachable — the arm slices on [engine.InstanceState] have
// unexported element types — and the parked tokens are the closest observable
// proxy for it. Counting waiters instead fails in BOTH directions: two sequential
// arms of one name each report a single waiter, so the second is suppressed; and
// an arm whose own branch arms another waiter of the same name makes the count
// grow every firing, re-authorising delivery until the drive hits its step limit.
//
// Keyed by instance id rather than held in one slot: a single slot does not
// collide across instances once the key carries the instance id, but it
// DISPLACES — instance B's entry evicts A's, so A's next identical park is
// delivered to again, and a handler value shared across concurrent drives
// produces a run-varying number of firings. Mutex-guarded because the harness
// documents concurrent drives as supported.
type armFireLog struct {
	mu   sync.Mutex
	seen map[string]map[string]struct{}
}

// deliverFor reports whether an arm-derived delivery should be made for instance
// id, given the node ids its tokens are currently parked on, recording them when
// it should. It delivers on the instance's first park, and thereafter only when
// some parked node has not been delivered against yet.
func (f *armFireLog) deliverFor(id string, nodes []string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	marked, started := f.seen[id]
	if !started {
		marked = make(map[string]struct{}, len(nodes))
		if f.seen == nil {
			f.seen = make(map[string]map[string]struct{})
		}
		f.seen[id] = marked
		for _, n := range nodes {
			marked[n] = struct{}{}
		}
		return true
	}

	fresh := false
	for _, n := range nodes {
		if _, ok := marked[n]; !ok {
			marked[n] = struct{}{}
			fresh = true
		}
	}
	return fresh
}

// waitingNodes returns the node ids of every parked token, the proxy armFireLog
// uses for arm identity.
func waitingNodes(state engine.InstanceState) []string {
	var out []string
	for _, t := range state.Tokens {
		if t.State == engine.TokenWaiting {
			out = append(out, t.NodeID)
		}
	}
	return out
}

// PublishSignal returns a [ParkHandler] that resolves a signal park by delivering
// a SignalReceived for name (with payload) when the instance awaits it; otherwise
// [Pass]. The trigger is stamped with the harness's fake clock so the run stays
// deterministic (a downstream relative timer is anchored on this instant).
//
// "Awaits it" spans every source [Park.AwaitingSignals] carries — a token
// signal-catch, an armed signal boundary, an event-based-gateway signal arm, or a
// signal-triggered event sub-process arm.
//
// A TOKEN signal-catch is never bounded: it is CONSUMED when it fires, so it
// cannot re-match, and every match is a real one — including a catch a loop
// returns to. An ARM is bounded, because a non-interrupting arm stays armed and
// would otherwise re-match an unchanged park until the drive hits its step limit.
// An arm-derived delivery fires once per instance per PARKED NODE: the first park
// delivers, and a later park delivers again only if some token is parked on a node
// this handler has not already fired at. Two arms of one name on different
// activities therefore both fire, while one arm re-matching its own unchanged park
// fires once. The bound is per handler value and per instance, so a handler shared
// across concurrent drives behaves identically to one handler per drive.
func (h *Harness) PublishSignal(name string, payload map[string]any) ParkHandler {
	var fired armFireLog
	return func(_ context.Context, p Park) (Decision, error) {
		if !slices.Contains(p.AwaitingSignals, name) {
			return Pass(), nil
		}
		if !anyToken(p.State.Tokens, func(t engine.Token) bool { return t.AwaitSignal == name }) &&
			!fired.deliverFor(p.State.InstanceID, waitingNodes(p.State)) {
			return Pass(), nil
		}
		return Deliver(engine.NewSignalReceived(h.clk.Now(), name, payload)), nil
	}
}

// DeliverMessage returns a [ParkHandler] that resolves a message park by
// delivering a MessageReceived for name (with correlationKey and payload) when the
// instance awaits that name; otherwise [Pass]. The trigger is stamped with the
// harness's fake clock for deterministic timing.
//
// Matching is by message NAME only — correlationKey is what gets delivered, not
// part of the match. The key an arm actually expects is readable off
// [Park.AwaitingMessages], which is why that field carries
// [engine.MessageWaiter] rather than a bare name.
//
// Like [Harness.PublishSignal], a token message-catch is never bounded and an
// arm-derived delivery fires once per instance per parked node.
func (h *Harness) DeliverMessage(name, correlationKey string, payload map[string]any) ParkHandler {
	var fired armFireLog
	return func(_ context.Context, p Park) (Decision, error) {
		if !slices.ContainsFunc(p.AwaitingMessages, func(m engine.MessageWaiter) bool { return m.Name == name }) {
			return Pass(), nil
		}
		if !anyToken(p.State.Tokens, func(t engine.Token) bool { return t.AwaitMessage == name }) &&
			!fired.deliverFor(p.State.InstanceID, waitingNodes(p.State)) {
			return Pass(), nil
		}
		return Deliver(engine.NewMessageReceived(h.clk.Now(), name, correlationKey, payload)), nil
	}
}

// Chain returns a [ParkHandler] that tries each handler in order and returns the
// first non-[Pass] decision (or the first error). If every handler passes, Chain
// passes.
func Chain(handlers ...ParkHandler) ParkHandler {
	return func(ctx context.Context, p Park) (Decision, error) {
		for _, h := range handlers {
			if h == nil {
				continue
			}
			d, err := h(ctx, p)
			if err != nil {
				return d, err
			}
			if d.kind != kindPass {
				return d, nil
			}
		}
		return Pass(), nil
	}
}

// CompleteTasks is the [Harness] convenience for [CompleteTasksWith] using the
// harness's own task service.
func (h *Harness) CompleteTasks(decide DecideTaskFunc) ParkHandler {
	return CompleteTasksWith(h.taskSvc, decide)
}
