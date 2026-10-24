import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { getOrder } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { Facts, GroupStatusPill, LineTable, OrderStatusPill, PageHeader, Stack } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { lineNosLabel, orderIdProblem, utcLabel, windowLabel } from "../lib";
import type { Order, OrderGroup } from "../types";

/** `orders`: a lookup by order id, then `orders/:orderId`. */
export function OrderLookupScreen() {
  const [orderId, setOrderId] = useState("");
  const navigate = useNavigate();
  const id = orderId.trim();
  const problem = id === "" ? null : orderIdProblem(id);
  const go = () => {
    if (id !== "" && problem === null) navigate(encodeURIComponent(id));
  };
  return (
    <Stack gap={5}>
      <PageHeader title="Orders" subtitle="A customer order, its cart lines and the promise of every shipment group" />
      <Card title="Look up an order">
        <Form label="Order lookup" onSubmit={go}>
          <FormRow>
            <TextField
              label="Order"
              value={orderId}
              onChange={setOrderId}
              placeholder="ord-3f9c1a2b"
              hint={problem ?? "The id checkout returned when the order was placed"}
            />
            <SubmitButton disabled={id === "" || problem !== null}>View order</SubmitButton>
          </FormRow>
        </Form>
      </Card>
    </Stack>
  );
}

/** The route element of `orders/:orderId`. */
export function OrderRoute() {
  const { orderId } = useParams();
  return orderId ? <OrderScreen orderId={orderId} /> : null;
}

/**
 * One order: GET /orders/{orderId} -- the status, the shopper, the
 * destination prefix, the cart lines and every shipment group with its
 * promise. The ship-to address is deliberately not here (its own restricted
 * screen); cancellation is an API operation.
 */
export function OrderScreen({ orderId }: { orderId: string }) {
  const order = useRequest(() => getOrder(orderId), `order|${orderId}`);
  return (
    <Stack gap={5}>
      <PageHeader title={`Order ${orderId}`} subtitle="Status, lines and the promise of each shipment group" />
      <RequestView state={order} what="the order">
        {(o) => <OrderBody order={o} />}
      </RequestView>
      <Link to="../orders">← Look up another order</Link>
    </Stack>
  );
}

function OrderBody({ order }: { order: Order }) {
  return (
    <>
      <Card title="Order">
        <Facts
          items={[
            { label: "Status", value: <OrderStatusPill status={order.status} /> },
            { label: "Shopper", value: order.shopperId },
            { label: "Destination prefix", value: order.destinationPostalPrefix },
            { label: "Placed at", value: utcLabel(order.placedAt) },
            ...(order.cancelledAt ? [{ label: "Cancelled at", value: utcLabel(order.cancelledAt) }] : []),
          ]}
        />
      </Card>
      <Card title="Lines">
        <LineTable lines={order.lines} />
      </Card>
      <Card title="Shipment groups">
        <Stack gap={4}>
          {order.groups.map((g) => (
            <GroupFacts key={g.groupNo} group={g} />
          ))}
        </Stack>
      </Card>
    </>
  );
}

function GroupFacts({ group }: { group: OrderGroup }) {
  return (
    <Facts
      items={[
        { label: `Group ${group.groupNo}`, value: <GroupStatusPill status={group.status} /> },
        { label: "Promise", value: group.promiseId },
        { label: "Tier", value: group.tier },
        { label: "Ship by", value: utcLabel(group.shipBy) },
        { label: "Deliver by", value: windowLabel(group.deliverByEarliest, group.deliverByLatest) },
        { label: "Ships", value: lineNosLabel(group.lineNos) },
        { label: "Revision", value: String(group.revisionNo ?? 0) },
        ...(group.cancelReason ? [{ label: "Cancel reason", value: group.cancelReason }] : []),
        ...(group.cancelRequestedAt ? [{ label: "Cancel requested", value: utcLabel(group.cancelRequestedAt) }] : []),
        ...(group.cancelRefusedReason ? [{ label: "Cancel refused", value: group.cancelRefusedReason }] : []),
      ]}
    />
  );
}
