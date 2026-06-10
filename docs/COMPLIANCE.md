# Compliance — AurelioMod Sentinel Stream

## DSA Art. 15 — Statement of Reasons

Cuando el sistema bloquea contenido, el `block_reason` del proto `AnalyzeResponse` se expone
al usuario vía:
- Discord: DM automático con `block_reason` + enlace a apelación
- Dashboard: historial de decisiones con `block_reason` visible para el streamer
- API: `GET /v1/workspaces/:id/decisions` incluye `decision` y `category`

**Ejemplo**: "Contenido bloqueado: violencia gráfica (confianza 94%). Si considerás que esto es un error, solicitá revisión en https://tudominio.com/appeal"

## GDPR Art. 22 — Derecho a Revisión Humana

**Procedimiento de apelación**:
1. Usuario recibe notificación de bloqueo con link de apelación
2. POST `/v1/workspaces/:id/appeals` crea un ticket de revisión
3. El contenido en cuarentena (R2) se preserva 7 días para revisión
4. Un moderador humano revisa y decide (ALLOW/BLOCK definitivo)
5. SLA: respuesta en 48h hábiles

**Minimización de datos**: Solo se procesan los bytes necesarios para el análisis.
No se almacenan datos personales del usuario final. Los hashes son seudoanonimizados.

## NIS2 Art. 21 — Plan de Respuesta a Incidentes

### Clasificación de severidad

| Nivel | Criterio | Acción |
|---|---|---|
| CRÍTICA | Servicio caído >1h, brecha de datos, CSAM | Notificar CSIRT en 24h |
| ALTA | Rendimiento degradado >4h, acceso no autorizado | Notificar CSIRT en 72h |
| MEDIA | Workspace individual afectado, falsos positivos | Reporte mensual |
| BAJA | Bug aislado, documentación | Registro interno |

### Plantilla de notificación 24h (CSIRT)

```
Entidad: AurelioMod — Sentinel Stream
Tiempo de detección: [UTC]
Tipo de incidente: [DDoS / Brecha / Caída / Vulnerabilidad]
Severidad: [Crítica / Alta]
Impacto transfronterizo: [Sí / No]
Acciones inmediatas: [contención]
Contacto: incident-response@aureliomod.com
```

### Contactos CSIRT por estado miembro

| País | CSIRT | Contacto |
|---|---|---|
| Alemania | BSI/CERT-Bund | certbund@bsi.bund.de |
| Francia | CERT-FR | cert-fr@ssi.gouv.fr |
| España | INCIBE-CERT | incidencias@incibe.es |
| Países Bajos | NCSC-NL | cert@ncsc.nl |

## AI Act Art. 52 — Transparencia

Este sistema utiliza inteligencia artificial (WaveSpeed AI) para moderación de contenido.
Las decisiones automatizadas pueden ser apeladas. El modelo utilizado es
`wavespeed-v3.2` (documentado en audit_log.analyst_version).

## DPIA — Data Protection Impact Assessment

**Resumen**: Disponible en `docs/DPIA.md`. Cubre: datos procesados, base legal,
riesgos identificados, mitigaciones implementadas.

## Retención de datos

| Tipo | Retención | Base legal |
|---|---|---|
| Contenido en cuarentena (R2) | 24 horas (lifecycle auto-delete) | Interés legítimo |
| Audit logs (Neon DB) | 90 días | NIS2 Art. 21 |
| Hashes (DragonflyDB) | Indefinido (seudoanonimizado) | Interés legítimo |
| Datos de pago (Stripe) | Gestionado por Stripe (PCI-DSS) | Contractual |
