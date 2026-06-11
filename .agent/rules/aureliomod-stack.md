# AurelioMod — Stack & Architecture Rules

> **Versión**: 2.0 — Junio 2026  
> **Propósito**: Fuente de verdad del stack tecnológico, decisiones de arquitectura y convenciones de desarrollo.  
> **Aplica a**: Todo el desarrollo de AurelioMod. Cualquier desviación requiere actualizar este documento.

---

## 1. Stack Tecnológico Definitivo

### Core
| Componente | Tecnología | Versión/Nota | Licencia | Decisión |
|---|---|---|---|---|
| Lenguaje | **Go** | 1.26.4+ | BSD | No negociable |
| RPC Framework | **ConnectRPC** | v1.20+ | Apache 2.0 | Reemplaza gRPC estándar |
| Hashing de contenido | **BLAKE3** | — | CC0/Public Domain | Reemplaza SHA-256 (~7x más rápido) |
| Message Queue | **NATS + JetStream** | v2.14+ | Apache 2.0 | Async entre Edge y Engine |
| WebSocket/Real-time | **Centrifugo** | v6.8+ | Apache 2.0 | Dashboard en tiempo real |
| API Gateway | **KrakenD** | Community Edition | Apache 2.0 | Rate limit, auth, circuit breaker |
| Auth Tokens | **PASETO** (v4) | aidantwoods/go-paseto | MIT | Reemplaza JWT; sin alg=none, sin CVEs |
| Circuit Breaker | **failsafe-go** + `failsafehttp`/`failsafegrpc` | v0.9+, suite completa | MIT | CB + retry + bulkhead + rate limiter + fallback (todo en 1 módulo) |
| SBOM | **syft** (Anchore) | Latest | Apache 2.0 | Generación de SBOM en CI (NIS2) |

### Datos
| Componente | Tecnología | Nota | Licencia |
|---|---|---|---|
| Cache/In-Memory | **DragonflyDB** | v1.38+, BSL license | BSL (free para uso) |
| Base de datos | **Neon DB** | Always-On (no cold start) | Propietario (free tier generoso) |
| Vector Database | **Weaviate** | v1.37+, Go 96% | BSD-3 | Búsqueda semántica L3, HNSW indexado |
| Object Storage (prod) | **Cloudflare R2** | S3-compatible, sin egress | Propietario |
| Object Storage (dev) | **MinIO** | S3-compatible | Apache 2.0 |

### Observabilidad
| Componente | Tecnología | Nota | Licencia |
|---|---|---|---|
| Métricas | **VictoriaMetrics** | v1.144+, PromQL compatible | Apache 2.0 |
| Trazas | **Grafana Tempo** | Object storage backend | AGPLv3 |
| Logs | **slog** (Go stdlib) | JSON output, structured | Go stdlib |
| Instrumentación | **OpenTelemetry** | Go SDK | Apache 2.0 |

### Infraestructura
| Componente | Tecnología | Nota | Licencia |
|---|---|---|---|
| Contenedores | **Docker** | Multi-stage builds, Distroless | Apache 2.0 |
| Orquestación (Fase 1) | **Docker Compose** | MVP, servidor único | — |
| Orquestación (Fase 2) | **Kamal** | Zero-downtime deploys | MIT |
| Orquestación (Fase 3) | **Kamal + LB externo** | Multi-VPS, pre-baked images | — |
| CI/CD | **Woodpecker CI** | Self-hosted | Apache 2.0 |
| Testing | **synctest** (Go 1.26) + **testcontainers** (suite-level) | — | Go stdlib / MIT |
| Secrets | **env vars + .env** (Fase 1) → **Vault** (Fase 3) | .env se excluye de git | — |

### Negocio
| Componente | Tecnología | Nota |
|---|---|---|
| Pagos | **Stripe** | Webhooks, suscripciones |
| URL Safety | **Google Web Risk API v1** | Verificación pre-yt-dlp |
| Media Processing | **FFmpeg** + **yt-dlp** | Sandboxed (firejail/nsjail) |

