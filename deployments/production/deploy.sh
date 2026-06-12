#!/bin/bash
# AurelioMod — Deploy a producción (Fase 2a: Docker Compose + Caddy)
# Uso: ./deploy.sh [--build]
#   --build  fuerza rebuild de imágenes (más lento, necesario tras cambios de código)
#   sin flag  solo levanta contenedores con imágenes existentes

set -euo pipefail
cd "$(dirname "$0")/../.."

COMPOSE_FILES="-f compose.yml -f compose.prod.yml"

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
sleep 8

# Verificar todos los servicios
for svc in caddy krakend control engine edge-discord nats dragonfly centrifugo ytdlp-sidecar; do
    if docker compose $COMPOSE_FILES ps "$svc" 2>/dev/null | grep -q 'Up'; then
        echo "  ✅ $svc"
    else
        echo "  ❌ $svc — revisá: docker compose $COMPOSE_FILES logs $svc"
    fi
done

echo ""
echo "🧪 Health check HTTP..."
HEALTH=$(curl -sf https://localhost:8080/__health 2>/dev/null || curl -sf http://localhost:8080/__health 2>/dev/null || echo "FAIL")
echo "  KrakenD: $HEALTH"

echo ""
echo "=== Deploy completado ==="
