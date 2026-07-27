package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/observability"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// HumanTaskStore is the neutral, dialect-parametrised SQL implementation of
// [humantask.TaskStore] over the wrkflw_human_task table. It works on
// PostgreSQL, MySQL, and SQLite via the dialect abstraction.
//
// SQL is written once with ? placeholders and run through
// [dialect.Dialect.Rebind] for the backend's native placeholder style.
// Timestamp codec follows the same pattern as [TimerStore]: Postgres and MySQL
// bind and scan time.Time natively; SQLite stores TEXT, written by [timeArg] as
// UTC RFC3339 with a FIXED-WIDTH nine-digit fraction — never time.RFC3339Nano,
// whose trimmed fraction does not sort lexicographically (ADR-0080, ADR-0151) —
// and read back through [parseTimeText], which stays tolerant of any fraction
// width so pre-ADR-0151 rows keep parsing. The codec is gated on
// [dialect.Dialect.TimestampsAsText] — NEVER compare [dialect.Dialect.Name]
// to "sqlite" directly.
//
// HumanTaskStore is safe for concurrent use: it carries no mutable state.
type HumanTaskStore struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
	// auditDrops counts audit columns dropped by a degraded list query, so the
	// condition is alertable instead of living only in a WARN log line.
	auditDrops metric.Int64Counter
}

// humanTaskStoreConfig holds the optional configuration for [HumanTaskStore].
type humanTaskStoreConfig struct {
	mp metric.MeterProvider
}

// HumanTaskStoreOption configures a [HumanTaskStore].
type HumanTaskStoreOption func(*humanTaskStoreConfig)

// WithHumanTaskMeterProvider sets the OTel meter provider backing the store's
// audit-drop counter. Default: the OTel global meter provider. A nil value is
// ignored.
//
// The store takes only a meter provider — no logger or tracer — because it emits
// a single counter and has no spans of its own; the same rule TaskService
// follows. Pass the provider the rest of your stack uses so
// wrkflw_human_task_audit_drops_total lands in one metric stream.
func WithHumanTaskMeterProvider(mp metric.MeterProvider) HumanTaskStoreOption {
	return func(c *humanTaskStoreConfig) {
		if mp != nil {
			c.mp = mp
		}
	}
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
func NewHumanTaskStore(conn any, d dialect.Dialect, opts ...HumanTaskStoreOption) (*HumanTaskStore, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	var cfg humanTaskStoreConfig
	for _, o := range opts {
		o(&cfg)
	}
	var obsOpts []observability.Option
	if cfg.mp != nil {
		obsOpts = append(obsOpts, observability.WithMeterProvider(cfg.mp))
	}
	tel := observability.New(kernel.InstrumentationScope, obsOpts...)
	return &HumanTaskStore{
		conn:    conn,
		dialect: d,
		auditDrops: tel.Int64Counter("wrkflw_human_task_audit_drops_total",
			"Human-task audit columns dropped by a degraded list query."),
	}, nil
}

func (s *HumanTaskStore) querier() database.Querier {
	q, _ := database.From(s.conn)
	return q
}

// humanTaskColumns is the canonical column list used in SELECT and INSERT
// statements. Order must match the scan order in [scanTask].
const humanTaskColumns = `task_id, instance_id, node_id, state, claimed_by,
	claimed_at, claim_actor, completed_by, completed_at, outcome, note,
	completion_actor, eligibility, candidates, vars, created_at, due_at`

// Upsert inserts or replaces the task identified by t.TaskID.
// The upsert conflict clause is dialect-specific (via [dialect.Dialect.UpsertTask]).
//
// The claim/completion audit (ADR-0148 amendment 2) is normalized across typed
// columns: claimed_by, claimed_at, completed_by, completed_at, outcome and note
// are scalars — indexable and directly queryable — and only each actor's
// roles/attributes remainder rides in a JSON column. The timestamps are the
// presence discriminators: claimed_at is NULL exactly when the task is
// unclaimed, completed_at exactly when it is not completed.
func (s *HumanTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	eligibility, err := json.Marshal(t.Eligibility)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal eligibility: %w", t.TaskID, err)
	}
	candidates, err := json.Marshal(t.Candidates)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal candidates: %w", t.TaskID, err)
	}
	vars, err := json.Marshal(t.Vars)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal vars: %w", t.TaskID, err)
	}
	claim, err := htClaimBinds(s.dialect, t.Claim)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal claim_actor: %w", t.TaskID, err)
	}
	completion, err := htCompletionBinds(s.dialect, t.Completion)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal completion_actor: %w", t.TaskID, err)
	}

	q := s.querier()
	_, err = q.Exec(ctx, s.dialect.Rebind(
		`INSERT INTO wrkflw_human_task (`+humanTaskColumns+`)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`+s.dialect.UpsertTask()),
		t.TaskID, t.InstanceID, t.NodeID, t.State.String(), htClaimantID(t),
		claim.claimedAt, claim.claimActor,
		completion.completedBy, completion.completedAt, completion.outcome,
		completion.note, completion.completionActor,
		eligibility, candidates, vars,
		timeArg(s.dialect, t.CreatedAt), s.dueArg(t.DueAt),
	)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: %w", t.TaskID, err)
	}
	return nil
}

