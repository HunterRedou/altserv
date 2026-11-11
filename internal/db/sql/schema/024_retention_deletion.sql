-- +goose Up
-- Step 4 adds controlled retention review and patient deletion.
--
-- The application never deletes care data on its own. A tenant may store a
-- retention policy, but a new policy is disabled and only an explicit
-- confirmation by a tenant chef enables it. The scheduled review job only
-- counts candidates and writes a retention.reviewed audit event; it does not
-- create deletion requests and it does not delete anything.
--
-- Patient deletion is a separate, explicit workflow:
--   one chef requests, a different chef approves, and a chef executes.
-- The deletion register is append-only evidence. Legal holds block deletion
-- while they are active.
--
-- No retention period is seeded here. The controller must confirm the period
-- after professional review (doc: retention-policy.md).

-- A patient needs a reliable end-of-care timestamp. updated_at changes on every
-- edit, so it cannot drive a retention period. status_changed_at changes only
-- when the status changes.
ALTER TABLE patients
  ADD COLUMN status_changed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp();

CREATE INDEX patients_tenant_status_changed_idx
  ON patients(tenant_id, status, status_changed_at);

-- Tenant-configured retention periods. Disabled by default. A row can be
-- created with a period, but it has no effect until a chef confirms it. The
-- period is a controller decision, not a legal determination by the software.
CREATE TABLE retention_policies (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  category TEXT NOT NULL CHECK (category IN (
    'patient_master_data',
    'care_documentation',
    'billing',
    'audit',
    'assignments',
    'break_glass'
  )),
  trigger_event TEXT NOT NULL CHECK (trigger_event IN (
    'end_of_care',
    'billing_year',
    'event_created',
    'revocation',
    'decision'
  )),
  period_months INT NOT NULL CHECK (period_months BETWEEN 1 AND 600),
  status TEXT NOT NULL DEFAULT 'disabled'
    CHECK (status IN ('disabled', 'confirmed')),
  -- Free text for the controller's rationale. It stays in this tenant-bound
  -- row and is never copied into audit metadata or logs.
  note TEXT NOT NULL DEFAULT '',
  created_by UUID NOT NULL,
  confirmed_by UUID,
  confirmed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

  CONSTRAINT retention_policies_tenant_category_key
    UNIQUE (tenant_id, category),
  CONSTRAINT retention_policies_category_trigger
    CHECK (
      (category = 'patient_master_data' AND trigger_event = 'end_of_care')
      OR (category = 'care_documentation' AND trigger_event = 'end_of_care')
      OR (category = 'billing' AND trigger_event = 'billing_year')
      OR (category = 'audit' AND trigger_event = 'event_created')
      OR (category = 'assignments' AND trigger_event = 'revocation')
      OR (category = 'break_glass' AND trigger_event = 'decision')
    ),
  CONSTRAINT retention_policies_note_length
    CHECK (char_length(note) <= 1000),
  CONSTRAINT retention_policies_confirmed_fields
    CHECK (
      (status = 'disabled' AND confirmed_by IS NULL AND confirmed_at IS NULL)
      OR (status = 'confirmed' AND confirmed_by IS NOT NULL AND confirmed_at IS NOT NULL)
    ),
  CONSTRAINT retention_policies_created_by_fk
    FOREIGN KEY (created_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT retention_policies_confirmed_by_fk
    FOREIGN KEY (confirmed_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX retention_policies_tenant_status_idx
  ON retention_policies(tenant_id, status, category);

-- A legal hold suspends deletion for one patient or for the whole tenant.
-- patient_id has no foreign key on purpose: the hold is evidence and must
-- survive the deletion of the patient it once protected.
CREATE TABLE legal_holds (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'lifted')),
  -- Free text. It may name a matter or a legal basis, so it is kept in the
  -- tenant-bound row and never copied into audit metadata or logs.
  reason TEXT NOT NULL,
  lift_reason TEXT NOT NULL DEFAULT '',
  declared_by UUID NOT NULL,
  declared_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  lifted_by UUID,
  lifted_at TIMESTAMPTZ,
  review_due_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

  CONSTRAINT legal_holds_reason_length
    CHECK (char_length(reason) BETWEEN 1 AND 1000),
  CONSTRAINT legal_holds_lift_reason_length
    CHECK (char_length(lift_reason) <= 1000),
  CONSTRAINT legal_holds_lifted_fields
    CHECK (
      (status = 'active' AND lifted_by IS NULL AND lifted_at IS NULL)
      OR (status = 'lifted' AND lifted_by IS NOT NULL AND lifted_at IS NOT NULL)
    ),
  CONSTRAINT legal_holds_declared_by_fk
    FOREIGN KEY (declared_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT legal_holds_lifted_by_fk
    FOREIGN KEY (lifted_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX legal_holds_tenant_status_idx
  ON legal_holds(tenant_id, status, declared_at DESC);
CREATE INDEX legal_holds_tenant_patient_idx
  ON legal_holds(tenant_id, patient_id)
  WHERE status = 'active';

-- One patient-scoped deletion request. patient_id has no foreign key on
-- purpose: the request and its decision are evidence and must survive the
-- deletion of the patient.
CREATE TABLE deletion_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled', 'executed')),
  -- Free text from the requester. Never copied into audit metadata or logs.
  reason TEXT NOT NULL,
  decision_reason TEXT NOT NULL DEFAULT '',
  requested_by UUID NOT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  decided_by UUID,
  decided_at TIMESTAMPTZ,
  cancelled_by UUID,
  cancelled_at TIMESTAMPTZ,
  executed_by UUID,
  executed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

  CONSTRAINT deletion_requests_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT deletion_requests_reason_length
    CHECK (char_length(reason) BETWEEN 1 AND 1000),
  CONSTRAINT deletion_requests_decision_reason_length
    CHECK (char_length(decision_reason) <= 1000),
  -- Four eyes: the decision maker must be a different chef.
  CONSTRAINT deletion_requests_four_eyes
    CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT deletion_requests_status_fields
    CHECK (
      (status = 'pending'
        AND decided_by IS NULL AND decided_at IS NULL
        AND cancelled_by IS NULL AND cancelled_at IS NULL
        AND executed_by IS NULL AND executed_at IS NULL)
      OR (status = 'approved'
        AND decided_by IS NOT NULL AND decided_at IS NOT NULL
        AND cancelled_by IS NULL AND cancelled_at IS NULL
        AND executed_by IS NULL AND executed_at IS NULL)
      OR (status = 'rejected'
        AND decided_by IS NOT NULL AND decided_at IS NOT NULL
        AND cancelled_by IS NULL AND cancelled_at IS NULL
        AND executed_by IS NULL AND executed_at IS NULL)
      OR (status = 'cancelled'
        AND cancelled_by IS NOT NULL AND cancelled_at IS NOT NULL
        AND executed_by IS NULL AND executed_at IS NULL)
      OR (status = 'executed'
        AND decided_by IS NOT NULL AND decided_at IS NOT NULL
        AND cancelled_by IS NULL AND cancelled_at IS NULL
        AND executed_by IS NOT NULL AND executed_at IS NOT NULL)
    ),
  CONSTRAINT deletion_requests_requested_by_fk
    FOREIGN KEY (requested_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT deletion_requests_decided_by_fk
    FOREIGN KEY (decided_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT deletion_requests_cancelled_by_fk
    FOREIGN KEY (cancelled_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT deletion_requests_executed_by_fk
    FOREIGN KEY (executed_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

-- A patient can have at most one open request. This prevents two chefs from
-- racing two approvals for the same patient.
CREATE UNIQUE INDEX deletion_requests_open_patient_idx
  ON deletion_requests(tenant_id, patient_id)
  WHERE status IN ('pending', 'approved');

CREATE INDEX deletion_requests_tenant_status_idx
  ON deletion_requests(tenant_id, status, requested_at DESC);
CREATE INDEX deletion_requests_tenant_patient_idx
  ON deletion_requests(tenant_id, patient_id, requested_at DESC);

-- Append-only evidence of an executed deletion. It records counts, not the
-- deleted personal data. The row must survive the patient.
CREATE TABLE deletion_register (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  deletion_request_id UUID NOT NULL,
  patient_id UUID NOT NULL,
  executed_by UUID NOT NULL,
  executed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  row_counts JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

  CONSTRAINT deletion_register_request_key
    UNIQUE (tenant_id, deletion_request_id),
  CONSTRAINT deletion_register_request_fk
    FOREIGN KEY (deletion_request_id, tenant_id)
    REFERENCES deletion_requests(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT deletion_register_executed_by_fk
    FOREIGN KEY (executed_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

CREATE INDEX deletion_register_tenant_time_idx
  ON deletion_register(tenant_id, executed_at DESC);

ALTER TABLE retention_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE retention_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE legal_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE legal_holds FORCE ROW LEVEL SECURITY;
ALTER TABLE deletion_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE deletion_requests FORCE ROW LEVEL SECURITY;
ALTER TABLE deletion_register ENABLE ROW LEVEL SECURITY;
ALTER TABLE deletion_register FORCE ROW LEVEL SECURITY;

CREATE POLICY retention_policies_isolation ON retention_policies
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY legal_holds_isolation ON legal_holds
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY deletion_requests_isolation ON deletion_requests
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY deletion_register_isolation ON deletion_register
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON retention_policies TO altserv_app;
GRANT SELECT, INSERT, UPDATE ON legal_holds TO altserv_app;
GRANT SELECT, INSERT, UPDATE ON deletion_requests TO altserv_app;
-- The register is append-only for the application role.
GRANT SELECT, INSERT ON deletion_register TO altserv_app;

-- A patient deletion removes the patient's break-glass grants. The grant rows
-- reference the patient with ON DELETE CASCADE, so the deletion workflow needs
-- DELETE here. The append-only guarantee in this migration covers the deletion
-- register and the audit log, not the grants themselves.
GRANT DELETE ON break_glass_grants TO altserv_app;

-- +goose StatementBegin
CREATE FUNCTION deletion_register_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'deletion_register is append-only';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER deletion_register_no_update_delete
  BEFORE UPDATE OR DELETE ON deletion_register
  FOR EACH ROW EXECUTE FUNCTION deletion_register_append_only();

CREATE TRIGGER deletion_register_no_truncate
  BEFORE TRUNCATE ON deletion_register
  FOR EACH STATEMENT EXECUTE FUNCTION deletion_register_append_only();

-- The retention review runs across tenants, so it cannot use the tenant-scoped
-- application role directly. This narrowly scoped SECURITY DEFINER function
-- returns candidate counts only and writes one retention.reviewed audit event
-- per tenant that has at least one confirmed policy. It never creates a
-- deletion request and never deletes data.
-- +goose StatementBegin
CREATE FUNCTION retention_review_candidates(filter_tenant UUID DEFAULT NULL)
RETURNS TABLE (tenant_id UUID, category TEXT, candidate_count BIGINT)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  RETURN QUERY
  WITH policies AS (
    SELECT rp.tenant_id, rp.category, rp.period_months
    FROM public.retention_policies rp
    WHERE rp.status = 'confirmed'
      AND (filter_tenant IS NULL OR rp.tenant_id = filter_tenant)
  ),
  counts AS (
    SELECT
      p.tenant_id,
      p.category,
      CASE p.category
        WHEN 'patient_master_data' THEN (
          SELECT count(*) FROM public.patients pt
          WHERE pt.tenant_id = p.tenant_id
            AND pt.status <> 'active'
            AND date_trunc('year', pt.status_changed_at)
                + interval '1 year'
                + make_interval(months => p.period_months) <= clock_timestamp()
            AND NOT EXISTS (
              SELECT 1 FROM public.legal_holds h
              WHERE h.tenant_id = pt.tenant_id
                AND h.status = 'active'
                AND (h.patient_id IS NULL OR h.patient_id = pt.id)
            )
        )
        WHEN 'care_documentation' THEN (
          SELECT count(*) FROM public.patients pt
          WHERE pt.tenant_id = p.tenant_id
            AND pt.status <> 'active'
            AND date_trunc('year', pt.status_changed_at)
                + interval '1 year'
                + make_interval(months => p.period_months) <= clock_timestamp()
            AND NOT EXISTS (
              SELECT 1 FROM public.legal_holds h
              WHERE h.tenant_id = pt.tenant_id
                AND h.status = 'active'
                AND (h.patient_id IS NULL OR h.patient_id = pt.id)
            )
        )
        WHEN 'billing' THEN (
          SELECT count(*) FROM public.billing_runs br
          JOIN public.reports r
            ON r.id = br.report_id AND r.tenant_id = br.tenant_id
          WHERE br.tenant_id = p.tenant_id
            AND date_trunc('year', br.generated_at)
                + interval '1 year'
                + make_interval(months => p.period_months) <= clock_timestamp()
            AND NOT EXISTS (
              SELECT 1 FROM public.legal_holds h
              WHERE h.tenant_id = br.tenant_id
                AND h.status = 'active'
                AND (h.patient_id IS NULL OR h.patient_id = r.patient_id)
            )
        )
        WHEN 'audit' THEN (
          SELECT count(*) FROM public.audit_events ae
          WHERE ae.tenant_id = p.tenant_id
            AND date_trunc('year', ae.occurred_at)
                + interval '1 year'
                + make_interval(months => p.period_months) <= clock_timestamp()
            AND NOT EXISTS (
              SELECT 1 FROM public.legal_holds h
              WHERE h.tenant_id = ae.tenant_id
                AND h.status = 'active'
                AND h.patient_id IS NULL
            )
        )
        WHEN 'assignments' THEN (
          SELECT count(*) FROM public.patient_assignments pa
          WHERE pa.tenant_id = p.tenant_id
            AND pa.revoked_at IS NOT NULL
            AND date_trunc('year', pa.revoked_at)
                + interval '1 year'
                + make_interval(months => p.period_months) <= clock_timestamp()
            AND NOT EXISTS (
              SELECT 1 FROM public.legal_holds h
              WHERE h.tenant_id = pa.tenant_id
                AND h.status = 'active'
                AND (h.patient_id IS NULL OR h.patient_id = pa.patient_id)
            )
        )
        WHEN 'break_glass' THEN (
          SELECT count(*) FROM public.break_glass_grants bg
          WHERE bg.tenant_id = p.tenant_id
            AND bg.decided_at IS NOT NULL
            AND date_trunc('year', bg.decided_at)
                + interval '1 year'
                + make_interval(months => p.period_months) <= clock_timestamp()
            AND NOT EXISTS (
              SELECT 1 FROM public.legal_holds h
              WHERE h.tenant_id = bg.tenant_id
                AND h.status = 'active'
                AND (h.patient_id IS NULL OR h.patient_id = bg.patient_id)
            )
        )
        ELSE 0
      END AS candidate_count
    FROM policies p
  ),
  audit AS (
    INSERT INTO public.audit_events (
      tenant_id, actor_user_id, action, entity_type, request_id, metadata
    )
    SELECT
      c.tenant_id,
      NULL,
      'retention.reviewed',
      'retention_policy',
      'maintenance',
      jsonb_build_object('candidates', jsonb_object_agg(c.category, c.candidate_count))
    FROM counts c
    GROUP BY c.tenant_id
    RETURNING 1
  )
  SELECT c.tenant_id, c.category, c.candidate_count
  FROM counts c;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION retention_review_candidates(UUID) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION retention_review_candidates(UUID) TO altserv_app;

-- +goose Down
DROP FUNCTION IF EXISTS retention_review_candidates(UUID);
DROP TRIGGER IF EXISTS deletion_register_no_truncate ON deletion_register;
DROP TRIGGER IF EXISTS deletion_register_no_update_delete ON deletion_register;
DROP FUNCTION IF EXISTS deletion_register_append_only();
DROP TABLE IF EXISTS deletion_register;
DROP TABLE IF EXISTS deletion_requests;
DROP TABLE IF EXISTS legal_holds;
DROP TABLE IF EXISTS retention_policies;
DROP INDEX IF EXISTS patients_tenant_status_changed_idx;
ALTER TABLE patients DROP COLUMN IF EXISTS status_changed_at;
