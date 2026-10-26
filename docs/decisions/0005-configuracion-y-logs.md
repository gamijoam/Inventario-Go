# ADR-0005: Configuración por archivo y entorno; logs con redacción de secretos

- **Estado:** Aceptada
- **Fecha:** 2026-10-26
- **Decisiones relacionadas:** DEC-009, secciones 28.3 y 28.5 (paso 4), BP-M0-003

## Contexto

La CLI debe configurarse sin secretos en el control de versiones, en flags ni
en logs, y sus diagnósticos no pueden revelar credenciales.

## Decisión

1. **Formato:** TOML (`github.com/BurntSushi/toml` v1.6.0): admite
   comentarios y es legible para soporte. Decodificación estricta: las claves
   desconocidas son error.
2. **Prioridad:** defecto < archivo < `BALANZAPRO_*` < `--log-level`. El
   archivo se busca en `--config`, `BALANZAPRO_CONFIG` o la ruta de la
   plataforma (XDG en Linux, `%APPDATA%` en Windows).
3. **Secretos:** tipo `config.Secret`, que se redacta en `String`, `%#v`,
   JSON, texto y `slog`; solo `Reveal()` entrega el valor. No hay flags para
   secretos.
4. **Errores sin secretos:** los errores de TOML informan línea, columna y
   clave, nunca el contenido; la URL de PostgreSQL se valida sin propagar
   errores de `net/url`, que incluyen la cadena completa.
5. **Presentación:** `config show` y `doctor` muestran la URL con
   `url.Redacted()` y parámetros sensibles (`password`, `sslpassword`,
   `passfile`, `sslkey`) ocultos; se conserva usuario, host, base y opciones.
6. **Permisos:** en sistemas POSIX se advierte si un archivo con
   credenciales es legible por otros usuarios. Los archivos de log se crean
   con permisos 0600.
7. **Logs:** `log/slog`, texto o JSON, a stderr o archivo; nunca a stdout. Un
   `ReplaceAttr` redacta claves sensibles (por subcadena: `password`,
   `secret`, `token`…; por palabra completa: `pin`, `pass`, `pwd`, `dsn`,
   `otp`), valores `Secret`, errores y textos con URLs con credenciales; el
   mensaje también se redacta.
8. **Código de salida 4** para configuración inválida.

## Consecuencias

- La redacción de logs es una red de seguridad, no un permiso para registrar
  secretos; las revisiones deben seguir evitándolo.
- El almacenamiento de credenciales con el llavero del sistema o un archivo
  de secretos separado queda como mejora futura (M5/M6).
- En Windows no se evalúan permisos del archivo; la ubicación por defecto
  está en el perfil del usuario.
