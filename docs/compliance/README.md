# Compliance documentation

## Status

Draft set for professional data-protection and billing review. These documents
describe the current design and the decisions that must be made before
production. They are not legal advice. Do not treat a checked box in this
folder as a legal approval.

## Documents

| File | Purpose | Primary owner |
| --- | --- | --- |
| `data-classification.md` | Classification levels and handling rules for every data set. | Data protection lead |
| `processing-activities.md` | Art. 30 record, role model, purposes, recipients, rights. | Controller and operator |
| `privacy-impact-assessment.md` | DPIA screening, risks, measures, residual risk. | Controller |
| `retention-policy.md` | Retention schedule, legal holds, current gaps. | Controller |
| `deletion-policy.md` | Lawful deletion, dependency order, tenant termination, backups. | Controller and operator |
| `access-control-policy.md` | Roles, tenant isolation, assignment access, platform admin, auth. | Operator |
| `incident-response.md` | Personal data breach assessment, 72-hour path, notification. | Data protection lead |
| `backup-policy.md` | Backup types, encryption, retention, restore drills, RPO/RTO. | Operator |
| `billing-requirements.md` | Billing law, workflow, approval, export, transmission, review. | Billing expert and tenant |

Review documents:

| File | Purpose |
| --- | --- |
| `review-packet.md` | Focused decisions for the data-protection lawyer, billing expert and security lead. |
| `review-record.md` | Signed decision log and gate status. Blank until the review is complete. |
| `REVIEW-REMINDER.txt` | Short reminder that the professional review is still open. |

Technical procedures are separate:

- `../runbooks/incident-response.md`
- `../runbooks/database-recovery.md`
- `../runbooks/key-rotation.md`
- `../runbooks/deployment.md`
- `../runbooks/load-testing.md`
- `../runbooks/slow-queries.md`
- `../platform-admin-patient-access-reminder.txt`

Design decisions are in `../decisions/`. The most relevant are:

- ADR-004: shared database tenants
- ADR-006: session authentication
- ADR-007: patient encryption
- ADR-008 and ADR-018: audit logging
- ADR-010: row-level security
- ADR-011: assignment authorization
- ADR-012: protected patient search
- ADR-017: billing assistant
- ADR-019: deployment and observability
- ADR-020: authentication hardening
- ADR-021: break-glass access

## Role summary

- The **tenant** (nursing service) is the controller for patient and employee
  data.
- The **altserv operator** is the processor for tenant data and the controller
  for its own contract and account data.
- **Subprocessors** need an Art. 28 agreement and a place in the processing
  record.
- **`platform_admin`** has no automatic patient access. A named patient can be
  read only under a tenant-approved, time-limited, read-only break-glass grant.
  Every step is audited.

The exact allocation, including possible joint controllership, must be fixed in
the contract.

## Required before production

### Data protection

- [ ] Controller and processor identified, with names and contacts.
- [ ] Art. 28 agreement signed with every tenant and subprocessor.
- [ ] Subprocessor list complete, with locations and transfer basis.
- [ ] DPIA decision recorded and signed. Consult the LDA Brandenburg if the
      residual risk is high.
- [ ] Legal bases confirmed, including Art. 9(2) for health data.
- [ ] Retention periods confirmed and an execution procedure agreed.
- [ ] Deletion procedure tested on a staging copy.
- [ ] Data subject rights procedure tested.
- [ ] Breach response plan tested with a tabletop exercise.
- [ ] Privacy notice for patients and employees reviewed.
- [ ] Hosting and backup locations confirmed as EU/EEA, or a transfer
      assessment is done.
- [ ] Works council involved where employee data is processed.

### Security

- [x] MFA for `chef` and `platform_admin` is implemented. The operator must
      still enrol every privileged account and store recovery codes safely.
- [x] Password reset and user-disable actions are implemented.
- [x] Session purge is scheduled at startup and on an interval.
- [x] Break-glass access for `platform_admin` is patient-scoped, approved by a
      tenant `chef`, time-limited, read-only and audited. The operator must
      still define the support verification process.
- [ ] Production secrets in a secret manager, not in `.env` on a server.
- [ ] TLS termination and HSTS verified.
- [ ] Backup encryption and restore drill verified.
- [ ] PITR verified.
- [ ] `/metrics` protected with `METRICS_TOKEN`.
- [ ] Alert receiver configured and tested.
- [ ] External append-only audit sink decided.

### Billing

- [ ] Service catalogs and definitions verified by a billing expert.
- [ ] No invented service codes or seed data.
- [ ] § 105 SGB XI and § 302 SGB V procedures confirmed for the first tenants.
- [ ] Rule set matches the payer's current rules.
- [ ] Approval workflow matches the tenant's internal control.
- [ ] Retention and archiving satisfy GoBD and the payer.
- [ ] The export is accepted by the payer, or a verified transmission is
      implemented.

## Known gaps

These are real gaps in the current implementation, not open design questions:

1. No tenant-wide break-glass grant and no support UI. One grant covers one
   named patient.
2. No email delivery for password reset or MFA changes. Reset tokens are handed
   over out of band.
3. No automated retention or deletion job, and no patient delete API.
4. Tenant deletion relies on cascades and may fail because of `RESTRICT` edges
   and the append-only audit trigger.
5. No external append-only audit sink. A superuser can disable the triggers.
6. No automated PITR drill and no shipped PITR product.
7. Billing transmission is deliberately `not_configured`.
8. No one-click DSAR export.
9. No shared rate-limit store for multi-instance deployments.
10. The Compose file is a template, not a production orchestrator.

## Document control

| Field | Value |
| --- | --- |
| Version | 0.1 draft |
| Last updated | 2026-11-11 |
| Next review | Before the first production tenant |
| Review trigger | New data category, role, integration, subprocessor, hosting region, legal change or breach |
| Approval | Data protection lead, legal counsel, billing expert, management |
| Review packet | `review-packet.md` |
| Review record | `review-record.md` |

## Review record

| Date | Reviewer | Area | Result | Follow-up |
| --- | --- | --- | --- | --- |
| | | Data protection | | |
| | | Legal | | |
| | | Billing | | |
| | | Security | | |
