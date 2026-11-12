# Internal security review

**Status:** internal review, not an external audit and not a penetration test.
**Date:** 2026-11-12
**Revision:** item 6 test-layout change on top of commit `d0fa469`.
**Reviewer:** engineering (self-review).

## Scope

The review covered the application process and its PostgreSQL schema:

- authentication, sessions, CSRF and role checks
- tenant isolation through row-level security and service rules
- patient-data encryption, the blind search index and key handling
- audit events and the denied-access path
- request logging and security headers
- the read-only load path and the metrics endpoint
- the negative tests that guard the rules above

## Out of scope

- an external penetration test or an independent security audit
- the hosting provider, the operating system, the container runtime and the
  reverse proxy
- a production secret manager, TLS certificate handling and HSTS at the edge
- a real production-sized load test
- point-in-time recovery, off-site backups and restore drills
- the professional legal, billing and data-protection review

## Method

1. Read the authorization and encryption code paths and the SQL policies.
2. Run `gofmt`, `go build ./...`, `go vet ./...`, `go test ./...` and
   `TEST_DB_URL=... go test -race ./...`.
3. Run the cross-package suites in `tests/integration` and `tests/security`.
4. Run the black-box suite in `tests/e2e` against a local server.
5. Run the read-only `cmd/loadgen` smoke test on loopback.
6. Check the response headers and the metrics protection by hand.

## Results

| Area | Result | Evidence |
| --- | --- | --- |
| Tenant A cannot read tenant B | Pass | `tests/security/TestCrossTenantPatientReadDenied`, `tests/integration/TestTenantIsolationAcrossPackages`, RLS test |
| Employee only sees assigned patients | Pass | `tests/security/TestEmployeeSeesOnlyAssignedPatients` |
| Expired substitute is denied | Pass | `tests/security/TestExpiredSubstituteDenied` |
| Revoked assignment is denied | Pass | `tests/security/TestRevokedAssignmentDenied` |
| `platform_admin` has no automatic patient access | Pass | `tests/security/TestPlatformAdminCannotReadPatients`, break-glass tests |
| Tenant roles cannot write global catalogs | Pass | `tests/security/TestTenantRolesCannotWriteGlobalCatalog` |
| `platform_admin` cannot write tenant rules or contracts | Pass | `tests/security/TestPlatformAdminCannotWriteTenantRules` |
| Tampered AES-GCM data fails | Pass | `tests/security/TestTamperedCiphertextFails` (bit flip and cross-patient swap) |
| Audit metadata contains no patient names | Pass | `tests/security/TestAuditMetadataContainsNoPatientNames` |
| Logs contain no query string or body | Pass | `middleware_hardening_test.go`, `middleware_logging_test.go` |
| Security headers | Pass | `tests/e2e/TestSecurityHeaders`, `middleware_hardening_test.go` |
| `/metrics` bearer token | Pass when `METRICS_TOKEN` is set | `tests/e2e/TestMetricsToken`, `observability_wiring_test.go` |
| Login, CSRF and logout | Pass | `tests/e2e/TestLoginFlow`, `handler_sessions_db_test.go` |
| Read-path smoke load | Pass on loopback | `docs/runbooks/load-testing.md` |
| `/app/` serves no file from disk | Pass after fix | `handler_app_test.go` |

## Findings

### Fixed: `/app/` exposed the working directory (critical)

Found on 2026-11-12, after the first version of this review. `/app/` was
served by `http.FileServer(http.Dir("."))`. Without a login it returned a
directory listing and every file next to the process, including `.env` with the
patient encryption, search and MFA keys, `connetion.txt` and the source code.
The Docker image only contained `start.html` in its working directory, so it
was less exposed; a deployment started from the repository folder was fully
exposed.

Fix: `start.html` is embedded with `go:embed` and served by an allowlist
handler (`handler_app.go`). Only `GET`/`HEAD` on `/app/` and `/app/start.html`
answer 200; every other path is 404 and nothing is read from disk. The
Dockerfile no longer copies `start.html`. The regression tests in
`handler_app_test.go` probe source files, dotfiles, traversal and encoded
paths, and fail against the old file server.

Follow-up: treat every key in a `.env` that was served by an affected process
as leaked and rotate it. Local development keys protect synthetic data only;
if a real key was ever used with such a process, follow
`docs/runbooks/key-rotation.md`.

### Known gaps

The following are known gaps, not surprises:

1. **No external audit or penetration test.** This review is a self-review. An
   independent test is still required before a production launch.
2. **No external append-only audit sink.** A database superuser can disable the
   audit triggers. A WORM sink or an external log service is still open.
3. **No shipped PITR product and no recorded PITR drill.** Backups and the
   restore drill exist, but point-in-time recovery is an operator task.
4. **Production hardening is incomplete.** Secrets still come from the
   environment; TLS and HSTS belong to the edge; `METRICS_TOKEN` is optional in
   local development; the login rate limiter is per process.
5. **Billing transmission is deliberately `not_configured`.** There is no
   automatic transmission to a care fund and no payer integration.
6. **The load numbers are loopback smoke numbers.** They are not capacity
   evidence and do not replace a staging test with database telemetry.
7. **The demo seed has no MFA enrolled.** A privileged demo login is redirected
   to MFA enrollment, which is the intended production behaviour.

## Residual risks

- A future change can weaken a rule without breaking the build if it does not
  add a negative test. The suites in `tests/security` are the regression guard;
  keep them in the release gate.
- The encryption keys are only as safe as the deployment environment. A key
  rotation makes old ciphertext unreadable unless the old key version is kept.
- The application trusts the reverse proxy for transport security. A
  misconfigured proxy can expose the process over plain HTTP.

## Next steps

1. Commission an external penetration test and record the result in
   `docs/compliance/review-record.md` when the professional review is signed.
2. Add an external append-only audit sink.
3. Ship and drill a PITR procedure.
4. Move secrets to a secret manager, enforce TLS/HSTS at the edge, require
   `METRICS_TOKEN` in production and connect an alert receiver.
5. Run a staging load test with the monitoring stack attached.

## Disclaimer

This document records an internal engineering review. It is not a certificate,
not a legal opinion and not a substitute for an independent security audit or
the professional review tracked in `docs/compliance/`.
