# Project overview

## How to read this document

This document gives a short overview of the last five points of the plan:
Points 17, 18, 19, 20 and 21. It also gives the project status and the next
steps.

The text uses simple technical English. Most sentences are short. Most
sentences use the active voice.

- **Must** marks a requirement.
- **Can** marks an ability.
- **Does not** marks a limit.

The diagrams are ASCII. They render in any editor and in any Markdown viewer.

The most recent feature deliverables are Points 13, 14 and 15 plus the Step 2
authentication hardening and the Step 3 break-glass access. They are summarized
in the project status.

---

## Point 17: HTTP middleware

### What it does

Point 17 handles cross-cutting HTTP concerns in one place. A handler does not
repeat these concerns.

The middleware chain does these tasks:

1. Add or accept a request id.
2. Count the request and measure its duration.
3. Recover from a panic.
4. Set security headers.
5. Limit the request body size.
6. Write a structured access log.
7. Authenticate the session.
8. Check the CSRF token.
9. Load the tenant context.
10. Pass the request to the service layer.

The service layer does the object-level check. Middleware does only a coarse
role check. A valid session is not a permission.

### Request pipeline

```
client
  |
  v
+-------------------+
| RequestID         |  accept X-Request-ID or generate a UUID
+---------+---------+
          |
          v
+-------------------+
| Metrics           |  method, route pattern, status, duration
+---------+---------+
          |
          v
+-------------------+
| Recover           |  panic -> log -> 500, no internal details
+---------+---------+
          |
          v
+-------------------+
| SecurityHeaders   |  nosniff, DENY, no-referrer, CSP, no-store
+---------+---------+
          |
          v
+-------------------+
| BodyLimit         |  1 MiB maximum
+---------+---------+
          |
          v
+-------------------+
| Logging           |  method, path, status, duration, request id
+---------+---------+     no query string, no body
          |
          v
+-------------------+
| Route handler     |  auth -> CSRF -> tenant context -> service
+---------+---------+
          |
          v
       response
```

### Rules

- The request id comes from the client or from a new UUID. The response returns
  it. The log and the audit event use it.
- A panic becomes a `500`. The client sees a generic message. The log keeps the
  panic and the request id.
- Every response gets `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, a restrictive
  `Content-Security-Policy` and a restrictive `Permissions-Policy`.
- `/api/` responses get `Cache-Control: no-store`.
- A JSON body is limited to 1 MiB.
- The login limiter allows 10 attempts per minute per source address. It is
  in memory and per process. A multi-instance deployment needs a shared store.
- The session cookie is `__Host-session`. It is `HttpOnly`, `Secure`,
  `SameSite=Lax` and `Path=/`. The database stores only a hash of the token.
- Unsafe methods need a valid CSRF token.
- The tenant id comes from the authenticated session. It never comes from a
  client header or a request body.
- The tenant id is set with
  `set_config('app.tenant_id', $1, true)`. The setting is transaction-local.
  A pooled connection cannot leak a tenant.
- The access log does not contain a query string or a body. It can contain an
  id in the path.

### Files

- `middleware_request_id.go`
- `middleware_recover.go`
- `middleware_security_headers.go`
- `middleware_body_limit.go`
- `middleware_logging.go`
- `middleware_login_rate_limit.go`
- `middleware_auth.go`
- `internal/db/tenant.go`

---

## Point 18: Server and configuration

### What it does

Point 18 gives the process safe HTTP timeouts, a controlled shutdown and a
validated configuration. A bad configuration stops the process at startup. It
does not fail later in production.

### Server timeouts

| Setting | Value | Purpose |
| --- | --- | --- |
| `ReadHeaderTimeout` | 5 s | Stop a slow-header attack. |
| `ReadTimeout` | 15 s | Bound the request read. |
| `WriteTimeout` | 30 s | Bound the response write. |
| `IdleTimeout` | 60 s | Close idle keep-alive connections. |
| `MaxHeaderBytes` | 1 MiB | Bound the header size. |
| `shutdownTimeout` | 10 s | Wait for in-flight requests. |

### Controlled shutdown

```
SIGTERM
  |
  v
