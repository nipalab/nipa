import { useCallback, useEffect, useState } from "react";
import { readSetting, writeSetting } from "@/lib/utils";

export const LAYOUT_PREFIX = "nipa.layout.";

const RESERVED_MAIN = 320;

export type WidthBounds = { initial: number; min: number; max: number };

const viewportWidth = () => (typeof window === "undefined" ? Infinity : window.innerWidth);

export function clampWidth(value: number, min: number, max: number, viewport = viewportWidth()): number {
  const upper = Math.max(min, Math.min(max, viewport - RESERVED_MAIN));
  return Math.round(Math.min(upper, Math.max(min, value)));
}

function load(key: string, initial: number): number {
  const stored = Number(readSetting(LAYOUT_PREFIX + key));
  return Number.isFinite(stored) && stored > 0 ? stored : initial;
}

export function useResizableWidth(key: string, bounds: WidthBounds) {
  const { initial, min, max } = bounds;
  const [requested, setRequested] = useState(() => load(key, initial));
  const [viewport, setViewport] = useState(viewportWidth);

  useEffect(() => {
    const onResize = () => setViewport(viewportWidth());
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  const preview = useCallback((w: number) => setRequested(clampWidth(w, min, max)), [min, max]);
  const commit = useCallback(
    (w: number) => {
      const next = clampWidth(w, min, max);
      setRequested(next);
      writeSetting(LAYOUT_PREFIX + key, String(next));
    },
    [key, min, max],
  );
  const reset = useCallback(() => {
    setRequested(initial);
    writeSetting(LAYOUT_PREFIX + key, null);
  }, [key, initial]);

  return { width: clampWidth(requested, min, max, viewport), min, max: clampWidth(max, min, max, viewport), preview, commit, reset };
}
