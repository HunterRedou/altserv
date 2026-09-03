-- +goose Up
ALTER TABLE firms ADD COLUMN status TEXT NOT NULL DEFAULT 'pending'
   CHECK (status IN ('pending', 'valid', 'invalid'));

-- +goose Down
ALTER TABLE firms DROP COLUMN status;
