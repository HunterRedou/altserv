# Runbook: key and credential rotation

## Scope
- `PATIENT_ENCRYPTION_KEY` / `PATIENT_KEY_VERSION` (AES-256-GCM, patient PII)
- `PATIENT_SEARCH_KEY` / `PATIENT_SEARCH_KEY_VERSION` (HMAC-SHA256, blind index)
- database credentials
- `METRICS_TOKEN`
- backup repository encryption
- TLS certificates

## Rules
- Generate keys with a cryptographically secure source:
  `openssl rand -base64 32`.
- Store keys in a secret manager or KMS. Never commit them, never put them in a
  container image and never log them.
- The AES key and the search key must never be the same value. The application
  rejects reuse at startup.
- Keep the previous key available for the rollback window. Destroy it only
  after the new key is verified and the retention period for backups allows it.
- Rotate after any suspected exposure, not only on a schedule.

## Current application constraint
The running application decrypts with one active key version at a time. Rows
store their `key_version`, but the process does not load a key ring. Therefore:

1. Data must be re-encrypted with the new key **before** the new key becomes
   active.
2. The re-encryption job must be written, reviewed and tested before the first
   production rotation. It does not exist in this repository yet.
3. Do not simply change `PATIENT_ENCRYPTION_KEY` in `.env` and restart. Existing
   rows would become unreadable.

The same applies to `PATIENT_SEARCH_KEY`: changing it invalidates every search
token until the index is rebuilt from decrypted names.

## 1. Prepare
1. Take a verified backup and run the logical restore drill.
2. Choose a maintenance window. Writes must be paused during the re-encryption
   unless the job is designed for online rotation.
3. Confirm the new key is stored in the secret manager and is readable by the
   deployment, but not yet active.
4. Confirm the rollback key is still available.

## 2. Rotate the patient AES key
1. Generate the new key and a new version number (`old + 1`).
2. Run the re-encryption job:
   - read each encrypted column with the old key
   - encrypt with the new key
   - update the row's `key_version`
   - commit in bounded batches per tenant
   - record progress outside patient data
3. Verify:
   - every encrypted column has the new `key_version`
   - a sample decrypts with the new key
   - row counts per table match the pre-rotation counts
   - a second run is a no-op
4. Deploy the new `PATIENT_ENCRYPTION_KEY` and `PATIENT_KEY_VERSION`.
5. Start one instance, log in with a test tenant and read a patient, an
   activity, a report and a billing export.
6. Keep the old key in the secret manager for the rollback window.

## 3. Rotate the search HMAC key
1. Generate a new key and version.
2. Rebuild `patient_search_tokens`:
   - decrypt the searchable name fields with the AES key
   - compute tokens with the new HMAC key
   - replace the rows and set the new `key_version`
3. Deploy the new `PATIENT_SEARCH_KEY` and `PATIENT_SEARCH_KEY_VERSION`.
4. Verify a known patient can be found and a non-existent term returns nothing.
5. Keep the old key until the index is verified and the rollback window ends.

## 4. Rotate database credentials
1. Create a new login role or change the password with the provider.
2. Grant the same memberships, including `altserv_app`, and the same RLS
   behaviour. Do not grant superuser.
3. Update the secret manager.
4. Perform a rolling restart. Readiness fails during the drain, so the load
   balancer stops new traffic.
5. Verify connections with `pg_stat_activity` and the pool metrics.
6. Revoke the old credential after the rollback window.

## 5. Rotate the metrics token
1. Generate a token of at least 16 characters.
2. Update `METRICS_TOKEN` for the app and the Prometheus `credentials_file` in
   the same change.
3. Restart Prometheus and the app. Confirm `up{job="altserv"} == 1`.
4. Revoke the old token.

## 6. Rotate backup encryption
Follow the backup product's procedure. For pgBackRest, a new repository cipher
requires a new repository or a documented re-encryption. Do not overwrite the
only repository password: old backups become unreadable. Record the key owner
and the recovery location outside the primary site.

## 7. Rollback
- If the new key fails, stop the app, restore the old key and version, and roll
  back the data only if the re-encryption job can reverse it. Otherwise restore
  the verified backup.
- Never run a partial re-encryption without a recorded checkpoint and a way to
  resume or restore.

## Verification checklist
- [ ] backup restored in a drill
- [ ] new keys in the secret manager, old keys still available
- [ ] re-encryption counts match
- [ ] login, patient read, report read and billing export work
- [ ] search works and a missing term returns nothing
- [ ] old credentials revoked
- [ ] metrics scrape and alerts work
- [ ] rotation recorded in the change log
