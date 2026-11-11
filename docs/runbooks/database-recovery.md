# Runbook: database recovery and restore drills

## Goal
Restore altserv after data loss or corruption without guessing. A backup that
has never been restored is not a backup.

## What must exist before production
- Continuous WAL archiving plus a base backup, for example pgBackRest, WAL-G
  or the managed provider's point-in-time-recovery (PITR) feature.
- A daily logical dump from `deployments/docker/backup.sh` as a second,
  independent copy for small installations.
- An off-site copy in a different failure domain.
- Encryption at rest for backups and a documented key owner.
- Agreed RPO and RTO values. Do not invent them; the operator and the customer
  must sign them off.
- A restore drill schedule. Restoring the newest backup into a scratch database
  proves the dump; a PITR drill proves the WAL archive.

## Signals
- `altserv_backup_age_seconds` grows beyond 26 hours. The `AltservBackupStale`
  alert fires.
- The database is unreachable and `/readyz` returns `503`.
- A bad migration or a destructive statement is reported.

## 1. Stop and assess
1. Do not restore over the running production database.
2. Record the time, the affected database, the last known good time and the
   suspected cause.
3. Stop application writes if corruption is ongoing. Scale the app to zero or
   point it at a maintenance page. Do not delete audit rows.
4. Preserve the current database volume and logs for analysis.

## 2. Verify the backup chain
```bash
# pgBackRest example. Use the provider's equivalent command otherwise.
pgbackrest --stanza=altserv info

# Logical dump check without a restore:
pg_restore --list /var/backups/altserv/altserv-YYYYMMDDTHHMMSSZ.dump | head
```

Check:
- the newest backup finished successfully
- the WAL archive has no gap between the base backup and the target time
- the backup belongs to the expected database and schema version

## 3. Logical restore drill (fast, non-destructive)
```bash
export ADMIN_DB_URL='postgres://admin@db-host:5432/postgres?sslmode=require'
export BACKUP_FILE=/var/backups/altserv/altserv-YYYYMMDDTHHMMSSZ.dump
./deployments/docker/restore-drill.sh
```
The script creates a database named `altserv_drill_*`, restores the dump,
prints row counts and drops the scratch database. It refuses any other name.

## 4. Point-in-time restore (PITR)
Run this on a new host or a new volume, never in place.
```bash
# Example with pgBackRest. The target is the last known good time in UTC.
pgbackrest --stanza=altserv --type=time \
  --target='2026-11-11 20:00:00+00' \
  --target-action=promote restore

# Start PostgreSQL, then verify before switching traffic.
pg_ctl -D /var/lib/postgresql/data start
```
Verify:
```sql
SELECT version();
SELECT max(version_id) FROM goose_db_version;
SELECT count(*) FROM tenants;
SELECT count(*) FROM audit_events;
SELECT tgname FROM pg_trigger WHERE tgrelid = 'audit_events'::regclass;
```
The goose version must match the deployed application. The audit triggers must
be present. If the restore is from before a migration, deploy the matching
application version first, or migrate the restored copy forward in a separate
step.

## 5. Switch traffic
1. Put the app into maintenance or scale it down.
2. Point `DB_URL` at the restored database through the secret manager.
3. Start one instance. Check `/readyz`, `/healthz` and the login path.
4. Start the remaining instances.
5. Watch error rate, latency, pool use and slow queries for at least 30 minutes.
6. Record the actual RPO (data lost between the target time and the incident)
   and RTO (time to service).

## 6. After the restore
- Run the integration test suite against the restored copy if possible.
- Verify that audit rows are append-only and that the application role has no
  `UPDATE`, `DELETE` or `TRUNCATE` on `audit_events`.
- Rotate database credentials if the incident involved a credential leak.
- Write a postmortem and add a regression test or alert for the failure mode.

## Restore drill record
Keep one record per drill:
- date and operator
- backup file or PITR target
- restore duration
- row counts and schema version
- checks that failed
- follow-up owner and due date

## Known gaps
- The logical dump restores data, not the exact point in time. PITR needs the
  WAL archive and is a separate control.
- The repository does not yet ship an automated PITR drill. The operator must
  run it against the chosen backup product.
- A database superuser can disable the audit triggers. A restore does not prove
  tamper evidence; an external log sink or WAL archive is still required.
