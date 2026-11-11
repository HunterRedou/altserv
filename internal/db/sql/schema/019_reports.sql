-- +goose Up
-- A report documents what happened during a visit (doc 13). A patient, a visit
-- and a report are separate entities: the visit schedules the deployment, the
-- report records the care. A report is created as a draft, submitted, approved
-- and finally marked ready for billing. After submission there is no silent
-- change: the report must be returned to draft first, and the return is stored
-- as a separate transition row with a reason.

-- Report activities reference performed activities, not catalog definitions.
ALTER TABLE activities ADD CONSTRAINT activities_id_tenant_key UNIQUE (id, tenant_id);

CREATE TABLE reports (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  visit_id UUID NOT NULL,
  patient_id UUID NOT NULL,
  -- employee_id is the employee who performed the visit and wrote the report.
  employee_id UUID NOT NULL,
  status TEXT NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft', 'submitted', 'approved', 'ready_for_billing')),
  note_enc BYTEA,
  note_key_version INT,
  -- Optimistic locking: every write must name the version it read.
  version INT NOT NULL DEFAULT 1 CHECK (version >= 1),
  created_by UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  submitted_at TIMESTAMPTZ,
  approved_at TIMESTAMPTZ,
  approved_by UUID,

  CONSTRAINT reports_id_tenant_key UNIQUE (id, tenant_id),
  -- One visit has at most one report.
  CONSTRAINT reports_visit_key UNIQUE (tenant_id, visit_id),
  CONSTRAINT reports_note_version
    CHECK ((note_enc IS NULL AND note_key_version IS NULL)
      OR (note_enc IS NOT NULL AND note_key_version >= 1)),
  CONSTRAINT reports_submitted_at
    CHECK (status = 'draft' OR submitted_at IS NOT NULL),
  CONSTRAINT reports_approved_fields
    CHECK ((status IN ('approved', 'ready_for_billing'))
      = (approved_at IS NOT NULL AND approved_by IS NOT NULL)),
  CONSTRAINT reports_visit_fk
    FOREIGN KEY (visit_id, tenant_id)
    REFERENCES visits(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT reports_patient_fk
    FOREIGN KEY (patient_id, tenant_id)
    REFERENCES patients(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT reports_employee_fk
    FOREIGN KEY (employee_id, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT reports_created_by_fk
    FOREIGN KEY (created_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT reports_approved_by_fk
    FOREIGN KEY (approved_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX reports_tenant_patient_idx ON reports(tenant_id, patient_id, created_at DESC);
CREATE INDEX reports_tenant_status_idx ON reports(tenant_id, status);
CREATE INDEX reports_tenant_employee_idx ON reports(tenant_id, employee_id, created_at DESC);

CREATE TABLE report_activities (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  report_id UUID NOT NULL,
  activity_id UUID NOT NULL,
  quantity INT NOT NULL CHECK (quantity > 0 AND quantity <= 10000),
  performed_at TIMESTAMPTZ NOT NULL,
  sort_order INT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT report_activities_id_tenant_key UNIQUE (id, tenant_id),
  -- A performed activity belongs to at most one report, so it cannot be
  -- reported twice and billed twice.
  CONSTRAINT report_activities_activity_key UNIQUE (tenant_id, activity_id),
  CONSTRAINT report_activities_report_fk
    FOREIGN KEY (report_id, tenant_id)
    REFERENCES reports(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT report_activities_activity_fk
    FOREIGN KEY (activity_id, tenant_id)
    REFERENCES activities(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX report_activities_report_idx ON report_activities(report_id, sort_order);

-- Returns and corrections are modeled separately from the report row. A return
-- moves a submitted report back to draft and stores the reason. The encrypted
-- reason may contain health data, so it uses the same key as the report note.
CREATE TABLE report_transitions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  report_id UUID NOT NULL,
  from_status TEXT NOT NULL,
  to_status TEXT NOT NULL,
  -- A return sends a submitted report back; a correction reopens an approved
  -- report. Both are explicit and stored separately from the report row.
  kind TEXT NOT NULL CHECK (kind IN ('return', 'correction')),
  actor_user_id UUID NOT NULL,
  reason_enc BYTEA,
  reason_key_version INT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT report_transitions_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT report_transitions_statuses
    CHECK (from_status IN ('draft', 'submitted', 'approved', 'ready_for_billing')
      AND to_status IN ('draft', 'submitted', 'approved', 'ready_for_billing')),
  CONSTRAINT report_transitions_reason_version
    CHECK ((reason_enc IS NULL AND reason_key_version IS NULL)
      OR (reason_enc IS NOT NULL AND reason_key_version >= 1)),
  CONSTRAINT report_transitions_report_fk
    FOREIGN KEY (report_id, tenant_id)
    REFERENCES reports(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT report_transitions_actor_fk
    FOREIGN KEY (actor_user_id, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX report_transitions_report_idx ON report_transitions(report_id, created_at);

ALTER TABLE reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE reports FORCE ROW LEVEL SECURITY;
CREATE POLICY reports_isolation ON reports
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE report_activities ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_activities FORCE ROW LEVEL SECURITY;
CREATE POLICY report_activities_isolation ON report_activities
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

ALTER TABLE report_transitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_transitions FORCE ROW LEVEL SECURITY;
CREATE POLICY report_transitions_isolation ON report_transitions
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON reports, report_activities TO altserv_app;
-- Transitions are append-only for the application role.
GRANT SELECT, INSERT ON report_transitions TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS report_transitions;
DROP TABLE IF EXISTS report_activities;
DROP TABLE IF EXISTS reports;
ALTER TABLE activities DROP CONSTRAINT IF EXISTS activities_id_tenant_key;
