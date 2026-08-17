-- +goose Up
CREATE TABLE firms(
  id UUID PRIMARY KEY,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  email TEXT NOT NULL,
  ustId TEXT NOT NULL,
  streetName TEXT NOT NULL,
  plz TEXT NOT NULL
);
-- +goose Down
DROP TABLE firms;
