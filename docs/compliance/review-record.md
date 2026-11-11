# Professional review record

## Purpose

This record holds the signed decisions from the professional review. The
questions are in `review-packet.md`. This file is blank until a named reviewer
fills it in.

Do not treat a blank field as approval. Do not start the next implementation
step until the gate in `review-packet.md` is recorded here.

## Review metadata

| Field | Value |
| --- | --- |
| Review version | 0.1 |
| Packet version | 0.1 |
| Review start | |
| Review end | |
| Controller | |
| Processor | |
| Documents reviewed | |
| Open items at close | |

## Decision log

Copy the decision IDs from `review-packet.md`. Write the result, the conditions
and the evidence.

| ID | Decision | Result | Conditions or required change | Evidence | Reviewer | Date |
| --- | --- | --- | --- | --- | --- | --- |
| DP-01 | Controller and processor | | | | | |
| DP-02 | Art. 28 agreement | | | | | |
| DP-03 | Subprocessor list | | | | | |
| DP-04 | DPIA decision | | | | | |
| DP-05 | Legal basis for health data | | | | | |
| DP-06 | Retention periods | | | | | |
| DP-07 | Audit evidence at termination | | | | | |
| DP-08 | Data subject rights | | | | | |
| DP-09 | Breach path | | | | | |
| DP-10 | Hosting and transfer basis | | | | | |
| DP-11 | Works council | | | | | |
| DP-12 | Privacy notice | | | | | |
| BI-01 | Service catalogs and codes | | | | | |
| BI-02 | Payer procedure | | | | | |
| BI-03 | Export or transmission | | | | | |
| BI-04 | Billing rule set | | | | | |
| BI-05 | Approval workflow | | | | | |
| BI-06 | Retention and GoBD | | | | | |
| BI-07 | VAT treatment | | | | | |
| BI-08 | Clearing house or portal | | | | | |
| SE-01 | MFA | | | | | |
| SE-02 | Password reset and disable | | | | | |
| SE-03 | Session purge | | | | | |
| SE-04 | Break-glass policy | | | | | |
| SE-05 | Audit evidence strategy | | | | | |
| SE-06 | Recovery target | | | | | |
| SE-07 | Production hardening | | | | | |

## Gate status

| Step | Required decisions | Complete | Date | Notes |
| --- | --- | --- | --- | --- |
| 2. Authentication gaps | SE-01, SE-02, SE-03, DP-05 | No | | |
| 3. Break-glass access | DP-01, DP-02, DP-09, SE-01, SE-04 | No | | |
| 4. Retention and deletion | DP-05, DP-06, DP-07, DP-08 | No | | |
| 5. Tenant termination | DP-06, DP-07, DP-10, BI-06, SE-05 | No | | |

## Conditions and follow-up

| ID | Condition | Owner | Due date | State |
| --- | --- | --- | --- | --- |
| | | | | |

## Sign-off

| Area | Name | Role | Date | Result | Signature |
| --- | --- | --- | --- | --- | --- |
| Data protection | | | | | |
| Legal | | | | | |
| Billing | | | | | |
| Security | | | | | |
| Management | | | | | |

## Notes

- A condition must have an owner and a due date.
- A deferred decision must have an owner and a due date.
- A rejected decision must name the required change.
- The record contains no patient data.
- Store the signed record with the contract documents.
