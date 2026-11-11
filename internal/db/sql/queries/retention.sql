-- name: CreateRetentionPolicy :one
INSERT INTO retention_policies (
  tenant_id, category, trigger_event, period_months, note, created_by
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetRetentionPolicy :one
SELECT * FROM retention_policies
WHERE id = $1
  AND tenant_id = $2;

-- name: ListRetentionPolicies :many
SELECT * FROM retention_policies
WHERE tenant_id = $1
ORDER BY category;

-- name: UpdateRetentionPolicy :one
-- Changing a period is a new decision, so the policy returns to disabled and
-- must be confirmed again.
UPDATE retention_policies
SET period_months = $3,
    note = $4,
    status = 'disabled',
    confirmed_by = NULL,
    confirmed_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: ConfirmRetentionPolicy :one
UPDATE retention_policies
SET status = 'confirmed',
    confirmed_by = $3,
    confirmed_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'disabled'
RETURNING *;

-- name: DisableRetentionPolicy :one
UPDATE retention_policies
SET status = 'disabled',
    confirmed_by = NULL,
    confirmed_at = NULL,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
RETURNING *;

-- name: CreateLegalHold :one
INSERT INTO legal_holds (
  tenant_id, patient_id, reason, declared_by, review_due_at
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetLegalHold :one
SELECT * FROM legal_holds
WHERE id = $1
  AND tenant_id = $2;

-- name: ListLegalHolds :many
SELECT * FROM legal_holds
WHERE tenant_id = $1
ORDER BY declared_at DESC;

-- name: ListActiveLegalHoldsForPatient :many
SELECT * FROM legal_holds
WHERE tenant_id = $1
  AND status = 'active'
  AND (patient_id IS NULL OR patient_id = $2)
ORDER BY declared_at DESC;

-- name: HasActiveLegalHold :one
SELECT EXISTS (
  SELECT 1 FROM legal_holds
  WHERE tenant_id = $1
    AND status = 'active'
    AND (patient_id IS NULL OR patient_id = $2)
) AS held;

-- name: LiftLegalHold :one
UPDATE legal_holds
SET status = 'lifted',
    lifted_by = $3,
    lifted_at = clock_timestamp(),
    lift_reason = $4,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'active'
RETURNING *;

-- name: CreateDeletionRequest :one
INSERT INTO deletion_requests (
  tenant_id, patient_id, reason, requested_by
)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetDeletionRequest :one
SELECT * FROM deletion_requests
WHERE id = $1
  AND tenant_id = $2;

-- name: ListDeletionRequests :many
SELECT * FROM deletion_requests
WHERE tenant_id = $1
ORDER BY requested_at DESC;

-- name: ApproveDeletionRequest :one
UPDATE deletion_requests
SET status = 'approved',
    decided_by = $3,
    decided_at = clock_timestamp(),
    decision_reason = $4,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'pending'
  AND requested_by <> $3
RETURNING *;

-- name: RejectDeletionRequest :one
UPDATE deletion_requests
SET status = 'rejected',
    decided_by = $3,
    decided_at = clock_timestamp(),
    decision_reason = $4,
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'pending'
  AND requested_by <> $3
RETURNING *;

-- name: CancelDeletionRequest :one
UPDATE deletion_requests
SET status = 'cancelled',
    cancelled_by = $3,
    cancelled_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status IN ('pending', 'approved')
RETURNING *;

-- name: MarkDeletionExecuted :one
UPDATE deletion_requests
SET status = 'executed',
    executed_by = $3,
    executed_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE id = $1
  AND tenant_id = $2
  AND status = 'approved'
  AND requested_by <> $3
RETURNING *;

-- name: CreateDeletionRegisterEntry :one
INSERT INTO deletion_register (
  tenant_id, deletion_request_id, patient_id, executed_by, row_counts
)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListDeletionRegister :many
SELECT * FROM deletion_register
WHERE tenant_id = $1
ORDER BY executed_at DESC;

-- name: CountPatientDeletionRows :one
-- Counts every row that the patient deletion removes, including rows removed
-- by cascade. The counts are stored in the register as evidence.
SELECT jsonb_build_object(
  'billing_runs', (
    SELECT count(*) FROM billing_runs br
    JOIN reports r ON r.id = br.report_id AND r.tenant_id = br.tenant_id
    WHERE br.tenant_id = $1 AND r.patient_id = $2
  ),
  'billing_lines', (
    SELECT count(*) FROM billing_lines bl
    JOIN billing_runs br ON br.id = bl.run_id AND br.tenant_id = bl.tenant_id
    JOIN reports r ON r.id = br.report_id AND r.tenant_id = br.tenant_id
    WHERE bl.tenant_id = $1 AND r.patient_id = $2
  ),
  'billing_findings', (
    SELECT count(*) FROM billing_findings bf
    JOIN billing_runs br ON br.id = bf.run_id AND br.tenant_id = bf.tenant_id
    JOIN reports r ON r.id = br.report_id AND r.tenant_id = br.tenant_id
    WHERE bf.tenant_id = $1 AND r.patient_id = $2
  ),
  'reports', (
    SELECT count(*) FROM reports
    WHERE tenant_id = $1 AND patient_id = $2
  ),
  'report_activities', (
    SELECT count(*) FROM report_activities ra
    JOIN reports r ON r.id = ra.report_id AND r.tenant_id = ra.tenant_id
    WHERE ra.tenant_id = $1 AND r.patient_id = $2
  ),
  'report_transitions', (
    SELECT count(*) FROM report_transitions rt
    JOIN reports r ON r.id = rt.report_id AND r.tenant_id = rt.tenant_id
    WHERE rt.tenant_id = $1 AND r.patient_id = $2
  ),
  'visits', (
    SELECT count(*) FROM visits
    WHERE tenant_id = $1 AND patient_id = $2
  ),
  'activities', (
    SELECT count(*) FROM activities
    WHERE tenant_id = $1 AND patient_id = $2
  ),
  'templates', (
    SELECT count(*) FROM templates
    WHERE tenant_id = $1 AND patient_id = $2
  ),
  'template_activities', (
    SELECT count(*) FROM template_activities ta
    JOIN templates t ON t.id = ta.template_id AND t.tenant_id = ta.tenant_id
    WHERE ta.tenant_id = $1 AND t.patient_id = $2
  ),
  'patient_assignments', (
    SELECT count(*) FROM patient_assignments
    WHERE tenant_id = $1 AND patient_id = $2
  ),
  'patient_search_tokens', (
    SELECT count(*) FROM patient_search_tokens
    WHERE tenant_id = $1 AND patient_id = $2
  ),
  'break_glass_grants', (
    SELECT count(*) FROM break_glass_grants
    WHERE tenant_id = $1 AND patient_id = $2
  ),
  'audit_events_retained', true
) AS row_counts;

-- name: DeleteBillingRunsForPatient :execrows
DELETE FROM billing_runs AS br
USING reports AS r
WHERE br.tenant_id = $1
  AND r.tenant_id = br.tenant_id
  AND r.id = br.report_id
  AND r.patient_id = $2;

-- name: DeleteReportsForPatient :execrows
DELETE FROM reports
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: DeleteVisitsForPatient :execrows
DELETE FROM visits
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: DeleteActivitiesForPatient :execrows
DELETE FROM activities
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: DeleteTemplatesForPatient :execrows
DELETE FROM templates
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: DeleteAssignmentsForPatient :execrows
DELETE FROM patient_assignments
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: DeletePatientSearchTokensForPatient :execrows
DELETE FROM patient_search_tokens
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: DeleteBreakGlassGrantsForPatient :execrows
DELETE FROM break_glass_grants
WHERE tenant_id = $1
  AND patient_id = $2;

-- name: DeletePatientRow :execrows
DELETE FROM patients
WHERE tenant_id = $1
  AND id = $2;

-- name: RetentionReviewCandidates :many
-- Cross-tenant review used by the scheduled job. It writes one
-- retention.reviewed audit event per tenant with a confirmed policy.
SELECT (p).tenant_id::uuid AS tenant_id,
       (p).category::text AS category,
       (p).candidate_count::bigint AS candidate_count
FROM retention_review_candidates(NULL::uuid) AS p;

-- name: ReviewRetentionCandidatesForTenant :many
-- On-demand review for one tenant. The tenant id comes from the session, never
-- from the request body, so a chef cannot read another tenant's counts.
SELECT (p).tenant_id::uuid AS tenant_id,
       (p).category::text AS category,
       (p).candidate_count::bigint AS candidate_count
FROM retention_review_candidates($1::uuid) AS p;
