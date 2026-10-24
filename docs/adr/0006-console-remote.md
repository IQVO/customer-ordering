# ADR 0006: the console remote (customer_ordering_mfe) — a read-only screen over the existing REST

## Status

Accepted (2026-10-24).

## Context

customer-ordering was a backend context of the retail network with no
user-facing screen of its own. Operators walk an order's life (place → track →
cancel) through the warehouse-console shell like every other context, and the
shell mounts per-context Module Federation remotes — this context had none.
This ADR records why a remote is needed and who consumes it.

## Decision

### The remote

`web/` is a Vite + React Module Federation remote, container
`customer_ordering_mfe`, exposing `./App` only (no props, relative routes),
built on the fleet's reference recipe (task-interleaving's `web/`) rather than
re-derived:

- PINNED host/remote contract: federation container `customer_ordering_mfe`,
  gateway mount `/mfes/customer-ordering/`, dev/preview port **4012** —
  deliberately off the console's 5181–5197 ladder, whose every slot an existing
  remote holds, and clear of 4010/4011 which other off-ladder remotes hold;
  4012 collides with no remote and no service port.
- The API base is resolved at RUNTIME from the console's `/config.json`
  (`window.__WAREHOUSE_CONFIG__.apiOrigin` →
  `${apiOrigin}/api/customer-ordering`); a production build throws when it is
  missing instead of falling back to a dev port. The remote calls ONLY its own
  context's API.
- Packaged as `nginxinc/nginx-unprivileged` (`web/Dockerfile`, `web/nginx.conf`),
  served under `/mfes/customer-ordering/` by warehouse-infra's Nginx web
  gateway. The API gateway never serves these bytes; no ingress/httproute; the
  chart Service stays ClusterIP.
- `@warehouse/ui-kit` is a sibling checkout consumed as a named BuildKit
  context; the image never installs from a foreign `node_modules`.
- The Vite config is a STATIC object, never the callback form (vitest
  mergeConfig breaks on callbacks — a fleet lesson); vitest.config.ts is
  standalone.

### The screens — existing REST reads only, no new endpoints

Three surfaces, exactly what `apis/openapi.yaml` serves today, all GETs:

1. **Orders** — `GET /orders/{orderId}`: the status, shopper, destination
   prefix, the cart lines and every shipment group with its promise (tier,
   ship-by, deliver-by window), revision and cancellation facts.
2. **Tracking** — `GET /orders/{orderId}/tracking`: per group the promise
   verdict, at-risk/unsourceable signals, lock and ship times, the packages
   with carrier, state, scans and exceptions, and the lost/returned outcome.
3. **Ship-to** — `GET /orders/{orderId}/ship-to`: the restricted address read
   (synthetic PII, label printing only), shown as its own clearly-labelled
   screen.

The remote is READ-ONLY: placing an order, cancelling one and registering a
shopper are API operations with `Idempotency-Key`, not screen operations.
Where a screen wanted data the API does not serve, the screen was cut, not the
API; no `PUT`/`POST` is ever sent.

### The deployment surface

The Helm chart grows a `frontend` component (`frontend-deployment.yaml`,
`frontend-service.yaml`, `_frontend.tpl`), default **off**; the Service
selects `component=frontend` so no Service ever selects two Deployments, and
the frontend Deployment renders NO env — nothing about the remote is
configured in the cluster. CI grows a `web` job (ui-kit sibling checkout,
node 22, then lint, typecheck, vitest and a production build inside `web/`);
npm never runs at the repo root. Registering the remote in the console shell
(its `vite.config.ts` entry, route, nav) is the console repo's change, not
this repo's.

## Consequences

- Operators get the order, its tracking and its ship-to in the console; the API
  surface is unchanged (openapi.yaml untouched).
- The dev port 4012 must stay unique across the fleet's remotes; any collision
  is fixed HERE, in the remote's repo.
- The MCP server (ADR 0005) and this remote are independent additions: the
  remote reads REST, the agents read MCP, both over the same three read use
  cases.
