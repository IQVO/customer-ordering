# ADR 0010: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01). Builds on [ADR 0002](./0002-aggregates-and-invariants.md)
(A13 the order id derives from the `Idempotency-Key`, A24 the address id does, A26
the order history keyset) and [ADR 0003](./0003-consumed-contracts-and-local-read-models.md)
(the consumed delivery-promise routes). Part of the fleet REST cutover: it deploys
together with every other service of the set (see the warehouse-docs REST guideline).

## Context

The 2026-10-30 portfolio assessment found three command-URL styles, two spellings of
the replay header, key reuse answered `422` in most services, lists under per-resource
keys and offset or `after` paging. customer-ordering had: `POST /orders/{orderId}:cancel`
(a custom-method suffix), `Idempotent-Replayed`, key reuse as `422`, a key stored per
`(route, key)` AFTER the handler ran (so the key row was not in the transaction of the
order and its outbox rows, and two concurrent first requests could both run), and
`GET /orders` paged with `after` (default 20, maximum 100, `422` on a bad cursor).

## Decision

The fleet guideline applies unchanged; nothing in it is a toggle.

- **Nouns, not verbs (R1, R2).** `POST /orders/{orderId}:cancel` becomes
  `POST /orders/{orderId}/cancellations` (a transition resource, guideline 1.4). The
  operation id `cancelOrder`, the status codes and the response body do not change.
  The old route is gone, with no alias; `TestRetiredRoutesAreGone` pins it.
- **Every POST is idempotent (R3-R6, R8).** The four POST routes (`/checkout`,
  `/orders/{orderId}/cancellations`, `/shoppers`, `/shoppers/{shopperId}/addresses`) sit
  behind `RequireIdempotencyKey`. `PUT` and `DELETE` do not exist in this service. The
  key is 1 to 128 printable ASCII characters (`400 idempotency-key-required` when
  absent, `400 idempotency-key-invalid` when malformed). The fingerprint is method + path
  + SHA-256 of the body. A repeat replays the stored answer (status, body,
  `Content-Type`, `Location`) with `Idempotency-Replayed: true`; the same key with
  another fingerprint is `409 idempotency-key-reused`. `Idempotent-Replayed` and the
  `422` reuse answer are retired everywhere.
- **The key is global and durable.** `ports.IdempotencyStore` is now one method,
  `Do(ctx, key, requestHash, at, fn)`: the Postgres store claims the key inside the
  transaction it opens, hands that transaction to `fn` through the context (every
  `UnitOfWork.Do` of the use case joins it as a savepoint), and stores the answer in the
  same commit. The aggregate, the outbox rows and the key row commit or roll back
  together; a 5xx or a panic rolls back and is never stored; a 4xx is stored and
  replayed; concurrent requests with one key are serialised by the unique index and the
  loser replays the winner's answer. No migration: `idempotency_keys.route` stays and is
  always the empty string. The in-memory store (unit tests, BDD) holds the store lock for
  the duration of `fn` and restores its snapshot on failure.
- **Keyset paging (R7).** `GET /orders` takes `limit` (1-500, default 100) and `cursor`
  and answers `{items, nextCursor}`. The cursor is the fleet `page` package
  (`internal/adapters/inbound/http/page`, copied unchanged): base64url JSON of the last
  row's `placedAt` (RFC 3339, nanoseconds) and `orderId`; the repositories, both
  Postgres and in-memory, order by `(placed_at DESC, order_id ASC)` and resume strictly
  after that position. `400 invalid-limit`, `400 invalid-cursor` and
  `400 unknown-query-parameter` (the retired `after`, `offset`, `nextToken` fail loudly).
  A missing `shopperId` stays `422 validation-failed`.

### Renamed routes

| Old | New |
| --- | --- |
| `POST /orders/{orderId}:cancel` | `POST /orders/{orderId}/cancellations` |
| `GET /orders?shopperId=&after=&limit=` (query) | `GET /orders?shopperId=&cursor=&limit=` |

### Exemptions (`x-pagination-exempt`)

- `GET /orders/{orderId}/tracking` is `view`: one composite tracking view of one order,
  bounded by the order's shipment groups; the arrays are parts of that single resource.
- `GET /shoppers?email=` is `view`: the e-mail is unique, so the call is a lookup of one
  shopper (404 when absent) whose representation contains its saved addresses (at most
  20); it is not a collection.

### Consumers changed in this repository

- `internal/adapters/outbound/deliverypromise/gateway.go` (the one outbound HTTP client)
  calls `POST /delivery-quotes`, `POST /promises/{id}/confirmations` and
  `POST /promises/{id}/cancellations`, and sends an `Idempotency-Key` on every POST
  (the quote mints a fresh UUID per call; promise calls keep `<orderId>:<groupNo>[:verb]`).
  The client never reads a collection, so no cursor loop is needed.
- The console remote under `web/` only issues `GET /orders/{id}`, `/tracking` and
  `/ship-to`: no change. MCP tools and A2A skills call the use cases, not HTTP.
- Other repositories that call these routes (storefront-bff, storefront-web) change in
  their own pull requests with the strings above.

## Consequences

- Breaking by design: new cancel URL, `cursor` instead of `after`, key reuse is `409`,
  the replay header is spelled `Idempotency-Replayed`, the page size bounds moved to
  100/500. All pull requests of the cutover merge and deploy as one set.
- A checkout now holds its database transaction while it calls delivery-promise (the
  key claim has to be in the transaction that stores the order). The call timeout
  (`DELIVERY_PROMISE_TIMEOUT`, 5 s) bounds it; size the Postgres connection pool for one connection per in-flight checkout.
- `.spectral.yaml` enforces R1-R8 in the `api-lint` job; the fleet integration tests
  (real router, real Postgres) pin fresh key, byte-identical replay, `409` reuse, `400`,
  N concurrent same-key requests with one order, cached `4xx` and uncached `5xx`.
