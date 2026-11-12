# Work packages

This folder holds the plan to the pilot. The text follows ASD-STE100: short
sentences, one instruction for each step, active voice.

"Pilot" means: one real care service uses altserv with real patient data. Do
not use real patient data before the pilot gate is closed.

## Files

| File | Content |
| --- | --- |
| `docs/work/STATUS.md` | One line for each work package, decision and review. |
| `docs/work/decisions.md` | Owner decisions D-1 to D-7 with context. |
| `docs/work/externals.md` | External reviews E-1 to E-6. |
| `docs/work/gate.md` | The pilot gate. |
| `WP-xx.md` | One card for each work package. |

## Rules for each session

1. Start a new session for each work package.
2. Use this prompt: "Read `docs/work/WP-xx.md`. Do WP-xx."
3. Read only the files in the card's "Read" list.
4. Do not read the conversation history.
5. If a step needs more context, read one more file. Write it in the card.
6. When you finish, update the card and its line in `docs/work/STATUS.md`.

## Standard procedure

```mermaid
flowchart LR
  A[Read the card] --> B[Read only the listed files]
  B --> C[Do the steps]
  C --> D{Error?}
  D -- yes --> E[Fix the error<br/>Write a regression test]
  E --> C
  D -- no --> F[Write the WP test]
  F --> G[gofmt, go build, go vet]
  G --> H[go test ./...]
  H --> I[TEST_DB_URL go test -race ./...]
  I --> J[Update docs]
  J --> K[Commit and push]
  K --> L[Update card and STATUS.md]
```

Commands:

```bash
gofmt -l .
go build ./...
go vet ./...
go test ./...
TEST_DB_URL='postgres://postgres:postgres@localhost:5432/altserv_test?sslmode=disable' go test -race ./...
```

## Permanent rules

- Do not invent Brandenburg LK codes, catalog data or § 105 / § 302 formats.
- `platform_admin` gets no automatic access to health data.
- Audit metadata contains no patient names, free text, notes, legal-basis text
  or health data.
- Use `clock_timestamp()` for age checks and for rows that one transaction can
  write more than once.
- Keep `docs/compliance/review-record.md` unsigned until a named reviewer signs.
- Keep `docs/platform-admin-patient-access-reminder.txt`.
- Do not commit `.env` or any secret.

## Card template

```
# WP-xx: <title>

Status: open | in progress | done (<commit>)
Phase: <0 to 4>
Depends on: <ids or "none">

## Goal
<one sentence>

## Read
- <exact path>

## Do
1. <one action>

## Test
- <test name>: <what it proves>

## Done when
- <checkable condition>

## Do not
- <scope limit>

## Notes
<findings from the session, at most 5 lines>
```

## Dependencies

```mermaid
flowchart TD
  subgraph P0[Phase 0: no decision needed]
    WP00[WP-00 Rotate local keys]
    WP01[WP-01 Require DB TLS in prod]
    WP02[WP-02 Require METRICS_TOKEN in prod]
    WP03[WP-03 HSTS header in prod]
    WP04[WP-04 Separate DB roles]
    WP05[WP-05 Tenant status and manual activation]
    WP06[WP-06 Mail interface and fake mailer]
  end

  subgraph DEC[Owner decisions]
    D1[D-1 Hosting]
    D2[D-2 RPO and RTO]
    D3[D-3 Activation and payment]
    D4[D-4 GDPR roles]
    D5[D-5 Pilot UI]
    D6[D-6 Pilot scope]
    D7[D-7 Support model]
  end

  subgraph P2[Phase 2: AWS]
    WP07[WP-07 Terraform network]
    WP08[WP-08 Terraform RDS]
    WP09[WP-09 App runtime and secrets]
    WP10[WP-10 PITR drill]
    WP11[WP-11 Backup alarm]
    WP12[WP-12 Staging load test]
  end

  subgraph P3[Phase 3: features]
    WP13[WP-13 Audit sink ADR]
    WP14[WP-14 Audit sink exporter]
    WP15[WP-15 Payment provider]
    WP16[WP-16 Remove VIES]
    WP17[WP-17 Mail provider adapter]
    WP18[WP-18 Operator-only tenant]
    WP19[WP-19 Art. 15 export]
  end

  subgraph P4[Phase 4: pilot]
    WP20[WP-20 Pilot UI]
    GATE{{Pilot gate}}
  end

  subgraph EXT[External reviews]
    E1[E-1 Data protection]
    E2[E-2 Billing expert]
    E3[E-3 Catalog data]
    E4[E-4 Art. 28 contracts]
    E5[E-5 Tax adviser]
    E6[E-6 Pentest]
  end

  D1 --> WP07 --> WP08 --> WP09
  WP01 --> WP08
  WP04 --> WP08
  D2 --> WP10
  WP08 --> WP10
  WP08 --> WP11
  WP09 --> WP12
  D1 --> WP13 --> WP14
  WP09 --> WP14
  D3 --> WP15
  WP05 --> WP15 --> WP16
  WP06 --> WP17
  D1 --> WP17
  D7 --> WP18
  WP05 --> WP18
  D5 --> WP20
  D6 --> WP20
  D4 --> E1
  D4 --> E4
  D3 --> E5
  WP09 --> E6

  WP10 & WP11 & WP12 & WP14 & WP16 & WP17 & WP18 & WP19 & WP20 --> GATE
  E1 & E2 & E3 & E4 & E5 & E6 --> GATE
```

## Order

```mermaid
flowchart LR
  P0[Phase 0<br/>WP-00 to WP-06] --> DEC[Decisions<br/>D-1 to D-7]
  DEC --> P2[Phase 2 AWS<br/>WP-07 to WP-12]
  P2 --> P3[Phase 3<br/>WP-13 to WP-19]
  P3 --> P4[Phase 4<br/>WP-20]
  P4 --> G{{Pilot gate}}
  DEC -.-> EXT[External reviews<br/>E-1 to E-6, in parallel]
  EXT -.-> G
```

Phase 0 does not need a decision. Make the decisions while phase 0 runs.