// Get returns the task for the given token or [humantask.ErrTaskNotFound].
//
// Get is fail-loud: unlike the list queries it does not degrade around an
// unreadable audit column. The caller named this exact task, so handing back a
// quietly incomplete audit would be worse than an error.
func (s *HumanTaskStore) Get(ctx context.Context, taskID string) (humantask.HumanTask, error) {
	q := s.querier()
	row := q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE task_id = ?`), taskID)
	t, auditErrs, err := s.scanTask(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return humantask.HumanTask{}, humantask.ErrTaskNotFound
	}
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("workflow-store: get task %s: %w", taskID, err)
	}
	if len(auditErrs) > 0 {
		return humantask.HumanTask{},
			fmt.Errorf("workflow-store: get task %s: %w", taskID, joinAuditErrors(auditErrs))
	}
	return t, nil
}

// AssignedTo returns all tasks currently claimed by actorID, sorted by TaskID.
//
// An empty actorID identifies no actor and always returns an empty result: it is
// not a wildcard. The claimed_by column is NOT NULL with an empty-string
// default, so an unclaimed row stores the empty string and a bare
// `claimed_by = ?` predicate would hand an unauthenticated or unresolved actor
// id every task nobody is holding. The guard is explicit rather than incidental
// so this store and [humantask.MemTaskStore] answer identically — both return a
// nil slice.
func (s *HumanTaskStore) AssignedTo(ctx context.Context, actorID string) ([]humantask.HumanTask, error) {
	if actorID == "" {
		return nil, nil
	}
	return s.query(ctx, "assigned-to",
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE claimed_by = ? ORDER BY task_id`,
		actorID)
}

