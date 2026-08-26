-- +goose Up
ALTER TABLE users
ADD COLUMN email TEXT NOT NULL
DEFAULT 'unset';

-- +goose Down
ALTER TABLE users
DROP COLUMN email;
