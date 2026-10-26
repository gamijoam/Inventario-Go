# Primeros pasos

## 1. Configurar la instalación

En una PC con SQLite (sin archivo de configuración se usan las rutas por
defecto):

```sh
balanzapro setup --name "Bodega Ana" --legal-name "Bodega Ana, C.A." --rif J-12345678-9
```

`setup` pide dos veces la contraseña del administrador (sin mostrarla; en
scripts use `--password-stdin`). Crea la base, aplica las migraciones y
registra en una sola operación:

| Nivel | Código por defecto | Para qué sirve |
|---|---|---|
| Organización | derivado del nombre (`BODEGA-ANA`) | Su negocio en BalanzaPro |
| Empresa legal | `PRINCIPAL` | Razón social y RIF que emiten documentos |
| Sucursal | `PRINCIPAL` | Lugar donde se vende; esta instalación la atiende |
| Almacén | `PRINCIPAL` | Donde está la mercancía de esa sucursal |
| Usuario administrador | `admin` (`--admin-user`) | Administra toda la organización; queda con la sesión iniciada |

Todo se puede renombrar con flags (`--code`, `--company-code`,
`--branch-name`, `--warehouse-name`…; ver `balanzapro setup --help`).
`setup` solo se ejecuta una vez; la segunda vez responde con el código 6.

El RIF es opcional. Se valida el formato (`J-12345678-9`, también acepta
`j123456789`); el dígito verificador **no** se comprueba todavía.

## 2. Crecer: más empresas, sucursales o almacenes

```sh
balanzapro company create --code norte --legal-name "Bodega Ana Norte, C.A." --rif J-87654321-0
balanzapro branch create --company norte --code maracay --name "Maracay"
balanzapro warehouse create --code danados --name "Mercancía dañada"   # en la sucursal de esta instalación
balanzapro warehouse create --company norte --branch maracay --code principal --name "Principal"
```

Los códigos se escriben en minúsculas o mayúsculas indistintamente y deben
ser únicos dentro de su nivel: puede haber una sucursal `PRINCIPAL` en cada
empresa.

## 3. Consultar

```sh
balanzapro org list         # organización y sucursal de esta instalación
balanzapro company list
balanzapro branch list [--company CÓDIGO]
balanzapro warehouse list [--company CÓDIGO --branch CÓDIGO]
```

Todos admiten `--json`. Las fechas se muestran en hora de Venezuela.

## Qué no se puede hacer todavía

- Desactivar o renombrar empresas, sucursales o almacenes.
- Mover una sucursal a otra empresa legal (requerirá un procedimiento
  propio por sus implicaciones fiscales).

Para crear usuarios y darles permisos, vea
[Usuarios y permisos](usuarios-y-permisos.md).
