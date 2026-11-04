import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return "Something went wrong";
}

export function readSetting(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeSetting(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable; settings then only last for the session.
  }
}

const PSEUDO_VERSION = /^v\d+\.\d+\.\d+-(?:\d+\.)?\d{14}-([0-9a-f]{12})(\+dirty)?$/;

// shortVersion turns Go pseudo-versions (dev builds) into dev-<sha>.
export function shortVersion(version: string): string {
  const m = PSEUDO_VERSION.exec(version);
  return m ? `dev-${m[1].slice(0, 7)}${m[2] ?? ""}` : version;
}

export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
