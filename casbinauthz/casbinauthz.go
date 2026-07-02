// Package casbinauthz is the consumer-facing façade for the casbin-backed
// authz.Authorizer. It is the only module-root package that imports casbin; the
// concrete evaluator lives in internal/authz/casbin.
//
// Consumers wire this package directly:
//
//	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings("", policyCSV))
//	// or
//	a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromEnforcer(syncedEnforcer))
//
// The returned authz.Authorizer is the stable port type; no internal types are
// exposed. The [Authorizer] concrete type additionally implements ReloadPolicy
// for hot-reloading the casbin policy without restarting the application.
package casbinauthz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/authz"
	internalcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
)

// Sentinel errors returned by [NewCasbinAuthorizer] when the caller supplies an
// invalid number of source options.
var (
	ErrNoAuthorizerSource        = errors.New("workflow-casbinauthz: no source configured")
	ErrMultipleAuthorizerSources = errors.New("workflow-casbinauthz: multiple sources configured")
)

// errNilSource is an unexported sentinel used inside source options to signal a
// nil argument. It is not exported because the wrapping fmt.Errorf adds context.
var errNilSource = errors.New("nil source")

// builderConfig accumulates the result of applying [Option]s. Exactly one
// source may be registered; the build func is set by the winning source option.
type builderConfig struct {
	build func() (authz.Authorizer, io.Closer, error)
	count int
}

// Option is a functional option for [NewCasbinAuthorizer]. Supply exactly one
// source option ([FromEnforcer], [FromStrings], or [FromDB]); zero or multiple
// sources are errors.
type Option func(*builderConfig) error

// FromEnforcer returns an [Option] that wraps a consumer-built
// [*casbinv2.SyncedEnforcer]. The returned closer is nil because the enforcer is
// managed by the caller.
func FromEnforcer(e *casbinv2.SyncedEnforcer) Option {
	return func(c *builderConfig) error {
		if e == nil {
			return fmt.Errorf("workflow-casbinauthz: %w: enforcer", errNilSource)
		}
		c.count++
		c.build = func() (authz.Authorizer, io.Closer, error) {
			return newFromEnforcer(e), nil, nil
		}
		return nil
	}
}

// FromStrings returns an [Option] that builds a [*casbinv2.SyncedEnforcer] from
// plain model and policy strings (using casbin's bundled string adapter). An
// empty modelText defaults to [DefaultModel]. The returned closer is nil.
func FromStrings(modelText, policyText string) Option {
	return func(c *builderConfig) error {
		c.count++
		c.build = func() (authz.Authorizer, io.Closer, error) {
			return newFromStrings(modelText, policyText)
		}
		return nil
	}
}

// FromDB returns an [Option] that builds a hybrid casbin [authz.Authorizer]
// whose policy is loaded from the casbin_rule table in pool, with an optional
// LISTEN/NOTIFY watcher that reloads policy when another node changes it.
//
// Call [MigrateCasbin] before using this option to ensure the schema exists.
//
// The returned [io.Closer] stops the watcher goroutine at shutdown.
func FromDB(ctx context.Context, pool *pgxpool.Pool, opts ...DBOption) Option {
	return func(c *builderConfig) error {
		c.count++
		c.build = func() (authz.Authorizer, io.Closer, error) {
			return newFromDB(ctx, pool, opts...)
		}
		return nil
	}
}

// NewCasbinAuthorizer builds a casbin-backed [authz.Authorizer] from the
// supplied source options. Exactly one source option ([FromEnforcer],
// [FromStrings], or [FromDB]) must be supplied.
//
// Returns [ErrNoAuthorizerSource] when no source is given and
// [ErrMultipleAuthorizerSources] when more than one is given.
//
// The second return value is an [io.Closer] that must be closed at shutdown
// when using [FromDB] (the watcher goroutine). For [FromEnforcer] and
// [FromStrings] it is nil.
func NewCasbinAuthorizer(opts ...Option) (authz.Authorizer, io.Closer, error) {
	var cfg builderConfig
	for _, o := range opts {
		if err := o(&cfg); err != nil {
			return nil, nil, err
		}
	}
	switch {
	case cfg.count == 0:
		return nil, nil, ErrNoAuthorizerSource
	case cfg.count > 1:
		return nil, nil, ErrMultipleAuthorizerSources
	}
	return cfg.build()
}

// DefaultModel is a combined RBAC (g) + resource-privilege (p) casbin model used
// when [FromStrings] receives an empty model text.
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
// Obtain one via [NewCasbinAuthorizer]; never construct it directly.
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
		return fmt.Errorf("workflow-casbinauthz: reload policy: %w", err)
	}
	return nil
}

// newFromEnforcer is the shared implementation for [FromEnforcer]. It wraps a
// pre-built enforcer into the facade type.
func newFromEnforcer(e *casbinv2.SyncedEnforcer) authz.Authorizer {
	return &Authorizer{inner: internalcasbin.New(e), enforcer: e}
}

// newFromStrings builds a [*casbinv2.SyncedEnforcer] from plain model and policy
// strings (using casbin's bundled string adapter) and returns the authorizer. An
// empty modelText defaults to [DefaultModel].
//
// Returns an error if the model string is malformed or the enforcer cannot be
// initialised; never panics.
func newFromStrings(modelText, policyText string) (authz.Authorizer, io.Closer, error) {
	if modelText == "" {
		modelText = DefaultModel
	}
	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return nil, nil, fmt.Errorf("workflow-casbinauthz: parse model: %w", err)
	}
	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter(policyText))
	if err != nil {
		return nil, nil, fmt.Errorf("workflow-casbinauthz: build enforcer: %w", err)
	}
	return newFromEnforcer(e), nil, nil
}

// newFromDB builds a hybrid casbin [authz.Authorizer] whose policy is loaded
// from (and saved to) the casbin_rule table in pool, with an optional
// LISTEN/NOTIFY watcher that reloads policy when another node changes it.
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
func newFromDB(ctx context.Context, pool *pgxpool.Pool, opts ...DBOption) (authz.Authorizer, io.Closer, error) {
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
	return newFromEnforcer(enforcer), closer, nil
}

// MigrateCasbin applies the casbin_rule schema to pool (tracked in its own
// casbin_goose_db_version table, independent of persistence.Migrate). Call it
// before [FromDB]. Never auto-run on import.
func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error {
	return internalcasbin.MigrateCasbin(ctx, pool)
}

// DBOption is a functional option for [FromDB].
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
