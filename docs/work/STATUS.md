# Status

Update one line when a work package, decision or review changes.

Last update: 2026-11-12.

## Done before this plan

| Item | Commit |
| --- | --- |
| Gap item 1: catalog administration | `6f48c8c` |
| Gap item 2: synthetic development seed | `d0fa469` |
| Gap item 6: tests layout, load smoke, security review | `5c70fdd` |
| Gap item 3: billing transmission boundary | `0756c88` |
| Critical fix: `/app/` exposed the working directory | `6c24b1a` |

## Work packages

| ID | Title | Phase | Depends on | Status |
| --- | --- | --- | --- | --- |
| WP-00 | Rotate local keys | 0 | none | open |
| WP-01 | Require DB TLS in prod | 0 | none | open |
| WP-02 | Require `METRICS_TOKEN` in prod | 0 | none | open |
| WP-03 | HSTS header in prod | 0 | none | open |
| WP-04 | Separate DB roles | 0 | none | open |
| WP-05 | Tenant status and manual activation | 0 | none | open |
| WP-06 | Mail interface and fake mailer | 0 | none | open |
| WP-07 | Terraform network | 2 | D-1 | blocked |
| WP-08 | Terraform RDS | 2 | WP-07, WP-01, WP-04 | blocked |
| WP-09 | App runtime and secrets | 2 | WP-08 | blocked |
| WP-10 | PITR drill script and record | 2 | WP-08, D-2 | blocked |
| WP-11 | Backup alarm | 2 | WP-08 | blocked |
| WP-12 | Staging load test | 2 | WP-09 | blocked |
| WP-13 | Audit sink ADR | 3 | D-1 | blocked |
| WP-14 | Audit sink exporter | 3 | WP-13, WP-09 | blocked |
| WP-15 | Payment provider | 3 | D-3, WP-05 | blocked |
| WP-16 | Remove VIES | 3 | WP-15 | blocked |
| WP-17 | Mail provider adapter | 3 | WP-06, D-1 | blocked |
| WP-18 | Operator-only tenant | 3 | D-7, WP-05 | blocked |
| WP-19 | Art. 15 export | 3 | none | open |
| WP-20 | Pilot UI | 4 | D-5, D-6 | blocked |

## Decisions

| ID | Topic | Status |
| --- | --- | --- |
| D-1 | Hosting | open |
| D-2 | RPO and RTO | open |
| D-3 | Activation and payment | open |
| D-4 | GDPR roles | open |
| D-5 | Pilot UI | open |
| D-6 | Pilot scope | open |
| D-7 | Support model | open |

## External reviews

| ID | Review | Status |
| --- | --- | --- |
| E-1 | Data protection review | open |
| E-2 | Billing expert | open |
| E-3 | Brandenburg catalog data | open |
| E-4 | Art. 28 contracts | open |
| E-5 | Tax adviser | open |
| E-6 | Penetration test | open |

## Pilot gate

Closed. See `docs/work/gate.md`.
