# ADR 0019: Every problem type has a replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-18). Narrows the sentence "a 4xx is stored and replayed" of
[ADR 0010](./0010-adopt-the-fleet-rest-api-guideline.md); the fleet decision is the owner's of
2026-11-18 (warehouse-harness-template `docs/idempotency-replay-classes.md` and
`.claude/rules/fleet/idempotency-and-outbox.md`).

## Context

`RequireIdempotencyKey` (`internal/adapters/inbound/http/idempotency.go`) stored every answer
below 500 under the key for 24 hours, a 4xx too, and replayed it with `Idempotency-Replayed: true`.
That is right for a refusal that is final for the request (a malformed body) and wrong for one a
retry could pass: a `409 concurrent-update` that was a lost race, a `404 not-found` for an order
another request creates a moment later, a `409 promise-capacity-exhausted` while capacity frees up.
Each of them was pinned for 24 hours under a key the client had every right to retry.

The store (`internal/adapters/outbound/postgres/idempotency.go`) claims the key row INSIDE the
transaction of the request, runs the handler behind `SAVEPOINT handler_run` and stores the answer in
the same commit, so releasing a key is the rollback that a 5xx already takes: the claim and every
write of the request disappear together.

## Decision

Every problem type of the catalogue has exactly one replay class, declared on its catalogue entry
(`problemCatalogue` in `internal/adapters/inbound/http/errors.go`, the field `replay`):

| Class | Meaning | Stored and replayed |
| --- | --- | --- |
| `FINAL` | the answer is a function of the request (or of state that only moves one way) | yes |
| `OUTCOME` | the command committed state and then refused; the writes commit with the stored answer | yes |
| `RETRYABLE` | nothing committed and the answer depends on state that can change | no, the key is released |

- A 2xx is stored. A 5xx and a panic are never stored. The middleware holds no list of slugs and no
  status range beyond those two: `writeError` marks the capturing writer with the class of the problem
  it answers (`markReplay`), the capture turns that into `idempotency.Record.Release`, and the store
  returns before its `UPDATE` so the deferred rollback drops the claim and the writes
  (`postgres/idempotency.go` 102; the in-memory double does the same).
- A 4xx answer whose problem declares no class (a handler that wrote a bare status) is not stored.
  There is no default class in the catalogue: `TestEveryProblemTypeDeclaresAReplayClass` fails for an
  entry without one, and `TestTheADRDeclaresTheClassOfEveryProblem` keeps the table below equal to the
  catalogue.
- Releasing the key means the request is forgotten: the same body under the same key runs again, and a
  different body under the same key is a **new request**, not `409 idempotency-key-reused`.
- A replay still carries `Idempotency-Replayed: true`; the 24-hour window is unchanged.
- **A concurrent duplicate while the first ends RETRYABLE.** The duplicate blocks on the unique index of
  the key row until the first transaction ends (`claimSQL`). The first rolls back and the row never
  existed, so the blocked claim inserts it and runs the handler itself: it never replays the released
  refusal and never runs alongside the first. The copies queued behind it replay its answer.
  `repocontract` `a copy waiting on a released key runs fn itself` (8 callers: 2 runs, never together,
  one refusal, one success, 6 replays) and `TestACopyWaitingOnARetryableFirstRunsItself` pin it.
- There is no MCP or A2A write tool (ADR 0005, ADR 0008): nothing else stores answers.

## The class of every problem type

`FINAL` and `OUTCOME` rows are stored; the reasons of the `RETRYABLE` rows say what can change. The
use cases run their writes in a nested unit of work (`unit_of_work.go` `nested`, a savepoint that is
rolled back on an error), so a refusal returned by a use case leaves none of that use case's writes
behind; the one exception is the `OUTCOME` below.

