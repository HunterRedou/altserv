# Billing requirements

## Status

Draft for professional billing and data-protection review. Billing rules,
service catalogs, payer procedures and legal bases must be verified by a
qualified billing expert before production. This document is not legal, tax or
billing advice.

## Purpose

Describe how altserv prepares billing data, which legal and technical
requirements apply, and what must be reviewed before a tenant uses the output
for a real claim.

## Scope

- service catalogs and service definitions
- tenant service contracts
- performed activities
- approved care reports
- billing runs, lines, findings and rules
- CSV and PDF exports
- the future electronic transmission boundary

## Legal framework to verify

| Area | Relevance |
| --- | --- |
| SGB XI | Long-term care insurance; § 105 SGB XI for the electronic data exchange with care funds. |
| SGB V | Statutory health insurance; § 302 SGB V for the electronic data exchange with health funds. |
| SGB X | Social administration procedure and data protection in social law. |
| § 147 AO, § 257 HGB | Retention of accounting and business records. |
| GoBD | Proper bookkeeping and IT systems, traceability, completeness, immutability. |
| UStG | VAT treatment, if the service is taxable. |
| GDPR and BDSG | Health data, confidentiality, access control, retention, deletion. |
| State and professional rules | Care documentation and billing duties in Brandenburg and the tenant's professional context. |

The exact procedure, data format, version and transmission channel for § 105
SGB XI and § 302 SGB V change over time. They must be taken from the current
technical rules of the payers, not from this document.

## Roles

| Role | Responsibility |
| --- | --- |
| Tenant (`chef`) | Owns the service contract, the care documentation, the billing run, the approval and the submission to the payer. |
| altserv operator | Provides the deterministic assistant and the export. Does not become the biller and does not submit claims. |
| Billing expert | Verifies service codes, rules, contracts, legal area and payer requirements. |
| Data protection lead | Reviews the health-data processing, access and retention. |

The tenant remains responsible for the correctness of a claim. The software is
an assistant, not a certified billing centre and not a substitute for human
review.

## Data used

| Data | Source | Classification |
| --- | --- | --- |
| Service catalog and definitions | Global read-only catalog | L1 |
| Tenant contract, payer, legal area, validity | `tenant_service_contracts` | L2/L3 |
| Performed activity, time, duration, service snapshot | `activities` | L3 |
| Report, status, version, approval | `reports` | L3 |
| Billing rule | `billing_rules` | L2 |
| Billing run, line, finding | `billing_runs`, `billing_lines`, `billing_findings` | L3 |
| Actor and audit event | `audit_events` | L2 |

The export contains the report id, service code, service name, quantity,
performed date, payer, contract reference and legal area. It does not contain
the patient name. It is still L3 because the tenant can link the report id to a
patient.

## Workflow

1. The tenant records a service catalog and contract with a verified legal area
   (`sgb_xi` or `sgb_v`) and validity.
2. An employee records an activity for an assigned patient, with a service
   definition, time and duration.
3. A visit is completed and a report is created, submitted and approved.
4. The report is marked `ready_for_billing`.
5. A `chef` generates a billing run. The engine checks:
   - report status
   - service and contract validity
   - legal-area match
   - duplicate activity
   - cross-run double billing
   - repeated service information
   - tenant rules: exclusion, prerequisite, combination
6. Errors block approval. Warnings and information do not block, but must be
   reviewed.
7. A `chef` approves the run with the current version.
8. A `chef` exports CSV or PDF for internal use.
9. The tenant submits the claim through the payer's verified procedure. The
   application does not transmit it.

## Approval and change control

- A billing run has a `draft`, `approved` and `voided` status.
- Generate, approve and void use optimistic locking. A stale version is
  rejected.
- An approved run must be voided before it can be regenerated.
- Voiding deletes the lines and findings so the activities can be billed again.
- An approved run cannot be silently changed.
- A report correction that would break an approved run is rejected with a
  conflict. Void the run first.
- Every generate, approve, void, export and rule creation is audited.

These controls support traceability and completeness. They do not replace the
tenant's own four-eyes review.

## Export and transmission

| Channel | State | Rule |
| --- | --- | --- |
| CSV | Implemented | Internal only. Semicolon-separated, formula-safe, ids and billing data. |
| PDF | Implemented | Internal only. Built-in font, no external PDF service. |
| Electronic transmission | Boundary only | Production adapter is `unconfigured`: the API answers 503 and no claim is sent. |
| Synthetic test payer | Development only | `BILLING_TRANSMISSION_ADAPTER=test`, rejected unless `PLATFORM=dev`. In-process, no network. |
| Email | Not allowed | Plain email is not acceptable for health data. |

