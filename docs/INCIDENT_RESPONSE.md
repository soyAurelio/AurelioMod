# Incident Response Plan — AurelioMod

## Contactos

- **Incident Response**: incident-response@aureliomod.com
- **CSIRT Alemania (BSI)**: certbund@bsi.bund.de — +49 228 9582 444
- **CSIRT España (INCIBE)**: incidencias@incibe.es — +34 987 877 899

## Plantilla 24h (NIS2 Art. 21)

```
ASUNTO: [CRÍTICA/ALTA] Notificación temprana de incidente — AurelioMod

Entidad: AurelioMod — Sentinel Stream
Tiempo de detección: [YYYY-MM-DD HH:MM UTC]
Tipo de incidente: [DDoS | Brecha de datos | Caída de servicio | Vulnerabilidad]
Severidad: [Crítica | Alta]
¿Intención maliciosa?: [Sí | No | Desconocido]
Impacto transfronterizo: [Sí (DE, ES, FR) | No]
Acciones inmediatas: [Describir pasos de contención tomados]
Datos afectados: [Ninguno | Tipos de datos comprometidos]
Contacto técnico: [nombre, email, teléfono]
```

## Runbook de recuperación

### Engine caído
1. `docker compose ps engine` — verificar estado
2. `docker compose logs engine --tail=50` — buscar errores
3. `docker compose restart engine`
4. Si no recupera: `docker compose up -d engine --force-recreate`

### WaveSpeed inalcanzable (>50% errores)
1. Verificar `WAVESPEED_PLAN` y `WAVESPEED_API_KEY` en .env
2. Verificar circuit breaker: `docker compose logs engine | grep circuit_breaker`
3. Si persiste: Engine opera en degraded mode (cache-only)

### Neon DB caída
1. Verificar conectividad: `docker compose exec control /control -ping-db` (si implementado)
2. Dashboard de Neon: https://console.neon.tech
3. Engine sigue funcionando con cache (L1/L2 DragonflyDB)
4. Audit events se pierden temporalmente (solo slog stdout)

### Ataque DDoS
1. Escalar VPS en Hetzner Cloud Console
2. Activar Cloudflare DDoS protection (si configurado en Fase 2)
3. Contactar CSIRT si >1h de indisponibilidad
