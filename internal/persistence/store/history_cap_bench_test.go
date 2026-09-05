package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// Benchmarks for the cost the whole-document snapshot imposes as an instance's
// history grows.
//
// THE CLAIM UNDER TEST. Every step persists the entire InstanceState as one
// JSON document (store_core.go's Create and Commit, both via marshalSnapshot),
// so the per-step cost is proportional to everything the instance has
// accumulated. Four collections grow without bound over an instance's life —
// History, Tasks, RootCompensations and ArchivedCompensations — and each
// CompensationRecord additionally holds a full copy of the instance variables.
// A step therefore costs O(size of the instance), and an instance that makes N
// transitions pays that N times: O(N^2) over its life.
//
// WHAT THESE MEASURE, AND WHAT THEY DO NOT. Both benchmarks are hermetic: no
// database, no Docker, no network. They measure the two costs the claim is
// actually about — driving the engine, and encoding the snapshot the store
// writes — and deliberately EXCLUDE the database round-trip and the JSONB
// column rewrite. Those are real and they grow with snapshot size too, so
// every number here is a LOWER BOUND on what a deployment pays per step. The
// exclusion is what keeps the benchmarks fast and reproducible enough to sit in
// CI; it is not a claim that I/O is free.
//
// The `marshal=off` arm exists so the quadratic term can be ATTRIBUTED rather
// than assumed. It drives the identical engine transitions and skips only the
// snapshot encoding, so the gap between it and `cap=off` is the snapshot's own
// contribution. Without that control, a quadratic curve could just as easily be
// the engine's doing.

// sink defeats dead-code elimination of the encoded snapshot. b.Loop already
// keeps the loop body alive, but the encoded bytes are otherwise unused and
// this costs nothing to be certain about.
var sink int

// benchHistorySizes is the transition counts the benchmarks sweep.
//
// Chosen to make the SHAPE legible rather than to reach a realistic maximum.
// Successive doublings are what expose the exponent: a linear cost doubles
// between neighbours, a quadratic one quadruples, and reading that off four
// doublings needs no curve fitting and no statistics beyond benchstat. The
// range stops at 800 because the curve is unambiguous well before then and the
// top point already dominates the wall-clock — pushing to 1600 would quadruple
// the most expensive arm to sharpen a conclusion the existing points have
// already made, which is how a benchmark ends up disabled in CI.
var benchHistorySizes = []int{50, 100, 200, 400, 800}

// benchStartVars is the variable payload an instance starts with. Every
// CompensationRecord copies it (copyVars), so it is a direct multiplier on the
// compensation half of the snapshot. Deliberately modest — a handful of
// realistic fields rather than a large document — so the numbers below are
// attributable to the NUMBER of records rather than to an inflated payload.
// A deployment carrying heavier variables pays proportionally more.
func benchStartVars() map[string]any {
	return map[string]any{
		"orderID":    "ord-8f3c1a92-77d4-4e1b-9a6f-2c5e8b0d4417",
		"customerID": "cus-2b7e5119",
		"amount":     149.95,
		"currency":   "EUR",
		"channel":    "web",
	}
}

// benchLoopDef builds start -> svc(compensable) -> xor -{attempts < n}-> svc ;
// -default-> park -> end.
//
// A LOOP rather than a chain of n service tasks: the definition stays the same
// size for every n, so what varies across the sweep is the instance's
// accumulated history and nothing else. A chain would grow the definition too
// and the two costs could not be told apart.
//
// svc is compensable, so each pass also appends a CompensationRecord carrying a
// copy of the instance variables — one of the four unbounded collections, and
// the one the issue singles out.
func benchLoopDef(n int) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "bench-history-loop", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("work"), activity.WithCompensateAction("undo")),
			gateway.NewExclusive("xor"),
			activity.NewServiceTask("park", activity.WithTaskAction("park")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "xor"},
			{ID: "f3", Source: "xor", Target: "svc", Condition: fmt.Sprintf("attempts < %d", n)},
			{ID: "f4", Source: "xor", Target: "park", IsDefault: true},
			{ID: "f5", Source: "park", Target: "end"},
		},
	}
}

// findInvokeAction returns the CommandID of the pending InvokeAction named
// name. Benchmarks cannot use the engine package's testing.T-based helper.
func findInvokeAction(cmds []engine.Command, name string) (string, bool) {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return ia.CommandID, true
		}
	}
	return "", false
}

