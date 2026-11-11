# Monitoring

## Endpoints
| Path | Purpose | Authentication |
| --- | --- | --- |
| `GET /healthz` | Liveness. The process is running. It does not touch the database. | Public |
| `GET /readyz` | Readiness. The database is reachable and the process is not draining. | Public |
| `GET /metrics` | Prometheus text exposition. | Public by default; set `METRICS_TOKEN` and send `Authorization: Bearer <token>` in production |

The health responses contain only `{"status":"ok"}`, `{"status":"ready"}`,
`{"status":"draining"}` or `{"status":"unavailable"}`. They never contain a
database error, a tenant id or patient data.

## Metrics
| Metric | Type | Meaning |
| --- | --- | --- |
| `altserv_http_requests_total` | counter | Requests by method, route pattern and status |
| `altserv_http_request_duration_seconds` | histogram | Request latency in seconds |
| `altserv_http_in_flight` | gauge | Requests being served |
| `altserv_db_pool_max_open_connections` | gauge | Configured pool limit |
| `altserv_db_pool_open_connections` | gauge | Open connections |
| `altserv_db_pool_in_use` | gauge | Connections in use |
| `altserv_db_pool_idle` | gauge | Idle connections |
| `altserv_db_pool_wait_count_total` | counter | Waits for a connection |
| `altserv_db_pool_wait_duration_seconds_total` | counter | Time spent waiting for a connection |
| `altserv_db_slow_queries` | gauge | Statements above `SLOW_QUERY_THRESHOLD` right now |
| `altserv_login_failures_total` | counter | Failed login attempts |
| `altserv_job_errors_total` | counter | Failed background jobs by job name |
| `altserv_backup_age_seconds` | gauge | Age of the backup marker, when configured |
| `altserv_backup_marker_read_errors_total` | counter | Failed reads of the backup marker |
| `altserv_process_start_time_seconds` | gauge | Process start time |

## Label policy
- The only HTTP labels are `method`, the route pattern such as
  `/api/patients/{id}`, and `status`.
- The middleware reads `Request.Pattern` after the mux matched the request. A
  raw path, a path parameter, a query string and a request body can never become
  a label.
- An unmatched request is labelled `route="unmatched"`.
- The login-failure counter has no label. Do not add an email address or an IP
  address. Both are personal data and would create unbounded cardinality.
- The job-error counter uses a small fixed set of job names.
- Do not add a tenant id, a patient id, a user id or an error message as a
  label.

## What monitoring must not do
- Do not send health data to a third party. The metrics are aggregate
  operational counters. They do not contain patient names, birth dates, notes,
  diagnoses, service codes, tenant ids or user ids.
- Do not send request bodies, query strings or cookies to the monitoring system.
- Do not enable a third-party uptime probe on a URL that contains a patient id
  or a search term.
- Do not log the attempted email address on a failed login.
- If a hosted monitoring service is used, review it as a processor under
  Art. 28 GDPR and keep it out of the patient-data path.

## Alerting
`deployments/monitoring/alerts.yml` covers:
- instance down
- readiness failing
- 5xx error rate above 5 percent
- p95 latency above one second
- pool above 80 percent
- slow queries
- backup older than 26 hours
- more than 20 failed logins in five minutes
- any background job error

`deployments/monitoring/alertmanager.yml` is a template. Add one verified
receiver before production.

## Dashboards
Import `deployments/monitoring/grafana-dashboard.json` and select the
Prometheus data source with uid `prometheus`, or change the uid in the JSON.

## Retention and access
- Keep monitoring data long enough to investigate an incident, but no longer
  than the documented retention policy.
- Restrict access to the monitoring system. It reveals traffic patterns and
  operational weaknesses even without patient data.
- Protect `/metrics` with `METRICS_TOKEN` in production. Use a token of at least
  16 characters and rotate it with `docs/runbooks/key-rotation.md`.
