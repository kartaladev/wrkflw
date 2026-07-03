package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// ChainLinkStore is the vendor-neutral, dialect-parametrised
// [kernel.ChainLinkStore] (process-instance chaining lineage, ADR-0045). It
// records one predecessor→successor hop per call to [Record]; the
// (predecessor_instance_id, outcome) primary key is the exactly-once backstop
// under at-least-once terminal-event delivery.
//
// SQL is written once with ? placeholders and run through
// [dialect.Dialect.Rebind] for the backend's native placeholder style. The
// insert-ignore syntax is supplied by the dialect via [InsertIgnorePrefix] and
// [InsertIgnoreDedup], keeping all per-dialect divergences behind the Dialect
// abstraction. Timestamp codec follows [timeArg] / [parseTimeText] (ADR-0080).
//
// ChainLinkStore is safe for concurrent use.
type ChainLinkStore struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// Compile-time checks: *ChainLinkStore satisfies [kernel.ChainLinkStore] and
// [kernel.ChainLineageReader].
var _ kernel.ChainLinkStore = (*ChainLinkStore)(nil)
var _ kernel.ChainLineageReader = (*ChainLinkStore)(nil)

// NewChainLinkStore constructs a ChainLinkStore over conn using the supplied
// dialect d. conn must be either a *pgxpool.Pool (Postgres) or a *sql.DB
// (MySQL, SQLite); any other type causes [database.From] to return an error
// when the first query is issued.
// Returns [ErrNilDependency] when conn is nil or d is nil.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	cls, err := store.NewChainLinkStore(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	cls, err := store.NewChainLinkStore(db, dialect.NewSQLite())
func NewChainLinkStore(conn any, d dialect.Dialect) (*ChainLinkStore, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &ChainLinkStore{conn: conn, dialect: d}, nil
}

// Record durably stores one predecessor→successor hop. A unique-constraint
// violation on (predecessor_instance_id, outcome) maps to
// [kernel.ErrChainLinkExists] — the exactly-once backstop under at-least-once
// terminal-event delivery. A duplicate Record call is not treated as an error
// by the Chainer; it simply skips and acks the redelivered event.
func (c *ChainLinkStore) Record(ctx context.Context, link kernel.ChainLink) error {
	var startVarsJSON []byte
	if link.StartVars != nil {
		var err error
		startVarsJSON, err = json.Marshal(link.StartVars)
		if err != nil {
			return fmt.Errorf("workflow-store: chain links: record: marshal start vars: %w", err)
		}
	}

	at := link.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}

	q, err := database.From(c.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: chain links: record: conn: %w", err)
	}

	// Use the dialect's insert-ignore form so a duplicate (predecessor, outcome)
	// is silently skipped without returning a driver error that callers would
	// have to translate. RowsAffected==0 means the row already exists.
	stmt := c.dialect.Rebind(
		c.dialect.InsertIgnorePrefix() +
			` INTO wrkflw_chain_links
			   (predecessor_instance_id, outcome, successor_instance_id,
			    predecessor_definition_ref, successor_definition_ref, start_vars, created_at)
			 VALUES (?,?,?,?,?,?,?)` +
			c.dialect.InsertIgnoreDedup(),
	)

	res, err := q.Exec(ctx, stmt,
		link.PredecessorID,
		string(link.Outcome),
		link.SuccessorID,
		link.PredecessorDefinitionRef,
		link.SuccessorDefinitionRef,
		startVarsJSON,
		timeArg(c.dialect, at.UTC()),
	)
	if err != nil {
		// Belt-and-suspenders: if the driver surfaces the unique violation as an
		// error (e.g. under a race between two concurrent Record calls), map it.
		if c.dialect.IsUniqueViolation(err) {
			return kernel.ErrChainLinkExists
		}
		return fmt.Errorf("workflow-store: chain links: record: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow-store: chain links: record: rows affected: %w", err)
	}
	if n == 0 {
		// INSERT was silently ignored by the conflict clause — the row already
		// existed; surface this as ErrChainLinkExists.
		return kernel.ErrChainLinkExists
	}
	return nil
}

