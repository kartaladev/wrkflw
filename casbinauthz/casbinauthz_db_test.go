package casbinauthz_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/casbinauthz"
	"github.com/zakyalvan/krtlwrkflw/database"
)

// authorizeOK is a helper that calls authB.Authorize with a privilege-based AuthzSpec
// and returns true when authorization succeeds. The privilege format is "obj act" (two
// space-separated tokens) matching DefaultModel's p = sub, obj, act definition.
func authorizeOK(t *testing.T, a authz.Authorizer, actorID, obj, act string) bool {
	t.Helper()
	spec := authz.AuthzSpec{
		Privileges: []string{obj + " " + act},
	}
	actor := authz.Actor{ID: actorID}
	err := a.Authorize(t.Context(), spec, actor, nil)
	return err == nil
}

// TestNewCasbinAuthorizerFromDB_MultiNodeReload validates the two-node watcher scenario:
// policy is written via raw SQL (simulating another node), a NOTIFY is sent on the
// shared channel, and node B must see the updated policy after reload.
func TestNewCasbinAuthorizerFromDB_MultiNodeReload(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, casbinauthz.MigrateCasbin(t.Context(), pool))

	const ch = "wrkflw_casbin_policy_db_test"

	// Node A — we don't exercise Authorize on A; it's the "other node" that wrote.
	_, closerA, err := casbinauthz.NewCasbinAuthorizerFromDB(t.Context(), pool,
		casbinauthz.WithNodeID("A"),
		casbinauthz.WithWatcherChannel(ch),
	)
	require.NoError(t, err)
	defer func() { _ = closerA.Close() }()

	// Node B — this is the node we verify receives the reload.
	authB, closerB, err := casbinauthz.NewCasbinAuthorizerFromDB(t.Context(), pool,
		casbinauthz.WithNodeID("B"),
		casbinauthz.WithWatcherChannel(ch),
	)
	require.NoError(t, err)
	defer func() { _ = closerB.Close() }()

	// Before policy seed: alice should NOT be authorized.
	assert.False(t, authorizeOK(t, authB, "alice", "process:42", "approve"),
		"alice must not be authorized before policy is seeded")

	// Seed policy directly in the DB (simulating what a remote node would do via the adapter).
	// DefaultModel: p = sub, obj, act  →  v0=sub, v1=obj, v2=act
	//               g = _, _           →  v0=member, v1=role
	_, err = pool.Exec(t.Context(),
		`INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p', 'admin', 'process:42', 'approve')`)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO casbin_rule (ptype, v0, v1) VALUES ('g', 'alice', 'admin')`)
	require.NoError(t, err)

	// Send NOTIFY from "A" so B reloads (payload != B's nodeID → B won't ignore it).
	_, err = pool.Exec(t.Context(), `SELECT pg_notify($1, $2)`, ch, "A")
	require.NoError(t, err)

	// B must reflect the new policy within 5 seconds.
	require.Eventually(t, func() bool {
		return authorizeOK(t, authB, "alice", "process:42", "approve")
	}, 5*time.Second, 50*time.Millisecond,
		"node B must see policy after LISTEN/NOTIFY-triggered reload")
}

// TestNewCasbinAuthorizerFromDB_WithoutWatcher validates that the constructor
// works and returns a functional authorizer even when the watcher is disabled.
func TestNewCasbinAuthorizerFromDB_WithoutWatcher(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, casbinauthz.MigrateCasbin(t.Context(), pool))

	// Seed policy before building the authorizer (no watcher means no reload, so
	// the initial LoadPolicy call at enforcer construction must pick it up).
	_, err := pool.Exec(t.Context(),
		`INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p', 'admin', 'doc', 'read')`)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO casbin_rule (ptype, v0, v1) VALUES ('g', 'bob', 'admin')`)
	require.NoError(t, err)

	a, closer, err := casbinauthz.NewCasbinAuthorizerFromDB(t.Context(), pool,
		casbinauthz.WithoutWatcher(),
	)
	require.NoError(t, err)
	require.NotNil(t, a)
	require.NotNil(t, closer)
	defer func() { _ = closer.Close() }()

	assert.True(t, authorizeOK(t, a, "bob", "doc", "read"),
		"bob must be authorized for doc read via DB-backed policy")
	assert.False(t, authorizeOK(t, a, "carol", "doc", "read"),
		"carol must not be authorized (no matching policy)")
}

// TestNewCasbinAuthorizerFromDB_ReturnsInterface asserts the returned type satisfies
// authz.Authorizer and that closer is non-nil.
func TestNewCasbinAuthorizerFromDB_ReturnsInterface(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, casbinauthz.MigrateCasbin(t.Context(), pool))

	a, closer, err := casbinauthz.NewCasbinAuthorizerFromDB(t.Context(), pool,
		casbinauthz.WithoutWatcher(),
	)
	require.NoError(t, err)

	assert.NotNil(t, a) // a non-nil interface must not wrap a nil concrete authorizer
	assert.NotNil(t, closer)
	_ = closer.Close()
}

// TestNewCasbinAuthorizerFromDB_WithModel validates that WithModel replaces the
// default model. We pass the same model text as DefaultModel to confirm the
// option wiring path works (a different model would need careful policy setup).
func TestNewCasbinAuthorizerFromDB_WithModel(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, casbinauthz.MigrateCasbin(t.Context(), pool))

	a, closer, err := casbinauthz.NewCasbinAuthorizerFromDB(t.Context(), pool,
		casbinauthz.WithModel(casbinauthz.DefaultModel),
		casbinauthz.WithoutWatcher(),
	)
	require.NoError(t, err)
	require.NotNil(t, a)
	defer func() { _ = closer.Close() }()
}
