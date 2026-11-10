# ADR-010: Row Level Security as tenant defense in depth

## Problem
Application code can forget a `WHERE tenant_id = ...` clause. A single missed
filter would leak data between care companies.

## Alternatives
- Application filters only
- Separate databases or schemas per tenant
- PostgreSQL Row Level Security in addition to application filters

## Decision
Enable and `FORCE ROW LEVEL SECURITY` on every tenant-bound table. Tenant
queries run in a transaction that executes `SET LOCAL ROLE altserv_app` and
`set_config('app.tenant_id', <uuid>, true)`. Policies compare `tenant_id` with
that transaction-local setting.

`altserv_app` is `NOLOGIN`, `NOSUPERUSER`, `NOBYPASSRLS`. The connection role
is a member of it; the role switch is transaction-local, so pooled connections
return to their original state.

## Reasons
- RLS is a second, independent check behind application filters.
- `FORCE ROW LEVEL SECURITY` also applies the policy to the table owner.
- A missing filter returns zero rows instead of another tenant's rows.
- Composite foreign keys (e.g. `sessions(user_id, tenant_id)`) block
  cross-tenant references at the schema level.

## Consequences
- Tenant-scoped code must go through `db.WithTenant` / `db.WithTenantTx`.
- Public paths (login lookup, tenant registration, verification, reset) still
  use the privileged connection and are reviewed separately.
- Integration tests must cover positive access, negative isolation, missing
  context and cross-tenant writes.
- Migrations must grant the app role access to new tenant-bound tables.

## Risks
- A query outside a tenant transaction silently sees nothing, which can look
  like missing data during development.
- The privileged connection remains powerful; a bug there bypasses RLS.
- `tenants_public_insert` allows public tenant creation; rate limiting and
  abuse protection are still open.

## Open questions
- Should the public registration path use a dedicated least-privilege role?
- How do platform admins legitimately read across tenants without weakening
  policies for everyone?
