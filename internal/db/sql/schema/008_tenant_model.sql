-- +goose Up
ALTER TABLE firms RENAME TO tenants;
ALTER TABLE users RENAME COLUMN firm_id TO tenant_id;
ALTER INDEX users_firm_id_idx RENAME TO users_tenant_id_idx;
ALTER TABLE users RENAME CONSTRAINT users_firm_id_fkey TO users_tenant_id_fkey;

ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'employee'
  CHECK (role IN ('platform_admin', 'chef', 'employee'));

UPDATE users SET role = 'chef' WHERE is_admin;

ALTER TABLE users DROP COLUMN is_admin;
ALTER TABLE users DROP COLUMN is_teamhead;

-- +goose Down
ALTER TABLE users ADD COLUMN is_admin BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN is_teamhead BOOLEAN NOT NULL DEFAULT false;

UPDATE users SET is_admin = (role = 'chef');

ALTER TABLE users DROP COLUMN role;

ALTER TABLE users RENAME CONSTRAINT users_tenant_id_fkey TO users_firm_id_fkey;
ALTER INDEX users_tenant_id_idx RENAME TO users_firm_id_idx;
ALTER TABLE users RENAME COLUMN tenant_id TO firm_id;
ALTER TABLE tenants RENAME TO firms;
