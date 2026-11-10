-- +goose Up
-- Reference catalogs and billable service definitions are global data. They
-- are read-only for the application; real Brandenburg catalog data must be
-- verified professionally before it is loaded (doc 9). No LK codes are seeded
-- here on purpose.
CREATE TABLE service_catalogs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT NOT NULL,
  name TEXT NOT NULL,
  region TEXT NOT NULL DEFAULT 'brandenburg',
  source_note TEXT NOT NULL DEFAULT '',
  valid_from TIMESTAMP NOT NULL,
  valid_until TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT service_catalogs_code_version_key UNIQUE (code, valid_from),
  CONSTRAINT service_catalogs_valid_range
    CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE INDEX service_catalogs_valid_idx
  ON service_catalogs(valid_from, valid_until);

CREATE TABLE service_definitions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  catalog_id UUID NOT NULL REFERENCES service_catalogs(id) ON DELETE CASCADE,
  code TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  legal_area TEXT NOT NULL
    CHECK (legal_area IN ('sgb_xi', 'sgb_v')),
  unit TEXT NOT NULL DEFAULT '',
  valid_from TIMESTAMP NOT NULL,
  valid_until TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT service_definitions_code_version_key
    UNIQUE (catalog_id, code, valid_from),
  CONSTRAINT service_definitions_valid_range
    CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE INDEX service_definitions_catalog_idx
  ON service_definitions(catalog_id, legal_area);
CREATE INDEX service_definitions_valid_idx
  ON service_definitions(valid_from, valid_until);

-- A tenant contract says which billable service the tenant may settle with
-- which payer, during which window. The validity window is the contract
-- version; historical activities keep their own snapshot.
CREATE TABLE tenant_service_contracts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  service_definition_id UUID NOT NULL
    REFERENCES service_definitions(id) ON DELETE RESTRICT,
  payer_name TEXT NOT NULL,
  contract_reference TEXT NOT NULL DEFAULT '',
  legal_area TEXT NOT NULL
    CHECK (legal_area IN ('sgb_xi', 'sgb_v')),
  valid_from TIMESTAMP NOT NULL,
  valid_until TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT tenant_service_contracts_version_key
    UNIQUE (tenant_id, service_definition_id, payer_name, valid_from),
  CONSTRAINT tenant_service_contracts_valid_range
    CHECK (valid_until IS NULL OR valid_until > valid_from),
  -- Composite key so activities can only reference a contract in their tenant.
  CONSTRAINT tenant_service_contracts_id_tenant_key UNIQUE (id, tenant_id)
);

CREATE INDEX tenant_service_contracts_tenant_idx
  ON tenant_service_contracts(tenant_id, valid_from, valid_until);
CREATE INDEX tenant_service_contracts_definition_idx
  ON tenant_service_contracts(service_definition_id);

-- An activity is what was actually done. It may reference a billable service
-- and contract, but it snapshots their state so a later catalog or contract
-- change cannot rewrite history (doc 9).
CREATE TABLE activities (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID NOT NULL,
  employee_id UUID NOT NULL,
  service_definition_id UUID
    REFERENCES service_definitions(id) ON DELETE RESTRICT,
  contract_id UUID,
  performed_at TIMESTAMP NOT NULL,
  duration_minutes INT NOT NULL
    CHECK (duration_minutes > 0 AND duration_minutes <= 1440),
  note_enc BYTEA,
  note_key_version INT,
  status TEXT NOT NULL DEFAULT 'recorded'
    CHECK (status IN ('recorded', 'voided')),

  -- Historical snapshot of the billable service state at recording time.
  service_code TEXT NOT NULL DEFAULT '',
  service_name TEXT NOT NULL DEFAULT '',
  legal_area TEXT NOT NULL DEFAULT ''
    CHECK (legal_area IN ('', 'sgb_xi', 'sgb_v')),
  contract_reference TEXT NOT NULL DEFAULT '',
  payer_name TEXT NOT NULL DEFAULT '',

  created_by UUID NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT activities_note_version
    CHECK ((note_enc IS NULL AND note_key_version IS NULL)
      OR (note_enc IS NOT NULL AND note_key_version >= 1)),

  CONSTRAINT activities_patient_fk
    FOREIGN KEY (patient_id, tenant_id)
    REFERENCES patients(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT activities_employee_fk
    FOREIGN KEY (employee_id, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT activities_created_by_fk
    FOREIGN KEY (created_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT activities_contract_fk
    FOREIGN KEY (contract_id, tenant_id)
    REFERENCES tenant_service_contracts(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX activities_tenant_patient_time_idx
  ON activities(tenant_id, patient_id, performed_at DESC);
CREATE INDEX activities_tenant_time_idx
  ON activities(tenant_id, performed_at DESC);
CREATE INDEX activities_contract_idx ON activities(contract_id);

-- Rules document how a tenant maps an activity label to a billable service.
-- They are configuration only for now; they are not applied automatically.
CREATE TABLE activity_service_rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  service_definition_id UUID NOT NULL
    REFERENCES service_definitions(id) ON DELETE RESTRICT,
  contract_id UUID,
  activity_label TEXT NOT NULL,
  legal_area TEXT NOT NULL
    CHECK (legal_area IN ('sgb_xi', 'sgb_v')),
  criteria JSONB NOT NULL DEFAULT '{}'::jsonb,
  valid_from TIMESTAMP NOT NULL,
  valid_until TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT activity_service_rules_version_key
    UNIQUE (tenant_id, activity_label, service_definition_id, valid_from),
  CONSTRAINT activity_service_rules_valid_range
    CHECK (valid_until IS NULL OR valid_until > valid_from),
  CONSTRAINT activity_service_rules_contract_fk
    FOREIGN KEY (contract_id, tenant_id)
    REFERENCES tenant_service_contracts(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX activity_service_rules_tenant_idx
  ON activity_service_rules(tenant_id, activity_label, valid_from, valid_until);

ALTER TABLE tenant_service_contracts ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_service_contracts FORCE ROW LEVEL SECURITY;
ALTER TABLE activities ENABLE ROW LEVEL SECURITY;
ALTER TABLE activities FORCE ROW LEVEL SECURITY;
ALTER TABLE activity_service_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_service_rules FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_service_contracts_isolation ON tenant_service_contracts
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY activities_isolation ON activities
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY activity_service_rules_isolation ON activity_service_rules
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- Reference data is readable by the application but written by migrations or
-- an administrative process outside the tenant role.
GRANT SELECT ON service_catalogs, service_definitions TO altserv_app;
GRANT SELECT, INSERT, UPDATE, DELETE
  ON tenant_service_contracts, activities, activity_service_rules TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS activity_service_rules;
DROP TABLE IF EXISTS activities;
DROP TABLE IF EXISTS tenant_service_contracts;
DROP TABLE IF EXISTS service_definitions;
DROP TABLE IF EXISTS service_catalogs;
