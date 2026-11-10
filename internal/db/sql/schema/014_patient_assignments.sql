-- +goose Up
CREATE TABLE patient_assignments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID NOT NULL,
  employee_id UUID NOT NULL,
  assigned_by UUID NOT NULL,
  assignment_type TEXT NOT NULL
    CHECK (assignment_type IN ('regular', 'substitute')),
  valid_from TIMESTAMP NOT NULL,
  valid_until TIMESTAMP,
  revoked_at TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT patient_assignments_valid_range
    CHECK (valid_until IS NULL OR valid_until > valid_from),

  -- Composite foreign keys keep every reference inside the same tenant.
  CONSTRAINT patient_assignments_patient_fk
    FOREIGN KEY (patient_id, tenant_id)
    REFERENCES patients(id, tenant_id) ON DELETE CASCADE,

  CONSTRAINT patient_assignments_employee_fk
    FOREIGN KEY (employee_id, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE CASCADE,

  CONSTRAINT patient_assignments_assigned_by_fk
    FOREIGN KEY (assigned_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX patient_assignments_tenant_employee_idx
  ON patient_assignments(tenant_id, employee_id, valid_from, valid_until);

CREATE INDEX patient_assignments_tenant_patient_idx
  ON patient_assignments(tenant_id, patient_id);

ALTER TABLE patient_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE patient_assignments FORCE ROW LEVEL SECURITY;

CREATE POLICY patient_assignments_isolation ON patient_assignments
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON patient_assignments TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS patient_assignments;