---

## 2. Arquitectura General

```
                         Cloudflare DNS / LB (Fase 2+)
                                   │
                          ┌─── KRAKEND ───┐
                          │ Rate limit     │
                          │ Auth (PASETO)  │
                          │ Circuit break  │
                          └───┬───┬───┬───┘
                              │   │   │
                   CONNECTRPC (gRPC + HTTP)
                              │   │   │
                 ┌────────────┘   │   └────────────┐
                 ▼                ▼                ▼
           ┌─────────┐    ┌──────────┐    ┌──────────┐
           │  EDGE   │    │  ENGINE  │    │ CONTROL  │
           │ Discord │    │ WaveSpeed│    │ Dashboard│
           │ Telegram│    │ FFmpeg   │    │ Auth     │
           │ Twitch  │    │ yt-dlp   │    │ Stripe   │
           └────┬────┘    └────┬─────┘    └────┬─────┘
                │              │               │
                └──────────────┼───────────────┘
                               │
                     NATS + JetStream  ←── Bus asíncrono compartido
                               │
          ┌──────────┬─────────┼─────────┬──────────┐
          ▼          ▼         ▼          ▼          ▼
    DRAGONFLYDB  WEAVIATE  NEON DB   CENTRIFUGO  R2/MINIO
    (BLAKE3/     (L3       (Auth/    (WebSocket  (Object
    pHash)       vector)   Planes)   Dashboard)  Storage)
          │          │         │          │          │
          └──────────┴─────────┴──────────┴──────────┘
                               │
                VICTORIAMETRICS + GRAFANA TEMPO
```

### Principios arquitectónicos
1. **Stateless services**: Todo servicio Go es stateless. El estado vive en DragonflyDB, Neon DB y NATS.
2. **Cache-first**: Toda consulta al Engine pasa por DragonflyDB (L1 BLAKE3, L2 pHash) antes de llegar a Weaviate (L3 vectorial).
3. **Cuarentena Invertida**: Bloquear contenido primero, analizar después (no al revés).
4. **Degradación elegante**: Si WaveSpeed cae → bloquear enlaces externos + usar decisiones cacheadas.
5. **gRPC interno, HTTP externo**: ConnectRPC unifica ambos protocolos.

---

## 3. Convenciones de Código Go

### Estructura de monorepo
```
aureliomod/
├── proto/                    # Protobuf definitions (shared)
│   └── aureliomod/
│       └── v1/
├── edge/                     # Bot adapters
│   ├── discord/
│   ├── telegram/
│   └── twitch/
├── engine/                   # Content analysis
│   ├── hasher/               # BLAKE3 + pHash
│   ├── analyzer/             # WaveSpeed integration
│   ├── media/                # FFmpeg + yt-dlp
│   └── quarantine/           # Quarantine logic
├── control/                  # Dashboard + API
│   ├── api/
│   ├── dashboard/
│   └── billing/
├── internal/                 # Shared packages
│   ├── nats/
│   ├── cache/
│   ├── auth/
│   └── telemetry/
├── deployments/              # Kamal configs, Dockerfiles
├── .woodpecker/              # CI pipelines
└── buf.gen.yaml              # ConnectRPC codegen
```

### Reglas de código
- **slog** para logging estructurado (siempre JSON en producción)
- **context.Context** se propaga a través de TODOS los límites gRPC/NATS
- **sync.Pool** para buffers de bytes de 32KB en operaciones de streaming
- **GOMAXPROCS = N-1** en el Engine (reservar 1 core para FFmpeg subprocess)
- **pprof** endpoint protegido con auth admin siempre habilitado
- **Linter**: golangci-lint con configuración estricta
- **Formato**: gofumpt (no gofmt)

