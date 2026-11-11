# Deletion policy

## Status

Draft for professional data-protection review. This document describes how
deletion is supposed to work and where the current implementation has gaps. It
is not legal advice.

## Principles

1. Deletion is a controlled process, not an ad-hoc SQL statement.
2. A deletion request is checked against Art. 17(3) GDPR exceptions and the
   retention schedule before anything is removed.
3. If a retention duty applies, data is restricted, not deleted.
4. Deletion is recorded without copying the deleted personal data into the log.
5. Backups are handled separately. A restored backup must not silently
   reintroduce data that was lawfully deleted.
6. A tenant termination is a project with a checklist, not a single click.

## Legal basis

- Art. 17 GDPR (right to erasure)
- Art. 5(1)(e) GDPR (storage limitation)
- Art. 5(1)(f) and Art. 32 GDPR (integrity and confidentiality)
- § 35 BDSG and the applicable professional and tax retention rules
- Art. 28(3)(g) GDPR (return or deletion at the end of the processing)

## What can be deleted, and how

| Data set | Delete path | Current state | Notes |
| --- | --- | --- | --- |
| Session | Expiry or logout | Logout revokes the session. `DeleteExpiredSessions` exists but is not scheduled. | Add a scheduled purge. |
| Patient | No API | Not implemented | Requires a controlled procedure. See below. |
| Patient search tokens | Cascades with the patient | Implemented via FK | No separate action needed. |
| Assignment | Revoke, not delete | Implemented | Revocation keeps the history. |
| Activity | No delete API | Not implemented | A `voided` status exists. It is not a deletion. |
| Visit | No delete API | Not implemented | Cancel keeps the record. |
| Report | No delete API | Not implemented | Corrections keep the transition history. |
| Billing run | Void | Implemented | Voiding deletes lines and findings but keeps the run row. |
| Billing line and finding | Cascades from a voided run | Implemented | Activities become billable again. |
| Audit event | Not deletable | Append-only triggers and revoked privileges | Tenant deletion cascades audit rows. |
| User | No delete API | Not implemented | Disable and revoke sessions instead. Records reference the user with `RESTRICT`. |
| Tenant | Database query only | `DeleteTenant` exists | Not exposed as an API. Used on a failed USt-IdNr check. Fails once the tenant has audit events. |
| Backup | Backup product expiry | Operator procedure | See `backup-policy.md`. |

## Patient deletion procedure

The application has no patient delete endpoint. A deletion must be a reviewed
database procedure with a backup and a recorded approval. Because of the foreign
key graph, the order matters.

### Before deleting

1. Confirm the legal basis and check for a retention duty.
2. Confirm there is no legal hold.
3. Export the data needed for the response to the data subject, if any.
4. Take a verified backup.
5. Record the approval: who, when, which patient id, which reason.
6. Work in a transaction and verify the row counts before commit.

### Dependency order

The schema uses `ON DELETE CASCADE` for most child rows, but several edges are
`ON DELETE RESTRICT`:

- `report_activities.activity_id` -> `activities`
- `billing_lines.activity_id` -> `activities`
- `billing_lines.report_activity_id` -> `report_activities`
- `reports.employee_id`, `reports.created_by`, `reports.approved_by` -> `users`
- `visits.employee_id`, `visits.actual_employee_id`, `visits.created_by` -> `users`
- `activities.created_by` -> `users`
- `billing_runs.generated_by`, `approved_by`, `voided_by` -> `users`

A patient with an approved billing run therefore cannot simply be deleted. A
safe order is:

1. Void or remove the billing runs for the patient's reports, so billing lines
   no longer reference report activities.
2. Remove or correct the reports and their activities.
3. Remove the visits.
4. Remove the activities.
5. Remove the assignments and search tokens.
6. Delete the patient row.
7. Verify that no orphan rows remain.

Do not disable the foreign keys. Do not truncate a table. Do not use
`session_replication_role = replica` to skip triggers.

### After deleting

- Confirm that the patient id returns no rows in every table.
- Confirm that the search index no longer returns the patient.
- Record the row counts and the operator, but not the patient name.
- Update the deletion register.

