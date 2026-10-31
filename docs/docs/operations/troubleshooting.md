---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
---

# Troubleshooting

Symptom, cause, check, fix, from the failure modes the code actually has. Log
messages are quoted exactly as `slog` writes them; see
[Observability](/docs/operations/observability) for the full list and
[Runbook](/docs/operations/runbook) for the procedures.

## Boot

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Process exits with status 1 and `service exited with error`, `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode` (real output) | a mandatory variable is missing; `cmd/api` reports one per start, in this order: `DATABASE_URL`, `KAFKA_BROKERS`, `DELIVERY_PROMISE_URL`, then `DELIVERY_PROMISE_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP` | the message names the variable; `kubectl logs deploy/customer-ordering --previous` | set the variable (chart: `database.*`, `kafka.brokers`, `config.deliveryPromiseUrl`, `config.*ConsumerGroup`); the chart itself refuses to render without them |
| `DELIVERY_PROMISE_TIMEOUT: "soon" is not a positive Go duration` | unparsable or non-positive duration (a boot error, unlike the relay knobs) | the quoted value | use `5s`, `750ms` |
| `cmd/a2a` exits with `A2A_AGENT_URL is required...` or `A2A_AGENT_URL="..." is not an absolute http(s) URL` | the Agent Card must advertise the URL clients dial | the message; chart `a2a.agentUrl` | set an absolute `http(s)` URL ending where JSON-RPC is served, for example `http://customer-ordering-a2a.<ns>.svc.cluster.local:8093/` |
| `retrying` WARN lines (`op` = `run migrations` or `ping postgres`), then exit | first dial of an injected pod reset, or Postgres/PgBouncer unreachable; the retry is bounded | `op`, `attempt`, `err` | restore the database; the last error is returned on exhaustion and the pod restarts |
| Boot blocks or fails in migrations behind PgBouncer | golang-migrate's session advisory lock does not survive transaction pooling | `MIGRATIONS_DATABASE_URL` equals the pooled DSN | give `MIGRATIONS_DATABASE_URL` a direct DSN (chart: `database.migrationsExistingSecretKey`) |
| Warnings `invalid OUTBOX_RELAY_INTERVAL; using the default`, `invalid OUTBOX_MAX_ATTEMPTS; using the default`, `ignoring invalid SHUTDOWN_DRAIN_DELAY` | a typo in an optional knob; the default is used, so a typo never removes the attempt cap | the `value` field | fix the value |
| `service exited with error`: `<consumer> stopped unexpectedly: exiting so the orchestrator restarts the service` | the relay or a consumer returned on its own (a lost broker, a failed reader) | the ERROR line before it (`consumer stopped`, `outbox relay stopped`) | restore Kafka; the restart is by design |

## Readiness and dependencies

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `/readyz` 503 `dependency-unavailable`, `/healthz` 200 | Postgres ping or the Kafka broker metadata request failed (detail names `postgres` or `kafka`, never the cause) | WARN log `readiness: dependency is not ready` has `dependency` and `error`; the verdict is cached 3 s | restore the dependency; it recovers within 3 s. Kafka down: writes are still accepted, events queue in `outbox_events` |
| `/readyz` 503 `{"status":"not_ready"}` | the pod is draining after SIGTERM | `shutdown: readiness flipped to not-ready` in the log | expected during rollout |
| Checkout answers 503 `delivery-promise-unavailable` | the synchronous call to delivery-promise failed or timed out (`DELIVERY_PROMISE_TIMEOUT`, default 5 s) | `curl $DELIVERY_PROMISE_URL/...` from the pod; `ordering_orders_placed_total{reason="delivery-promise-unavailable"}` | restore delivery-promise or correct `DELIVERY_PROMISE_URL`; nothing is placed |
| Checkout answers 409 `offer-not-quotable`, `promise-capacity-exhausted` or `book-closed` | delivery-promise refused: the offer expired or its capacity is gone, or the book is closed | the `reason` attribute of `ordering.orders.placed` | the caller asks for new offers and retries with a new `Idempotency-Key` |
| Checkout failed after some groups were confirmed | the later confirm failed; the earlier promises of the same checkout are cancelled again (ADR 0002 A14) | log `cancelled a confirmed promise of a failed checkout`; if `compensating cancel of a confirmed promise failed; the hold stays at delivery-promise` the hold remains | release the promise in delivery-promise by hand |

