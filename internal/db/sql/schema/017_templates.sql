-- +goose Up
-- A template is a patient-specific, versioned list of suggested activities
-- (doc 11). Rows are immutable versions: editing a template creates a new
-- version and marks the previous one superseded, so a historical report can
-- always name the exact template version it used.
CREATE TABLE templates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID NOT NULL,
  name TEXT NOT NULL,
  created_by UUID NOT NULL,
  version INT NOT NULL CHECK (version >= 1),
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'superseded', 'archived')),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT templates_version_key
    UNIQUE (tenant_id, patient_id, name, version),
  -- Composite key so template activities stay inside the same tenant.
  CONSTRAINT templates_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT templates_patient_fk
    FOREIGN KEY (patient_id, tenant_id)
    REFERENCES patients(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT templates_created_by_fk
    FOREIGN KEY (created_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

-- Only one active version may exist per patient template name.
CREATE UNIQUE INDEX templates_active_key
  ON templates(tenant_id, patient_id, name)
  WHERE status = 'active';
CREATE INDEX templates_tenant_patient_idx
  ON templates(tenant_id, patient_id, status, name);

CREATE TABLE template_activities (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  template_id UUID NOT NULL,
  -- Doc 11 calls this column activity_id. It references the billable service
  -- definition (the catalog activity), not a performed activity row: a
  -- template is a suggestion and must not create performed activities.
  activity_id UUID NOT NULL
    REFERENCES service_definitions(id) ON DELETE RESTRICT,
  default_quantity INT NOT NULL DEFAULT 1 CHECK (default_quantity > 0),
  sort_order INT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT template_activities_template_fk
    FOREIGN KEY (template_id, tenant_id)
    REFERENCES templates(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT template_activities_unique
    UNIQUE (template_id, activity_id)
);

CREATE INDEX template_activities_template_idx
  ON template_activities(tenant_id, template_id, sort_order);

ALTER TABLE templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE templates FORCE ROW LEVEL SECURITY;
ALTER TABLE template_activities ENABLE ROW LEVEL SECURITY;
ALTER TABLE template_activities FORCE ROW LEVEL SECURITY;

CREATE POLICY templates_isolation ON templates
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY template_activities_isolation ON template_activities
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON templates, template_activities TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS template_activities;
DROP TABLE IF EXISTS templates;
