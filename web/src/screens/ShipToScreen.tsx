import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { getShipTo } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { Facts, PageHeader, Stack } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { orderIdProblem } from "../lib";
import type { ShipTo } from "../types";

/** `ship-to`: a lookup by order id, then `ship-to/:orderId`. */
export function ShipToLookupScreen() {
  const [orderId, setOrderId] = useState("");
  const navigate = useNavigate();
  const id = orderId.trim();
  const problem = id === "" ? null : orderIdProblem(id);
  const go = () => {
    if (id !== "" && problem === null) navigate(encodeURIComponent(id));
  };
  return (
    <Stack gap={5}>
      <PageHeader title="Ship-to" subtitle="The delivery address of an order (synthetic data; label printing only)" />
      <Card title="Look up an order's ship-to">
        <Form label="Ship-to lookup" onSubmit={go}>
          <FormRow>
            <TextField
              label="Order"
              value={orderId}
              onChange={setOrderId}
              placeholder="ord-3f9c1a2b"
              hint={problem ?? "The address is held only by this context and never appears in an event"}
            />
            <SubmitButton disabled={id === "" || problem !== null}>View ship-to</SubmitButton>
          </FormRow>
        </Form>
      </Card>
    </Stack>
  );
}

/** The route element of `ship-to/:orderId`. */
export function ShipToRoute() {
  const { orderId } = useParams();
  return orderId ? <ShipToScreen orderId={orderId} /> : null;
}

/**
 * The one restricted read: GET /orders/{orderId}/ship-to -- the synthetic
 * delivery address. Read-only like every screen of this remote; the address
 * is never cached by the service and never leaves it in an event.
 */
export function ShipToScreen({ orderId }: { orderId: string }) {
  const shipTo = useRequest(() => getShipTo(orderId), `shipTo|${orderId}`);
  return (
    <Stack gap={5}>
      <PageHeader title={`Ship-to of ${orderId}`} subtitle="The delivery address (synthetic data)" />
      <RequestView state={shipTo} what="the ship-to">
        {(s) => <ShipToBody shipTo={s} />}
      </RequestView>
      <Link to="../ship-to">← Look up another order's ship-to</Link>
    </Stack>
  );
}

function ShipToBody({ shipTo }: { shipTo: ShipTo }) {
  const lines = [shipTo.line1, shipTo.line2].filter((l): l is string => Boolean(l));
  return (
    <Card title="Address">
      <Facts
        items={[
          { label: "Name", value: shipTo.name },
          { label: "Address", value: lines.join(", ") },
          { label: "City", value: shipTo.city },
          { label: "Region", value: shipTo.region },
          { label: "Postal code", value: shipTo.postalCode },
          { label: "Country", value: shipTo.countryCode },
          ...(shipTo.phone ? [{ label: "Phone", value: shipTo.phone }] : []),
        ]}
      />
    </Card>
  );
}