### Hashing de contenido (sobre píxeles crudos)
```go
import "lukechampine/blake3"

// L1 Cache: BLAKE3 sobre píxeles RGB crudos decodificados (NO sobre bytes JPEG)
// Esto garantiza determinismo cross-version de FFmpeg y cross-arquitectura CPU.
func HashContent(rgbPixels []byte) string {
    return hex.EncodeToString(blake3.Sum256(rgbPixels))
}

// Ejemplo de pipeline:
//   rawBytes := readFromDiscord()
//   rgbPixels, jpegBytes := normalize(rawBytes) // FFmpeg: decode → 480p → rgb24
//   hash := HashContent(rgbPixels)              // Hash sobre píxeles, determinista
//   store(jpegBytes, hash)                      // JPEG a R2, hash a DragonflyDB L1
```

### Pipeline de Normalización (antes del hash)
El Engine aplica normalización **explícita y documentada** antes del hash. El hash L1 se calcula sobre **píxeles RGB crudos decodificados**, NO sobre bytes codificados (JPEG/PNG). Esto garantiza determinismo cross-version de FFmpeg y cross-arquitectura de CPU. Los JPEGs re-encoded se usan para storage/quarantine, no para hashing.

| Paso | Operación | Herramienta | Propósito |
|---|---|---|---|
| 1 | Decode + resize a 480p | FFmpeg (`-vf scale=-2:480,format=rgb24`) | Unificar resolución; decodificar a espacio de color determinista |
| 2 | Strip EXIF/metadata | FFmpeg (`-map_metadata -1`) | Eliminar timestamps, geolocation, campos variables |
| 3 | Output raw RGB24 pixels | FFmpeg (`-f rawvideo -pix_fmt rgb24`) | Bytes de píxeles crudos → BLAKE3 hash |
| 4 | BLAKE3 hash | `blake3.Sum256(rgbPixels)` | Hash determinista sobre píxeles, NO sobre bytes JPEG |
| 5 | (Separado) JPEG Q85 encode | FFmpeg (`-q:v 3`) | Output para storage/quarantine — NO usado para hash |

### ⚠️ Regla de versionado de FFmpeg (Obligatoria)
```dockerfile
# Dockerfile del Engine — NO usar :latest
FROM jrottenberg/ffmpeg:7.1-ubuntu AS ffmpeg
```
La versión de FFmpeg debe estar **pineada explícitamente** y ser **idéntica en dev, CI y producción**. Si se actualiza FFmpeg, los hashes L1 de contenido cacheado se invalidan si se usa JPEG hash. Con el hash sobre píxeles crudos, esto es menos crítico pero se mantiene la regla por consistencia visual del re-encoding.

**Flujo completo L1→L2→L3:**
```
Contenido crudo
    │
    ▼
[FFmpeg: decode → resize 480p → strip EXIF]
    │
    ├──► raw RGB24 pixels ──► [BLAKE3 Sum256] ──► hit en DragonflyDB? ──► ✅ DECISIÓN CACHEADAS
    │                                                      │ miss
    │                                                      ▼
    │                              [pHash perceptual] ──► distancia Hamming ≤5? ──► ✅ DECISIÓN CACHEADAS
    │                              (sobre mismos RGB24)       │ miss
    │                                                      ▼
    │                              [Weaviate vector search] ──► similitud >0.92? ──► ✅ DECISIÓN CACHEADAS
    │                                                      │ miss
    │                                                      ▼
    │                              [WaveSpeed API] ──► Nueva decisión → almacenar en L1, L2, L3
    │
    └──► JPEG Q85 encode ──► Storage/Quarantine (R2)
```
**Coherencia L1↔L2**: Tanto BLAKE3 (L1) como pHash (L2) operan sobre los mismos píxeles RGB24 decodificados. El pHash reduce la imagen a 8×8 o 16×16 píxeles y compara distribución de grises — trabaja nativamente sobre píxeles, no sobre bytes JPEG. Documentarlo explícitamente garantiza que ambos niveles de cache parten del mismo punto de normalización.

