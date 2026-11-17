import type { BlameCommit, BlameLine, FileLockDetail, MaintenanceWindow, RepoActivity } from "../daemon/types";

export function nipaLink(repoUrl: string | undefined, segments: string[]): string | undefined {
  if (!repoUrl) {
    return undefined;
  }
  let url: URL;
  try {
    url = new URL(repoUrl);
  } catch {
    return undefined;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return undefined;
  }
  const [org, project] = url.pathname.split("/").filter(Boolean);
  if (!org || !project) {
    return undefined;
  }
  const parts = [org, project, ...segments.flatMap((s) => s.split("/"))].filter(Boolean).map(encodeURIComponent);
  let link = `nipa://${url.host}/${parts.join("/")}`;
  const loopback = url.hostname === "localhost" || url.hostname === "[::1]" || /^127\./.test(url.hostname);
  const defaultScheme = loopback ? "http:" : "https:";
  if (url.protocol !== defaultScheme) {
    link += `?server=${encodeURIComponent(`${url.protocol}//${url.host}`)}`;
  }
  return link;
}

export function coversPath(lockPath: string, rel: string): boolean {
  const prefix = trimSlashes(lockPath);
  return prefix === "" || rel === prefix || rel.startsWith(`${prefix}/`);
}

function trimSlashes(p: string): string {
  let start = 0;
  let end = p.length;
  while (start < end && p[start] === "/") {
    start++;
  }
  while (end > start && p[end - 1] === "/") {
    end--;
  }
  return p.slice(start, end);
}

export function coveringLock(locks: FileLockDetail[], rel: string, filter: (lock: FileLockDetail) => boolean = () => true): FileLockDetail | undefined {
  let best: FileLockDetail | undefined;
  for (const lock of locks) {
    if (filter(lock) && coversPath(lock.path, rel) && (!best || lock.path.length > best.path.length)) {
      best = lock;
    }
  }
  return best;
}

const MARKER = /^(<{7}|>{7})(\s|$)/m;

export function hasConflictMarkers(text: string): boolean {
  return MARKER.test(text);
}

export function relativeAge(iso: string, now: number): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return "";
  }
  const minutes = Math.max(0, Math.floor((now - then) / 60_000));
  if (minutes < 1) {
    return "now";
  }
  if (minutes < 60) {
    return `${minutes} min`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} h`;
  }
  const days = Math.floor(hours / 24);
  if (days < 30) {
    return `${days} d`;
  }
  if (days < 365) {
    return `${Math.floor(days / 30)} mo`;
  }
  return `${Math.floor(days / 365)} y`;
}

export interface BlameLabel {
  text: string;
  commit?: BlameCommit;
  uncommitted: boolean;
  first: boolean;
}

export const BLAME_WIDTH = 22;

export function blameLabels(lines: BlameLine[], commits: BlameCommit[], now: number): BlameLabel[] {
  const byId = new Map(commits.map((c) => [c.id, c]));
  const out: BlameLabel[] = [];
  let previous: string | undefined;
  for (const line of lines) {
    const key = line.uncommitted ? "\0uncommitted" : line.commit_id || "\0older";
    const commit = line.commit_id ? byId.get(line.commit_id) : undefined;
    let text: string;
    if (line.uncommitted) {
      text = "Not pushed";
    } else if (!commit) {
      text = "Older";
    } else {
      const age = relativeAge(commit.created_at, now);
      text = age ? `${commit.author_name} · ${age}` : commit.author_name;
    }
    if (text.length > BLAME_WIDTH) {
      text = `${text.slice(0, BLAME_WIDTH - 1)}…`;
    }
    out.push({ text, commit, uncommitted: line.uncommitted, first: key !== previous });
    previous = key;
  }
  return out;
}

export interface MaintenanceNote {
  title: string;
  tooltip: string;
}

const MODE_TEXT: Record<string, string> = {
  read_only: "read-only",
  full: "closed",
  frozen: "frozen",
};

export function maintenanceNote(activity: RepoActivity | undefined): MaintenanceNote | undefined {
  const windows = activity?.maintenance;
  if (!windows) {
    return undefined;
  }
  const scoped: [string, MaintenanceWindow | null][] = [
    ["Server", windows.server],
    ["Organization", windows.org],
    ["Project", windows.project],
  ];
  const active = scoped.filter(([, w]) => w?.active) as [string, MaintenanceWindow][];
  if (!active.length) {
    return undefined;
  }
  const rank = (mode: string) => ["read_only", "full", "frozen"].indexOf(mode);
  const [scope, window] = active.reduce((a, b) => (rank(b[1].mode) > rank(a[1].mode) ? b : a), active[0]);
  const mode = MODE_TEXT[window.mode] ?? window.mode;
  const parts = [`${scope} maintenance (${mode})`];
  if (window.message) {
    parts.push(window.message);
  }
  if (window.ends_at) {
    parts.push(`Estimated end ${new Date(window.ends_at).toLocaleString()}`);
  }
  return { title: `$(tools) ${mode}`, tooltip: parts.join("\n") };
}
