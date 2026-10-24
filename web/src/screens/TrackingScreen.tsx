import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { getOrderTracking } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { Facts, GroupStatusPill, OrderStatusPill, PackageTable, PageHeader, Stack } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { orderIdProblem, utcLabel, windowLabel } from "../lib";
import type { Tracking, TrackingGroup } from "../types";

/** `tracking`: a lookup by order id, then `tracking/:orderId`. */
export function TrackingLookupScreen() {
  const [orderId, setOrderId] = useState("");
  const navigate = useNavigate();
  const id = orderId.trim();
  const problem = id === "" ? null : orderIdProblem(id);
  const go = () => {
    if (id !== "" && problem === null) navigate(encodeURIComponent(id));
  };
  return (
    <Stack gap={5}>
      <PageHeader title="Tracking" subtitle="How an order is travelling: each group's promise, packages and scans" />
      <Card title="Look up an order's tracking">
        <Form label="Tracking lookup" onSubmit={go}>
          <FormRow>
            <TextField
              label="Order"
              value={orderId}
              onChange={setOrderId}
              placeholder="ord-3f9c1a2b"
              hint={problem ?? "Tracking exists from the moment the order is placed"}
            />
            <SubmitButton disabled={id === "" || problem !== null}>View tracking</SubmitButton>
          </FormRow>
        </Form>
      </Card>
    </Stack>
  );
}

/** The route element of `tracking/:orderId`. */
export function TrackingRoute() {
  const { orderId } = useParams();
  return orderId ? <TrackingScreen orderId={orderId} /> : null;
}

/**
 * The tracking view: GET /orders/{orderId}/tracking -- per shipment group the
 * promise window, lock and ship times, the at-risk / unsourceable signals and
 * the promise verdict, the packages with their scans, and the lost/returned
 * outcome of a group that got one.
 */
export function TrackingScreen({ orderId }: { orderId: string }) {
  const tracking = useRequest(() => getOrderTracking(orderId), `tracking|${orderId}`);
  return (
    <Stack gap={5}>
      <PageHeader title={`Tracking ${orderId}`} subtitle="The OrderTrackingView per shipment group and package" />
      <RequestView state={tracking} what="the tracking">
        {(t) => <TrackingBody tracking={t} />}
      </RequestView>
      <Link to="../tracking">← Look up another order's tracking</Link>
    </Stack>
  );
}

function TrackingBody({ tracking }: { tracking: Tracking }) {
  return (
    <>
      <Card title="Order">
        <Facts
          items={[
            { label: "Status", value: <OrderStatusPill status={tracking.status} /> },
            { label: "Groups", value: String(tracking.groups.length) },
          ]}
        />
      </Card>
      {tracking.groups.map((g) => (
        <TrackingGroupCard key={g.groupNo} group={g} />
      ))}
    </>
  );
}

function TrackingGroupCard({ group }: { group: TrackingGroup }) {
  return (
    <Card title={`Group ${group.groupNo}`}>
      <Stack gap={3}>
        <Facts
          items={[
            { label: "Status", value: <GroupStatusPill status={group.status} /> },
            { label: "Promise", value: group.promiseId },
            { label: "Deliver by", value: windowLabel(group.deliverByEarliest, group.deliverByLatest) },
            ...(group.promiseVerdict ? [{ label: "Promise verdict", value: group.promiseVerdict }] : []),
            ...(group.lateByMinutes !== undefined ? [{ label: "Late by", value: `${group.lateByMinutes} min` }] : []),
            ...(group.atRiskReason ? [{ label: "At risk", value: group.atRiskReason }] : []),
            ...(group.unsourceableReason ? [{ label: "Unsourceable", value: group.unsourceableReason }] : []),
            ...(group.lockedAt ? [{ label: "Locked at", value: utcLabel(group.lockedAt) }] : []),
            ...(group.shippedAt ? [{ label: "Shipped at", value: utcLabel(group.shippedAt) }] : []),
            ...(group.lostAt ? [{ label: "Lost at", value: utcLabel(group.lostAt) }] : []),
            ...(group.returnedAt ? [{ label: "Returned at", value: utcLabel(group.returnedAt) }] : []),
            ...(group.outcomePackage ? [{ label: "Outcome package", value: group.outcomePackage }] : []),
            ...(group.cancelRefusedReason ? [{ label: "Cancel refused", value: group.cancelRefusedReason }] : []),
          ]}
        />
        {group.packages.length > 0 ? <PackageTable packages={group.packages} /> : "No package has been labelled for this group yet."}
      </Stack>
    </Card>
  );
}
