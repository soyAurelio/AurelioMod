#!/bin/bash
# OWASP ZAP Baseline Scan — AurelioMod API Gateway
# Uso: ./owasp-zap-scan.sh [http://localhost:8080]
set -euo pipefail
TARGET="${1:-http://localhost:8080}"
echo "=== OWASP ZAP Baseline Scan — $TARGET ==="
docker run --rm --network host -v "$(pwd):/zap/wrk" \
  ghcr.io/zaproxy/zaproxy:stable \
  zap-baseline.py -t "$TARGET" -r scan-report.html
echo "Reporte: scan-report.html"
