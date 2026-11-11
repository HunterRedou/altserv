# Access control policy

## Status

Draft for professional data-protection review. This document describes the
implemented access model and the controls the operator must add before
production. It is not legal advice.

## Principles

1. Least privilege. A user gets the smallest role and the smallest patient set
   that the task needs.
2. Tenant isolation is enforced in the database, not only in the application.
3. Patient access is assignment-based for employees.
4. `platform_admin` is not a patient-access role.
5. Every privileged action is audited.
6. Access is reviewed and removed when it is no longer needed.
7. Authentication and authorisation are separate. A valid session is not a
   permission.

## Roles

| Role | Scope | Can do | Cannot do |
| --- | --- | --- | --- |
| `platform_admin` | Platform, but stored inside one tenant | Create users and assign any role inside its own tenant. | Read patient health data by default. No automatic patient access. No cross-tenant user API. |
| `chef` | One tenant | Manage users, patients, assignments, contracts, templates, visits, reports, billing rules, billing runs, and read the audit log. | Cross tenant boundaries. Create or promote a `platform_admin`. |
| `employee` | One tenant, assigned patients | Read assigned patients, manage templates for assigned patients, start and complete assigned visits, create and submit reports, read reports. | Manage users, patients, assignments, contracts, billing or audit. Access a patient without a valid assignment. |

Role changes:

- A `chef` may change an `employee` to `chef` and a `chef` to `employee` inside
  the tenant.
- A `chef` may not create or promote a `platform_admin`.
- A `platform_admin` may assign any role inside its own tenant.
- No user may change their own role.
- The last `chef` in a tenant cannot be demoted.
- Every role change is audited with the old and new role.

## Tenant isolation

- Every tenant-bound table has `ENABLE ROW LEVEL SECURITY` and
  `FORCE ROW LEVEL SECURITY`.
- Tenant queries run as the non-superuser role `altserv_app`, which is not
  `BYPASSRLS`.
- The transaction sets `app.tenant_id` locally. The setting is cleared when the
  transaction ends, so a pooled connection cannot leak a tenant.
- The tenant id always comes from the session, never from a client header or a
  request body.
- Composite foreign keys `(id, tenant_id)` keep references inside one tenant.
- A missing tenant filter is a defect. Do not rely on the application alone.

## Patient access

| Actor | Access rule |
| --- | --- |
| `chef` | All patients in the own tenant. |
| `employee` | Only a patient with an assignment that is not revoked and whose validity contains `clock_timestamp()`. |
| `platform_admin` | Denied. |

The validity check uses `clock_timestamp()`, not `NOW()`, so a long transaction
cannot use a stale time. The check is in the service layer and is backed by the
assignment table. Revoking an assignment removes access immediately for new
requests.

`chef` can create, list and revoke assignments. `employee` cannot manage
assignments. This prevents an employee from granting themselves a patient.

## Platform administrator

`platform_admin` exists to operate the platform, not to read care data.

- No automatic patient access.
- No silent impersonation.
- Support access to tenant data requires an explicit instruction from the
  tenant and a documented legal basis.
- Future break-glass access must be:
  - requested for a named tenant and a named purpose
  - approved by the tenant or by an on-call policy
  - time-limited, for example 60 minutes
  - read-only where possible
  - audited with the actor, tenant, reason and expiry
  - reviewed after use

Break-glass is not implemented. Until it is, support must work with the tenant
on a screen share or with a tenant-provided export. Do not add a hidden
superuser path.

A `platform_admin` is stored with a `tenant_id`, like every user. It must not be
a member of a tenant that holds patient data. If it is, it can create a `chef`
account in that tenant and thereby reach patient data indirectly. The operator
must keep platform staff in an operator-only tenant, or add an explicit
separation that the code enforces. The current code does not enforce it.

There is also no cross-tenant user API. A `platform_admin` cannot administer
another tenant's users. This is a real limitation for support and must be
addressed before a multi-tenant production launch.

See `../platform-admin-patient-access-reminder.txt`.

## Authentication

Implemented:

- Email and password login.
- Argon2id password hash.
- Session cookie `__Host-session` with `HttpOnly`, `Secure`, `SameSite=Lax`
  and `Path=/`.
