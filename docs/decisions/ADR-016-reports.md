# ADR-016: Reports, approvals and returns

## Problem
A visit records that a deployment was planned and performed. It does not record
the care itself. Doc 13 requires a separate report that documents what happened
during the visit, which performed activities were part of it, and who approved
it for billing. After a report is submitted there must be no silent change. A
return or correction must be explicit and traceable. Two users may edit the same
draft at the same time, so a stale write must not overwrite a newer one.

## Alternatives
- Store the report inside the visit row
- Let a report point at catalog service definitions instead of performed activities
- Allow direct edits in every status and keep the history only in the audit log
- Use a lock on the report row instead of a version column
- Store the return reason as plain text in the report or audit metadata
- Use one generic `status_change` table for every action

## Decision
A report is one row in `reports` with its own lifecycle:

- A report belongs to exactly one visit and one patient. The database enforces
  one report per `(tenant_id, visit_id)`.
- `employee_id` is the employee who performed the visit. When a substitute
  started the visit, the report uses the actual employee, not the planned one.
- `status` is one of `draft`, `submitted`, `approved`, `ready_for_billing`.
- `note_enc` stores the free-text report note with AES-256-GCM. The AAD is
  `v<version>|<tenant>|<report>|note`, so a note cannot be moved to another
  report or tenant.
- `version` supports optimistic locking. Every update and every status
  transition names the version it read. A stale version is rejected with `409`
  and never overwrites a newer draft.
- `submitted_at`, `approved_at` and `approved_by` are set by the database clock
  when the matching transition happens.

Performed activities are linked in `report_activities`:

- A row references `activities(id)`, not a catalog definition. The report keeps
  the care that was actually done, and later catalog changes cannot rewrite it.
- `performed_at` is copied from the activity as a snapshot.
- A performed activity may belong to at most one report. The unique key
  `(tenant_id, activity_id)` prevents double reporting, which also removes one
  path to double billing.
- The service verifies that each activity belongs to the report's patient, has
  status `recorded`, and is not already attached to another report.

Status flow:

- `draft` → `submitted` by the report's employee or a chef. The report must
  contain at least one activity.
- `submitted` → `approved` by a chef only.
- `approved` → `ready_for_billing` by a chef only.
- `submitted` → `draft` is a **return**. It requires a reason.
- `approved` → `draft` is a **correction**. It requires a reason.
- After submission there is no silent change. `PATCH` on a non-draft report
  returns `409`, and the caller must return or correct the report first.

Returns and corrections are modeled separately from the report row in
`report_transitions`:

- `from_status`, `to_status` and `kind` (`return` or `correction`) describe the
  action.
- `reason_enc` is encrypted with AES-256-GCM. The AAD is
  `v<version>|<tenant>|<transition>|reason`.
- The reason is never copied into `audit_events`, because it may contain health
  data. The audit event stores the transition id, the statuses and the kind.
- The application role may only `SELECT` and `INSERT` transitions. They are
  append-only.

Access:

- A chef may read and write every report in the tenant.
- An employee may read a report for a patient they are currently assigned to.
  They may write or submit only their own report, meaning the report's
  `employee_id` is their user id.
- An unassigned employee gets `404` for reads so the report's existence is not
  revealed. A write gets `403`.
- `platform_admin` gets no automatic access (ADR-004 and the platform-admin
  reminder). Future break-glass access must be explicit, time-limited and
  audited.

Atomicity:

- The report, its activity rows, the status change and the audit event are
  written in one tenant transaction. A failure rolls back all of them.

## Reasons
- A separate report keeps the visit a scheduling record and the report a care
  record, as doc 12 and doc 13 require.
- Referencing performed activities preserves the historical care state, while
  catalog and contract changes continue to evolve independently.
- A version column is simpler than row locks, works across requests, and gives
  the client a clear `409` to resolve.
- Separate transition rows keep the report row small and make a return or
  correction a first-class event instead of an overwritten field.
- Encrypting the return reason treats it like the report note, because both may
  contain health data.
- A unique `(tenant_id, activity_id)` is a cheap database guard against the same
  performed activity being reported twice.

## Consequences
- Billing (doc 14) can use only `ready_for_billing` reports and their confirmed
  activities.
- The audit log has one event per report action. A full return history is
  available from `report_transitions` and the audit log.
- A returned report keeps its `submitted_at`; the next submission overwrites it.
  The transition table keeps the history.
- A corrected approved report loses its `approved_at` and `approved_by` until it
  is approved again.
- The client must send the current `version` on every write. A `409` means the
  client must reload the report.

## Risks
- The unique `(tenant_id, activity_id)` means an activity can never be moved to a
  corrected replacement report. The report must be edited instead.
- The report note and return reason are encrypted with one key version. Key
  rotation must re-encrypt both fields.
- The service uses the database clock for status times but the application clock
  is not involved. Tests compare presence and order, not fixed values.
- A report can only be created for a completed visit. If the business wants
  drafts during an in-progress visit, that rule must change.
- Employees can only write their own report. If a chef wants an employee to
  finish another employee's draft, the chef must do it or the report ownership
  must change explicitly.

## Open questions
- Should a correction after `ready_for_billing` be allowed, and if so, must the
  billing export be reversed first?
- Do reports need a frozen copy of the visit's planned and actual times for the
  billing archive, in addition to the visit id?
- Should a report require a signature or a second approval for certain legal
  areas?
- Should an activity be allowed to move to a new report after a correction, or
  is editing the existing report always the correct path?
