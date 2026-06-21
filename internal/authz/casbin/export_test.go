package casbin

import (
	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPGAdapter exposes the unexported pgAdapter constructor for black-box tests.
func NewPGAdapter(pool *pgxpool.Pool) persist.Adapter { return newPGAdapter(pool) }

// NewPGWatcher exposes the unexported pgWatcher constructor for black-box tests.
func NewPGWatcher(pool *pgxpool.Pool, channel, nodeID string) persist.Watcher {
	return newPGWatcher(pool, channel, nodeID)
}
