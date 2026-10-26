# Configuración de balanzapro

## Prioridad

De menor a mayor: **valores por defecto < archivo TOML < variables de entorno**.
El flag `--log-level` prevalece sobre todo para el nivel de log.

Los secretos (la URL de PostgreSQL con contraseña) se aceptan en el archivo o
en el entorno, **nunca como flags**, para que no queden en el historial de la
terminal ni en la lista de procesos.

## Ubicación del archivo

1. `--config <ruta>`
2. variable `BALANZAPRO_CONFIG`
3. ruta por defecto:
   - Linux: `$XDG_CONFIG_HOME/balanzapro/config.toml` (o `~/.config/balanzapro/config.toml`)
   - Windows: `%APPDATA%\BalanzaPro\config.toml`

Si el archivo por defecto no existe se usan los valores por defecto. Si se
indicó una ruta con `--config` o `BALANZAPRO_CONFIG` y no existe, es un error.

```sh
balanzapro config path         # ruta en uso
balanzapro config show         # valores efectivos, origen y advertencias
balanzapro config show --json  # lo mismo para scripts (balanzapro.config/v1)
balanzapro doctor              # incluye la verificación "config"
```

## Claves

| Clave | Variable de entorno | Por defecto | Valores |
|---|---|---|---|
| `database.driver` | `BALANZAPRO_DB_DRIVER` | `sqlite` | `sqlite`, `postgres` |
| `database.sqlite.path` | `BALANZAPRO_SQLITE_PATH` | Linux: `~/.local/share/balanzapro/balanzapro.db`; Windows: `%LOCALAPPDATA%\BalanzaPro\balanzapro.db` | ruta local (no en carpeta compartida) |
| `database.postgres.url` | `BALANZAPRO_POSTGRES_URL` | — | `postgres://usuario:clave@host:5432/base?sslmode=...` (**secreto**) |
| `log.level` | `BALANZAPRO_LOG_LEVEL` | `warn` | `debug`, `info`, `warn`, `error` |
| `log.format` | `BALANZAPRO_LOG_FORMAT` | `text` | `text`, `json` |
| `log.file` | `BALANZAPRO_LOG_FILE` | vacío (stderr) | ruta; se crea con permisos 0600 |
| `session.file` | `BALANZAPRO_SESSION_FILE` | Linux: `~/.local/state/balanzapro/session` (o `$XDG_STATE_HOME`); Windows: `%LOCALAPPDATA%\BalanzaPro\session` | token de la sesión iniciada; se crea con permisos 0600 |

Las claves desconocidas son un error, para detectar errores de escritura.

## Ejemplo

```toml
[database]
driver = "postgres"

[database.sqlite]
path = "/var/lib/balanzapro/balanzapro.db"

[database.postgres]
url = "postgres://balanzapro:CAMBIAR@127.0.0.1:5432/balanzapro?sslmode=disable"

[log]
level = "info"
format = "json"
file = "/var/log/balanzapro/balanzapro.log"
```

Si el archivo contiene la URL de PostgreSQL, protéjalo: `chmod 600 config.toml`.
`doctor` y `config show` advierten si otros usuarios pueden leerlo. Nunca lo
agregue al repositorio.

## Logs

- Van a stderr o al archivo configurado; **nunca a stdout**, así la salida
  `--json` sigue siendo un único documento válido.
- Durante la TUI, si no hay archivo configurado, se descartan para no
  ensuciar la pantalla.
- Se redactan automáticamente las claves con nombres sensibles (`password`,
  `pin`, `token`, `secret`, `dsn`, `authorization`…), los valores de tipo
  secreto y la contraseña de cualquier URL con credenciales.

## Base de datos

```sh
balanzapro db status    # motor, conexión, instalación y migraciones (no modifica nada)
balanzapro db migrate   # crea la base SQLite si no existe y aplica migraciones
```

En SQLite, si la base ya tenía migraciones y hay nuevas, `db migrate` crea
antes un respaldo `bp.db.pre-NNNN-AAAAMMDDTHHMMSSZ.bak` junto al archivo. En
PostgreSQL, respalde antes con `pg_dump`.

## Códigos de salida relacionados

- `4`: configuración inválida (archivo ilegible, TOML mal formado, clave
  desconocida o valor fuera de rango). Los mensajes indican línea y clave,
  nunca el contenido del secreto.
- `5`: no se pudo conectar con la base de datos (`db status`, `db migrate`).
- `doctor` reporta la configuración inválida como `[FAIL] config` y la base
  inaccesible como `[FAIL] database` (código 3); una base sin inicializar o
  con migraciones pendientes es `[WARN] database`.
