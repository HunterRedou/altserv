-- +goose Up
CREATE TABLE patients (
  id UUID PRIMARY KEY,
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  first_name_enc BYTEA NOT NULL,
  last_name_enc BYTEA NOT NULL,
  birth_date_enc BYTEA NOT NULL,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'inactive', 'archived')),
  key_version INT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Composite key for future tenant-safe foreign keys (assignments, reports, ...).
ALTER TABLE patients ADD CONSTRAINT patients_id_tenant_id_key UNIQUE (id, tenant_id);

CREATE INDEX patients_tenant_id_idx ON patients(tenant_id);
CREATE INDEX patients_tenant_status_idx ON patients(tenant_id, status);

CREATE TABLE audit_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id UUID,
  occurred_at TIMESTAMP NOT NULL DEFAULT NOW(),
  request_id TEXT NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX audit_events_tenant_time_idx ON audit_events(tenant_id, occurred_at DESC);
CREATE INDEX audit_events_entity_idx ON audit_events(entity_type, entity_id);

ALTER TABLE patients ENABLE ROW LEVEL SECURITY;
ALTER TABLE patients FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events FORCE ROW LEVEL SECURITY;

CREATE POLICY patients_isolation ON patients
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY audit_events_isolation ON audit_events
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- The application may append and read audit events, but never rewrite them.
GRANT SELECT, INSERT ON audit_events TO altserv_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON patients TO altserv_app;

-- +goose StatementBegin
CREATE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit_events is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_events_no_update_delete
  BEFORE UPDATE OR DELETE ON audit_events
  FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();

-- +goose Down
DROP TRIGGER IF EXISTS audit_events_no_update_delete ON audit_events;
DROP FUNCTION IF EXISTS audit_events_append_only();
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS patients;
