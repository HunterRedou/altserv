# Deployment assets

| Path | Purpose |
| --- | --- |
| `docker/Dockerfile` | Multi-stage, non-root image. Build from the repository root. |
| `docker/docker-compose.yml` | Local and small-installation template with PostgreSQL, Prometheus, Alertmanager and a readiness probe. |
| `docker/backup.sh` | Verified logical dump and backup marker. Not a PITR replacement. |
| `docker/restore-drill.sh` | Restores the newest dump into a scratch database and checks core tables. |
| `monitoring/prometheus.yml` | Scrape config for the app, Prometheus and the readiness probe. |
| `monitoring/alerts.yml` | Availability, latency, error, pool, slow-query, backup, login and job alerts. |
| `monitoring/alertmanager.yml` | Receiver template. Add a verified receiver before production. |
| `monitoring/blackbox.yml` | HTTP 2xx readiness probe module. |
| `monitoring/grafana-dashboard.json` | Overview dashboard. |
| `monitoring/README.md` | Metric list, label policy and the no-health-data rule. |
| `loadtest/k6.js` | Read-only load test. |
| `sql/explain-analyze.sql` | Read-only plans for the hot paths. |
| `sql/slow-queries.sql` | Investigation queries for a slow-query alert. |

Runbooks are in `docs/runbooks`:
- `database-recovery.md`
- `key-rotation.md`
- `incident-response.md`
- `deployment.md`
- `load-testing.md`
- `slow-queries.md`

The Compose file is a template, not a production orchestrator. Production needs
managed secrets, TLS, a verified backup/PITR product and an on-call receiver.
