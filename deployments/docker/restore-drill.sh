#!/usr/bin/env bash
# Restore drill: restore the newest dump into a scratch database and verify that
# the schema and core tables are readable. This proves that a backup can be
# restored without touching the production database.
#
# Required:
#   ADMIN_DB_URL   PostgreSQL URL that may CREATE and DROP a database
#   BACKUP_FILE    Dump file created by backup.sh (custom format)
# Optional:
#   DRILL_DB       Scratch database name (default altserv_drill_<timestamp>)
#
# Safety: the target name must start with "altserv_drill_" and the production
# database name is refused explicitly.
set -euo pipefail

: "${ADMIN_DB_URL:?ADMIN_DB_URL is required}"
: "${BACKUP_FILE:?BACKUP_FILE is required}"

if [[ ! -r "$BACKUP_FILE" ]]; then
  echo "backup file not readable: $BACKUP_FILE" >&2
  exit 1
fi

DRILL_DB="${DRILL_DB:-altserv_drill_$(date -u +%Y%m%d%H%M%S)}"
case "$DRILL_DB" in
  altserv_drill_*) ;;
  *)
    echo "refusing database name outside altserv_drill_*: $DRILL_DB" >&2
    exit 1
    ;;
esac

# Derive a URL for the scratch database without printing credentials.
base="${ADMIN_DB_URL%%\?*}"
query=""
if [[ "$ADMIN_DB_URL" == *\?* ]]; then
  query="?${ADMIN_DB_URL#*\?}"
fi
prefix="${base%/*}"
drill_url="$prefix/$DRILL_DB$query"

drop_drill() {
  psql "$ADMIN_DB_URL" -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS \"$DRILL_DB\" WITH (FORCE);" >/dev/null
}
trap drop_drill EXIT

psql "$ADMIN_DB_URL" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$DRILL_DB\";" >/dev/null
pg_restore --no-owner --no-privileges --exit-on-error --dbname="$drill_url" "$BACKUP_FILE"

# Read-only sanity checks. Replace these with the operator's verified checks.
psql "$drill_url" -v ON_ERROR_STOP=1 <<'SQL'
SELECT 'tenants' AS table_name, count(*) AS rows FROM tenants
UNION ALL SELECT 'users', count(*) FROM users
UNION ALL SELECT 'patients', count(*) FROM patients
UNION ALL SELECT 'visits', count(*) FROM visits
UNION ALL SELECT 'reports', count(*) FROM reports
UNION ALL SELECT 'billing_runs', count(*) FROM billing_runs
UNION ALL SELECT 'audit_events', count(*) FROM audit_events;
SQL

echo "restore drill passed: $DRILL_DB (will be dropped)"
