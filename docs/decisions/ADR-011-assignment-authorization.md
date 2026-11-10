# ADR-011: Assignment-based patient authorization

## Problem
Tenant isolation is not enough. Inside a care company, an employee must not
see every patient just because both belong to the same tenant. Doc 4 also says
a platform admin must not automatically get access to health data.

## Alternatives
- Tenant-wide access for every authenticated user
- Role-only access (`chef` sees all, `employee` sees all)
- Assignment-based access with a validity window and revocation
- Break-glass access for platform admins from day one

## Decision
Access to patient data is decided per request:

```
chef            -> all patients in the chef's own tenant
employee        -> only patients with a currently valid, non-revoked assignment
platform_admin  -> no automatic patient access
```

`patient_assignments` stores `tenant_id`, `patient_id`, `employee_id`,
`assigned_by`, `assignment_type` (`regular` or `substitute`), `valid_from`,
`valid_until`, and `revoked_at`. Composite foreign keys keep patient,
employee and assigner inside the same tenant.

Only a `chef` may create, list or revoke assignments. Employees cannot grant
themselves rights. Every change and every denied access attempt is audited.

The object-level check runs in the service/query path on every relevant
request, not only in HTTP middleware. The employee list uses a single SQL query
that joins the active assignment, so unauthorized patients are filtered before
decryption.

## Reasons
- A missed tenant filter is already covered by RLS; a missed assignment check
  is covered here.
- Validity and revocation are evaluated against the database clock on every
  request, so access disappears immediately.
- A platform admin can manage tenants and users without silently reading care
  documentation.
- Keeping the rule in one `AccessChecker` implementation avoids divergent
  copies in handlers.

## Consequences
- Employees need an explicit assignment before any patient request succeeds.
- An unassigned patient is reported as `404` to avoid leaking patient existence.
- Assignment changes and denied access produce audit events.
- Future features (visits, reports, templates) must reuse the same
  `CanAccessPatient` check instead of inventing a second rule.
- The platform-admin restriction is recorded in
  `docs/platform-admin-patient-access-reminder.txt` for later review.

## Risks
- A chef can assign any employee in the tenant; no four-eyes review exists yet.
- The in-memory login rate limiter is per instance, not distributed.
- Assignment rows are retained after revocation for audit purposes; retention
  rules still need to be defined.
- Break-glass support access does not exist. If it is added, it must be
  explicit, time-limited, reasoned and audited.

## Open questions
- Should substitute assignments require a reason or a link to the absent
  employee?
- Should chefs be able to delegate assignment management to a team lead?
- Which retention period applies to revoked assignments?
