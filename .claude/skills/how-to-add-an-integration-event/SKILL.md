---
name: how-to-add-an-integration-event
description: Publish or consume a cross-service Kafka event in this service: CloudEvents 1.0 type naming, AsyncAPI, the type catalogue ADR, the encoder, the transactional outbox, the inbound consumers. Use when touching internal/adapters kafka or outbox code, a domain event, or apis/asyncapi*.yaml.
---

# How to add an integration event

customer-ordering publishes two events and consumes fourteen (ADR 0003, ADR 0004).
The envelope is CloudEvents 1.0 only; read `.claude/rules/integration-events.md` and
`.claude/rules/fleet/cloudevents.md` first. The worked example for publishing is
`CustomerOrderCancelled`; for consuming, `FulfillmentShipped`.

## Publishing a new event

1. **Domain event.** A typed struct in `internal/domain/customerorder/events.go`
   (`CustomerOrderCancelled`): embeds `shared.Header`, fields exactly the model's
   `data` list, methods `EventName()`, `Entity()` (`customerorder`, lowercase, no
   separators) and `SubjectID()` (the orderId), plus a `shared.Event` assertion. The
   aggregate command (`CancelOrder` in `cancel.go`) returns it.
2. **Contract, one commit.** Add the message and its `data` schema to
   `apis/asyncapi.yaml`, AND one
   `type: com.retailnetwork.commerce.customer-ordering.customerorder.<Event> stream: events`
   line to `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`.
   `TestEventCatalogueMatchesContract` fails when the two disagree, so the push fails
   when they are not in the same commit. Lint:
   `npx --yes @stoplight/spectral-cli lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml`.
   Evolution is additive only; a breaking change is a new `:v2` dataschema. Check the
   consumers' AsyncAPI (order-sourcing, delivery-promise) still reads what you send.
3. **Encoder.** In `internal/adapters/outbound/kafka/encoder.go` add the payload
   struct (camelCase json tags, instants through `ts`, arrays never null, NO address)
   and a `case` in `payloadFor`. The envelope itself is built by
   `internal/adapters/kafka/cloudevents` only; never hand-build one. Add a golden
   test and `testdata/<Event>.golden.json` (exact JSON of every attribute and the
   payload) in `encoder_test.go`.
4. **Publish from the use case** through `Deps.publish` inside the same
   `ports.UnitOfWork` as the `Save` (`internal/application/usecases/deps.go`): the
   aggregate row and the outbox row commit or roll back together. Never publish after
   the commit and never call Kafka from a use case.
5. **Nothing else.** The relay (`internal/adapters/outbound/outbox/relay.go`) already
   drains every row; the topic is `retailnetwork.customer-ordering.events`. An
   analytics-only event would need its own topic constant, stream and ADR.
6. **Prove it.** `go test -tags=integration ./internal/adapters/outbound/outbox/ -race -count=1`
   reads events back from a real broker; extend `cmd/api/e2e_integration_test.go`
   when the event is part of a REST flow.

## Consuming a new event from a sibling context

1. **Read the producer's contract now:**
   `git -C ~/warehouse-systems/<producer> show origin/feature/service:apis/asyncapi.yaml`.
   The full `type` string must be byte-identical.
2. **Vendor a tolerant copy** into `apis/asyncapi.yaml` (a `receive` message with
   only the fields you read) and record the decision in a new ADR that supersedes or
   amends ADR 0003.
3. **Dispatch.** Add the full type constant and a decoder in
   `internal/adapters/inbound/kafka/handlers.go`, pin the string in
   `TestConsumedTypeStringsAreTheProducersContracts`, and route it in
   `dispatch_test.go`.
4. **Use case.** Add `Consumer.Apply<Event>` in
   `internal/application/usecases/consume_{promise,sourcing,tracking}.go`. It runs
   through `Consumer.handle`, which writes the inbox row (`processed_events`, keyed by
   the CloudEvents `id`) in the same transaction as the effect; a replay is a no-op.
5. **Consumer group.** A new consumer needs its own mandatory `*_CONSUMER_GROUP`
   variable (boot error naming it; `cmd/api/config.go`), never a literal
   (`TestKafkaConsumerGroupNeverHardcodedInline`). A bad message goes to
   `<topic>.dlq`; DLQ writes guard shutdown (`TestDeadLetterCallsGuardCancellation`).
6. **Prove it** with the dispatch test and, for a REST flow, the e2e test, which
   produces the sibling events straight onto the real topics.

## Verify before opening the PR

`make check`, `go test ./internal/architecture/ -count=1` (catalogue, CloudEvents-only,
relay cap), the spectral lint of the AsyncAPI, and the integration command above.
