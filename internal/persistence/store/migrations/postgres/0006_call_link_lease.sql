-- +goose Up

ALTER TABLE wrkflw_call_links ADD COLUMN claimed_at TIMESTAMPTZ;
ALTER TABLE wrkflw_call_links ADD COLUMN claimed_by TEXT;

-- +goose Down

ALTER TABLE wrkflw_call_links DROP COLUMN claimed_by;
ALTER TABLE wrkflw_call_links DROP COLUMN claimed_at;
