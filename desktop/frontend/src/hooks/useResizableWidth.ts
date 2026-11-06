import { useCallback, useState } from "react";
import { readSetting, writeSetting } from "@/lib/utils";

export const LAYOUT_PREFIX = "nipa.layout.";

const RESERVED_MAIN = 320;

export type WidthBounds = { initial: number; min: number; max: number };

export function clampWidth(value: number, min: number, max: number): number {
  const viewport = typeof window === "undefined" ? Infinity : window.innerWidth - RESERVED_MAIN;
  const upper = Math.max(min, Math.min(max, viewport));
  return Math.round(Math.min(upper, Math.max(min, value)));
}

function load(key: string, { initial, min, max }: WidthBounds): number {
  const stored = Number(readSetting(LAYOUT_PREFIX + key));
  return clampWidth(Number.isFinite(stored) && stored > 0 ? stored : initial, min, max);
}

export function useResizableWidth(key: string, bounds: WidthBounds) {
  const { initial, min, max } = bounds;
  const [width, setWidth] = useState(() => load(key, bounds));

  const preview = useCallback((w: number) => setWidth(clampWidth(w, min, max)), [min, max]);
  const commit = useCallback(
    (w: number) => {
      const next = clampWidth(w, min, max);
      setWidth(next);
      writeSetting(LAYOUT_PREFIX + key, String(next));
    },
    [key, min, max],
  );
  const reset = useCallback(() => {
    setWidth(clampWidth(initial, min, max));
    writeSetting(LAYOUT_PREFIX + key, null);
  }, [key, initial, min, max]);

  return { width, min, max: clampWidth(max, min, max), preview, commit, reset };
}
