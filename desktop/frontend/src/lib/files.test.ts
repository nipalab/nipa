import { describe, expect, it } from "vitest";
import type { FileEntry, FileLock } from "@/api/desktop";
import {
  ancestors,
  baseName,
  canPreview,
  hasEmbeddedThumbnail,
  dirsToRefresh,
  kindName,
  lockFor,
  lockState,
  needsRemote,
  nextSort,
  parentOf,
  readViewMode,
  remoteOnly,
  sortEntries,
  statusName,
  thumbKey,
  writeViewMode,
} from "./files";

const entry = (over: Partial<FileEntry>): FileEntry => ({
  name: "x",
  path: "x",
  isDir: false,
  size: 0,
  binary: false,
  kind: "text",
  status: "",
  staged: false,
  childrenChanged: false,
  tracked: true,
  hash: "h",
  mtime: 0,
  ...over,
});

const lock = (over: Partial<FileLock>): FileLock => ({
  id: "l",
  path: "x",
  branch: "",
  global: true,
  heldBy: "me",
  heldByName: "Me",
  mine: true,
  mergeRequest: 0,
  acquiredAt: "",
  inScope: true,
  requests: [],
  ...over,
});

describe("files helpers", () => {
  it("splits paths", () => {
    expect(parentOf("a/b/c.png")).toBe("a/b");
    expect(parentOf("c.png")).toBe("");
    expect(baseName("a/b/c.png")).toBe("c.png");
    expect(ancestors("a/b/c.png")).toEqual(["", "a", "a/b"]);
    expect(ancestors("c.png")).toEqual([""]);
    expect(dirsToRefresh(["a/b/c.png", "a/d.txt", "e"]).sort()).toEqual(["", "a", "a/b"]);
  });

  it("finds the most specific covering lock in scope", () => {
    const locks = [lock({ path: "art", heldByName: "Bob", mine: false }), lock({ path: "art/hero.psd" }), lock({ path: "art/x.png", inScope: false }), lock({ path: "artwork" })];
    expect(lockFor("art/hero.psd", locks)?.path).toBe("art/hero.psd");
    expect(lockFor("art/other.png", locks)?.path).toBe("art");
    expect(lockFor("art/x.png", locks)?.path).toBe("art");
    expect(lockFor("arts/a.png", locks)).toBeUndefined();
    expect(lockFor("cave/a.unity", [lock({ path: "cave/" })])?.path).toBe("cave/");
    expect(lockState(lockFor("art/hero.psd", locks))).toBe("mine");
    expect(lockState(lockFor("art/other.png", locks))).toBe("theirs");
    expect(lockState(undefined)).toBe("");
  });

  it("sorts folders first by any column", () => {
    const rows = [
      entry({ name: "b.png", path: "b.png", kind: "image", size: 30, status: "modified" }),
      entry({ name: "a10.txt", path: "a10.txt", size: 5 }),
      entry({ name: "a2.txt", path: "a2.txt", size: 50, status: "conflict" }),
      entry({ name: "Zdir", path: "Zdir", isDir: true }),
      entry({ name: "adir", path: "adir", isDir: true }),
    ];
    const names = (key: Parameters<typeof nextSort>[1], desc = false) => sortEntries(rows, { key, desc }, [lock({ path: "b.png", mine: false })]).map((r) => r.name);
    expect(names("name")).toEqual(["adir", "Zdir", "a2.txt", "a10.txt", "b.png"]);
    expect(names("name", true)).toEqual(["Zdir", "adir", "b.png", "a10.txt", "a2.txt"]);
    expect(names("size")).toEqual(["adir", "Zdir", "a10.txt", "b.png", "a2.txt"]);
    expect(names("size", true)).toEqual(["adir", "Zdir", "a2.txt", "b.png", "a10.txt"]);
    expect(names("kind")).toEqual(["adir", "Zdir", "b.png", "a2.txt", "a10.txt"]);
    expect(names("status")).toEqual(["adir", "Zdir", "a2.txt", "b.png", "a10.txt"]);
    expect(names("lock")).toEqual(["adir", "Zdir", "b.png", "a2.txt", "a10.txt"]);
    expect(nextSort({ key: "name", desc: false }, "name")).toEqual({ key: "name", desc: true });
    expect(nextSort({ key: "name", desc: true }, "size")).toEqual({ key: "size", desc: false });
  });

  it("lists server folders outside the sparse checkout", () => {
    expect(needsRemote("", [])).toBe(false);
    expect(needsRemote("", ["Assets/Art"])).toBe(true);
    expect(needsRemote("Assets", ["Assets/Art"])).toBe(true);
    expect(needsRemote("Assets/Art/x", ["Assets/Art"])).toBe(false);
    const rows = remoteOnly(
      [
        { name: "Assets", path: "Assets" },
        { name: "Source", path: "Source" },
        { name: "Docs", path: "Docs" },
      ],
      [entry({ name: "Assets", path: "Assets", isDir: true })],
      ["Docs"],
    );
    expect(rows.map((r) => [r.name, r.remote, r.isDir])).toEqual([["Source", true, true]]);
  });

  it("keys thumbnails and names kinds and statuses", () => {
    expect(thumbKey(entry({ hash: "abc" }))).toBe("abc");
    expect(thumbKey(entry({ hash: "", size: 9 }))).toBe("9");
    expect(thumbKey(entry({ status: "modified", mtime: 5, size: 2 }))).toBe("modified:5:2");
    expect(canPreview(entry({ kind: "image" }))).toBe(true);
    expect(canPreview(entry({ kind: "image", status: "missing" }))).toBe(false);
    expect(canPreview(entry({ kind: "text" }))).toBe(false);
    expect(canPreview(entry({ kind: "opaque", path: "Art/hero.blend" }))).toBe(true);
    expect(canPreview(entry({ kind: "opaque", path: "Content/Hero.UASSET", status: "deleted" }))).toBe(false);
    expect(hasEmbeddedThumbnail("Maps/Level.umap")).toBe(true);
    expect(hasEmbeddedThumbnail("a.png")).toBe(false);
    expect(kindName(entry({ isDir: true }))).toBe("Folder");
    expect(kindName(entry({ kind: "model" }))).toBe("3D model");
    expect(kindName(entry({ kind: "weird" }))).toBe("File");
    expect(statusName("missing")).toBe("Missing from disk");
    expect(statusName("")).toBe("Unchanged");
  });

  it("remembers the view mode", () => {
    expect(readViewMode()).toBe("list");
    writeViewMode("grid");
    expect(readViewMode()).toBe("grid");
  });
});