| Problem type | Class | Status | Reason |
| --- | --- | --- | --- |
| `malformed-request` | FINAL | 400 | the body is not valid JSON for the operation: the request alone (`decodeJSON`) |
| `invalid-limit` | FINAL | 400 | the `limit` of a list; GET only, classified for completeness |
| `invalid-cursor` | FINAL | 400 | the cursor of a list; GET only |
| `unknown-query-parameter` | FINAL | 400 | a parameter the collection does not declare; GET only |
| `not-found` | RETRYABLE | 404 | `CancelOrder` loads the order of the path (`cancel.go` 24) and `AddSavedAddress` the shopper (`shopper.go` 58). An order id derives from a checkout's `Idempotency-Key` (A13), so another request can create it a moment later; judgement call J1 |
| `offer-not-quotable` | OUTCOME | 409 | see below |
| `promise-capacity-exhausted` | RETRYABLE | 409 | only `POST /promises` answers it (delivery-promise `openapi.yaml`), so nothing is confirmed yet (`checkout.go` 238-243); capacity frees up |
| `book-closed` | RETRYABLE | 409 | same route, same reason; a book can reopen |
| `group-spans-nodes` | RETRYABLE | 422 | `POST /promises` again: whether a group ships from one node depends on delivery-promise's read model; unsure, so RETRYABLE (J2) |
| `delivery-promise-unavailable` | RETRYABLE | 503 | a 5xx is never stored |
| `line-without-promise` | FINAL | 422 | every line in exactly one group, every group with a promise: the request (`place.go` `checkGroup`, and `preflight` `checkout.go` 176-194 raises it before delivery-promise is called) |
| `promise-destination-mismatch` | RETRYABLE | 422 | compares the order's destination with the terms delivery-promise answered (`place.go` 210), raised after `MakePromise` and before any confirm (`checkout.go` 246): nothing committed, and the terms are another service's answer (J2) |
| `status-regression` | FINAL | 409 | on a keyed route only `CancelOrder` raises it, for a DELIVERED, LOST or RETURNED order (`cancel.go` 25-27); those group states are terminal (`commands.go` 43, 92, 131) so the order never leaves them (J3) |
| `cancel-after-lock` | RETRYABLE | 409 | every group is locked or beyond (`cancel.go` 41-46), but an event can still cancel a locked group (`ConfirmGroupCancelled`, `cancel.go` 79-95) and the repeat is then a no-op success (`cancel.go` 37-43) |
| `shopper-exists` | FINAL | 409 | the e-mail is taken (`shoppers.go` 113 and 150); a shopper is never deleted and an e-mail never changes |
| `too-many-addresses` | FINAL | 422 | 20 saved addresses (`shopper.go` 178-181); saved addresses are only ever added |
| `concurrent-update` | RETRYABLE | 409 | the optimistic-lock guard of an aggregate or of the policy version lost a race (`orders.go` 226 and 247, `shoppers.go` 119 and 152, `policies.go` 80); the retry reads the new version. Also `PlaceOrder` answers it when the derived order id belongs to another shopper (`checkout.go` 205): a retry gets the same refusal, harmlessly |
| `illegal-transition` | RETRYABLE | 409 | the aggregate's state does not allow the command now; only the event consumers reach it (`commands.go` 24 and 131), no keyed route answers it today, and a state that events move is what the class is for |
| `validation-failed` | RETRYABLE | 422 | MIXED, J4: most are a field of the request, but "shopper is not registered" and "not a saved address of the shopper" (`checkout.go` 154, 161 and 213) name facts another request can create |

### The one OUTCOME: `offer-not-quotable`

