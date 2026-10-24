import type { ReactNode } from "react";
import { DataTable, StatusPill } from "@warehouse/ui-kit";
import type { GroupStatus, OrderLine, OrderStatus, TrackingPackage } from "../types";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

type Tone = "neutral" | "progress" | "success" | "warning" | "danger";

/** The order lifecycle is customer-ordering's own, so StatusPill's tone
 *  override carries it. */
const ORDER_TONE: Record<OrderStatus, Tone> = {
  PLACED: "neutral",
  IN_FULFILLMENT: "progress",
  PARTIALLY_SHIPPED: "progress",
  SHIPPED: "progress",
  PARTIALLY_DELIVERED: "progress",
  DELIVERED: "success",
  LOST: "danger",
  RETURNED: "warning",
  CANCELLED: "neutral",
};

const GROUP_TONE: Record<GroupStatus, Tone> = {
  PROMISED: "neutral",
  LOCKED: "progress",
  SHIPPED: "progress",
  DELIVERED: "success",
  LOST: "danger",
  RETURNED: "warning",
  CANCELLATION_REQUESTED: "warning",
  CANCELLED: "neutral",
};

export function OrderStatusPill({ status }: { status: OrderStatus }) {
  return <StatusPill status={status} tone={ORDER_TONE[status]} size="sm" />;
}

export function GroupStatusPill({ status }: { status: GroupStatus }) {
  return <StatusPill status={status} tone={GROUP_TONE[status]} size="sm" />;
}

/** The cart lines of an order. */
export function LineTable({ lines }: { lines: OrderLine[] }) {
  return (
    <DataTable
      rowKey={(l) => String(l.lineNo)}
      rows={lines}
      columns={[
        { key: "lineNo", header: "Line", align: "right", render: (l) => String(l.lineNo) },
        { key: "item", header: "Catalog item", render: (l) => l.catalogItemId },
        { key: "qty", header: "Quantity", align: "right", render: (l) => String(l.quantity) },
      ]}
    />
  );
}

/** The packages of one tracking group. */
export function PackageTable({ packages }: { packages: TrackingPackage[] }) {
  return (
    <DataTable
      rowKey={(p) => p.trackingNumber}
      rows={packages}
      columns={[
        { key: "tracking", header: "Tracking number", render: (p) => p.trackingNumber },
        { key: "carrier", header: "Carrier", render: (p) => p.carrier || "—" },
        { key: "state", header: "State", render: (p) => p.state || "—" },
        { key: "scan", header: "Last scan node", render: (p) => p.lastScanNodeId || "—" },
        { key: "exception", header: "Exception", render: (p) => p.exceptionReason || "—" },
      ]}
    />
  );
}
