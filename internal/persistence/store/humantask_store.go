package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// HumanTaskStore is the neutral, dialect-parametrised SQL implementation of
// [humantask.TaskStore] over the wrkflw_human_task table. It works on
// PostgreSQL, MySQL, and SQLite via the dialect abstraction.
//
// SQL is written once with ? placeholders and run through
// [dialect.Dialect.Rebind] for the backend's native placeholder style.
// Timestamp codec follows the same pattern as [TimerStore]: Postgres and MySQL
// bind and scan time.Time natively; SQLite stores TEXT as RFC3339Nano and needs
// the [parseTimeText] helper on the read side (ADR-0080). The codec is gated on
// [dialect.Dialect.TimestampsAsText] — NEVER compare [dialect.Dialect.Name]
// to "sqlite" directly.
//
// HumanTaskStore is safe for concurrent use: it carries no mutable state.
type HumanTaskStore struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// Compile-time check that *HumanTaskStore satisfies the public port.
var _ humantask.TaskStore = (*HumanTaskStore)(nil)

// NewHumanTaskStore constructs a durable task store over conn (a *pgxpool.Pool
// or *sql.DB) using the supplied dialect. Returns [ErrNilDependency] when conn
// or d is nil.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	ts, err := store.NewHumanTaskStore(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	ts, err := store.NewHumanTaskStore(db, dialect.NewSQLite())
func NewHumanTaskStore(conn any, d dialect.Dialect) (*HumanTaskStore, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &HumanTaskStore{conn: conn, dialect: d}, nil
}

func (s *HumanTaskStore) querier() database.Querier {
	q, _ := database.From(s.conn)
	return q
}

// humanTaskColumns is the canonical column list used in SELECT and INSERT
// statements. Order must match the scan order in [scanTask].
const humanTaskColumns = `task_token, instance_id, node_id, state, claimed_by,
	eligibility, candidates, vars, created_at, due_at, def_id, def_version`

// Upsert inserts or replaces the task identified by t.TaskToken.
// The upsert conflict clause is dialect-specific (via [dialect.Dialect.UpsertTask]).
func (s *HumanTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	eligibility, err := json.Marshal(t.Eligibility)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal eligibility: %w", t.TaskToken, err)
	}
	candidates, err := json.Marshal(t.Candidates)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal candidates: %w", t.TaskToken, err)
	}
	vars, err := json.Marshal(t.Vars)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal vars: %w", t.TaskToken, err)
	}

	q := s.querier()
	_, err = q.Exec(ctx, s.dialect.Rebind(
		`INSERT INTO wrkflw_human_task (`+humanTaskColumns+`)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`+s.dialect.UpsertTask()),
		t.TaskToken, t.InstanceID, t.NodeID, t.State.String(), t.ClaimedBy,
		eligibility, candidates, vars,
		timeArg(s.dialect, t.CreatedAt), s.dueArg(t.DueAt),
		t.DefID, t.DefVersion,
	)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: %w", t.TaskToken, err)
	}
	return nil
}

// Get returns the task for the given token or [humantask.ErrTaskNotFound].
func (s *HumanTaskStore) Get(ctx context.Context, taskToken string) (humantask.HumanTask, error) {
	q := s.querier()
	row := q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE task_token = ?`), taskToken)
	t, err := s.scanTask(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return humantask.HumanTask{}, humantask.ErrTaskNotFound
	}
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("workflow-store: get task %s: %w", taskToken, err)
	}
	return t, nil
}

// AssignedTo returns all tasks currently claimed by actorID, sorted by TaskToken.
func (s *HumanTaskStore) AssignedTo(ctx context.Context, actorID string) ([]humantask.HumanTask, error) {
	return s.query(ctx, "assigned-to",
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE claimed_by = ? ORDER BY task_token`,
		actorID)
}

