#!/bin/bash
# AurelioMod — Deploy a producción (Fase 1: Docker Compose)
# Uso: ./deploy.sh [--build]
#   --build  fuerza rebuild de imágenes (más lento, necesario tras cambios de código)
#   sin flag  solo levanta contenedores con imágenes existentes

set -euo pipefail
cd "$(dirname "$0")/../.."

COMPOSE_FILES="-f compose.yml -f deployments/production/compose.prod.yml"

if [ "${1:-}" = "--build" ]; then
    echo "🏗️  Build + Deploy..."
    docker compose $COMPOSE_FILES build --pull
    docker compose $COMPOSE_FILES up -d --force-recreate
else
    echo "🚀 Deploy (sin rebuild)..."
    docker compose $COMPOSE_FILES up -d
fi

echo ""
echo "⏳ Esperando health checks..."
sleep 5

# Verificar servicios críticos
for svc in caddy krakend control engine; do
    if docker compose $COMPOSE_FILES ps "$svc" | grep -q 'Up'; then
        echo "  ✅ $svc"
    else
        echo "  ❌ $svc — revisá: docker compose $COMPOSE_FILES logs $svc"
    fi
done

echo ""
echo "🧪 Health check..."
HEALTH=$(curl -sf https://localhost:8080/healthz 2>/dev/null || curl -sf http://localhost:8080/healthz 2>/dev/null || echo "FAIL")
echo "  $HEALTH"

echo ""
echo "=== Deploy completado ==="