## REST answers

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| 400 `idempotency-key-required` | a POST without the header, or a key outside 1 to 255 characters | the request headers | send a unique key per logical request |
| 422 `idempotency-key-reused` right after a 400 | any response below 500 is stored under `(route, key)` for 24 h, including a rejected request; a corrected body with the same key then counts as a different request (reproduced in the [Quickstart](/docs/overview/quickstart)) | `SELECT * FROM idempotency_keys WHERE key = '...'` | use a new key; keep the body byte-stable per logical request |
| 400 `malformed-request`, detail `json: unknown field "x"` | unknown field, trailing data, empty or invalid JSON (the decoder is strict) | the detail names the field | send only the fields of the OpenAPI schema |
| 422 `validation-failed` | a domain or request validation failed (bad ship-to country code, empty cancel reason, bad `limit`, bad `after` cursor) | the detail | fix the input; `GET /orders` needs `shopperId` and `limit` at most 100 |
| 422 `line-without-promise`, `group-spans-nodes`, `promise-destination-mismatch` | the checkout groups do not cover each line exactly once, a group needs more than one node, or a promise was made for another destination | the detail | rebuild the checkout request from fresh offers |
| 404 `not-found` | unknown order or shopper (also on `GET /orders?shopperId=` for an unknown shopper) | the id | check the id |
| 409 `shopper-exists` | the lower-cased e-mail is registered | `GET /shoppers?email=` | use the existing shopper |
| 422 `too-many-addresses` | a shopper holds at most 20 saved addresses | `GET /shoppers/{id}` | fix: the API has no delete route for saved addresses, so the limit cannot be freed through it |
| 409 `cancel-after-lock` | every shipment group is locked; cancellation is now a return | the order's group statuses | not cancellable |
| 409 `concurrent-update` | optimistic version guard lost a race | repeated | retry with the same key; the use case already retried once (2 attempts) |
| 409 `illegal-transition`, `status-regression` | the order does not allow the change in its state | `GET /orders/{id}` | none |
| 500 `internal-error` | anything else (database error, bug) | ERROR `http request` line with the `request_id` and `trace_id` | follow the trace; the response never carries the cause |
| `GET /orders/{id}/ship-to` header `Cache-Control: no-store` and an address | by design: the only order route returning the address; restricted to label printing | | do not cache or log it |

## Outbox and events

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `outbox_events` rows with `published_at IS NULL` pile up; `outbox relay pass failed` repeats | Kafka unreachable or the topic refused the write | `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;` and the `last_error` column | restore Kafka; the relay drains in id order by itself |
| ERROR `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause`; later events overtake the parked one | the row failed `OUTBOX_MAX_ATTEMPTS` (20) times | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;` | fix the cause, then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;` |
| One failed first write to `retailnetwork.customer-ordering.events` with an unknown-topic error | the topic does not exist yet; the broker auto-creates it on the first write | the relay retries with the same event id | none |
| A consumer logs `<consumer> handling failed; retrying the same message` | a transient failure (database, contention with a sibling group on the same order); capped backoff 200 ms to 5 s | `attempt` / `max_attempts` (5) | after 5 attempts the message goes to `<topic>.dlq` |
| Messages in `<consumed topic>.dlq` | the handler gave up (5 attempts) or the payload can never be applied; the `x-dlq-error` header has the cause | broker tooling on the `.dlq` topic | fix the cause, re-publish the original to the source topic |
| WARN `skipping a message that is not a valid CloudEvent` | a producer wrote a record that is not a CloudEvent | `topic` | fix the producer |
| INFO `consumed event skipped`, `reason: order ...: not found` | an event about an order this database does not hold: normal when a fresh group replays history, or in a shared kind cluster after a database reset | `eventId`, `type` | none; if it appears for real orders, check that the consumer's database is the one `cmd/api` writes |
| An order's status or tracking does not move | the producing context has not published yet, the consumer is lagging, or the event would move the status backwards (skipped by design, ADR 0002 A7) | `processed_events` for the event id; broker tooling for the group lag | wait for the producer; the service exports no lag metric |
| A new local run re-reads every old event | a fresh consumer group id replays each topic from the first offset | `consumer running` log line shows `group_id` | expected |
| Silence on a consumed topic, or WARN `<consumer> broker connection stalled or lost; fetching again after the backoff` | the topic has no messages yet (its producer never wrote), or the broker connection dropped and is being re-dialled | broker tooling; the readers dial lazily, so a broker outage never blocks boot | check the producer context and the broker |

## MCP and A2A

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| MCP client gets a session or 400 error on `tools/list` | Streamable HTTP needs `initialize` first and the `Mcp-Session-Id` header afterwards, with `Accept: application/json, text/event-stream` | [MCP tools](/docs/mcp/tools#try-it) | follow the handshake |
| MCP `tools/call` returns `isError: true` with `not-found: order ... not found` | unknown or malformed order id | the text | check the id |
| A2A returns JSON-RPC error `-32009` `this version is not supported: requested "0.3", served "1.0"` | the request did not carry `A2A-Version: 1.0` | the header | send it |
| A2A reply carries a `problem` part with 404 | `no order exists with orderId ...`, answered in-band, never as a JSON-RPC error | the data part | check the id |
| Agent Card advertises a wrong URL | `A2A_AGENT_URL` is wrong for the network the client is on | `GET /.well-known/agent-card.json`, `supportedInterfaces[0].url` | correct `A2A_AGENT_URL` (chart `a2a.agentUrl`) |

## Local development

| Symptom | Cause | Fix |
| --- | --- | --- |
| `make integration` fails to start containers | Docker is not running; the integration tests boot their own Postgres and Kafka with testcontainers and have no `DATABASE_URL` gate | start Docker |
| `make lint` says golangci-lint is not installed | the pinned version is not on `PATH` | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` |
| `git push` fails with "parallel golangci-lint is running" | another lint run holds the lock; the pre-push hook runs `make check` | wait about a minute and push again |
| `npm run build` in `docs/` fails on `api-reference/rest/sidebar` | the generated REST pages are missing | `npm run gen-api-docs` |
