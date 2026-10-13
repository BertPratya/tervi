-- +goose Up
ALTER TABLE machines ADD COLUMN last_seen_at timestamptz;

-- +goose Down
ALTER TABLE machines DROP COLUMN last_seen_at;
