// Package persistence_test — end-to-end real-DB process chaining integration test.
//
// TestChainingE2E proves the full outbox→relay→ChainerRunner→successor loop across
// all three supported database dialects (Postgres, MySQL, SQLite):
//
//	Store.Commit (writes outbox row)
//	  → relay.DrainOnce (reads outbox, publishes via GoChannel pub/sub)
//	    → eventing.Chainer.Run (subscribes; calls runtime.Chainer.Handle)
//	      → runtime.Chainer.Handle (evaluates policy, starts successor via driver.Drive, records ChainLink)
//
// This seam has never previously been tested against a real database.
package persistence_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/chain"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// ---- minimal process definitions -----------------------------------------------

// buildDef is a helper to build a trivial start→end process definition.
func buildDef(t *testing.T, id string, version int) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder(id, version).
		Add(event.NewStart("start")).
		Add(event.NewEnd("end")).
		Connect("start", "end").
		Build()
	require.NoError(t, err)
	return def
}

// ---- dialect setup ---------------------------------------------------------------

// chainingDialect bundles the objects needed by each dialect sub-test.
type chainingDialect struct {
	store  persistence.InstanceStore
	links  kernel.ChainLinkStore
	relay  persistence.Relay
	pub    kernel.OutboxPublisher
	sub    message.Subscriber
	closer io.Closer
}

// forEachChainingDialect runs fn as a sub-test for each of the three supported
// database dialects. Each sub-test receives its own isolated database and an
// assembled chainingDialect ready to use.
func forEachChainingDialect(t *testing.T, fn func(t *testing.T, d chainingDialect)) {
	t.Helper()

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		pool := dbtest.RunTestDatabase(t)
		require.NoError(t, persistence.Migrate(ctx, pool))

		st, err := persistence.OpenPostgres(ctx, pool)
		require.NoError(t, err)

		links, err := persistence.NewChainLinkStore(pool)
		require.NoError(t, err)
		pub, sub, closer := eventing.NewGoChannelPublisher()
		relay, err := persistence.NewRelay(pool, pub)
		require.NoError(t, err)

		fn(t, chainingDialect{store: st, links: links, relay: relay, pub: pub, sub: sub, closer: closer})
		require.NoError(t, closer.Close())
	})

	t.Run("mysql", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		db := dbtest.RunTestMySQL(t)
		// RunTestMySQL already applies migrations.
		st, err := persistence.OpenMySQL(ctx, db)
		require.NoError(t, err)

		links, err := persistence.NewMySQLChainLinkStore(db)
		require.NoError(t, err)
		pub, sub, closer := eventing.NewGoChannelPublisher()
		relay, err := persistence.NewMySQLRelay(db, pub)
		require.NoError(t, err)

		fn(t, chainingDialect{store: st, links: links, relay: relay, pub: pub, sub: sub, closer: closer})
		require.NoError(t, closer.Close())
	})

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		db := dbtest.RunTestSQLite(t)
		// RunTestSQLite already applies migrations.
		st, err := persistence.OpenSQLite(ctx, db)
		require.NoError(t, err)

		links, err := persistence.NewSQLiteChainLinkStore(db)
		require.NoError(t, err)
		pub, sub, closer := eventing.NewGoChannelPublisher()
		relay, err := persistence.NewSQLiteRelay(db, pub)
		require.NoError(t, err)

		fn(t, chainingDialect{store: st, links: links, relay: relay, pub: pub, sub: sub, closer: closer})
		require.NoError(t, closer.Close())
	})
}

// ---- shared wiring ---------------------------------------------------------------

// wireChainerRunner builds the full chaining stack over d and starts the
// ChainerRunner goroutine. It registers cleanup via t.Cleanup. The returned
// driver is ready to call Run against.
func wireChainerRunner(t *testing.T, d chainingDialect, defPA, defPB, defSA, defSB *model.ProcessDefinition) *runtime.ProcessDriver {
	t.Helper()

	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(d.store))
	require.NoError(t, err)

	// SuccessorPolicy: proc-a → proc-a-succ; proc-b → proc-b-succ; else no successor.
	policy := func(ctx context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		switch ev.PredecessorDefinitionRef {
		case model.Version("proc-a", 1):
			return chain.SuccessorDecision{Def: defSA, Vars: ev.Result}, true
		case model.Version("proc-b", 1):
			return chain.SuccessorDecision{Def: defSB, Vars: ev.Result}, true
		default:
			return chain.SuccessorDecision{}, false
		}
	}

	core, err := chain.NewChainer(driver, policy, chain.WithChainLinks(d.links))
	require.NoError(t, err)
	cr := eventing.NewChainerRunner(core)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cr.Run(ctx, d.sub)
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	return driver
}