### Conexiones
- **DragonflyDB**: Pool de 100 conexiones por instancia Edge. Broker compartido para Centrifugo en Fase 2+ (multi-instancia).
- **Neon DB**: max 200 conexiones (pool nativo de Neon)
- **Weaviate**: gRPC nativo, conexión persistente desde Engine para búsqueda L3
- **NATS**: Conexión persistente por servicio, reconnect automático
- **ConnectRPC**: HTTP/2 persistente con multiplexación
- **Centrifugo**: Usa DragonflyDB como broker compartido para sticky sessions entre múltiples instancias del Engine (requisito Fase 2+)

---

## 4. Estrategia de Audit Logging (NIS2 / GDPR Compliance)

Cada decisión de moderación genera un **audit event inmutable** que permite trazabilidad completa para auditorías NIS2 e impugnaciones bajo GDPR Art. 22.

### Esquema del Audit Event
```json
{
  "audit_id": "evt_abc123",
  "workspace_id": "ws_xyz",
  "content_hash": "b3:a1b2c3d4...",
  "decision": "blocked",
  "confidence": 0.94,
  "category": "violence_graphic",
  "analyst_version": "wavespeed-v3.2",
  "normalization_pipeline": "480p+strip_exif+jpeg_q85",
  "processing_ms": 142,
  "timestamp_utc": "2026-06-03T22:45:00Z"
}
```

### Destinos del Audit Event
| Destino | Propósito | Retención |
|---|---|---|
| **slog (JSON stdout)** | Logs en tiempo real → VictoriaMetrics/Grafana | 30 días (rotación) |
| **Neon DB (tabla `audit_log`)** | Append-only, sin UPDATE/DELETE vía RBAC | 90 días (NIS2) |
| **R2 (cold storage)** | Auditorías históricas, exportación bajo solicitud | 12 meses |

### Reglas de la tabla audit_log
- **Append-only**: RBAC restringe INSERT. Nadie (ni admin) puede UPDATE o DELETE.
- **Índices**: `(workspace_id, timestamp_utc)` para consultas por cliente + fecha.
- **Particionamiento**: Por mes (facilita purga automática a los 90 días).
- **Inmutable**: Cualquier intento de modificación genera alerta en VictoriaMetrics.

### ¿Por qué esto es suficiente para compliance?
- **GDPR Art. 22**: Si un usuario impugna un bloqueo, audit_log demuestra exactamente qué análisis se hizo, con qué modelo, y con qué confianza. El `category` se expone en el dashboard como "statement of reasons" DSA.
- **NIS2 Art. 21**: En una auditoría de seguridad, podés demostrar trazabilidad completa del pipeline de moderación. Los audit events exportados de R2 son el registro oficial.
- **AI Act Art. 52**: El campo `analyst_version` documenta qué versión del modelo tomó la decisión.

---

## 5. Estrategia de Testing
```go
func TestStreamingTimeout(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        // Tiempo determinista, sin sleeps reales
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        // ...
    })
}
```

### Testcontainers (suite-level, NO por test)
```go
var natsContainer *testcontainers.NATSContainer

func TestMain(m *testing.M) {
    // Levantar UNA vez para todo el paquete
    ctx := context.Background()
    natsContainer = startNATS(ctx)
    defer natsContainer.Terminate(ctx)
    
    code := m.Run()
    os.Exit(code)
}
```

### Neon Database Branching para CI
```yaml
# .woodpecker/test.yml
steps:
  test:
    image: golang:1.26
    commands:
      - neon branches create --parent main --name ci-$CI_BUILD_NUMBER
      - export DATABASE_URL=$(neon connection-string ci-$CI_BUILD_NUMBER)
      - go test ./... -count=1 -race
      - neon branches delete ci-$CI_BUILD_NUMBER
```

---

## 6. Estrategia de Infraestructura por Fase

### Fase 1: MVP (Servidor único, <10 clientes)
```
Orquestación: docker compose
Servidor:     1x Hetzner VPS (CPX31: 4 vCPU, 8GB RAM)
Deploy:       git pull + docker compose up -d
Secretos:     Doppler
CI/CD:        Woodpecker (tests + lint)
```
**No necesitás**: Kamal, Load Balancer, Multi-VPS, auto-scaling.

