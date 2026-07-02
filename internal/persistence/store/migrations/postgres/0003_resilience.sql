-- +goose Up

-- Resilience: outbox poison isolation / dead-letter quarantine (ADR-0017).
ALTER TABLE wrkflw_outbox ADD COLUMN status          TEXT        NOT NULL DEFAULT 'pending';
ALTER TABLE wrkflw_outbox ADD COLUMN retry_count     INT         NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_outbox ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE wrkflw_outbox ADD COLUMN last_error      TEXT;

-- Existing published rows become 'published'.
UPDATE wrkflw_outbox SET status = 'published' WHERE published_at IS NOT NULL;

-- Replace the unpublished partial index with a claim index over due pending rows.
DROP INDEX IF EXISTS wrkflw_outbox_unpublished_idx;
CREATE INDEX wrkflw_outbox_claim_idx ON wrkflw_outbox (next_attempt_at) WHERE status = 'pending';
CREATE INDEX wrkflw_outbox_dead_idx  ON wrkflw_outbox (id) WHERE status = 'dead';

-- Idempotency: consumer dedup table (ADR-0018).
CREATE TABLE wrkflw_processed_message (
    subscriber   TEXT        NOT NULL,
    message_id   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber, message_id)
);

-- +goose Down

DROP TABLE IF EXISTS wrkflw_processed_message;
DROP INDEX IF EXISTS wrkflw_outbox_dead_idx;
DROP INDEX IF EXISTS wrkflw_outbox_claim_idx;
CREATE INDEX wrkflw_outbox_unpublished_idx ON wrkflw_outbox (id) WHERE published_at IS NULL;
ALTER TABLE wrkflw_outbox DROP COLUMN last_error;
ALTER TABLE wrkflw_outbox DROP COLUMN next_attempt_at;
ALTER TABLE wrkflw_outbox DROP COLUMN retry_count;
ALTER TABLE wrkflw_outbox DROP COLUMN status;
