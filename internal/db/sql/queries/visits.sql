-- name: PatientExists :one
SELECT EXISTS (
  SELECT 1 FROM patients
  WHERE id = $1
    AND tenant_id = $2
);

-- name: CreateVisit :one
INSERT INTO visits (
  tenant_id, patient_id, employee_id, planned_start, status, created_by
)
VALUES ($1, $2, $3, $4, 'planned', $5)
RETURNING *;

-- name: GetVisit :one
SELECT * FROM visits
WHERE id = $1
  AND tenant_id = $2;

-- name: ListVisitsByPatient :many
SELECT * FROM visits
WHERE tenant_id = $1
  AND patient_id = $2
ORDER BY planned_start DESC, created_at DESC
LIMIT sqlc.arg(max_results);

-- name: UpdateVisitSchedule :one
UPDATE visits
SET employee_id = $3,
    planned_start = $4,
    updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'planned'
RETURNING *;

-- name: StartVisit :one
UPDATE visits
SET actual_employee_id = $3,
    actual_start = clock_timestamp(),
    status = 'in_progress',
    updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'planned'
RETURNING *;

-- name: CompleteVisit :one
UPDATE visits
SET actual_end = clock_timestamp(),
    status = 'completed',
    updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'in_progress'
RETURNING *;

-- name: CancelVisit :one
UPDATE visits
SET status = 'cancelled',
    updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND status IN ('planned', 'in_progress')
RETURNING *;

-- name: CorrectVisit :one
UPDATE visits
SET actual_start = $3,
    actual_end = $4,
    updated_at = NOW()
WHERE id = $1
  AND tenant_id = $2
  AND status IN ('in_progress', 'completed')
RETURNING *;
