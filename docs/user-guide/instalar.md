# Instalar y probar

BalanzaPro se distribuye como un solo ejecutable, sin instalador. Con SQLite
no hace falta instalar ninguna base de datos: los datos quedan en un archivo
local.

> Versión de prueba, **no apta para producción**. Los datos de estos
> ejemplos y las tasas de cambio son ficticios.

## Descargar (Windows)

1. Abra la página de **Releases** del repositorio y elija la versión más
   reciente.
2. Descargue `balanzapro_<versión>_windows_amd64.zip` y, si quiere comprobar
   la descarga, `SHA256SUMS`. En PowerShell:

   ```powershell
   Get-FileHash .\balanzapro_<versión>_windows_amd64.zip   # compare con SHA256SUMS
   ```
3. Descomprima el `.zip`, por ejemplo en `C:\BalanzaPro`.
4. Abra **Windows Terminal** (o PowerShell) en esa carpeta y compruebe:

   ```powershell
   .\balanzapro.exe version
   .\balanzapro.exe doctor
   ```

El ejecutable no está firmado: la primera vez Windows SmartScreen puede
mostrar «Windows protegió su PC». Use «Más información → Ejecutar de todas
formas» solo si lo descargó del repositorio.

Los datos quedan en `%LOCALAPPDATA%\BalanzaPro\balanzapro.db` (ver
[Configuración](configuracion.md)). Para empezar de cero, cierre la sesión y
borre esa carpeta.

En Linux es igual con el `.tar.gz`: `tar xzf …` y `./balanzapro`.

## Probar de punta a punta

Los comandos van en PowerShell desde la carpeta del ejecutable. Agregue
`--help` a cualquiera para ver sus opciones y `--json` para la salida de
scripts.

### 1. Configurar el negocio

```powershell
.\balanzapro.exe setup --name "Bodega Ana" --rif J-12345678-9
```

Pide dos veces la contraseña del administrador `admin` y deja la sesión
iniciada. Vea [Primeros pasos](primeros-pasos.md).

### 2. Productos y existencia inicial

```powershell
.\balanzapro.exe product create --name "Harina de maíz 1 kg" --sku HARINA
.\balanzapro.exe product create --name "Queso blanco" --sku QUESO --unit KG
.\balanzapro.exe inventory in --product HARINA --qty 20 --unit-cost 1.10 --reason initial_load
.\balanzapro.exe inventory stock
```

### 3. Tasa y compra en bolívares

```powershell
.\balanzapro.exe rate set --base USD --quote VES --rate 45.725 --source official --label BCV
.\balanzapro.exe purchase supplier create --code DIST --name "Distribuidora, C.A."
.\balanzapro.exe purchase receipt create --supplier DIST --currency VES --document FAC-1
.\balanzapro.exe purchase receipt line RC-000001 --product HARINA --qty 30 --unit-cost 57
.\balanzapro.exe purchase receipt expense RC-000001 --concept Flete --amount 2 --currency USD
.\balanzapro.exe purchase receipt confirm RC-000001
.\balanzapro.exe inventory kardex --product HARINA
```

La compra se convierte a USD con la tasa del día, el flete se reparte y el
costo promedio cambia. El kardex muestra cada movimiento con su valor.

### 4. Precios con aprobación

```powershell
.\balanzapro.exe price-list create --code general --name General --margin 25
.\balanzapro.exe price propose --list general --product HARINA --price 1.50
.\balanzapro.exe price approve PP-000001
.\balanzapro.exe purchase receipt create --supplier DIST
.\balanzapro.exe purchase receipt line RC-000002 --product HARINA --qty 10 --unit-cost 1.40
.\balanzapro.exe purchase receipt confirm RC-000002
.\balanzapro.exe price show
```

La segunda compra no cambia el precio: deja una **propuesta** (PP-000002)
que puede aprobar, ajustar, programar o rechazar. Vea
[Listas y precios](precios.md).

### 5. Usuarios con permisos distintos

```powershell
.\balanzapro.exe user create --username maria --name "María"
.\balanzapro.exe user assign --username maria --role ALMACENISTA --company principal --branch principal
.\balanzapro.exe auth logout
.\balanzapro.exe auth login --user maria
.\balanzapro.exe price approve PP-000002     # denegado: el almacenista solo propone
```

Para volver como administrador: `.\balanzapro.exe auth login --user admin`.
Vea [Usuarios y permisos](usuarios-y-permisos.md).

### 6. Pantalla interactiva

Sin argumentos, `.\balanzapro.exe` abre la TUI: inicie sesión (o entra
directo si ya lo hizo con `auth login`), y consulte productos, existencias y
kardex, precios, y apruebe o rechace la propuesta PP-000002. Vea
[Pantalla interactiva](tui.md).
