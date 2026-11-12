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

## Break-glass after deploy
- The migration `internal/db/sql/schema/023_break_glass.sql` adds the
  `break_glass_grants` table. It needs no new secret.
- A `platform_admin` must name the target tenant and patient. Support must know
  both before a request.
- A `chef` of the target tenant must approve. Define an on-call `chef` for out
  of hours.
- Grants last 1 to 60 minutes. The default is 30 minutes.
- A grant is read-only. Do not add a write route.
- Monitor `break_glass.*` audit events and review open grants. Revoke a grant as
  soon as the support case ends.

## Retention and deletion after deploy

- The migration `internal/db/sql/schema/024_retention_deletion.sql` adds the
  retention and deletion tables, RLS, append-only triggers, the review
  function, and `patients.status_changed_at`. Run goose before the new
  application.
- `RETENTION_REVIEW_INTERVAL` controls the background review. The default is
  24h. The minimum is 1h and the maximum is 7d.
- The review job reports candidate counts only. It never deletes or creates
  deletion requests. Monitor `altserv_retention_reviews_total` and
  `altserv_retention_review_candidates_total`.
- No retention period is seeded. The controller must create and confirm a
  policy after professional review.
- Legal holds and deletion requests are chef-only. `platform_admin` is denied.
- The deletion register is append-only. Do not grant update or delete on it.
- The application role needs `DELETE` on `break_glass_grants` so a patient
  deletion can cascade. Migration 024 grants it.

## Tenant termination after deploy

- Migration `internal/db/sql/schema/025_tenant_termination.sql` adds the
  `terminate_tenant(tenant_id)` function and relaxes the audit trigger so a
  DELETE is allowed only when `app.allow_audit_delete = 'true'`.
- Termination is an operator procedure, not an API. Run
  `SELECT terminate_tenant('<tenant uuid>');` as the migration role or any
  role with `EXECUTE` on the function.
- Export the tenant's audit trail first if the contract requires evidence to
  survive. The function removes the audit rows.
- Back up before terminating. A terminated tenant cannot be restored from the
  live database; restore from backup if needed.
- The append-only trigger still blocks ordinary deletes from `audit_events`.

## Catalog administration after deploy

- Migration `internal/db/sql/schema/026_catalog_administration.sql` adds
  `create_service_catalog(...)` and `create_service_definition(...)`. Both are
  `SECURITY DEFINER`, owned by the migration role and granted to `altserv_app`.
  The application role keeps `SELECT` only on `service_catalogs` and
  `service_definitions`.
- Only a `platform_admin` may create a catalog or definition through the API.
  A `chef` or `employee` gets `403`. Tenant contracts and activity rules keep
  their existing rules.
- A catalog requires a non-empty `source_note`. It records who verified the
  data. Do not load an invented catalog.
- A definition must sit inside its catalog window. When the catalog has an end
  date, the definition needs one too and it must not extend past the catalog.
- Bulk load with the operator tool:
  ```bash
  go run ./cmd/catalogimport -file catalog.json -tenant "$OPERATOR_TENANT_ID" -dry-run
  go run ./cmd/catalogimport -file catalog.json -tenant "$OPERATOR_TENANT_ID"
  ```
  `DB_URL` must be a connection role that is a member of `altserv_app`. The
  import runs in one transaction and writes one `catalog.imported` audit event.
- The import tool has no logged-in user, so the audit actor user id is `NULL`.
  Run it from a controlled operator account and keep the manifest in change
  control.
- Verify that only `platform_admin` can write:
  ```sql
  SELECT has_table_privilege('altserv_app', 'service_catalogs', 'INSERT'),
         has_table_privilege('altserv_app', 'service_definitions', 'INSERT'),
         has_function_privilege('altserv_app',
           'create_service_catalog(text,text,text,text,timestamp,timestamp)',
           'EXECUTE');
  ```
  The two table checks must be `false`; the function check must be `true`.

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
