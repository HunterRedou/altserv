# Privacy impact assessment (DPIA, Art. 35 GDPR)

## Status

Draft for professional data-protection review. This is a working DPIA
screening and assessment template. The controller decides whether a DPIA is
required and signs the result. It is not legal advice.

## 1. Why a DPIA is likely required

Art. 35(3)(b) GDPR requires a DPIA for large-scale processing of special
category data under Art. 9. altserv processes health data for care recipients
across multiple tenants. The processing is not a one-off; it is the core of the
service.

Art. 35(3)(a) may also apply if the operator systematically evaluates or scores
people. The current design does not score patients or employees, and it has no
generative AI. That part is not triggered today, but it must be re-checked if a
risk-scoring feature is added.

The controller must also check the national DPIA list. In Germany, the
supervisory authorities publish a list under § 35 BDSG. If the processing
matches an entry, a DPIA is mandatory. If the residual risk remains high after
the measures in this document, the controller must consult the supervisory
authority under Art. 36 before production. In Brandenburg the competent
authority is the Landesbeauftragte für den Datenschutz und für das Recht auf
Akteneinsicht Brandenburg (LDA Brandenburg).

## 2. Description of the processing

### Nature

- Multi-tenant SaaS backend for nursing services.
- Tenants are separated logically in one PostgreSQL database with row-level
  security and a tenant-bound transaction role.
- Patient name and birth date are encrypted with AES-256-GCM at the
  application level.
- Care notes and correction reasons are encrypted the same way.
- A separate HMAC-SHA256 key builds a blind search index over patient names.
- Users authenticate with a session cookie and CSRF token.
- `chef` manages users, patients, assignments, templates, visits, reports and
  billing inside one tenant.
- `employee` sees only patients with a currently valid, non-revoked assignment.
- `platform_admin` is a support role without automatic patient access.
- Billing is deterministic. It is not generative AI. Exports are internal CSV
  and PDF. No automatic transmission to a payer is configured.
- Audit events are append-only. Reads and writes are recorded.

### Scope

| Item | Value |
| --- | --- |
| Data subjects | Care recipients (patients), employees of the tenant, tenant contact persons, platform staff |
| Data categories | Identity and contact data, health and care data, billing data, authentication data, audit data |
| Number of data subjects | To be provided by the operator. A multi-tenant nursing SaaS is likely large-scale. |
| Geography | Germany, primarily Brandenburg. Confirm the actual tenant footprint. |
| Hosting | EU/EEA only, unless a transfer assessment is done. |
| Retention | Per `retention-policy.md`; the controller confirms the care-documentation period. |
| Recipients | Hosting, backup and monitoring subprocessors; the tenant's billing office |

### Context

- Patients are often elderly or in need of care. They may not be able to
  exercise their rights without help.
- Employees have a power relationship with the tenant and may fear monitoring.
- Care documentation can reveal diagnoses, mobility, medication and family
  situation.
- A breach can cause discrimination, distress, financial harm or physical harm
  if care is disrupted.

### Purpose

Provide a lawful, documented care and billing record for the tenant, with
strict tenant separation and assignment-based access. The purpose is limited to
care documentation, care organisation and billing preparation. It is not
marketing, research, scoring or model training.

## 3. Necessity and proportionality

| Question | Assessment |
| --- | --- |
| Is the processing necessary for the purpose? | Yes. The tenant needs a care record and a billing basis. |
| Is the data minimised? | Partly. The system stores name and birth date, which are needed to identify a patient. Notes are free text and can contain more than necessary. The tenant must train staff. |
| Is the purpose limited? | Yes by design. No analytics on patient data, no model training, no advertising. |
| Is the retention limited? | The policy exists, but the application has no automated retention job. This is a gap. |
| Are data subject rights supported? | Partly. Read and correct are supported. Erasure and a full DSAR export are manual. |
| Are the data subjects informed? | The controller must provide the Art. 13/14 notice. The application does not display one. |
| Is the processing transparent? | The controller's notice and the Art. 28 contract must explain the roles and the subprocessors. |
| Is the processing fair? | Employees are not monitored beyond the care record. The audit log records actions, not productivity. |

## 4. Risks

Likelihood and impact are rated low, medium or high before the listed
measures. The controller must confirm the rating.

