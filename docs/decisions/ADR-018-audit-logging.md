# ADR-018: Restricted audit reads, role changes and retention

## Problem
ADR-008 introduced an append-only `audit_events` table written inside the same
transaction as the business change. Doc 15 adds requirements that ADR-008 did
not close:

- role changes and user creation must be audited
- the log must have restricted read access, not only restricted writes
- a privileged connection must not be able to truncate the table
- reading the log is itself sensitive and must be auditable
- metadata must stay minimal, without health data or full before/after dumps
- legally required retention and deletion periods also apply to audit data

The application also needs a way for a chef to inspect the log without giving
every tenant user a read path.

## Alternatives
- Leave the log write-only and inspect it only with `psql`
- Let every `chef` and `platform_admin` read the log through the normal API
- Give the application role `SELECT, INSERT, UPDATE, DELETE` and rely on the
  trigger alone
- Implement automatic deletion of old events now, before the legal periods are
  verified
- Add a separate `auditor` role and a second service
- Return raw database rows, including all metadata, without filtering

## Decision

### Read API
- `GET /api/audit-events` is the only read path. It is restricted to `chef`.
- An `employee` and a `platform_admin` get `403`. A platform admin gets no
  automatic tenant access (ADR-004 and the platform-admin reminder). Future
  break-glass access must be explicit, time-limited and audited.
- The read is tenant-scoped by the session tenant and by RLS. A chef of tenant
  A never sees an event of tenant B.
- Filters are `action`, `entity_type`, `entity_id`, `from`, `to` and `limit`.
  Times are RFC 3339. The default limit is 50 and the maximum is 200. A value
  outside the range, a malformed UUID, a malformed time or `from >= to` returns
  `400`.
- The response contains the stored columns only: ids, action, entity type and
  id, `occurred_at`, `request_id` and the JSONB metadata. No patient name and no
  free text is added by the read path.

### Auditing the audit log
- A successful read writes `audit.listed` with the count and the applied
  filters. It does not contain the returned rows.
- A denied read writes `access.denied` with `attempted_action = audit.read`.
  The denial is best effort so a denied read never turns into a `500`.
- The read and its `audit.listed` event run in one tenant transaction, like
  every other business change.

### Role changes
- `PATCH /api/users/{id}` changes a role.
- A `chef` may move a user between `chef` and `employee`, in both directions.
- A `chef` may not assign or remove `platform_admin`. Only a `platform_admin`
  may assign any role.
- Nobody may change their own role. This prevents a chef from locking
  themselves out and prevents a privilege self-escalation.
- The last `chef` of a tenant cannot be demoted. The check counts chefs in the
  same tenant inside the transaction and returns `409` when only one remains.
- A role change writes `user.role_changed` with `old_role` and `new_role` only.
- Creating a user writes `user.created` with the new role only.
- A user of another tenant is not found (`404`), so the API does not reveal
  users outside the session tenant.

### Append-only hardening
- Migration 021 adds a statement-level `BEFORE TRUNCATE` trigger. The existing
  row-level trigger from migration 013 covers `UPDATE` and `DELETE` but not
  `TRUNCATE`.
- The application role is explicitly revoked from `UPDATE`, `DELETE` and
  `TRUNCATE` and keeps only `SELECT` and `INSERT`.
- Both guards are tested: an update, a delete and a truncate all fail, for the
  application role and for a privileged test connection.

### Retention and deletion
- Exact retention and deletion periods are a legal question. They are not
  invented here.
- The application role has no delete path. Automatic deletion is deliberately
  not implemented, because an unverified period could destroy evidence or
  violate a retention duty.
- A future purge must be a controlled maintenance procedure, not an API:
  - a separate database role that is not the application role
  - a documented legal basis and a verified period per event class
  - whole, time-based windows only, never single rows selected by actor or
    entity
  - the purge itself recorded outside the purged table, for example in the
    operator's change log and the external log sink
  - a restore and audit check afterwards
- The same rule applies to a tenant deletion: the tenant cascade must be
  reviewed against retention duties before it is used in production.

## Reasons
- A chef-only read matches the existing approval and billing model and keeps the
  audit log need-to-know.
- Denying `platform_admin` follows the platform-admin rule: no automatic
  health-data or tenant access.
- Auditing the read makes misuse of the log visible and answers "who looked at
  the log and when?".
- Explicit revokes plus both triggers make accidental or privileged rewriting
  fail loudly instead of silently.
- Auditing role changes closes the most direct privilege-escalation path.
- Refusing to invent retention periods follows the project rule that legal and
  catalog data must be professionally verified.
- A separate purge role keeps the append-only guarantee for the running
  application while leaving a lawful, auditable deletion path open.

## Consequences
- Every audit read adds one audit row, so the log grows with inspection.
- A chef who needs a complete export still has to page with `limit` and time
  filters.
- Role changes now require the current role and the new role to be valid for
  the actor. A tenant can no longer remove its last chef through the API.
- The audit log cannot be cleaned from the application. A production purge is
  an operational task with legal sign-off.
- A `platform_admin` cannot read a tenant's audit log without a future,
  explicit break-glass mechanism.

## Risks
- A database superuser can still disable the triggers or drop the table. The
  guards protect against the application role and accidental statements, not
  against a compromised database superuser. An external append-only sink or WAL
  archiving is the next step for tamper evidence.
- Audit metadata is only as good as its callers. A future handler could put
  free text into `metadata`; code review and tests must keep it to ids,
  statuses, counts and role names.
- `occurred_at` is a `TIMESTAMP` without time zone. Filters compare it as a
  `timestamptz` in the database session time zone. A future migration should
  move it to `TIMESTAMPTZ` to remove the ambiguity.
- A very large tenant will need pagination by cursor, not only `limit`.
- Retention is unresolved until legal review. The log grows without bound in
  the meantime.

## Open questions
- What retention period applies to login, patient access, billing and role
  events, and do they differ?
- Is a dedicated `auditor` role needed in addition to `chef`?
- Should audit events be streamed to an external WORM store for tamper
  evidence?
- Does a tenant deletion need to retain a pseudonymised audit subset?
- Should failed logins be recorded as `auth.login_failed`, and with which
  identifiers to avoid storing an email address unnecessarily?
- Should `occurred_at` be migrated to `TIMESTAMPTZ` before production?
