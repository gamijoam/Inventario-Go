# Registro de decisiones técnicas (ADR)

Cada ADR documenta una decisión técnica, su contexto y sus consecuencias. Las
decisiones de negocio (DEC-xxx) viven en el documento maestro; los ADR
registran cómo se implementan.

| ADR | Título | Estado |
|---|---|---|
| [0001](0001-repositorio-y-modulo.md) | Repositorio, módulo Go, licencia y estructura | Aceptada |
| [0002](0002-decimal-exacto-y-redondeo.md) | Decimal exacto y política provisional de redondeo | Aceptada (parámetros provisionales) |
| [0003](0003-identificadores-y-ambito.md) | Identificadores globales UUIDv7 y ámbito de negocio | Aceptada |
| [0004](0004-cli-tui.md) | CLI con Cobra y TUI con Bubble Tea v2 | Aceptada |
| [0005](0005-configuracion-y-logs.md) | Configuración por archivo y entorno; logs con redacción de secretos | Aceptada |
| [0006](0006-persistencia-sqlite-postgres.md) | Drivers, conexión y migraciones para SQLite y PostgreSQL | Aceptada |

Estados posibles: *Propuesta*, *Aceptada*, *Reemplazada por ADR-xxxx*, *Rechazada*.
