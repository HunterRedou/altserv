-- +goose Up
-- Electronic billing transmission is a boundary, not a live payer
-- integration. The concrete SGB XI §105 or SGB V §302 procedure, format and
-- channel must be verified with the payer before a real adapter is wired
-- (doc 14, docs/compliance/billing-requirements.md).
--
-- This table records every attempt in an append-only way. It stores ids,
-- status codes, a payload fingerprint and the payer's reference. It does not
-- store the claim payload, patient names, free text or other health data.
CREATE TABLE billing_transmissions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  run_id UUID NOT NULL,
  -- The legal procedure of this submission. A run may only be transmitted as
  -- one legal area at a time; a mixed run must be split first.
  legal_area TEXT NOT NULL CHECK (legal_area IN ('sgb_xi', 'sgb_v')),
  -- The adapter that handled the attempt: unconfigured or test. A real payer
  -- adapter must only be added after professional verification.
  adapter TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'rejected', 'failed')),
  -- The caller-supplied idempotency key. Replaying the same key returns the
  -- existing attempt instead of submitting again.
  idempotency_key TEXT NOT NULL,
  -- SHA-256 over the run and line snapshots. It proves which data was
  -- submitted without storing the claim payload again.
  payload_sha256 TEXT NOT NULL,
  -- Retry counter for rejected or failed attempts of the same run.
  attempt INT NOT NULL DEFAULT 1 CHECK (attempt >= 1),
  external_reference TEXT NOT NULL DEFAULT '',
  response_code TEXT NOT NULL DEFAULT '',
  -- A bounded payer message. It must not contain patient names, free text
  -- reasons or health data; the service only stores the adapter's code.
  response_message TEXT NOT NULL DEFAULT '',
  submitted_by UUID NOT NULL,
  -- clock_timestamp(), not NOW(): several attempts in one transaction must
  -- still order correctly.
  submitted_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

  CONSTRAINT billing_transmissions_id_tenant_key UNIQUE (id, tenant_id),
  CONSTRAINT billing_transmissions_idempotency_key
    UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT billing_transmissions_message_length
    CHECK (length(response_message) <= 500),
  CONSTRAINT billing_transmissions_run_fk
    FOREIGN KEY (run_id, tenant_id)
    REFERENCES billing_runs(id, tenant_id) ON DELETE RESTRICT,
  CONSTRAINT billing_transmissions_submitted_by_fk
    FOREIGN KEY (submitted_by, tenant_id)
    REFERENCES users(id, tenant_id) ON DELETE RESTRICT
);

-- A run has at most one open or accepted submission. A rejected or failed
-- attempt does not block a corrected retry with a new idempotency key.
CREATE UNIQUE INDEX billing_transmissions_active_run_idx
  ON billing_transmissions(tenant_id, run_id)
  WHERE status IN ('pending', 'accepted');

CREATE INDEX billing_transmissions_tenant_idx
  ON billing_transmissions(tenant_id, submitted_at DESC);

ALTER TABLE billing_transmissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_transmissions FORCE ROW LEVEL SECURITY;

CREATE POLICY billing_transmissions_isolation ON billing_transmissions
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

-- Append-only at the application role. There is no DELETE grant: a
-- transmission is an accounting record and must stay traceable. The tenant
-- cascade still removes the rows when a tenant is deleted by the owner.
GRANT SELECT, INSERT, UPDATE ON billing_transmissions TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS billing_transmissions;
