# Runbook: deployment and controlled restart

## Goal
Deploy a new altserv version without dropping requests or corrupting data.

## Preconditions
- `gofmt`, `go build`, `go vet`, `go test ./...` and the DB-backed race tests
  are green.
- The image is built from a clean commit and tagged with the commit digest.
- A verified backup exists. For a schema change, the restore drill passed.
- The migration is backward compatible with the running version (expand and
  contract). If it is not, use a two-step deploy.
- The change was reviewed. No secret is in the image or the repository.

## Build and scan
```bash
docker build -f deployments/docker/Dockerfile -t altserv:"$GIT_SHA" .
docker run --rm altserv:"$GIT_SHA" --help || true
# Scan the image with the operator's approved scanner before pushing.
docker push registry.example.invalid/altserv:"$GIT_SHA"
```

## Migrations
Run goose before the new application only when the migration is backward
compatible with the old version.
```bash
goose -dir internal/db/sql/schema postgres "$DB_URL" status
goose -dir internal/db/sql/schema postgres "$DB_URL" up
```
For a destructive or non-backward-compatible change:
1. deploy the expand step
2. deploy the application that writes both shapes
3. backfill
4. deploy the application that reads the new shape
5. deploy the contract step in a later release

Never run a destructive migration and the code that depends on it in the same
step without a tested rollback.

## Rolling restart
The process handles `SIGTERM` in this order:
1. `/readyz` starts returning `503` and the load balancer stops new traffic.
2. The process waits `SHUTDOWN_DRAIN_DELAY` (default 5s).
3. The HTTP server stops accepting connections and waits up to 10s for
   in-flight requests.
4. The process exits.

With systemd:
```bash
systemctl restart altserv
```
With Docker Compose:
```bash
docker compose -f deployments/docker/docker-compose.yml up -d --no-deps app
```
With Kubernetes, set `terminationGracePeriodSeconds` to at least
`SHUTDOWN_DRAIN_DELAY + 15s` and use the readiness probe on `/readyz`.

Do not send `SIGKILL` during a normal deploy. It skips the drain and can cut a
report or billing transaction.

## Authentication after deploy
- The migration `internal/db/sql/schema/022_auth_hardening.sql` adds MFA,
  password reset, user disable and the session purge.
- Back up `MFA_ENCRYPTION_KEY` (or the derived key source) with the other
  secrets. A lost MFA key forces every privileged user to re-enroll.
- A `chef` or `platform_admin` without MFA gets a restricted session and must
  enroll before other routes work. Plan the first login for each privileged
  account.
- Tell users to store their recovery codes. There is no support bypass.
- `SESSION_PURGE_INTERVAL` controls the background purge. The default is 1h.
- A password reset token is returned to the issuer. No email is sent. Hand the
  token over out of band.

## Verify
```bash
curl -fsS https://altserv.example.invalid/healthz
curl -fsS https://altserv.example.invalid/readyz
curl -fsS -H "Authorization: Bearer $METRICS_TOKEN" \
  https://altserv.example.invalid/metrics | grep -E \
  'altserv_http_requests_total|altserv_http_request_duration|altserv_db_pool|altserv_db_slow_queries'
```
Check:
- `up{job="altserv"} == 1` in Prometheus
- error rate below the alert threshold
- p95 latency unchanged
- pool use below 80 percent
- no new slow-query alert
- an employee login, a privileged MFA login, a patient read and a report read
  work

## Rollback
1. Redeploy the previous image digest.
2. Do not roll back a migration that has already dropped or rewritten data.
   Restore the verified backup instead, or ship a forward fix.
3. If the previous version cannot read the new schema, restore the backup and
   redeploy the matching application version.
4. Record the rollback and the reason.

## Controlled restart for maintenance
1. Announce the window.
2. Check that no billing run is being approved and no report is mid-correction.
3. Take a backup or confirm the latest one.
4. Restart one instance, verify readiness, then continue with the next.
5. Keep the drain enabled even for a maintenance restart.

## Known gaps
- The repository has no Kubernetes manifests. The Docker Compose file is a
  template, not a production orchestrator.
- Image signing and SBOM generation are operator tasks and are not automated
  here.
