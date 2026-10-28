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

## Probar con la pantalla interactiva

Haga doble clic en `balanzapro.exe` o, en Windows Terminal, ejecútelo sin
argumentos:

```powershell
.\balanzapro.exe
```

1. **Configuración inicial.** La primera vez se abre el asistente: escriba
   el nombre del negocio (p. ej. `Bodega Ana`), el RIF si quiere, elija la
   moneda con ←/→ y ponga dos veces la contraseña del administrador. Con
   **Enter** en el último campo queda todo creado y con la sesión iniciada.
2. **Productos.** Menú *Inventario → Productos*, **F2**: cree
   `Harina de maíz 1 kg` con SKU `HARINA`; otra vez **F2** para
   `Queso blanco` con SKU `QUESO` y unidad `KG` (←/→ en el campo Unidad).
   **F3** edita y **F4** inactiva.
3. **Unidades.** *Inventario → Unidades de medida*, **F2**: cree `SACO`
   con 0 decimales.
4. **Existencia inicial.** *Movimientos → Entrada de mercancía*: producto
   `HARINA` (o **F5** para elegirlo), cantidad `20`, costo `1.10`, motivo
   *Carga inicial*. Pruebe también una salida por merma, una reserva para
   el pedido `PED-1`, un bloqueo por daño y un conteo físico con su
   aprobación (este último por otro usuario: ver el paso 5 de abajo).

5. **Compra en bolívares.** *Compras → Proveedores*, **F2**: `DIST`,
   `Distribuidora, C.A.`. Luego *Compras → Recepciones*, **F2**: proveedor
   `DIST`, moneda `VES`, factura `FAC-1`. En el documento: **F2** agrega
   `HARINA`, 30, costo 57; como falta la tasa del día, **F6** la registra
   (45.725, oficial BCV); **F3** agrega el flete de 2 USD; **F9** confirma.
   El kardex de HARINA muestra la compra y el nuevo costo promedio.

Vea [Pantalla interactiva](tui.md) para todas las teclas. Las listas de
precios se crean por ahora con los comandos de abajo (paso 4); luego las
propuestas se aprueban en la TUI.

## Probar de punta a punta

Los comandos van en PowerShell desde la carpeta del ejecutable. Agregue
`--help` a cualquiera para ver sus opciones y `--json` para la salida de
scripts.

### 1. Configurar el negocio

Si no usó el asistente de la TUI:

```powershell
.\balanzapro.exe setup --name "Bodega Ana" --rif J-12345678-9
```

Pide dos veces la contraseña del administrador `admin` y deja la sesión
iniciada. Vea [Primeros pasos](primeros-pasos.md).

### 2. Productos y existencia inicial

Si no creó los productos en la TUI:

```powershell
.\balanzapro.exe product create --name "Harina de maíz 1 kg" --sku HARINA
.\balanzapro.exe product create --name "Queso blanco" --sku QUESO --unit KG
```

La existencia inicial, si no la cargó en la TUI:

```powershell
.\balanzapro.exe inventory in --product HARINA --qty 20 --unit-cost 1.10 --reason initial_load
.\balanzapro.exe inventory stock
```

### 3. Tasa y compra en bolívares

Si no la hizo en la TUI:

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

### 6. De vuelta en la pantalla interactiva

Abra `.\balanzapro.exe` (entra directo si la sesión sigue iniciada) y
consulte *Inventario → Existencias y kardex*, *Precios → Precios vigentes*,
y apruebe o rechace la propuesta PP-000002 en *Precios → Propuestas de
precio* (**A** aprueba, **X** rechaza). Vea [Pantalla interactiva](tui.md).
