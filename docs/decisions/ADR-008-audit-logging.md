# ADR-008: Append-only audit logging

## Problem
Care software must show who accessed or changed patient data, when, and in
which tenant. Logs must be trustworthy and must not become a second copy of
the PII they protect.

## Alternatives
- Application log files only
- A generic `logs` table writable by the app
- An append-only `audit_events` table with RLS and a trigger

## Decision
Write structured audit events to `audit_events` inside the same transaction as
the business change. An event contains `tenant_id`, optional `actor_user_id`,
`action`, `entity_type`, optional `entity_id`, `request_id`, `occurred_at` and
JSONB `metadata`.

The application role may only `SELECT` and `INSERT` audit rows. A `BEFORE
UPDATE OR DELETE` trigger raises an exception, so rows cannot be rewritten even
by a privileged connection. RLS restricts every tenant to its own events.

Audit metadata is deliberately minimal (for example `{"status":"active"}`) and
must never contain patient names, free text or other unnecessary health data.

## Reasons
- The business change and its audit record commit or roll back together; no
  change exists without its event.
- Append-only plus RLS makes tampering and cross-tenant reads detectable and
  hard.
- `request_id` correlates an event with the HTTP request and server logs.
- A JSONB column can evolve without a migration per event type.

## Consequences
- Handlers that change data must pass the transaction's `*db.Queries` to the
  recorder; a separate connection would break atomicity.
- Failed access (e.g. an employee trying to read a patient) is recorded as
  `access.denied`, best effort, so a denied request does not turn into a 500.
- Development reset uses `TRUNCATE tenants CASCADE` because `DELETE` would hit
  the append-only trigger.
- Retention, export and legal deletion rules need an explicit policy.

## Risks
- Audit writes add latency and storage growth.
- A compromised application process can still write false events; only
  shipping events to an external append-only sink closes that gap.
- IP address and user agent are not stored yet.

## Open questions
- Retention period and archival target?
- Should audit reads require a separate `auditor` role?
- Do we ship events to an external WORM store in production?
