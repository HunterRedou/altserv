-- +goose Up
ALTER TABLE sessions ADD COLUMN tenant_id UUID;

UPDATE sessions s
SET tenant_id = u.tenant_id
FROM users u
WHERE u.id = s.user_id;

ALTER TABLE sessions ALTER COLUMN tenant_id SET NOT NULL;

-- Composite key so a session can only reference a user in the same tenant.
ALTER TABLE users ADD CONSTRAINT users_id_tenant_id_key UNIQUE (id, tenant_id);

ALTER TABLE sessions DROP CONSTRAINT sessions_user_id_fkey;
ALTER TABLE sessions ADD CONSTRAINT sessions_user_tenant_fkey
  FOREIGN KEY (user_id, tenant_id) REFERENCES users(id, tenant_id) ON DELETE CASCADE;

CREATE INDEX sessions_tenant_id_idx ON sessions(tenant_id);

-- +goose Down
DROP INDEX sessions_tenant_id_idx;

ALTER TABLE sessions DROP CONSTRAINT sessions_user_tenant_fkey;
ALTER TABLE sessions ADD CONSTRAINT sessions_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE users DROP CONSTRAINT users_id_tenant_id_key;

ALTER TABLE sessions DROP COLUMN tenant_id;
