-- +goose Up

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
CREATE INDEX wrkflw_outbox_dead_idx  ON wrkflw_outbox (id);

CREATE TABLE wrkflw_definitions (
    def_id      VARCHAR(255) NOT NULL,
    version     INT          NOT NULL,
    definition  JSON         NOT NULL,
    created_at  DATETIME(6)  NOT NULL,
    PRIMARY KEY (def_id, version)
);

-- +goose Down

DROP TABLE IF EXISTS wrkflw_definitions;
DROP TABLE IF EXISTS wrkflw_outbox;
DROP TABLE IF EXISTS wrkflw_journal;
DROP TABLE IF EXISTS wrkflw_instances;
