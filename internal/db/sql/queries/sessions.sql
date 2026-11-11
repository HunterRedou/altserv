-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, tenant_id, csrf_token, created_at, expires_at)
VALUES ($1, $2, $3, $4, NOW(), $5)
RETURNING *;

-- name: GetSessionUser :one
SELECT users.id, users.name, users.email, users.role, users.tenant_id,
       users.mfa_enabled, sessions.csrf_token
FROM sessions
JOIN users ON users.id = sessions.user_id
JOIN tenants ON tenants.id = users.tenant_id
WHERE sessions.token_hash = $1
  AND sessions.revoked_at IS NULL
  AND sessions.expires_at > NOW()
  AND tenants.status = 'valid'
  AND users.disabled_at IS NULL;

-- name: RevokeSession :exec
UPDATE sessions
SET revoked_at = NOW()
WHERE token_hash = $1
  AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at < NOW()
   OR revoked_at IS NOT NULL;
