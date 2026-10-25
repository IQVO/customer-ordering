---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Route -> use case, kept in sync with `apis/openapi.yaml` (the routes are registered
in `internal/adapters/inbound/http/server.go` `NewRouter`, handlers in `handlers.go`,
wired in `cmd/api/main.go` `buildServer`):

- `POST /checkout` -> `PlaceOrder` (201; the order id derives from the Idempotency-Key; exactly one of `shipTo` / `shipToAddressId`, A25)
- `GET /orders?shopperId=&after=&limit=` -> `ListOrdersByShopper` (newest first, keyset cursor, default limit 20 max 100, A26)
- `GET /orders/{orderId}` -> `GetOrder`
- `GET /orders/{orderId}/tracking` -> `GetOrderTracking`
- `GET /orders/{orderId}/ship-to` -> `GetShipTo` (restricted: the only order route that returns the address)
- `POST /orders/{orderId}:cancel` -> `CancelOrder` (body `{"reason"}`)
- `POST /shoppers` -> `RegisterShopper` (201)
- `GET /shoppers/{shopperId}` -> `GetShopper` (profile with `savedAddresses[]`, A27)
- `GET /shoppers?email=` -> `FindShopperByEmail` (trimmed, lower-cased match, A16/A27)
- `POST /shoppers/{shopperId}/addresses` -> `AddSavedAddress` (201; the `addressId` derives from the Idempotency-Key, A24)
- `GET /healthz` (liveness), `GET /readyz` (readiness): not business operations.

## Conventions

- **Errors** are RFC 9457 `application/problem+json`: `type` is `/problems/<slug>`
  (`errors.go` `problemCatalogue`), plus `title`, `status`, `detail`, `instance`.
  Shared: `not-found` 404, `illegal-transition` 409, `concurrent-update` 409,
  `validation-failed` 422; one slug per model `Err...` (`cancel-after-lock`,
  `status-regression`, `line-without-promise`, `promise-destination-mismatch`,
  `shopper-exists`, `too-many-addresses`) and per delivery-promise refusal
  (`offer-not-quotable` 409 with
  the re-quoted offers, `promise-capacity-exhausted`, `book-closed`,
  `group-spans-nodes`, `delivery-promise-unavailable` 503). An unmapped error is an
  opaque 500 `internal-error`. A new `Err` needs a catalogue row and a row in
  `errors_test.go` `TestEveryTypedErrorHasItsStatus`.
- **Idempotency-Key** is required on the four state-changing routes
  (`idempotency.go`): same key + same request replays the first response
  (`Idempotent-Replayed: true`), same key + other request is 422
  `idempotency-key-reused`, missing key 400 `idempotency-key-required`, keys live 24 h
  in `idempotency_keys`. A new state-changing route registers through
  `s.idempotent(route)`, never a bare `r.Post`.
- **NUL** (U+0000) is rejected before anything else (`nul.go`): 404
  `resource-not-found` in the path, 400 elsewhere.
- Bodies are decoded strictly: unknown fields are 400 `malformed-request`; a value
  the domain refuses is 422.
- **Handlers hold no business logic**: decode, call a use case, encode the view.
- **PII**: the address appears only in the `ship-to` response and in the
  shopper routes' `savedAddresses` (the address the shopper saved, A27); never
  log a body.
- **Auth**: none. REST is unauthenticated, as in the rest of the network
  (`internal/architecture/fitness_test.go` `TestNoAuthMiddlewareReintroduced` keeps it so).
- Unit tests (`handlers_test.go`) run the real use cases over the memory test
  doubles; the real wiring is proven on Postgres and Kafka in
  `cmd/api/e2e_integration_test.go`.
