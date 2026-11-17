import { useSyncExternalStore } from "react";
import { desktop } from "@/api/desktop";
import { readSetting, writeSetting } from "@/lib/settings";

const ZOOM_KEY = "nipa.zoom";

export const ZOOM_STEPS = [0.5, 0.67, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2] as const;

const listeners = new Set<() => void>();
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

const nearest = (level: number): number => ZOOM_STEPS.reduce((best, z) => (Math.abs(z - level) < Math.abs(best - level) ? z : best), 1);

export function storedZoom(): number {
  const v = Number(readSetting(ZOOM_KEY));
  return Number.isFinite(v) && v > 0 ? nearest(v) : 1;
}

export function nextZoom(level: number, direction: 1 | -1): number {
  const i = ZOOM_STEPS.indexOf(nearest(level) as (typeof ZOOM_STEPS)[number]);
  return ZOOM_STEPS[Math.max(0, Math.min(ZOOM_STEPS.length - 1, i + direction))];
}

export const formatZoom = (level: number): string => `${Math.round(level * 100)}%`;

export function setZoom(level: number): number {
  const next = nearest(level);
  writeSetting(ZOOM_KEY, next === 1 ? null : String(next));
  void desktop.setZoom(next).catch(() => {});
  listeners.forEach((l) => l());
  return next;
}

// applyStoredZoom restores the saved zoom at startup (macOS ignores the
// window's initial zoom option).
export function applyStoredZoom() {
  const level = storedZoom();
  if (level !== 1) void desktop.setZoom(level).catch(() => {});
}

export const useZoom = (): number => useSyncExternalStore(subscribe, storedZoom);
