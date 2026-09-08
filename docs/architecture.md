# Architecture and interview notes

```text
HTTP (Iris)
  -> Controller: parse/validate request, map status codes
  -> Service: business rules and cross-aggregate checks
  -> Repository: parameterized SQL and row mapping
  -> MySQL (source of truth)
  -> Redis (Cache-Aside for hot product list)
```

## Runtime hardening

- All request bodies are capped by `MAX_REQUEST_BODY_BYTES` (1 MiB by default).
- Client-supplied request IDs are accepted only when they contain bounded ASCII characters; otherwise a cryptographically random ID is generated.
- Write endpoints return the same `{success,message,data}` envelope so clients can handle errors consistently.
- An idempotency key is bound to the original user, product, quantity and initial status. Reusing it with different parameters returns `409 Conflict` rather than silently returning an unrelated order.
- The API process runs as a non-root user in the container, while `/health` and `/ready` support orchestration probes.

## Reliability decisions

- MySQL foreign keys and indexes protect references and common list queries.
- Redis is an optimization only; cache failures fall back to MySQL.
- Login issues a signed, expiring Bearer token. `AUTH_SECRET` must be injected in production.
- Order writes use a MySQL transaction and `SELECT ... FOR UPDATE`; inventory deduction and order creation commit atomically.
- A unique `Idempotency-Key` prevents duplicate orders and duplicate stock deduction, including concurrent retries.
- Order reads and lifecycle operations are scoped to the authenticated user. Cancelling a pending order restores inventory in the same transaction.
- `/health` is intentionally dependency-light for container liveness; database readiness can be added as a separate probe.

## API examples

```bash
curl http://localhost:8080/health
curl -X POST http://localhost:8080/user/login -d 'userName=demo&password=demo123456'
curl -X POST http://localhost:8080/order/create -d 'userID=1&productID=1&quantity=2&orderStatus=0'
```
