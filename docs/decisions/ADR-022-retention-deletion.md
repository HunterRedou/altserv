# ADR-022: Retention review and patient deletion

## Problem

The compliance documents said that retention is a manual procedure and that
there is no patient delete API. There was no controlled way to review retention
candidates, no legal hold, and no four-eyes deletion. Without these, a tenant
could not lawfully delete a patient, and there was no evidence that a
deletion happened.

The step had to add retention and deletion without weakening the rules:

- the application never deletes care data on its own
- a retention policy is disabled until a tenant `chef` confirms it
- the review job reports candidate counts only
- a legal hold blocks deletion while it is active
- patient deletion needs two different tenant chefs
- `platform_admin` is denied
- the deletion register is append-only evidence
- audit metadata never contains patient names, free-text reasons, notes,
  legal-basis text or health data

## Alternatives

- Let a single `chef` delete a patient.
- Let `platform_admin` delete a patient.
- Seed default retention periods in the migration.
- Let the review job create deletion requests.
- Let the review job delete candidates.
- Store the free-text reason in audit metadata.
- Let the application role update or delete register rows.
- Skip foreign keys and delete with raw SQL.

## Decision

### Retention policies

- A policy is tenant-configured. It binds a category to a trigger event and a
  period in months. Categories and triggers are fixed pairs enforced by the
  database.
- A new policy is `disabled`. Only a tenant `chef` confirms it. A changed
  period returns the policy to `disabled` and must be confirmed again.
- No period is seeded. The controller must confirm the period after
  professional review.
- Confirmation is the controller's explicit decision that the period has been
  reviewed.

### Legal holds

- A hold is tenant-bound. It can be patient-scoped or tenant-wide.
- A tenant `chef` declares and lifts a hold. A hold restricts processing, so
  one chef is enough.
- An active hold blocks a deletion request, an approval and an execution.
- A hold is reviewed at least every six months. `DefaultLegalHoldReviewMonths`
  is 6.

### Retention review

- `retention_review_candidates(filter_tenant UUID DEFAULT NULL)` is
  `SECURITY DEFINER`. It returns candidate counts only.
- The job calls it with `NULL` (all tenants). The chef API calls it with the
  session tenant.
- The function writes one `retention.reviewed` audit event per tenant that has a
  confirmed policy. It never creates a deletion request and never deletes data.
- The job runs once at startup and then every `RETENTION_REVIEW_INTERVAL`
  (default 24h, minimum 1h, maximum 7d).
- Metrics are `altserv_retention_reviews_total` and
  `altserv_retention_review_candidates_total`.

### Patient deletion

- A tenant `chef` requests deletion of one patient. An active legal hold blocks
  the request immediately.
- A different tenant `chef` approves or rejects. The requester cannot approve.
  `platform_admin` is denied.
- The approving chef executes. The executor must differ from the requester. An
  active legal hold blocks execution.
- Execution deletes in dependency order: billing runs, reports, report
  activities, visits, activities, templates, assignments, search tokens,
  break-glass grants, then the patient row.
- A row-count snapshot is stored in the append-only `deletion_register`.
- The request is marked executed. A second execution is a conflict.

### Deletion register

- The register is append-only evidence. The application role has `SELECT` and
  `INSERT` only. Update, delete and truncate are blocked by triggers for every
  role.
- The register stores row counts, not the deleted personal data.

### Time handling

- `patients.status_changed_at` is a reliable end-of-care timestamp. `updated_at`
  changes on every edit, so it cannot drive a retention period.
- `UpdatePatient` changes `status_changed_at` only on a real status transition,
  using `clock_timestamp()`.
- The review uses `clock_timestamp()` for age checks.

### Tenant scoping

- Every retention and deletion table has `ENABLE ROW LEVEL SECURITY` and
  `FORCE ROW LEVEL SECURITY`.
- `legal_holds.patient_id` and `deletion_requests.patient_id` have no patient
  foreign key, so the rows survive patient deletion. The tenant foreign key
  preserves isolation.

### Audit

- The following actions are written in the tenant: `retention.policy_created`,
  `retention.policy_updated`, `retention.policy_confirmed`,
  `retention.policy_disabled`, `retention.policies_listed`,
  `retention.legal_hold_declared`, `retention.legal_hold_lifted`,
  `retention.legal_holds_listed`, `retention.reviewed`,
  `deletion.requested`, `deletion.approved`, `deletion.rejected`,
  `deletion.cancelled`, `deletion.executed`, `deletion.listed`,
  `deletion.register_listed`, and `access.denied`.
- Metadata contains ids, counts, categories, periods, scopes and patient ids.
  It never contains patient names, free-text reasons, notes, legal-basis text
  or health data. The reason stays in the tenant-bound row.

## Reasons

- A disabled-by-default policy keeps an unreviewed period from deleting data.
- Requiring confirmation after a period change prevents a silent change.
- Reporting candidates only keeps the application from deleting on its own.
- A legal hold is a tenant decision, so one chef is enough to declare it.
- Four eyes on deletion keeps one compromised account from erasing a patient.
- Denying `platform_admin` keeps support from erasing care data.
- An append-only register gives evidence without copying personal data.
- `clock_timestamp()` closes the stale-time gap in a long transaction.

## Consequences

- A tenant needs two chefs to delete a patient. A single-chef tenant must
  promote an employee first. The chef role-change path already allows this.
- The review job never deletes. A tenant must act on the candidate counts
  through the deletion workflow.
- Break-glass grants cascade away with a deleted patient, so the evidence that
  support access was approved can be lost. Export the grant first if the
  contract requires it.
- Tenant termination still fails once the tenant has audit events. Step 5 must
  address this.
- The application role needs `DELETE` on `break_glass_grants` for patient
  deletion to cascade. Migration 024 grants it.

## Risks

- A tenant chef may approve a deletion without checking the request. The
  four-eyes rule, the reason field and the audit trail reduce, but do not
  remove, this risk.
- A legal hold that is never reviewed can block deletion indefinitely. The
  six-month review date and the audit trail mitigate this.
- A candidate count that nobody acts on leaves data beyond its period. The
  operator must monitor the metrics and the audit events.
- A restored backup can reintroduce deleted data. The operator must document
  the backup window.

## Open questions

- Which care documentation period applies to the first tenants?
- Should the application expose a guided deletion workflow with a four-eyes
  check beyond the API?
- Should audit events be exported before a tenant is deleted?
- How is a deletion request handled when the subject is also in a backup?
- Should open deletion requests appear in the operations dashboard?