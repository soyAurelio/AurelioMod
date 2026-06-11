#!/bin/bash
# AurelioMod — Backup script for all stateful services
# Uso: ./backup.sh [--full]
#   --full  also backs up DragonflyDB (cache, optional)
#
# Backups are stored in ./backups/YYYY-MM-DD/
# Requires: pg_dump, aws CLI (for R2 upload)

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BACKUP_DIR="$SCRIPT_DIR/../backups/$(date +%Y-%m-%d)"
mkdir -p "$BACKUP_DIR"

echo "=== AurelioMod Backup $(date) ==="

# 1. Neon DB (PostgreSQL)
echo "[1/4] Neon DB..."
if [ -n "${NEON_DATABASE_URL:-}" ]; then
    pg_dump "$NEON_DATABASE_URL" \
        --format=custom \
        --no-owner \
        --no-acl \
        > "$BACKUP_DIR/neon-$(date +%H%M).dump"
    echo "  ✅ $(du -h "$BACKUP_DIR"/neon-*.dump | tail -1 | cut -f1)"
else
    echo "  ⚠️  NEON_DATABASE_URL not set — skipping"
fi

# 2. Weaviate
echo "[2/4] Weaviate..."
WEAVIATE="${WEAVIATE_URL:-http://localhost:8090}"
BACKUP_ID="backup-$(date +%Y%m%d-%H%M)"
if curl -sf -X POST "$WEAVIATE/v1/backups" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"$BACKUP_ID\"}" > /dev/null 2>&1; then
    echo "  ✅ Backup $BACKUP_ID started"
else
    echo "  ⚠️  Weaviate backup failed (may be offline)"
fi

# 3. VictoriaMetrics
echo "[3/4] VictoriaMetrics..."
if command -v vmbackup &> /dev/null; then
    vmbackup \
        -storageDataPath=/storage \
        -dst="$BACKUP_DIR/vmetrics-$(date +%H%M).vm" \
        -snapshot.createTimeout=30s 2>&1 | tail -1
    echo "  ✅ VictoriaMetrics backup complete"
else
    echo "  ⚠️  vmbackup not installed — skipping"
fi

# 4. DragonflyDB (only with --full)
if [ "${1:-}" = "--full" ]; then
    echo "[4/4] DragonflyDB..."
    DRAGONFLY="${DRAGONFLY_ADDR:-localhost:6380}"
    if redis-cli -h "${DRAGONFLY%:*}" -p "${DRAGONFLY#*:}" BGSAVE > /dev/null 2>&1; then
        echo "  ✅ BGSAVE triggered"
    else
        echo "  ⚠️  DragonflyDB BGSAVE failed"
    fi
else
    echo "[4/4] DragonflyDB skipped (use --full for cache backup)"
fi

# Upload to R2 if configured
if [ -n "${R2_BACKUP_BUCKET:-}" ]; then
    echo ""
    echo "=== Uploading to R2 ==="
    aws s3 sync "$BACKUP_DIR" "s3://$R2_BACKUP_BUCKET/$(date +%Y-%m-%d)/" \
        --endpoint-url "${R2_ENDPOINT:-https://auto.r2.cloudflarestorage.com}" \
        --no-progress 2>&1 | tail -3
fi

echo ""
echo "=== Backup completado: $BACKUP_DIR ==="
