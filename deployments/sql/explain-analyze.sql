-- Read-only query plans for the altserv hot paths.
--
-- Run as a database superuser or a role that can SET ROLE altserv_app, against
-- a copy of production data. The transaction is rolled back, so nothing is
-- changed. ANALYZE executes the SELECTs; it does not write rows.
--
--   psql "$DB_URL" -f deployments/sql/explain-analyze.sql
--
-- Read the plan bottom-up. Look for Seq Scan on a large table, a nested loop
-- with a high row count, or a sort that spills to disk. Compare the estimated
-- rows with the actual rows; a large difference means stale statistics.
--
-- Never paste a plan that contains patient names, notes or other health data
-- into a ticket. This file selects only ids and timestamps.

\set ON_ERROR_STOP on

SELECT id AS tenant_id FROM tenants ORDER BY created_at LIMIT 1 \gset
SELECT id AS patient_id FROM patients WHERE tenant_id = :'tenant_id' LIMIT 1 \gset
SELECT id AS visit_id FROM visits WHERE tenant_id = :'tenant_id' LIMIT 1 \gset
SELECT id AS report_id FROM reports WHERE tenant_id = :'tenant_id' LIMIT 1 \gset
SELECT id AS run_id FROM billing_runs WHERE tenant_id = :'tenant_id' LIMIT 1 \gset
SELECT id AS employee_id FROM users WHERE tenant_id = :'tenant_id' AND role = 'employee' LIMIT 1 \gset

BEGIN;
SET LOCAL ROLE altserv_app;
SELECT set_config('app.tenant_id', :'tenant_id', true);

-- 1. Patient list for one tenant.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, status, created_at, updated_at
FROM patients
WHERE tenant_id = :'tenant_id'
ORDER BY created_at DESC
LIMIT 50;

-- 2. Blind search lookup by tenant and HMAC token.
EXPLAIN (ANALYZE, BUFFERS)
SELECT DISTINCT patient_id
FROM patient_search_tokens
WHERE tenant_id = :'tenant_id'
  AND token_hash = decode(repeat('00', 32), 'hex');

-- 3. Performed activities for one patient in a time window.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, performed_at, duration_minutes, status
FROM activities
WHERE tenant_id = :'tenant_id'
  AND patient_id = :'patient_id'
  AND performed_at >= now() - interval '30 days'
ORDER BY performed_at DESC;

-- 4. Visits for one patient.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, planned_start, actual_start, status
FROM visits
WHERE tenant_id = :'tenant_id'
  AND patient_id = :'patient_id'
ORDER BY planned_start DESC
LIMIT 50;

-- 5. Reports for one visit.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, status, version, created_at
FROM reports
WHERE tenant_id = :'tenant_id'
  AND visit_id = :'visit_id'
ORDER BY created_at DESC;

-- 6. Report activities for one report.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, activity_id, quantity, performed_at, sort_order
FROM report_activities
WHERE tenant_id = :'tenant_id'
  AND report_id = :'report_id'
ORDER BY sort_order;

-- 7. Billing lines for one run.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, activity_id, quantity, service_code, sort_order
FROM billing_lines
WHERE tenant_id = :'tenant_id'
  AND run_id = :'run_id'
ORDER BY sort_order;

-- 8. Currently valid assignments for one employee. clock_timestamp() is used
--    so the plan matches the application's validity check.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, patient_id, valid_from, valid_until
FROM patient_assignments
WHERE tenant_id = :'tenant_id'
  AND employee_id = :'employee_id'
  AND revoked_at IS NULL
  AND valid_from <= clock_timestamp()
  AND (valid_until IS NULL OR valid_until > clock_timestamp());

-- 9. Audit list for one tenant, newest first.
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, action, entity_type, entity_id, occurred_at, request_id
FROM audit_events
WHERE tenant_id = :'tenant_id'
ORDER BY occurred_at DESC
LIMIT 50;

ROLLBACK;
