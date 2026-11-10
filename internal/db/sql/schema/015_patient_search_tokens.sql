-- +goose Up
CREATE TABLE patient_search_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  patient_id UUID NOT NULL,
  token_hash BYTEA NOT NULL,
  token_type TEXT NOT NULL
    CHECK (token_type IN ('first_name', 'last_name')),
  key_version INT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT NOW(),

  CONSTRAINT patient_search_tokens_patient_fk
    FOREIGN KEY (patient_id, tenant_id)
    REFERENCES patients(id, tenant_id) ON DELETE CASCADE,

  CONSTRAINT patient_search_tokens_unique
    UNIQUE (tenant_id, patient_id, token_hash, token_type)
);

CREATE INDEX patient_search_tokens_lookup_idx
  ON patient_search_tokens(tenant_id, token_hash);

ALTER TABLE patient_search_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE patient_search_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY patient_search_tokens_isolation ON patient_search_tokens
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON patient_search_tokens TO altserv_app;

-- +goose Down
DROP TABLE IF EXISTS patient_search_tokens;
