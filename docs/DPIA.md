# DPIA — Data Protection Impact Assessment

## 1. Descripción del procesamiento

AurelioMod Sentinel Stream analiza contenido multimedia (imágenes, videos, audio)
enviado por usuarios de plataformas de streaming (Discord, Twitch) para detectar
contenido malicioso o inapropiado antes de que sea visible para la audiencia.

## 2. Datos procesados

| Dato | Categoría | Retención |
|---|---|---|
| Bytes de imagen/video/audio | Contenido generado por usuario | 24h (cuarentena R2) |
| Hash perceptual (pHash) | Seudoanonimizado | Indefinido |
| BLAKE3 hash | Seudoanonimizado | Indefinido |
| Decisión de moderación | Operacional | 90 días (audit log) |
| API key de workspace | Autenticación | Hasta cancelación |
| Email y pago (Stripe) | Datos personales | Gestionado por Stripe |

## 3. Base legal

- **Interés legítimo** (GDPR Art. 6.1.f): Proteger a la audiencia de contenido dañino
- **Contractual**: Prestación del servicio de moderación contratado por el streamer
- **Obligación legal**: Reporte CSAM (NCMEC), compliance NIS2

## 4. Riesgos y mitigaciones

| Riesgo | Probabilidad | Impacto | Mitigación |
|---|---|---|---|
| Acceso no autorizado a contenido | Baja | Alto | PASETO v4, nsjail sandbox, Distroless |
| Fuga de datos en tránsito | Baja | Alto | TLS (Caddy), HTTPS en todas las conexiones |
| Decisión automatizada injusta | Media | Medio | Procedimiento de apelación (GDPR Art. 22) |
| Retención excesiva | Baja | Medio | R2 lifecycle 24h, Neon DB TTL 90 días |
| Transferencia internacional (WaveSpeed EE.UU.) | Media | Medio | SCCs + TIA requeridos (pendiente) |

## 5. Medidas técnicas

- Datos seudoanonimizados: hashes BLAKE3/pHash sin referencia a usuario final
- Cifrado en tránsito: TLS 1.3 vía Caddy
- Cifrado en reposo: R2 server-side encryption, Neon DB encryption at rest
- Sandboxing: FFmpeg/yt-dlp ejecutados en nsjail (sin red, sin disco persistente)
- Auditoría: audit_log append-only en Neon DB (90 días)

## 6. Conclusión

El procesamiento es necesario y proporcional para el servicio contratado. Los riesgos
residuales son aceptables con las mitigaciones implementadas. Revisar antes del
lanzamiento a producción y tras cualquier cambio significativo en el pipeline.
