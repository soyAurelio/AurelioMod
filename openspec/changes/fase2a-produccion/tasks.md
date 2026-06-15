# SDD Tasks: Fase 2a — Producción Vultr São Paulo

**Change**: fase2a-produccion  
**Status**: tasks  

---

## Task List

### 🔧 Fase 1: Configuración (cambios de infraestructura, sin código Go)

- [x] **T1 — compose.yml: quitar servicios managed**  
  Servicios `weaviate`, `minio`, `grafana`, `tempo`, `victoriametrics` marcados con `profiles: ["dev"]`.  
  Solo se levantan con `--profile dev`. Producción no los incluye.  
  WEAVIATE_HOSTNAME, OTEL_EXPORTER_OTLP_ENDPOINT, GOMEMLIMIT, R2_* ya leen de env.  
  **Archivos**: `compose.yml`

- [x] **T2 — compose.prod.yml: crear override de producción**  
  Creado con Caddy SSL, hardening de seguridad, límites de recursos por servicio.  
  **Archivos**: `compose.prod.yml`

- [x] **T3 — Caddyfile: actualizar para producción**  
  Dominio `api.aureliomod.com`, Let's Encrypt, HSTS, rate limiting.  
  **Archivos**: `deployments/production/Caddyfile`

- [x] **T4 — setup.sh: actualizar para Vultr SP**  
  Script con swap 2GB, soporte Vultr/Hetzner, schema Weaviate Cloud.  
  **Archivos**: `deployments/production/setup.sh`

- [x] **T5 — deploy.sh: actualizar para producción**  
  Usa `compose.prod.yml` desde raíz, healthcheck post-deploy.  
  **Archivos**: `deployments/production/deploy.sh`

- [x] **T6 — .env.example: actualizar con vars de producción**  
  30 variables documentadas: R2, Weaviate Cloud, Grafana Cloud, feature gates.  
  **Archivos**: `.env.example`

- [x] **T7 — weaviate-schema.json: schema para ModeratedContent**  
  Schema JSON con propiedades: content_hash, decision, category, confidence.  
  **Archivos**: `deployments/weaviate-schema.json`

- [ ] **T8 — krakend.json: verificar backend engine para réplicas**  
  Asegurar que el backend de engine soporte múltiples hosts.  
  Agregar health check al backend.  
  **Archivos**: `deployments/krakend.json`

### 🔧 Fase 2: Código Go (cambios mínimos)

- [x] **T9 — cmd/engine: soporte R2 env vars**  
  Engine lee `R2_ENDPOINT`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET` del entorno.  
  Gateado por `R2_AUDIT_ENABLED=true`. Compatible con S3 (MinIO dev, R2 prod).  
  **Archivos**: `cmd/engine/main.go`

- [x] **T10 — cmd/engine: GOMEMLIMIT desde env**  
  Go 1.19+ lee `GOMEMLIMIT` automáticamente. Documentado en `cmd/engine/main.go:92`.  
  **Archivos**: `cmd/engine/main.go`

### ✅ Fase 3: Verificación (sin cambios de código)

- [x] **T11 — Verificar build local**  
  `go build ./...` — sin errores.  
  `docker compose -f compose.yml -f compose.prod.yml config` — sin errores.

- [ ] **T12 — Verificar healthchecks locales**  
  `docker compose -f compose.yml up -d` (sin override prod).  
  Todos los healthchecks verdes. (Requiere VPS o entorno local con servicios)

- [ ] **T13 — Verificar NATS nkey**  
  Engine se conecta a NATS con nkey auth.  
  (Requiere servicios corriendo)

- [ ] **T14 — Verificar OTLP**  
  Engine exporta trazas sin errores de endpoint.  
  (Requiere Grafana Cloud credentials o Tempo local)

- [x] **T15 — Verificar compose.prod.yml**  
  `docker compose -f compose.yml -f compose.prod.yml config` — sin errores de parseo.  
  7/9 servicios core con límites de recursos definidos. Caddy (proxy ligero) y centrifugo sin límites explícitos.

---

## Orden de Ejecución

```
T1 → T2 → T3 → T4 → T5 → T6 → T7 → T8 → T9 → T10 → T11 → T12 → T13 → T14 → T15
```

## Estimación de Cambios

| Fase | Archivos | Líneas estimadas |
|---|---|---|
| Configuración (T1-T8) | 7-8 archivos | ~300 líneas |
| Código Go (T9-T10) | 1-2 archivos | ~50 líneas |
| Verificación (T11-T15) | 0 archivos | comandos |
| **Total** | **8-10 archivos** | **~350 líneas** |

Dentro del budget de 600 líneas ✅