/readyz returns 503
  |
  v
wait SHUTDOWN_DRAIN_DELAY        default 5 s, maximum 5 min
  |
  v
HTTP server stops accepting new connections
  |
  v
wait up to 10 s for in-flight requests
  |
  v
process exits
```

The load balancer must use `/readyz`, not `/healthz`. The orchestrator grace
period must be longer than the drain delay plus the shutdown timeout.

### Configuration

The configuration comes from environment variables. Secrets are not in the
source code, in Git or in a log.

| Variable | Default | Rule |
| --- | --- | --- |
| `DB_URL` | none | Required. |
| `PLATFORM` | `dev` | Environment name. |
| `PORT` | `8080` | Listen port. |
| `SESSION_DURATION` | `24h` | Positive, maximum 30 days. |
| `PATIENT_ENCRYPTION_KEY` | none | Base64, exactly 32 bytes. |
| `PATIENT_KEY_VERSION` | `1` | At least 1. |
| `PATIENT_SEARCH_KEY` | none | Base64, exactly 32 bytes, different from the AES key. |
| `PATIENT_SEARCH_KEY_VERSION` | `1` | At least 1. |
| `DB_MAX_OPEN_CONNS` | `20` | Positive. |
| `DB_MAX_IDLE_CONNS` | `5` | Not above the open limit. |
| `DB_CONN_MAX_LIFETIME` | `30m` | Positive. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Maximum 5 min. |
| `SLOW_QUERY_THRESHOLD` | `1s` | Maximum 1 min. |
| `METRICS_TOKEN` | empty | Optional; at least 16 characters. |
| `BACKUP_MARKER_FILE` | empty | Optional marker for the backup-age metric. |

### Rules

- The process must fail fast when the configuration is invalid.
- The AES key and the search HMAC key must be different values. The
  configuration rejects reuse.
- A secret must never appear in an error message or a log line.
- The database pool must stay below the PostgreSQL connection limit.

### Files

- `internal/server/server.go`
- `internal/server/router.go`
- `internal/server/routes.go`
- `internal/server/errors.go`
- `internal/server/response.go`
- `internal/config/config.go`
- `internal/config/validation.go`
- `main.go`

---

## Point 19: Automated tests

### What it does

Point 19 protects the critical rules with automated tests. The tests use real
PostgreSQL for the database paths. They do not mock the database behaviour that
the security depends on.

### Test layers

```
+---------------------------+
| Load tests                |  k6, concurrent traffic
+---------------------------+
| Restore tests             |  backup -> restore -> verify
+---------------------------+
| Concurrency tests         |  optimistic locking, race detector
+---------------------------+
| Integration tests         |  real PostgreSQL, RLS, migrations
+---------------------------+
| Negative security tests   |  denied access must stay denied
+---------------------------+
| Unit tests                |  rules, validation, crypto, auth
+---------------------------+
```

### Critical cases that must stay green

- Tenant A cannot read or change tenant B data.
- An employee sees only a patient with a valid, non-revoked assignment.
- An expired or revoked assignment denies access.
- A user cannot change their own role.
- A chef cannot create or promote a `platform_admin`.
- A revoked session is invalid.
- A submitted or approved report cannot change without a return or correction.
- A transaction rolls back on error.
- Tampered AES-GCM data fails to decrypt.
- Logs do not contain a password, a token, a query string or a body.
- The same activity cannot be billed twice.
- A stale optimistic-lock version is rejected.

The cross-cutting suites live under `tests/`: `tests/integration` proves that
packages work together, `tests/security` holds the negative authorization and
crypto cases, and `tests/e2e` is a black-box smoke test against a running
process. `tests/README.md` maps every critical case above to its test.

### How to run

```
gofmt -l .
go build ./...
go vet ./...
go test ./...

TEST_DB_URL=postgres://.../altserv_test?sslmode=disable \
  go test -race ./...

TEST_DB_URL=postgres://.../altserv_test?sslmode=disable \
  go test ./tests/integration/... ./tests/security/...

