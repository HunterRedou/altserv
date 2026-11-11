# Retention policy

## Status

Draft for professional data-protection review. Retention periods depend on the
controller's legal and professional duties. This document gives a framework and
the technical facts; the controller must confirm the periods and record the
decision. It is not legal advice.

## Principles

1. Personal data is kept only as long as the purpose or a legal duty requires
   (Art. 5(1)(e) GDPR).
2. A statutory retention duty overrides a deletion request. The data is then
   restricted instead of deleted.
3. Retention periods start at the end of the calendar year in which the event
   occurred, unless a specific rule says otherwise.
4. Every period has an owner, a trigger and a deletion method.
5. Backups follow `backup-policy.md`. Deleting a live row does not instantly
   delete every backup copy.
6. Audit and billing records may need a longer period than care notes. Do not
   use one period for everything.

## Retention schedule

The "proposed" column is a starting point for the controller. It is not a legal
determination.

| Data set | Purpose | Proposed retention | Legal anchors to verify | Trigger |
| --- | --- | --- | --- | --- |
| Tenant contract and tax records | Contract, invoicing, tax | 6 or 10 years | § 147 AO, § 257 HGB, § 14b UStG | End of contract or end of year |
| Tenant contact data | Service relationship | Duration of contract plus the tax period | Art. 6(1)(b), § 147 AO | Contract end |
| User account and role | Employment or service relationship | Duration of the account plus the audit period | Art. 6(1)(b), Art. 6(1)(f) | Account closure |
| Session data | Authentication | Until expiry or revocation, then purge (a scheduled job is required) | Art. 6(1)(f), Art. 32 | Expiry or logout |
| Patient master data | Care and identification | Per the care documentation duty | § 630f BGB (10 years for a patient record), professional and state rules | End of care |
| Care documentation (visits, activities, reports, notes) | Care proof and continuity | Per the care documentation duty | § 630f BGB, SGB XI documentation duties, professional rules | End of care |
| Assignments | Access proof and care organisation | While needed plus the audit period | Art. 6(1)(f), Art. 32 | Revocation plus period |
| Billing runs and lines | Billing and accounting | 10 years | § 147 AO, § 257 HGB, payer rules | End of the billing year |
| Billing findings | Billing proof | Same as the billing run | § 147 AO, § 257 HGB | End of the billing year |
| Audit events | Security and accountability | To be agreed; at least 12 months, longer for incidents | Art. 5(2), Art. 32; no single statutory period | Rolling or case-based |
| Application logs | Operations and security | 30 to 90 days, unless an incident needs longer | Art. 6(1)(f), Art. 32 | Rolling |
| Metrics | Availability and capacity | 15 days to 12 months, aggregate only | Art. 6(1)(f) | Rolling |
| Backups | Recovery | 14 to 35 days for dumps; PITR window per product | Art. 32(1)(c) | Rolling |
| Support tickets | Support and evidence | Per the operator's policy, no health data by default | Art. 6(1)(b), Art. 6(1)(f) | Ticket closure plus period |
| Search tokens | Patient search | Same as the patient master data | Art. 6(1)(b) | Patient deletion or name change |

The care documentation period is the most sensitive item. § 630f(3) BGB
requires a patient record to be kept for ten years after the end of the
treatment relationship, unless another law requires longer. Nursing
documentation and SGB XI duties can add or change the period. The controller
must confirm the applicable rule with counsel and with the professional
supervision authority.

## What the application currently does

| Capability | Status |
| --- | --- |
| Patient status `inactive` and `archived` | Implemented. It is not a deletion. |
| Patient delete API | Not implemented. |
| Tenant delete | Implemented as a database query, but it fails once the tenant has audit events because the append-only trigger blocks the cascade. It is used only when the USt-IdNr check fails, before care data exists, and it is not exposed as an API. |
| Session expiry | Implemented. The `DeleteExpiredSessions` query exists but is not scheduled. |
| Automated retention job | Not implemented. |
| Audit log deletion | Blocked by append-only triggers. |
| Backup expiry | Depends on the backup product and the operator. |
| Deletion evidence | Not automated. The operator must keep a record. |

This means retention is currently a manual, documented procedure. The
application does not enforce it. A production launch must not rely on a period
that nobody executes.

## Legal hold

If litigation, an audit or an investigation is likely, the affected data must be
preserved even if its normal period has ended. A legal hold:

- is declared in writing by the controller or the operator
- names the tenants, patients or time range
- suspends deletion for that scope
- is reviewed at least every six months
- is lifted in writing

The hold must not be used to keep data indefinitely without a reason.

## Deletion triggers

| Trigger | Action |
| --- | --- |
| End of the care relationship | Start the care documentation period. Do not delete immediately. |
| End of the retention period | Delete or anonymise the data set, unless a legal hold applies. |
| Patient requests erasure | Check Art. 17(3) exceptions. If a retention duty applies, restrict instead of delete and inform the data subject. |
| Employee leaves | Disable the account, revoke sessions, keep the audit trail and any care records the employee authored. |
| Tenant leaves | Return the data in a usable format, then delete it after the agreed period. Confirm in writing. |
| Failed USt-IdNr verification | The current code deletes the tenant and all dependent rows. Review this against the contract and retention duties before production. |
| Backup expiry | The backup product deletes old copies. Confirm that the period matches this policy. |

## Deletion in backups

A row deleted from the live database may still exist in a backup. The controller
and the operator must agree:

- the maximum backup retention
- whether a restore would reintroduce deleted data
- how a deletion request is handled during the backup window
- how a restored copy is cleaned before it is used in production

For a small installation with daily dumps and a 14-day retention, a deleted row
can survive up to 14 days in a backup. Document this in the privacy notice.

## Audit of retention

- Keep a retention schedule with owner and last review date.
- Record each deletion run: date, scope, row counts, operator, reason.
- Do not record patient names in the deletion log.
- Review the schedule at least annually and after a legal change.
- Test one deletion in a staging copy before running it in production.

## Open questions

- Which care documentation period applies to the first tenants?
- Is § 630f BGB applicable to the tenant's documentation, or does a
  professional or SGB XI rule set a different period?
- How long must audit events be kept, and is an external sink needed?
- Should expired sessions be purged by a scheduled job? (Recommended.)
- Should archived patients be excluded from search and lists by default?
- What is the backup retention, and how is a deletion request handled inside
  that window?
- Who signs off a legal hold, and who reviews it?