// ClaimableBy returns all Unclaimed tasks for which the actor is eligible.
// Eligibility is granted when actor.ID is in Candidates OR actor.Roles and
// task Eligibility.Roles share at least one value. Results are sorted by
// TaskToken. The SQL WHERE clause restricts to Unclaimed rows; the Go loop
// then applies the candidate/role eligibility filter.
func (s *HumanTaskStore) ClaimableBy(ctx context.Context, actor authz.Actor) ([]humantask.HumanTask, error) {
	rows, err := s.query(ctx, "claimable-by",
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE state = ? ORDER BY task_token`,
		humantask.Unclaimed.String())
	if err != nil {
		return nil, err
	}
	actorRoles := htRoleSet(actor.Roles)
	var result []humantask.HumanTask
	for _, t := range rows {
		if htCandidateContains(t.Candidates, actor.ID) || htHasRoleOverlap(actorRoles, t.Eligibility.Roles) {
			result = append(result, t)
		}
	}
	return result, nil
}

// query executes a SELECT query and returns the scanned tasks.
func (s *HumanTaskStore) query(ctx context.Context, op, sqlText string, args ...any) ([]humantask.HumanTask, error) {
	q := s.querier()
	rows, err := q.Query(ctx, s.dialect.Rebind(sqlText), args...)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: human task %s: %w", op, err)
	}
	defer func() { _ = rows.Close() }()

	var result []humantask.HumanTask
	for rows.Next() {
		t, err := s.scanTask(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("workflow-store: human task %s: scan: %w", op, err)
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: human task %s: rows: %w", op, err)
	}
	return result, nil
}

// scanTask decodes one row via the supplied Scan function (works for both
// database.Row.Scan and database.Rows.Scan).
//
// Timestamp handling mirrors [TimerStore.scanArmedTimer]:
//   - SQLite (TimestampsAsText): scan created_at into string, due_at into
//     sql.NullString, then parse with [parseTimeText].
//   - Postgres/MySQL: scan created_at into time.Time, due_at into sql.NullTime,
//     then normalise to UTC.
func (s *HumanTaskStore) scanTask(scan func(dest ...any) error) (humantask.HumanTask, error) {
	var (
		t           humantask.HumanTask
		stateStr    string
		eligibility []byte
		candidates  []byte
		vars        []byte
	)

	if s.dialect.TimestampsAsText() {
		// SQLite TEXT timestamp path.
		var createdStr string
		var dueStr sql.NullString
		if err := scan(
			&t.TaskToken, &t.InstanceID, &t.NodeID, &stateStr, &t.ClaimedBy,
			&eligibility, &candidates, &vars, &createdStr, &dueStr,
			&t.DefID, &t.DefVersion,
		); err != nil {
			return humantask.HumanTask{}, err
		}
		ct, err := parseTimeText(createdStr)
		if err != nil {
			return humantask.HumanTask{}, fmt.Errorf("parse created_at: %w", err)
		}
		t.CreatedAt = ct
		if dueStr.Valid {
			dt, err := parseTimeText(dueStr.String)
			if err != nil {
				return humantask.HumanTask{}, fmt.Errorf("parse due_at: %w", err)
			}
			t.DueAt = &dt
		}
	} else {
		// Native time.Time path (Postgres / MySQL).
		var createdAt time.Time
		var dueAt sql.NullTime
		if err := scan(
			&t.TaskToken, &t.InstanceID, &t.NodeID, &stateStr, &t.ClaimedBy,
			&eligibility, &candidates, &vars, &createdAt, &dueAt,
			&t.DefID, &t.DefVersion,
		); err != nil {
			return humantask.HumanTask{}, err
		}
		t.CreatedAt = createdAt.UTC()
		if dueAt.Valid {
			dt := dueAt.Time.UTC()
			t.DueAt = &dt
		}
	}

	t.State = htParseTaskState(stateStr)
	if len(eligibility) > 0 {
		if err := json.Unmarshal(eligibility, &t.Eligibility); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("unmarshal eligibility: %w", err)
		}
	}
	if len(candidates) > 0 {
		if err := json.Unmarshal(candidates, &t.Candidates); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("unmarshal candidates: %w", err)
		}
	}
	if len(vars) > 0 {
		if err := json.Unmarshal(vars, &t.Vars); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("unmarshal vars: %w", err)
		}
	}
	return t, nil
}

// dueArg converts an optional *time.Time to the dialect-correct bind value.
// nil stays nil (writes NULL); non-nil values go through [timeArg].
func (s *HumanTaskStore) dueArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeArg(s.dialect, *t)
}

// htParseTaskState converts the stored string back to a [humantask.TaskState].
func htParseTaskState(s string) humantask.TaskState {
	switch s {
	case humantask.Claimed.String():
		return humantask.Claimed
	case humantask.Completed.String():
		return humantask.Completed
	case humantask.Cancelled.String():
		return humantask.Cancelled
	default:
		return humantask.Unclaimed
	}
}

// ─── eligibility helpers ──────────────────────────────────────────────────────
//
// These mirror the unexported helpers in humantask/memory.go. They are
// prefixed "ht" to avoid redeclaring the identically-named functions if this
// file is ever compiled alongside a future internal package that imports them.

// htRoleSet builds a set from a slice of role strings for O(1) lookup.
func htRoleSet(roles []string) map[string]struct{} {
	s := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		s[r] = struct{}{}
	}
	return s
}

// htCandidateContains reports whether id appears in the candidates slice.
func htCandidateContains(candidates []string, id string) bool {
	for _, c := range candidates {
		if c == id {
			return true
		}
	}
	return false
}

// htHasRoleOverlap reports whether specRoles contains any role present in actorSet.
func htHasRoleOverlap(actorSet map[string]struct{}, specRoles []string) bool {
	for _, r := range specRoles {
		if _, ok := actorSet[r]; ok {
			return true
		}
	}
	return false
}
