// Interface settings live in the app's desktop.json through the Go service:
// the webview's localStorage is not kept across restarts on every platform.
// hydrateSettings loads them before the first render (or gives up waiting
// after a timeout and keeps loading in the background); reads are then served
// from memory and writes are saved back after a short delay. Until the stored
// state arrives, writes go to localStorage and are replayed over it.

type Load = () => Promise<Record<string, string> | null>;
type Save = (all: Record<string, string>) => Promise<unknown>;
type HydrateOptions = { timeoutMs?: number; retries?: number; retryDelayMs?: number };

const PREFIX = "nipa.";
export const SAVE_DELAY_MS = 300;
// Mirrors the service limits (internal/service/uistate.go), so one oversized
// value can never make every save fail.
export const MAX_ENTRIES = 5000;
export const MAX_ENTRY_BYTES = 64 << 10;

let cache: Map<string, string> | null = null;
let saver: Save | null = null;
let timer: ReturnType<typeof setTimeout> | undefined;
let pending = new Map<string, string | null>();
let removed: string[] = [];
let listening = false;

const encoder = new TextEncoder();

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
  if (!saver || !cache) return;
  clearTimeout(timer);
  timer = setTimeout(() => void flushSettings(), SAVE_DELAY_MS);
}

function adopt(stored: Record<string, string>, save: Save) {
  const next = new Map(Object.entries(stored));
  let changed = false;
  if (next.size === 0) {
    for (const key of localKeys()) {
      const value = localGet(key);
      if (value !== null) next.set(key, value);
    }
    changed = next.size > 0;
  } else {
    for (const prefix of removed) {
      for (const key of [...next.keys()]) {
        if (key.startsWith(prefix)) {
          next.delete(key);
          changed = true;
        }
      }
    }
    for (const [key, value] of pending) {
      if (value === null) next.delete(key);
      else next.set(key, value);
      changed = true;
    }
  }
  pending = new Map();
  removed = [];
  cache = next;
  saver = save;
  if (changed) schedule();
  if (!listening && typeof window !== "undefined") {
    listening = true;
    window.addEventListener("pagehide", () => void flushSettings());
  }
}

export function hydrateSettings(load: Load, save: Save, opts: HydrateOptions = {}): Promise<void> {
  const { timeoutMs = 2000, retries = 5, retryDelayMs = 1000 } = opts;
  return new Promise((resolve) => {
    const wait = setTimeout(resolve, timeoutMs);
    const done = () => {
      clearTimeout(wait);
      resolve();
    };
    const attempt = (n: number) =>
      load().then(
        (stored) => {
          adopt(stored ?? {}, save);
          done();
        },
        () => {
          if (n >= retries) return done();
          setTimeout(() => void attempt(n + 1), retryDelayMs * 2 ** n);
        },
      );
    void attempt(0);
  });
}

// savable drops entries the service would refuse, so the rest still saves.
function savable(entries: Map<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  let count = 0;
  for (const [key, value] of entries) {
    if (encoder.encode(key).length + encoder.encode(value).length > MAX_ENTRY_BYTES) {
      console.warn(`Not saving interface setting ${key}: too large`);
      continue;
    }
    if (count === MAX_ENTRIES) {
      console.warn(`Not saving every interface setting: more than ${MAX_ENTRIES}`);
      break;
    }
    out[key] = value;
    count++;
  }
  return out;
}

export function flushSettings(): Promise<void> {
  clearTimeout(timer);
  timer = undefined;
  if (!cache || !saver) return Promise.resolve();
  return saver(savable(cache)).then(
    () => undefined,
    (err: unknown) => console.warn("Could not save the interface state", err),
  );
}

export function readSetting(key: string): string | null {
  if (cache) return cache.get(key) ?? null;
  return localGet(key);
}

export function writeSetting(key: string, value: string | null) {
  localSet(key, value);
  if (!cache) {
    pending.set(key, value);
    return;
  }
  if ((cache.get(key) ?? null) === value) return;
  if (value === null) cache.delete(key);
  else cache.set(key, value);
  schedule();
}

// removeSettings drops every setting whose key starts with prefix.
export function removeSettings(prefix: string) {
  for (const key of localKeys()) if (key.startsWith(prefix)) localSet(key, null);
  if (!cache) {
    removed.push(prefix);
    for (const key of [...pending.keys()]) if (key.startsWith(prefix)) pending.delete(key);
    return;
  }
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
  pending = new Map();
  removed = [];
}
