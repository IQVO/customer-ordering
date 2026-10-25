import { NO_VALUE } from "@warehouse/ui-kit";

/** "2026-10-24 10:00:00Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 19)}Z`;
}

/** The promise window of a group as one label: "earliest – latest". */
export function windowLabel(earliest: string | undefined, latest: string | undefined): string {
  return `${utcLabel(earliest)} – ${utcLabel(latest)}`;
}

/** The id rule the service's shared.ValidID enforces, mirrored client-side:
 *  1..64 characters, no whitespace, control characters or "/". */
export function orderIdProblem(id: string): string | null {
  if (id.length === 0) return "Enter an order id.";
  if (id.length > 64) return "An order id is at most 64 characters.";
  // eslint-disable-next-line no-control-regex
  if (/[\s/\u0000-\u001f\u007f]/.test(id)) return "An order id cannot contain spaces, control characters or '/'.";
  return null;
}

/** Pluralised line label: "lines 1, 2". */
export function lineNosLabel(lineNos: number[] | undefined): string {
  if (!lineNos || lineNos.length === 0) return NO_VALUE;
  return `line${lineNos.length === 1 ? "" : "s"} ${lineNos.join(", ")}`;
}
