---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the hexagonal order (domain invariant, use case, handler, apis/openapi.yaml, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the
invariant it relies on validates nothing and gets bypassed.

The worked example is `POST /orders/{orderId}:cancel`:
`internal/domain/customerorder/cancel.go` `CancelOrder` ->
`internal/application/usecases/cancel.go` `CancelOrder` ->
`internal/adapters/inbound/http/handlers.go` `handleCancelOrder`. The read side
example is `GET /orders/{orderId}` (`usecases/queries.go` `GetOrder` -> `handleGetOrder`).

## 1. Domain first

Does the rule already exist in `internal/domain/<customerorder|shopper|shipto>/`? A
handler holds no business logic. A new rule goes into the aggregate with a
table-driven test in the same package (`cancel_test.go`, `place_test.go`,
`status_test.go`, ...) and a named `Err...` variable (`customerorder/order.go`).
Pure Go: no I/O, `now` and ids passed in. Then re-run the mutation gate for that
package (`gremlins unleash ./internal/domain/<pkg>`).

## 2. Application: the use case

Add it to `internal/application/usecases/` (`checkout.go`, `cancel.go`, `shopper.go`
for commands; `queries.go` for reads). A command embeds `Deps` (`deps.go`):
`transact` runs load, domain call, `Save` and `publish` (outbox append) in ONE
`ports.UnitOfWork` and retries on `ports.ErrConcurrentUpdate`. A query holds only
its read port. Return a view from `internal/application/views`. If a new repository
method is needed, add it to `internal/application/ports/ports.go` (interfaces only),
implement it in `internal/adapters/outbound/postgres/` AND
`internal/adapters/outbound/memory/`, and extend the shared suite in
`internal/adapters/outbound/repocontract/` so both run it. Unit-test the use case in
`internal/application/usecases/*_test.go`.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- add the request body to `dto.go` (strict decoding, unknown fields are 400);
- add the use-case field to `Server` and the route to `NewRouter` in `server.go`; a
  state-changing route goes through `s.idempotent(routeName)` (a new `route...`
  constant), a read uses `r.Get`;
- write the handler in `handlers.go` (decode, call the use case, encode the view,
  `writeError` for every failure);
- map a new domain `Err` in `errors.go` `problemCatalogue` (kebab-case slug, the
  status the OpenAPI documents) and add it to `errors_test.go`
  `TestEveryTypedErrorHasItsStatus`;
- wire the use case in `cmd/api/main.go` `buildServer`.

## 4. Contract: apis/openapi.yaml

Describe the operation, its `Idempotency-Key` parameter (writes), the success body
and every problem it can return, then lint:
`npx --yes @stoplight/spectral-cli lint apis/openapi.yaml --ruleset .spectral.yaml`
(the `api-lint` CI job runs the same). Keep `.claude/rules/rest-api.md` and the
README in step.

## 5. Behaviour

Add the happy path and each refusal to `features/*.feature` (`checkout.feature`,
`cancellation.feature`, ...; the steps are in `internal/bdd/features_test.go` and
`assertions_test.go`, refusals are named by error) and a case to
`internal/adapters/inbound/http/handlers_test.go`. The real middleware, Postgres and
Kafka are proven by `cmd/api/e2e_integration_test.go`.

## 6. Verify before opening the PR

`make check`, `make coverage`,
`go test -tags=integration ./cmd/... -race -count=1 -timeout 900s`, and the spectral
lint above. The ship-to address must not leak: only `GET /orders/{orderId}/ship-to`
returns it.
