#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
BACKUP_DIR=${BACKUP_DIR:-/var/backups/hectix}
RETENTION_DAYS=${RETENTION_DAYS:-14}
ts=$(date -u +%Y%m%dT%H%M%SZ)
db_file="$BACKUP_DIR/db-$ts.dump"
up_file="$BACKUP_DIR/uploads-$ts.tar.gz"

mkdir -p "$BACKUP_DIR"
umask 077

docker compose exec -T db pg_dump -U hectix -d hectix -Fc > "$db_file.part"
mv "$db_file.part" "$db_file"

docker run --rm -v hectix_uploads:/srv/uploads:ro -v "$BACKUP_DIR:/backup" alpine \
  tar -czf "/backup/uploads-$ts.tar.gz.part" --exclude=./.tmp -C /srv/uploads .
mv "$up_file.part" "$up_file"
chmod 600 "$up_file"

find "$BACKUP_DIR" -maxdepth 1 -type f \( -name 'db-*.dump' -o -name 'uploads-*.tar.gz' \) \
  -mtime +"$RETENTION_DAYS" -delete

if [[ -n "${OFFSITE_CMD:-}" ]]; then
  $OFFSITE_CMD "$db_file" "$up_file"
fi

echo "backup ok: $db_file $up_file"