E2E_BASE_URL=https://staging.example.invalid \
  go test ./tests/e2e/...
```

The database tests skip when `TEST_DB_URL` is empty. A production check must run
them with a real database.

### Files

- `*_test.go` in the root package and in every `internal/` package
- `tests/integration/`, `tests/security/`, `tests/e2e/` and `tests/testutil/`
- `tests/README.md` (test layout and critical-case map)
- `internal/db/sql/schema/migrations_test.go`
- `cmd/loadgen/main.go`
- `deployments/loadtest/k6.js`
- `deployments/docker/restore-drill.sh`

---

## Point 20: Deployment and resilience

### What it does

Point 20 makes the service operable. An operator can answer these questions:

- Is the process alive?
- Should the load balancer send traffic?
- How many requests fail, and how slow are they?
- Is the database pool exhausted?
- Are statements slow?
- Did logins fail, or did a background job fail?
- How old is the last backup?

### Topology

```
             +----------------------+
   client -->| load balancer        |
             | probes GET /readyz   |
             +----------+-----------+
                        |
                        v
             +----------------------+        +---------------------+
             | altserv (Go)         |------->| PostgreSQL          |
             | GET /healthz         |        | RLS, pool, WAL      |
             | GET /readyz          |        +----------+----------+
             | GET /metrics         |                   |
             +----------+-----------+                   v
                        |                     +---------------------+
                        v                     | backup + PITR       |
             +----------------------+         | archive             |
             | Prometheus           |         +---------------------+
             | alerts -> Alertmanager|
             +----------------------+
```

### Health endpoints

| Endpoint | Meaning | Behaviour |
| --- | --- | --- |
| `GET /healthz` | Liveness | Does not touch the database. Returns `200` while the process runs. |
| `GET /readyz` | Readiness | Pings the database with a 2 s timeout. Returns `503` while draining. |
| `GET /metrics` | Metrics | Prometheus text format. Public in development. Protect it in production. |

A database outage must not cause a restart loop. Therefore `/healthz` does not
ping the database.

### Metrics

| Metric | Meaning |
| --- | --- |
| `altserv_http_requests_total` | Request count by method, route pattern and status. |
| `altserv_http_request_duration_seconds` | Latency histogram. |
| `altserv_http_in_flight` | Requests in flight. |
| `altserv_db_pool_open_connections` | Open database connections. |
| `altserv_db_pool_in_use` | Connections in use. |
| `altserv_db_pool_idle` | Idle connections. |
| `altserv_db_pool_wait_count_total` | Wait events. |
| `altserv_db_pool_wait_duration_seconds_total` | Wait time. |
| `altserv_db_slow_queries` | Statements above the slow threshold. |
| `altserv_login_failures_total` | Failed logins. |
| `altserv_job_errors_total` | Background job errors. |
| `altserv_backup_age_seconds` | Age of the last successful backup marker. |
| `altserv_process_start_time_seconds` | Process start time. |

### Privacy rule

Metrics must not contain health data. They must not contain a tenant id, a
patient id, a user id, an email address, a query string or a request body.

HTTP labels use the matched route pattern only. The middleware reads
`Request.Pattern` after the mux matched the request. A raw path cannot become a
label.

The monitoring provider, if any, is a subprocessor. It must not receive health
data.

### Backup and recovery

- `deployments/docker/backup.sh` makes a logical dump.
- The backup job touches `BACKUP_MARKER_FILE` only after a successful dump.
- The application exports the marker age. `AltservBackupStale` fires above
  26 hours.
- A logical dump is not point-in-time recovery. Production needs a PITR
  product, for example pgBackRest or WAL-G.
- `deployments/docker/restore-drill.sh` tests a restore.
- A backup that was never restored is not a backup.

### Load and query analysis

- `deployments/loadtest/k6.js` drives concurrent traffic.
- `deployments/sql/explain-analyze.sql` shows a query plan.
- `deployments/sql/slow-queries.sql` reads `pg_stat_activity`.
- `docs/runbooks/slow-queries.md` gives the procedure.

### Runbooks

- `docs/runbooks/database-recovery.md`
- `docs/runbooks/key-rotation.md`
- `docs/runbooks/incident-response.md`
- `docs/runbooks/deployment.md`
- `docs/runbooks/load-testing.md`
- `docs/runbooks/slow-queries.md`

### Limits

- The Compose file is a template. It is not a production orchestrator.
- Production needs managed secrets, TLS, a verified PITR restore and a tested
  alert receiver.
- The backup marker proves only that the script reached the marker. It does not
  prove that the repository is healthy.

---

## Point 21: GDPR and legal requirements

### What it does

Point 21 documents the data-protection and billing duties. It does not make a
legal decision. It gives the controller and the operator a structure to review,
complete and sign.

### Roles

```
patient and employee data
          |
          v