### Transmission boundary

The boundary is described in ADR-026
(`../decisions/ADR-026-billing-transmission-boundary.md`). It provides:

- `POST /api/billing-runs/{id}/transmit` and
  `GET /api/billing-runs/{id}/transmissions`, `chef` only
- an idempotency key per submission; a replay returns the earlier attempt
- at most one pending or accepted attempt per run, enforced by a unique index
- a recorded, retryable attempt for a rejection or an adapter failure
- one legal area per submission; a run with mixed legal areas is rejected
- no void of a run with a pending or accepted attempt
- an append-only `billing_transmissions` table with RLS and no `DELETE` grant
- a SHA-256 fingerprint of the submitted lines instead of a payload copy
- audit events without payer messages, procedure text, names or free text

No § 105 or § 302 format is implemented. A real adapter needs the items in the
review list below and a recorded payer test.

A future transmission must:

- use the current § 105 SGB XI or § 302 SGB V procedure and format
- use an authenticated and encrypted channel
- be idempotent and traceable
- record the submission and the response
- handle rejections and corrections
- be tested with the payer before production
- be covered by the Art. 28 agreement and the DPIA

Do not add a "send by email" shortcut. Do not paste a billing file into a
support ticket.

## Retention and archiving

- Billing runs, lines and findings are accounting records. Keep them for the
  statutory period, normally 10 years under § 147 AO and § 257 HGB.
- The export is a copy. The tenant must archive the submitted claim and the
  payer's response in a way that satisfies GoBD.
- Do not delete an approved run to "clean up". Void it and keep the history.
- A correction must be traceable. Keep the original and the corrected version.
- The audit trail must show who generated, approved, voided and exported.

## Data protection

- Billing data is health data when it can be linked to a person. Apply the L3
  rules in `data-classification.md`.
- Access is `chef` only. An `employee` cannot read or export billing data.
- The export is generated on demand and is not stored by the application.
  After download it is the tenant's responsibility.
- The application does not send billing data to a third party.
- The export must not be used as test data. Use synthetic data.
- If a payer portal or a clearing house is added, review it as a processor and
  document the transfer.

## Technical controls

- Deterministic rule engine, no generative AI.
- Global read-only service catalog; tenant-bound contracts, activities and
  rules.
- Composite tenant foreign keys and row-level security.
- Unique `(tenant_id, activity_id)` on billing lines, so an activity is billed
  at most once.
- `ON DELETE RESTRICT` on billing-line references, so a report correction
  cannot silently orphan a line.
- Optimistic locking on generate, approve and void.
- Audit of every state change and export.
- CSV formula injection protection.
- PDF uses a built-in font and no external service.
- No production transmission path. The transmission boundary is idempotent,
  audited and append-only; only the synthetic test payer is wired, and only in
  development.

## Professional review before production

The following must be signed off by a qualified billing expert and, where
relevant, by a data protection lawyer:

- [ ] Service catalogs and definitions are real, current and correctly coded.
- [ ] No invented Brandenburg service codes or seed data.
- [ ] Each tenant contract names the correct payer, legal area and validity.
- [ ] The § 105 SGB XI procedure and format are confirmed for the tenant.
- [ ] The § 302 SGB V procedure and format are confirmed for the tenant.
- [ ] The rule set matches the payer's current rules.
- [ ] The approval workflow matches the tenant's internal control.
- [ ] The export is sufficient for the payer, or a verified transmission is
      implemented.
- [ ] A real transmission adapter is idempotent on the idempotency key, runs
      through an outbox, and has a recorded payer test.
- [ ] The payer's response messages are checked for personal data before they
      are stored in `billing_transmissions.response_message`.
- [ ] Retention and archiving satisfy GoBD and the payer.
- [ ] The data protection review and the DPIA cover billing.
- [ ] Staff are trained that the assistant does not replace human review.

## Open questions

- Which payers and procedures will the first tenants use?
- Is a clearing house required, and is it a processor?
- Which service catalog version is binding for each contract?
- Who performs the four-eyes approval in a small tenant?
- How are rejections and retroactive corrections handled?
- Is the PDF acceptable to the payer, or is a structured format required?
- Which VAT treatment applies to the tenant's services?
