# ADR-019: Deployment, health checks and observability

## Problem
The application had production timeouts and a graceful shutdown, but no way for
an operator to answer basic questions:

- Is this process alive, and should the load balancer send it traffic?
- How many requests fail, and how slow are they?
- Is the database pool exhausted?
- Are statements running longer than expected?
- Did a login fail repeatedly, or did a background job fail?
- How old is the last successful backup?

Doc 20 also asks for deployment and monitoring assets, runbooks for database
recovery, key rotation and incident response, controlled restarts, backups,
point-in-time recovery, restore drills, load tests and `EXPLAIN ANALYZE`.
Monitoring must not send unnecessary health data to a third party.

## Alternatives
- Add the Prometheus client library and the OpenTelemetry SDK
- Export metrics only from PostgreSQL and a separate exporter
- Log everything and build metrics from log parsing
- Use a hosted APM agent that receives request payloads
- Rely on the load balancer's TCP check only
- Put health checks behind authentication

## Decision

### Health endpoints
- `GET /healthz` is liveness. It only proves that the process runs. It never
  touches the database, so a database outage does not cause a restart loop.
- `GET /readyz` is readiness. It pings the database with a two-second timeout and
  returns `503` while the process is draining.
- Both are public and return a single `status` field. They never return a
  database error, a tenant id or patient data.
- A load balancer must use `/readyz`, not `/healthz`.

### Controlled restart
`SIGTERM` starts a drain:
1. `/readyz` returns `503`.
2. The process waits `SHUTDOWN_DRAIN_DELAY` (default 5s, maximum 5m).
3. The HTTP server shuts down and waits up to the existing 10s for in-flight
   requests.
4. The process exits.

This gives the load balancer time to stop new traffic before the listener
closes. The operator must set the orchestrator's grace period longer than the
drain plus the shutdown timeout.

### Metrics
- `internal/observability` is a small, dependency-free Prometheus registry. It
  writes the text exposition format directly.
- HTTP labels are `method`, route pattern and `status` only. The middleware
  reads `Request.Pattern` after the mux matched the request, so a raw path, a
  path parameter, a query string and a request body cannot become a label.
- The registry exposes request counts, a latency histogram, in-flight requests,
  pool statistics, slow-query count, failed logins, job errors, backup age and
  process start time.
- `GET /metrics` is public by default for local development. In production,
  `METRICS_TOKEN` requires `Authorization: Bearer <token>` with a constant-time
  comparison. A token shorter than 16 characters is rejected at startup.
- The slow-query gauge samples `pg_stat_activity` every 30 seconds and counts
  statements in the current database above `SLOW_QUERY_THRESHOLD` (default 1s).
  It stores only a count, never statement text.
- The backup age comes from the modification time of an optional marker file.
  The backup job touches the marker only after a successful, verified dump.

### No health data to monitoring
- Metrics are aggregate operational counters. They contain no patient id,
  tenant id, user id, email address, query string, request body or free text.
- The failed-login counter has no label. An email address or IP address would be
  personal data and would create unbounded cardinality.
- The logging middleware already avoids query strings and bodies.
- A hosted monitoring service is a processor under Art. 28 GDPR and must be
  reviewed as such. It stays out of the patient-data path.

### Database pool
`DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS` and `DB_CONN_MAX_LIFETIME` bound the
pool. The defaults are 20, 5 and 30 minutes. The validation rejects an idle
limit above the open limit and a non-positive lifetime.

### Deployment and operations assets
- `deployments/docker`: multi-stage image, non-root runtime, health check,
  Compose template with PostgreSQL, Prometheus, Alertmanager and a blackbox
  readiness probe.
- `deployments/monitoring`: scrape config, alert rules, Alertmanager template,
  blackbox module, Grafana dashboard.
- `deployments/loadtest/k6.js`: read-only load test.
- `deployments/sql`: `EXPLAIN ANALYZE` templates and slow-query helpers.
- `docs/runbooks`: database recovery and restore drills, key rotation,
  incident response, deployment and controlled restart, load testing, slow
  queries.

## Reasons
- A dependency-free registry avoids a new supply-chain surface for a small
  metric set and keeps the binary easy to audit.
- Liveness without a database check prevents a database outage from restarting
  every instance.
- Readiness that fails during drain makes a rolling restart safe.
- Route patterns are the only safe HTTP label. They keep cardinality bounded
  and cannot leak a patient id.
- A token on `/metrics` is cheap and stops the endpoint from revealing traffic
  patterns and operational weaknesses.
- Sampling `pg_stat_activity` answers "is something slow right now?" without
  installing an extension or storing SQL text.
- A marker file lets the existing backup job drive a real alert without coupling
  the app to the backup product.
- Runbooks and load tests turn the operational requirements into repeatable
  procedures.

## Consequences
- The app now exposes three public endpoints. They must be reachable by the load
  balancer and Prometheus but should not be exposed to the public internet
  without a proxy rule.
- The metric set is intentionally small. There is no tracing, no per-user
  metric and no SQL text.
- The slow-query gauge is a snapshot. Per-statement totals need
  `pg_stat_statements`, which is documented but not required.
- The drain adds up to `SHUTDOWN_DRAIN_DELAY` to every deploy. The default is
  short.
- The Compose file is a template. Production needs a real orchestrator, managed
  secrets, TLS and a verified backup/PITR product.
- The Grafana dashboard and alert rules need the operator to choose a receiver
  and a data source.

## Risks
- A misconfigured readiness probe can remove healthy instances. Test the probe
  against `/readyz` in staging.
- A missing or stale backup marker silently disables the backup alert. The
  `AltservBackupStale` alert only works when `BACKUP_MARKER_FILE` is set and the
  backup job touches it. The operator must monitor the backup job itself as
  well.
- The slow-query sample can miss a short spike between samples. It is a gauge,
  not a full statement log.
- A leaked `METRICS_TOKEN` reveals aggregate traffic data. Rotate it.
- The pool defaults may be too small or too large for a specific database. They
  are a starting point, not a capacity plan.
- The dependency-free registry is not a full Prometheus client. It does not
  support exemplars, native histograms or OpenMetrics negotiation.

## Open questions
- Should the readiness check also verify the migration version, so a
  half-migrated instance never receives traffic?
- Should metrics be pushed over OTLP instead of scraped, if the operator's
  monitoring platform requires it?
- Should the slow-query watcher use `pg_stat_statements` when available, for
  per-statement totals?
- What are the agreed RPO and RTO values, and how often is the PITR drill run?
- Should the metrics endpoint move to a separate listener that is not exposed by
  the public proxy?
