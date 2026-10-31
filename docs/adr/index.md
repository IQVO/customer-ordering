# Architecture decision records

The decisions of `customer-ordering`, in the order they were taken. The ADR bodies
are immutable; a changed decision gets a new ADR that supersedes the old one.
The *Status* column is copied from each ADR's own `## Status` section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-customer-ordering-bounded-context.md) | Customer Ordering is a bounded context of the retail network | Accepted (bootstrap) |
| [0002](./0002-aggregates-and-invariants.md) | the CustomerOrder and Shopper aggregates, their invariants and the assumptions the model left open | Accepted (2026-10-23) |
| [0003](./0003-consumed-contracts-and-local-read-models.md) | consumed contracts, local read models and relationships | Accepted (2026-10-23) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-23) |
| [0005](./0005-mcp-server-adoption.md) | a read-only MCP server | Accepted (2026-10-24) |
| [0006](./0006-console-remote.md) | the console remote (customer_ordering_mfe) — a read-only screen over the existing REST | Accepted (2026-10-24) |
| [0007](./0007-fleet-observability-standard.md) | Adopt the fleet observability standard (logs, metrics, traces) | Accepted (2026-10-25) |
| [0008](./0008-a2a-inbound-adapter.md) | A2A inbound adapter: a read-only Agent2Agent agent | Accepted (2026-10-30) |
| [0009](./0009-delivery-window-time-zone.md) | the delivery window carries its local time zone | Accepted (2026-10-30) |
