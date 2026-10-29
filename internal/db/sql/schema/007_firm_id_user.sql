-- +goose Up
ALTER TABLE users ADD COLUMN firm_id UUID NOT NULL REFERENCES firms(id) ON DELETE CASCADE;
CREATE INDEX users_firm_id_idx ON users(firm_id);

-- +goose Down
ALTER TABLE users DROP COLUMN firm_id;
