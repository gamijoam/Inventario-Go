# Pantalla interactiva (TUI)

Sin argumentos, `balanzapro` (en Windows, `.\balanzapro.exe`) abre la
pantalla interactiva en la terminal. Se maneja con el teclado y cada
pantalla ocupa la terminal completa: **Esc** vuelve a la anterior y **q**
sale. En Windows conviene usar **Windows Terminal**.

Las reglas y los permisos son los mismos que en los comandos: la TUI solo
muestra lo que su usuario puede usar.

## Iniciar sesión

Si ya inició sesión en este equipo (con la TUI o con `balanzapro auth
login`), entra directo al menú. Si no, pide usuario y contraseña. La
contraseña no se muestra. **Tab** cambia de campo y **Enter** entra.

La sesión es la misma para la TUI y los comandos: cerrar sesión en uno la
cierra en el otro.

Si la instalación todavía no está configurada, la TUI lo dice y solo ofrece
diagnóstico y versión. Ciérrela y ejecute `balanzapro setup` (ver
[Primeros pasos](primeros-pasos.md)).

## Menú

La cabecera muestra su usuario y la empresa y sucursal de esta instalación.
Elija con **↑/↓** y **Enter**, o con el número de la opción.

| Opción | Qué muestra | Permiso |
|---|---|---|
| Productos | Catálogo. **Enter** abre las existencias del producto; **p**, sus precios | ver catálogo |
| Existencias | Física, reservada, bloqueada y disponible por almacén, con costo promedio y valor. **Enter** abre el kardex | ver existencias |
| Precios | Precio vigente por lista, el programado y la propuesta pendiente. **Enter** abre la propuesta | ver precios |
| Propuestas de precio | Pendientes (**t** muestra todas). **Enter** abre una | ver precios |
| Diagnóstico | Las verificaciones de `balanzapro doctor`; **r** repite | — |
| Versión | Versión, commit y fecha de compilación | — |
| Cerrar sesión | Cierra la sesión de este equipo | — |

## Listas

En cualquier lista:

- **↑/↓**, **Re Pág/Av Pág** e **Inicio/Fin** mueven la selección.
- **/** abre el filtro: escriba y la lista deja las filas que contienen el
  texto. **Enter** fija el filtro y **Esc** lo borra.
- **r** recarga.

## Aprobar o rechazar un precio

En una propuesta pendiente, con permiso para aprobar:

- **a** abre la confirmación de la aprobación:
  - **Precio**: el propuesto, que puede ajustar;
  - **Vigencia**: vacía rige ya; `AAAA-MM-DD HH:MM` (hora de Caracas) la
    programa.

  **Enter** confirma y **Esc** cancela.
- **x** abre el rechazo: escriba el motivo y confirme con **Enter**.

Si el precio cambió después de la propuesta, o el nuevo queda por debajo
del costo sin el permiso extraordinario, la confirmación muestra el motivo y
no cambia nada. Vea [Listas y precios](precios.md).

## Qué viene después

Por ahora la TUI consulta y aprueba. Registrar movimientos, compras, tasas y
listas se hace todavía con los comandos (ver las demás guías); cada módulo
irá sumando sus pantallas, empezando por la caja de M4.
