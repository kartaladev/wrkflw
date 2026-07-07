-- +goose Up

-- Durable timer descriptor (Plan 3 Tasks 1-2): persist the TriggerSpec and a
-- truthful next-run instant so a SQL-backed one-shot timer re-arms at its
-- original absolute fire time after a restart (closes the Plan-2 rehydration
-- regression). fire_at is renamed next_run to reflect that the column now holds
-- the authoritative next-run instant, not a fixed absolute deadline.
ALTER TABLE wrkflw_timers RENAME COLUMN fire_at TO next_run;
ALTER INDEX wrkflw_timers_fire_at_idx RENAME TO wrkflw_timers_next_run_idx;

ALTER TABLE wrkflw_timers ADD COLUMN trigger_kind    SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_timers ADD COLUMN trigger_payload JSONB;

-- +goose Down

ALTER TABLE wrkflw_timers DROP COLUMN trigger_payload;
ALTER TABLE wrkflw_timers DROP COLUMN trigger_kind;
ALTER INDEX wrkflw_timers_next_run_idx RENAME TO wrkflw_timers_fire_at_idx;
ALTER TABLE wrkflw_timers RENAME COLUMN next_run TO fire_at;
