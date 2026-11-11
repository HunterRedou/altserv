-- +goose Up
-- The billing assistant turns a released report into deterministic billing
-- suggestions (doc 14). It is not a generative model: the same report, contract
-- and rule set always produce the same lines and findings. A chef must approve
-- a run before it can be exported. No real catalog, contract or rule data is
-- seeded here on purpose; that data must be verified professionally first.

-- Tenant-specific structural billing rules. The engine also enforces generic
-- rules (validity, billability, report status, double billing). These rows add
-- exclusions, prerequisites and combination rules that only the tenant can
-- define.
CREATE TABLE billing_rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('exclusion', 'prerequisite', 'combination')),
  service_definition_id UUID NOT NULL
    REFERENCES service_definitions(id) ON DELETE RESTRICT,
  related_service_definition_id UUID NOT NULL
    REFERENCES service_definitions(id) ON DELETE RESTRICT,
  legal_area TEXT NOT NULL CHECK (legal_area IN ('sgb_xi', 'sgb_v')),
  description TEXT NOT NULL DEFAULT '',
  valid_from TIMESTAMP NOT NULL,
  valid_until TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT billing_rules_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT billing_rules_distinct_services
    CHECK (service_definition_id <> related_service_definition_id),
  CONSTRAINT billing_rules_valid_range
    CHECK (valid_until IS NULL OR valid_until > valid_from),
  CONSTRAINT billing_rules_version_key
    UNIQUE (tenant_id, kind, service_definition_id,
      related_service_definition_id, valid_from)
);

CREATE INDEX billing_rules_tenant_idx
  ON billing_rules(tenant_id, valid_from, valid_until);

-- A run is one deterministic evaluation of a report. A report has at most one
-- active (draft or approved) run. Voided runs stay for history but keep no
-- lines, so the activities can be billed again by a corrected run.
CREATE TABLE billing_runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  report_id UUID NOT NULL,
  status TEXT NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft', 'approved', 'voided')),
  -- Optimistic locking for regenerate, approve and void.
  version INT NOT NULL DEFAULT 1 CHECK (version >= 1),
  generated_by UUID NOT NULL,
  generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  approved_by UUID,
  approved_at TIMESTAMPTZ,
  voided_by UUID,
  voided_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT billing_runs_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT billing_runs_approved_fields
    CHECK (status <> 'approved'
      OR (approved_at IS NOT NULL AND approved_by IS NOT NULL)),
  CONSTRAINT billing_runs_voided_fields
    CHECK (status <> 'voided'
      OR (voided_at IS NOT NULL AND voided_by IS NOT NULL)),
  CONSTRAINT billing_runs_report_fk
    FOREIGN KEY (report_id, tenant_id)
    REFERENCES reports(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT billing_runs_generated_by_fk
    FOREIGN KEY (generated_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT billing_runs_approved_by_fk
    FOREIGN KEY (approved_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT billing_runs_voided_by_fk
    FOREIGN KEY (voided_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX billing_runs_active_report_idx
  ON billing_runs(tenant_id, report_id)
  WHERE status IN ('draft', 'approved');

CREATE INDEX billing_runs_tenant_status_idx
  ON billing_runs(tenant_id, status, generated_at DESC);

-- One billing line per performed activity. The service fields are snapshots
-- from the activity and contract, so a later catalog or contract change cannot
-- rewrite an approved run.
CREATE TABLE billing_lines (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  run_id UUID NOT NULL,
  report_activity_id UUID NOT NULL,
  activity_id UUID NOT NULL,
  service_definition_id UUID NOT NULL
    REFERENCES service_definitions(id) ON DELETE RESTRICT,
  contract_id UUID NOT NULL,
  quantity INT NOT NULL CHECK (quantity > 0 AND quantity <= 10000),
  performed_at TIMESTAMPTZ NOT NULL,
  service_code TEXT NOT NULL,
  service_name TEXT NOT NULL,
  legal_area TEXT NOT NULL CHECK (legal_area IN ('sgb_xi', 'sgb_v')),
  payer_name TEXT NOT NULL,
  contract_reference TEXT NOT NULL DEFAULT '',
  sort_order INT NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT billing_lines_id_tenant_key UNIQUE (id, tenant_id),
  -- An activity can be billed at most once. A voided run deletes its lines, so
  -- a corrected run can bill the activity again.
  CONSTRAINT billing_lines_activity_key UNIQUE (tenant_id, activity_id),
  CONSTRAINT billing_lines_run_fk
    FOREIGN KEY (run_id, tenant_id)
    REFERENCES billing_runs(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT billing_lines_report_activity_fk
    FOREIGN KEY (report_activity_id, tenant_id)
    REFERENCES report_activities(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT billing_lines_activity_fk
    FOREIGN KEY (activity_id, tenant_id)
    REFERENCES activities(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT billing_lines_contract_fk
    FOREIGN KEY (contract_id, tenant_id)
    REFERENCES tenant_service_contracts(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX billing_lines_run_idx ON billing_lines(run_id, sort_order);

-- Findings make every suggestion checkable. An error blocks approval; a
-- warning or info is shown to the chef but does not block.
CREATE TABLE billing_findings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  run_id UUID NOT NULL,
  severity TEXT NOT NULL CHECK (severity IN ('error', 'warning', 'info')),
  code TEXT NOT NULL,
  message TEXT NOT NULL,
  activity_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT billing_findings_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT billing_findings_run_fk
    FOREIGN KEY (run_id, tenant_id)
    REFERENCES billing_runs(id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX billing_findings_run_idx ON billing_findings(run_id, severity, code);

ALTER TABLE billing_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_rules FORCE ROW LEVEL SECURITY;
ALTER TABLE billing_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_runs FORCE ROW LEVEL SECURITY;
ALTER TABLE billing_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_lines FORCE ROW LEVEL SECURITY;
ALTER TABLE billing_findings ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_findings FORCE ROW LEVEL SECURITY;

CREATE POLICY billing_rules_isolation ON billing_rules
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY billing_runs_isolation ON billing_runs
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY billing_lines_isolation ON billing_lines
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY billing_findings_isolation ON billing_findings
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON billing_rules, billing_runs TO altserv_app;
-- Lines and findings are replaced as a set while a run is a draft. They are
-- never updated in place.
GRANT SELECT, INSERT, DELETE ON billing_lines, billing_findings TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS billing_findings;
DROP TABLE IF EXISTS billing_lines;
DROP TABLE IF EXISTS billing_runs;
DROP TABLE IF EXISTS billing_rules;