| ID | Risk | Likelihood | Impact | Affected |
| --- | --- | --- | --- | --- |
| R1 | Cross-tenant data exposure through an application bug or a missing tenant filter | Medium | High | Patients, employees |
| R2 | An employee reads a patient without a valid assignment | Medium | High | Patients |
| R3 | A compromised employee account is used to read or change care data | Medium | High | Patients |
| R4 | A `platform_admin` or support person reads patient data without a legal basis | Low | High | Patients |
| R5 | Database or backup theft exposes encrypted and unencrypted L3 fields | Low | High | Patients, employees |
| R6 | A lost encryption key makes care data unreadable and disrupts care | Low | High | Patients, tenant |
| R7 | A breach is not detected or not reported in time | Medium | High | Patients, controller |
| R8 | Data is kept longer than necessary | Medium | Medium | Patients, employees |
| R9 | A deletion request is not fulfilled because there is no deletion procedure | Medium | Medium | Patients |
| R10 | Free-text notes contain excessive or irrelevant health data | High | Medium | Patients |
| R11 | A subprocessor or third-country transfer is used without a contract | Low | High | All |
| R12 | Billing data is sent to the wrong payer or the wrong person | Low | High | Patients, tenant |
| R13 | A test or demo environment uses production patient data | Low | High | Patients |
| R14 | Monitoring or logs leak patient data to a third party | Low | High | Patients |

## 5. Measures

### Organisational

- Art. 28 agreement with every tenant and every subprocessor.
- Documented instructions for support access.
- A written policy that patient data must not be used for analytics, model
  training, advertising or tests.
- Role-based training for tenant staff and platform staff.
- A data subject rights procedure with named owners and response times.
- A breach response plan with a 72-hour decision path.
- A retention and deletion schedule with evidence.
- Vendor review before onboarding a subprocessor.

### Technical

- TLS for all network traffic.
- AES-256-GCM for patient name, birth date, care notes and correction reasons.
- Separate HMAC key for the search index, tenant-bound input, key versioning.
- PostgreSQL row-level security with `FORCE ROW LEVEL SECURITY`.
- Tenant-bound transaction role `altserv_app`; no `BYPASSRLS`.
- Role checks in the service layer in addition to RLS.
- Assignment check with `clock_timestamp()` for employee access.
- `platform_admin` denied patient access by default.
- Session cookie with `HttpOnly`, `Secure` and `SameSite` settings; CSRF token;
  login rate limit; session expiry.
- Append-only audit log with triggers and revoked update, delete and truncate.
- No health data in metrics; route patterns only.
- No query string or body in request logs.
- Encrypted backups, access-controlled, with restore drills and a backup-age
  alert.
- Optimistic locking and status workflows to prevent silent changes.
- Deterministic billing rules and an explicit approval step.

### Contractual

- Controller/processor roles and instructions.
- Subprocessor approval and notice.
- Audit and information rights.
- Deletion or return at the end of the contract.
- Breach notification without undue delay.
- Confidentiality of support staff.
- Insurance and liability.
- A clause that the operator may not use tenant data for its own purposes.

## 6. Residual risk

After the measures, the main residual risks are:

- A software defect can still cause cross-tenant exposure. Mitigation: RLS,
  integration tests, code review, least privilege, monitoring and a fast
  incident response.
- A compromised `chef` account can read all patients in its tenant. Mitigation:
  MFA, strong authentication, rate limiting, session revocation and audit. The
  residual risk is a stolen password plus a stolen second factor, or a leaked
  MFA seed.
- Free-text notes can contain excessive data. Mitigation: training and a
  documentation guideline. The application cannot enforce content.
- Retention and deletion are manual. Mitigation: a documented procedure and a
  periodic review until an automated job exists.
- A superuser can disable the audit triggers. Mitigation: restrict superuser
  access, use an external append-only sink, and monitor administrative actions.

The controller must decide whether the residual risk is acceptable. If it is
not, the processing must not start, or the controller must consult the
supervisory authority under Art. 36.

## 7. Consultation

| Step | Owner | Status |
| --- | --- | --- |
| Data protection officer involved | Controller | To be confirmed |
| Works council involved (employee data) | Controller | To be confirmed |
| Professional legal review | Controller | To be confirmed |
| Security review | Operator | To be confirmed |
| Supervisory authority consultation (Art. 36), if needed | Controller | To be decided |
| Sign-off | Controller | To be confirmed |

## 8. Review

Review this DPIA:

- before the first production tenant
- when a new data category, role, integration, subprocessor or hosting region is
  added
- after a personal data breach
- at least every three years

## 9. Open questions

- What is the actual number of data subjects and tenants at launch?
- Which national DPIA list entry applies, if any?
- Is a data protection officer required for the operator or the tenant?
- Which retention period applies to care documentation in the tenant's context?
- Should MFA also cover `employee` accounts that can read patient data?
- Which external append-only audit sink will be used?
- Which subprocessors are contracted, and where are they located?
- Is a works council agreement needed for the employee audit data?
