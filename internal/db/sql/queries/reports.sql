-- name: GetVisitForReport :one
SELECT * FROM visits
WHERE id = $1
  AND tenant_id = $2;

-- name: CreateReport :one
INSERT INTO reports (
  id, tenant_id, visit_id, patient_id, employee_id, status,
  note_enc, note_key_version, created_by
)
VALUES ($1, $2, $3, $4, $5, 'draft', $6, $7, $8)
RETURNING *;

-- name: GetReport :one
SELECT * FROM reports
WHERE id = $1
  AND tenant_id = $2;

-- name: GetReportByVisit :one
SELECT * FROM reports
WHERE tenant_id = $1
  AND visit_id = $2;

-- name: ListReportsByVisit :many
SELECT * FROM reports
WHERE tenant_id = $1
  AND visit_id = $2
ORDER BY created_at DESC;

-- name: ListReportsByPatient :many
SELECT * FROM reports
WHERE tenant_id = $1
  AND patient_id = $2
ORDER BY created_at DESC
LIMIT sqlc.arg(max_results);

-- name: UpdateReportDraft :one
UPDATE reports
SET note_enc = $3,
    note_key_version = $4,
    version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'draft'
  AND version = $5
RETURNING *;

-- name: TouchReportDraft :one
UPDATE reports
SET version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'draft'
  AND version = $3
RETURNING *;

-- name: SubmitReport :one
UPDATE reports
SET status = 'submitted',
    submitted_at = clock_timestamp(),
    version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'draft'
  AND version = $3
RETURNING *;

-- name: ApproveReport :one
UPDATE reports
SET status = 'approved',
    approved_at = clock_timestamp(),
    approved_by = $3,
    version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'submitted'
  AND version = $4
RETURNING *;

-- name: ReturnReport :one
UPDATE reports
SET status = 'draft',
    approved_at = NULL,
    approved_by = NULL,
    version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status IN ('submitted', 'approved')
  AND version = $3
RETURNING *;

-- name: MarkReportReadyForBilling :one
UPDATE reports
SET status = 'ready_for_billing',
    version = version + 1,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'approved'
  AND version = $3
RETURNING *;

-- name: InsertReportActivity :one
INSERT INTO report_activities (
  tenant_id, report_id, activity_id, quantity, performed_at, sort_order
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteReportActivities :exec
DELETE FROM report_activities
WHERE report_id = $1
  AND tenant_id = $2;

-- name: ListReportActivities :many
SELECT * FROM report_activities
WHERE report_id = $1
  AND tenant_id = $2
ORDER BY sort_order, performed_at, created_at;

-- name: GetReportActivityAttachment :one
SELECT report_id FROM report_activities
WHERE tenant_id = $1
  AND activity_id = $2;

-- name: CreateReportTransition :one
INSERT INTO report_transitions (
  id, tenant_id, report_id, from_status, to_status, kind, actor_user_id,
  reason_enc, reason_key_version
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListReportTransitions :many
SELECT * FROM report_transitions
WHERE report_id = $1
  AND tenant_id = $2
ORDER BY created_at, id;
