---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
---

# Testing

The test pyramid as it is on this branch. Every layer has a `make` target; the
CI jobs of `.github/workflows/ci.yml` call the same targets, so a green local
`make check-all` predicts a green pipeline (apart from the Docker-based jobs).

| Layer | What it proves | Where | Run with |
| --- | --- | --- | --- |
| Unit | domain invariants, use cases over in-memory doubles, HTTP handlers over `httptest`, adapters (encoder, gateway, relay, consumers) | `internal/domain/*`, `internal/application/usecases`, `internal/adapters/**`, `cmd/*` | `make test` (`go test ./... -race`) |
| BDD acceptance | the real HTTP API against Gherkin scenarios | `features/*.feature`, run by `internal/bdd` | `make bdd` |
| Repository contract | the same contract suite runs against the in-memory and the Postgres repositories | `internal/adapters/outbound/repocontract`, used by `memory` and `postgres` tests | `make test`, `make integration` |
| Integration | real Postgres and Kafka via testcontainers: repositories, migrations, the relay, checkout compensation, consumed outcomes, the real entry points of `cmd/api`, `cmd/mcp`, `cmd/a2a` | files named `*_integration_test.go` (build tag `integration`), 12 files, 17 test functions | `make integration` (needs only Docker) |
| Contract (events) | the encoder output equals golden CloudEvents, and the catalogue matches `apis/asyncapi.yaml` | `internal/adapters/outbound/kafka/testdata/*.golden.json`, `internal/architecture/catalogue_fitness_test.go` | `make test`, `make arch-test` |
| Architecture fitness | the hexagon and the fleet rules are executable tests | `internal/architecture/` | `make arch-test` |
| Mutation | the domain tests assert rather than merely execute | `.gremlins.yaml`, `internal/domain/*` | `make mutation`, `make mutation-full` |
| Coverage gate | at least 90% over `internal/domain` and `internal/application` | `Makefile` (`COVERAGE_THRESHOLD`) | `make coverage` |
| Web | lint, `tsc -b`, vitest and build of the console remote | `web/` | `npm run lint && npx tsc -b && npm test && npm run build` inside `web/` |

## Targets

| Target | Runs |
| --- | --- |
| `make check` | `fmt-check vet build lint test`, the pre-push gate (lefthook runs it on `git push`; pre-commit runs `fmt-check vet lint`) |
| `make check-fast` | `fmt-check vet arch-test` and the tests of the packages changed against HEAD |
| `make check-all` | `check` plus `coverage arch-test bdd` |
| `make integration` | `go test -tags=integration ./... -race -count=1`, deliberately not part of `check` |
| `make mutation` | gremlins on every `internal/domain/<pkg>` in turn (the fast blocking gate) |
| `make mutation-full` | gremlins on `./internal/domain` with `--workers 1 --timeout-coefficient 30` |
| `make vuln` | `govulncheck ./...` |
| `make guide-lint`, `make harness-test` | lint the agent guides, unit-test the agent hooks |

## BDD scenarios

Eight feature files, **59 scenarios** (`features/`):

| File | Scenarios |
| --- | --- |
| `checkout.feature` | 11 |
| `fulfilment_status.feature` | 10 |
| `shopper.feature` | 8 |
| `api_errors.feature` | 8 |
| `cancellation.feature` | 7 |
| `orders_history.feature` | 7 |
| `api_orders.feature` | 5 |
| `api_idempotency.feature` | 3 |

`internal/bdd/features_test.go` (`TestFeatures`) runs them in-process against
the real HTTP router with in-memory repositories, a fake delivery-promise and a
fake clock, so they need no database and no broker.

## Fitness tests