+-----------------------------+
| Tenant (nursing service)    |  controller
| decides purpose and means   |
+--------------+--------------+
               |  Art. 28 agreement
               v
+-----------------------------+
| altserv operator            |  processor for tenant data
| processes on instruction    |  controller for own account data
+--------------+--------------+
               |
               v
+-----------------------------+
| Subprocessors               |  hosting, backup, monitoring
| Art. 28, listed, approved   |
+-----------------------------+
```

### Documents

| File | Purpose |
| --- | --- |
| `docs/compliance/data-classification.md` | Levels L0 to L4 and handling rules. |
| `docs/compliance/processing-activities.md` | Art. 30 record, roles, Art. 28 checklist. |
| `docs/compliance/privacy-impact-assessment.md` | DPIA screening, risks, measures, residual risk. |
| `docs/compliance/retention-policy.md` | Retention schedule and legal holds. |
| `docs/compliance/deletion-policy.md` | Lawful deletion, dependency order, backups. |
| `docs/compliance/access-control-policy.md` | Roles, tenant isolation, platform admin, authentication. |
| `docs/compliance/incident-response.md` | Breach assessment and the 72-hour path. |
| `docs/compliance/backup-policy.md` | Encryption, retention, restore drills, RPO and RTO. |
| `docs/compliance/billing-requirements.md` | SGB XI, SGB V, GoBD, export and transmission. |
| `docs/compliance/README.md` | Index, production checklist and known gaps. |

### Key controls

- The tenant is the controller. The operator is the processor. The contract
  must state this, including any joint controllership.
- Every tenant and every subprocessor needs an Art. 28 agreement.
- A DPIA is likely required for large-scale health data. The controller decides
  and signs. In Brandenburg the authority is the LDA Brandenburg.
- Purpose limitation: no analytics on patient data, no model training, no
  advertising. The application has no generative AI.
- Data minimisation: the tenant must train staff on free-text notes.
- Retention and deletion need an owner and an execution procedure. The
  application does not run an automated retention job yet.
- `platform_admin` has no automatic patient access. Break-glass access is
  patient-scoped, approved by a tenant `chef`, time-limited, read-only and
  audited.
- A personal data breach follows the 72-hour path. The processor notifies the
  controller without undue delay.
- Backups are personal data. They need encryption, access control and a
  deletion rule.
- Billing follows the current § 105 SGB XI or § 302 SGB V procedure. The
  application does not transmit a claim in production. Exports are internal
  CSV and PDF. The transmission boundary (ADR-026) is idempotent and audited,
  but only the synthetic development payer is wired.

### Confirmed technical facts in the documents

- `DELETE FROM tenants` fails once the tenant has audit events. The append-only
  trigger blocks the cascade. This was confirmed on a scratch database.
- `archived` is a status, not an access control. A chef can still read an
  archived patient. Employees lose access.
- There is no patient delete API and no automated retention job.
- A `platform_admin` is stored with a tenant id. It must not belong to a tenant
  that holds patient data.
- MFA, password reset, user disable and the scheduled session purge are
  implemented. See the Step 2 section above.

### Status

All Point 21 documents are drafts. They need professional data-protection and
billing review before production. A checked box is not a legal approval.

---

## Authentication hardening (Step 2)

### What it does

Step 2 closes the authentication gaps in the access-control policy. It adds
MFA, password reset, user disable and a scheduled session purge.

`chef` and `platform_admin` must use a second factor. An `employee` does not.

### Login flow

```
email + password
       |
       v
