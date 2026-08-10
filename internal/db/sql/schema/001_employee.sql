-- +goose Up
CREATE TABLE users (
  id UUID PRIMARY KEY,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  name TEXT NOT NULL,
  is_admin BOOLEAN NOT NULL,
  is_teamhead BOOLEAN NOT NULL
);
-- +goose Down
DROP TABLE users;
