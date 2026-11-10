-- +goose Up
-- The API must never connect as a superuser or a BYPASSRLS role. This role is
-- NOLOGIN on purpose: the deployment grants LOGIN/password outside migrations.
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'altserv_app') THEN
    CREATE ROLE altserv_app NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
  END IF;
END
$$;
-- +goose StatementEnd

-- The migration/connection role needs to be able to SET LOCAL ROLE altserv_app.
GRANT altserv_app TO CURRENT_USER;

GRANT USAGE ON SCHEMA public TO altserv_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenants, users, sessions TO altserv_app;

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;

-- Tenant rows are visible only for the tenant in the transaction-local context.
CREATE POLICY tenants_isolation ON tenants
  USING (id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- Public tenant registration must insert a new tenant before a session exists.
CREATE POLICY tenants_public_insert ON tenants
  FOR INSERT WITH CHECK (true);

CREATE POLICY users_isolation ON users
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY sessions_isolation ON sessions
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP POLICY IF EXISTS sessions_isolation ON sessions;
DROP POLICY IF EXISTS users_isolation ON users;
DROP POLICY IF EXISTS tenants_public_insert ON tenants;
DROP POLICY IF EXISTS tenants_isolation ON tenants;

ALTER TABLE sessions DISABLE ROW LEVEL SECURITY;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;
ALTER TABLE tenants DISABLE ROW LEVEL SECURITY;

REVOKE ALL ON tenants, users, sessions FROM altserv_app;
REVOKE USAGE ON SCHEMA public FROM altserv_app;

-- The role is intentionally not dropped: it may be shared by other databases.
