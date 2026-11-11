# Incident response and personal data breach policy

## Status

Draft for professional data-protection review. This document covers the legal
and organisational response to a personal data breach. The technical steps are
in `../runbooks/incident-response.md`. It is not legal advice.

## Scope

This policy applies to:

- a confirmed or suspected personal data breach
- unauthorised access, loss, alteration or disclosure of personal data
- a lost device, a leaked credential or key
- a ransomware or malware event that affects personal data
- a subprocessor breach that affects tenant data
- a misdirected billing export or email

A security incident without personal data can still be a SEV1 operationally,
but it does not trigger the Art. 33/34 duties by itself.

## Roles

| Role | Responsibility |
| --- | --- |
| Incident commander | Runs the response, decides, coordinates. |
| Security lead | Contains the technical incident and preserves evidence. |
| Data protection lead / DPO | Assesses the breach, decides on notification, documents it. |
| Legal counsel | Reviews the notification and the wording. |
| Communications lead | Handles customer and public communication. |
| Management | Approves resources, external communication and regulatory contact. |
| Processor contact | Receives and escalates a breach at a subprocessor. |

One person may hold several roles in a small team. Record who held which role
and when.

## Definitions

- **Personal data breach**: a breach of security leading to the accidental or
  unlawful destruction, loss, alteration, unauthorised disclosure of, or access
  to, personal data (Art. 4(12) GDPR).
- **Controller**: the tenant for patient and employee data.
- **Processor**: the altserv operator for tenant data.
- **Supervisory authority**: in Brandenburg, the Landesbeauftragte für den
  Datenschutz und für das Recht auf Akteneinsicht Brandenburg (LDA
  Brandenburg), unless another authority is competent.

## Notification duties

| Duty | Who | Deadline | Legal basis |
| --- | --- | --- | --- |
| Notify the controller | Processor | Without undue delay after becoming aware | Art. 33(2) GDPR, Art. 28(3)(f) |
| Notify the supervisory authority | Controller | Within 72 hours after becoming aware, unless the breach is unlikely to result in a risk | Art. 33(1) GDPR |
| Notify the data subjects | Controller | Without undue delay if the breach is likely to result in a high risk | Art. 34 GDPR |
| Document the breach | Controller and processor | Immediately and continuously | Art. 33(5) GDPR |
| Inform the tenant | Operator | Per the contract, normally within 24 hours | Art. 28 agreement |

The 72-hour clock starts when the controller becomes aware, not when the
incident is fully understood. A first notification can be incomplete; it must be
updated in phases.

## Response phases

### 1. Detect and report

- Any person who suspects a breach reports it immediately to the incident
  commander and the data protection lead.
- Do not wait for proof. A credible suspicion is enough to start the assessment.
- Do not discuss the details in a public channel.

### 2. Contain

- Stop the leak: revoke sessions, rotate keys or credentials, block the path at
  the proxy, disable the account.
- Preserve evidence before rebuilding: logs, audit rows, database snapshots,
  container image digests, secret-manager audit logs.
- Do not delete data or audit rows to "clean up".
- Do not pay a ransom without management and legal approval.

### 3. Assess

Answer these questions in writing:

1. What happened, and when?
2. Which tenants, data subjects and data categories are affected?
3. Was the data encrypted, and is the key also compromised?
4. How many data subjects are affected?
5. Is the breach ongoing?
6. What is the likely impact: discrimination, distress, financial loss,
   physical harm, loss of care continuity?
7. Is the risk unlikely, a risk, or a high risk?
8. Is a subprocessor involved?

If the key and the encrypted data are both exposed, treat the data as
unencrypted.

### 4. Decide on notification

| Situation | Authority | Data subjects |
| --- | --- | --- |
| No risk | No | No |
| Risk but not high | Yes, within 72h | No |
| High risk | Yes, within 72h | Yes, without undue delay |
| Processor breach | The controller notifies; the processor supports | The controller decides |

The data protection lead makes a recommendation. Legal counsel and management
approve. Record the decision and the reasons, including a decision not to
notify.

### 5. Notify

A notification to the supervisory authority should contain, as far as known:

- the nature of the breach
- the categories and approximate number of data subjects
- the categories and approximate number of records
- the name and contact of the data protection lead
- the likely consequences
- the measures taken or proposed
- any cross-border aspect

A notification to data subjects should be clear and plain:

- what happened
- what data was affected
- what the controller has done
- what the data subject should do
- who to contact

Do not speculate about the cause, do not blame a person, and do not include
unnecessary health data.

### 6. Recover and verify

- Remove the cause, not only the symptom.
- Restore from a verified backup or fail over.
- Verify login, patient read, report read and billing export.
- Watch error rate, latency, pool use and slow queries.
- Keep the incident channel open until the commander closes it.

### 7. Learn

- Blameless postmortem within the agreed period.
- Timeline, impact, root cause, detection gap, response gap.
- Follow-up actions with owner and due date.
- Add a regression test, alert or runbook change.
- Update this policy, the DPIA and the processing record if needed.
- Record whether a notification was made and who decided.

## Processor obligations

The operator must:

- notify the controller without undue delay
- provide the information the controller needs for Art. 33(3)
- not notify the supervisory authority on the controller's behalf unless the
  contract says so
- cooperate with the controller and the authority
- keep the breach record
- ensure subprocessors notify the operator in the same way

The contract must name the contact and the maximum notification time. A
24-hour processor-to-controller notification is a common target; it is not a
statutory deadline, so write it into the contract.

## Breach register

Keep one register with:

- internal incident id
- date and time of awareness
- date and time of containment
- affected tenants and data categories
- number of data subjects and records
- risk assessment and decision
- notification dates and recipients
- measures and follow-up
- owner and closure date

The register contains personal data. Restrict access and apply the retention
policy.

## Communication rules

- Use factual language.
- Do not include patient names, health data, credentials or exploit details.
- Do not use a public status page for tenant-specific data.
- Keep a single source of truth for the timeline.
- Get legal approval before a regulatory or public statement.

## Testing and training

- Run a tabletop exercise at least once a year.
- Test the contact list, including the subprocessors.
- Test a mock notification with the data protection lead and legal counsel.
- Train support staff to recognise a breach report.
- Review the plan after every incident and every material change.

## Gaps

- There is no automatic session revocation API. Use a documented database
  procedure until one exists.
- There is no external append-only audit sink. A compromised superuser can
  alter the local audit table.
- There is no automated breach register.
- The subprocessor contact list must be completed.
- The operator must confirm the LDA Brandenburg contact and the notification
  portal before production.

## Open questions

- Who is the data protection lead, and who is the deputy?
- What is the contractual processor notification time?
- Which subprocessors must be contacted, and who owns each relationship?
- Is cyber insurance in place, and what does it require?
- Who may speak to the authority and to the press?
