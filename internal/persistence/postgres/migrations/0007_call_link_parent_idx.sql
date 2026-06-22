-- +goose Up

CREATE INDEX wrkflw_call_links_parent_running_idx ON wrkflw_call_links (parent_instance_id) WHERE status = 'running';

-- +goose Down

DROP INDEX wrkflw_call_links_parent_running_idx;
