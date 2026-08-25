-- +goose Up
ALTER TABLE firms
  ADD COLUMN hashed_password TEXT NOT NULL
  DEFAULT 'unset';
ALTER TABLE users
  ADD COLUMN hashed_password TEXT NOT NULL
  DEFAULT 'unset';

-- goose Down
ALTER TABLE firms
   DROP COLUMN hashed_password;
ALTER TABLE users
   DROP COLUMN hashed_password;


