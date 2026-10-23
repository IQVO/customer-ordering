# customer-ordering

Retail network, bounded context **Customer Ordering** (Generic, commerce tier).

Turns a shopper's cart into a customer order: obtains a promise for every shipment group at checkout, records the order with the shopper and ship-to address (synthetic PII, held only here), shows status and tracking, and handles customer cancellation.

Status: under construction. The design is proposed in
[retail-network-docs](https://iqvo.github.io/retail-network-docs/contexts/customer-ordering); `model/model.yaml` there is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.commerce.customer-ordering.<entity>.<Event>`), transactional outbox.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
