# Runbook: load testing

## Goal
Measure whether altserv meets its latency and error targets under expected
traffic, and find the point where it stops meeting them.

## Rules
- Load-test a staging copy or a production-sized copy, not production, unless
  the operator explicitly approves a read-only test in a maintenance window.
- Use a dedicated read-only account. Never use a real chef account and never
  run a write scenario against real patient data.
- Do not put patient data into the load-test database. Use synthetic tenants and
  patients that were created for the test.
- Watch the database while the test runs. A load test that saturates the pool
  can affect other users of the same database.

## Test assets
- `deployments/loadtest/k6.js`: read-only scenario for `/api/me`,
  `/api/patients`, `/healthz` and `/readyz`.
- `cmd/loadgen`: stdlib read-only fallback for machines without k6. It uses the
  same paths and thresholds and exits non-zero when a threshold fails.
- `deployments/sql/explain-analyze.sql`: plans for the hot paths.
- `deployments/monitoring/grafana-dashboard.json`: latency, error rate, pool,
  slow queries, login failures, job errors and backup age.

## 1. Prepare
1. Create the load-test tenant, employee and a few synthetic patients.
2. Confirm the test account can log in and list patients.
3. Take a baseline: no test running, record p50/p95/p99 latency, error rate,
   pool in use, slow queries and CPU for 15 minutes.
4. Set `VUS`, `DURATION` and `SLEEP` for the first run. Start with 10 VUs for
   2 minutes.

## 2. Run
```bash
BASE_URL=https://staging.example.invalid \
LOAD_EMAIL=loadtest@example.invalid \
LOAD_PASSWORD='...' \
VUS=10 DURATION=2m SLEEP=0.5 \
k6 run deployments/loadtest/k6.js
```

The script logs in once and shares the session cookie, so the login rate limiter
is not part of the measurement. The default thresholds fail the run when the
error rate is at or above 1 percent, p95 is at or above 500 ms, or p99 is at or
above 1500 ms. Change the thresholds only with a documented reason.

### Fallback without k6

`cmd/loadgen` is a small stdlib generator for a machine where k6 is not
installed. It logs in once, sends read-only GET requests with a fixed
concurrency, and prints the request rate, error rate, latency percentiles and
status distribution. It exits non-zero when the same thresholds fail.

```bash
go run ./cmd/loadgen \
  -base https://staging.example.invalid \
  -email loadtest@example.invalid -password '...' \
  -paths /api/me,/api/patients,/healthz,/readyz \
  -c 10 -d 2m -warmup 10s
```

Do not run it against production with a real chef account. It is a fallback for
a repeatable smoke measurement, not a replacement for k6 or a capacity model.
The `hey` command on some developer machines is the HEY email client, not
rakyll/hey; check `hey --help` before using it.

## 3. Observe
During the run, watch:
- `altserv_http_request_duration_seconds` p95 and p99
- `altserv_http_requests_total` by status
- `altserv_http_in_flight`
- `altserv_db_pool_in_use` and `altserv_db_pool_max_open_connections`
- `altserv_db_slow_queries`
- database CPU, disk I/O, locks and `pg_stat_activity`

Stop the run if:
- the error rate exceeds 1 percent for more than one minute
- p95 exceeds 1 second for more than two minutes
- the pool stays above 90 percent
- the database starts swapping or the disk fills
- a real user reports a problem

## 4. Increase in steps
Use 10, 25, 50 and 100 VUs, or the expected peak. Keep each step long enough to
reach a steady state. Record the result after each step. Do not jump straight to
the maximum.

## 5. Interpret
- Latency rises with VUs but the error rate stays flat: the system is near its
  capacity. Check pool wait time and slow queries before adding instances.
- Pool wait time rises first: the database or the pool limit is the bottleneck.
  Raise `DB_MAX_OPEN_CONNS` only after checking PostgreSQL's connection limit.
- One route dominates the latency: inspect its plan with
  `deployments/sql/explain-analyze.sql`.
- CPU is low but latency is high: look for locks, I/O waits or a slow external
  call. The app has no generative AI and no synchronous third-party health-data
  call, so the database is the usual cause.

## 6. Record
Keep a short result table:
| Date | Commit | VUs | Duration | p95 | p99 | Error rate | Pool peak | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |

Attach the k6 summary and the Grafana snapshot. Do not attach plans or logs that
contain patient data.

### Recorded local smoke run (2026-11-12)

This run used `cmd/loadgen` on loopback against one `go run .` process, the
synthetic demo employee and synthetic patients from `cmd/devseed`. Measurement
duration was 20 seconds after a 2-second warmup. It is a reproducibility check,
not capacity evidence: it ran on the developer machine, not on staging, and it
did not capture database, pool or host telemetry.

| Paths | Concurrency | Requests | Requests/s | Errors | p95 | p99 |
| --- | --- | --- | --- | --- | --- | --- |
| `/api/me`, `/api/patients`, `/healthz`, `/readyz` | 10 | 418,598 | 20,917 | 0 | 1.57 ms | 4.08 ms |
| `/api/me`, `/api/patients`, `/healthz`, `/readyz` | 25 | 363,286 | 18,153 | 0 | 4.85 ms | 8.05 ms |
| `/api/me`, `/api/patients`, `/healthz`, `/readyz` | 50 | 750,942 | 37,488 | 0 | 3.63 ms | 6.36 ms |
| `/api/patients` | 10 | 242,363 | 12,114 | 0 | 1.41 ms | 4.72 ms |

All runs met the default thresholds. The numbers are not a statement about
production throughput; they only show that the read paths stay fast under a
loopback smoke load.

## Known gaps
- There is no write-path load test. A safe write test needs a disposable tenant
  and a cleanup job.
- The login rate limiter is per process and per source address. A login load
  test from one host is intentionally limited.
- The k6 script is a starting point, not a capacity model. The operator must
  define the expected peak from real traffic.
