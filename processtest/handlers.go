package processtest

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
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
// decide is invoked at most once per task token; the decision (actor, output,
// accept) is memoized and reused for that token's claim and completion, so the
// completion always uses the same actor that claimed — the handler does not rely
// on decide being idempotent.
func CompleteTasksWith(svc *task.TaskService, decide DecideTaskFunc) ParkHandler {
	type verdict struct {
		actor  authz.Actor
		output map[string]any
		accept bool
	}
	memo := make(map[string]verdict)

	return func(ctx context.Context, p Park) (Decision, error) {
		for _, tsk := range p.OpenTasks {
			v, seen := memo[tsk.TaskToken]
			if !seen {
				actor, output, ok := decide(tsk)
				v = verdict{actor: actor, output: output, accept: ok}
				memo[tsk.TaskToken] = v
			}
			if !v.accept {
				continue // decide declined this task; try the next open one
			}

			switch tsk.State {
			case humantask.Unclaimed:
				trg, err := svc.Claim(ctx, tsk.TaskToken, v.actor)
				if err != nil {
					return Decision{}, fmt.Errorf("processtest: claim task %q: %w", tsk.TaskToken, err)
				}
				return Deliver(trg), nil
			case humantask.Claimed:
				trg, err := svc.Complete(ctx, tsk.TaskToken, v.actor, v.output)
				if err != nil {
					return Decision{}, fmt.Errorf("processtest: complete task %q: %w", tsk.TaskToken, err)
				}
				return Deliver(trg), nil
			}
		}
		return Pass(), nil
	}
}

// PublishSignal returns a [ParkHandler] that resolves a signal park by delivering
// a SignalReceived for name (with payload) when any token awaits it; otherwise
// [Pass]. The trigger is stamped with the harness's fake clock so the run stays
// deterministic (a downstream relative timer is anchored on this instant).
func (h *Harness) PublishSignal(name string, payload map[string]any) ParkHandler {
	return func(_ context.Context, p Park) (Decision, error) {
		for _, s := range p.AwaitingSignals {
			if s == name {
				return Deliver(engine.NewSignalReceived(h.clk.Now(), name, payload)), nil
			}
		}
		return Pass(), nil
	}
}

// DeliverMessage returns a [ParkHandler] that resolves a message park by
// delivering a MessageReceived for name (with correlationKey and payload) when any
// token awaits it; otherwise [Pass]. The trigger is stamped with the harness's
// fake clock for deterministic timing.
func (h *Harness) DeliverMessage(name, correlationKey string, payload map[string]any) ParkHandler {
	return func(_ context.Context, p Park) (Decision, error) {
		for _, m := range p.AwaitingMessages {
			if m == name {
				return Deliver(engine.NewMessageReceived(h.clk.Now(), name, correlationKey, payload)), nil
			}
		}
		return Pass(), nil
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