A confirm that fails after earlier groups were confirmed cancels those promises at delivery-promise
(`checkout.go` 249-254, `compensate` 275-297) and enqueues every cancel that failed on
`compensation_cancels` (`enqueueFailedCancels` 303-314). That write runs through `inTx`, which joins the
request's transaction (`unit_of_work.go` 85-87), so it commits with the stored answer: the command
committed state (a confirmed-then-cancelled promise, and a queue row) and then refused. The refusal of
a confirm is always `offer-not-quotable` (`gateway.go` `mapRefusal` 260-261: every 404, 409 or 422 that
is not one of the two other slugs); `promise-capacity-exhausted`, `book-closed` and `group-spans-nodes`
come from `POST /promises` only. Re-executing it under the same key is also unsafe: `MakePromise` and
`ConfirmPromise` use the deterministic keys `<orderId>:<groupNo>` and `:confirm` and an order id derives
from the checkout's key, so the retry would meet delivery-promise's stored 2xx of the earlier confirm
for a promise this service has cancelled since. The answer is therefore remembered, and the caller
(who must re-quote anyway, the answer carries the fresh offers) uses a new key. `offer-not-quotable` is
also what `MakePromise` answers for an expired or consumed offer, which only moves one way, so the same
class is right there. Pinned by `TestAnOutcomeRefusalCommitsItsCompensationRowWithTheStoredAnswer`
(real router, real Postgres). Judgement call J5.

### Outside the store (no class: they never reach the replay decision)

| Problem type | Status | Reason |
| --- | --- | --- |
| `idempotency-key-required` | 400 | written by the middleware before a key exists |
| `idempotency-key-invalid` | 400 | written by the middleware before the store is called |
| `idempotency-key-reused` | 409 | written on a replay with another fingerprint; it is never the stored answer |
| `resource-not-found` | 404 | the NUL guard, before the middleware (`nul.go`) |
| `dependency-unavailable` | 503 | `/readyz`, not a keyed route |
| `internal-error` | 500 | a 5xx is never stored |

## Judgement calls (each is one line of the catalogue to flip)

- **J1. `not-found` is RETRYABLE** although a shopper id is minted by this service: the slug is shared
  with the order, whose id the caller can derive before the checkout commits, and nothing is deleted
  but the order may simply not exist yet.
- **J2. Delivery-promise-dependent refusals** (`group-spans-nodes`, `promise-destination-mismatch`,
  `promise-capacity-exhausted`, `book-closed`) are RETRYABLE: they are another service's answer about
  state that changes, and nothing was confirmed when they are raised. A retry costs one more call
  under the same deterministic keys.
- **J3. `status-regression` is FINAL**, against the plan's candidate list (RETRYABLE): on a keyed
  route it is raised for a closed order only, and a closed order never reopens.
- **J4. `validation-failed` is RETRYABLE although most of its causes are the request's own.** One class
  per slug; a stored "the shopper is not registered" would pin a state that the next request changes.
  Plain field validation is therefore not replayed any more: the retry runs the checks again, which
  cost nothing (`preflight` runs before delivery-promise). The clean fix is own problem types for the
  two referenced facts, a contract change left to the owner.
- **J5. `offer-not-quotable` is OUTCOME** (not FINAL, not RETRYABLE). It behaves exactly as before
  (stored and replayed, writes committed) but names why: the compensation write must commit with it.
- **J6. `cancel-after-lock` is RETRYABLE** (the plan's candidate said probably FINAL): a locked group
  can still be cancelled by an event, after which the same cancel succeeds.

## Consequences

- After a RETRYABLE refusal the key is free: a different body under it is a new request, not
  `idempotency-key-reused`. A client that reuses a key on purpose now gets its retry executed.
- **Deterministic-key callers.** checkout calls `POST /checkout` with key = sessionId
  (`internal/adapters/outbound/customerordering/gateway.go` there): `concurrent-update` (a
  retriable 409 to it), `promise-capacity-exhausted` and `book-closed` are no longer pinned for
  24 hours; `offer-not-quotable` still is, as before, so a session that re-quotes needs a new key.
  This service calls delivery-promise with `<orderId>:<groupNo>`, `:confirm` and `:cancel`;
  `mapRefusal` reads the status and the problem slug only, never `Idempotency-Replayed`, so it does
  not depend on whether delivery-promise stored a refusal.
- `apis/openapi.yaml` descriptions of the key, the replay header and the routes say which answers are
  stored; `validation-failed`, `not-found` and the retriable 409s tell clients to retry the same key.
- A 5xx at the confirm step still rolls back the compensation row it just enqueued, because the 5xx
  rolls back the whole request (existing behaviour, unchanged; reported to the owner).