// driveInstance runs one instance through n loop passes, encoding the snapshot
// after every step exactly as Create and Commit do when marshal is true.
//
// Returns the number of transitions actually driven — so callers report
// per-transition cost against the real figure rather than the requested one —
// and the final state, for callers that want to inspect what accumulated.
//
// THE ONLY DRIVER. Both benchmarks and every guard in
// history_cap_bench_guard_test.go reach the engine through this function. An
// earlier revision had stateAfter carrying a second near-identical copy of this
// loop, which meant the guards proved one driver while BenchmarkInstanceLifetime
// — the one producing the headline result — ran on the other, unguarded:
// truncating it left every guard green and the benchmark reporting a tidy,
// meaningless curve. Collapsing the two is what makes the guards cover both.
// Keep it that way; a second copy silently un-guards whichever benchmark uses it.
func driveInstance(ctx context.Context, b testing.TB, def *model.ProcessDefinition, n, historyCap int, marshal bool) (int, engine.InstanceState) {
	b.Helper()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	r, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "bench-1"},
		engine.NewStartInstance(t0, benchStartVars()), engine.StepOptions{})
	if err != nil {
		b.Fatalf("start instance: %v", err)
	}

	transitions := 0
	for i := 1; i <= n; i++ {
		cmdID, ok := findInvokeAction(r.Commands, "work")
		if !ok {
			break // the loop guard sent the token to park; the life is over
		}
		r, err = engine.Step(ctx, def, r.State,
			engine.NewActionCompleted(t0, cmdID, map[string]any{"attempts": i}), engine.StepOptions{})
		if err != nil {
			b.Fatalf("transition %d: %v", i, err)
		}
		transitions++

		if marshal {
			snap, err := store.MarshalSnapshotForTest(r.State, historyCap)
			if err != nil {
				b.Fatalf("marshal snapshot at transition %d: %v", i, err)
			}
			sink += len(snap)
		}
	}
	return transitions, r.State
}

// BenchmarkInstanceLifetime measures what one instance costs over its whole
// life: n transitions, with the snapshot encoded after every one of them, which
// is what the store does per step.
//
// Read ns/op as "the cost of an instance that made n transitions". If the O(N^2)
// claim holds, doubling n roughly QUADRUPLES ns/op and B/op on the cap=off arm,
// and the reported ns/transition roughly doubles. The cap=100 arm should stay
// close to linear in n, and marshal=off isolates the engine's own contribution.
func BenchmarkInstanceLifetime(b *testing.B) {
	ctx := b.Context()

	arms := []struct {
		name       string
		historyCap int
		marshal    bool
	}{
		// Today's shipped default: historyCap <= 0 keeps full inline history.
		{name: "cap=off", historyCap: 0, marshal: true},
		// The option that already exists but is off by default (WithHistoryCap).
		{name: "cap=100", historyCap: 100, marshal: true},
		// Control: identical engine work, no snapshot encoding.
		{name: "marshal=off", historyCap: 0, marshal: false},
	}

	for _, arm := range arms {
		for _, n := range benchHistorySizes {
			def := benchLoopDef(n) // setup: excluded from the measured region
			b.Run(fmt.Sprintf("%s/transitions=%d", arm.name, n), func(b *testing.B) {
				b.ReportAllocs()

				// Accumulated, not sampled from the last iteration: the total
				// is what the division actually wants, and it stays correct if
				// an engine change ever makes the per-iteration count vary.
				total := 0
				for b.Loop() {
					driven, _ := driveInstance(ctx, b, def, n, arm.historyCap, arm.marshal)
					total += driven
				}

				// Per-transition cost is where the exponent is easiest to read:
				// flat for a linear system, growing with n for a quadratic one.
				// Derived from the transitions actually driven, so a driver that
				// stopped early could not quietly flatter the figure.
				if total > 0 {
					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(total), "ns/transition")
				}
			})
		}
	}
}

// BenchmarkSnapshotMarshal isolates the per-step cost: encoding one snapshot of
// an instance that already has n transitions behind it. This is the mechanism
// BenchmarkInstanceLifetime integrates over — if this is linear in n, the
// lifetime cost is necessarily quadratic.
//
// The state is built by driving the real engine, not hand-assembled, so History,
// RootCompensations, Tokens and Variables all hold whatever the engine actually
// produces rather than what a fixture author assumed it produces.
func BenchmarkSnapshotMarshal(b *testing.B) {
	ctx := b.Context()

	for _, historyCap := range []int{0, 100} {
		for _, n := range benchHistorySizes {
			name := fmt.Sprintf("cap=off/history=%d", n)
			if historyCap > 0 {
				name = fmt.Sprintf("cap=%d/history=%d", historyCap, n)
			}
			b.Run(name, func(b *testing.B) {
				// Setup: drive an instance to n transitions ONCE, then encode
				// that finished state repeatedly. Inside b.Run so that filtering
				// to one sub-benchmark does not first drive every other size;
				// still outside the measured region, because b.Loop resets the
				// timer when it is first called. Verified rather than assumed:
				// moving this in left ns/op unchanged.
				def := benchLoopDef(n)
				st := stateAfter(ctx, b, def, n)

				b.ReportAllocs()
				for b.Loop() {
					snap, err := store.MarshalSnapshotForTest(st, historyCap)
					if err != nil {
						b.Fatalf("marshal snapshot: %v", err)
					}
					sink += len(snap)
				}
			})
		}
	}
}

// stateAfter drives an instance to n transitions and returns the resulting
// state, for callers that want to measure or assert something ABOUT that state
// rather than the cost of reaching it.
//
// A thin wrapper over driveInstance rather than a second loop: see that
// function's note. Marshalling is off because it does not affect the state
// produced, only the cost of producing it.
func stateAfter(ctx context.Context, b testing.TB, def *model.ProcessDefinition, n int) engine.InstanceState {
	b.Helper()
	_, st := driveInstance(ctx, b, def, n, 0, false)
	return st
}
