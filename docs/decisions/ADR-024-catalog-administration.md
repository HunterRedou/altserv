# ADR-024: Catalog administration and verified catalog import

## Status

Accepted. Supersedes the access rule for global catalogs in ADR-013. The
tenant-bound contract and activity-rule rules from ADR-013 stay in force.

## Problem

ADR-013 made `service_catalogs` and `service_definitions` global, read-only
reference data. That protected tenants from one another, but it left no
controlled way to load a verified catalog:

- the application role had `SELECT` only, so a platform admin could not create
  a catalog through the API
- inserting rows with ad-hoc SQL bypassed validation and produced no audit event
- a catalog without provenance could be loaded by accident, which is exactly
  what doc 9 forbids
- a service definition could outlive its catalog, leaving a definition that no
  active catalog covers

Real Brandenburg catalog data still has to be professionally verified before it
is loaded. The software must make that review the normal path, not an optional
habit.

## Alternatives

- Grant `INSERT`, `UPDATE` and `DELETE` on the global tables to `altserv_app`.
- Keep catalog writes migration-only and require a new migration per catalog.
- Let each tenant maintain its own copy of the global catalog.
- Two reviewed `SECURITY DEFINER` functions plus an operator import tool.

## Decision

### Write path

Two `SECURITY DEFINER` functions from migration
`026_catalog_administration.sql` are the only write path:

- `create_service_catalog(code, name, region, source_note, valid_from, valid_until)`
- `create_service_definition(catalog_id, code, name, description, legal_area,
  unit, valid_from, valid_until)`

The functions are owned by the migration role, set `search_path = pg_catalog,
public`, validate the row and raise SQLSTATE `22023` for bad input and `23503`
for a missing catalog. `altserv_app` gets `EXECUTE` only; it keeps `SELECT`
only on the tables. The functions run inside the caller's tenant transaction, so
the audit event and the catalog write commit or roll back together.

Validation enforced in both Go and SQL:

- code, name and `valid_from` are required
- `source_note` is required and non-empty; it records who verified the catalog
- `valid_until` must be after `valid_from`
- the legal area is `sgb_xi` or `sgb_v`
- a definition must start inside its catalog window
- when the catalog has an end date, the definition must also end on or before
  it, so an open-ended definition cannot outlive its catalog

### Access rules

- Every authenticated role (`chef`, `employee`, `platform_admin`) may read
  global catalogs and definitions. Global reference data is not patient data.
- Only `platform_admin` may create a catalog or a definition. A tenant role must
  never change shared reference data, or one tenant could change another
  tenant's catalog.
- Tenant-bound contracts and activity rules keep the ADR-013 rules: `chef` and
  `employee` read, only `chef` writes, `platform_admin` has no automatic
  access.
- `platform_admin` still gets no automatic patient health-data access. Global
  catalog reads do not change the platform-admin reminder.

### Audit

Creating a catalog emits `service_catalog.created`; creating a definition emits
`service_definition.created`. Metadata carries identifiers, the legal area and
booleans, never the free-text source note or a description. A denied write
emits `access.denied` with the attempted action.

### Import tool

`cmd/catalogimport` loads one verified catalog version and its services from a
strict JSON manifest. It requires a `source_note`, rejects unknown fields, and
rejects a definition listed twice for the same `valid_from`. It supports
`-dry-run` to validate without writing, and `-tenant` to select the operator
tenant for the audit event. A real import runs in one transaction and writes one
`catalog.imported` audit event for the whole manifest, so a large catalog does
not flood the audit log.

The import tool uses `DB_URL`, which must be a member of `altserv_app`. It is an
operator tool, not an HTTP route.

## Reasons

- Keeping `INSERT` off the application role means a bug or a compromised
  tenant session cannot rewrite global reference data.
- `SECURITY DEFINER` with a fixed `search_path` gives one reviewed write path
  instead of raw SQL in several places.
- A mandatory source note makes provenance visible at the row level and in the
  API response.
- Containment keeps the catalog and its definitions consistent, so a report
  never references a service that the catalog no longer covers.
- One audit event per import keeps the audit trail readable.

## Consequences

- ADR-013's statement that `platform_admin` has no catalog access is superseded
  for global catalogs. It remains true for tenant contracts and rules.
- The catalog tables still have no RLS. Write access is the protected part;
  reads are intentionally open to authenticated roles.
- A catalog change does not rewrite past activity snapshots.
- A real import needs an operator tenant UUID and a connection that can assume
  `altserv_app`.
- The repository still ships no invented Brandenburg codes. Test data must be
  labelled as test data.

## Risks

- `valid_from` and `valid_until` are `timestamp` columns, while the read
  queries compare them to `clock_timestamp()`. The session time zone is
  `Europe/Amsterdam`, so a value near midnight can shift by the UTC offset.
  This is a pre-existing model issue; it is documented, not changed here.
- The import CLI writes the audit event with a `NULL` actor user id, because it
  runs without a logged-in user. The operator tenant and the request id are
  recorded, but not a person. Run imports from a controlled operator account.
- A `SECURITY DEFINER` function is powerful. Its `search_path` is fixed and it
  is granted only to `altserv_app`, but a future change must keep those
  properties.

## Open questions

- Which real Brandenburg catalog and payer data will be loaded, and who signs
  off the verification?
- Should a future version record the reviewing person in a dedicated column in
  addition to the source note?
- Should the timestamp columns move to `timestamptz` in a separate migration?
