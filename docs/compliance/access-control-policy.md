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
| `platform_admin` | Platform, but stored inside one tenant | Create users and assign any role inside its own tenant. Request a named-patient break-glass grant that a tenant `chef` must approve. | Read patient health data by default. No automatic patient access. No tenant-wide grant. No cross-tenant user API. |
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
| `platform_admin` | Denied, except one named patient under an active, tenant-approved break-glass grant. |

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

### Break-glass access

Break-glass access is implemented as a patient-scoped, read-only flow:

- A `platform_admin` requests one named patient in one named tenant. The request
  includes a reason. There is no tenant-wide grant.
- A `chef` of the patient's tenant approves or denies the request. A requester
  cannot approve their own grant. An `employee` cannot decide.
- Approval lasts 1 to 60 minutes. The default is 30 minutes.
- The only data route is `GET /api/break-glass/grants/{id}/patient`. It returns
  the patient record and nothing else. There is no write path.
- The requester or a chef of the patient's tenant can revoke a pending or
  approved grant. Revocation is immediate.
- The active check uses `clock_timestamp()`, so a long transaction cannot use a
  stale time.
- The grant row belongs to the patient's tenant and has row-level security. The
  requester may belong to another tenant, so it must pass `tenant_id`
  explicitly.
- Every request, decision, revocation, list and read is audited in the patient's
  tenant. The free-text reason is stored in the grant row, not in audit
  metadata.
- A forbidden attempt writes `access.denied`. The denial row is committed before
  the API returns `403`.
- A read needs an active grant and is audited in the same transaction. If the
  audit write fails, the read fails.

See `../decisions/ADR-021-break-glass-access.md`.

## Retention and deletion

- Every retention and deletion endpoint is chef-only. `platform_admin` and
  `employee` are denied.
- A tenant `chef` creates, updates, confirms, disables and lists retention
  policies. A policy starts disabled; only an explicit confirmation enables it.
  No retention period is seeded; the controller must confirm it after
  professional review.
- A tenant `chef` declares and lifts legal holds. A hold can be patient-scoped
  or tenant-wide. Active holds block deletion.
- Patient deletion needs four eyes: one tenant `chef` requests, a different
  tenant `chef` approves and executes. The requester cannot approve or execute.
  `platform_admin` is denied.
- The retention review reports candidate counts only. It never creates a
  deletion request and never deletes data.
- The deletion register is append-only evidence. It stores row counts, not the
  deleted personal data.
- Audit metadata for retention and deletion never contains patient names,
  free-text reasons, notes, legal-basis text or health data.

A `platform_admin` is stored with a `tenant_id`, like every user. It must not be
a member of a tenant that holds patient data. If it is, it can create a `chef`
account in that tenant and thereby reach patient data indirectly. The operator
must keep platform staff in an operator-only tenant, or add an explicit
separation that the code enforces. The current code does not enforce it.

There is also no cross-tenant user API. A `platform_admin` cannot administer
another tenant's users. This is a real limitation for support and must be
addressed before a multi-tenant production launch.

See `../platform-admin-patient-access-reminder.txt`.

## Tenant termination

- Tenant termination is an operator procedure, not an API. It runs as the
  migration role through `terminate_tenant(tenant_id)`.
- `platform_admin` and `chef` cannot terminate a tenant through the
  application. They can only request and approve care-data deletion.
- The append-only audit trigger still blocks ordinary deletes. Only the
  controlled function can remove audit rows, and only for its own transaction.

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
- Disabled accounts cannot log in and lose every session immediately.
- Role changes revoke the target user's sessions.

### Multi-factor authentication

- MFA is required for `chef` and `platform_admin`. `employee` accounts may log
  in without MFA; the enforcement point is the role, not the tenant.
- The second factor is TOTP: SHA-1, 6 digits, 30-second step, plus or minus one
  step. Recovery codes are an alternate second factor.
- A privileged login with MFA enabled returns `202` and a challenge. No session
  cookie is set until the second factor is accepted.
- A privileged login without MFA returns a restricted session with
  `mfa_enrollment_required: true`. Only `/api/me`, `/api/logout`,
  `/api/mfa/enroll` and `/api/mfa/confirm` are reachable. Every other route
  returns `403` until enrollment completes.
- Challenges are stored as hashes, live 5 minutes, allow at most 5 attempts and
  are consumed in the same transaction as the session.
- Recovery codes are shown once, stored as SHA-256 hashes and are single use.
  There are 10 codes. Regenerating them replaces the old set.
- The TOTP seed is encrypted with AES-256-GCM. The associated data binds the
  ciphertext to the key version, tenant and user. The MFA key is
  `MFA_ENCRYPTION_KEY`, or an HMAC derivation of `PATIENT_ENCRYPTION_KEY` when
  that variable is unset. The configuration rejects a direct reuse of the
  patient encryption key.
- Disabling MFA or replacing recovery codes requires the current password and a
  valid second factor. Both actions are audited.

### Password reset

- A public request endpoint always answers `202` with the same message. It never
  reveals whether the account exists.
- Reset tokens are stored as hashes, live 30 minutes and are single use.
- A confirmed reset changes the password, revokes every session for that user
  and writes an audit event in one transaction.
- A `chef` or `platform_admin` may issue a reset token for another user in the
  same tenant. The token is returned to the issuer because no email delivery is
  configured yet.
- A `chef` may not reset a `platform_admin`, and no user may use the admin
  endpoint for their own account.
- The public reset endpoints have a separate rate limit of 5 attempts per minute
  per source address.

### Session purge

- Expired and revoked sessions, expired challenges and expired reset tokens are
  deleted by `purge_expired_auth_rows()`.
- The function is `SECURITY DEFINER` and writes an `auth.sessions_purged` audit
  event per affected tenant, so the count and the evidence commit together.
- The purge runs at startup and then every `SESSION_PURGE_INTERVAL` (default
  one hour). The metric `altserv_sessions_purged_total` counts removed rows.

Not implemented, and required before production:

- Account lockout or a global login throttle that is not per process.
- SSO or an identity provider for staff.
- Email delivery for password reset and MFA changes. Tokens are handed over out
  of band.
- A user-facing session list. Sessions can be revoked in bulk by an
  administrator, but a user cannot see or revoke a single session.
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
- user create, role change, disable and enable
- MFA enrollment, MFA enabled, MFA disabled and recovery-code regeneration
- password reset requested, issued and completed
- session purge
- break-glass request, approval, denial, revocation, list and patient read
- audit log read
- denied access

Audit metadata must not contain patient names, notes or free-text health data.
Entity ids are allowed.

## Gaps

- No tenant-wide break-glass grant and no support UI. One grant covers one named
  patient, so an incident that spans many patients needs one request per
  patient.
- No shared rate-limit store.
- No automated access review.
- No external append-only audit sink.
- No email delivery for reset tokens or MFA changes.
- No user-facing session list or single-session revoke.
- The pre-tenant authentication lookups (login, session, MFA challenge and
  reset token) use the privileged connection because the tenant is not known
  yet. ADR-010 records this as a path to review separately. Before production,
  replace it with a narrow least-privilege function, not a superuser or
  `BYPASSRLS` role.
- The audit log is readable by `chef` for the own tenant; it is not readable by
  `employee`.
- `platform_admin` is tenant-bound and has no cross-tenant support API. It must
  not belong to a tenant that holds patient data.

## Open questions

- Who approves `platform_admin` access, and how often is it reviewed?
- Is a tenant allowed to grant a support person temporary access, and under
  which contract clause?
- Should session lifetime be shorter for `chef` and `platform_admin`?
- Which identity provider will be used for staff SSO?
