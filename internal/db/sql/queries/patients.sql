-- name: CreatePatient :one
INSERT INTO patients (
  id, tenant_id, first_name_enc, last_name_enc, birth_date_enc,
  status, key_version, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
RETURNING *;

-- name: GetPatient :one
SELECT * FROM patients
WHERE id = $1
  AND tenant_id = $2;

-- name: ListPatients :many
SELECT * FROM patients
WHERE tenant_id = $1
ORDER BY created_at DESC;

-- name: ListPatientsForEmployee :many
SELECT p.* FROM patients p
WHERE p.tenant_id = $1
  AND p.status <> 'archived'
  AND EXISTS (
    SELECT 1
    FROM patient_assignments a
    WHERE a.tenant_id = p.tenant_id
      AND a.patient_id = p.id
      AND a.employee_id = $2
      AND a.revoked_at IS NULL
      AND a.valid_from <= clock_timestamp()
      AND (a.valid_until IS NULL OR a.valid_until > clock_timestamp())
  )
ORDER BY p.created_at DESC;

-- name: UpdatePatient :one
UPDATE patients
SET first_name_enc = $3,
    last_name_enc = $4,
    birth_date_enc = $5,
    status = $6,
    key_version = $7,
    updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: DeletePatientSearchTokens :exec
DELETE FROM patient_search_tokens
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: CreatePatientSearchToken :exec
INSERT INTO patient_search_tokens (
  tenant_id, patient_id, token_hash, token_type, key_version
)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, patient_id, token_hash, token_type) DO NOTHING;

-- name: SearchPatients :many
SELECT p.* FROM patients p
WHERE p.tenant_id = $1
  AND (
    sqlc.arg(employee_id)::uuid = '00000000-0000-0000-0000-000000000000'::uuid
    OR (
      p.status <> 'archived'
      AND EXISTS (
        SELECT 1
        FROM patient_assignments a
        WHERE a.tenant_id = p.tenant_id
          AND a.patient_id = p.id
          AND a.employee_id = sqlc.arg(employee_id)
          AND a.revoked_at IS NULL
          AND a.valid_from <= clock_timestamp()
          AND (a.valid_until IS NULL OR a.valid_until > clock_timestamp())
      )
    )
  )
  AND (
    sqlc.arg(token_hashes)::text = ''
    OR NOT EXISTS (
      SELECT 1
      FROM regexp_split_to_table(sqlc.arg(token_hashes)::text, ',') AS terms(term)
      WHERE NOT EXISTS (
        SELECT 1
        FROM patient_search_tokens t
        WHERE t.tenant_id = p.tenant_id
          AND t.patient_id = p.id
          AND t.token_hash = decode(terms.term, 'hex')
      )
    )
  )
ORDER BY p.created_at DESC
LIMIT sqlc.arg(max_results);
