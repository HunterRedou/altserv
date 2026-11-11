-- name: CreateMFAChallenge :one
INSERT INTO mfa_challenges (token_hash, tenant_id, user_id, purpose, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetMFAChallenge :one
SELECT
  c.token_hash,
  c.tenant_id,
  c.user_id,
  c.purpose,
  c.expires_at,
  c.attempts,
  c.consumed_at,
  u.name,
  u.email,
  u.role,
  u.created_at,
  u.updated_at,
  u.mfa_secret_enc,
  u.mfa_secret_key_version,
  u.mfa_enabled,
  u.disabled_at,
  t.status AS tenant_status
FROM mfa_challenges c
JOIN users u ON u.id = c.user_id AND u.tenant_id = c.tenant_id
JOIN tenants t ON t.id = c.tenant_id
WHERE c.token_hash = $1;

-- name: IncrementMFAChallengeAttempts :one
UPDATE mfa_challenges
SET attempts = attempts + 1
WHERE token_hash = $1
RETURNING attempts;

-- name: ConsumeMFAChallenge :one
UPDATE mfa_challenges
SET consumed_at = clock_timestamp()
WHERE token_hash = $1
  AND consumed_at IS NULL
RETURNING token_hash;

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes
WHERE tenant_id = $1
  AND user_id = $2;

-- name: CreateRecoveryCode :exec
INSERT INTO user_recovery_codes (tenant_id, user_id, code_hash)
VALUES ($1, $2, $3);

-- name: ConsumeRecoveryCode :one
UPDATE user_recovery_codes
SET used_at = clock_timestamp()
WHERE tenant_id = $1
  AND user_id = $2
  AND code_hash = $3
  AND used_at IS NULL
RETURNING *;

-- name: SetUserMFASecret :one
UPDATE users
SET mfa_secret_enc = $3,
    mfa_secret_key_version = $4,
    mfa_enabled = FALSE,
    mfa_enrolled_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: EnableUserMFA :one
UPDATE users
SET mfa_secret_enc = $3,
    mfa_secret_key_version = $4,
    mfa_enabled = TRUE,
    mfa_enrolled_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: DisableUserMFA :one
UPDATE users
SET mfa_secret_enc = NULL,
    mfa_secret_key_version = NULL,
    mfa_enabled = FALSE,
    mfa_enrolled_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: DisableUser :one
UPDATE users
SET disabled_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: EnableUser :one
UPDATE users
SET disabled_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: RevokeAllUserSessions :exec
UPDATE sessions
SET revoked_at = clock_timestamp()
WHERE user_id = $1
  AND tenant_id = $2
  AND revoked_at IS NULL;

-- name: UpdateUserPassword :one
UPDATE users
SET hashed_password = $3,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: DeleteUnusedPasswordResetTokens :exec
DELETE FROM password_reset_tokens
WHERE tenant_id = $1
  AND user_id = $2
  AND used_at IS NULL;

-- name: CreatePasswordResetToken :one
INSERT INTO password_reset_tokens (token_hash, tenant_id, user_id, expires_at, issued_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetPasswordResetToken :one
SELECT
  t.token_hash,
  t.tenant_id,
  t.user_id,
  t.created_at,
  t.expires_at,
  t.used_at,
  t.issued_by,
  u.email,
  u.role,
  u.disabled_at,
  tenants.status AS tenant_status
FROM password_reset_tokens t
JOIN users u ON u.id = t.user_id AND u.tenant_id = t.tenant_id
JOIN tenants ON tenants.id = t.tenant_id
WHERE t.token_hash = $1;

-- name: ConsumePasswordResetToken :one
UPDATE password_reset_tokens
SET used_at = clock_timestamp()
WHERE token_hash = $1
  AND used_at IS NULL
RETURNING token_hash;

-- name: PurgeExpiredAuthRows :many
SELECT (p).tenant_id::uuid AS tenant_id, (p).deleted_count::bigint AS deleted_count
FROM purge_expired_auth_rows() AS p;
