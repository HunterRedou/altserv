# ADR-004: Shared database with tenants

## Problem
How to isolate multiple care companies (tenants) in one application.

## Alternatives
- One database per tenant
- One schema per tenant
- One database, one schema, `tenant_id` on tenant-bound tables

## Decision
One database and one schema. Every tenant-bound table carries `tenant_id`.
Row Level Security is planned from the start (doc 3).

## Reasons
- New tenants are rows, not databases or generated code.
- Migrations run once, not once per tenant.
- Cross-tenant reporting and support are possible.
- Lower operational cost for the first version.

## Consequences
- Every tenant-bound query must filter by `tenant_id`.
- Composite foreign keys prevent cross-tenant references.
- RLS policies and negative isolation tests are required.
- The tenant table is `tenants`; the column is `tenant_id` (migration 008 renamed `firms`).
- User roles are `platform_admin`, `chef`, `employee` (migration 008).

## Risks
- One missing filter leaks data between tenants.
- A `platform_admin` currently still needs a `tenant_id`; this may change.

## Open questions
- Should `platform_admin` users be tenant-bound or platform-level?
- `tenants.hashed_password` is still present but unused. Login should belong to users only.
