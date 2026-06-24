-- +goose Up

-- Carry the source instance's definition ref ("defID:version") on each outbox
-- row so the relay can republish it as event metadata for chaining's
-- PredecessorDef (ADR-0047). Defaulted so pre-existing rows are valid.
ALTER TABLE wrkflw_outbox ADD COLUMN def TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE wrkflw_outbox DROP COLUMN def;