- Session token stored as a hash.
- CSRF token required on unsafe methods.
- Login rate limit of 10 attempts per minute per source address.
- Session expiry, default 24 hours, configurable up to 30 days.
- Logout revokes the session.
- Unverified tenants cannot obtain a session.

Not implemented, and required before production:

- Multi-factor authentication, at least for `chef` and `platform_admin`.
- A password reset flow with a single-use, expiring token.
- Account lockout or a global login throttle that is not per process.
- SSO or an identity provider for staff.
- A "disable user" action.
- A session list and "revoke all sessions" action for a user.
- A shared rate-limit store for multi-instance deployments.

Until these exist, the operator must compensate with a strong password policy,
a trusted proxy, network restrictions and monitoring of failed logins.

## Session and token rules

- Never log a session token, a CSRF token or a password.
- Never put a session token in a URL.
- Keep the session lifetime as short as the workflow allows.
- Revoke sessions when a user leaves, changes role, or is suspected of
  compromise.
- Rotate the CSRF token with the session.

## Database access

- The application uses a dedicated login role with membership in
  `altserv_app`.
- The application role is not a superuser and does not have `BYPASSRLS`.
- Migrations use a separate role.
- Human access to production uses a named account, not a shared password.
- Direct `psql` access is limited to on-call engineers and is audited by the
  database or the access proxy.
- Superuser access can disable the audit triggers. Treat it as a high-risk
  action with a four-eyes rule.
- The audit table grants `SELECT` and `INSERT` to the application role, and
  `UPDATE`, `DELETE` and `TRUNCATE` are revoked.

## Infrastructure and secrets

- Secrets live in a secret manager, not in the repository or the image.
- The AES key and the search HMAC key are different values.
- The `METRICS_TOKEN` protects `/metrics` in production.
- Backup repositories have their own credentials and encryption keys.
- Access to the secret manager is role-based and audited.
- Rotate on a schedule and after any suspected exposure. See
  `../runbooks/key-rotation.md`.

## Joiner, mover, leaver

| Event | Action |
| --- | --- |
| New tenant user | `chef` creates the account with the smallest role. No shared accounts. |
| New platform staff | Named account, least privilege, MFA, signed policy. |
| Role change | Four-eyes review, audit entry, session revocation if privileged. |
| Patient assignment | `chef` creates a time-bound assignment. |
| Assignment end | Revoke; do not wait for expiry if the task is finished. |
| Employee leaves | Revoke sessions, expire assignments, disable the account, keep records. |
| Tenant leaves | Follow `deletion-policy.md`. |
| Suspected compromise | Revoke sessions, rotate credentials, preserve evidence, follow `incident-response.md`. |

## Review

- Review tenant `chef` accounts at least quarterly.
- Review `platform_admin` accounts at least quarterly.
- Review database and infrastructure access at least quarterly.
- Review assignments when a care relationship ends.
- Review audit logs for denied access and role changes.
- Record each review with date, reviewer and findings.

## Audit

The following actions are audited:

- login and failed login (counted; the email is not logged)
- patient create, read, update and search
- assignment create and revoke
- catalog, contract, activity, template, visit and report actions
- report submit, approve, return and ready-for-billing
- billing generate, approve, void, export and rule creation
- user create and role change
- audit log read
- denied access

Audit metadata must not contain patient names, notes or free-text health data.
Entity ids are allowed.

## Gaps

- No MFA.
- No password reset.
- No user-disable action.
- No break-glass access.
- No shared rate-limit store.
- No automated access review.
- No external append-only audit sink.
- The audit log is readable by `chef` for the own tenant; it is not readable by
  `employee`.
- `platform_admin` is tenant-bound and has no cross-tenant support API. It must
  not belong to a tenant that holds patient data.

## Open questions

- Which roles need MFA at launch?
- Who approves `platform_admin` access, and how often is it reviewed?
- Is a tenant allowed to grant a support person temporary access, and under
  which contract clause?
- Should session lifetime be shorter for `chef` and `platform_admin`?
- Which identity provider will be used for staff SSO?
