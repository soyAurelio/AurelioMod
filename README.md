# 🛡 AurelioMod — Sentinel Stream

**Escudo Multimedia Activo | Ultra-Baja Latencia | B2B SaaS**

Protege a creadores de contenido en directo de ataques multimedia maliciosos.
Análisis en milisegundos vía IA — imágenes, GIFs, videos y enlaces externos.

## Arquitectura

```
Discord/Telegram → Edge Bots → Engine (L1→L2→L3 cache + WaveSpeed AI)
                                    │
                              Neon DB (audit) + Stripe (billing)
                                    │
KrakenD API Gateway → Control API (Fiber v3) → Dashboard (futuro)
```

## Stack

| Capa | Tecnología |
|---|---|
| Lenguaje | Go 1.26 |
| RPC | ConnectRPC v1.20 |
| Cache L1/L2 | DragonflyDB |
| Vector DB (L3) | Weaviate |
| Base de datos | Neon DB (PostgreSQL) |
| Message Queue | NATS JetStream |
| API Gateway | KrakenD CE |
| Reverse Proxy | Caddy (SSL) |
| Auth | PASETO v4 |
| Pagos | Stripe |
| IA | WaveSpeed AI |

## Desarrollo rápido

```bash
# Requisitos: Docker, Go 1.26+
cp .env.example .env  # completar con valores reales
docker compose up -d   # levanta todos los servicios

# API en http://localhost:8080
curl http://localhost:8080/healthz
```

## Deploy a producción

```bash
# 1. Provisionar VPS (Hetzner CPX31, Ubuntu 24.04)
curl -sL https://raw.githubusercontent.com/soyAurelio/AurelioMod/master/deployments/production/setup.sh | sudo bash

# 2. Completar .env con valores de producción
# 3. Apuntar DNS: api.tudominio.com → IP del VPS
# 4. Deploy
./deployments/production/deploy.sh --build
curl https://api.tudominio.com/healthz
```

## Estructura del monorepo

```
cmd/            # Entrypoints (engine, edge-discord, control, ytdlp-sidecar)
edge/           # Bot adapters (discord, telegram, twitch)
engine/         # Content analysis (pipeline, hasher, analyzer, media, audit)
control/        # Business logic (api, billing)
internal/       # Shared packages (cache, nats, auth, paseto, circuitbreaker)
proto/          # ConnectRPC contracts
deployments/    # Dockerfiles, configs, schemas, production
```

## Cumplimiento

- **DSA Art. 15**: Statement of reasons generado automáticamente al bloquear contenido
- **GDPR Art. 22**: Procedimiento de apelación para decisiones automatizadas
- **NIS2**: Plan de respuesta a incidentes con plantillas 24h/72h
- **AI Act Art. 52**: Transparencia de uso de IA
- Ver [COMPLIANCE.md](docs/COMPLIANCE.md) para detalles completos.

## Licencia

Propietario. Uso interno. AurelioMod © 2026.