## Tenant termination

Tenant termination is more than `DELETE FROM tenants`. The current `DeleteTenant`
query relies on cascades. Because some edges are `RESTRICT`, a tenant with
reports, visits, activities or billing runs may fail to delete, or may delete in
an unexpected order.

There is a second blocker: `audit_events` has a `BEFORE DELETE` trigger that
raises `audit_events is append-only`. The tenant foreign key is
`ON DELETE CASCADE`, so the cascade tries to delete the audit rows and the
trigger aborts the whole statement. A tenant with any audit event therefore
cannot be deleted with `DELETE FROM tenants`. This was confirmed on a scratch
database.

A controlled termination must:

1. Return the tenant's data in a usable format, as agreed in the contract.
2. Confirm the end date and the retention period in writing.
3. Revoke all sessions and disable all users.
4. Stop new writes.
5. Decide what happens to the audit trail. Export it if the contract or the
   retention policy requires it. Do not disable the append-only trigger
   casually.
6. Delete in dependency order, or use a reviewed procedure that is tested on a
   staging copy with the same schema.
7. Verify that no tenant row, user, patient or care record remains.
8. Confirm the deletion in writing to the tenant.
9. Record the deletion without the personal data.

The current code path that deletes a tenant after a failed USt-IdNr check is
acceptable only while the tenant has no care data. It must not be used to
terminate an active tenant.

## Employee offboarding

Do not delete a user who authored or approved care records. The foreign keys use
`RESTRICT` on purpose. Instead:

1. Revoke all sessions.
2. Disable the account (set a locked password or an explicit disabled state, if
   available; otherwise remove the ability to log in by rotating the password
   hash to an unusable value).
3. Remove or expire the patient assignments.
4. Keep the audit trail and the authored care records.
5. Delete the account only after the retention period and only if no record
   references it.

There is no dedicated "disable user" API yet. This is a gap. A production
process needs one.

## Restriction instead of deletion

When a retention duty or a legal hold applies, restrict the data:

- set the patient status to `inactive` or `archived`
- remove assignments so employees can no longer reach the patient
- keep the record for the required period
- document the restriction and its reason
- review the restriction at least annually

`archived` is a status, not an access control. The employee list and employee
search exclude archived patients, but the `chef` list and `chef` search include
them by design. A chef can still open an archived patient. If a restriction
must hide the record from everyone, remove the assignments and confirm that the
read path denies access, or add an explicit restriction rule. Do not assume
that `archived` alone hides the data.

## Backups

Deleting a live row does not delete it from an existing backup. The operator
must document:

- the maximum backup retention
- the fact that a restore can reintroduce deleted data
- how a restored copy is cleaned before production use
- how a deletion request is handled during the backup window

For a small installation with 14-day dumps, a deleted row can survive up to 14
days in a backup. State this in the privacy notice and in the deletion response.

## Audit log

Audit events are append-only. They are not deleted to satisfy an erasure
request, because they are the evidence that the processing was lawful and
secure. Their own retention period must be defined. Tenant deletion cascades
the tenant's audit rows, which is a conflict with the idea of an independent
audit trail. Use an external append-only sink if audit evidence must survive a
tenant termination.

## Verification

A deletion is complete only when:

- the target rows are gone from the live database
- the search index no longer returns the subject
- the API returns "not found" for the subject
- the deletion is recorded in the register
- the backup and restore implications are documented
- the data subject or the controller receives a written response

## Gaps

- No patient delete API or guided deletion procedure.
- No scheduled session purge.
- No "disable user" API.
- Tenant deletion relies on cascades and may fail because of `RESTRICT` edges
  and the append-only audit trigger.
- No automated deletion register.
- No automated way to delete a subject from backups.
- Archived patients remain visible to `chef`; `archived` is not a restriction
  control.

## Open questions

- Who approves a patient deletion, and who executes it?
- Which retention duty applies to the first tenants?
- Should the application expose a guided deletion workflow with a four-eyes
  check?
- Should audit events be exported before a tenant is deleted?
- How is a deletion request handled when the subject is also in a backup?
