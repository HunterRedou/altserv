# Professional review packet

## Purpose

This packet gives the reviewer a short list of decisions. The reviewer does not
need to read every sentence first. The reviewer can start with the decision
table and then read the linked document.

The packet is a working document. It is not legal advice. A decision is valid
only when a named reviewer signs it in `review-record.md`.

## Reviewer roles

| Area | Reviewer | Must confirm |
| --- | --- | --- |
| Data protection | Data protection lawyer or DPO | Roles, legal bases, DPIA, retention, deletion, rights, breach path. |
| Billing | Qualified billing expert | Service codes, payer procedure, format, rules, GoBD, retention. |
| Security | Operator security lead | MFA, break-glass, audit sink, recovery, production hardening. |
| Management | Operator management | Resources, contracts, liability, go/no-go. |

One person can hold more than one role in a small team. Record the name and the
role.

## Documents to read

| Document | Read it for |
| --- | --- |
| `data-classification.md` | Data levels and handling rules. |
| `processing-activities.md` | Art. 30 record, roles, Art. 28 checklist. |
| `privacy-impact-assessment.md` | DPIA screening, risks, measures, residual risk. |
| `retention-policy.md` | Retention schedule, legal holds, current gaps. |
| `deletion-policy.md` | Deletion order, tenant termination, backups. |
| `access-control-policy.md` | Roles, tenant isolation, platform admin, authentication. |
| `incident-response.md` | Breach assessment, 72-hour path, notification. |
| `backup-policy.md` | Encryption, retention, restore drills, RPO/RTO. |
| `billing-requirements.md` | SGB XI, SGB V, GoBD, export and transmission. |
| `README.md` | Production checklist and known gaps. |

## How to record a decision

Use one row per decision in the tables below. Write one of these values:

- **Accepted** — the draft position is correct.
- **Accepted with conditions** — the draft position is correct after the named
  change. Write the change in the conditions column.
- **Rejected** — the draft position is not correct. Write the required change.
- **Deferred** — the decision needs more information. Write the owner and the
  due date.

A decision is not complete without a name and a date.

## Part A: Data protection decisions

| ID | Decision | Legal anchor | Draft position | Reviewer decision | Conditions | Owner | Date |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DP-01 | Who is the controller and who is the processor? | Art. 4(7), Art. 4(8), Art. 28 | The tenant is the controller. The operator is the processor for tenant data and the controller for its own account data. | | | | |
| DP-02 | Is the Art. 28 agreement sufficient? | Art. 28(3) | Use the checklist in `processing-activities.md`. | | | | |
| DP-03 | Is the subprocessor list complete? | Art. 28(2), Art. 28(4) | The list must be real. It is not complete yet. | | | | |
| DP-04 | Is a DPIA required, and is the residual risk acceptable? | Art. 35, § 35 BDSG | A DPIA is likely required for large-scale health data. The controller decides. | | | | |
| DP-05 | Is the legal basis for health data correct? | Art. 6, Art. 9(2), § 22 BDSG | Art. 9(2)(h) with § 22 BDSG, or explicit consent. Confirm. | | | | |
| DP-06 | Which retention periods apply? | Art. 5(1)(e), § 630f BGB, § 147 AO, § 257 HGB | The schedule in `retention-policy.md` is a proposal. Confirm each row. | | | | |
| DP-07 | What happens to audit events at tenant termination? | Art. 5(2), Art. 32 | Export, de-identify or external sink. Not decided. | | | | |
| DP-08 | Is the data subject rights procedure sufficient? | Art. 15 to Art. 21 | Read and correct are supported. Erasure and a full export are manual. | | | | |
| DP-09 | Is the breach path correct? | Art. 33, Art. 34 | Processor notifies the controller without undue delay. The controller notifies the authority within 72 hours. | | | | |
| DP-10 | Is the hosting and backup location lawful? | Chapter V GDPR | EU/EEA only, unless a transfer assessment is done. Confirm the real locations. | | | | |
| DP-11 | Is the works council involved? | § 87 BetrVG, employee data | To be confirmed by the controller. | | | | |
| DP-12 | Is the privacy notice complete? | Art. 13, Art. 14 | The application does not display a notice. The controller must provide one. | | | | |

