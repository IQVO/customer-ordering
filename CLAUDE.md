# customer-ordering

harness-template: v4

Retail network, bounded context Customer Ordering (Generic, commerce tier). Turns a cart into a customer order: promises and confirms every shipment group at checkout, holds the shopper and the ship-to address (synthetic PII, only here), shows status and tracking, handles cancellation. Publishes 2 events, consumes 17, makes 4 synchronous calls to delivery-promise (quote, make, confirm and the compensating cancel).
Design source of truth: IQVO/warehouse-docs `network/model/model.yaml` (context customer-ordering). This service belongs to the retail network, not the warehouse-systems fleet.
This project is a domain-driven design study, not associated with any company: never name a real company, brand or product in code, comments, ADRs, specs, tests or fixtures (README `## Disclaimer`).

## Commands
`make check` (fmt, vet, build, lint, unit + handler + BDD; the pre-push gate), `make coverage` (>= 90% over internal/domain + internal/application), `make mutation` (gremlins per internal/domain/<pkg>, thresholds below measurement),
`make arch-test`, `make integration` (testcontainers Postgres + Kafka, needs Docker; build tag `integration`), `make vuln`. Lint: `golangci-lint run --allow-parallel-runners ./...`.
Guides: `python3 scripts/harness/repo_lint.py`, `python3 scripts/harness/guide_lint.py` must stay clean. Never gate a commit on a piped command.

## Layout (hexagonal)
`internal/domain/{shared,shipto,shopper,customerorder}` pure Go; `internal/application/{ports (interfaces only),views,usecases,readmodel,promising,outbox,idempotency}`;
`internal/adapters/inbound/{http (chi),kafka (3 consumers + DLQ)}`; `internal/adapters/outbound/{postgres,kafka,outbox,deliverypromise,clock,ids,memory,repocontract}`; `internal/adapters/kafka/cloudevents` (the ONLY envelope builder);
`internal/testing/{pgtest,kafkatest}` (testcontainers helpers); `internal/bootretry`; `cmd/api` (composition root, `run(ctx)`); `features/` (godog feature files, runner in `internal/bdd`, never a .go file at the repo root). Migrations are embedded in `internal/adapters/outbound/postgres/migrations`.

## Runtime rule
Real PostgreSQL and Kafka only. Mandatory: `DATABASE_URL`, `KAFKA_BROKERS`, `DELIVERY_PROMISE_URL`, `DELIVERY_PROMISE_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP` (boot error naming the variable, raised before the listener binds; `MIGRATIONS_DATABASE_URL` is an optional direct DSN).
The outbox relay and the three consumers are always on and the process exits non-zero when one ends on its own (`OUTBOX_MAX_ATTEMPTS` default 20, 0 = unlimited; `OUTBOX_RELAY_INTERVAL` 1s).
`/healthz` = liveness; `/readyz` = drain gate + Postgres ping + Kafka metadata request with an EMPTY non-nil topic list (503 `dependency-unavailable` when Kafka is down, writes still accepted via the outbox).
Shutdown: /readyz 503, `SHUTDOWN_DRAIN_DELAY` (5s), drain HTTP, stop consumers, stop relay, close pool. cmd/** never imports internal/adapters/outbound/memory or repocontract. No authentication on REST. See docs/docs/overview/runtime.md.

## REST
chi router, 6 operations of apis/openapi.yaml (POST /checkout, GET /orders/{id}, GET /orders/{id}/tracking, POST /orders/{id}/cancellations, GET /orders/{id}/ship-to, POST /shoppers) plus /healthz, /readyz. Every state-changing route takes `Idempotency-Key` (table `idempotency_keys`, key is global (looked up by key alone), 24h, replays the first response, same key + other request = 409 `idempotency-key-reused`).
NUL: 404 in the path, 400 elsewhere. Errors are RFC 9457 problem+json, `type` = `/problems/<slug>`: not-found, illegal-transition, concurrent-update, validation-failed + one kebab-case slug per model Err (cancel-after-lock, line-without-promise, ...). The ship-to address is returned ONLY by GET /orders/{id}/ship-to; never log a body, never put it in an event.

## Events: CloudEvents 1.0 is MANDATORY
Published type `com.retailnetwork.commerce.customer-ordering.customerorder.<Event>` (CustomerOrderPlaced, CustomerOrderCancelled); source `/retailnetwork/customer-ordering`; dataschema `urn:retailnetwork:customer-ordering:events:<Event>:v1`;
both on `retailnetwork.customer-ordering.events` (nothing analytics-only); subject and Kafka key = orderId; header `content-type: application/cloudevents+json; charset=UTF-8`. CustomerOrderCancelled is a REQUEST: order-sourcing answers FulfillmentCancelled or FulfillmentCancelRefused.
Consumed (full-type dispatch, inbox `processed_events`, DLQ `<topic>.dlq`): delivery-promise Promise{AtRisk,Revised,Kept,Broken,Unkeepable}; order-sourcing Fulfillment{Locked,Shipped,Unsourceable,Cancelled,CancelRefused}; shipping-and-tracking Label{Issued,Voided}, Package{Scanned,Delivered,ExceptionRaised,Lost,Returned} (same topic and `SHIPPING_CONSUMER_GROUP`; Lost/Returned close the group, A19). Re-read the producer's AsyncAPI before changing a consumer.
Use internal/adapters/kafka/cloudevents only. Transactional outbox: aggregates + outbox rows in ONE Postgres transaction, id minted once at append, at-least-once. No `warehouse.*` topic, ever.
A new event: domain struct + apis/asyncapi.yaml + ADR 0004 `type:` line in the SAME commit + encoder payload + golden test (.claude/skills/how-to-add-an-integration-event).

## Domain
Aggregates, invariants and assumptions A1-A23 are in docs/adr/0002; consumed contracts, read models and relationships in 0003. Order status is DERIVED from the group states (incl. terminal LOST/RETURNED, A23) and only moves forward; cancel applies to unlocked groups only. Checkout confirms every promise synchronously before the order is placed (a failed confirm = re-quote, no order; promises already confirmed by that checkout are cancelled through `POST /promises/{id}/cancellations`, A14).

## Workflow
GitFlow: feature/* -> PR into develop, CI green before merge, never push develop or main. lefthook: pre-commit fmt/vet/lint, pre-push `make check`. Commit and push after each step.
Deploy: Helm chart `charts/customer-ordering` (one `api` component; mandatory values `database.*`, `kafka.brokers`, `config.deliveryPromiseUrl`, `config.{deliveryPromise,orderSourcing,shipping}ConsumerGroup`, refused empty at render; `terminationGracePeriodSeconds` 60, must exceed drain delay + 50 s). `bash charts/customer-ordering/tests/render_test.sh` and `python3 charts/customer-ordering/tests/test_service_selectors.py` guard it; a new env var in cmd/api needs a chart value. CI `helm-lint` (PRs) and `chart-tests`. Registry secrets (row 11) are for the repo admin.

A2A: `cmd/a2a` (port 8093) is this context's read-only Agent2Agent agent, skills `get_order` (read), `get_order_tracking` (read), decided in `docs/adr/0008-a2a-inbound-adapter.md`; delegation only (facts stay CloudEvents on Kafka), unauthenticated like REST and MCP; rules in `.claude/rules/fleet/a2a.md`.
 Agent authority: every MCP tool and A2A skill declares a level (`com.warehouse/authority` _meta / `authority:L<n>` skill tag; L1 read, L2 recommend, L3 write - ADR 11); the MCP Streamable HTTP handler is stateless and answers the 2026-07-28 revision.

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/outbox/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
