# Mayorea · Backend

API en Go (PostgreSQL) para el MVP de Mayorea. Las reglas de negocio son el port de `src/domain` del frontend.

## Arquitectura

Clean Architecture; las dependencias apuntan siempre hacia adentro.

```
cmd/api                  composición: elige adaptadores y cablea casos de uso
internal/domain          entidades y reglas puras (crédito, FEFO, cobro multimoneda). Sin I/O.
internal/usecase         servicios de aplicación + puertos (interfaces de repositorio, Clock, TxManager)
internal/adapter/httpapi entrega HTTP/JSON (net/http, sin framework)
internal/adapter/postgres  repositorios sobre pgx
internal/adapter/memory  repositorios en memoria (tests y desarrollo del frontend)
internal/platform        config y reloj
migrations               SQL embebido, se aplica al arrancar
```

- **S**: cada servicio tiene un solo motivo de cambio (Catalog, Orders, Dispatch, Collections).
- **O/L**: Postgres y memoria son intercambiables tras los mismos puertos.
- **I**: un repositorio por agregado, interfaces pequeñas.
- **D**: `usecase` define los puertos; los adaptadores los implementan.

## Reglas que hace cumplir

- Montos en centavos enteros, tasa BCV en diezmilésimas. Nunca `float`.
- Pedido reserva stock (`committed`) con las filas bloqueadas: no se sobrevende.
- Cliente con más de 7 días de mora: solo contado.
- Despacho: FEFO por lote, factura con la tasa del día congelada, todo o nada.
- Cobro mixto (USD efectivo + Bs a la tasa del día) cierra al centavo y registra el diferencial cambiario.

## Correr

```bash
cp .env.example .env
make up            # Postgres local (docker)
make tidy          # primera vez: baja dependencias y crea go.sum
make run           # o: make run-mem  (sin base de datos)
make test
```

## Autenticación y roles

Login con correo y contraseña (bcrypt) → token opaco que se manda como `Authorization: Bearer <token>`. El servidor guarda solo el hash SHA-256 del token, así que se puede revocar (logout, cambio de clave, usuario desactivado) y una base filtrada no sirve para suplantar sesiones. Rol y estado del usuario se leen en cada petición: un cambio de rol aplica de inmediato. Tras 5 intentos fallidos el correo se bloquea 15 minutos.

| Rol | Puede |
|---|---|
| `admin` | todo, incluida la gestión de usuarios |
| `credito` | aprobar pedidos, cobrar, tasa BCV, clientes, aprobar liquidaciones |
| `galpon` | productos, lotes, camiones, picking, despacho y salida de camiones |
| `preventista` | ver catálogo y tomar pedidos (quedan a su nombre) |
| `chofer` | sacar y operar **su** camión: salida, entregar, vender, tomar pedidos, cobrar y cerrar ruta |

Los permisos viven en `internal/domain/auth.go`; cada ruta declara el suyo en `httpapi/router.go`. El primer administrador se crea con `BOOTSTRAP_OWNER_EMAIL` / `BOOTSTRAP_OWNER_PASSWORD` mientras no haya usuarios. No se puede dejar el sistema sin un dueño activo.

## API (`/api/v1`)

Todo requiere sesión salvo `GET /health` y `POST /auth/login`.

| Método | Ruta | |
|---|---|---|
| POST | `/auth/login` `{email,password}` · `/auth/logout` · `/auth/password` `{current,new}` | sesión |
| GET | `/auth/me` | usuario, rol y permisos (para ocultar pantallas en el frontend) |
| GET/POST/PATCH | `/users` · `/users/{id}` · POST `/users/{id}/password` | solo `admin` |

| Método | Ruta | |
|---|---|---|
| GET/PUT | `/exchange-rate` | tasa BCV `{rate}` en diezmilésimas |
| GET/PUT | `/company` | ficha de la empresa (nombre, RIF, contacto, logo): la lee cualquier usuario con sesión, solo el `admin` la edita |
| GET/POST | `/products` · `/products/{id}/lots` | catálogo y recepción de lotes |
| GET | `/lots` | todos los lotes con existencia (la vista del galpón, en una sola llamada) |
| GET/POST | `/customers` · GET `/customers/{id}/credit` | clientes y estado de crédito |
| GET/POST | `/orders` · GET `/orders/{id}[/review]` | pedidos |
| POST | `/orders/{id}/approve` · `/reject` | aprobación de crédito |
| GET/POST | `/trucks` · GET `/trucks/{id}/load-plan` · POST `/trucks/{id}/dispatch` | picking FEFO y despacho |
| POST | `/trucks/{id}/depart` `{extra:[{productId,quantity}]}` | sale el camión: factura sus pedidos y sube carga extra (el chofer, solo el suyo) |
| GET | `/trips[?truckId=&status=]` · `/trips/{id}` | viajes; el detalle trae stock a bordo, resumen y diferencias |
| POST | `/trips/{id}/deliveries` · `/sales` · `/presales` · `/payments` | entregar, vender del camión, tomar pedido, cobrar en la calle |
| POST | `/trips/{id}/close` · `/settlement/approve` | el chofer cierra la ruta; la oficina aprueba el cuadre |
| GET | `/invoices?customerId=&pending=true` · `/invoices/{id}` | facturas |
| POST | `/invoices/{id}/payments/preview` · `/payments` | cobro mixto |

Errores: `{"error": "mensaje"}` con 400 (inválido), 401 (sin sesión), 403 (sin permiso), 404, 409 (estado cambió), 422 (regla de negocio), 429 (demasiados intentos de login).

## Fuera de esta primera entrega

Multi-galpón (tenant), reportes y sincronización offline por cola (para reintentar operaciones del chofer sin duplicarlas hace falta un id de operación idempotente). El modelo está listo para sumarlos como nuevos casos de uso sin tocar el dominio existente.
