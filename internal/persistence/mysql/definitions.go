package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time assertion: DefinitionStore satisfies runtime.DefinitionRegistry.
var _ runtime.DefinitionRegistry = (*DefinitionStore)(nil)

// DefinitionStore is the MySQL-backed durable process-definition store.
//
// It satisfies [runtime.DefinitionRegistry] via [DefinitionStore.Lookup], which
// resolves a DefRef of the form "defID:version" (exact match) or "defID" (latest
// version by descending version order).
//
// Definitions are serialised as JSON into wrkflw_definitions and deserialised
// by [GetDefinition] and [Lookup].
type DefinitionStore struct {
	db *sql.DB
}

// NewDefinitionStore constructs a DefinitionStore over db.
// db must already be connected; call [Migrate] before first use.
func NewDefinitionStore(db *sql.DB) *DefinitionStore {
	return &DefinitionStore{db: db}
}

// PutDefinition upserts a definition into wrkflw_definitions, keyed by
// (def_id, version). The operation is idempotent: re-inserting the same
// (defID, version) pair overwrites the stored JSON with the new value.
//
// def.ID and def.Version must be non-empty / non-zero; the database schema
// enforces uniqueness on (def_id, version).
func (d *DefinitionStore) PutDefinition(ctx context.Context, def *model.ProcessDefinition) error {
	data, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("workflow-persistence-mysql: put definition %s:%d: marshal: %w", def.ID, def.Version, err)
	}

	_, err = d.db.ExecContext(ctx,
		`INSERT INTO wrkflw_definitions (def_id, version, definition, created_at)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE definition = VALUES(definition)`,
		def.ID, def.Version, data, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("workflow-persistence-mysql: put definition %s:%d: %w", def.ID, def.Version, err)
	}
	return nil
}

// GetDefinition fetches the definition identified by (defID, version).
// Returns ([runtime.ErrDefinitionNotFound]) when no row matches.
func (d *DefinitionStore) GetDefinition(ctx context.Context, defID string, version int) (*model.ProcessDefinition, error) {
	var data []byte
	err := d.db.QueryRowContext(ctx,
		`SELECT definition FROM wrkflw_definitions WHERE def_id = ? AND version = ?`,
		defID, version,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s:%d", runtime.ErrDefinitionNotFound, defID, version)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: get definition %s:%d: %w", defID, version, err)
	}

	var def model.ProcessDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: get definition %s:%d: unmarshal: %w", defID, version, err)
	}
	return &def, nil
}

// Lookup satisfies [runtime.DefinitionRegistry]. defRef is interpreted as:
//   - "defID:version" — exact (defID, version) lookup via [GetDefinition].
//   - "defID"         — the definition with the highest version for defID.
//
// Returns ([runtime.ErrDefinitionNotFound]) when no matching row exists.
// ctx is passed to the SQL query, enabling cancellation propagation.
func (d *DefinitionStore) Lookup(ctx context.Context, defRef string) (*model.ProcessDefinition, error) {
	if id, ver, ok := strings.Cut(defRef, ":"); ok {
		n, err := strconv.Atoi(ver)
		if err != nil {
			return nil, fmt.Errorf("workflow-persistence-mysql: lookup %q: bad version segment: %w", defRef, err)
		}
		return d.GetDefinition(ctx, id, n)
	}

	// No colon: return the latest version.
	var data []byte
	err := d.db.QueryRowContext(ctx,
		`SELECT definition FROM wrkflw_definitions
		 WHERE def_id = ?
		 ORDER BY version DESC
		 LIMIT 1`,
		defRef,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", runtime.ErrDefinitionNotFound, defRef)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: lookup %q: %w", defRef, err)
	}

	var def model.ProcessDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: lookup %q: unmarshal: %w", defRef, err)
	}
	return &def, nil
}
