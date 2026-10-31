# Before the first release: customer-ordering

The first merge to `main` runs `docker-publish` and `release`. Two services built from
this template (pick-face-replenishment, task-interleaving) each needed a rework because
packaging was missing or wrong when that moment came. Work through this list on a feature
branch, **before** the first `develop` → `main` PR. Delete a row only when the service
really does not have that part, and say so in the PR.

`new-service.sh` already generated `Dockerfile` and `.dockerignore` for you (verbatim from
the fleet reference). The chart is NOT generated: it is shaped by what the service runs
(consumer groups, MCP, analytics, frontend), so copy the closest sibling's
`charts/<repo>/` and adapt it. References on `origin/develop`:
`IQVO/pick-face-replenishment` (Kafka consumers, MCP, analytics, frontend) and
`IQVO/task-interleaving`.

| # | Item | Status | Done when | Guarded by |
|---|------|--------|-----------|------------|
| 1 | **Real runtime only.** `cmd/api` wires Postgres, Kafka and delivery-promise and nothing else; the test doubles under `internal/adapters/outbound/memory` are never imported by `cmd/**`. `DATABASE_URL`, `KAFKA_BROKERS`, `DELIVERY_PROMISE_URL` and the three `*_CONSUMER_GROUP` variables are mandatory. | [x] done | `cmd/**` has no import of the memory or repocontract packages; `run(ctx)` returns a boot error naming the missing variable before anything opens. | `TestRuntimeBinaryHasNoInMemoryAdapters`; boot errors in `cmd/api/main_test.go` (`TestLoadRuntimeNamesTheMissingVariable`); `cmd/api/e2e_integration_test.go`. |
| 2 | **No Go file at the repo root.** The godog feature files are in `features/`, the runner in `internal/bdd`. | [x] done | `ls *.go` prints nothing; `make bdd` finds `TestFeatures`. | `TestNoGoFilesAtRepoRoot`. |
| 3 | **`Dockerfile` builds every `cmd/*`.** `ENTRYPOINT ["./api"]` is `cmd/api`, `EXPOSE 8080` is the default `HTTP_ADDR`. | [x] done | `docker build .` succeeds and `docker run --rm <image>` exits 1 with `DATABASE_URL is required` (verified). The build stage digest was moved to the one the sibling services pin, because the template's older digest ships Go 1.27.2 while `go.mod` requires 1.27.3. | `trivy-scan` / `docker-publish` jobs. |
| 4 | **`.dockerignore`** excludes docs, harness, test output. | [x] done | Build context is small. | none: review once. |
| 5 | **Helm chart `charts/customer-ordering/`** with `Chart.yaml` `name: customer-ordering`; chart directory, `Chart.yaml` name and the `helm package` file name must all be `customer-ordering`. | [x] done | `helm lint charts/customer-ordering -f charts/customer-ordering/ci/default-values.yaml`, `ct lint --charts charts/customer-ordering --validate-maintainers=false --check-version-increment=false` and `helm template` with the required values pass (Helm 4.3.1 locally; CI pins 3.16.4). One component (`api`); the release job packages `customer-ordering-<version>.tgz`. | `helm-lint` (pull requests), `chart-tests`, `release`. |
| 6 | **`DATABASE_URL` comes from a Secret**, never from the ConfigMap. | [x] done | `helm template` shows `secretKeyRef` (the Secret is `database.existingSecret` or the one the chart builds from `database.url`); `MIGRATIONS_DATABASE_URL` is an optional key of the same Secret (`optional: true`). | `render_test.sh` (row 8). |
| 7 | **Pod hardening and shutdown room.** `terminationGracePeriodSeconds` above the binary's bounds: `SHUTDOWN_DRAIN_DELAY` (5 s) + HTTP drain (10 s) + the three consumers awaited one after the other (3 x 10 s) + relay (10 s) = 55 s worst case, so 60 s (the earlier 35 s counted one consumer). | [x] done | Values carry 60 and the chart refuses anything that does not exceed `config.shutdownDrainDelay` + 50 s; non-root (uid 1000), read-only root filesystem, no privilege escalation, `drop: [ALL]`, `RuntimeDefault` seccomp, `/healthz` and `/readyz` probes. | `render_test.sh` (row 8). |
| 8 | **Chart tests in `charts/customer-ordering/tests/`.** Every env var `cmd/api` reads must be rendered: `DATABASE_URL`, `MIGRATIONS_DATABASE_URL`, `KAFKA_BROKERS`, `DELIVERY_PROMISE_URL`, `DELIVERY_PROMISE_TIMEOUT`, the three `*_CONSUMER_GROUP`, `HTTP_ADDR`, `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY`, `LOG_LEVEL`. | [x] done | `bash charts/customer-ordering/tests/render_test.sh` prints `PASS` (it scans `cmd/api` for the variables it reads, so a new one fails until the chart renders it; it also asserts no `*_MODE`, the refusals, the hardening and the image tag); `python3 charts/customer-ordering/tests/test_service_selectors.py` prints `PASS`. | `chart-tests` job (runs every `tests/*.sh` and `test_*.py`). |
| 9 | **Consumer group ids are chart values**, stable and service-prefixed, never derived from the pod name: `DELIVERY_PROMISE_CONSUMER_GROUP`, `ORDER_SOURCING_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP`. | [x] done | Each is rendered from `config.deliveryPromiseConsumerGroup`, `config.orderSourcingConsumerGroup`, `config.shippingConsumerGroup`, refuses to render empty (naming the value), does not change with the release name and cannot be set through `extraEnv`. | `render_test.sh` (row 8). |
| 10 | **Optional CI jobs match the service.** `web` and `docs-api-drift` are not in `ci.yml` (no `web/`, no generated docs); `api-lint` runs the two Spectral rulesets on `apis/openapi.yaml` and `apis/asyncapi.yaml`. | [x] done | No job in `ci.yml` is permanently red or skipped for a missing directory. | first PR's CI run. |
| 11 | **Repository secrets** `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN`; GHCR push allowed for the Actions token. | [ ] ask the repo admin | A dry run of `docker-publish` on a throwaway branch. | none. |
| 12 | **`govulncheck` is clean** before the PR to `main`. | [x] done | `make vuln` exits 0 after bumping `golang.org/x/crypto` to v0.56.0, `golang.org/x/text` to v0.41.0, `github.com/moby/go-archive` to v0.3.0 and `github.com/klauspost/compress` to v1.18.7 (GO-2026-6355, GO-2026-6354, GO-2026-6629, GO-2026-6253). | `vuln` job. |

Follow-up left: the repository secrets (row 11, for the repo admin; this checklist cannot see or create them). The chart (rows 5-9) is done. Keep this file: it is the
service's packaging record. It prevents three failures: a runtime that can run without Postgres/Kafka (row 1), Go files at the root (row 2), a release
that fails on the first push to `main` because packaging was never built (rows 3-9).
