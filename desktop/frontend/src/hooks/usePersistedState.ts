import { useCallback, useState, type Dispatch, type SetStateAction } from "react";
import { readSetting, writeSetting } from "@/lib/settings";

type Options<T> = {
  // valid rejects stored values that no longer fit (renamed tabs, old shapes).
  valid?: (value: unknown) => value is T;
  // prefer wins over the stored value when set, e.g. a deep link's target.
  prefer?: T;
};

function load<T>(key: string, fallback: T, valid?: (value: unknown) => value is T): T {
  const raw = readSetting(key);
  if (raw === null) return fallback;
  try {
    const parsed: unknown = JSON.parse(raw);
    return !valid || valid(parsed) ? (parsed as T) : fallback;
  } catch {
    return fallback;
  }
}

const store = (key: string, value: unknown) => writeSetting(key, value === undefined ? null : JSON.stringify(value));

// usePersistedState is useState whose value is kept under key across
// restarts and remounts.
export function usePersistedState<T>(key: string, fallback: T, opts: Options<T> = {}): [T, Dispatch<SetStateAction<T>>] {
  const [value, setValue] = useState<T>(() => {
    if (opts.prefer === undefined) return load(key, fallback, opts.valid);
    store(key, opts.prefer);
    return opts.prefer;
  });
  const set = useCallback<Dispatch<SetStateAction<T>>>(
    (next) =>
      setValue((prev) => {
        const v = typeof next === "function" ? (next as (p: T) => T)(prev) : next;
        store(key, v);
        return v;
      }),
    [key],
  );
  return [value, set];
}
