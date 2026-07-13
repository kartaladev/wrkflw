-- +goose Up

-- Consolidated MySQL 8.0+ schema (ADR-0132). Folds the former incremental set
-- (0001 init … 0004 timers_trigger) into final-state CREATE statements: columns
-- added by later ALTERs are declared inline and the wrkflw_timers.fire_at →
-- next_run rename is applied directly. The logical schema converges with
-- Postgres/SQLite (guarded by TestMigrationParity_LogicalSchemaConverges).
--
-- MySQL notes: no partial indexes (full indexes used instead), and the journal
-- payload column is named "trigger_" because "trigger" is a reserved word
-- (surfaced via dialect.NewMySQL().JournalTriggerColumn()).

CREATE TABLE wrkflw_instances (
    instance_id  VARCHAR(255) PRIMARY KEY,
    def_id       VARCHAR(255) NOT NULL,
    def_version  INT          NOT NULL,
    status       SMALLINT     NOT NULL,
    snapshot     JSON         NOT NULL,
    version      BIGINT       NOT NULL,
    started_at   DATETIME(6)  NOT NULL,
    ended_at     DATETIME(6),
    updated_at   DATETIME(6)  NOT NULL
);
CREATE INDEX wrkflw_instances_status_idx ON wrkflw_instances (status);
CREATE INDEX wrkflw_instances_list_idx   ON wrkflw_instances (started_at DESC, instance_id DESC);

CREATE TABLE wrkflw_journal (
    instance_id VARCHAR(255) NOT NULL,
    seq         BIGINT       NOT NULL,
    kind        VARCHAR(255) NOT NULL,
    trigger_    JSON         NOT NULL,
    occurred_at DATETIME(6)  NOT NULL,
    applied_at  DATETIME(6)  NOT NULL,
    PRIMARY KEY (instance_id, seq),
    CONSTRAINT fk_journal_instance FOREIGN KEY (instance_id) REFERENCES wrkflw_instances(instance_id)
);

CREATE TABLE wrkflw_outbox (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    instance_id     VARCHAR(255) NOT NULL,
    topic           VARCHAR(255) NOT NULL,
    payload         JSON         NOT NULL,
    dedup_key       VARCHAR(255) NOT NULL UNIQUE,
    created_at      DATETIME(6)  NOT NULL,
    published_at    DATETIME(6),
    status          VARCHAR(50)  NOT NULL DEFAULT 'pending',
    retry_count     INT          NOT NULL DEFAULT 0,
    next_attempt_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_error      TEXT,
    definition_ref  VARCHAR(255) NOT NULL DEFAULT ''
);
CREATE INDEX wrkflw_outbox_claim_idx ON wrkflw_outbox (next_attempt_at);
CREATE INDEX wrkflw_outbox_dead_idx  ON wrkflw_outbox (status, id);

CREATE TABLE wrkflw_definitions (
    def_id      VARCHAR(255) NOT NULL,
    version     INT          NOT NULL,
    definition  JSON         NOT NULL,
    created_at  DATETIME(6)  NOT NULL,
    PRIMARY KEY (def_id, version)
);

CREATE TABLE wrkflw_processed_message (
    subscriber   VARCHAR(255) NOT NULL,
    message_id   VARCHAR(255) NOT NULL,
    processed_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (subscriber, message_id)
);

-- Call-links: claimed_at/claimed_by lease columns folded in. MySQL has no
-- partial indexes, so pending_idx is a composite covering index on the claim
-- filter columns rather than a partial index.
CREATE TABLE wrkflw_call_links (
    child_instance_id   VARCHAR(255) PRIMARY KEY,
    parent_instance_id  VARCHAR(255) NOT NULL,
    parent_command_id   VARCHAR(255) NOT NULL,
    parent_def_id       VARCHAR(255) NOT NULL,
    parent_def_version  INT          NOT NULL,
    depth               INT          NOT NULL,
    status              VARCHAR(50)  NOT NULL DEFAULT 'running',
    output              JSON,
    error               TEXT,
    created_at          DATETIME(6)  NOT NULL,
    notified_at         DATETIME(6),
    claimed_at          DATETIME(6),
    claimed_by          VARCHAR(255)
);
CREATE INDEX wrkflw_call_links_pending_idx        ON wrkflw_call_links (status, notified_at, claimed_at, child_instance_id);
CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id);

-- Timers: next_run (formerly fire_at) + trigger_kind/trigger_payload folded in.
CREATE TABLE wrkflw_timers (
    instance_id     VARCHAR(255) NOT NULL,
    timer_id        VARCHAR(255) NOT NULL,
    next_run        DATETIME(6)  NOT NULL,
    kind            SMALLINT     NOT NULL,
    def_id          VARCHAR(255) NOT NULL,
    def_version     INT          NOT NULL,
    trigger_kind    SMALLINT     NOT NULL DEFAULT 0,
    trigger_payload JSON         NULL,
    PRIMARY KEY (instance_id, timer_id)
);
CREATE INDEX wrkflw_timers_next_run_idx ON wrkflw_timers (next_run);

CREATE TABLE wrkflw_chain_links (
    predecessor_instance_id    VARCHAR(255) NOT NULL,
    outcome                    VARCHAR(255) NOT NULL,
    successor_instance_id      VARCHAR(255) NOT NULL,
    predecessor_definition_ref VARCHAR(255) NOT NULL DEFAULT '',
    successor_definition_ref   VARCHAR(255) NOT NULL DEFAULT '',
    start_vars                 JSON,
    created_at                 DATETIME(6)  NOT NULL,
    PRIMARY KEY (predecessor_instance_id, outcome)
);
CREATE INDEX wrkflw_chain_links_successor_idx ON wrkflw_chain_links (successor_instance_id);

-- Human task state table (ADR-0098). Indexes declared inline (MySQL style).
CREATE TABLE wrkflw_human_task (
    task_token  VARCHAR(255) NOT NULL,
    instance_id VARCHAR(255) NOT NULL,
    node_id     VARCHAR(255) NOT NULL,
    state       VARCHAR(64)  NOT NULL,
    claimed_by  VARCHAR(255) NOT NULL DEFAULT '',
    eligibility JSON         NOT NULL,
    candidates  JSON         NOT NULL,
    vars        JSON         NOT NULL,
    created_at  DATETIME(6)  NOT NULL,
    due_at      DATETIME(6)  NULL,
    PRIMARY KEY (task_token),
    INDEX idx_wrkflw_human_task_instance   (instance_id),
    INDEX idx_wrkflw_human_task_state      (state),
    INDEX idx_wrkflw_human_task_claimed_by (claimed_by)
);

-- +goose Down

DROP TABLE IF EXISTS wrkflw_human_task;
DROP TABLE IF EXISTS wrkflw_chain_links;
DROP TABLE IF EXISTS wrkflw_timers;
DROP TABLE IF EXISTS wrkflw_call_links;
DROP TABLE IF EXISTS wrkflw_processed_message;
DROP TABLE IF EXISTS wrkflw_definitions;
DROP TABLE IF EXISTS wrkflw_journal;
DROP TABLE IF EXISTS wrkflw_instances;
