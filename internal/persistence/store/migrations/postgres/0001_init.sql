-- +goose Up

-- Consolidated PostgreSQL schema (ADR-0132). This single migration folds the
-- former incremental set (0001 init … 0011 timers_trigger) into final-state
-- CREATE statements: columns added by later ALTERs are declared inline, indexes
-- reflect their final definition, and the wrkflw_timers.fire_at → next_run
-- rename is applied directly. The logical schema is identical to applying the
-- old chain in order (guarded by TestMigrationParity_LogicalSchemaConverges).

CREATE TABLE wrkflw_instances (
    instance_id  TEXT PRIMARY KEY,
    def_id       TEXT        NOT NULL,
    def_version  INT         NOT NULL,
    status       SMALLINT    NOT NULL,
    snapshot     JSONB       NOT NULL,
    version      BIGINT      NOT NULL,
    started_at   TIMESTAMPTZ NOT NULL,
    ended_at     TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX wrkflw_instances_status_idx ON wrkflw_instances (status) WHERE ended_at IS NULL;
-- Composite index for the keyset cursor on the instance-enumeration query
-- (ORDER BY started_at DESC, instance_id DESC + the keyset predicate).
CREATE INDEX wrkflw_instances_list_idx ON wrkflw_instances (started_at DESC, instance_id DESC);

CREATE TABLE wrkflw_journal (
    instance_id TEXT        NOT NULL REFERENCES wrkflw_instances(instance_id),
    seq         BIGINT      NOT NULL,
    kind        TEXT        NOT NULL,
    trigger     JSONB       NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (instance_id, seq)
);

-- Outbox: status/retry_count/next_attempt_at/last_error (resilience, ADR-0017)
-- and definition_ref (chaining metadata, ADR-0047) are folded in as final
-- columns. The original wrkflw_outbox_unpublished_idx was superseded by the
-- claim/dead indexes below and is intentionally absent.
CREATE TABLE wrkflw_outbox (
    id              BIGSERIAL   PRIMARY KEY,
    instance_id     TEXT        NOT NULL,
    topic           TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    dedup_key       TEXT        NOT NULL UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL,
    published_at    TIMESTAMPTZ,
    status          TEXT        NOT NULL DEFAULT 'pending',
    retry_count     INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT,
    definition_ref  TEXT        NOT NULL DEFAULT ''
);
-- Claim index over due pending rows; dead-letter index over quarantined rows.
CREATE INDEX wrkflw_outbox_claim_idx ON wrkflw_outbox (next_attempt_at) WHERE status = 'pending';
CREATE INDEX wrkflw_outbox_dead_idx  ON wrkflw_outbox (id) WHERE status = 'dead';

CREATE TABLE wrkflw_definitions (
    def_id      TEXT        NOT NULL,
    version     INT         NOT NULL,
    definition  JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (def_id, version)
);

-- Consumer dedup table (idempotency, ADR-0018).
CREATE TABLE wrkflw_processed_message (
    subscriber   TEXT        NOT NULL,
    message_id   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber, message_id)
);

-- Call-link correlation table for async call activity (ADR-0024/0025). The
-- claimed_at/claimed_by lease columns are folded in as final columns.
CREATE TABLE wrkflw_call_links (
    child_instance_id   TEXT PRIMARY KEY,
    parent_instance_id  TEXT        NOT NULL,
    parent_command_id   TEXT        NOT NULL,
    parent_def_id       TEXT        NOT NULL,
    parent_def_version  INT         NOT NULL,
    depth               INT         NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'running',
    output              JSONB,
    error               TEXT,
    created_at          TIMESTAMPTZ NOT NULL,
    notified_at         TIMESTAMPTZ,
    claimed_at          TIMESTAMPTZ,
    claimed_by          TEXT
);
-- The notifier's claim set: terminal but not yet delivered to the parent.
CREATE INDEX wrkflw_call_links_pending_idx ON wrkflw_call_links (child_instance_id)
    WHERE status IN ('completed','failed');
-- Parent-scoped lookup of still-running children.
CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id) WHERE status = 'running';

-- Armed-timer table for timer rehydration on restart (ADR-0027). next_run holds
-- the authoritative next-run instant (formerly fire_at); trigger_kind/
-- trigger_payload carry the durable TriggerSpec descriptor.
CREATE TABLE wrkflw_timers (
    instance_id     TEXT        NOT NULL,
    timer_id        TEXT        NOT NULL,
    next_run        TIMESTAMPTZ NOT NULL,
    kind            SMALLINT    NOT NULL,
    def_id          TEXT        NOT NULL,
    def_version     INT         NOT NULL,
    trigger_kind    SMALLINT    NOT NULL DEFAULT 0,
    trigger_payload JSONB,
    PRIMARY KEY (instance_id, timer_id)
);
CREATE INDEX wrkflw_timers_next_run_idx ON wrkflw_timers (next_run);

-- Process-instance chaining lineage (ADR-0045). One row per predecessor->
-- successor hop; the (predecessor_instance_id, outcome) PK is the exactly-once
-- backstop.
CREATE TABLE wrkflw_chain_links (
    predecessor_instance_id    TEXT        NOT NULL,
    outcome                    TEXT        NOT NULL,
    successor_instance_id      TEXT        NOT NULL,
    predecessor_definition_ref TEXT        NOT NULL DEFAULT '',
    successor_definition_ref   TEXT        NOT NULL DEFAULT '',
    start_vars                 JSONB,
    created_at                 TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (predecessor_instance_id, outcome)
);
CREATE INDEX wrkflw_chain_links_successor_idx ON wrkflw_chain_links (successor_instance_id);

-- Human task state table (ADR-0098). task_token is the primary key;
-- eligibility/candidates/vars stored as JSONB for predicate evaluation.
CREATE TABLE wrkflw_human_task (
    task_token  TEXT        NOT NULL,
    instance_id TEXT        NOT NULL,
    node_id     TEXT        NOT NULL,
    state       TEXT        NOT NULL,
    claimed_by  TEXT        NOT NULL DEFAULT '',
    eligibility JSONB       NOT NULL,
    candidates  JSONB       NOT NULL,
    vars        JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    due_at      TIMESTAMPTZ,
    PRIMARY KEY (task_token)
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
