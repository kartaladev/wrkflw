package persistence

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// DurableProvider is a coherent set of durable graph leaves for one backend,
// suitable for service.WithDurableStore. It bundles the durable instance store,
// definition registry, lister, human-task store, timer store, and call-link
// store so a consumer can flip the whole service graph durable in one call.
//
// Construct it with NewDurableProvider (Postgres), NewMySQLDurableProvider, or
// NewSQLiteDurableProvider. The backend schema must already be migrated (see
// Migrate / MigrateMySQL / MigrateSQLite).
type DurableProvider struct {
	instanceStore kernel.InstanceStore
	definitions   kernel.DefinitionRegistry
	lister        kernel.InstanceLister
	taskStore     humantask.TaskStore
	timerStore    kernel.TimerStore
	callLinkStore kernel.CallLinkStore
}

func (p *DurableProvider) InstanceStore() kernel.InstanceStore    { return p.instanceStore }
func (p *DurableProvider) Definitions() kernel.DefinitionRegistry { return p.definitions }
func (p *DurableProvider) Lister() kernel.InstanceLister          { return p.lister }
func (p *DurableProvider) TaskStore() humantask.TaskStore         { return p.taskStore }
func (p *DurableProvider) TimerStore() kernel.TimerStore          { return p.timerStore }
func (p *DurableProvider) CallLinkStore() kernel.CallLinkStore    { return p.callLinkStore }

// NewDurableProvider builds a PostgreSQL-backed provider over pool. The schema
// must already be migrated (persistence.Migrate).
func NewDurableProvider(ctx context.Context, pool *pgxpool.Pool) (*DurableProvider, error) {
	is, err := OpenPostgres(ctx, pool)
	if err != nil {
		return nil, err
	}
	defs, err := NewDefinitionStore(pool)
	if err != nil {
		return nil, err
	}
	lister, err := NewLister(pool)
	if err != nil {
		return nil, err
	}
	tasks, err := NewTaskStore(pool)
	if err != nil {
		return nil, err
	}
	timers, err := NewTimerStore(pool)
	if err != nil {
		return nil, err
	}
	links, err := NewCallLinkStore(pool)
	if err != nil {
		return nil, err
	}
	return &DurableProvider{
		instanceStore: is,
		definitions:   defs,
		lister:        lister,
		taskStore:     tasks,
		timerStore:    timers,
		callLinkStore: links,
	}, nil
}

// NewMySQLDurableProvider builds a MySQL-backed provider over db. The schema
// must already be migrated (persistence.MigrateMySQL).
func NewMySQLDurableProvider(ctx context.Context, db *sql.DB) (*DurableProvider, error) {
	is, err := OpenMySQL(ctx, db)
	if err != nil {
		return nil, err
	}
	defs, err := NewMySQLDefinitionStore(db)
	if err != nil {
		return nil, err
	}
	lister, err := NewMySQLLister(db)
	if err != nil {
		return nil, err
	}
	tasks, err := NewMySQLTaskStore(db)
	if err != nil {
		return nil, err
	}
	timers, err := NewMySQLTimerStore(db)
	if err != nil {
		return nil, err
	}
	links, err := NewMySQLCallLinkStore(db)
	if err != nil {
		return nil, err
	}
	return &DurableProvider{
		instanceStore: is,
		definitions:   defs,
		lister:        lister,
		taskStore:     tasks,
		timerStore:    timers,
		callLinkStore: links,
	}, nil
}

// NewSQLiteDurableProvider builds a SQLite-backed provider over db. The schema
// must already be migrated (persistence.MigrateSQLite). Remember SQLite requires
// db.SetMaxOpenConns(1).
func NewSQLiteDurableProvider(ctx context.Context, db *sql.DB) (*DurableProvider, error) {
	is, err := OpenSQLite(ctx, db)
	if err != nil {
		return nil, err
	}
	defs, err := NewSQLiteDefinitionStore(db)
	if err != nil {
		return nil, err
	}
	lister, err := NewSQLiteLister(db)
	if err != nil {
		return nil, err
	}
	tasks, err := NewSQLiteTaskStore(db)
	if err != nil {
		return nil, err
	}
	timers, err := NewSQLiteTimerStore(db)
	if err != nil {
		return nil, err
	}
	links, err := NewSQLiteCallLinkStore(db)
	if err != nil {
		return nil, err
	}
	return &DurableProvider{
		instanceStore: is,
		definitions:   defs,
		lister:        lister,
		taskStore:     tasks,
		timerStore:    timers,
		callLinkStore: links,
	}, nil
}
