# ADR-0007: Jerarquía organizacional y aislamiento por ámbito

- **Estado:** Aceptada
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-008.A (aprobada), DEC-003, secciones 22.3–22.8, 23.1 y 25.5; BP-M1-001

## Contexto

DEC-008.A aprobó organización (tenant) → empresas legales → sucursales →
almacenes. Ningún dato puede cruzar organizaciones ni empresas aunque un ID
sea válido, y una instalación de una PC debe quedar lista sin pedir cuatro
identificadores en cada comando.

## Decisión

1. **Dominio** (`internal/domain/org`): `Organization`, `Company`, `Branch`,
   `Warehouse`. Los constructores reciben la entidad padre, no IDs sueltos,
   y cada entidad expone su `BusinessScope`.
2. **Códigos legibles** (`PRINCIPAL`, `ALM-01`): 1–32 caracteres `A-Z0-9_-`,
   normalizados a mayúsculas. Únicos dentro del padre: organización (global),
   empresa por organización, sucursal por empresa, almacén por sucursal. El
   usuario trabaja con códigos; los UUIDv7 son internos.
3. **RIF**: solo formato `L-NNNNNNNN-D` (V, E, J, P, G, C), opcional y único
   por organización (índice único parcial). El dígito verificador y su uso
   fiscal quedan pendientes de validación profesional.
4. **Aislamiento en tres capas** (sección 22.5):
   - Go: cada caso de uso recibe el ámbito del actor; las búsquedas se hacen
     siempre dentro de su organización y lo que el actor no ve responde
     `no encontrado`, sin revelar si existe. Un actor de empresa no crea
     empresas; uno de sucursal no crea sucursales.
   - SQL: claves foráneas **compuestas** `(organization_id, company_id[, branch_id])`
     con `UNIQUE` candidatas en el padre y `ON DELETE RESTRICT`, en ambos motores.
   - RLS de PostgreSQL en la nube: se diseñará con la API (M5); hoy no aplica
     porque la base local solo la usa el proceso de la sucursal.
5. **Instalación ↔ sucursal** (`installation_binding`): `balanzapro setup`
   aplica migraciones y crea organización, empresa, sucursal y almacén en una
   sola transacción, asociados a la instalación. Solo puede ejecutarse una vez.
6. **Actor de la CLI local**: hasta BP-M1-002 (usuarios y permisos), la CLI
   actúa con el ámbito de la organización asociada a la instalación. Nunca
   acepta IDs de organización como argumento.
7. **CLI**: `setup`, `org list`, `company list|create`, `branch list|create`,
   `warehouse list|create`, con `--json`. Nuevos códigos de salida: `6`
   conflicto (código o RIF repetido, setup repetido) y `7` no encontrado o
   instalación sin configurar.

## Consecuencias

- Pendientes: desactivar o renombrar entidades, cambio de contexto
  (`branch use`) y mostrar la sucursal activa en la TUI.
- DEC-008.B (catálogo compartido entre empresas) y DEC-008.C (transferencias
  entre empresas) siguen abiertas; no hay operaciones entre empresas.
- Mover una sucursal a otra empresa no está permitido (no se edita la FK);
  requerirá un procedimiento explícito.
