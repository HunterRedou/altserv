# Backup policy

## Status

Draft for professional data-protection review. This policy covers the backup
of personal data and the recovery of the service. It is not legal advice.

## Purpose

Ensure that altserv can restore tenant data after loss or corruption, while
protecting the confidentiality and integrity of that data. A backup is a copy
of personal data and is subject to the same data-protection rules as the live
database.

## Scope

- the PostgreSQL database, including all tenant data
- the schema and migration version
- configuration needed to start the application (without secrets in the dump)
- the backup marker used by the monitoring alert

Not in scope as data backups, but needed for recovery:

- container images and deployment manifests
- secret-manager configuration and key versions
- TLS material
- the monitoring and alerting configuration

## Roles

| Role | Responsibility |
| --- | --- |
| Operator | Runs the backup job, monitors it, stores the copies, runs the restore drills. |
| Controller (tenant) | Defines the required recovery point and recovery time in the contract. |
| Subprocessor | Provides the backup repository, if used, under an Art. 28 agreement. |
| Data protection lead | Reviews the encryption, access and deletion rules. |

## Backup types

| Type | What it protects | Current asset |
| --- | --- | --- |
| Logical dump | Full database at a point in time; portable across versions | `deployments/docker/backup.sh` |
| WAL archive plus base backup | Point-in-time recovery (PITR) to a chosen time | Operator's backup product, for example pgBackRest or WAL-G |
| Managed provider snapshot | Provider-level recovery | Provider feature, if used |
| Configuration backup | Rebuild the deployment | Repository plus secret-manager export |

The logical dump alone is not a PITR solution. A production launch must have a
PITR capability with a tested restore. The repository documents the procedure
but does not ship the backup product.

## Encryption

- Backups must be encrypted at rest with a key that is not stored next to the
  backup.
- Use the backup product's encryption or an encrypted volume. Document which.
- Encrypt in transit.
- Keep the backup key in the secret manager or KMS, with a named owner and a
  recovery procedure.
- The database dump contains encrypted patient fields, but also unencrypted
  L3 fields such as visits, reports and billing lines. Treat the dump as L3
  data.
- If a backup key is exposed, follow `../runbooks/key-rotation.md` and
  `incident-response.md`.

## Retention

| Copy | Proposed retention | Notes |
| --- | --- | --- |
| Daily logical dump | 14 days | Enough for a short rollback; confirm with the tenant. |
| Weekly logical dump | 8 weeks | Optional; protects against a slow corruption. |
| PITR WAL | 14 to 35 days | Depends on the product and the agreed RPO. |
| Monthly archive | Per contract and tax duty | May be needed for billing records. |
| Off-site copy | Same as the source | Different failure domain. |

The retention must match `retention-policy.md`. Do not keep a backup longer
than the purpose requires. Do not delete a backup that is under a legal hold.

## Storage and location

- Keep at least one copy in a different failure domain from the primary
  database.
- Prefer an EU/EEA location. Any third-country transfer needs a legal review
  and safeguards.
- Restrict access to the backup repository to named operators and the backup
  service account.
- Do not store backups on the same host or the same volume as the database.
- Protect the repository from deletion by the application account. A compromised
  app must not be able to wipe the backups.

## Access

- Backup access is a privileged action.
- Use a named account and MFA where the provider supports it.
- Log every restore and every repository change.
- Do not download a production backup to a laptop. Restore it in the
  provider's environment or a controlled scratch host.
- Delete a scratch copy after the drill.

## Restore drills

A backup that has never been restored is not a backup.

- Run a logical restore drill at least monthly. Use
  `deployments/docker/restore-drill.sh`.
- Run a PITR drill at least quarterly, or per the contract.
- Record: date, operator, backup file or target time, duration, row counts,
  schema version, failures and follow-up.
- Test the restored copy with the application before declaring success.
- Verify that the audit triggers exist after the restore.
- Verify that the application role cannot update, delete or truncate audit
  events.

## Monitoring

- The backup job touches `BACKUP_MARKER_FILE` only after a successful,
  verified dump.
- The application exports `altserv_backup_age_seconds`.
- `AltservBackupStale` fires when the age exceeds 26 hours.
- `AltservBackupMarkerMissing` fires when the marker is configured but cannot
  be read.
- Monitor the backup job itself as well. The marker only proves that the last
  run reached the marker, not that the repository is healthy.
- Test the alert by letting a staging backup age past the threshold.

## Deletion in backups

- A deleted live row can remain in a backup until that backup expires.
- Document the maximum window in the privacy notice.
- If a data subject requests erasure, explain the backup window and the
  restoration risk.
- When a backup is restored for production, re-apply lawful deletions that
  happened after the backup was taken.
- Do not restore an old backup over the live database without an incident
  decision and a written record.

## RPO and RTO

The contract must state:

- **RPO** (recovery point objective): how much data may be lost.
- **RTO** (recovery time objective): how long a restore may take.

Do not invent these values. The operator and the tenant must agree them and
test them. A logical dump taken every 24 hours implies an RPO of up to 24 hours.
PITR can reduce the RPO to minutes, but only if the WAL archive is healthy.

## Subprocessors

If a provider stores the backups:

- sign an Art. 28 agreement
- record the location and the transfer basis
- confirm encryption and access control
- confirm the deletion and return process at contract end
- include the provider in the breach notification path

## Gaps

- The repository does not ship a PITR product or an automated PITR drill.
- The logical dump script is a starting point, not a full backup system.
- There is no automated test that restores into a fresh environment and runs
  the application test suite.
- The backup marker only proves that the script reached the marker.
- There is no documented backup-key recovery test.
- The off-site copy and its retention are operator tasks.

## Open questions

- What RPO and RTO will the contract promise?
- Which backup product is used, and where is the repository located?
- How long are WAL segments kept?
- Who holds the backup encryption key, and who is the deputy?
- How often is the PITR drill run, and who signs the record?
- How is a deletion request handled inside the backup window?
