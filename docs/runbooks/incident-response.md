# Runbook: incident response

## Goal
Limit harm, keep evidence, restore service and report what must be reported.
This runbook covers the technical response. The legal and data-protection
decisions are in `docs/compliance/incident-response.md`.

## Severity
| Level | Example | First response |
| --- | --- | --- |
| SEV1 | Data leak, key compromise, production down for all tenants | Page on-call immediately, open an incident channel |
| SEV2 | One tenant cannot work, high error rate, backup chain broken | Respond within the agreed support window |
| SEV3 | Degraded performance, single job failing, one user blocked | Handle in business hours |

## Roles
- **Incident commander**: decides, coordinates, does not debug.
- **Operations lead**: runs the technical mitigation.
- **Scribe**: keeps a timestamped log of actions and evidence.
- **Communications lead**: updates customers and management.
- **Legal/data protection**: decides on breach notification.

One person may hold several roles in a small team. Write down who held which
role.

## 1. First 15 minutes
1. Confirm the report. Do not trust a single alert without checking `/healthz`,
   `/readyz`, `/metrics` and the logs.
2. Declare a severity and open a channel. Start the timestamped log.
3. Protect people and data before restoring convenience:
   - if credentials or keys may be exposed, rotate them
   - if an account is compromised, revoke its sessions and disable it
   - if data is leaking, block the path at the proxy or stop the instance
4. Preserve evidence. Do not delete audit rows, logs, database volumes or
   container images. Do not rebuild the host before a snapshot if compromise is
   suspected.

## 2. Triage
Check in this order:
```bash
curl -fsS https://altserv.example.invalid/healthz
curl -fsS https://altserv.example.invalid/readyz
curl -fsS -H "Authorization: Bearer $METRICS_TOKEN" \
  https://altserv.example.invalid/metrics | grep -E \
  'altserv_http_requests_total|altserv_http_request_duration|altserv_db_pool|altserv_db_slow_queries|altserv_backup_age|altserv_login_failures|altserv_job_errors'
```
Then:
- request log: method, route, status, request id. It contains no query string
  and no body.
- database: `pg_stat_activity`, locks, replication, disk space.
- backup: `altserv_backup_age_seconds` and the backup job log.
- recent deploys, migrations and configuration changes.

## 3. Playbooks

### Suspected account takeover
1. Revoke the user's sessions in the database and disable the account.
2. Check `audit_events` for the actor: login, patient reads, exports, role
   changes.
3. Reset the password and require a new login.
4. Check whether the role was escalated. A `chef` cannot create a
   `platform_admin`; a platform admin can assign any role.
5. Preserve the audit rows. They are append-only.

### Data leak or unauthorised read
1. Stop the leaking path. Do not delete evidence.
2. Identify the tenants, patients and time window from `audit_events` and the
   access logs. Do not copy patient data into the incident channel.
3. Notify the data-protection lead. A personal-data breach may require a
   notification within 72 hours of becoming aware.
4. Rotate exposed credentials and keys.
5. Write a factual timeline for the notification.

### Database unavailable or corrupted
1. Follow `docs/runbooks/database-recovery.md`.
2. Do not restore over the live database.
3. Check the backup age before assuming a recovery point.

### Key or secret exposure
1. Follow `docs/runbooks/key-rotation.md`.
2. Assume all data protected by the key is exposed until proven otherwise.
3. Rotate the database password, metrics token and TLS material as well.

### Backup failure
1. Check the backup job, disk space and the repository.
2. Do not touch the marker file to silence the alert. The alert reflects the
   last real backup.
3. Run a restore drill after the backup succeeds.

## 4. Communication
- Internal update at least every 30 minutes for SEV1/SEV2.
- Customer wording must be factual: what is affected, what is being done, what
  the customer should do. Do not speculate about cause or blame.
- Do not include patient names, health data or credentials in tickets, chat or
  status pages.

## 5. Recovery
1. Remove the cause, not only the symptom.
2. Restore from a verified backup or fail over.
3. Verify readiness, login, a patient read, a report read and a billing export.
4. Watch error rate, latency, pool use and slow queries for at least 30 minutes.
5. Keep the incident channel open until the commander closes it.

## 6. After the incident
- Blameless postmortem within the agreed period.
- Timeline, impact, root cause, detection gap, response gap.
- Follow-up actions with owner and due date.
- Add a regression test, alert or runbook change for the failure mode.
- Record whether a data-protection notification was required and who decided.

## Evidence to preserve
- application and proxy logs for the window
- `audit_events` for the affected tenants
- database snapshots and WAL archive
- container image digests and deployment records
- secret-manager audit log
- the incident log and decisions

## Known gaps
- There is no automatic session revocation API yet. Use a database update or a
  documented administrative procedure until one exists.
- There is no external append-only audit sink yet. A compromised superuser can
  still alter the local audit table.
