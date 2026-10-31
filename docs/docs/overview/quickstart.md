---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 4
---

# Quickstart

Build the services, see what `cmd/api` refuses to boot without, run it on
Postgres and Kafka and make the first calls. Every answer below was captured
from a real run of `cmd/api` and `cmd/mcp` built from this branch against a
throwaway Postgres 16 and the fleet's single Kafka broker.

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | `1.27.3` (`go.mod`) | everything |
| `make` | any | the targets below |
| Docker | any recent | the Postgres container below and `make integration` (testcontainers boots its own Postgres and Kafka) |
| golangci-lint | `v2.14.0` (`GOLANGCI_VERSION` in the `Makefile`, pinned in CI) | `make lint`, `make check` |
| gremlins | `v0.6.0` (`GREMLINS_VERSION`) | `make mutation`, `make mutation-full` |
| govulncheck | latest | `make vuln` |
| Spectral (`@stoplight/spectral-cli`) | latest | linting the two specs, see [Testing](/docs/development/testing#ci-jobs) |
| Node 20 or 22 | docs: `>=20`, `web/` CI job: 22 | this documentation site (`docs/`) and the console remote (`web/`) |
| lefthook | optional | `lefthook install` wires `fmt-check vet lint` at pre-commit and `make check` at pre-push |

The fleet runs **one** Kafka broker: in the kind cluster it is reachable from
the host at `localhost:9092`. The docker-compose Kafka is retired, and this
repository ships no compose file. `cmd/api` needs Postgres **and** Kafka to
boot and to be ready; there is no mode without them. `cmd/mcp` and `cmd/a2a`
need only Postgres.

## Build, test, run

```bash
make help          # list the targets
make build         # go build ./...
make test          # go test ./... -race
make arch-test     # go test ./internal/architecture/... -v
make bdd           # go test ./... -run TestFeatures -v
make coverage      # tests with -race plus the 90% gate over domain + application
make check         # fmt-check vet build lint test
make check-all     # check + coverage + arch-test + bdd
make integration   # go test -tags=integration ./... -race -count=1 (needs Docker)
```

Build the API and start it with nothing configured:

```bash
go build -o customer-ordering ./cmd/api
./customer-ordering
```

It refuses to boot before opening a connection or binding the port, and names
the first missing mandatory variable (exit status 1, real output):

```text
{"time":"2026-10-31T12:45:47.138097-03:00","level":"ERROR","msg":"service exited with error","error":"DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode"}
```

With `DATABASE_URL` set the next refusals are `KAFKA_BROKERS`,
`DELIVERY_PROMISE_URL` and then each consumer group variable, one boot error at
a time (`cmd/api/config.go`, `loadRuntime`).

## Run on Postgres and Kafka

Start a throwaway database. The service never creates topics through an admin
API: the cluster broker auto-creates `retailnetwork.customer-ordering.events`
on the relay's first write, and the three consumed topics exist once their
producers have published.

```bash
docker run -d --rm --name co-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=customer_ordering \
  -p 127.0.0.1:55432:5432 postgres:16

export DATABASE_URL='postgres://postgres:pw@127.0.0.1:55432/customer_ordering?sslmode=disable'
export KAFKA_BROKERS=localhost:9092
export DELIVERY_PROMISE_URL=http://localhost:8000/api/delivery-promise   # Kong route of the kind cluster
export DELIVERY_PROMISE_CONSUMER_GROUP=customer-ordering-dev-delivery-promise
export ORDER_SOURCING_CONSUMER_GROUP=customer-ordering-dev-order-sourcing
export SHIPPING_CONSUMER_GROUP=customer-ordering-dev-shipping
export HTTP_ADDR=127.0.0.1:18080
./customer-ordering
```

The group ids are yours to choose; use ids that differ from the deployed
service's, or you steal its partitions. A fresh group replays each topic from
the first offset: the first lines of a real run are
`consumed event skipped` records with `reason` `order <id>: not found` for
every historical event of an order this database does not hold. That is
normal, and harmless (ADR 0002, A7).

Migrations run at boot. After the first start `schema_migrations` reads
`4|f` (version 4, not dirty) and the public schema holds `assignment_links,
customer_orders, group_signals, idempotency_keys, order_groups, order_lines,
outbox_events, processed_events, schema_migrations, shopper_addresses,
shoppers, tracking_packages`.

The startup log (JSON lines on stdout) is, in order: `postgres adapters
configured`, `telemetry configured`, one `consumer running` line per consumer
(`consumer`, `topic`, `group_id`, `brokers`), `outbox relay running`
(`interval`, `max_attempts`, `topic`) and `http server listening`. The
[Observability](/docs/operations/observability) page lists every message.

## First calls

REST is unauthenticated. Every state-changing request needs an
`Idempotency-Key` header.

```bash
curl -si localhost:18080/readyz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}
```

Register a shopper with one saved address (the e-mail is lower-cased):

```bash
curl -si -X POST localhost:18080/shoppers \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: qs-shopper-2' \
  -d '{"email":"Ana@example.test","savedAddresses":[{"name":"Ana Souza","line1":"1 Test Street","city":"Springfield","region":"SP","postalCode":"01000","countryCode":"BR"}]}'
```

```text
HTTP/1.1 201 Created
Content-Type: application/json
Location: /shoppers/d1baa263-7de4-4584-8ae2-6608f93f8e0f

{"shopperId":"d1baa263-7de4-4584-8ae2-6608f93f8e0f","email":"ana@example.test","status":"ACTIVE","registeredAt":"2026-10-31T15:44:21.456895Z","savedAddresses":[{"addressId":"2045f59b-544a-4730-a354-b5b612e855f2","name":"Ana Souza","line1":"1 Test Street","city":"Springfield","region":"SP","postalCode":"01000","countryCode":"BR"}]}
```

Observed answers for the other first reads and the common mistakes:

| Request | Answer |
| --- | --- |
| `GET /shoppers?email=ANA@example.test` | `200`, the same profile (trimmed, lower-cased match) |
| `GET /orders?shopperId=d1baa263-...` | `200 {"items":[]}`: no orders yet |
| `GET /orders/ord-ghost` | `404` `/problems/not-found`, detail `order ord-ghost: not found` |
| `POST /checkout` `{}` without the header | `400` `/problems/idempotency-key-required`, detail `send an Idempotency-Key header of 1 to 255 characters, unique per logical request` |
| `POST /shoppers` with an unknown JSON field (`name`) | `400` `/problems/malformed-request`, detail `request is not valid for this operation: json: unknown field "name"` |
| the same key again with another body | `422` `/problems/idempotency-key-reused` |

The last two rows are one real sequence: the `400` was stored under
`qs-shopper-1` (any response below 500 is stored for 24 h), so re-sending the
corrected body with the same key was refused with `422`. A new key (`qs-shopper-2`
above) succeeded. See [Troubleshooting](/docs/operations/troubleshooting).

All errors are `application/problem+json`, for example:

```json
{"type":"/problems/not-found","title":"Not found","status":404,"detail":"order ord-ghost: not found","instance":"/orders/ord-ghost"}
```

`POST /checkout` needs a live delivery-promise and a quotable offer; the
`PlaceOrder` flow is covered by the BDD features and by the end-to-end test
`cmd/api/e2e_integration_test.go`. A checkout against a shared broker publishes
`CustomerOrderPlaced`, so try it only against a disposable stack.

### The MCP server

```bash
export DATABASE_URL='postgres://postgres:pw@127.0.0.1:55432/customer_ordering?sslmode=disable'
export MCP_ADDR=127.0.0.1:18090
go run ./cmd/mcp       # listens on :8090 by default, /healthz and /mcp
```

See [MCP tools](/docs/mcp/tools) for the live `tools/list` answer. `cmd/a2a` is
started the same way and additionally needs `A2A_AGENT_URL`, for example
`http://localhost:8093/`.

Stop `cmd/api` with Ctrl-C: it flips `/readyz` to 503, waits
`SHUTDOWN_DRAIN_DELAY` (5 s), drains HTTP, stops the consumers and the relay
and closes the pool. Remove the database with `docker rm -f co-pg`.

## Building these docs

```bash
cd docs
npm ci
npm run build         # also fails on broken links and anchors
npm run gen-api-docs  # regenerate docs/api-reference/rest from apis/openapi.yaml
```

The REST reference pages are generated from `apis/openapi.yaml`; run the
generator after any spec change and commit the result, because `sidebars.ts`
imports the generated `sidebar.ts`.
