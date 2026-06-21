// Package casbinauthz is the consumer-facing façade for the casbin-backed
// authz.Authorizer. It is the only module-root package that imports casbin; the
// concrete evaluator lives in internal/authz/casbin.
//
// Consumers wire this package directly:
//
//	a, err := casbinauthz.NewCasbinAuthorizerFromStrings("", policyCSV)
//	// or
//	a := casbinauthz.NewCasbinAuthorizer(syncedEnforcer)
//
// The returned authz.Authorizer is the stable port type; no internal types are
// exposed. The [Authorizer] concrete type additionally implements ReloadPolicy
// for hot-reloading the casbin policy without restarting the application.
package casbinauthz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/authz"
	internalcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
)

// DefaultModel is a combined RBAC (g) + resource-privilege (p) casbin model used
// when [NewCasbinAuthorizerFromStrings] receives an empty model text.
//
// The matcher uses g(r.sub, p.sub) so that inherited roles (via g lines) are
// taken into account, and accepts wildcard "*" on both obj and act so that
// broad grant lines like `p, manager, approve, *` work alongside fine-grained
// ones. The attribute predicate is NOT modeled here — it is evaluated by
// expr-lang (see ADR-0010).
const DefaultModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && (r.obj == p.obj || p.obj == "*") && (r.act == p.act || p.act == "*")
`

// Authorizer is the module-root façade type. It satisfies [authz.Authorizer] and
// additionally exposes [Authorizer.ReloadPolicy] for consumers that want to
// hot-reload after a policy change without restarting the application.
//
// Obtain one via [NewCasbinAuthorizer] or [NewCasbinAuthorizerFromStrings];
// never construct it directly.
type Authorizer struct {
	inner    *internalcasbin.Authorizer
	enforcer *casbinv2.SyncedEnforcer
}

var _ authz.Authorizer = (*Authorizer)(nil)

// Authorize delegates to the internal casbin evaluator.
func (a *Authorizer) Authorize(ctx context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error {
	return a.inner.Authorize(ctx, spec, actor, vars)
}

// ReloadPolicy reloads the enforcer's policy from its backing adapter. Useful
// when the policy CSV is stored externally (file, DB) and has been updated.
func (a *Authorizer) ReloadPolicy() error {
	if err := a.enforcer.LoadPolicy(); err != nil {
		return fmt.Errorf("casbinauthz: reload policy: %w", err)
	}
	return nil
}

// NewCasbinAuthorizer wraps a consumer-built [*casbin.SyncedEnforcer] and
// returns an [authz.Authorizer]. The returned value also implements
// ReloadPolicy (accessible via type assertion).
func NewCasbinAuthorizer(e *casbinv2.SyncedEnforcer) authz.Authorizer {
	return &Authorizer{inner: internalcasbin.New(e), enforcer: e}
}

// NewCasbinAuthorizerFromStrings builds a [*casbin.SyncedEnforcer] from plain
// model and policy strings (using casbin's bundled string adapter) and returns
// the authorizer. An empty modelText defaults to [DefaultModel].
//
// Returns an error if the model string is malformed or the enforcer cannot be
// initialised; never panics.
func NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error) {
	if modelText == "" {
		modelText = DefaultModel
	}
	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return nil, fmt.Errorf("casbinauthz: parse model: %w", err)
	}
	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter(policyText))
	if err != nil {
		return nil, fmt.Errorf("casbinauthz: build enforcer: %w", err)
	}
	return NewCasbinAuthorizer(e), nil
}

// MigrateCasbin applies the casbin_rule schema to pool (tracked in its own
// casbin_goose_db_version table, independent of persistence.Migrate). Call it
// before NewCasbinAuthorizerFromDB. Never auto-run on import.
func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error {
	return internalcasbin.MigrateCasbin(ctx, pool)
}

// DBOption is a functional option for [NewCasbinAuthorizerFromDB].
type DBOption func(*internalcasbin.DBConfig)

// WithModel overrides the casbin model text. Defaults to [DefaultModel].
func WithModel(text string) DBOption {
	return func(c *internalcasbin.DBConfig) { c.ModelText = text }
}

// WithoutWatcher disables the LISTEN/NOTIFY policy-reload watcher. Use this for
// single-node deployments where cross-node reload is unnecessary.
func WithoutWatcher() DBOption {
	return func(c *internalcasbin.DBConfig) { c.WatcherEnabled = false }
}

// WithWatcherChannel overrides the Postgres NOTIFY channel name. Defaults to
// "wrkflw_casbin_policy". Only effective when the watcher is enabled.
func WithWatcherChannel(name string) DBOption {
	return func(c *internalcasbin.DBConfig) { c.WatcherChannel = name }
}

// WithNodeID overrides the node identifier used to suppress self-notifications.
// Defaults to a random process-unique value. Only effective when the watcher is
// enabled.
func WithNodeID(id string) DBOption {
	return func(c *internalcasbin.DBConfig) { c.NodeID = id }
}

// defaultNodeID returns a process-unique node identifier derived from crypto/rand.
// This is appropriate for edge infrastructure where a random value is sufficient.
func defaultNodeID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "node-" + hex.EncodeToString(b[:])
}

// NewCasbinAuthorizerFromDB builds a hybrid casbin [authz.Authorizer] whose
// policy is loaded from (and saved to) the casbin_rule table in pool, with an
// optional LISTEN/NOTIFY watcher that reloads policy when another node changes it.
//
// Call [MigrateCasbin] before this function to ensure the schema exists.
//
// The returned [io.Closer] stops the watcher goroutine at shutdown. Always close
// it, even when the watcher is disabled (the no-op closer is safe to call). On
// error, any partially started watcher is closed before returning.
//
// Default configuration:
//   - Model: [DefaultModel] (combined RBAC + resource-privilege)
//   - Watcher: enabled on channel "wrkflw_casbin_policy"
//   - NodeID: random process-unique value
func NewCasbinAuthorizerFromDB(ctx context.Context, pool *pgxpool.Pool, opts ...DBOption) (authz.Authorizer, io.Closer, error) {
	cfg := internalcasbin.DBConfig{
		ModelText:      DefaultModel,
		WatcherEnabled: true,
		WatcherChannel: "wrkflw_casbin_policy",
		NodeID:         defaultNodeID(),
	}
	for _, o := range opts {
		o(&cfg)
	}
	enforcer, closer, err := internalcasbin.NewDBEnforcer(ctx, pool, cfg)
	if err != nil {
		return nil, nil, err
	}
	// Reuse the existing single wrapping path: NewCasbinAuthorizer(enforcer).
	return NewCasbinAuthorizer(enforcer), closer, nil
}