## Part B: Billing decisions

| ID | Decision | Legal anchor | Draft position | Reviewer decision | Conditions | Owner | Date |
| --- | --- | --- | --- | --- | --- | --- | --- |
| BI-01 | Are the service catalogs and codes real and current? | § 105 SGB XI, § 302 SGB V | No invented codes. The catalog must be verified. | | | | |
| BI-02 | Which payer procedure applies to the first tenants? | § 105 SGB XI, § 302 SGB V | Not configured. The tenant submits the claim. | | | | |
| BI-03 | Is the export format accepted, or is transmission required? | Payer technical rules | CSV and PDF are internal only. Transmission is `not_configured`. | | | | |
| BI-04 | Does the rule set match the payer's current rules? | Payer rules | The engine checks status, contract, legal area, duplicates, double billing and tenant rules. | | | | |
| BI-05 | Does the approval workflow match the tenant's control? | GoBD, internal control | Draft, approved, voided. A chef approves. | | | | |
| BI-06 | Is retention and archiving sufficient? | § 147 AO, § 257 HGB, GoBD | Keep billing records for the statutory period. The tenant archives the claim and the response. | | | | |
| BI-07 | Which VAT treatment applies? | UStG | To be confirmed. | | | | |
| BI-08 | Is a clearing house or payer portal a processor? | Art. 28 | Not used today. Review it before use. | | | | |

## Part C: Security decisions

| ID | Decision | Draft position | Reviewer decision | Conditions | Owner | Date |
| --- | --- | --- | --- | --- | --- | --- | --- |
| SE-01 | Is MFA required for `chef` and `platform_admin`? | Required before production. | | | | |
| SE-02 | Are password reset and user disable required? | Required before production. | | | | |
| SE-03 | Is a scheduled session purge required? | Required. The query exists, but no job runs it. | | | | |
| SE-04 | Is the break-glass policy acceptable? | Not implemented. Future access must be explicit, time-limited, read-only where possible and audited. | | | | |
| SE-05 | Which audit evidence strategy is used? | Export, de-identify or external sink. Not decided. | | | | |
| SE-06 | Is the recovery target acceptable? | No PITR product and no automated drill yet. | | | | |
| SE-07 | Is the production hardening complete? | Secrets, TLS, `METRICS_TOKEN`, alert receiver and operator-only tenant are required. | | | | |

## Part D: Gate map

The review must answer the decisions that the next step needs.

| Next step | Decisions needed before the step starts |
| --- | --- |
| 2. Authentication gaps | SE-01, SE-02, SE-03, DP-05 |
| 3. Break-glass access | DP-01, DP-02, DP-09, SE-01, SE-04 |
| 4. Retention and deletion | DP-05, DP-06, DP-07, DP-08 |
| 5. Tenant termination | DP-06, DP-07, DP-10, BI-06, SE-05 |

Do not start a step until its decisions are recorded in `review-record.md`.

## Part E: Sign-off

| Area | Name | Role | Date | Result | Signature |
| --- | --- | --- | --- | --- | --- |
| Data protection | | | | | |
| Legal | | | | | |
| Billing | | | | | |
| Security | | | | | |
| Management | | | | | |

## Open items that block the review

1. The subprocessor list is not complete.
2. The hosting and backup locations are not confirmed.
3. The retention periods are proposals, not decisions.
4. The audit evidence strategy is not chosen.
5. The payer and the format for the first tenants are not confirmed.
6. The DPIA decision is not recorded.
7. The works council position is not known.

Do not mark the review complete while an item in this list is open.
