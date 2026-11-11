-- +goose Up
-- Point 2 closes the authentication gaps: MFA for privileged roles, recovery
-- codes, single-use password reset tokens, a user disable state and a scheduled
-- session purge.

ALTER TABLE users
  ADD COLUMN disabled_at TIMESTAMPTZ,
  ADD COLUMN mfa_secret_enc BYTEA,
  ADD COLUMN mfa_secret_key_version INT,
  ADD COLUMN mfa_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN mfa_enrolled_at TIMESTAMPTZ;

ALTER TABLE users
  ADD CONSTRAINT users_mfa_secret_check
  CHECK (
    NOT mfa_enabled
    OR (mfa_secret_enc IS NOT NULL AND mfa_secret_key_version IS NOT NULL)
  );

CREATE INDEX users_tenant_disabled_idx ON users(tenant_id, disabled_at);

-- A login challenge is a short-lived, single-use second step. It is stored as a
-- hash like a session token, so a database leak cannot replay it.
CREATE TABLE mfa_challenges (
  token_hash BYTEA PRIMARY KEY,
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  purpose TEXT NOT NULL CHECK (purpose IN ('login', 'enroll')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  expires_at TIMESTAMPTZ NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  consumed_at TIMESTAMPTZ,
  FOREIGN KEY (user_id, tenant_id) REFERENCES users(id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX mfa_challenges_user_idx ON mfa_challenges(user_id);
CREATE INDEX mfa_challenges_expires_idx ON mfa_challenges(expires_at);

-- Recovery codes are single-use. Only the SHA-256 hash is stored.
CREATE TABLE user_recovery_codes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  code_hash BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  used_at TIMESTAMPTZ,
  UNIQUE (user_id, code_hash),
  FOREIGN KEY (user_id, tenant_id) REFERENCES users(id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX user_recovery_codes_user_idx ON user_recovery_codes(user_id) WHERE used_at IS NULL;

-- Password reset tokens are single-use and expire. The plaintext token is only
-- returned once to the issuer. Delivery by email is not configured.
CREATE TABLE password_reset_tokens (
  token_hash BYTEA PRIMARY KEY,
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at TIMESTAMPTZ,
  issued_by UUID REFERENCES users(id) ON DELETE SET NULL,
  FOREIGN KEY (user_id, tenant_id) REFERENCES users(id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX password_reset_tokens_user_idx ON password_reset_tokens(user_id) WHERE used_at IS NULL;
CREATE INDEX password_reset_tokens_expires_idx ON password_reset_tokens(expires_at);

ALTER TABLE mfa_challenges ENABLE ROW LEVEL SECURITY;
ALTER TABLE mfa_challenges FORCE ROW LEVEL SECURITY;
ALTER TABLE user_recovery_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_recovery_codes FORCE ROW LEVEL SECURITY;
ALTER TABLE password_reset_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE password_reset_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY mfa_challenges_isolation ON mfa_challenges
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY user_recovery_codes_isolation ON user_recovery_codes
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY password_reset_tokens_isolation ON password_reset_tokens
  USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON mfa_challenges, user_recovery_codes, password_reset_tokens TO altserv_app;

-- Maintenance deletes expired authentication rows across all tenants. It runs
-- as a narrowly scoped SECURITY DEFINER function because the application role
-- is tenant-scoped by RLS. The function writes the purge audit event itself, so
-- the count and the audit row commit together.
-- +goose StatementBegin
CREATE FUNCTION purge_expired_auth_rows() RETURNS TABLE (tenant_id UUID, deleted_count BIGINT)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
BEGIN
  RETURN QUERY
  WITH expired AS (
    SELECT s.tenant_id, count(*)::bigint AS n
    FROM public.sessions s
    WHERE s.expires_at < clock_timestamp()
       OR s.revoked_at IS NOT NULL
    GROUP BY s.tenant_id
  ), audited AS (
    INSERT INTO public.audit_events (
      tenant_id, actor_user_id, action, entity_type, request_id, metadata
    )
    SELECT e.tenant_id, NULL, 'auth.sessions_purged', 'session', 'maintenance',
           jsonb_build_object('count', e.n)
    FROM expired e
    RETURNING 1
  ), deleted AS (
    DELETE FROM public.sessions s
    WHERE s.expires_at < clock_timestamp()
       OR s.revoked_at IS NOT NULL
    RETURNING s.tenant_id
  )
  SELECT d.tenant_id, count(*)::bigint
  FROM deleted d
  GROUP BY d.tenant_id;

  DELETE FROM public.mfa_challenges
  WHERE expires_at < clock_timestamp()
     OR consumed_at IS NOT NULL;

  DELETE FROM public.password_reset_tokens
  WHERE expires_at < clock_timestamp()
     OR used_at IS NOT NULL;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION purge_expired_auth_rows() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION purge_expired_auth_rows() TO altserv_app;

-- +goose Down
DROP FUNCTION IF EXISTS purge_expired_auth_rows();

DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS user_recovery_codes;
DROP TABLE IF EXISTS mfa_challenges;

DROP INDEX IF EXISTS users_tenant_disabled_idx;

ALTER TABLE users
  DROP CONSTRAINT IF EXISTS users_mfa_secret_check,
  DROP COLUMN IF EXISTS mfa_enrolled_at,
  DROP COLUMN IF EXISTS mfa_enabled,
  DROP COLUMN IF EXISTS mfa_secret_key_version,
  DROP COLUMN IF EXISTS mfa_secret_enc,
  DROP COLUMN IF EXISTS disabled_at;
