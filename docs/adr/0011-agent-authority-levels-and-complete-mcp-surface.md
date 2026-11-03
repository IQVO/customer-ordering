# ADR 0011: agent authority levels and complete MCP surface

## Status

Accepted (2026-11-02). Applies the fleet decision "Agentic Standard v2"
(`docs/architecture/agentic-standard.md` and the fleet rule
`agents/fleet/agent-authority.md` in IQVO/warehouse-docs) to this context.
Extends [ADR 0005](./0005-mcp-server-adoption.md) (the read-only MCP server)
and [ADR 0008](./0008-a2a-inbound-adapter.md) (the read-only A2A agent);
touches no decision of [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md)
(no event changes: this context has no write tool or skill to attribute).

## Context

The fleet is standardising what an agent may do with a surface. Until now the
only signal was the MCP `readOnlyHint` annotation and the A2A `read` tag:
they say *whether* a call changes state, not *what level of authority* it
represents. A delegating agent (or a reviewer) could not read the reach of a
tool off the registry. Separately:

- the MCP handler was built with the Streamable HTTP default (stateful)
  options, which advertises only the 2025-11-25 revision and older, so a
  client speaking the 2026-07-28 per-request handshake (`server/discover`)
  was refused with `-32022 protocol version not supported`;
- the server offered tools only: no resource (a read model an agent can load
  without a tool call) and no prompt (this context's operator SOP).

## Decision

### Authority levels, declared where the tool is registered

Authority levels are the strings `L1`..`L4`. Every MCP tool and every A2A
skill declares exactly one; a read is `L1` or `L2`, a write is `L3` unless an
ADR of the owning context grants `L4`. This context's whole agent surface is
a pure read of its own state ([ADR 0005](./0005-mcp-server-adoption.md),
[ADR 0008](./0008-a2a-inbound-adapter.md)), so every declaration is `L1`:

| Surface | Name | Level | Why |
| --- | --- | --- | --- |
| MCP | `get_order` | `L1` | reads facts this context owns |
| MCP | `get_order_tracking` | `L1` | reads facts this context owns |
| MCP | `get_ship_to` | `L1` | reads facts this context owns |
| A2A | `get_order` | `L1` | the same read, delegated |
| A2A | `get_order_tracking` | `L1` | the same read, delegated |

- **MCP** declares it in the tool's `Meta` under `com.warehouse/authority`
  (the fleet-pinned key), through the pinned helper shape
  `Meta: authorityMeta(authorityL1)` with the const ladder
  `authorityMetaKey`, `authorityL1..4` in `authority.go`. It is returned
  verbatim in `tools/list` `_meta` and pinned in
  `testdata/tool_registry.golden.json`.
- **A2A** declares it as the skill tag `authority:L1` next to the existing
  `read` tags (no skill here is a write, so no `write` tag exists to keep).
- **L4 is not granted here.** No ADR of this context lets an agent act
  without a human; the surface has no write at all.

The level is a label for callers and reviewers; it is **not** access control
and nothing decides anything from it (the surface stays unauthenticated, like
REST, per the fleet decision).

### Stateless MCP

`mcp.NewStreamableHTTPHandler` now receives
`&mcp.StreamableHTTPOptions{Stateless: true}`. No `Mcp-Session-Id` is issued
and the server answers `server/discover` for the 2026-07-28 revision, while a
legacy `initialize` (for example `2025-06-18`) still answers `200`, so
existing session clients keep working. Nothing in this adapter relied on
cross-call session state: every tool is a single keyed read; a tool that ever
needs longer state takes an explicit, server-minted handle as an argument.
`stateless_test.go` pins the wire facts with raw POSTs (no SDK client), and
the revert-to-nil was proven: with the default options the first two probes
fail (no `2026-07-28` in `supportedVersions`; `tools/list` answers
`-32022`) while the legacy `initialize` still passes.

### One resource and one prompt

The MCP surface beyond tools ([ADR 0005](./0005-mcp-server-adoption.md)
charter: curated, bounded, never a dump):

- **Resource** `warehouse://customer-ordering/orders/{orderId}/tracking`
  (JSON): the order-tracking read model of one order — the same
  `GetOrderTracking` use case the `get_order_tracking` tool and
  `GET /orders/{orderId}/tracking` serve. Bounded by construction: one
  order's groups and packages. The ship-to address is deliberately not a
  resource.
- **Prompt** `triage_order_status` (argument `orderId`, required): the
  operator SOP for answering a customer's question about one order — read the
  order, read its tracking, interpret the promise verdicts and signals, know
  what to escalate. It names tools but never calls them, and it cannot write.

Resources and prompts are `L1` by nature; they declare no tool `Meta`.

## Consequences

- A reviewer reads an agent's reach off the registry; a new tool or skill
  cannot ship without declaring a level. Two sensors enforce it:
  `internal/architecture/mcp_authority_fitness_test.go` (the fleet's static
  go/ast sensor, copied verbatim from warehouse-harness-template — it also
  fails the build if the server ever offers no resource or no prompt) and the
  card-level `TestEveryAgentToolDeclaresAuthority` in the a2a adapter.
- A level can only change through a reviewed golden edit
  (`testdata/tool_registry.golden.json` carries the `_meta` authority).
- Clients speaking the 2026-07-28 revision work without an initialize round
  trip; clients speaking the legacy revisions see no difference except that
  no session id is issued (they did not need one for these reads).
- Facts still travel as CloudEvents on Kafka; MCP still carries tools (now
  plus one resource and one prompt) and A2A still carries delegation.
  Nothing here adds a producer, a transport or a write.
