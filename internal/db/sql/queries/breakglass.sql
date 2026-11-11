-- name: CreateBreakGlassGrant :one
INSERT INTO break_glass_grants (tenant_id, patient_id, requested_by, reason)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetBreakGlassGrant :one
SELECT * FROM break_glass_grants
WHERE id = $1
  AND tenant_id = $2;

-- name: GetBreakGlassGrantForRequester :one
SELECT * FROM break_glass_grants
WHERE id = $1
  AND tenant_id = $2
  AND requested_by = $3;

-- name: GetActiveBreakGlassGrantForRequester :one
SELECT * FROM break_glass_grants
WHERE id = $1
  AND tenant_id = $2
  AND requested_by = $3
  AND status = 'approved'
  AND revoked_at IS NULL
  AND expires_at > clock_timestamp();

-- name: ListBreakGlassGrantsByTenant :many
SELECT * FROM break_glass_grants
WHERE tenant_id = $1
ORDER BY requested_at DESC, id DESC;

-- name: ListBreakGlassGrantsByRequester :many
SELECT * FROM break_glass_grants
WHERE tenant_id = $1
  AND requested_by = $2
ORDER BY requested_at DESC, id DESC;

-- name: ApproveBreakGlassGrant :one
UPDATE break_glass_grants
SET status = 'approved',
    decided_by = $3,
    decided_at = clock_timestamp(),
    expires_at = clock_timestamp() + make_interval(mins => $4::int),
    decision_reason = $5,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'pending'
RETURNING *;

-- name: DenyBreakGlassGrant :one
UPDATE break_glass_grants
SET status = 'denied',
    decided_by = $3,
    decided_at = clock_timestamp(),
    decision_reason = $4,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'pending'
RETURNING *;

-- name: RevokeBreakGlassGrant :one
UPDATE break_glass_grants
SET status = 'revoked',
    revoked_at = clock_timestamp(),
    decision_reason = $3,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status IN ('pending', 'approved')
RETURNING *;

-- name: TouchBreakGlassGrant :one
UPDATE break_glass_grants
SET last_used_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;
