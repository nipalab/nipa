import type { MaintenanceWindow } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";
import { translate } from "@/i18n";

export type { MaintenanceWindow };

const rank: Record<string, number> = { read_only: 1, full: 2, frozen: 3 };

export function activeWindow(windows: MaintenanceWindow[]): MaintenanceWindow | undefined {
  let best: MaintenanceWindow | undefined;
  for (const w of windows) {
    if (w.active && (rank[w.mode] ?? 0) > (best ? (rank[best.mode] ?? 0) : 0)) best = w;
  }
  return best;
}

export function upcomingWindow(windows: MaintenanceWindow[]): MaintenanceWindow | undefined {
  return windows.filter((w) => !w.active).sort((a, b) => a.startsAt.localeCompare(b.startsAt))[0];
}

export const blocksSync = (w?: MaintenanceWindow): boolean => w?.mode === "full" || w?.mode === "frozen";

export function modeLabel(mode: string): string {
  if (mode === "read_only") return translate("maintenance.readOnly");
  if (mode === "full") return translate("maintenance.closed");
  return translate("maintenance.frozen");
}

export function modeDescription(mode: string): string {
  if (mode === "read_only") return translate("maintenance.readOnlyHelp");
  if (mode === "full") return translate("maintenance.closedHelp");
  return translate("maintenance.frozenHelp");
}

export function scopeName(w: MaintenanceWindow): string {
  if (w.scope === "project") return translate("maintenance.project", { name: w.project });
  if (w.scope === "org") return translate("maintenance.org", { name: w.org });
  return translate("maintenance.server");
}

export function formatLocal(iso: string): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function headline(w: MaintenanceWindow): string {
  const values = { scope: scopeName(w), mode: modeLabel(w.mode) };
  return w.active ? translate("maintenance.isNow", values) : translate("maintenance.willBe", { ...values, start: formatLocal(w.startsAt) });
}

export function blockedNote(w: MaintenanceWindow): string {
  return translate("maintenance.unavailable", { scope: scopeName(w), mode: modeLabel(w.mode) });
}

export function maintenanceOf(err: unknown): MaintenanceWindow | undefined {
  if (typeof err !== "object" || err === null) return undefined;
  const cause = (err as { cause?: unknown }).cause;
  if (typeof cause !== "object" || cause === null) return undefined;
  const w = (cause as { maintenance?: MaintenanceWindow }).maintenance;
  return w && typeof w.mode === "string" ? { ...w, active: true } : undefined;
}

export function maintenanceText(w: MaintenanceWindow): string {
  const parts = [headline({ ...w, active: true })];
  if (w.message) parts.push(w.message);
  if (w.endsAt) parts.push(translate("maintenance.estimatedEnd", { end: formatLocal(w.endsAt) }));
  return parts.join(" · ");
}
