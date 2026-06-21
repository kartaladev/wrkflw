-- +goose Up

-- Composite index for the keyset cursor on the instance-enumeration query.
-- Supports ORDER BY started_at DESC, instance_id DESC and the keyset predicate
-- (started_at, instance_id) < ($cursorTime, $cursorID).
CREATE INDEX wrkflw_instances_list_idx
    ON wrkflw_instances (started_at DESC, instance_id DESC);

-- +goose Down

DROP INDEX wrkflw_instances_list_idx;