`make arch-test` runs `internal/architecture/`. The rules, by test:

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | dependencies point inward only: domain imports nothing of the application or adapters, application imports no adapter |
| `TestRuntimeBinaryHasNoInMemoryAdapters` (+ sensor) | `cmd/**` never imports `outbound/memory` or `repocontract` |
| `TestMCPServerIsReadOnly`, `TestMCPAdapterDependencyRule`, `TestMCPOptionalToolInputsCarryOmitempty` | `cmd/mcp` wires no write use case and never imports the REST adapter; optional tool inputs carry `omitempty` |
| `TestA2AAdapterDependencyRule`, `TestA2AServerIsReadOnlyAndHasNoOutboundClient`, `TestA2ASDKIsImportedByTheAdapterAndItsCommandOnly` | the same for `cmd/a2a`, plus no outbound A2A client |
| `TestNoAuthMiddlewareReintroduced` | no bearer, JWT or API-key check under the inbound adapters |
| `TestEventCatalogueMatchesContract` | the event types in code equal `apis/asyncapi.yaml` |
| `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestReplayConsumersSetCommitInterval` | every Kafka message is a CloudEvent; replaying consumers set a commit interval |
| `TestKafkaWriterTopicNotSetTwice`, `TestKafkaWritersSetRequiredAcks` | writers never set the topic on both writer and message; acks are `RequireAll` |
| `TestKafkaConsumerGroupNeverHardcodedInline` | group ids come from configuration |
| `TestOutboxRelaysPassAttemptCap` | a relay is built with `WithMaxAttempts` so a poison row cannot block it |
| `TestDeadLetterCallsGuardCancellation` | dead-letter calls respect context cancellation on shutdown |
| `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers` | integration tests start their own containers: no `KAFKA_BROKERS` or `DATABASE_URL` gate, no hard-coded `localhost:9092` |
| `TestNoGoFilesAtRepoRoot` | Go files live under `cmd/` and `internal/` |

Several rules have a companion `...SensorBites` test that feeds the detector a
bad fixture and expects it to fail, so a rule cannot go green by finding
nothing.

## Mutation testing

`.gremlins.yaml` sets one worker, a timeout coefficient of 30 and thresholds
`efficacy: 98`, `mutant-coverage: 98`. The last measurement recorded in the file
(2026-10-23, gremlins v0.6.0) is 100% efficacy and mutant coverage for each
domain package: `shared` (31 killed), `shipto` (21), `shopper` (17),
`customerorder` (139), 0 lived. `make mutation` and the `mutation-fast` job loop
over every `internal/domain/*/` package.

## CI jobs

`ci.yml` runs on push to `main` and `develop`, on pull requests, weekly
(Monday 06:00 UTC) and on manual dispatch.

| Job | What it runs | When |
| --- | --- | --- |
| `lint` | golangci-lint v2.14.0 | every push and PR |
| `guide-lint` | `scripts/harness/guide_lint.py`, `test_hook.py` (advisory, `continue-on-error`) | every push and PR |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif (gate 15) | every push and PR |
| `test` | build, vet, `make coverage` (90% gate), uploads `coverage.out` | every push and PR |
| `bdd` | `go test ./... -run TestFeatures -v` | every push and PR |
| `integration` | `go test -tags=integration ./... -race -count=1` | every push and PR |
| `mutation-fast` | gremlins over every `internal/domain/*/` | every push and PR |
| `mutation` | exhaustive gremlins run, opens a `harness:red` issue on a scheduled failure | schedule and manual dispatch |
| `drift` | dead code, `go mod tidy -diff`, knip on `web/`, coverage-quality (advisory) | schedule and manual dispatch |
| `api-lint` | Spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml` | every push and PR |
| `vuln` | `govulncheck ./...` | every push and PR |
| `helm-lint` | `ct lint --charts charts/customer-ordering`, selector test | pull requests |
| `chart-tests` | every `charts/customer-ordering/tests/*.sh` and `test_*.py` (a test that prints `SKIP` fails) | every push and PR |
| `arch-test` | `go test ./internal/architecture/... -v` | every push and PR |
| `web` | `npm ci`, lint, `tsc -b`, vitest, build in `web/` against a sibling `warehouse-ui-kit` checkout | every push and PR |
| `docker-publish` | image to GHCR, cosign signature, SBOM attestation | push to `main`, after the gate jobs |
| `release` | tags the next patch version, publishes the chart as OCI | push to `main`, after `docker-publish` |

The documentation site has its own workflow, `.github/workflows/docs.yml`: it
builds the site on pull requests and deploys it on push to `main`.

## Writing tests

- Domain and use-case tests use the in-memory doubles from
  `internal/adapters/outbound/memory`; they are never imported by `cmd/**`.
- A new repository behaviour goes into `repocontract` so both implementations
  are held to it.
- A new Postgres or Kafka test must be a `*_integration_test.go` file with the
  `integration` build tag and must start its containers through
  `internal/testing/pgtest` or `kafkatest`; the fitness tests reject an env gate.
- A new Gherkin scenario goes into `features/` and a step into `internal/bdd`.