+----------------------+
| role needs MFA?      |
+----+------------+----+
     | no         | yes
     v            v
  session   +---------------------+
            | MFA enrolled?       |
            +----+-----------+----+
                 | no        | yes
                 v           v
      restricted session   202 + challenge
      (enroll only)            |
                 |             v
                 |      TOTP or recovery code
                 |             |
                 +-------------+--> session
```

Rules:

- A challenge lives 5 minutes. It allows 5 attempts.
- A challenge is consumed in the same transaction as the session.
- A recovery code works one time.
- A privileged account without MFA can only reach the enrollment endpoints.
- A role change revokes the target user's sessions.
- A disabled account cannot log in and loses every session.

### Reset and purge

```
reset request --> hashed token (30 min, one use) --> new password
                                                    |
                                                    v
                                            revoke all sessions
```

- The public reset request never shows whether an account exists.
- A `chef` or `platform_admin` can issue a token for another user.
- A `chef` cannot reset a `platform_admin`.
- The purge deletes expired and revoked sessions, challenges and reset tokens.
- The purge writes one audit event per tenant. The metric
  `altserv_sessions_purged_total` counts the rows.

### Limits

- No email delivery. Tokens are handed over out of band.
- No shared rate-limit store. The limit is per process.
- No user-facing session list.
- MFA does not replace a strong password policy.

See `docs/decisions/ADR-020-authentication-hardening.md`.

---

## Break-glass access (Step 3)

### What it does

Step 3 adds a controlled support path for a `platform_admin`. A
`platform_admin` still has no automatic patient access. A support person can
read one named patient only after a `chef` of the patient's tenant approves a
short window.

### Flow

```
platform_admin                        tenant chef
     |                                     |
     | request tenant + patient + reason   |
     +--------------> pending ------------>|
     |                                     |
     |                            approve / deny
     |                                     |
     |<------------- approved -------------+
     |           (1 to 60 minutes)
     |
     | GET .../patient
     v
  patient record  +  audit event
```

### Rules

- A grant names one tenant and one patient. There is no tenant-wide grant.
- Only a `platform_admin` can request.
- Only a `chef` of the patient's tenant can approve or deny.
- A requester cannot approve their own grant.
- The window is 1 to 60 minutes. The default is 30 minutes.
- Access is read-only. The only route is `GET .../patient`.
- The requester or the tenant `chef` can revoke at any time.
- The active check uses `clock_timestamp()`.
- Every request, decision, revocation, list and read is audited in the patient's
  tenant.
- A forbidden attempt writes `access.denied`. The denial row is committed before
  the API returns `403`.
- Audit metadata holds ids, counts, duration and expiry. It does not hold the
  patient name, the reason or health data.
- The grant row has row-level security and belongs to the patient's tenant.

### Limits

- No tenant-wide grant and no support UI.
- No cross-tenant user API.
- A `platform_admin` is still tenant-bound. Keep platform staff in an
  operator-only tenant.
- Support needs a reachable `chef`. Out of hours, the tenant must nominate an
  on-call `chef`.

See `docs/decisions/ADR-021-break-glass-access.md`.

---

## Retention and deletion (Step 4)

### What it does

Step 4 adds controlled retention review and patient deletion. The application

never deletes care data on its own. A tenant may store a retention policy, but a

new policy is disabled and only an explicit confirmation by a tenant `chef`

enables it. The scheduled review only counts candidates and writes a

`retention.reviewed` audit event. It does not create deletion requests and does

not delete anything.

Patient deletion is a separate, explicit workflow: one `chef` requests, a

different `chef` approves and executes. `platform_admin` is denied. The deletion

register is append-only evidence. Legal holds block deletion while active.

### Flow

```

chef A requests deletion of patient P

        |

        v

+---------------------+

| pending request     |  active legal hold -> 409

+----------+----------+

           |

           v

chef B approves (different person)

        |

        v

+---------------------+

| approved            |

