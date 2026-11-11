# Data classification

## Status

Draft for professional data-protection review. This document is not legal
advice. It describes how altserv classifies data so that access, encryption,
retention and deletion rules can be applied consistently. The controller and
the operator must confirm the classification and the handling rules before
production.

## Purpose and scope

This policy covers every data set that altserv stores, processes, transmits or
backs up, including:

- tenant and contract data
- user accounts and sessions
- patient master data
- care documentation (assignments, visits, activities, reports)
- billing data
- audit events
- operational logs and metrics
- backups and exports
- secrets and keys

It applies to the application, the database, backups, exports, monitoring and
support access.

## Roles

| Role | Responsibility |
| --- | --- |
| Controller (Verantwortlicher) | The nursing service (tenant) decides why and how patient and employee data is processed. |
| Processor (Auftragsverarbeiter) | The altserv operator processes tenant data only on documented instructions, under an Art. 28 GDPR agreement. |
| Platform administrator (`platform_admin`) | Operates the platform. Has no automatic access to patient health data. |
| Tenant chef (`chef`) | Administers users, patients, assignments, reports and billing inside one tenant. |
| Tenant employee (`employee`) | Sees only patients with a currently valid, non-revoked assignment. |

The exact allocation between controller, processor and possible joint
controllership must be fixed in the contract. See
`processing-activities.md` and `../decisions/ADR-004-shared-database-tenants.md`.

## Classification levels

| Level | Name | Definition | Examples |
| --- | --- | --- | --- |
| L0 | Public | May be published without harm. | `start.html`, public documentation, the `status` field of `/healthz` and `/readyz`. |
| L1 | Internal | Non-personal operational data. Unauthorised disclosure is undesirable but not harmful to data subjects. | Aggregate metrics, service catalog definitions, alert rules, runbooks, configuration without secrets. |
| L2 | Confidential personal data | Personal data that is not health data. Disclosure can harm or embarrass a person. | Tenant contact email and address, USt-IdNr, employee name and email, role, session identifiers, request IDs, billing amounts, payer names. |
| L3 | Special category / high risk | Health data and care documentation, or data that reveals health status. Disclosure can cause serious harm. | Patient first and last name, birth date, care notes, visit and report content, report transition reasons, activities and service codes tied to a patient, billing lines tied to a patient. |
| L4 | Secrets | Credentials and keys. Disclosure breaks confidentiality or integrity of the whole platform. | `PATIENT_ENCRYPTION_KEY`, `PATIENT_SEARCH_KEY`, database passwords, session token hashes, CSRF tokens, `METRICS_TOKEN`, backup encryption keys, TLS private keys. |

L3 is special category data under Art. 9 GDPR when it relates to an identified
or identifiable natural person. Pseudonymous UUIDs do not remove that status:
the tenant can re-identify a patient from its own records.

## Data inventory

| Data set | Examples | Level | Storage | Protection |
| --- | --- | --- | --- | --- |
| Tenant master data | email, USt-IdNr, street, postal code, status | L2 | `tenants` | TLS, RLS, role-restricted API |
| User accounts | name, email, password hash, role, tenant | L2 | `users` | Argon2id hash, TLS, RLS |
| Sessions | token hash, CSRF token, expiry, tenant | L2/L4 | `sessions` | Hashed session token, HttpOnly cookie, TLS |
| Patient master data | first name, last name, birth date, status | L3 | `patients` | AES-256-GCM at application level, TLS, RLS |
| Patient search index | HMAC tokens, token type | L3 (pseudonymous) | `patient_search_tokens` | Separate HMAC key, tenant-bound input, RLS |
| Assignments | patient, employee, validity, revocation | L3 | `patient_assignments` | RLS, chef-only management, audit |
| Service catalogs | catalog and service definitions | L1 | `service_catalogs`, `service_definitions` | Read-only global data |
| Tenant contracts | contract reference, payer, validity | L2/L3 | `tenant_service_contracts` | RLS, chef-only management |
| Activities | patient, employee, time, duration, note, service snapshot | L3 | `activities` | Note encrypted, TLS, RLS |
| Templates | template and template activities | L1/L3 | `templates`, `template_activities` | RLS; a template itself is not patient-bound |
| Visits | patient, planned and actual employee, times, status | L3 | `visits` | TLS, RLS |
| Reports | patient, visit, employee, status, note, version | L3 | `reports` | Note encrypted, TLS, RLS |
| Report activities | activity reference, quantity, performed time | L3 | `report_activities` | TLS, RLS |
| Report transitions | return or correction reason, actor | L3 | `report_transitions` | Reason encrypted, TLS, RLS |
| Billing runs | report, status, version, approver | L2/L3 | `billing_runs` | RLS, chef-only |
| Billing lines | service code, quantity, payer, performed time | L3 | `billing_lines` | RLS, chef-only |
| Billing findings | rule result, message, activity reference | L2/L3 | `billing_findings` | RLS, chef-only |
| Audit events | actor, action, entity id, request id, metadata | L2 | `audit_events` | Append-only, RLS, no health data in metadata |
| Application logs | method, path, status, request id, duration | L2 | Log sink | No query string, no body, no patient data |
| Metrics | counters, histograms, pool and job signals | L1 | Prometheus | No tenant, patient or user label |
| Backups | full database dump and WAL archive | L3/L4 | Backup repository | Must be encrypted at rest and access-controlled by the operator; confirm with the backup product |
| Exports | CSV and PDF of a billing run | L3 | Downloaded by the tenant | Generated on demand; not stored by the app |

