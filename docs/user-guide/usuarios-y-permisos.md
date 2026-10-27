# Usuarios y permisos

## Sesión

Todos los comandos de negocio exigen una sesión iniciada en el equipo:

```sh
balanzapro auth login --user admin     # pide la contraseña sin mostrarla
balanzapro auth whoami                 # quién soy y qué roles tengo
balanzapro auth passwd                 # cambiar mi contraseña
balanzapro auth logout
```

- La sesión vence tras **1 hora sin uso** o **12 horas** desde que se inició.
- Tras **5 intentos fallidos** la cuenta se bloquea **15 minutos**; si se
  vuelve a bloquear sin un acceso correcto entre medio, el bloqueo dura el
  doble (30 min, 1 h…, hasta 24 h). Un administrador puede desbloquearla.
- El mensaje de error es el mismo si el usuario no existe, la contraseña es
  incorrecta o la cuenta está bloqueada: así no se puede averiguar qué
  usuarios existen.
- Nunca escriba la contraseña como argumento. En scripts, use
  `--password-stdin` (una línea por contraseña).

Contraseñas: de 8 a 128 caracteres, sin reglas de mayúsculas o símbolos;
se rechazan las muy comunes (`12345678`, `password1`, `venezuela1`…), las
de un solo carácter repetido y las que contienen el usuario. Una frase como
`cafe con leche` es válida y fácil de recordar.

## Roles y ámbitos

Un usuario recibe **roles en un ámbito**: toda la organización, una empresa,
una sucursal o un almacén. Un permiso vale en ese ámbito y en todo lo que
contiene; nunca fuera de él.

```sh
balanzapro user create --username luis --name "Luis Pérez"     # pide la contraseña inicial
balanzapro user assign --username luis --role ENCARGADO --company PRINCIPAL --branch PRINCIPAL
balanzapro user list
```

Plantillas sembradas en cada organización (se pueden editar):

| Rol | Permisos iniciales |
|---|---|
| `ADMIN_ORG` | Todo, salvo los permisos extraordinarios |
| `ADMIN_EMPRESA` | Ver estructura y catálogo; crear sucursales y almacenes; ver y gestionar usuarios; ver y ajustar existencias |
| `ENCARGADO` | Ver estructura y catálogo; crear almacenes; ver usuarios; ver y ajustar existencias |
| `ALMACENISTA` | Ver estructura y catálogo; ver y ajustar existencias |
| `CAJERO`, `OPERADOR_TECNICO` | Ver estructura, catálogo y existencias (los permisos de venta llegan en M4) |

```sh
balanzapro role list
balanzapro role permissions                                   # catálogo completo
balanzapro role create --code SUPERVISOR --name Supervisor --permission org.structure.read,identity.user.read
balanzapro role edit --code CAJERO --add org.warehouse.create
```

Los permisos **extraordinarios** (aprobar ventas bajo costo, restaurar
respaldos) no vienen en ninguna plantilla: un administrador de la
organización debe agregarlos expresamente a un rol.

## Reglas de seguridad

- Solo puede asignar roles cuyos permisos usted tenga en ese ámbito.
- No puede cambiar sus propias asignaciones ni desactivarse.
- Para gestionar a un usuario (desactivar, desbloquear, restablecer su
  contraseña) necesita `identity.user.manage` en el ámbito de **todos** sus
  roles.
- Siempre debe quedar al menos un administrador activo de toda la
  organización.
- Retirar un rol o desactivar a un usuario surte efecto en su siguiente
  comando; desactivar y restablecer la contraseña cierran sus sesiones.

```sh
balanzapro user disable --username luis
balanzapro user enable --username luis
balanzapro user unlock --username luis
balanzapro user reset-password --username luis
balanzapro user unassign --username luis --role ENCARGADO --company PRINCIPAL --branch PRINCIPAL
```

Códigos de salida: **8** permiso denegado, **9** sin sesión o credenciales
incorrectas.