+----------+----------+

           |

           v

chef B executes

        |

        v

delete in dependency order

        |

        v

append-only register entry

```

### Rules

- Retention policies are tenant-configured, disabled by default, and confirmed

  by a `chef`. No period is seeded.

- The review function `retention_review_candidates(filter_tenant UUID DEFAULT NULL)`

  is `SECURITY DEFINER`. The job calls it with `NULL` (all tenants); the chef API

  calls it with the session tenant.

- The scheduled job runs once at startup and then every `RETENTION_REVIEW_INTERVAL`

  (default 24h, minimum 1h, maximum 7d). Metrics:

  `altserv_retention_reviews_total` and `altserv_retention_review_candidates_total`.

- Legal holds are tenant-bound, patient-scoped or tenant-wide, and block deletion

  while active. A hold is reviewed at least every six months.

- Deletion requires four eyes by two different tenant chefs. `platform_admin` is

  denied. An active legal hold blocks request, approval and execution.

- Execution order: billing runs, reports, report activities, visits, activities,

  templates, assignments, search tokens, break-glass grants, then the patient.

- The `deletion_register` is append-only: the app role has `SELECT` and `INSERT`

  only; update, delete and truncate are blocked by triggers for every role.

- Audit metadata never contains patient names, free-text reasons, notes,

  legal-basis text or health data.

### Limits

- Tenant termination still fails once the tenant has audit events. Step 5 must

  address this.

- Break-glass grants cascade away with a deleted patient, so the evidence that

  support access was approved can be lost. Export the grant first if required.

- No automated way to delete a subject from backups.

See `docs/decisions/ADR-022-retention-deletion.md`.

---

## Project status

### Completed

| Area | State |
| --- | --- |
| Go backend, PostgreSQL, sqlc, migrations | Complete |
| Session authentication, CSRF, rate limit | Complete |
| Step 2: MFA, password reset, user disable, session purge | Complete |
| Step 3: break-glass access for `platform_admin` | Complete |
| Step 4: retention review and patient deletion | Complete |
| Step 5: tenant termination | Complete |
| Catalog administration and verified catalog import | Complete |
| Synthetic development seed data | Complete |
| Billing transmission boundary and synthetic test payer | Complete; production adapter not configured |
| Tenant isolation with row-level security | Complete |
| Patient encryption and protected search | Complete |
| Assignments, catalogs, templates, visits | Complete |
| Point 13: reports and approvals | Complete |
| Point 14: rule-based billing assistant | Complete |
| Point 15: audit logging and role-change audit | Complete |
| Point 17: HTTP middleware | Complete |
| Point 18: server and configuration | Complete |
| Point 19: automated tests | Complete |
| Point 19: `tests/` layout, load smoke and internal security review | Complete |
| Point 20: deployment, health, metrics, runbooks | Complete |
| Point 21: compliance documentation | Complete as a draft |

### Recent commits

```
d0fa469 Add synthetic development seed data
6f48c8c Add catalog administration and verified catalog import
a25b684 Add tenant-approved break-glass access for platform admins
c9208a3 Add MFA, password reset, user disable, and session purge
0a008cf Add GDPR compliance documentation and legal review checklists
7b8d87d Add health checks, metrics, deployment assets, and operations runbooks
8bb64bc Add restricted audit reads, role-change auditing, and audit hardening
```

### Verification

The following checks pass:

- `gofmt -l .` is clean.
- `go build ./...` passes.
- `go vet ./...` passes.
- `go test ./...` passes.
- `TEST_DB_URL=... go test -race ./...` passes.
- `TEST_DB_URL=... go test ./tests/integration/... ./tests/security/...` passes.
- The black-box suite in `tests/e2e` passes against a local server.
- The read-only `cmd/loadgen` smoke run is recorded in
  `docs/runbooks/load-testing.md`. The numbers are loopback smoke numbers, not
  capacity evidence.
- The internal review in `docs/security-review.md` records the checked areas,
  the findings and the remaining gaps. It is not an external audit.
- Migrations `internal/db/sql/schema/021_audit_hardening.sql`,
  `internal/db/sql/schema/022_auth_hardening.sql`,
  `internal/db/sql/schema/023_break_glass.sql`,
  `internal/db/sql/schema/024_retention_deletion.sql`,
  `internal/db/sql/schema/025_tenant_termination.sql` and
  `internal/db/sql/schema/026_catalog_administration.sql` are applied to the
  development and test databases. Goose version is 26.
- The deployment YAML and JSON files are valid.
- `docker compose config` passes.
- All compliance links and file references resolve.

### Known gaps

These are real gaps. They are not open design questions.

1. No tenant-wide break-glass grant and no support UI. One grant covers one
   named patient.
2. No email delivery for password reset or MFA changes.
3. Tenant termination is now a controlled procedure via `terminate_tenant()`. Audit evidence must be exported first if the contract requires it.
4. Tenant deletion fails once the tenant has audit events.
5. No external append-only audit sink. A superuser can disable the triggers.
6. No shipped PITR product and no automated PITR drill.
7. Billing transmission is deliberately `not_configured` in production. The
   boundary, idempotency and audit exist; a real payer adapter does not.
8. No one-click data-subject access export.
9. `platform_admin` is tenant-bound. There is no cross-tenant support API.
10. No shared rate-limit store for multi-instance deployments.
11. The Compose file is a template, not a production orchestrator.
12. No real Brandenburg catalog data is loaded. Test catalogs must stay clearly
    labelled until a domain expert verifies the real data.
13. No external penetration test. `docs/security-review.md` is an internal
    review, not an independent audit.
14. The recorded load numbers are loopback smoke numbers. No staging capacity
    test with database, pool and host telemetry has been run.

---

## Next steps

Do these steps in order. Do not start a production tenant before step 1 and
step 2 are complete.

1. **Professional review.** Give the Point 21 documents to a data-protection
   lawyer and a billing expert. Record the result. Do not treat the drafts as
   approval.
2. **Implement retention and deletion.** Add a scheduled job, a patient
   deletion procedure with a four-eyes check, and a deletion register.
3. **Tenant termination.** Implemented via `terminate_tenant()`. Export the
   audit trail first if the contract requires evidence to survive.
4. **Prove recovery.** Select a PITR product. Run a quarterly PITR drill and
   record the result. Test the backup key recovery.
5. **Add an external append-only audit sink.** Keep audit evidence independent
   of the database superuser.
6. **Verify billing.** Confirm the § 105 SGB XI and § 302 SGB V procedure for
   the first tenants. Add a verified transmission only after a payer test.
   The boundary from ADR-026 is ready; a real adapter must run through an
   outbox and be idempotent on the idempotency key.
7. **Harden production.** Use a secret manager, TLS and HSTS, set
   `METRICS_TOKEN`, configure and test the alert receiver, and keep platform
   staff in an operator-only tenant.
8. **Extend the support model.** Decide how a `platform_admin` supports another
   tenant beyond one named patient. Add a tenant-wide grant or a cross-tenant
   support API only with a documented legal basis.
9. **Load verified catalog data.** Have a domain expert verify a real
   Brandenburg catalog, then import it with `cmd/catalogimport`. Keep the source
   note on the catalog and never load invented codes.
10. **Prove security and capacity independently.** Commission an external
    penetration test and run a staging load test with the monitoring stack
    attached. Record both results; do not treat the internal review or the
    loopback smoke numbers as a substitute.

---

## Document map

| Document | Content |
| --- | --- |
| `docs/decisions/` | Architecture decision records, ADR-001 to ADR-026. |
| `docs/runbooks/` | Operations procedures for recovery, keys, incidents, deployment, load and slow queries. |
| `docs/compliance/` | Data-protection and billing policies and checklists. |
| `docs/security-review.md` | Internal security review; not an external audit. |
| `tests/README.md` | Test layout, how to run each suite, and the critical-case map. |
| `deployments/` | Docker, monitoring, load-test and SQL assets. |
| `docs/platform-admin-patient-access-reminder.txt` | The rule that `platform_admin` has no automatic patient access. |
