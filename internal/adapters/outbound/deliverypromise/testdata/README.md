# delivery-promise contract data

`delivery-promise.openapi.yaml` is a byte copy of `apis/openapi.yaml` from IQVO/delivery-promise `develop`
(the producer's own file; never edited here). It is listed in `contracts/pinned.json`, and the CI job
`pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when it differs from the producer's
`develop`. Refresh = copy the producer's file over it, never edit it, then fix what the contract tests
report.

customer-ordering calls four routes, all POST (the model's sync_calls QuoteDeliveryOffers and
MakePromise, ConfirmPromise E7, and the compensating CancelPromise the compensation worker retries):
`/delivery-quotes` (`quoteDeliveryOffers`), `/promises` (`makePromise`),
`/promises/{promiseId}/confirmations` (`confirmPromise`) and `/promises/{promiseId}/cancellations`
(`cancelPromise`). The compensation queue worker (`internal/adapters/outbound/compensation`) calls no
HTTP of its own: it retries `Gateway.CancelPromise`, so it rides the same pin.

The `*.example.json` fixtures satisfy the schemas of that copy. The spec carries no full response
examples for these routes (only field-level `example` values and the problem examples of the error
responses), so the fixtures are derived from the schemas; producer example backfill pending. The
problem fixtures use the slugs the gateway maps (`promise-capacity-exhausted`, `book-closed`,
`group-spans-nodes`, `not-found`, `illegal-transition`). Refresh fixtures together with the pin when
the producer contract changes.
