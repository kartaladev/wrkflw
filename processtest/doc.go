// Package processtest is a consumer-facing test harness for the workflow engine.
// It lets a consumer unit-test a process definition end-to-end — fully in-memory,
// deterministic, and without Docker — instead of hand-rolling the park/deliver
// loop that a running instance requires.
//
// # Why
//
// A started instance does not run straight to a terminal status: it advances
// synchronously until it parks on an external stimulus — a human task, a timer, a
// signal, a message, or an async child — then waits. Driving it to completion
// means discovering why it parked, building the right trigger, delivering it, and
// repeating. This package absorbs that loop.
//
// # Harness
//
// [New] wires the whole in-memory stack ([kernel.MemInstanceStore], a [FakeClock], a
// [MemScheduler], a [SpyCatalog], a [SpyAuthorizer], an in-memory
// human-task store and [task.TaskService], and optionally a [signal.SignalBus]).
// [Harness.Start] runs an instance; [Harness.DriveToCompletion] drives it using a
// [ParkHandler]. Accessors ([Harness.Catalog], [Harness.Authorizer],
// [Harness.Clock], …) expose the collaborators for assertions.
//
// # Park handling
//
// The drive loop classifies each park into a [Park] (a primary [Reason] plus
// discrete open-tasks / awaiting-signals / awaiting-messages / armed-timers /
// incidents fields) and asks a [ParkHandler] for a [Decision]: [Deliver] a
// trigger, [AdvanceTimers], [Stop], [Abort], or [Pass]. Ready-made combinators —
// [AutoTimers], [Harness.CompleteTasks], [Harness.PublishSignal], [Harness.DeliverMessage], and
// [Chain] — keep common flows to one line:
//
//	h.DriveToCompletion(ctx, def, id, processtest.Chain(
//		processtest.AutoTimers(),
//		h.CompleteTasks(decide),
//	))
//
// # Fakes
//
// [SpyCatalog], [SpyAuthorizer], and [CaptureSender] are also usable standalone,
// independent of the [Harness].
//
// # Store conformance
//
// [RunTaskStoreConformance] checks a [humantask.TaskStore] implementation against
// the claim invariant its Upsert contract requires (ADR-0183): each invalid shape
// is rejected, and the rejected row reaches neither Get nor either inbox query.
// Since ADR-0184 it also checks the positive half: every legal shape must be
// accepted and read back, and two accepted controls (an Unclaimed task and a
// Claimed task) must reach the inbox their shape belongs in — a store whose list
// queries always return nothing now fails, where before it passed the whole
// suite vacuously. Consumers who supply their own task store should run it from
// their own test suite: adopting the invariant does not change the interface
// signature, so a non-conforming store keeps compiling and keeps accepting
// contradictory rows.
//
// # Lower-level entry point
//
// [DriveToCompletion] (the package function) runs the same loop against a
// consumer-built [runtime.ProcessDriver]; it cannot auto-advance timers (no owned
// scheduler), so timer flows there require the handler to build their own
// triggers. Prefer the [Harness] for anything with timers.
package processtest
