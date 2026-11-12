import type { FileEntry, FileLock, RemoteDir } from "@/api/desktop";
import { isCovered } from "@/lib/sparse";
import { readSetting, writeSetting } from "@/lib/utils";

export type SortKey = "name" | "kind" | "size" | "status" | "lock";
export type Sort = { key: SortKey; desc: boolean };
export type ViewMode = "list" | "grid";
export type LockState = "mine" | "theirs" | "";

// Row is what the browser lists: a local entry, or a folder that only exists
// on the server because the clone's sparse checkout leaves it out.
export type Row = FileEntry & { remote?: boolean };

const VIEW_KEY = "nipa.files.view";

export const readViewMode = (): ViewMode => (readSetting(VIEW_KEY) === "grid" ? "grid" : "list");
export const writeViewMode = (mode: ViewMode) => writeSetting(VIEW_KEY, mode);

export const parentOf = (path: string): string => {
  const i = path.lastIndexOf("/");
  return i < 0 ? "" : path.slice(0, i);
};

export const baseName = (path: string): string => path.slice(path.lastIndexOf("/") + 1);

// ancestors lists every folder above path, root ("") first.
export function ancestors(path: string): string[] {
  const out = [""];
  const parts = path.split("/").filter(Boolean);
  for (let i = 1; i < parts.length; i++) out.push(parts.slice(0, i).join("/"));
  return out;
}

const covers = (prefix: string, path: string) => path === prefix || path.startsWith(`${prefix}/`);

// lockFor returns the in-scope lock covering path, the most specific first.
export function lockFor(path: string, locks: FileLock[]): FileLock | undefined {
  let best: FileLock | undefined;
  for (const l of locks) {
    const at = l.path.replace(/\/+$/, "");
    if (!l.inScope || !covers(at, path)) continue;
    if (!best || at.length > best.path.replace(/\/+$/, "").length) best = l;
  }
  return best;
}

export const lockState = (lock?: FileLock): LockState => (lock ? (lock.mine ? "mine" : "theirs") : "");

const statusRank: Record<string, number> = { conflict: 0, missing: 1, deleted: 2, modified: 3, added: 4, untracked: 5 };
const lockRank: Record<LockState, number> = { theirs: 0, mine: 1, "": 2 };

const byName = (a: Row, b: Row) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" });

// sortEntries keeps folders first and sorts each group by the chosen column,
// falling back to the name.
export function sortEntries(rows: Row[], sort: Sort, locks: FileLock[]): Row[] {
  const key = (r: Row): number | string => {
    switch (sort.key) {
      case "kind":
        return r.isDir ? "" : r.kind;
      case "size":
        return r.isDir ? 0 : r.size;
      case "status":
        return statusRank[r.status] ?? 9;
      case "lock":
        return lockRank[lockState(lockFor(r.path, locks))];
      default:
        return "";
    }
  };
  const dir = sort.desc ? -1 : 1;
  return [...rows].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    const ka = key(a);
    const kb = key(b);
    if (ka !== kb) return (ka < kb ? -1 : 1) * dir;
    return byName(a, b) * (sort.key === "name" ? dir : 1);
  });
}

export const nextSort = (sort: Sort, key: SortKey): Sort => (sort.key === key ? { key, desc: !sort.desc } : { key, desc: false });

// needsRemote says whether listing dir must ask the server for folders the
// sparse checkout leaves out: never for a full checkout or inside the set.
export const needsRemote = (dir: string, sparse: string[]): boolean => sparse.length > 0 && !isCovered(dir, sparse);

// remoteOnly turns the server's folders of dir that the clone does not have
// into greyed rows.
export function remoteOnly(dirs: RemoteDir[], local: FileEntry[], sparse: string[]): Row[] {
  const have = new Set(local.map((e) => e.name));
  return dirs
    .filter((d) => !have.has(d.name) && !isCovered(d.path, sparse))
    .map((d) => ({
      name: d.name,
      path: d.path,
      isDir: true,
      size: 0,
      binary: false,
      kind: "",
      status: "",
      staged: false,
      childrenChanged: false,
      tracked: false,
      hash: "",
      mtime: 0,
      remote: true,
    }));
}

// thumbKey identifies a file version for the thumbnail cache: the synced hash
// for clean files, the working file's stamp for changed ones.
export const thumbKey = (e: FileEntry): string => (e.status ? `${e.status}:${e.mtime}:${e.size}` : e.hash || `${e.size}`);

export const canPreview = (e: FileEntry): boolean => !e.isDir && e.kind === "image" && e.status !== "missing" && e.status !== "deleted";

const statusNames: Record<string, string> = {
  modified: "Modified",
  added: "Added",
  deleted: "Deleted",
  missing: "Missing from disk",
  untracked: "New, not tracked yet",
  conflict: "Conflict",
};

export const statusName = (status: string): string => statusNames[status] ?? "Unchanged";

const kindNames: Record<string, string> = {
  text: "Text",
  image: "Image",
  model: "3D model",
  audio: "Audio",
  video: "Video",
  font: "Font",
  table: "Table",
  scene: "Scene",
  opaque: "Binary",
};

export const kindName = (e: FileEntry): string => (e.isDir ? "Folder" : (kindNames[e.kind] ?? "File"));

// dirsToRefresh lists the folders whose listing a change notice affects:
// every folder above each changed path (their children_changed flags move).
export function dirsToRefresh(paths: string[]): string[] {
  const out = new Set<string>();
  for (const p of paths) for (const d of ancestors(p)) out.add(d);
  return [...out];
}
