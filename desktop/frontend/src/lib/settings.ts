// Interface settings live in the app's desktop.json through the Go service:
// the webview's localStorage is not kept across restarts on every platform.
// hydrateSettings loads them once before the first render; reads are then
// served from memory and writes are saved back after a short delay.

type Load = () => Promise<Record<string, string> | null>;
type Save = (all: Record<string, string>) => Promise<unknown>;

const PREFIX = "nipa.";
export const SAVE_DELAY_MS = 300;

let cache: Map<string, string> | null = null;
let saver: Save | null = null;
let timer: ReturnType<typeof setTimeout> | undefined;

function localGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function localSet(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable; the stored copy still has the value.
  }
}

function localKeys(): string[] {
  try {
    return Object.keys(localStorage).filter((k) => k.startsWith(PREFIX));
  } catch {
    return [];
  }
}

function schedule() {
  if (!saver) return;
  clearTimeout(timer);
  timer = setTimeout(() => void flushSettings(), SAVE_DELAY_MS);
}

export async function hydrateSettings(load: Load, save: Save): Promise<void> {
  let stored: Record<string, string>;
  try {
    stored = (await load()) ?? {};
  } catch {
    return;
  }
  cache = new Map(Object.entries(stored));
  saver = save;
  if (cache.size === 0) {
    for (const key of localKeys()) {
      const value = localGet(key);
      if (value !== null) cache.set(key, value);
    }
    if (cache.size > 0) schedule();
  }
  if (typeof window !== "undefined") window.addEventListener("pagehide", () => void flushSettings());
}

export function flushSettings(): Promise<void> {
  clearTimeout(timer);
  timer = undefined;
  if (!cache || !saver) return Promise.resolve();
  return saver(Object.fromEntries(cache)).then(
    () => undefined,
    () => undefined,
  );
}

export function readSetting(key: string): string | null {
  if (cache) return cache.get(key) ?? null;
  return localGet(key);
}

export function writeSetting(key: string, value: string | null) {
  localSet(key, value);
  if (!cache) return;
  if ((cache.get(key) ?? null) === value) return;
  if (value === null) cache.delete(key);
  else cache.set(key, value);
  schedule();
}

// removeSettings drops every setting whose key starts with prefix.
export function removeSettings(prefix: string) {
  for (const key of localKeys()) if (key.startsWith(prefix)) localSet(key, null);
  if (!cache) return;
  let changed = false;
  for (const key of [...cache.keys()]) {
    if (key.startsWith(prefix)) {
      cache.delete(key);
      changed = true;
    }
  }
  if (changed) schedule();
}

// repoSetting names a setting kept per clone.
export const repoSetting = (root: string, name: string) => `${PREFIX}repo:${root}:${name}`;
export const repoSettingsPrefix = (root: string) => `${PREFIX}repo:${root}:`;

export function resetSettingsForTest() {
  clearTimeout(timer);
  timer = undefined;
  cache = null;
  saver = null;
}
