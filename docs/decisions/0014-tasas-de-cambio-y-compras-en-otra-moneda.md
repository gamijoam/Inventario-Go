# ADR-0014: Tasas de cambio versionadas y compras en otra moneda

- **Estado:** Aceptada
- **Fecha:** 2026-10-27
- **Decisiones relacionadas:** DEC-013.P1, DEC-007.G (parcial), secciones 16.8, 25.1–25.3 y 25.7; BP-M3-002

## Contexto

El criterio de BP-M3-002 es "conversión exacta e historial de tasa". La
sección 25.7 pide registrar cada tasa con su dirección inequívoca, su
fuente, su vigencia, su versión y quién la registró. También pide guardar
en cada operación la tasa usada, de modo que una tasa nueva no recalcule
documentos ya confirmados. Cuando no hay tasa vigente, la operación que la
necesita se bloquea: no se inventa una tasa ni se usa una vencida en
silencio.

BP-M3-001 dejó las compras en la moneda de valoración de la empresa
(ADR-0013). Este ticket agrega la moneda original a órdenes, recepciones,
gastos y costos de reposición, y la conversión a la moneda de valoración.

## Parámetros ratificados por el usuario (2026-10-27)

| Tema | Decisión |
|---|---|
| Fuente | Registro **manual con fuente declarada**: oficial (p. ej. BCV), manual o proveedor, con etiqueta y referencia. No hay descarga automática |
| Aprobación | La tasa queda **vigente al registrarla**, con un permiso propio. Nunca se edita: una corrección es una versión nueva |
| Tasa de una compra | La **vigente a la fecha del documento** (la factura), fijada al confirmar. Un gasto en otra moneda usa la de su propia fecha. Sin tasa vigente, no se confirma |

## Decisión

1. **Dominio** (`internal/domain/exchange`): `Rate` significa `1 Base =
   Value Quote` desde `ValidOn`, una fecha comercial de Caracas. Cumple estas
   reglas:
   - tasa positiva, hasta 12 decimales, con un tope de 10¹²;
   - fuente con tipo y etiqueta obligatorios;
   - el **par** no tiene dirección: USD/VES y VES/USD compiten por la
     misma vigencia.

   `Latest` elige la tasa vigente así: la de fecha más reciente que no sea
   posterior al día consultado y, dentro de esa fecha, la de mayor versión.
   Una fecha futura deja la tasa programada. `Convert` multiplica de base a
   cotizada, divide de cotizada a base y redondea **una sola vez**
   (alejándose de cero) a la escala que pide quien llama.
2. **Vigencia por fecha comercial**, no por instante: el BCV publica una
   tasa por día y las facturas tienen fecha, no hora. La versión es un
   correlativo por empresa y par; si dos registros simultáneos toman el
   mismo número, uno se reintenta.
3. **Compras** (`purchase`):
   - las órdenes, recepciones, gastos y costos de reposición llevan su
     moneda;
   - la recepción lleva además la fecha de la factura, que no puede ser
     futura;
   - al confirmar, cada línea se valora como `cantidad × costo` convertido
     a la moneda de valoración y redondeado a 6 decimales;
   - la recepción guarda la tasa aplicada;
   - una recepción desde una orden hereda su moneda.
4. **Gastos:** un gasto en otra moneda se convierte **al registrarse**, con
   la tasa vigente a su fecha, y guarda el importe original, la tasa y el
   importe convertido. La fecha predeterminada es la de la factura si la
   recepción está en borrador, y la de hoy si el gasto es tardío. El
   prorrateo (ADR-0013) se hace sobre importes ya convertidos, y la base
   "por valor" es el costo convertido de cada línea.

   El gasto es un comprobante propio, con su fecha, así que su tasa no tiene
   por qué ser la de la factura.
5. **Historia intacta:** la tasa es de solo inserción y el documento guarda
   cuál aplicó. Una corrección posterior, aunque sea para la misma fecha,
   solo afecta a lo que se confirme después.
6. **Permisos:**

   | Permiso | Qué permite | Plantillas |
   |---|---|---|
   | `currency.rate.read` | Ver tasas | ADMIN_EMPRESA, ENCARGADO, ALMACENISTA, CAJERO |
   | `currency.rate.manage` | Registrar tasas, con **ámbito de empresa**, porque la tasa rige para todas sus sucursales | ADMIN_EMPRESA, ENCARGADO |

   ADMIN_ORG los recibe todos. Un encargado asignado a una sucursal no
   registra tasas; uno asignado a la empresa, sí.
7. **Base de datos** (migración 0009):
   - tabla `exchange_rates`, de solo inserción, con el par derivado y
     validado por un CHECK y la versión única por empresa y par;
   - columnas de moneda, fecha y tasa en los documentos de compra;
   - los disparadores de SQLite y PostgreSQL exigen lo mismo:
     - una recepción confirmada tiene todas sus líneas valoradas;
     - tiene una tasa si y solo si su moneda no es la de valoración;
     - esa tasa es de su empresa, cubre el par y ya regía a la fecha del
       documento;
     - un gasto nuevo cumple lo mismo con su propia fecha;
     - la moneda y la fecha no cambian.

   La tasa aplicada se valida con disparadores y no con clave foránea, para
   que la reversión pueda quitar la columna; las tasas no se borran.

   Los documentos anteriores toman la moneda de valoración de su empresa y
   la fecha de Caracas en que se crearon. Sus líneas y gastos quedan con el
   valor convertido en NULL, que se lee como el importe original. La
   reversión falla sin cambios si existe algún documento en otra moneda.
8. **Contratos JSON:** `purchase-receipt(s)` y `product-costs` pasan a `v2`,
   porque `currency` y `unit_cost` ahora están en la moneda del documento.
   Los importes en la moneda de valoración van en campos propios:
   `valuation_currency`, `valued_cost`, `valued_amount` y
   `valued_unit_cost`.

## Alternativas consideradas

- **Descargar la tasa del BCV.** El documento la desaconseja hasta validar
  fiabilidad, términos y mecanismo de acceso. Puede agregarse como otra
  fuente sin cambiar el modelo.
- **Aprobación por un segundo usuario.** Da más control, pero bloquea al
  comercio pequeño que actualiza la tasa a diario.
- **Tasa vigente al confirmar.** Es más simple, pero una factura del lunes
  confirmada el miércoles tomaría la tasa del miércoles.
- **Vigencia por instante UTC.** Obliga a elegir una hora para cada factura
  y no aporta nada mientras las tasas sean diarias.
- **Clave foránea compuesta hacia la tasa.** En SQLite exige reconstruir
  `purchase_receipts`, que el kardex referencia. Los disparadores verifican
  lo mismo y algo más: la empresa, el par y la vigencia.

## Consecuencias

- Las diferencias cambiarias no aparecen todavía: no hay cuentas por pagar
  ni pagos. Cuando lleguen, el pago llevará su propia tasa y la diferencia
  frente a la tasa de la factura se registrará aparte, sin tocar el
  inventario ni la compra.
- Quedan pendientes, por decisión del documento:
  - tolerancia de antigüedad de una tasa (hoy rige hasta que otra la
    reemplace);
  - tasas distintas para precio, cobro y reportes;
  - uso fiscal de cada fuente.
- BP-M3-003 usará el último costo y el costo de reposición, cada uno en su
  moneda, para sugerir precios en la moneda de cada lista.
