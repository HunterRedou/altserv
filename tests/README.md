# Test layout

The repository has three kinds of tests. They are deliberately separate so a
developer can run the fast unit suite without a database and an operator can run
the black-box suite against a deployed instance.

| Layer | Location | Needs | Purpose |
| --- | --- | --- | --- |
| Unit and package tests | next to the code (`*_test.go` in the root and `internal/`) | Go only; DB tests need `TEST_DB_URL` | Business rules, validation, handlers, SQL access, migrations |
| Cross-package integration | `tests/integration/` | `TEST_DB_URL` | Packages work together; seeded data is coherent |
| Negative authorization and crypto | `tests/security/` | `TEST_DB_URL` | Tenant isolation, assignment rules, platform-admin limits, AES-GCM tampering |
| Black-box smoke | `tests/e2e/` | a running server (`E2E_BASE_URL`) | Health, readiness, headers, metrics protection, login/logout |

Shared database and service helpers live in `tests/testutil/`. It is test
support, not production code.

## Running the suites

Unit tests only (DB-backed tests skip when `TEST_DB_URL` is unset):

```bash
go test ./...
```

Package tests against a real PostgreSQL database:

```bash
TEST_DB_URL='postgres://postgres:postgres@localhost:5432/altserv_test?sslmode=disable' \
  go test ./...
```

Race detector, which is the required gate before a release:

```bash
TEST_DB_URL='postgres://postgres:postgres@localhost:5432/altserv_test?sslmode=disable' \
  go test -race ./...
```

Cross-cutting suites:

```bash
TEST_DB_URL='postgres://postgres:postgres@localhost:5432/altserv_test?sslmode=disable' \
  go test ./tests/integration/... ./tests/security/...
```

Black-box smoke test against a running instance:

```bash
E2E_BASE_URL=http://127.0.0.1:8080 go test ./tests/e2e/...
```

The optional login flow uses a dedicated test account and also checks CSRF and
session revocation:

```bash
E2E_BASE_URL=https://altserv.example.invalid \
E2E_EMAIL=loadtest@example.invalid \
E2E_PASSWORD='...' \
go test ./tests/e2e/...
```

When `METRICS_TOKEN` is configured on the server, set `E2E_METRICS_TOKEN` to the
same value. The suite then proves that `/metrics` rejects an unauthenticated
request and accepts the bearer token. Without it, the suite only checks the
local-development behaviour (open endpoint).

## Mapping the critical cases from doc 19

| Critical case | Where it is covered |
| --- | --- |
| Tenant A must not read tenant B | `tests/security/TestCrossTenantPatientReadDenied`, `tests/integration/TestTenantIsolationAcrossPackages`, `internal/db/tenant_db_test.go` (RLS) |
| Employee only sees assigned patients | `tests/security/TestEmployeeSeesOnlyAssignedPatients`, `internal/patients/service_lifecycle_test.go`, `internal/patients/service_test.go` |
| Expired substitute is denied | `tests/security/TestExpiredSubstituteDenied`, `internal/assignments/service_db_test.go` |
| No role self-upgrade | `handler_create_user_test.go` (`TestHandlerUpdateUserRoleRejectsSelfChange`, `TestHandlerUserRejectsChefAssigningPlatformAdmin`) |
| Revoked session is invalid | `handler_sessions_db_test.go` (`TestSessionFlow`), `internal/auth/session_test.go`, `tests/e2e` (logout) |
| Submitted reports are not silently changed | `internal/reports/service_test.go`, `internal/reports/service_db_test.go` |
| Transaction rollback | `internal/*/service_test.go` audit-failure tests, `internal/db/tenant_db_test.go`, `internal/breakglass/handler_test.go` |
| Tampered AES-GCM data fails | `tests/security/TestTamperedCiphertextFails`, `internal/patients/service_test.go` (`TestCipherRejectsTampering`, `TestCipherRejectsWrongAAD`), `internal/reports/service_test.go` |
| No sensitive logs | `middleware_hardening_test.go` (`TestLoggingDoesNotLogQueryString`), `middleware_logging_test.go` (`TestLoggingDoesNotLogRequestBody`) |
| No double billing | `internal/billing/rules_test.go`, `internal/billing/service_test.go`, `internal/billing/service_db_test.go` |
| Migration safety | `internal/db/sql/schema/migrations_test.go` |
| Concurrency | `internal/observability/metrics_test.go` (`TestConcurrentObservation`) and the DB transaction tests |
| Load test | `cmd/loadgen`, `deployments/loadtest/k6.js`, `docs/runbooks/load-testing.md` |
| Restore test | `deployments/docker/restore-drill.sh`, `docs/runbooks/database-recovery.md` |

## Test data rules

- Use synthetic data only. Never copy production patient data into a test or
  development database.
- The cross-cutting suites seed a coherent synthetic tenant through
  `internal/devseed`. The catalog is marked `SYNTH-` and must not be mistaken
  for a real Brandenburg catalog.
- Every database test runs inside one transaction and rolls it back. A test
  must not leave rows behind, and it must not depend on rows another test wrote.
- `db.WithTenantTx` has no savepoints. When a test expects a database error,
  use a separate transaction for that error path so the next assertion does not
  run in an aborted transaction.
- A regression test is required for every fixed error. Put a unit test next to
  the code; put a cross-package or authorization test in `tests/` when the bug
  spans packages or roles.

## CI order

1. `gofmt -l .`
2. `go build ./...`
3. `go vet ./...`
4. `go test ./...`
5. `TEST_DB_URL=... go test -race ./...`
6. `TEST_DB_URL=... go test ./tests/integration/... ./tests/security/...`
7. `E2E_BASE_URL=... go test ./tests/e2e/...` against a staging instance

The black-box suite is not a substitute for the external security review or a
penetration test. It only proves the transport contract of one running process.
