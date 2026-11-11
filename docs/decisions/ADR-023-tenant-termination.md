# ADR-023: Tenant termination

## Problem

Tenant termination was a documented procedure, but it could not run. The
`audit_events` table has a `BEFORE DELETE` trigger that raises
`audit_events is append-only`. The tenant foreign key is `ON DELETE CASCADE`,
so deleting a tenant cascades to `audit_events` and the trigger aborts the
whole statement. A tenant with any audit event therefore could not be deleted
with `DELETE FROM tenants`. RESTRICT edges from care records to users made a
plain cascade fail as well.

The step had to make termination possible without weakening the append-only
guarantee for normal operation:

- ordinary code must still be unable to delete or rewrite audit rows
- termination must remove the audit trail in a controlled way
- termination must respect dependency order
- termination must not be an ad-hoc SQL statement

## Alternatives

- Drop the append-only trigger during termination.
- Grant `DELETE` on `audit_events` to the application role.
- Disable row-level security and delete with raw SQL.
- Keep audit events forever and never delete the tenant.
- Move audit events to an external sink before deletion.
- Use `session_replication_role = replica` to skip triggers.

## Decision

### Audit trigger gate

The trigger function now allows `DELETE` only when the transaction sets
`app.allow_audit_delete = 'true'`. `UPDATE` stays blocked for every caller.
The gate is transaction-local, so it cannot leak across pooled connections.

### Termination function

`terminate_tenant(p_tenant_id UUID)` is `SECURITY DEFINER` and runs as the
migration role. It:

1. Validates that the tenant exists.
2. Sets `app.allow_audit_delete = 'true'` for its own transaction.
3. Deletes in dependency order: billing findings, billing lines, billing runs,
   report activities, reports, visits, activities, template activities,
   templates, patient assignments, patient search tokens, break-glass grants,
   patients, legal holds, retention policies, deletion requests, deletion
   register, audit events.
4. Deletes the tenant row. The remaining tenant-bound rows cascade.

The function is granted to `altserv_app`, so the application can call it, but
it is intended as an operator procedure. It is not exposed as an HTTP route.

### Audit evidence

The function removes the audit rows. If the contract requires evidence to
survive termination, the operator must export the audit trail before calling
the function. An external append-only sink is the recommended long-term
solution.

### Access

- Termination is not a `chef` or `platform_admin` API. It is an operator
  procedure run against the database.
- `platform_admin` still only observes. It cannot terminate a tenant.

## Reasons

- A transaction-local gate keeps the append-only rule intact for all normal
  code while allowing a controlled exception.
- A `SECURITY DEFINER` function keeps the exception inside one reviewed
  procedure with one parameter.
- Dependency-ordered deletes avoid the RESTRICT edges without disabling
  foreign keys.
- Keeping termination out of the HTTP API avoids a dangerous one-click path.

## Consequences

- A tenant can now be terminated even when it has audit events and care data.
- The audit trail is removed by default. Operators must export it first if
  evidence must survive.
- The application role can call the function. Operators should restrict who
  can connect to the database instead.
- The trigger still blocks ordinary deletes, so the append-only guarantee
  holds for application code.

## Risks

- An operator can call the function with any tenant id. Database access control
  and auditing must compensate.
- A terminated tenant cannot be restored from the live database. Restore from
  backup.
- A restored backup can reintroduce a terminated tenant. Document the restore
  procedure.

## Open questions

- Should the audit trail be exported automatically before termination?
- Should termination require a second operator approval?
- Should there be an HTTP route for termination, and under which role?
- How long must a terminated tenant's audit evidence be kept?