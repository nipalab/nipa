import { useCallback, useState } from "react";
import { readSetting, writeSetting } from "@/lib/utils";
import { LAYOUT_PREFIX } from "@/hooks/useResizableWidth";

export const COLUMN_MAX = 1200;

type Widths = Record<string, number>;

function load(key: string, min: number): Widths {
  const raw = readSetting(`${LAYOUT_PREFIX}cols.${key}`);
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    const out: Widths = {};
    for (const [id, w] of Object.entries(parsed)) {
      if (typeof w === "number" && Number.isFinite(w)) out[id] = clamp(w, min);
    }
    return out;
  } catch {
    return {};
  }
}

const clamp = (w: number, min: number) => Math.round(Math.min(COLUMN_MAX, Math.max(min, w)));

export function useColumnWidths(key: string, defaults: Widths, min = 48) {
  const [stored, setStored] = useState<Widths>(() => load(key, min));
  const [loadedKey, setLoadedKey] = useState(key);
  if (loadedKey !== key) {
    setLoadedKey(key);
    setStored(load(key, min));
  }

  const save = (next: Widths) => writeSetting(`${LAYOUT_PREFIX}cols.${key}`, Object.keys(next).length ? JSON.stringify(next) : null);

  const width = useCallback((id: string, fallback = 160) => stored[id] ?? defaults[id] ?? fallback, [stored, defaults]);
  const preview = useCallback((id: string, w: number) => setStored((s) => ({ ...s, [id]: clamp(w, min) })), [min]);
  const commit = useCallback(
    (id: string, w: number) =>
      setStored((s) => {
        const next = { ...s, [id]: clamp(w, min) };
        save(next);
        return next;
      }),
    [key, min],
  );
  const reset = useCallback(
    (id: string) =>
      setStored((s) => {
        const next = { ...s };
        delete next[id];
        save(next);
        return next;
      }),
    [key],
  );

  return { width, min, max: COLUMN_MAX, preview, commit, reset };
}
