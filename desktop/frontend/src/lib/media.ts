// mediaPrefix is where the desktop app serves the files the viewers prepared
// (service.MediaPrefix in Go).
export const mediaPrefix = "/nipa-media/";

export type Fetcher = (url: string, init?: { signal?: AbortSignal }) => Promise<Pick<Response, "ok" | "status" | "arrayBuffer">>;

// fetchBytes reads a prepared media file; a missing one (evicted, or the app
// restarted) fails with its HTTP status.
export async function fetchBytes(url: string, signal?: AbortSignal, fetcher: Fetcher = fetch): Promise<ArrayBuffer> {
  const res = await fetcher(url, { signal });
  if (!res.ok) throw new Error(`the file could not be loaded (HTTP ${res.status})`);
  return res.arrayBuffer();
}

// mediaExpired asks whether any of the tokens was evicted; a failed check
// counts as still available, so the original error is shown.
export async function mediaExpired(tokens: (string | null | undefined)[], hasMedia: (token: string) => Promise<boolean>): Promise<boolean> {
  const live = tokens.filter((t): t is string => !!t);
  try {
    const alive = await Promise.all(live.map((t) => hasMedia(t)));
    return alive.some((a) => !a);
  } catch {
    return false;
  }
}
