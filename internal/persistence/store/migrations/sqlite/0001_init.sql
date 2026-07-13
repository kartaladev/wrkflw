-- +goose Up

-- Consolidated SQLite schema (ADR-0132). Single migration folding the former
-- incremental set (0001 init + 0002 human_task + 0003 timers_trigger) into
-- final-state CREATE statements. The logical schema converges with Postgres/
-- MySQL (guarded by TestMigrationParity_LogicalSchemaConverges).
--
-- Type mapping:
--   TEXT/VARCHAR            → TEXT
--   INT/SMALLINT/BIGINT     → INTEGER
--   JSONB/JSON              → TEXT
--   TIMESTAMPTZ/DATETIME(6) → TEXT  (RFC3339 UTC strings, e.g. "2006-01-02T15:04:05Z")
--   BIGSERIAL PRIMARY KEY   → INTEGER PRIMARY KEY (SQLite rowid autoincrement)
--
-- Timestamps are stored as RFC3339 UTC strings. The OutboxStatsQuery in
-- dialect/sqlite.go uses julianday(created_at), which requires ISO8601-
-- compatible strings — the store layer MUST write UTC. SQLite has no partial
-- indexes, so partial-index dialects are matched with plain indexes.

CREATE TABLE wrkflw_instances (
    instance_id  TEXT    PRIMARY KEY,
    def_id       TEXT    NOT NULL,
    def_version  INTEGER NOT NULL,
    status       INTEGER NOT NULL,
    snapshot     TEXT    NOT NULL,
    version      INTEGER NOT NULL,
    started_at   TEXT    NOT NULL,
    ended_at     TEXT,
    updated_at   TEXT    NOT NULL
);
CREATE INDEX wrkflw_instances_status_idx ON wrkflw_instances (status);
CREATE INDEX wrkflw_instances_list_idx   ON wrkflw_instances (started_at DESC, instance_id DESC);

-- "trigger" is NOT reserved in SQLite (unlike MySQL), so the column keeps the
-- canonical name matching Postgres and JournalTriggerColumn().
CREATE TABLE wrkflw_journal (
    instance_id TEXT    NOT NULL REFERENCES wrkflw_instances(instance_id),
    seq         INTEGER NOT NULL,
    kind        TEXT    NOT NULL,
    trigger     TEXT    NOT NULL,
    occurred_at TEXT    NOT NULL,
    applied_at  TEXT    NOT NULL,
    PRIMARY KEY (instance_id, seq)
);

-- Outbox: id autoincrements via INTEGER PRIMARY KEY (rowid alias).
CREATE TABLE wrkflw_outbox (
    id              INTEGER PRIMARY KEY,
    instance_id     TEXT    NOT NULL,
    topic           TEXT    NOT NULL,
    payload         TEXT    NOT NULL,
    dedup_key       TEXT    NOT NULL UNIQUE,
    created_at      TEXT    NOT NULL,
    published_at    TEXT,
    status          TEXT    NOT NULL DEFAULT 'pending',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    last_error      TEXT,
    definition_ref  TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX wrkflw_outbox_claim_idx ON wrkflw_outbox (next_attempt_at);
CREATE INDEX wrkflw_outbox_dead_idx  ON wrkflw_outbox (status, id);

CREATE TABLE wrkflw_definitions (
    def_id      TEXT    NOT NULL,
    version     INTEGER NOT NULL,
    definition  TEXT    NOT NULL,
    created_at  TEXT    NOT NULL,
    PRIMARY KEY (def_id, version)
);

CREATE TABLE wrkflw_processed_message (
    subscriber   TEXT NOT NULL,
    message_id   TEXT NOT NULL,
    processed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (subscriber, message_id)
);

-- Call-links: claimed_at/claimed_by lease columns folded in.
CREATE TABLE wrkflw_call_links (
    child_instance_id  TEXT    PRIMARY KEY,
    parent_instance_id TEXT    NOT NULL,
    parent_command_id  TEXT    NOT NULL,
    parent_def_id      TEXT    NOT NULL,
    parent_def_version INTEGER NOT NULL,
    depth              INTEGER NOT NULL,
    status             TEXT    NOT NULL DEFAULT 'running',
    output             TEXT,
    error              TEXT,
    created_at         TEXT    NOT NULL,
    notified_at        TEXT,
    claimed_at         TEXT,
    claimed_by         TEXT
);
CREATE INDEX wrkflw_call_links_pending_idx        ON wrkflw_call_links (status, notified_at, claimed_at, child_instance_id);
CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id);

-- Timers: next_run (formerly fire_at) + trigger_kind/trigger_payload folded in.
CREATE TABLE wrkflw_timers (
    instance_id     TEXT    NOT NULL,
    timer_id        TEXT    NOT NULL,
    next_run        TEXT    NOT NULL,
    kind            INTEGER NOT NULL,
    def_id          TEXT    NOT NULL,
    def_version     INTEGER NOT NULL,
    trigger_kind    INTEGER NOT NULL DEFAULT 0,
    trigger_payload TEXT,
    PRIMARY KEY (instance_id, timer_id)
);
CREATE INDEX wrkflw_timers_next_run_idx ON wrkflw_timers (next_run);

CREATE TABLE wrkflw_chain_links (
    predecessor_instance_id    TEXT NOT NULL,
    outcome                    TEXT NOT NULL,
    successor_instance_id      TEXT NOT NULL,
    predecessor_definition_ref TEXT NOT NULL DEFAULT '',
    successor_definition_ref   TEXT NOT NULL DEFAULT '',
    start_vars                 TEXT,
    created_at                 TEXT NOT NULL,
    PRIMARY KEY (predecessor_instance_id, outcome)
);
CREATE INDEX wrkflw_chain_links_successor_idx ON wrkflw_chain_links (successor_instance_id);

-- Human task state table (ADR-0098). eligibility/candidates/vars stored as TEXT
-- (JSON serialised by the store layer); due_at nullable.
CREATE TABLE wrkflw_human_task (
    task_token  TEXT NOT NULL PRIMARY KEY,
    instance_id TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    state       TEXT NOT NULL,
    claimed_by  TEXT NOT NULL DEFAULT '',
    eligibility TEXT NOT NULL,
    candidates  TEXT NOT NULL,
    vars        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    due_at      TEXT
);
CREATE INDEX idx_wrkflw_human_task_instance   ON wrkflw_human_task (instance_id);
CREATE INDEX idx_wrkflw_human_task_state      ON wrkflw_human_task (state);
CREATE INDEX idx_wrkflw_human_task_claimed_by ON wrkflw_human_task (claimed_by);

-- +goose Down

DROP TABLE IF EXISTS wrkflw_human_task;
DROP TABLE IF EXISTS wrkflw_chain_links;
DROP TABLE IF EXISTS wrkflw_timers;
DROP TABLE IF EXISTS wrkflw_call_links;
DROP TABLE IF EXISTS wrkflw_processed_message;
DROP TABLE IF EXISTS wrkflw_definitions;
DROP TABLE IF EXISTS wrkflw_outbox;
DROP TABLE IF EXISTS wrkflw_journal;
DROP TABLE IF EXISTS wrkflw_instances;
