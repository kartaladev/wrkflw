package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time checks: ChainLinkStore satisfies runtime.ChainLinkStore and
// runtime.ChainLineageReader.
var _ runtime.ChainLinkStore = (*ChainLinkStore)(nil)
var _ runtime.ChainLineageReader = (*ChainLinkStore)(nil)

// ChainLinkStore is the Postgres-backed runtime.ChainLinkStore (process-instance
// chaining lineage, ADR-0045). Record persists one predecessor->successor hop;
// the (predecessor_instance_id, outcome) primary key is the exactly-once
// backstop. Migrate must be applied before calling any method.
type ChainLinkStore struct {
	pool *pgxpool.Pool
}

// NewChainLinkStore constructs a ChainLinkStore over the given pool.
func NewChainLinkStore(pool *pgxpool.Pool) *ChainLinkStore {
	return &ChainLinkStore{pool: pool}
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
			return fmt.Errorf("workflow-postgres: chain links: record: marshal start vars: %w", err)
		}
	}
	_, err := c.pool.Exec(ctx,
		`INSERT INTO wrkflw_chain_links
		   (predecessor_instance_id, outcome, successor_instance_id,
		    predecessor_definition_ref, successor_definition_ref, start_vars, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		link.PredecessorID, string(link.Outcome), link.SuccessorID,
		link.PredecessorDefinitionRef, link.SuccessorDefinitionRef, startVars, link.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return runtime.ErrChainLinkExists
		}
		return fmt.Errorf("workflow-postgres: chain links: record: %w", err)
	}
	return nil
}

// PredecessorOf returns the ChainLink that produced successorID. Returns
// (nil, nil) when successorID was not started by chaining (no row with
// successor_instance_id=$1).
func (c *ChainLinkStore) PredecessorOf(ctx context.Context, successorID string) (*runtime.ChainLink, error) {
	row := c.pool.QueryRow(ctx,
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE successor_instance_id = $1`,
		successorID,
	)
	link, err := scanChainLink(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("workflow-postgres: chain links: predecessor of: %w", err)
	}
	return &link, nil
}

// SuccessorsOf returns all ChainLinks fanned out from predecessorID, ordered by
// outcome for deterministic results. Returns an empty (non-nil) slice when no
// successors exist.
func (c *ChainLinkStore) SuccessorsOf(ctx context.Context, predecessorID string) ([]runtime.ChainLink, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE predecessor_instance_id = $1
		  ORDER BY outcome`,
		predecessorID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-postgres: chain links: successors of: query: %w", err)
	}
	defer rows.Close()

	links := []runtime.ChainLink{}
	for rows.Next() {
		link, err := scanChainLink(rows)
		if err != nil {
			return nil, fmt.Errorf("workflow-postgres: chain links: successors of: scan: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-postgres: chain links: successors of: rows: %w", err)
	}
	return links, nil
}

// LookupBySuccessor returns the link whose successor_instance_id equals
// successorID; ok=false (no error) when no such hop exists.
func (c *ChainLinkStore) LookupBySuccessor(ctx context.Context, successorID string) (runtime.ChainLink, bool, error) {
	row := c.pool.QueryRow(ctx,
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE successor_instance_id = $1`,
		successorID,
	)
	link, err := scanChainLink(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return runtime.ChainLink{}, false, nil
		}
		return runtime.ChainLink{}, false, fmt.Errorf("workflow-postgres: chain links: lookup: %w", err)
	}
	return link, true, nil
}

// ListByPredecessor returns all hops fanned out from predecessorID, ordered by
// outcome for deterministic results.
func (c *ChainLinkStore) ListByPredecessor(ctx context.Context, predecessorID string) ([]runtime.ChainLink, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE predecessor_instance_id = $1
		  ORDER BY outcome`,
		predecessorID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-postgres: chain links: list: query: %w", err)
	}
	defer rows.Close()

	var links []runtime.ChainLink
	for rows.Next() {
		link, err := scanChainLink(rows)
		if err != nil {
			return nil, fmt.Errorf("workflow-postgres: chain links: list: scan: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-postgres: chain links: list: rows: %w", err)
	}
	return links, nil
}

// scanChainLink scans one row (QueryRow or Query result) into a ChainLink. The
// column projection must be: predecessor_instance_id, outcome,
// successor_instance_id, predecessor_definition_ref, successor_definition_ref, start_vars, created_at.
func scanChainLink(row pgx.Row) (runtime.ChainLink, error) {
	var (
		link          runtime.ChainLink
		outcome       string
		startVarsJSON []byte
	)
	if err := row.Scan(
		&link.PredecessorID, &outcome, &link.SuccessorID,
		&link.PredecessorDefinitionRef, &link.SuccessorDefinitionRef, &startVarsJSON, &link.CreatedAt,
	); err != nil {
		return runtime.ChainLink{}, err
	}
	link.Outcome = runtime.Outcome(outcome)
	link.CreatedAt = link.CreatedAt.UTC() // normalize TIMESTAMPTZ to UTC-located (pgx may return host zone)
	if len(startVarsJSON) > 0 {
		if err := json.Unmarshal(startVarsJSON, &link.StartVars); err != nil {
			return runtime.ChainLink{}, fmt.Errorf("workflow-postgres: chain links: unmarshal start vars: %w", err)
		}
	}
	return link, nil
}