### Fase 2: Crecimiento (2+ servidores, 10-50 clientes)
```
Orquestación: Kamal
Servidores:   2x Hetzner VPS
LB:           Cloudflare Load Balancer
Deploy:       kamal deploy (zero-downtime)
Secretos:     Kamal .env.encrypted
NATS:         Cluster entre ambos VPS
DragonflyDB:  Primario + Réplica
```

### Fase 3: Enterprise (3+ servidores, 50+ clientes)
```
Orquestación: Kamal + pre-baked images (Packer)
LB:           Cloudflare + Hetzner Cloud Networks
Secretos:     HashiCorp Vault
Auto-scaling: cloud-init + docker run en nuevos VPS
Multi-region: Frankfurt + Ashburn
Compliance:   SOC 2 Tipo I
```

### ¿Cuándo pasar de fase?
| Trigger | Acción |
|---|---|
| >5 clientes pagadores | Migrar de compose a Kamal |
| >20% CPU sostenido en VPS único | Añadir segundo VPS + LB |
| Downtime >5min inaceptable | Pre-baked images + auto-scaling |
| Cliente enterprise pide SOC 2 | Fase 3 completa |

---

## 7. Seguridad

### Principios
- **Zero trust entre servicios**: Todo gRPC autenticado con PASETO v4 (incluso interno). Sin JWT.
- **Sandboxing**: yt-dlp y FFmpeg corren en firejail/nsjail
- **Rate limiting**: KrakenD lo maneja en el borde
- **Circuit breaker**: failsafe-go en todas las llamadas a WaveSpeed (5 fallos/60s → abrir, 30s → semi-abrir)
- **Secrets**: Nunca en código, nunca en imágenes Docker
- **Dependencias**: govulncheck en CI, verificación de sumdb.golang.org
- **SBOM**: syft genera Software Bill of Materials en cada release (requisito NIS2)

### Checklists
- [ ] PASETO v4 con rotación de keys implementado
- [ ] Circuit breaker (failsafe-go) en llamadas a WaveSpeed
- [ ] Rate limiting configurado en KrakenD
- [ ] Sandboxing de yt-dlp activo
- [ ] endpoints pprof protegidos con auth
- [ ] govulncheck + syft SBOM en CI pipeline
- [ ] Imágenes Distroless (sin shell)
- [ ] MFA en todo acceso administrativo

### Cumplimiento Legal desde MVP (Obligatorio Día 1)
Estos requisitos aplican desde el momento en que procesás datos de ciudadanos europeos. No son "Fase 2", son **MVP**.

| Regulación | Requisito | Implementación |
|---|---|---|
| **GDPR Art. 22** | Derecho a revisión humana de decisiones automatizadas | Dashboard expone `block_reason` (campo `category` del proto). Procedimiento de apelación documentado (email + SLA 48h). |
| **DSA Art. 15** | "Statement of reasons" al bloquear contenido | Notificación automática con: razón del bloqueo, base factual, opciones de recurso. Ya está en el modelo de datos. |
| **NIS2 Art. 21** | Plan de respuesta a incidentes | Documento con plantillas de notificación 24h/72h/1-mes al CSIRT, umbrales de severidad, contactos por estado miembro. |
| **DPIA** | Evaluación de Impacto en Protección de Datos | Documento pre-deploy que describa: datos procesados, base legal, riesgos, mitigaciones. |
| **AI Act Art. 52** | Transparencia de uso de IA | Aviso en dashboard: "Este sistema utiliza IA para moderación de contenido". |

---

## 8. CI/CD Pipeline (Woodpecker)

