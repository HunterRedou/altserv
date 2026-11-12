# ADR-025: Synthetic development seed data

## Status

Accepted.

## Problem

A fresh development database is empty. Without reference data and a demo tenant,
nobody can exercise the application by hand: there is no catalog to search, no
contract to bill against, no patient to assign and no visit to start. Every
developer rebuilt that state with ad-hoc SQL, which had three problems:

- ad-hoc SQL bypassed the service layer, so encrypted patient data and the
  blind search index were missing or wrong
- the rows were not marked as test data, so a synthetic catalog could be
  mistaken for a real Brandenburg catalog
- there was no guard that stopped the same script from running against
  production

doc 16 allows `db/seeds` only for synthetic development data. The seed has to
be a real, repeatable path that produces a coherent dataset through the same
business rules the application uses.

## Alternatives

- Keep ad-hoc SQL snippets in the runbook.
- Ship a SQL seed file and load it with `psql`.
- Copy a production dump into development.
- A Go command in `cmd/devseed` that calls the service layer and refuses to run
  outside development.

## Decision

### Command and guard

`cmd/devseed` creates one demo tenant with three users, a synthetic catalog
with three services, a contract, an activity rule, two patients, assignments, a
template and a planned visit.

- It refuses to run unless `PLATFORM=dev`. The guard is the exported
  `devseed.EnsureDevelopment`, so a caller cannot forget it.
- It refuses to run when the demo tenant or any of the three demo user emails
  already exists. A second run returns `ErrAlreadySeeded` instead of
  duplicating rows.
- The password comes from `-password` or `DEV_SEED_PASSWORD`. There is no
  built-in default.
- Patient data is written through the patient service with the configured
  encryption and search keys. The seed never writes plaintext patient rows.

### Data is visibly synthetic

- The catalog code is `SYNTH-DEMO-2026`.
- Every service code starts with `SYNTH-`.
- The catalog source note is `SYNTHETISCHE ENTWICKLUNGSDATEN - kein echter
  Brandenburger Katalog, nicht fachlich verifiziert`.
- The payer and contract reference carry a `SYNTH`/`Synthetische` marker.

No real Brandenburg code or payer is invented or implied. Real catalog data is
loaded with `cmd/catalogimport` after professional verification (ADR-024).

### Structure

`devseed.Run` opens one transaction, calls `devseed.RunTx` and commits.
`devseed.RunTx` is exported so integration tests can seed inside their own
transaction and roll everything back. The seed uses the existing services for
catalog, contracts, rules, patients, assignments, templates and visits, so every
row gets the normal validation and audit events.

## Reasons

- A Go command can use the service layer. A SQL file cannot encrypt patient
  data or maintain the search index.
- The `PLATFORM=dev` guard makes an accidental production run fail before the
  first write.
- Visible `SYNTH` markers make it hard to confuse demo data with verified
  reference data.
- The same code path is used by tests, so the seed cannot silently drift from
  the schema.

## Consequences

- The demo tenant is bound to one deployment and one set of patient keys. A key
  rotation makes the old demo patients unreadable; delete and reseed instead.
- The seed is not a migration and does not run automatically. It is an explicit
  operator step.
- The command needs `DB_URL`, `PLATFORM`, `PATIENT_ENCRYPTION_KEY`,
  `PATIENT_KEY_VERSION`, `PATIENT_SEARCH_KEY` and `PATIENT_SEARCH_KEY_VERSION`.
- The demo users have no MFA enrolled. A privileged login is therefore sent to
  MFA enrollment first, which is the intended production behaviour.

## Risks

- Someone can still copy the demo data to production by hand. The `SYNTH`
  markers and the source note make that visible in review, but they do not
  prevent it.
- The seed depends on the current service rules. When a rule changes, the seed
  may need an update; the DB-backed test catches that early.
- The fixed demo user emails mean two demo tenants cannot coexist in one
  database. That is deliberate: it keeps the guard simple.

## Open questions

- Should a future version create the demo tenant under a random suffix so
  several demo tenants can coexist?
- Should the seed also create a completed visit and a submitted report for
  billing demos?
