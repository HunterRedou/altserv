# Record of processing activities (Art. 30 GDPR)

## Status

Draft for professional data-protection review. This is a template that the
controller and the operator must complete, confirm and sign. It is not legal
advice. A record under Art. 30 GDPR must be in writing, kept up to date and
made available to the supervisory authority on request.

## Role model

| Party | Role | Notes |
| --- | --- | --- |
| Nursing service (tenant) | Controller for patient and employee data | Decides the purpose and essential means of care documentation and billing. |
| altserv operator | Processor for tenant data | Processes only on documented instructions under an Art. 28 GDPR agreement. |
| altserv operator | Controller for its own contract and account data | Tenant contact data, invoicing, support records, security logs. |
| Hosting, backup, monitoring providers | Subprocessors | Need a contract and a documented instruction. Must be listed and approved. |
| Platform administrator | Natural person acting for the operator | Support role without automatic patient access. Stored with a `tenant_id`, so platform staff must live in an operator-only tenant that holds no patient data. |

If the operator uses tenant data for its own purposes (for example product
analytics on patient data, model training or marketing), it becomes a
controller or joint controller for that processing. That is not allowed under
the current design and would require a new legal review. The current design
forbids it.

The contract must state:

- the subject matter and duration of the processing
- the nature and purpose
- the type of personal data and the categories of data subjects
- the controller's instructions, including transfers to third countries
- confidentiality of the persons authorised to process
- Art. 32 security measures
- the conditions for engaging subprocessors (Art. 28(2) and 28(4))
- support for data subject rights (Art. 28(3)(e))
- support for Art. 32 to Art. 36 duties (Art. 28(3)(f))
- deletion or return of data at the end of the contract (Art. 28(3)(g))
- audit and information rights of the controller
- liability and indemnity
- the role of `platform_admin` and any break-glass access

### Art. 28 agreement checklist

- [ ] The processor acts only on documented instructions, including transfers.
- [ ] The persons authorised to process are bound by confidentiality.
- [ ] Art. 32 security measures are described and kept current.
- [ ] Subprocessors need prior authorisation and the same obligations.
- [ ] The processor supports data subject rights.
- [ ] The processor supports Art. 32 to Art. 36 duties.
- [ ] Data is deleted or returned at the end of the contract.
- [ ] The controller has audit and information rights.
- [ ] The processor notifies a breach without undue delay.
- [ ] The processor names a security contact and a deputy.
- [ ] The agreement covers the `platform_admin` role and break-glass access.
- [ ] The agreement lists every subprocessor and every location.
- [ ] The agreement states the liability and indemnity rules.

## Processing activities

### PA-01 Tenant onboarding and contract management

| Field | Value |
| --- | --- |
| Purpose | Create and administer a tenant, verify the USt-IdNr, manage the contract. |
| Controller | altserv operator |
| Data subjects | Tenant contact persons, sole traders |
| Data categories | Company name, email, address, USt-IdNr, status, contract data (L2) |
| Legal basis | Art. 6(1)(b) GDPR (contract), Art. 6(1)(c) (tax and commercial law) |
| Recipients | Tax adviser, payment provider, VIES for USt-IdNr verification |
| Third-country transfer | None intended. VIES is an EU service. Confirm. |
| Retention | Contract and tax records per § 147 AO and § 257 HGB; see `retention-policy.md`. |
| Security | TLS, role-restricted API, RLS, audit. |

Note: if the USt-IdNr check fails, the current code deletes the tenant row and
all dependent rows. That works for a fresh pending tenant with no audit events.
It fails once the tenant has audit rows, because the append-only trigger blocks
the cascade. The deletion of account data must be reviewed against the contract
and retention duties. A grace period or a manual review may be required.

### PA-02 User administration and authentication

| Field | Value |
| --- | --- |
| Purpose | Create accounts, assign roles, log in and out, protect sessions. |
| Controller | Tenant for its users; operator for platform staff |
| Data subjects | Employees of the tenant, platform staff |
| Data categories | Name, email, password hash, role, tenant, session token hash, CSRF token, timestamps (L2/L4) |
| Legal basis | Art. 6(1)(b) (employment or service contract), Art. 6(1)(f) (security) |
| Recipients | None beyond the hosting provider |
| Third-country transfer | None intended |
| Retention | While the account exists; sessions until expiry or revocation; see `retention-policy.md`. |
| Security | Argon2id password hash, HttpOnly session cookie, CSRF protection, login rate limit, TLS, audit of role changes. |

