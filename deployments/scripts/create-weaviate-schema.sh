#!/bin/bash
# AurelioMod — Crear schema de Weaviate para L3 cache
# Uso: ./create-weaviate-schema.sh [weaviate_url]
# Default: http://localhost:8090

set -euo pipefail
WEAVIATE="${1:-http://localhost:8090}"

echo "Creando schema ModeratedContent en $WEAVIATE..."

curl -sf -X POST "$WEAVIATE/v1/schema" \
  -H "Content-Type: application/json" \
  -d '{
    "class": "ModeratedContent",
    "description": "L3 cache: previously moderated content decisions",
    "properties": [
      {"name": "content_hash", "dataType": ["string"], "description": "BLAKE3 hash of normalized content"},
      {"name": "decision", "dataType": ["string"], "description": "Moderation decision (DECISION_BLOCK/ALLOW/QUEUED/ERROR)"},
      {"name": "category", "dataType": ["string"], "description": "Classification category"},
      {"name": "confidence", "dataType": ["number"], "description": "AI model confidence (0.0-1.0)"},
      {"name": "workspace_id", "dataType": ["string"], "description": "Workspace that triggered the analysis"}
    ]
  }' 2>&1

echo ""
echo "Schema creado. Verificando..."
curl -sf "$WEAVIATE/v1/schema/ModeratedContent" | python3 -m json.tool 2>/dev/null | head -20
