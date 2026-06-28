package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time check: ChainLinkStore satisfies runtime.ChainLinkStore.
var _ runtime.ChainLinkStore = (*ChainLinkStore)(nil)

// ChainLinkStore is the MySQL-backed runtime.ChainLinkStore (process-instance
// chaining lineage, ADR-0045). Record persists one predecessor->successor hop;
// the (predecessor_instance_id, outcome) primary key is the exactly-once
// backstop. Migrate must be applied before calling any method.
type ChainLinkStore struct {
	db *sql.DB
}

// NewChainLinkStore constructs a ChainLinkStore over the given *sql.DB.
// Migrate must be applied before calling any method.
func NewChainLinkStore(db *sql.DB) *ChainLinkStore {
	return &ChainLinkStore{db: db}
}

// Record stores one predecessor->successor hop. A unique-violation on
// (predecessor_instance_id, outcome) maps to runtime.ErrChainLinkExists — the
// exactly-once backstop under at-least-once terminal-event delivery.
func (c *ChainLinkStore) Record(ctx context.Context, link runtime.ChainLink) error {
	var startVars []byte
	if link.StartVars != nil {
		var err error
		startVars, err = json.Marshal(link.StartVars)
		if err != nil {
			return fmt.Errorf("workflow-persistence-mysql: chain links: record: marshal start vars: %w", err)
		}
	}
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO wrkflw_chain_links
		   (predecessor_instance_id, outcome, successor_instance_id,
		    predecessor_definition_ref, successor_definition_ref, start_vars, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		link.PredecessorID, string(link.Outcome), link.SuccessorID,
		link.PredecessorDefinitionRef, link.SuccessorDefinitionRef, startVars, link.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return runtime.ErrChainLinkExists
		}
		return fmt.Errorf("workflow-persistence-mysql: chain links: record: %w", err)
	}
	return nil
}

// LookupBySuccessor returns the link whose successor_instance_id equals
// successorID; ok=false (no error) when no such hop exists.
func (c *ChainLinkStore) LookupBySuccessor(ctx context.Context, successorID string) (runtime.ChainLink, bool, error) {
	row := c.db.QueryRowContext(ctx,
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE successor_instance_id = ?`,
		successorID,
	)
	link, err := scanMySQLChainLink(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runtime.ChainLink{}, false, nil
		}
		return runtime.ChainLink{}, false, fmt.Errorf("workflow-persistence-mysql: chain links: lookup: %w", err)
	}
	return link, true, nil
}

// ListByPredecessor returns all hops fanned out from predecessorID, ordered by
// outcome for deterministic results.
func (c *ChainLinkStore) ListByPredecessor(ctx context.Context, predecessorID string) ([]runtime.ChainLink, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE predecessor_instance_id = ?
		  ORDER BY outcome`,
		predecessorID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: chain links: list: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []runtime.ChainLink
	for rows.Next() {
		link, err := scanMySQLChainLink(rows)
		if err != nil {
			return nil, fmt.Errorf("workflow-persistence-mysql: chain links: list: scan: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: chain links: list: rows: %w", err)
	}
	return links, nil
}

// rowScanner is the minimal interface satisfied by both *sql.Row and *sql.Rows,
// allowing scanMySQLChainLink to be used for both QueryRow and Query results.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanMySQLChainLink scans one row into a ChainLink. The column projection must
// be: predecessor_instance_id, outcome, successor_instance_id,
// predecessor_definition_ref, successor_definition_ref, start_vars, created_at.
func scanMySQLChainLink(row rowScanner) (runtime.ChainLink, error) {
	var (
		link          runtime.ChainLink
		outcome       string
		startVarsJSON []byte
	)
	if err := row.Scan(
		&link.PredecessorID, &outcome, &link.SuccessorID,
		&link.PredecessorDefinitionRef, &link.SuccessorDefinitionRef,
		&startVarsJSON, &link.CreatedAt,
	); err != nil {
		return runtime.ChainLink{}, err
	}
	link.Outcome = runtime.Outcome(outcome)
	if len(startVarsJSON) > 0 {
		if err := json.Unmarshal(startVarsJSON, &link.StartVars); err != nil {
			return runtime.ChainLink{}, fmt.Errorf("workflow-persistence-mysql: chain links: unmarshal start vars: %w", err)
		}
	}
	return link, nil
}