## Handling rules

### L0 Public

- May be served without authentication.
- Must not contain personal data or internal hostnames.

### L1 Internal

- Read access for operators and developers who need it.
- No personal data.
- May be shared in tickets and incident reports.

### L2 Confidential personal data

- Access on a need-to-know basis.
- Encrypted in transit with TLS.
- Not posted in tickets, chat or status pages.
- Redact identifiers before sharing a screenshot or log excerpt.
- Retain only as long as the purpose or a legal duty requires.

### L3 Special category / high risk

- Access only through the application with a valid tenant session and, for
  employees, a currently valid assignment.
- Encrypted at rest. Patient name and birth date are encrypted at the
  application level. Care notes and correction reasons are encrypted at the
  application level. Other L3 fields rely on database access control and RLS.
  Confirm that the hosting provider encrypts the underlying disk and the backup
  repository; that control is not part of the application code.
- Never copy into tickets, chat, email, monitoring or test data.
- Test data must be synthetic. Do not use production patient data in a test
  database.
- Exports are generated only for an approved billing run and are the tenant's
  responsibility after download.
- Do not send L3 data to a third-party monitoring, analytics or AI service. The
  application has no generative AI and no third-party health-data path.

### L4 Secrets

- Stored only in the secret manager or the deployment environment, never in the
  repository, an image or a log.
- The AES key and the search HMAC key must be different values. The application
  rejects reuse at startup.
- Rotate on a schedule and after any suspected exposure. See
  `../runbooks/key-rotation.md`.
- Backups of secrets are as sensitive as the secrets themselves.

## Pseudonymous identifiers

UUIDs are pseudonymous, not anonymous. They become personal data as soon as a
tenant can link them to a patient or employee. Treat every patient id, user id
and tenant id as L2 at minimum, and as L3 when it appears next to care data.

Operational logs may contain a path with an id. They must not contain a name, a
search term or a note. Metrics use route patterns only, never raw paths.

## Special category data

The following are health data under Art. 9 GDPR when linked to a person:

- patient name and birth date
- care notes in activities and reports
- return and correction reasons
- visits, activities and reports
- service codes and billing lines that reveal a care service
- assignment data that reveals which employee cares for which patient

Processing requires a legal basis under Art. 9(2), for example Art. 9(2)(h)
together with § 22 BDSG, or explicit consent. The controller must document the
basis. The operator may process this data only on documented instructions.

## Review triggers

Review this classification when:

- a new data field, export, integration or subprocessor is added
- a new role or break-glass access is introduced
- the hosting location or backup product changes
- a legal retention or documentation duty changes
- an incident shows that a data set was classified too low

## Open questions

- Which L3 fields must also be encrypted at the application level, beyond the
  current patient name, birth date, care note and correction reason?
- Is disk encryption at the hosting provider contractually guaranteed and
  documented?
- Which subprocessors receive L2 or L3 data, and where are they located?
- Should the audit log be exported to an external append-only sink?
- Which retention periods apply to care documentation in the tenant's federal
  state and professional context?
