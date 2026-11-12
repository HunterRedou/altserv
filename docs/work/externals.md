# External reviews

Qualified people outside the project must do these reviews. Record each
result in `docs/compliance/review-record.md` with the reviewer's name and the
date. A blank field is not an approval.

| ID | Review | Input | Depends on |
| --- | --- | --- | --- |
| E-1 | Data protection review: DPIA, legal bases, retention periods, § 393 SGB V | `docs/compliance/review-packet.md` | D-4 |
| E-2 | Billing expert: contracts, rules, § 105 SGB XI / § 302 SGB V procedure | `docs/compliance/billing-requirements.md` | none |
| E-3 | Verified Brandenburg catalog data, loaded with `cmd/catalogimport` | `docs/decisions/ADR-024-catalog-administration.md` | E-2 |
| E-4 | Art. 28 agreements with AWS, the payment provider and the mail provider | `docs/compliance/processing-activities.md` | D-4, D-1, D-3 |
| E-5 | Tax adviser: VAT status, USt-IdNr requirement, invoice rules (§ 14 UStG, GoBD) | `docs/work/decisions.md`, D-3 | D-3 |
| E-6 | Penetration test against staging | `docs/security-review.md` | WP-09 |

## Rules

- Start E-1, E-2 and E-5 early. They take the longest.
- Do not load catalog data before E-3 is signed.
- Do not connect a real payer adapter before E-2 is signed and a payer test is
  recorded.
- E-6 must have no open critical or high finding before the pilot gate.
