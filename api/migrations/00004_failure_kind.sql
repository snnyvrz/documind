-- +goose Up
ALTER TABLE documents ADD COLUMN IF NOT EXISTS failure_kind text NOT NULL DEFAULT 'retryable';

-- +goose Down
ALTER TABLE documents DROP COLUMN IF EXISTS failure_kind;
