# ADR-0021: Precios y administración en la TUI

- **Estado:** Aceptada
- **Fecha:** 2026-10-28
- **Decisiones relacionadas:** ADR-0015 (listas y propuestas), ADR-0007 y ADR-0008 (estructura, identidad y permisos), ADR-0018 a ADR-0020 (TUI)

## Contexto

Tras TUI-4 quedaban en la CLI las listas de precios, los márgenes, las
propuestas manuales y la administración (usuarios, roles, estructura y
contraseñas). Con TUI-5 todo lo de M1–M3 se hace desde la TUI antes de M4.

## Parámetros ratificados por el usuario (2026-10-28)

| Tema | Decisión |
|---|---|
| Menús | Precios suma *Listas de precios* y *Proponer precio*; menú nuevo **Administración** (Usuarios, Roles, Estructura); *Cambiar mi contraseña* en Sistema |
| Roles | **Lista de casillas** con todos los permisos y su descripción, agrupados por módulo; Espacio marca y F10 guarda. Los roles de fábrica se ven pero no se editan |
| Ámbito | **Selección en cascada**: organización, empresa, sucursal o almacén, cada nivel elegido de los existentes |
| Contraseñas | **Ambas**: la propia (pide la actual) y, con permiso, restablecer y desbloquear la de otro |

## Decisión

1. **Roles de fábrica de solo lectura en la TUI.** Los sembrados por
   `identity.Templates()` se abren sin poder editarse; **F7** los copia
   como rol propio. El servicio permite editarlos (la CLI lo hace); la TUI
   lo evita para que nadie le quite permisos al ADMIN_ORG por accidente
   desde un menú.

2. **Editor de casillas.** Muestra el catálogo completo en orden estable,
   con el grupo de cada módulo y los permisos extraordinarios al final. Al
   guardar un rol existente envía solo la diferencia (agregar y quitar),
   que el servicio aplica sobre el rol vigente.

3. **Ámbito en cascada.** El formulario carga toda la estructura visible
   al abrirse y recalcula las opciones de cada nivel al cambiar el de
   arriba, sin consultar de nuevo. Los niveles que no aplican muestran
   «—» y se envían vacíos. El formulario genérico admite un aviso de cambio
   de selección para esto.

4. **Contraseña propia.** El backend vuelve a autenticar el token de la
   sesión y llama al cambio de contraseña del servicio, que verifica la
   actual con las reglas de bloqueo y revoca las demás sesiones; la de la
   TUI (y los comandos de este equipo) sigue abierta.

5. **Estructura como árbol** en una lista: empresas, sucursales sangradas y
   almacenes debajo; F2, F3 y F4 crean en el contexto de la fila elegida.

6. **Precios vigentes**: F2 propone para el producto elegido y F4 anula un
   precio programado (exige el permiso de aprobar, como el servicio).

## Consecuencias

- Todo M1–M3 se opera sin comandos; la CLI queda para scripts.
- A partir de M4, cada ticket entrega su backend y enseguida su pantalla.