### PA-03 Patient master data

| Field | Value |
| --- | --- |
| Purpose | Identify the care recipient and link care documentation. |
| Controller | Tenant |
| Data subjects | Patients / care recipients |
| Data categories | First name, last name, birth date, status (L3) |
| Legal basis | Art. 6(1)(b) or Art. 6(1)(c); Art. 9(2)(h) with § 22 BDSG for health data. Confirm with counsel. |
| Recipients | None beyond the hosting and backup providers |
| Third-country transfer | None intended |
| Retention | Per the tenant's care documentation duty; see `retention-policy.md`. |
| Security | AES-256-GCM for name and birth date, TLS, RLS, assignment check, audit of reads and writes. |

### PA-04 Assignments and access control

| Field | Value |
| --- | --- |
| Purpose | Decide which employee may see which patient, and when. |
| Controller | Tenant |
| Data subjects | Patients, employees |
| Data categories | Patient id, employee id, assignment type, validity, revocation, actor (L3) |
| Legal basis | Art. 6(1)(b), Art. 9(2)(h) with § 22 BDSG; Art. 32 security duty |
| Recipients | None |
| Third-country transfer | None intended |
| Retention | While the assignment is needed for care and for the audit trail; see `retention-policy.md`. |
| Security | Chef-only management, `clock_timestamp()` validity check, revocation, audit of creation and revocation. |

### PA-05 Visit planning and care documentation

| Field | Value |
| --- | --- |
| Purpose | Plan visits, record what happened, document the care. |
| Controller | Tenant |
| Data subjects | Patients, employees |
| Data categories | Planned and actual employee, times, status, activities, service codes, care notes, report content, correction reasons (L3) |
| Legal basis | Art. 6(1)(b) or Art. 6(1)(c); Art. 9(2)(h) with § 22 BDSG; professional documentation duties |
| Recipients | None beyond the hosting and backup providers |
| Third-country transfer | None intended |
| Retention | Per the tenant's care documentation duty and statutory retention; see `retention-policy.md`. |
| Security | AES-256-GCM for notes and reasons, TLS, RLS, assignment check, status workflow, optimistic locking, audit. |

### PA-06 Billing preparation and export

| Field | Value |
| --- | --- |
| Purpose | Turn approved care reports into internal billing runs, check rules and export CSV or PDF. |
| Controller | Tenant |
| Data subjects | Patients, employees |
| Data categories | Report, service code, quantity, performed time, payer, contract reference, billing status, findings (L3) |
| Legal basis | Art. 6(1)(b) or Art. 6(1)(c); Art. 9(2)(h) with § 22 BDSG. The payer procedure is governed by § 105 SGB XI or § 302 SGB V, not by GDPR. |
| Recipients | The tenant and its billing office. No automatic transmission is configured. |
| Third-country transfer | None intended |
| Retention | Per § 147 AO and § 257 HGB, and the payer's requirements; see `retention-policy.md`. |
| Security | Chef-only, deterministic rules, approval with optimistic locking, audit of generate, approve, void and export. |

The application does not transmit billing data to a payer. The export is a file
that the tenant handles. See `billing-requirements.md`.

### PA-07 Audit and security logging

| Field | Value |
| --- | --- |
| Purpose | Prove who accessed or changed what, detect misuse and support incident response. |
| Controller | Operator for platform security; tenant for tenant-level accountability |
| Data subjects | Employees, patients (via entity ids), platform staff |
| Data categories | Actor user id, action, entity type and id, timestamp, request id, metadata (L2; no health data) |
| Legal basis | Art. 6(1)(f) (security), Art. 32; Art. 5(2) accountability |
| Recipients | None by default. An external append-only sink may be added. |
| Third-country transfer | None intended |
| Retention | To be agreed; at least the security review period. See `retention-policy.md`. |
| Security | Append-only triggers, revoke of update, delete and truncate, RLS, chef-only read API, no health data in metadata. |