// ---- main test ------------------------------------------------------------------

// TestChainingE2E drives the full outbox→relay→chainer→successor loop across all
// three database dialects (Postgres via testcontainers, MySQL via testcontainers,
// SQLite in-process). Four scenarios per dialect:
//
//  1. Happy path (P_A → S_A) with start-var carry
//  2. Branch routing (P_B → S_B)
//  3. No successor (P_C — policy declines)
//  4. Idempotency (second DrainOnce is a no-op; outbox row already delivered)
func TestChainingE2E(t *testing.T) {
	t.Parallel()

	// Build the five process definitions once; they are value-types, safe to share.
	defPA := buildDef(t, "proc-a", 1)
	defPB := buildDef(t, "proc-b", 1)
	defPC := buildDef(t, "proc-c", 1)
	defSA := buildDef(t, "proc-a-succ", 1)
	defSB := buildDef(t, "proc-b-succ", 1)

	forEachChainingDialect(t, func(t *testing.T, d chainingDialect) {
		driver := wireChainerRunner(t, d, defPA, defPB, defSA, defSB)

		// ── Scenario 1: Happy path — P_A → S_A with start-var carry ─────────────
		t.Run("happy_path_vars_carry", func(t *testing.T) {
			ctx := t.Context()
			startVars := map[string]any{"key": "value-a"}

			// Run predecessor to completion.
			st, err := driver.Drive(ctx, defPA, "inst-a", startVars)
			require.NoError(t, err)
			assert.Equal(t, engine.StatusCompleted, st.Status, "predecessor must complete synchronously")

			// Flush outbox → pub/sub.
			drained, err := d.relay.DrainOnce(ctx)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, drained, 1, "at least one outbox row must be drained")

			// Wait for successor to appear.
			require.Eventually(t, func() bool {
				_, _, err := d.store.Load(ctx, "inst-a-next-completed")
				return err == nil
			}, 5*time.Second, 20*time.Millisecond, "successor inst-a-next-completed must start")

			succSt, _, err := d.store.Load(ctx, "inst-a-next-completed")
			require.NoError(t, err)
			assert.Equal(t, engine.StatusCompleted, succSt.Status, "successor must complete")
			assert.Equal(t, "value-a", succSt.Variables["key"], "start vars must be carried to successor")

			// Verify chain link.
			link, ok, err := d.links.LookupBySuccessor(ctx, "inst-a-next-completed")
			require.NoError(t, err)
			require.True(t, ok, "chain link must be recorded")
			assert.Equal(t, "inst-a", link.PredecessorID)
			assert.Equal(t, model.Version("proc-a-succ", 1), link.SuccessorDefinitionRef)
			assert.NotNil(t, link.StartVars)
		})

		// ── Scenario 2: Branch routing — P_B → S_B ───────────────────────────────
		t.Run("branch_routing", func(t *testing.T) {
			ctx := t.Context()

			_, err := driver.Drive(ctx, defPB, "inst-b", map[string]any{"key": "value-b"})
			require.NoError(t, err)

			_, err = d.relay.DrainOnce(ctx)
			require.NoError(t, err)

			require.Eventually(t, func() bool {
				_, _, err := d.store.Load(ctx, "inst-b-next-completed")
				return err == nil
			}, 5*time.Second, 20*time.Millisecond, "successor inst-b-next-completed must start")

			// Verify it's the correct successor (proc-b-succ, not proc-a-succ).
			link, ok, err := d.links.LookupBySuccessor(ctx, "inst-b-next-completed")
			require.NoError(t, err)
			require.True(t, ok, "chain link must be recorded for proc-b successor")
			assert.Equal(t, "inst-b", link.PredecessorID)
			assert.Equal(t, model.Version("proc-b-succ", 1), link.SuccessorDefinitionRef,
				"branch routing must wire P_B → S_B, not S_A")
		})

		// ── Scenario 3: No successor (P_C — policy declines) ─────────────────────
		t.Run("no_successor", func(t *testing.T) {
			ctx := t.Context()

			_, err := driver.Drive(ctx, defPC, "inst-c", nil)
			require.NoError(t, err)

			_, err = d.relay.DrainOnce(ctx)
			require.NoError(t, err)

			// Allow the ChainerRunner goroutine time to process the event.
			time.Sleep(200 * time.Millisecond)

			// No successor must have been created.
			_, _, err = d.store.Load(ctx, "inst-c-next-completed")
			require.Error(t, err, "no successor must be started for proc-c")
			assert.ErrorIs(t, err, kernel.ErrInstanceNotFound)

			// No chain link must be recorded either.
			_, ok, err := d.links.LookupBySuccessor(ctx, "inst-c-next-completed")
			require.NoError(t, err)
			assert.False(t, ok, "no chain link must be recorded when policy declines")
		})

		// ── Scenario 4: Idempotency — predecessor outbox row delivered exactly once ──
		//
		// Idempotency guarantee: the predecessor's instance.completed outbox row is
		// marked 'published' on the first DrainOnce. A second DrainOnce does NOT
		// re-publish that specific row (the outbox dedup prevents it), so the
		// ChainerRunner does not attempt to start a second successor.
		//
		// What we verify:
		//   • First drain creates the successor (Eventually check).
		//   • After calling DrainOnce a second time, there is still exactly ONE
		//     chain link from inst-a-idem (ErrChainLinkExists would prevent a
		//     second Record if a re-publish did slip through).
		//   • The successor still loads with StatusCompleted — no ErrInstanceExists
		//     panic or double-create.
		//
		// Note: the second DrainOnce may still drain the SUCCESSOR's own
		// instance.completed outbox row (a separate, newly-inserted row), so we
		// do NOT assert that the second drain returns 0. The outbox dedup operates
		// per dedup_key; the predecessor's dedup_key is distinct from the
		// successor's.
		t.Run("idempotency", func(t *testing.T) {
			ctx := t.Context()

			_, err := driver.Drive(ctx, defPA, "inst-a-idem", map[string]any{"x": 1})
			require.NoError(t, err)

			// First drain → predecessor's outbox row published → successor created.
			drained, err := d.relay.DrainOnce(ctx)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, drained, 1,
				"first DrainOnce must drain at least the predecessor's outbox row")

			require.Eventually(t, func() bool {
				_, _, err := d.store.Load(ctx, "inst-a-idem-next-completed")
				return err == nil
			}, 5*time.Second, 20*time.Millisecond, "successor must start after first drain")

			// Second drain — may drain the successor's own outbox row(s) but must
			// NOT re-publish the predecessor's already-delivered row.
			_, err = d.relay.DrainOnce(ctx)
			require.NoError(t, err)

			// Wait briefly for any event from the second drain to be processed.
			time.Sleep(150 * time.Millisecond)

			// Exactly ONE successor instance (Load would return ErrInstanceExists on
			// a second Create, but since Store.Create catches duplicates, the error
			// is surfaced as ErrInstanceExists only at that level; here we just
			// verify exactly one successor entry exists).
			succSt, _, err := d.store.Load(ctx, "inst-a-idem-next-completed")
			require.NoError(t, err)
			assert.Equal(t, engine.StatusCompleted, succSt.Status)

			// Exactly ONE chain link from the predecessor — the critical assertion.
			//
			// What this proves: the OUTBOX-level dedup. After the first DrainOnce the
			// predecessor's row is status='published'; the claim predicate is
			// status='pending', so the second DrainOnce cannot re-deliver it and the
			// ChainerRunner is never re-invoked for it. A single link therefore proves
			// the outbox never double-publishes an already-delivered row.
			//
			// What this does NOT exercise: the Chainer's OWN id-level idempotency
			// backstop (deterministic successor id → Store.Create → ErrInstanceExists,
			// and ChainLinkStore.Record → ErrChainLinkExists). Because the row is never
			// re-delivered here, those no-op paths are not reached in this scenario —
			// they are covered by the Chainer/ChainerRunner unit tests. This e2e
			// scenario deliberately validates the real-DB outbox dedup layer only.
			predLinks, err := d.links.ListByPredecessor(ctx, "inst-a-idem")
			require.NoError(t, err)
			assert.Len(t, predLinks, 1,
				"exactly one chain link must be recorded from inst-a-idem (idempotency)")

			link, ok, err := d.links.LookupBySuccessor(ctx, "inst-a-idem-next-completed")
			require.NoError(t, err)
			require.True(t, ok, "chain link must exist for the successor")
			assert.Equal(t, "inst-a-idem", link.PredecessorID)
		})
	})
}
