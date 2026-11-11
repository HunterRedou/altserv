-- +goose Up
-- Step 3 closes the platform-admin patient access gap with an explicit,
-- time-limited, read-only break-glass flow. A platform_admin requests access
-- to one named patient in one named tenant. A chef of that tenant approves or
-- denies the request. Only an approved, unexpired, unrevoked grant allows a
-- read. Every read is audited in the patient's tenant.
--
-- The table is tenant-bound: the row belongs to the tenant that owns the
-- patient, not to the tenant of the requesting platform_admin. This lets the
-- chef of the patient's tenant decide, and it keeps the grant inside the
-- tenant's retention and deletion boundary.

CREATE TABLE break_glass_grants (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID NOT NULL,
  -- The requester is a platform_admin and may belong to another tenant, so
  -- this is a plain user reference rather than a composite tenant reference.
  requested_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  -- The decision maker is a chef of tenant_id. The service checks the role.
  decided_by UUID REFERENCES users(id) ON DELETE RESTRICT,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'approved', 'denied', 'revoked', 'expired')),
  -- The reason is free text and may contain patient context. It is stored in
  -- this tenant-bound row and is never copied into audit metadata or logs.
  reason TEXT NOT NULL,
  decision_reason TEXT NOT NULL DEFAULT '',
  requested_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  decided_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

  CONSTRAINT break_glass_grants_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT break_glass_grants_patient_fk
    FOREIGN KEY (patient_id, tenant_id)
    REFERENCES patients(id, tenant_id) ON DELETE CASCADE,
  CONSTRAINT break_glass_grants_reason_length
    CHECK (char_length(reason) BETWEEN 1 AND 1000),
  CONSTRAINT break_glass_grants_decision_reason_length
    CHECK (char_length(decision_reason) <= 1000),
  CONSTRAINT break_glass_grants_expiry_after_request
    CHECK (expires_at IS NULL OR expires_at > requested_at),
  CONSTRAINT break_glass_grants_decision_fields CHECK (
    (status = 'pending'
      AND decided_at IS NULL AND decided_by IS NULL
      AND expires_at IS NULL AND revoked_at IS NULL)
    OR (status = 'approved'
      AND decided_at IS NOT NULL AND decided_by IS NOT NULL
      AND expires_at IS NOT NULL AND revoked_at IS NULL)
    OR (status = 'denied'
      AND decided_at IS NOT NULL AND decided_by IS NOT NULL
      AND expires_at IS NULL AND revoked_at IS NULL)
    OR (status = 'revoked'
      AND revoked_at IS NOT NULL)
    OR (status = 'expired'
      AND decided_at IS NOT NULL AND decided_by IS NOT NULL
      AND expires_at IS NOT NULL)
  )
);

CREATE INDEX break_glass_grants_tenant_status_idx
  ON break_glass_grants(tenant_id, status, requested_at DESC);
CREATE INDEX break_glass_grants_requester_idx
  ON break_glass_grants(tenant_id, requested_by, requested_at DESC);
CREATE INDEX break_glass_grants_patient_idx
  ON break_glass_grants(tenant_id, patient_id);

ALTER TABLE break_glass_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE break_glass_grants FORCE ROW LEVEL SECURITY;

CREATE POLICY break_glass_grants_isolation ON break_glass_grants
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- A grant is created, decided and revoked. It is never deleted through the
-- application role, so the decision evidence stays with the tenant.
GRANT SELECT, INSERT, UPDATE ON break_glass_grants TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS break_glass_grants;
