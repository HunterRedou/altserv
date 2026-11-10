# ADR-013: Service catalog, contracts and activity snapshots

## Problem
Care activities must be billable and reportable. The billable service, its legal
area (SGB XI or SGB V) and the payer contract come from reference data that is
maintained outside the tenant and changes over time. A report must show what was
true when the activity happened, not what the catalog says today. At the same
time, no invented Brandenburg service codes or payer data may ship with the
software; real catalog data has to be verified by domain experts.

## Alternatives
- Store only the current service id and join at report time
- Let tenants maintain their own global service catalog
- Copy the whole catalog row into every activity
- Separate global reference tables plus tenant contracts, with a snapshot of the
  billable state on the activity

## Decision
Global reference data and tenant data are separated:

- `service_catalogs` and `service_definitions` are global, read-only tables
  written by migrations or an administrative process. They carry a validity
  window (`valid_from`, `valid_until`) and a legal area.
- `tenant_service_contracts` belong to one tenant, reference a service
  definition and store the payer name, contract reference and validity window.
  They are protected by RLS and a composite foreign key to the tenant.
- `activities` reference a patient, an employee and, when billable, a service
  definition and contract. They store a snapshot of `service_code`,
  `service_name`, `legal_area`, `contract_reference` and `payer_name`.
- `activity_service_rules` map a free activity label to a service definition and
  optionally a contract. Rules are configuration only; they are never applied
  automatically.

The billable snapshot is taken from a re-read of the definition and contract
inside the same transaction, never from the client. Both references are set
together or neither is set. The definition and contract must cover the service,
share the legal area and be valid at `performed_at`.

Access rules:

- `chef` and `employee` may read global catalogs and definitions.
- `platform_admin` gets no automatic catalog or activity access (ADR-004 and the
  platform-admin reminder).
- Only a `chef` may create contracts or activity rules.
- `chef` reads activities tenant-wide; `employee` reads activities only for
  currently assigned patients.

The activity free-text note is encrypted with the patient cipher and an AAD of
`v<keyVersion>|<tenantID>|<activityID>|note`. Audit events record identifiers,
durations and booleans, never the note text.

## Reasons
- A snapshot keeps historical reports stable when a catalog or contract changes.
- Global reference tables avoid per-tenant divergence of statutory service
  definitions.
- Tenant-bound contracts keep payer data isolated under RLS.
- Requiring the definition and contract together prevents half-billable rows.
- Chef-only configuration stops employees from granting themselves billable
  rights.

## Consequences
- Catalog changes do not rewrite past activities; only new activities see the
  new data.
- Activity rows grow because they duplicate the billable snapshot fields.
- The catalog and contract tables need a seeding and maintenance process outside
  the tenant role; this repository ships no invented service codes.
- Rules exist but no automatic matching is implemented yet.

## Risks
- If a contract or definition is edited incorrectly, only future activities are
  affected; past snapshots stay as recorded.
- Global reference data has no RLS, so a compromised application role could read
  every definition. Definitions are not tenant secret, but write access must stay
  outside the application role.
- Snapshot fields can drift from the source if the source is changed without a
  new version. Contract versions are therefore unique per definition and start
  date.

## Open questions
- Which real Brandenburg catalog and payer data will be loaded, and who verifies
  it?
- Should activity rules be applied automatically, and if so with which
  confirmation step?
- Do reports need a stored contract id on the snapshot for dispute handling?
