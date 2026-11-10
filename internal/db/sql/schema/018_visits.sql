-- +goose Up
-- A visit is the planned or actual deployment of one employee to one patient
-- (doc 12). Patient, visit and report are separate entities: a visit only
-- schedules and records the deployment, it does not contain the care report.
-- The planned employee and the employee who actually performed the visit are
-- stored separately so substitutions are explicit and reportable.
CREATE TABLE visits (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID NOT NULL,
  -- Planned employee. The actual employee is recorded when the visit starts.
  employee_id UUID NOT NULL,
  actual_employee_id UUID,
  planned_start TIMESTAMPTZ NOT NULL,
  actual_start TIMESTAMPTZ,
  actual_end TIMESTAMPTZ,
  status TEXT NOT NULL DEFAULT 'planned'
    CHECK (status IN ('planned', 'in_progress', 'completed', 'cancelled')),
  created_by UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT visits_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT visits_patient_fk
    FOREIGN KEY (patient_id, tenant_id)
    REFERENCES patients(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT visits_employee_fk
    FOREIGN KEY (employee_id, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT visits_actual_employee_fk
    FOREIGN KEY (actual_employee_id, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT visits_created_by_fk
    FOREIGN KEY (created_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT visits_actual_range
    CHECK (actual_end IS NULL OR actual_start IS NULL OR actual_end >= actual_start),
  CONSTRAINT visits_completed_requires_times
    CHECK (status <> 'completed' OR (actual_start IS NOT NULL AND actual_end IS NOT NULL)),
  CONSTRAINT visits_in_progress_requires_start
    CHECK (status <> 'in_progress' OR (actual_start IS NOT NULL AND actual_end IS NULL))
);

CREATE INDEX visits_tenant_employee_start_idx
  ON visits(tenant_id, employee_id, planned_start);
CREATE INDEX visits_tenant_actual_employee_start_idx
  ON visits(tenant_id, actual_employee_id, planned_start);
CREATE INDEX visits_tenant_patient_start_idx
  ON visits(tenant_id, patient_id, planned_start DESC);
CREATE INDEX visits_tenant_status_idx
  ON visits(tenant_id, status);

ALTER TABLE visits ENABLE ROW LEVEL SECURITY;
ALTER TABLE visits FORCE ROW LEVEL SECURITY;

CREATE POLICY visits_isolation ON visits
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON visits TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS visits;
