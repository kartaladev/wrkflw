-- +goose Up

-- Call-link correlation table for async call activity (ADR-0024, ADR-0025).
-- One row per child instance; doubles as the durable parent-notification queue.
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
    notified_at         TIMESTAMPTZ
);

-- The notifier's claim set: terminal but not yet delivered to the parent.
CREATE INDEX wrkflw_call_links_pending_idx ON wrkflw_call_links (child_instance_id)
    WHERE status IN ('completed','failed');

-- +goose Down

DROP INDEX IF EXISTS wrkflw_call_links_pending_idx;
DROP TABLE IF EXISTS wrkflw_call_links;