// ClaimableBy returns all Unclaimed tasks for which the actor is eligible.
// Eligibility is granted when actor.ID is in Candidates OR actor.Roles and
// task Eligibility.Roles share at least one value. Results are sorted by
// TaskID. The SQL WHERE clause restricts to Unclaimed rows; the Go loop
// then applies the candidate/role eligibility filter.
func (s *HumanTaskStore) ClaimableBy(ctx context.Context, actor authz.Actor) ([]humantask.HumanTask, error) {
	rows, err := s.query(ctx, "claimable-by",
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE state = ? ORDER BY task_id`,
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
		// An unreadable audit column degrades that one row rather than the whole
		// query. The degrade is column-scoped: [HumanTaskStore.scanTask] rebuilds
		// each audit record from the scalar columns that did decode, so a corrupt
		// claim_actor costs the claimant's roles and attributes and nothing else —
		// not the claim itself, not the completion beside it, and never the row's
		// State. A row therefore cannot come back reporting a lifecycle state whose
		// record is nil. Anything else is a genuine scan failure and still aborts.
		t, auditErrs, err := s.scanTask(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("workflow-store: human task %s: scan: %w", op, err)
		}
		for _, auditErr := range auditErrs {
			slog.WarnContext(ctx, "workflow-store: dropping unreadable human-task audit column",
				"op", op,
				"task_id", auditErr.TaskID,
				"column", auditErr.Column,
				"error", auditErr.Err,
			)
			s.auditDrops.Add(ctx, 1, metric.WithAttributes(
				attribute.String("op", op),
				attribute.String("column", auditErr.Column),
			))
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
//
// The audit is normalized (ADR-0148 amendment 2): claimed_at and completed_at
// are the sole presence discriminators — NULL exactly when the corresponding
// lifecycle event has not happened — and claimed_by/completed_by supply the
// actor ids, never a fabricated actor. Presence is deliberately NOT keyed on
// the id columns: an empty actor id is a legitimate value, and keying on it
// would resurrect the fabricated-claim bug this design retired.
//
// scanTask returns three values: the task, the audit columns that could not be
// decoded, and a fatal error. The two error channels are separate because the
// read paths want different behaviour — a list query degrades around the audit
// columns while [HumanTaskStore.Get] fails loud — and because the returned task
// is meaningful precisely when the second value is non-empty. A non-nil third
// value means the row could not be decoded at all and the task is unusable.
func (s *HumanTaskStore) scanTask(scan func(dest ...any) error) (humantask.HumanTask, []*auditDecodeError, error) {
	var (
		t               humantask.HumanTask
		stateStr        string
		claimedBy       string
		claimActor      []byte
		completedBy     sql.NullString
		outcome         sql.NullString
		note            sql.NullString
		completionActor []byte
		eligibility     []byte
		candidates      []byte
		vars            []byte

		// Audit timestamps are captured raw and resolved in the descriptive
		// section below, so a malformed value degrades with the rest of the audit
		// instead of sinking a row whose load-bearing columns are all intact.
		// Exactly one form is populated, per [dialect.Dialect.TimestampsAsText].
		claimedAtText, completedAtText sql.NullString
		claimedAtTime, completedAtTime sql.NullTime
	)

	asText := s.dialect.TimestampsAsText()
	if asText {
		// SQLite TEXT timestamp path.
		var createdStr string
		var dueStr sql.NullString
		if err := scan(
			&t.TaskID, &t.InstanceID, &t.NodeID, &stateStr, &claimedBy,
			&claimedAtText, &claimActor, &completedBy, &completedAtText, &outcome, &note,
			&completionActor, &eligibility, &candidates, &vars,
			&createdStr, &dueStr,
		); err != nil {
			return humantask.HumanTask{}, nil, err
		}
		ct, err := parseTimeText(createdStr)
		if err != nil {
			return humantask.HumanTask{}, nil, fmt.Errorf("parse created_at: %w", err)
		}
		t.CreatedAt = ct
		if dueStr.Valid {
			dt, err := parseTimeText(dueStr.String)
			if err != nil {
				return humantask.HumanTask{}, nil, fmt.Errorf("parse due_at: %w", err)
			}
			t.DueAt = &dt
		}
	} else {
		// Native time.Time path (Postgres / MySQL).
		var createdAt time.Time
		var dueAt sql.NullTime
		if err := scan(
			&t.TaskID, &t.InstanceID, &t.NodeID, &stateStr, &claimedBy,
			&claimedAtTime, &claimActor, &completedBy, &completedAtTime, &outcome, &note,
			&completionActor, &eligibility, &candidates, &vars,
			&createdAt, &dueAt,
		); err != nil {
			return humantask.HumanTask{}, nil, err
		}
		t.CreatedAt = createdAt.UTC()
		if dueAt.Valid {
			dt := dueAt.Time.UTC()
			t.DueAt = &dt
		}
	}

	t.State = htParseTaskState(stateStr)

	// ── Load-bearing columns ──────────────────────────────────────────────────
	// Decoded FIRST, and a failure is fatal to the row. These drive routing and
	// authorization — ClaimableBy filters on Eligibility and Candidates, and the
	// Authorizer evaluates Eligibility and Vars — so a task missing them is not
	// merely less informative, it is unroutable and would silently vanish from
	// (or wrongly appear in) an actor's inbox. Serving a half-decoded row here
	// would be worse than failing.
	if len(eligibility) > 0 {
		if err := json.Unmarshal(eligibility, &t.Eligibility); err != nil {
			return humantask.HumanTask{}, nil, fmt.Errorf("unmarshal eligibility: %w", err)
		}
	}
	if len(candidates) > 0 {
		if err := json.Unmarshal(candidates, &t.Candidates); err != nil {
			return humantask.HumanTask{}, nil, fmt.Errorf("unmarshal candidates: %w", err)
		}
	}
	if len(vars) > 0 {
		if err := json.Unmarshal(vars, &t.Vars); err != nil {
			return humantask.HumanTask{}, nil, fmt.Errorf("unmarshal vars: %w", err)
		}
	}

	// ── Descriptive columns ───────────────────────────────────────────────────
	// Decoded LAST, on purpose, and reported as [auditDecodeError]s alongside a
	// task that is complete in every load-bearing respect. Nothing routes on the
	// claim/completion audit, so a list query can degrade it and still serve a
	// fully actionable row (see query); a point read still fails loudly.
	//
	// The ordering is the contract: any new column added here must be classified
	// deliberately. Put it above this line if anything filters, authorizes, or
	// routes on it; below only if losing it costs nothing but display.
	//
	// DEGRADATION RULE — a column that will not decode costs exactly what that one
	// column carried, never a neighbouring record and never the task's State:
	//
	//   - claim_actor / completion_actor hold only the actor's roles and
	//     attributes; the id lives in its own scalar column, so the record is
	//     rebuilt around an id-only actor. That id is also what AssignedTo matched
	//     on, so rebuilding keeps the row truthful rather than guessing.
	//   - claimed_at / completed_at are the presence discriminators, and non-NULL
	//     is the discriminator — not parseability. A garbled instant still proves
	//     the lifecycle event happened, so the record is still built, with a zero
	//     At marking the instant as unknown. Such a record cannot be written back:
	//     [htClaimBinds] rejects a zero timestamp, so a read-modify-write of the
	//     corrupt row fails loudly instead of replacing the lost instant with a
	//     fabricated one.
	//
	// The point is the invariant [humantask.HumanTask] documents: Claim is nil
	// exactly when the task was never claimed. Blanking a record while State still
	// says Claimed/Completed would hand a consumer a task that reads as unclaimed —
	// an inbox would offer a Claim action that cannot succeed, and any
	// task.Claim.Actor.ID would nil-deref. A degraded row must stay self-consistent.
	var auditErrs []*auditDecodeError

	claimedAt, claimed, err := htAuditTime(asText, claimedAtText, claimedAtTime)
	if err != nil {
		auditErrs = append(auditErrs, &auditDecodeError{TaskID: t.TaskID, Column: "claimed_at", Err: err})
	}
	if claimed {
		actor, err := htDecodeActor(claimedBy, claimActor)
		if err != nil {
			auditErrs = append(auditErrs, &auditDecodeError{TaskID: t.TaskID, Column: "claim_actor", Err: err})
			actor = authz.Actor{ID: claimedBy}
		}
		t.Claim = &humantask.Claim{Actor: actor, At: claimedAt}
	}

	completedAt, completed, err := htAuditTime(asText, completedAtText, completedAtTime)
	if err != nil {
		auditErrs = append(auditErrs, &auditDecodeError{TaskID: t.TaskID, Column: "completed_at", Err: err})
	}
	if completed {
		actor, err := htDecodeActor(completedBy.String, completionActor)
		if err != nil {
			auditErrs = append(auditErrs, &auditDecodeError{TaskID: t.TaskID, Column: "completion_actor", Err: err})
			actor = authz.Actor{ID: completedBy.String}
		}
		t.Completion = &humantask.Completion{
			Actor:   actor,
			At:      completedAt,
			Outcome: outcome.String,
			Note:    note.String,
		}
	}

	return t, auditErrs, nil
}

// htClaimantID returns the ID of the task's current claimant, or the empty
// string when the task is unclaimed. It feeds the indexed claimed_by column that
// backs the AssignedTo lookup. It is NOT a presence discriminator: an unclaimed
// task and a claim by an actor with an empty id are indistinguishable here, so
// the read path keys presence on claimed_at instead.
func htClaimantID(t humantask.HumanTask) string {
	if t.Claim == nil {
		return ""
	}
	return t.Claim.Actor.ID
}

// htClaimBind holds the bind values of the claim columns that are not already
// covered by [htClaimantID]. Both are nil for an unclaimed task, which writes
// SQL NULL; claimedAt is the presence discriminator the read path keys on.
type htClaimBind struct {
	claimedAt  any
	claimActor any
}

// htClaimBinds projects an optional [humantask.Claim] onto its normalized
// columns. The claimant id is a plain scalar handled by [htClaimantID]; only the
// roles/attributes remainder needs encoding, so an encoding failure can only
// come from a caller-supplied attribute value.
func htClaimBinds(d dialect.Dialect, c *humantask.Claim) (htClaimBind, error) {
	if c == nil {
		return htClaimBind{}, nil
	}
	if c.At.IsZero() {
		return htClaimBind{}, errZeroAuditTime("claim")
	}
	actor, err := htMarshalActorRemainder(c.Actor)
	if err != nil {
		return htClaimBind{}, err
	}
	return htClaimBind{claimedAt: timeArg(d, c.At), claimActor: actor}, nil
}

// htCompletionBind holds the bind values of the completion columns. All are nil
// for a task that has not been completed, which writes SQL NULL; completedAt is
// the presence discriminator the read path keys on.
type htCompletionBind struct {
	completedBy     any
	completedAt     any
	outcome         any
	note            any
	completionActor any
}

// htCompletionBinds projects an optional [humantask.Completion] onto its
// normalized columns. Outcome and note are bound verbatim — an empty outcome on
// a completed task is a distinct, meaningful state from a task that was never
// completed, and only the latter writes NULL.
func htCompletionBinds(d dialect.Dialect, c *humantask.Completion) (htCompletionBind, error) {
	if c == nil {
		return htCompletionBind{}, nil
	}
	if c.At.IsZero() {
		return htCompletionBind{}, errZeroAuditTime("completion")
	}
	actor, err := htMarshalActorRemainder(c.Actor)
	if err != nil {
		return htCompletionBind{}, err
	}
	return htCompletionBind{
		completedBy:     c.Actor.ID,
		completedAt:     timeArg(d, c.At),
		outcome:         c.Outcome,
		note:            c.Note,
		completionActor: actor,
	}, nil
}

// htActorRemainder is the JSON shape of the claim_actor / completion_actor
// columns: everything an [authz.Actor] carries except its ID, which lives in its
// own scalar column (claimed_by / completed_by) so it stays indexable and
// queryable. Losing this remainder costs display detail and nothing else, which
// is what makes the audit degradable rather than load-bearing.
type htActorRemainder struct {
	Roles      []string       `json:"roles,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// htMarshalActorRemainder encodes the non-id part of an actor for its JSON
// column. An actor with neither roles nor attributes encodes to "{}", which
// decodes back to the same zero-valued remainder.
func htMarshalActorRemainder(a authz.Actor) ([]byte, error) {
	return json.Marshal(htActorRemainder{Roles: a.Roles, Attributes: a.Attributes})
}

// htDecodeActor rebuilds an actor from its scalar id column and its JSON
// remainder. An empty remainder yields an actor carrying only the id — that is a
// faithful reconstruction of an actor that had no roles or attributes, not a
// fabrication.
func htDecodeActor(id string, remainder []byte) (authz.Actor, error) {
	actor := authz.Actor{ID: id}
	if len(remainder) == 0 {
		return actor, nil
	}
	var rem htActorRemainder
	if err := json.Unmarshal(remainder, &rem); err != nil {
		return authz.Actor{}, err
	}
	actor.Roles, actor.Attributes = rem.Roles, rem.Attributes
	return actor, nil
}

// htAuditTime resolves a scanned nullable audit timestamp to a UTC instant and
// reports whether the lifecycle event it discriminates happened at all. asText
// selects which of the two scan targets the dialect populated: SQLite stores the
// instant as fixed-width RFC3339 TEXT as written by [timeArg] — never
// time.RFC3339Nano, whose trimmed fraction breaks lexicographic ordering
// (ADR-0151) — while Postgres and MySQL bind time.Time natively (ADR-0080).
// [parseTimeText] deliberately stays tolerant of any fraction width, so rows
// written before ADR-0151 still read back here.
//
// Presence is reported from the column being non-NULL, INDEPENDENTLY of whether
// its value parses: a garbled instant still proves the lifecycle event happened,
// and reporting it as "never happened" is exactly what would let a claimed task
// come back with a nil Claim. An unparseable value therefore returns
// (zero, true, err) so the caller degrades the instant, not the record.
func htAuditTime(asText bool, text sql.NullString, native sql.NullTime) (time.Time, bool, error) {
	if asText {
		if !text.Valid {
			return time.Time{}, false, nil
		}
		parsed, err := parseTimeText(text.String)
		if err != nil {
			return time.Time{}, true, err
		}
		return parsed, true, nil
	}
	if !native.Valid {
		return time.Time{}, false, nil
	}
	return native.Time.UTC(), true, nil
}

// errZeroAuditTime rejects an audit record whose timestamp is the zero time.
//
// Presence is keyed on the timestamp column (ADR-0148 amendment 2), so a record
// without one is incoherent — it would persist as "claimed, at no time". It is
// also unstorable: MySQL DATETIME's range starts at 1000-01-01, so a zero
// time.Time surfaces as an opaque out-of-range driver error. [humantask.TaskStore]
// is a public port, so a caller assembling a Claim by hand gets this instead.
//
// The engine never produces one — every audit record is stamped from
// Trigger.OccurredAt — so this guards consumer and test code, not the engine.
func errZeroAuditTime(record string) error {
	return fmt.Errorf("workflow-store: %s has a zero timestamp: presence is keyed on the timestamp, so it must be set", record)
}

// auditDecodeError reports that a task's claim or completion column could not be
// decoded. It carries the task and column so a caller can log precisely which row
// is corrupt, and it wraps the underlying JSON error for errors.Is/As.
//
// The distinction exists because the two read paths want different behaviour: a
// caller that named one task must be told its audit is unreadable, whereas an
// inbox query scanning many rows must not deny every actor their whole task list
// because one row is corrupt.
type auditDecodeError struct {
	TaskID string
	Column string
	Err    error
}

func (e *auditDecodeError) Error() string {
	return fmt.Sprintf("unmarshal %s for task %s: %v", e.Column, e.TaskID, e.Err)
}

func (e *auditDecodeError) Unwrap() error { return e.Err }

// joinAuditErrors folds every audit column that failed into one error, so a
// fail-loud read reports all of them rather than only the first. A row can carry
// two independent audit records, and a caller told about one corrupt column
// would otherwise fix it and immediately hit the next.
func joinAuditErrors(errs []*auditDecodeError) error {
	joined := make([]error, len(errs))
	for i, e := range errs {
		joined[i] = e
	}
	return errors.Join(joined...)
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

// htCandidateContains reports whether id identifies one of the candidate actors.
func htCandidateContains(candidates []authz.Actor, id string) bool {
	for _, c := range candidates {
		if c.ID == id {
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
