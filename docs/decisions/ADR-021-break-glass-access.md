# ADR-021: Break-glass access for platform administrators

## Problem

The access-control policy and the platform-admin reminder said that a
`platform_admin` must not get automatic access to patient health data. The
system enforced that rule, but it had no controlled path for the rare support
case where a tenant needs help with one patient. Without such a path, support
would have to ask the tenant for a screen share or an export, or the operator
would be tempted to add a hidden superuser route.

The step had to add support access without weakening the default rule:

- one named patient, not a whole tenant
- a tenant decision, not an operator decision
- a short time window
- read-only
- audited in the patient's tenant
- no free-text health data in audit metadata

## Alternatives

- Give `platform_admin` a read-only role across all tenants.
- Add a tenant-wide support grant.
- Add a permanent support account in each tenant.
- Let a platform admin approve their own request.
- Use a signed support ticket in an external system as the only control.
- Add a superuser database path outside the application.
- Allow write access during break-glass.

## Decision

### Scope

- Break-glass access is patient-scoped. A grant names exactly one tenant and one
  patient. There is no tenant-wide grant.
- Access is read-only. The only data route is
  `GET /api/break-glass/grants/{id}/patient`. It returns the patient record. It
  does not expose visits, reports, activities, billing data or documents.
- A grant does not change any role. `platform_admin` stays a non-patient role.
  The normal patient API still returns `403` for a platform admin.

### Request and approval

- Only a `platform_admin` may request a grant.
- The request must name `tenant_id`, `patient_id` and a reason.
- The patient must exist in that tenant. The existence check runs inside the
  tenant-scoped transaction, so a platform admin cannot probe another tenant.
- Only a `chef` of the patient's tenant may approve or deny. An `employee`
  cannot decide, even with a valid assignment.
- A requester cannot approve their own grant. Because only a platform admin can
  request and only a chef can approve, the two identities are always different.
- Approval takes a duration from 1 to 60 minutes. The default is 30 minutes.
- The requester or a chef of the patient's tenant can revoke a pending or
  approved grant.
- A denial is terminal. A revoked grant cannot be reactivated. A new request is
  required.

### Time handling

- `requested_at`, `decided_at`, `expires_at`, `revoked_at` and `last_used_at`
  are `TIMESTAMPTZ`.
- The active-grant query checks
  `status = 'approved' AND revoked_at IS NULL AND expires_at > clock_timestamp()`.
  `clock_timestamp()` is used instead of `NOW()` so a long transaction cannot
  use a stale time.
- The API reports an approved grant as `expired` when the stored expiry has
  passed. The stored status stays `approved`; expiry is derived, not written.

### Tenant scoping

- The grant row belongs to the tenant that owns the patient, not to the tenant
  of the requesting platform admin. This lets the tenant chef decide and keeps
  the grant inside the tenant's retention and deletion boundary.
- The table has `ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY`. The
  policy compares `tenant_id` with `app.tenant_id`.
- The requester may belong to another tenant. `requested_by` is therefore a
  plain foreign key to `users(id)`, not a composite tenant reference.
- Every request, decision, revocation and read runs through `withTenant` for the
  patient's tenant. There is no privileged cross-tenant query in the request
  path.
- Because a platform admin does not share the patient's tenant, the requester
  must pass `tenant_id` explicitly. It is a query parameter for the list and
  patient routes and a body field for request and revoke.

### Audit

The following actions are written in the patient's tenant:

- `break_glass.requested`
- `break_glass.approved`
- `break_glass.denied`
- `break_glass.revoked`
- `break_glass.listed`
- `break_glass.patient_read`
- `access.denied` for a forbidden attempt

Metadata contains ids, counts, duration, expiry and the previous status. It does
not contain the patient name, the free-text reason, the decision reason or any
health data. The reason stays in the tenant-bound grant row.

The read audit event is written in the same transaction as the read. If the
audit write fails, the read fails. A support read without evidence is not
allowed.

A forbidden attempt commits its `access.denied` row before the API returns
`403`. The transaction contains no business write at that point. Without this,
the normal rollback on a rejected request would discard the denial evidence.
This commit path is limited to `ErrForbidden`; every other error still rolls
back.

### Patient read path

The normal patient service denies `platform_admin`. Break-glass uses a separate,
narrow method `patients.Service.GetForSupport`. Only
`breakglass.Service.ReadPatient` may call it, and only after an active grant is
validated. The method has no role check on purpose, so it must not be wired to a
normal handler.

## Reasons

- A patient-scoped grant is the smallest useful support permission. It does not
  open a whole tenant.
- Requiring a tenant chef to approve keeps the controller in control of the
  health data. The operator cannot grant itself access.
- A short window limits the value of a stolen session or a forgotten grant.
- Read-only support avoids a second write path with its own validation and audit
  rules.
- `clock_timestamp()` closes the stale-time gap in a long transaction.
- A tenant-bound row means the grant appears in the tenant's audit trail and
  follows the tenant's retention rules.
- Keeping the free-text reason out of audit metadata follows the audit rule that
  metadata must not contain health data or free text.

## Consequences

- A support case now needs a tenant chef to be reachable. Out of hours, the
  tenant must nominate an on-call chef or accept a delay.
- The platform admin needs a session in its own tenant and must know the target
  tenant and patient id. The support process must give both.
- The grant list for a platform admin is scoped to the named tenant and to the
  own requests. There is no global list.
- The feature does not fix the separate limitation that a `platform_admin` is
  stored inside one tenant. Platform staff must still be kept in an
  operator-only tenant.
- The grant table is append-by-update only for the application role. A grant is
  never deleted through the API, so the decision evidence stays with the tenant.
- Tenant deletion now also cascades break-glass grants. The append-only audit
  trigger still blocks a tenant delete that has audit rows. This does not change
  the tenant termination gap.

## Risks

- A tenant chef may approve without checking the request. The reason field, the
  audit trail and the short window reduce, but do not remove, this risk. The
  operator must define a verification step.
- A leaked platform-admin session can request access, but it still needs a chef
  approval. A leaked chef session can approve, but the read still needs the
  platform-admin session.
- A grant left open for 60 minutes is a 60-minute exposure. Revocation is
  immediate, and the review process must check open grants.
- The read returns the full patient record, not a masked view. A support workflow
  that needs less data would need a narrower response.
- The API reports expiry from the application clock for display. The security
  check is in SQL, so a skewed application clock cannot extend access.

## Open questions

- Which support cases justify break-glass, and who verifies the ticket before a
  chef approves?
- Should the patient read return a reduced field set for support?
- Should a grant require a second chef approval above a threshold?
- Should there be a tenant-wide grant for incident response, with stronger
  controls?
- How long must a break-glass grant be retained after the tenant ends?
- Should open grants appear in the operations dashboard and alert when unused or
  about to expire?
