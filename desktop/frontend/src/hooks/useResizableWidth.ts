import { useCallback, useEffect, useState } from "react";
import { readSetting, writeSetting } from "@/lib/utils";

export const LAYOUT_PREFIX = "nipa.layout.";

export const RESERVED_MAIN = 320;

export type WidthBounds = { initial: number; min: number; max: number };

const viewportWidth = () => (typeof window === "undefined" ? Infinity : window.innerWidth);

export function clampWidth(value: number, min: number, max: number, container = viewportWidth()): number {
  const upper = Math.max(min, Math.min(max, container - RESERVED_MAIN));
  return Math.round(Math.min(upper, Math.max(min, value)));
}

function load(key: string, initial: number): number {
  const stored = Number(readSetting(LAYOUT_PREFIX + key));
  return Number.isFinite(stored) && stored > 0 ? stored : initial;
}

export function useResizableWidth(key: string, bounds: WidthBounds) {
  const { initial, min, max } = bounds;
  const [requested, setRequested] = useState(() => load(key, initial));
  const [pane, setPane] = useState<HTMLElement | null>(null);
  const [container, setContainer] = useState(viewportWidth);

  useEffect(() => {
    const parent = pane?.parentElement;
    const measure = () => setContainer(parent ? parent.clientWidth : viewportWidth());
    measure();
    window.addEventListener("resize", measure);
    const observer = parent && typeof ResizeObserver !== "undefined" ? new ResizeObserver(measure) : null;
    if (parent) observer?.observe(parent);
    return () => {
      window.removeEventListener("resize", measure);
      observer?.disconnect();
    };
  }, [pane]);

  const preview = useCallback((w: number) => setRequested(clampWidth(w, min, max, container)), [min, max, container]);
  const commit = useCallback(
    (w: number) => {
      const next = clampWidth(w, min, max, container);
      setRequested(next);
      writeSetting(LAYOUT_PREFIX + key, String(next));
    },
    [key, min, max, container],
  );
  const reset = useCallback(() => {
    setRequested(initial);
    writeSetting(LAYOUT_PREFIX + key, null);
  }, [key, initial]);

  return {
    ref: setPane,
    width: clampWidth(requested, min, max, container),
    min,
    max: clampWidth(max, min, max, container),
    preview,
    commit,
    reset,
  };
}