```yaml
# .woodpecker.yml
steps:
  lint:
    image: golangci/golangci-lint:latest
    commands:
      - golangci-lint run ./...

  test:
    image: golang:1.26
    commands:
      - neon branches create --parent main --name ci-$CI_BUILD_NUMBER
      - go test ./... -count=1 -race -coverprofile=coverage.out
      - neon branches delete ci-$CI_BUILD_NUMBER

  security:
    image: golang:1.26
    commands:
      - go install golang.org/x/vuln/cmd/govulncheck@latest
      - govulncheck ./...

  sbom:
    image: anchore/syft:latest
    commands:
      - syft dir:. -o spdx-json > sbom-$CI_COMMIT_SHA.spdx.json
    artifacts:
      - sbom-*.spdx.json

  build:
    image: golang:1.26
    commands:
      - CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" ./...

  # Solo en main/master:
  deploy:
    image: kamal:latest
    when:
      branch: main
    commands:
      - kamal deploy
```

---

## 9. Referencias

- [ConnectRPC Docs](https://connectrpc.com/docs/go/getting-started)
- [NATS JetStream](https://docs.nats.io/nats-concepts/jetstream)
- [Centrifugo Docs](https://centrifugal.dev)
- [KrakenD Community](https://www.krakend.io)
- [VictoriaMetrics](https://docs.victoriametrics.com)
- [DragonflyDB](https://www.dragonflydb.io/docs)
- [Neon Database Branching](https://neon.tech/docs/introduction/branching)
- [Weaviate Docs](https://weaviate.io/developers/weaviate)
- [failsafe-go](https://failsafe-go.dev)
- [PASETO Spec](https://github.com/paragonie/paseto)
- [go-paseto](https://pkg.go.dev/aidanwoods.dev/go-paseto)
- [syft (SBOM)](https://github.com/anchore/syft)
- [Kamal Docs](https://kamal-deploy.org)
- [Woodpecker CI](https://woodpecker-ci.org)
- [synctest](https://pkg.go.dev/testing/synctest)
- [BLAKE3 Go](https://pkg.go.dev/lukechampine.com/blake3)

---

> **Última actualización**: Junio 2026  
> **Mantenedor**: AurelioMod  
> **Cambios a este documento**: PR requerido, revisión de arquitectura requerida.

## 10. Fase 2 — Multi-VPS (documentado, no implementado aún)

### Triggers de migración
| Trigger | Acción |
|---|---|
| >5 clientes pagadores | Migrar de compose a Kamal |
| >20% CPU sostenido en VPS único | Añadir segundo VPS + LB |
| Downtime >5min inaceptable | Pre-baked images + auto-scaling |
| Cliente enterprise pide SOC 2 | Fase 3 completa |

### mTLS interno (requerido en multi-VPS)
- Certificados generados por step-ca o HashiCorp Vault PKI
- ConnectRPC con TLS config: `connect.WithTLS(...)` en todos los handlers
- NATS con TLS: `tls {}` block en nats-server.conf
- DragonflyDB con TLS: `--tls-port 6380` + certificados
- Rotación automática de certificados cada 30 días

### Multi-region
- Engine instances: Frankfurt (Hetzner) + Ashburn (DigitalOcean)
- DragonflyDB replicación cross-region
- R2 sirve ambas regiones desde Cloudflare edge
- Centrifugo con Redis Cluster para sticky sessions cross-region

### Escalamiento
- Kamal + pre-baked images (Packer)
- Cloudflare Load Balancer
- Auto-scaling: cloud-init + docker run en nuevos VPS
- Secrets: Doppler → Kamal .env.encrypted → HashiCorp Vault

## 11. Observabilidad — Completada ✅

| Componente | Estado | Detalle |
|---|---|---|
| Métricas | ✅ | VictoriaMetrics v1.144 + OpenTelemetry SDK |
| Trazas | ✅ | Grafana Tempo via OTLP gRPC |
| Logs | ✅ | slog JSON structured en todos los servicios |
| Dashboards | ✅ | Grafana con engine dashboard provisionado |
| Alertas | ✅ | vmalert.yml con reglas de latencia y errores |
| pprof | ✅ | Puerto 6060 protegido con PASETO admin token |

## 12. Seguridad en Runtime — Implementado ✅

| Medida | Estado | Detalle |
|---|---|---|
| Distroless | ✅ | Control y Edge-Discord usan gcr.io/distroless/static-debian12:nonroot |
| Distroless Engine | ✅ | distroless/cc-debian12:nonroot + FFmpeg + nsjail |
| nsjail sandbox | ✅ | FFmpeg ejecutado con nsjail: sin red, /tmp write-only |
| yt-dlp sandbox | ✅ | Sidecar container aislado, sin --no-check-certificates |
| read-only rootfs | ✅ | Todos los contenedores con `read_only: true` en prod |
| no-new-privileges | ✅ | `security_opt: [no-new-privileges:true]` |
| Seccomp profiles | ✅ | `seccomp:default_and_perf.json` en prod |
| non-root user | ✅ | USER nonroot:nonroot en Distroless |
| capabilities drop | ✅ | `cap_drop: [ALL]` en prod |

## 13. Estrategia de Backups y Disaster Recovery

### Backups
| Dato | Herramienta | Frecuencia | Retención |
|---|---|---|---|
| Neon DB | `pg_dump` + S3 | Diario | 30 días |
| Weaviate | Backup API → R2 | Diario | 7 días |
| VictoriaMetrics | `vmbackup` → R2 | Diario | 30 días |
| DragonflyDB | RDB snapshot | Horario | 24 horas |

### RPO (Recovery Point Objective)
- Neon DB: 24 horas (backup diario)
- Weaviate: 24 horas
- VictoriaMetrics: 24 horas
- DragonflyDB: 1 hora (cache, puede reconstruirse)

### RTO (Recovery Time Objective)
- Servicio completo: <2 horas (docker compose up en VPS fresca)
- Base de datos: <30 minutos (pg_restore desde último dump)

### Procedimiento de restore
```bash
# 1. Neon DB
pg_restore -d "$NEON_DATABASE_URL" latest-backup.dump

# 2. Weaviate
curl -X POST http://weaviate:8080/v1/backups/restore \
  -H "Content-Type: application/json" \
  -d '{"id": "latest"}'

# 3. VictoriaMetrics
vmrestore -src latest-backup.vm -dst /storage

# 4. DragonflyDB
# Copiar dump.rdb al directorio de datos y reiniciar
```

## 14. Pruebas de Carga

### Herramienta: k6 (Grafana)
```javascript
// loadtest/k6-smoke.js — Smoke test: 10 VUs, 30s
import http from 'k6/http';
export const options = { vus: 10, duration: '30s' };
export default function() {
  http.get('http://engine:8080/healthz');
}
```

### Escenarios
| Escenario | VUs | Duración | Objetivo |
|---|---|---|---|
| Smoke | 10 | 30s | Health checks básicos |
| Load | 50 | 5min | 50 req/s al Engine |
| Stress | 200 | 10min | Encontrar breaking point |
| Soak | 50 | 1h | Memory leaks, GC pressure |

### Métricas objetivo (Fase 1)
- p50 < 200ms
- p95 < 1s
- p99 < 2s
- Error rate < 1%
- Sin OOM kills en 1h de soak test

## 15. Gestión de Incidentes

### Runbooks documentados en docs/INCIDENT_RESPONSE.md
- Engine caído → restart vía docker compose
- WaveSpeed inalcanzable → degraded mode (cache-only)
- Neon DB caída → seguir con cache L1/L2, audit events solo stdout
- Ataque DDoS → escalar VPS, activar Cloudflare DDoS protection

### Contactos CSIRT (NIS2)
| País | Organismo | Contacto |
|---|---|---|
| Alemania | BSI/CERT-Bund | certbund@bsi.bund.de |
| Francia | CERT-FR | cert-fr@ssi.gouv.fr |
| España | INCIBE-CERT | incidencias@incibe.es |
| Países Bajos | NCSC-NL | cert@ncsc.nl |

### Severidades
| Nivel | Criterio | Notificación |
|---|---|---|
| CRÍTICA | Servicio caído >1h, brecha de datos | CSIRT 24h |
| ALTA | Rendimiento degradado >4h | CSIRT 72h |
| MEDIA | Workspace único afectado | Reporte mensual |
| BAJA | Bug aislado | Registro interno |
