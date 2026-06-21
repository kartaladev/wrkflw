-- +goose Up

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

CREATE TABLE wrkflw_journal (
    instance_id TEXT        NOT NULL REFERENCES wrkflw_instances(instance_id),
    seq         BIGINT      NOT NULL,
    kind        TEXT        NOT NULL,
    trigger     JSONB       NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (instance_id, seq)
);

CREATE TABLE wrkflw_outbox (
    id           BIGSERIAL PRIMARY KEY,
    instance_id  TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    dedup_key    TEXT        NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
CREATE INDEX wrkflw_outbox_unpublished_idx ON wrkflw_outbox (id) WHERE published_at IS NULL;

CREATE TABLE wrkflw_definitions (
    def_id      TEXT        NOT NULL,
    version     INT         NOT NULL,
    definition  JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (def_id, version)
);

-- +goose Down

DROP TABLE wrkflw_definitions;
DROP TABLE wrkflw_outbox;
DROP TABLE wrkflw_journal;
DROP TABLE wrkflw_instances;
