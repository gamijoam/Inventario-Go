# ADR-0017: TUI de pantallas completas sobre un puerto Backend

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-005.F, sección 27 (V1 CLI/TUI), sección 28.6; ADR-0004 (CLI y TUI)

## Contexto

V1 se opera desde CLI/TUI en Windows y Linux. Hasta ahora la TUI solo traía
diagnóstico y versión. El usuario quiere usar el sistema desde la TUI y
pidió avanzar así: primero el backend de cada módulo y después sus
pantallas.

El documento pide de la TUI:

- navegación completa por teclado;
- formularios y confirmación en las acciones irreversibles;
- cabecera con el ámbito activo;
- contraseña oculta;
- terminal pequeña y colores opcionales;
- estados explícitos.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Primer paso | Base de la TUI y pantallas de uso diario: productos, existencias y kardex, precios, y propuestas con aprobar o rechazar |
| Diseño | **Pantallas a pantalla completa**; Esc vuelve |
| Módulos nuevos | Por cada ticket, primero el PR del backend y, al fusionarlo, otro con su pantalla |
| Pruebas en Windows | Un release en el repositorio por cada PR de TUI (ADR-0016) |

## Decisión

1. **Puerto `tui.Backend`.** La TUI depende de una interfaz con los casos de
   uso que muestra, no de la CLI ni de la persistencia. La CLI la implementa
   (`tuiBackend`) sobre la base abierta y **el mismo archivo de sesión** que
   usan los comandos: iniciar o cerrar sesión en uno vale para el otro.

   Cada llamada lleva el límite de tiempo de los comandos. La TUI solo
   oculta opciones según los permisos de la sesión; quien autoriza es
   siempre la capa application.
2. **Pila de pantallas.** El modelo raíz guarda una pila de pantallas, que
   son punteros con `Init`, `Update`, `View`, `Title`, `Help` y
   `Capturing`.
   - Enter abre una pantalla y Esc la cierra. Al volver, la anterior se
     recarga si sabe hacerlo.
   - Mientras el usuario escribe (`Capturing`), q y Esc llegan al campo y
     no son atajos.
   - Los resultados asíncronos llevan el identificador de la pantalla que
     los pidió. Si esa pantalla ya se cerró, se descartan.
3. **Componentes Bubbles v2**, fijados en `v2.1.1`, compatibles con
   bubbletea v2.1.0 y lipgloss v2.0.6, sin actualizarlos:
   - `table`, envuelta en una lista con filtro `/`; las columnas se ajustan
     al contenido y a la terminal;
   - `textinput`, con el cursor fijo, sin parpadeo ni temporizadores.

   La fila elegida se marca con inversión, así que se distingue sin color.
4. **Pantallas de este ticket:**
   - inicio de sesión;
   - menú según permisos;
   - productos, existencias, kardex y precios;
   - propuestas: lista, detalle, aprobar con precio y vigencia, y rechazar
     con motivo, ambos con un paso de confirmación;
   - diagnóstico y versión.

   Sin base configurada, solo diagnóstico y versión, con la explicación.
5. **Pruebas.**
   - El modelo se prueba sin terminal, con un backend falso: pulsaciones,
     filtro, permisos, aprobar, rechazar y cerrar sesión.
   - La CLI prueba su `tuiBackend` contra SQLite real: sesión compartida,
     permisos y aprobación.
   - Además se probó el ejecutable en una pseudoterminal.

## Alternativas consideradas

- **Menú lateral fijo.** Da más contexto, pero el usuario prefirió pantallas
  completas, que aprovechan mejor las terminales pequeñas.
- **TUI llamando a la CLI.** Habría acoplado la TUI al texto de los
  comandos; el puerto la deja lista para otros frontends.
- **Componentes propios en lugar de Bubbles.** Habría más código que
  mantener para tablas y campos de texto; el documento ya proponía Bubbles
  v2.

## Consecuencias

- Cada ticket de M4 en adelante suma sus métodos al `Backend` y sus
  pantallas en el PR siguiente al del backend.
- Quedan pendientes para próximos tickets de TUI:
  - registrar movimientos, compras, tasas y listas desde la TUI;
  - administración de usuarios;
  - paginación del kardex (hoy los primeros 1000 movimientos).