### PA-08 Operations, monitoring and incident response

| Field | Value |
| --- | --- |
| Purpose | Keep the service available, detect failures, investigate incidents. |
| Controller | Operator |
| Data subjects | None directly. Logs may contain user or patient ids. |
| Data categories | Request method, route pattern, status, duration, pool statistics, slow-query count, failed-login count, job errors, backup age (L1/L2) |
| Legal basis | Art. 6(1)(f) (security and availability), Art. 32 |
| Recipients | Monitoring provider, if used. Must be a processor and must not receive health data. |
| Third-country transfer | Only with a documented review and safeguards |
| Retention | Monitoring retention per `retention-policy.md`; logs no longer than needed |
| Security | No query string, body, patient name, tenant id or user id in metrics; route patterns only. |

### PA-09 Backup and recovery

| Field | Value |
| --- | --- |
| Purpose | Restore the service after data loss or corruption. |
| Controller | Operator as processor for tenant data |
| Data subjects | All data subjects in the backed-up database |
| Data categories | Full database content, including L3 and L4-adjacent data (L3) |
| Legal basis | Art. 6(1)(f), Art. 32(1)(c) (availability and resilience) |
| Recipients | Backup provider, if used |
| Third-country transfer | Only with a documented review and safeguards |
| Retention | Per `backup-policy.md` and `retention-policy.md` |
| Security | Encrypted at rest, access-controlled, restore drills, backup-age alert. |

### PA-10 Support access

| Field | Value |
| --- | --- |
| Purpose | Help a tenant with a technical problem. |
| Controller | Tenant for the data; operator acts on instruction |
| Data subjects | Patients and employees of the requesting tenant |
| Data categories | Whatever the support case needs. Default is no patient data. |
| Legal basis | Art. 6(1)(b) or Art. 6(1)(f); Art. 9(2)(h) with § 22 BDSG if health data is involved |
| Recipients | Support staff |
| Third-country transfer | None intended |
| Retention | Support ticket per the operator's retention policy |
| Security | `platform_admin` has no automatic patient access. Break-glass access is patient-scoped, approved by a tenant `chef`, time-limited, read-only and audited. |

## Recipients and subprocessors

The operator must keep a list of subprocessors with:

- name and address
- service and data categories
- location and any third-country transfer
- contract status (Art. 28)
- security evidence
- notification procedure for changes

Likely categories are hosting, managed PostgreSQL, object storage for backups,
monitoring and email. The list must be real. Do not name a provider that has
not been contracted.

## Third-country transfers

The default design assumes hosting and backups in the EU/EEA. Any transfer to a
third country needs:

- a legal basis under Chapter V GDPR
- an adequacy decision or standard contractual clauses
- a transfer impact assessment
- a documented instruction from the controller
- a note in this record

## Data subject rights

| Right | Support in altserv | Gap |
| --- | --- | --- |
| Access (Art. 15) | Tenant users can read patient, visit, report and billing data through the API. | No one-click DSAR export. The operator must support a manual extract. |
| Rectification (Art. 16) | Patient, visit, report and billing corrections exist. Report corrections are audited. | A corrected report keeps its transition history by design. |
| Erasure (Art. 17) | No patient delete API. Tenant deletion cascades. | A controlled deletion procedure is required. See `deletion-policy.md`. |
| Restriction (Art. 18) | Patient status can be set to `inactive` or `archived`. | No formal restriction flag or processing lock. |
| Portability (Art. 20) | Billing CSV export exists. | No structured export of all patient data. |
| Objection (Art. 21) | Not applicable to the core care contract in the same way; assess per case. | Document the decision. |

The controller answers the request. The operator supports it and does not
answer for the controller.

## Review and sign-off

- [ ] Controller name and contact confirmed
- [ ] Processor name and contact confirmed
- [ ] Art. 28 agreement signed
- [ ] Subprocessor list complete
- [ ] Third-country transfer review done
- [ ] Legal bases confirmed by counsel
- [ ] Retention periods confirmed
- [ ] Data subject rights procedure tested
- [ ] DPIA decision recorded
- [ ] Record added to the Art. 30 register
