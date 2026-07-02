-- +goose Up

-- SQLite consolidated schema (all PG migrations 0001–0009 applied in one file).
-- Type mapping:
--   TEXT/VARCHAR      → TEXT
--   INT/SMALLINT/BIGINT → INTEGER
--   JSONB/JSON        → TEXT
--   TIMESTAMPTZ/DATETIME(6) → TEXT  (stores RFC3339 UTC strings)
--   BIGSERIAL PRIMARY KEY   → INTEGER PRIMARY KEY (SQLite rowid autoincrement)
--   BYTEA             → BLOB
--
-- Timestamps are stored as RFC3339 UTC strings (e.g. "2006-01-02T15:04:05Z").
-- The OutboxStatsQuery in dialect/sqlite.go uses julianday(created_at), which
-- requires ISO8601-compatible strings — the store layer (Task 8) MUST write UTC.

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

-- Full index (SQLite has no partial indexes); covers active-instance queries.
CREATE INDEX wrkflw_instances_status_idx ON wrkflw_instances (status);

-- Composite cursor index for keyset pagination (started_at DESC, instance_id DESC).
CREATE INDEX wrkflw_instances_list_idx ON wrkflw_instances (started_at DESC, instance_id DESC);

-- Journal: "trigger" is NOT a reserved keyword in SQLite (unlike MySQL),
-- so the column is named "trigger" to match Postgres and the dialect's
-- JournalTriggerColumn() return value.
CREATE TABLE wrkflw_journal (
    instance_id TEXT    NOT NULL REFERENCES wrkflw_instances(instance_id),
    seq         INTEGER NOT NULL,
    kind        TEXT    NOT NULL,
    trigger     TEXT    NOT NULL,
    occurred_at TEXT    NOT NULL,
    applied_at  TEXT    NOT NULL,
    PRIMARY KEY (instance_id, seq)
);

-- Outbox: id is autoincrement via INTEGER PRIMARY KEY (SQLite rowid alias).
-- status column stores 'pending' or 'dead' — required by OutboxStatsQuery and
-- the relay claim site. julianday(created_at) in OutboxStatsQuery requires
-- ISO8601 UTC values written by the store layer.
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

-- Plain indexes (SQLite does not support partial WHERE indexes).
CREATE INDEX wrkflw_outbox_claim_idx ON wrkflw_outbox (next_attempt_at);
CREATE INDEX wrkflw_outbox_dead_idx  ON wrkflw_outbox (status, id);

-- Definitions: (def_id, version) PK — the UpsertDefinition ON CONFLICT target.
CREATE TABLE wrkflw_definitions (
    def_id      TEXT    NOT NULL,
    version     INTEGER NOT NULL,
    definition  TEXT    NOT NULL,
    created_at  TEXT    NOT NULL,
    PRIMARY KEY (def_id, version)
);

-- Processed-message dedup: (subscriber, message_id) PK is the ON CONFLICT DO
-- NOTHING target used by InsertIgnoreDedup.
CREATE TABLE wrkflw_processed_message (
    subscriber   TEXT NOT NULL,
    message_id   TEXT NOT NULL,
    processed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (subscriber, message_id)
);

-- Call-links: child_instance_id PK; claimed_at/claimed_by support leased-claim.
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

-- Plain index covering the claim filter (status, notified_at, claimed_at) and
-- the parent-running lookup. SQLite does not support partial WHERE indexes.
CREATE INDEX wrkflw_call_links_pending_idx        ON wrkflw_call_links (status, notified_at, claimed_at, child_instance_id);
CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id);

-- Timers: (instance_id, timer_id) PK — the UpsertTimer ON CONFLICT target.
CREATE TABLE wrkflw_timers (
    instance_id TEXT    NOT NULL,
    timer_id    TEXT    NOT NULL,
    fire_at     TEXT    NOT NULL,
    kind        INTEGER NOT NULL,
    def_id      TEXT    NOT NULL,
    def_version INTEGER NOT NULL,
    PRIMARY KEY (instance_id, timer_id)
);

CREATE INDEX wrkflw_timers_fire_at_idx ON wrkflw_timers (fire_at);

-- Chain-links: (predecessor_instance_id, outcome) PK — exactly-once backstop
-- for the successor per terminal outcome.
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

-- Ancestry lookup: find which predecessor produced a given successor.
CREATE INDEX wrkflw_chain_links_successor_idx ON wrkflw_chain_links (successor_instance_id);

-- +goose Down

DROP INDEX IF EXISTS wrkflw_chain_links_successor_idx;
DROP TABLE IF EXISTS wrkflw_chain_links;
DROP INDEX IF EXISTS wrkflw_timers_fire_at_idx;
DROP TABLE IF EXISTS wrkflw_timers;
DROP INDEX IF EXISTS wrkflw_call_links_parent_running_idx;
DROP INDEX IF EXISTS wrkflw_call_links_pending_idx;
DROP TABLE IF EXISTS wrkflw_call_links;
DROP TABLE IF EXISTS wrkflw_processed_message;
DROP TABLE IF EXISTS wrkflw_definitions;
DROP INDEX IF EXISTS wrkflw_outbox_dead_idx;
DROP INDEX IF EXISTS wrkflw_outbox_claim_idx;
DROP TABLE IF EXISTS wrkflw_outbox;
DROP TABLE IF EXISTS wrkflw_journal;
DROP INDEX IF EXISTS wrkflw_instances_list_idx;
DROP INDEX IF EXISTS wrkflw_instances_status_idx;
DROP TABLE IF EXISTS wrkflw_instances;
