#!/usr/bin/env bash
# Logical backup for a small altserv installation.
#
# This script is NOT a full disaster-recovery plan. Production must also run
# continuous WAL archiving (pgBackRest, WAL-G or the managed provider's PITR)
# so a restore can reach a point in time, not only the last dump.
#
# Required:
#   DB_URL              PostgreSQL URL with read access to the database
#   BACKUP_DIR          Directory for dump files (default /var/backups/altserv)
#   BACKUP_MARKER_FILE  Marker touched after a successful dump. The app reads
#                       its mtime and exports altserv_backup_age_seconds.
# Optional:
#   RETENTION_DAYS      Delete local dumps older than this (default 14)
set -euo pipefail

: "${DB_URL:?DB_URL is required}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/altserv}"
BACKUP_MARKER_FILE="${BACKUP_MARKER_FILE:-/var/lib/altserv/last-backup}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"

umask 077
mkdir -p "$BACKUP_DIR" "$(dirname "$BACKUP_MARKER_FILE")"

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
target="$BACKUP_DIR/altserv-$timestamp.dump"
partial="$target.partial"

cleanup() {
  rm -f "$partial"
}
trap cleanup EXIT

# Custom format is compressed and supports pg_restore --clean --if-exists.
pg_dump --format=custom --no-owner --no-privileges --file="$partial" "$DB_URL"

# Verify that pg_restore can read the archive before it replaces a good backup.
pg_restore --list "$partial" >/dev/null

mv "$partial" "$target"
trap - EXIT

# The marker is touched only after the dump is complete and readable.
touch "$BACKUP_MARKER_FILE"

# Local retention. Off-site copies and their retention are a separate control.
find "$BACKUP_DIR" -type f -name 'altserv-*.dump' -mtime "+$RETENTION_DAYS" -delete

echo "backup written: $target"