// LookupBySuccessor returns the link whose successor_instance_id equals
// successorID; ok=false (no error) when no such hop exists (successorID is a
// chain root).
func (c *ChainLinkStore) LookupBySuccessor(ctx context.Context, successorID string) (kernel.ChainLink, bool, error) {
	q, err := database.From(c.conn)
	if err != nil {
		return kernel.ChainLink{}, false, fmt.Errorf("workflow-store: chain links: lookup: conn: %w", err)
	}

	row := q.QueryRow(ctx, c.dialect.Rebind(
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE successor_instance_id = ?`),
		successorID,
	)
	link, err := c.scanChainLink(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kernel.ChainLink{}, false, nil
		}
		return kernel.ChainLink{}, false, fmt.Errorf("workflow-store: chain links: lookup: %w", err)
	}
	return link, true, nil
}

// ListByPredecessor returns all hops fanned out from predecessorID, ordered by
// outcome for deterministic results (admin/audit). Returns a nil slice (no
// error) when no successors exist.
func (c *ChainLinkStore) ListByPredecessor(ctx context.Context, predecessorID string) ([]kernel.ChainLink, error) {
	q, err := database.From(c.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: chain links: list: conn: %w", err)
	}

	rows, err := q.Query(ctx, c.dialect.Rebind(
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE predecessor_instance_id = ?
		  ORDER BY outcome`),
		predecessorID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: chain links: list: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var links []kernel.ChainLink
	for rows.Next() {
		link, err := c.scanChainLink(rows)
		if err != nil {
			return nil, fmt.Errorf("workflow-store: chain links: list: scan: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: chain links: list: rows: %w", err)
	}
	return links, nil
}

// PredecessorOf returns the [kernel.ChainLink] that produced successorID.
// Returns (nil, nil) when successorID was not started by chaining — that is,
// no row with successor_instance_id = successorID exists in wrkflw_chain_links.
//
// Implements [kernel.ChainLineageReader].
func (c *ChainLinkStore) PredecessorOf(ctx context.Context, successorID string) (*kernel.ChainLink, error) {
	q, err := database.From(c.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: chain links: predecessor of: conn: %w", err)
	}

	row := q.QueryRow(ctx, c.dialect.Rebind(
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE successor_instance_id = ?`),
		successorID,
	)
	link, err := c.scanChainLink(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("workflow-store: chain links: predecessor of: %w", err)
	}
	return &link, nil
}

// SuccessorsOf returns all [kernel.ChainLink]s fanned out from predecessorID,
// ordered by outcome for deterministic results. Returns an empty (non-nil) slice
// when no successors exist — the caller must not treat a nil result as distinct
// from an empty result.
//
// Implements [kernel.ChainLineageReader].
func (c *ChainLinkStore) SuccessorsOf(ctx context.Context, predecessorID string) ([]kernel.ChainLink, error) {
	q, err := database.From(c.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: chain links: successors of: conn: %w", err)
	}

	rows, err := q.Query(ctx, c.dialect.Rebind(
		`SELECT predecessor_instance_id, outcome, successor_instance_id,
		        predecessor_definition_ref, successor_definition_ref, start_vars, created_at
		   FROM wrkflw_chain_links
		  WHERE predecessor_instance_id = ?
		  ORDER BY outcome`),
		predecessorID,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: chain links: successors of: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Non-nil empty slice: the interface contract requires callers to receive an
	// empty (not nil) slice when no successors exist.
	links := []kernel.ChainLink{}
	for rows.Next() {
		link, err := c.scanChainLink(rows)
		if err != nil {
			return nil, fmt.Errorf("workflow-store: chain links: successors of: scan: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: chain links: successors of: rows: %w", err)
	}
	return links, nil
}

// chainLinkScanner is the minimal interface satisfied by both a single-row
// query result (from [database.Querier.QueryRow]) and a multi-row cursor (from
// [database.Querier.Query]), allowing [scanChainLink] to be reused for both.
type chainLinkScanner interface {
	Scan(dest ...any) error
}

// scanChainLink reads one row into a [kernel.ChainLink]. The column projection
// must be: predecessor_instance_id, outcome, successor_instance_id,
// predecessor_definition_ref, successor_definition_ref, start_vars, created_at.
//
// The created_at column is handled via the time codec (ADR-0080): SQLite stores
// TEXT as RFC3339Nano ([dialect.Dialect.TimestampsAsText] == true) and requires
// [parseTimeText]; Postgres and MySQL scan into time.Time natively and are then
// normalised to UTC.
func (c *ChainLinkStore) scanChainLink(row chainLinkScanner) (kernel.ChainLink, error) {
	var (
		link          kernel.ChainLink
		outcome       string
		startVarsJSON []byte
	)

	if c.dialect.TimestampsAsText() {
		var createdAtStr string
		if err := row.Scan(
			&link.PredecessorID, &outcome, &link.SuccessorID,
			&link.PredecessorDefinitionRef, &link.SuccessorDefinitionRef,
			&startVarsJSON, &createdAtStr,
		); err != nil {
			return kernel.ChainLink{}, err
		}
		t, err := parseTimeText(createdAtStr)
		if err != nil {
			return kernel.ChainLink{}, fmt.Errorf("workflow-store: chain links: parse created_at: %w", err)
		}
		link.CreatedAt = t // already UTC from parseTimeText
	} else {
		// Native time.Time path (Postgres TIMESTAMPTZ / MySQL DATETIME(6)).
		if err := row.Scan(
			&link.PredecessorID, &outcome, &link.SuccessorID,
			&link.PredecessorDefinitionRef, &link.SuccessorDefinitionRef,
			&startVarsJSON, &link.CreatedAt,
		); err != nil {
			return kernel.ChainLink{}, err
		}
		link.CreatedAt = link.CreatedAt.UTC() // normalise to UTC (ADR-0080)
	}

	link.Outcome = kernel.Outcome(outcome)
	if len(startVarsJSON) > 0 {
		if err := json.Unmarshal(startVarsJSON, &link.StartVars); err != nil {
			return kernel.ChainLink{}, fmt.Errorf("workflow-store: chain links: unmarshal start vars: %w", err)
		}
	}
	return link, nil
}
