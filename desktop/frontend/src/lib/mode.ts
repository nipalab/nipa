import { useSyncExternalStore } from "react";
import { readSetting, writeSetting } from "@/lib/settings";

export type Mode = "simple" | "full";

const MODE_KEY = "nipa.mode";
const TOUR_KEY = "nipa.tour";

export const TOUR_STEPS = 4;

const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

export const storedMode = (): Mode | null => {
  const v = readSetting(MODE_KEY);
  return v === "simple" || v === "full" ? v : null;
};

export function setMode(mode: Mode) {
  writeSetting(MODE_KEY, mode);
  emit();
}

// useStoredMode is null until the first-run question was answered.
export const useStoredMode = (): Mode | null => useSyncExternalStore(subscribe, storedMode);

export const useMode = (): Mode => useStoredMode() ?? "full";

export const useSimple = (): boolean => useMode() === "simple";

// Tour state: null = never started, a step index while running, "done".
export type TourState = number | "done" | null;

export const readTour = (): TourState => {
  const v = readSetting(TOUR_KEY);
  if (v === "done") return "done";
  const n = v === null ? NaN : Number(v);
  return Number.isInteger(n) && n >= 0 && n < TOUR_STEPS ? n : null;
};

export function writeTour(state: TourState) {
  writeSetting(TOUR_KEY, state === null ? null : String(state));
  emit();
}

export const startTour = () => writeTour(0);

// startTourOnce begins the tour after the first clone only.
export const startTourOnce = () => {
  if (readTour() === null) startTour();
};

export const useTour = (): TourState => useSyncExternalStore(subscribe, readTour);

export const labels = (simple: boolean) => ({
  update: simple ? "Get latest" : "Update",
  push: simple ? "Submit" : "Push",
});
