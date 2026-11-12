import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Commit, FileEntry, FileLock, LockStatus, Repo, RepoChanged } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  files: vi.fn(),
  remoteTree: vi.fn(),
  lockStatus: vi.fn(),
  lock: vi.fn(),
  unlock: vi.fn(),
  fileInfo: vi.fn(),
  fileView: vi.fn(),
  history: vi.fn(),
  imageDiff: vi.fn(),
  openFile: vi.fn(),
  revealFile: vi.fn(),
  fileLink: vi.fn(),
  onRepoChanged: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { FilesView } from "./FilesView";

const repo: Repo = { root: "/work/game", name: "game", url: "https://nipa.test/studio/game", branch: "main", sparse: null };

const entry = (path: string, over: Partial<FileEntry> = {}): FileEntry => ({
  name: path.slice(path.lastIndexOf("/") + 1),
  path,
  isDir: false,
  size: 100,
  binary: false,
  kind: "text",
  status: "",
  staged: false,
  childrenChanged: false,
  tracked: true,
  hash: `h-${path}`,
  mtime: 0,
  ...over,
});
const folder = (path: string, over: Partial<FileEntry> = {}) => entry(path, { isDir: true, kind: "", hash: "", ...over });

const listings: Record<string, FileEntry[]> = {
  "": [folder("Assets", { childrenChanged: true }), folder("Source"), entry("README.md", { size: 2048 })],
  Assets: [
    folder("Assets/Audio"),
    entry("Assets/hero.png", { kind: "image", binary: true, size: 5000, status: "modified", mtime: 9 }),
    entry("Assets/rock.png", { kind: "image", binary: true, size: 300 }),
    entry("Assets/notes.txt", { size: 10 }),
    entry("Assets/gone.wav", { kind: "audio", binary: true, status: "missing" }),
  ],
  "Assets/Audio": [entry("Assets/Audio/theme.wav", { kind: "audio", binary: true })],
  Source: [],
};

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
  ...over,
});

const lockStatus = (): LockStatus => ({
  userId: "me",
  branch: "main",
  defaultBranch: true,
  canReleaseAny: false,
  locks: [lock({ id: "a", path: "Assets/rock.png", heldBy: "bob", heldByName: "Bob", mine: false }), lock({ id: "b", path: "Assets/notes.txt" })],
  files: [],
});

const commit = (id: string, message: string): Commit => ({ id, hash: `${id}hash`, parents: [], author: "Mina", email: "mina@x", message, date: new Date().toISOString() });

let changed: ((c: RepoChanged) => void) | undefined;
const props = { busy: false, notify: vi.fn(), onHistory: vi.fn(), onRestore: vi.fn(), onAddSparse: vi.fn() };
const view = (over: Partial<Parameters<typeof FilesView>[0]> = {}) => renderWithProviders(<FilesView repo={repo} {...props} {...over} />);
const list = () => screen.getByRole("table", { name: "Files" });
const rowNames = () => within(list()).getAllByRole("row").slice(1).map((r) => r.querySelector("[role=cell] .truncate")?.textContent);
const clipboard = { writeText: vi.fn() };

beforeEach(() => {
  vi.resetAllMocks();
  changed = undefined;
  api.files.mockImplementation((_root: string, dir: string) => Promise.resolve({ dir, entries: listings[dir] ?? [] }));
  api.lockStatus.mockResolvedValue(lockStatus());
  api.lock.mockResolvedValue({ locked: ["Assets/hero.png"], failed: [] });
  api.unlock.mockResolvedValue(undefined);
  api.fileInfo.mockImplementation((_root: string, path: string) => Promise.resolve({ entry: entry(path), lastCommit: commit("c9", "Repaint the hero") }));
  api.fileView.mockResolvedValue({ path: "x", status: "", kind: "text", old: null, new: { path: "x", mode: 420, size: 4, binary: false, hash: "" }, additions: 0, deletions: 0, hunks: [] });
  api.history.mockResolvedValue({ commits: [commit("c9", "Repaint the hero"), commit("c4", "First hero")], next: "" });
  api.imageDiff.mockResolvedValue({ old: null, new: { src: "data:image/png;base64,AAAA", hdr: false } });
  api.openFile.mockResolvedValue(undefined);
  api.revealFile.mockResolvedValue(undefined);
  api.fileLink.mockResolvedValue("nipa://nipa.test/studio/game/file/Assets/hero.png");
  api.onRepoChanged.mockImplementation((fn: (c: RepoChanged) => void) => {
    changed = fn;
    return () => {};
  });
  clipboard.writeText.mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: clipboard });
});

const openAssets = async () => {
  view();
  const tree = await screen.findByRole("navigation", { name: "Folders" });
  await userEvent.click(await within(tree).findByText("Assets"));
  await waitFor(() => expect(rowNames()).toContain("hero.png"));
};

const selectRow = async (name: string) => {
  const row = within(list()).getAllByRole("row").find((r) => r.textContent?.includes(name))!;
  await userEvent.click(row);
  return row;
};

describe("FilesView", () => {
  it("lists the root, browses folders from the tree and the breadcrumb", async () => {
    view();
    await waitFor(() => expect(rowNames()).toEqual(["Assets", "Source", "README.md"]));
    expect(screen.getByLabelText("Changes inside", { selector: "[role=row] *" })).toBeInTheDocument();
    const tree = screen.getByRole("navigation", { name: "Folders" });
    await userEvent.click(within(tree).getByText("Assets"));
    await waitFor(() => expect(rowNames()).toEqual(["Audio", "gone.wav", "hero.png", "notes.txt", "rock.png"]));
    expect(api.files).toHaveBeenCalledWith("/work/game", "Assets");
    expect(within(tree).getByText("Audio")).toBeInTheDocument();
    await userEvent.click(within(tree).getByRole("button", { name: "Collapse Assets" }));
    expect(within(tree).queryByText("Audio")).not.toBeInTheDocument();
    await userEvent.click(within(tree).getByRole("button", { name: "Expand Assets" }));
    expect(await within(tree).findByText("Audio")).toBeInTheDocument();

    fireEvent.doubleClick(within(list()).getAllByRole("row").find((r) => r.textContent?.includes("Audio"))!);
    await waitFor(() => expect(rowNames()).toEqual(["theme.wav"]));
    const crumbs = screen.getByRole("navigation", { name: "Folder path" });
    expect(within(crumbs).getByText("Audio")).toHaveAttribute("aria-current", "location");
    await userEvent.click(within(crumbs).getByText("game"));
    await waitFor(() => expect(rowNames()).toEqual(["Assets", "Source", "README.md"]));
    expect(api.remoteTree).not.toHaveBeenCalled();
  });

  it("sorts by any column, folders first", async () => {
    await openAssets();
    await userEvent.click(screen.getByRole("button", { name: "Size" }));
    expect(rowNames()).toEqual(["Audio", "notes.txt", "gone.wav", "rock.png", "hero.png"]);
    expect(screen.getByRole("columnheader", { name: "Size" })).toHaveAttribute("aria-sort", "ascending");
    await userEvent.click(screen.getByRole("button", { name: "Size" }));
    expect(rowNames()).toEqual(["Audio", "hero.png", "rock.png", "gone.wav", "notes.txt"]);
    await userEvent.click(screen.getByRole("button", { name: "Status" }));
    expect(rowNames()).toEqual(["Audio", "gone.wav", "hero.png", "notes.txt", "rock.png"]);
    await userEvent.click(screen.getByRole("button", { name: "Lock" }));
    expect(rowNames()).toEqual(["Audio", "rock.png", "notes.txt", "gone.wav", "hero.png"]);
    await userEvent.click(screen.getByRole("button", { name: "Kind" }));
    expect(rowNames()[1]).toBe("gone.wav");
    expect(within(list()).getByText("Bob")).toBeInTheDocument();
    expect(within(list()).getByText("You")).toBeInTheDocument();
  });

  it("switches to thumbnails, remembers it, and only previews images", async () => {
    await openAssets();
    await userEvent.click(screen.getByRole("button", { name: "Thumbnails" }));
    const grid = screen.getByRole("grid", { name: "Files" });
    expect(localStorage.getItem("nipa.files.view")).toBe("grid");
    await waitFor(() => expect(within(grid).getByAltText("rock.png")).toBeInTheDocument());
    expect(within(grid).getByAltText("hero.png")).toBeInTheDocument();
    expect(api.imageDiff).toHaveBeenCalledTimes(2);
    expect(api.imageDiff).toHaveBeenCalledWith("/work/game", null, { source: "working", path: "Assets/rock.png" }, { maxSize: 128 });
    expect(within(grid).getByLabelText("Locked by Bob")).toBeInTheDocument();
    await userEvent.click(within(grid).getByRole("gridcell", { name: /notes\.txt/ }));
    expect(within(grid).getByRole("gridcell", { name: /notes\.txt/ })).toHaveAttribute("aria-selected", "true");
    fireEvent.doubleClick(within(grid).getByRole("gridcell", { name: /Audio/ }));
    await waitFor(() => expect(within(screen.getByRole("grid", { name: "Files" })).getByRole("gridcell", { name: /theme\.wav/ })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "List" }));
    expect(localStorage.getItem("nipa.files.view")).toBe("list");
  });

  it("shows the selected file with its last change and versions", async () => {
    await openAssets();
    await selectRow("notes.txt");
    const detail = screen.getByRole("region", { name: "File details" });
    expect(await within(detail).findByRole("button", { name: "Repaint the hero" })).toBeInTheDocument();
    expect(within(detail).getByText("Locked by you")).toBeInTheDocument();
    await userEvent.click(within(detail).getByRole("button", { name: "Repaint the hero" }));
    expect(props.onHistory).toHaveBeenCalledWith("Assets/notes.txt", "c9");
    const versions = within(detail).getByRole("region", { name: "Versions" });
    await waitFor(() => expect(within(versions).getAllByRole("button", { name: "Get this version" })).toHaveLength(2));
    expect(api.history).toHaveBeenCalledWith("/work/game", { branch: "main", path: "Assets/notes.txt", limit: 20 });
    await userEvent.click(within(versions).getAllByRole("button", { name: "Get this version" })[1]);
    expect(props.onRestore).toHaveBeenCalledWith("Assets/notes.txt", expect.objectContaining({ id: "c4" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("asks before replacing local changes with another version", async () => {
    await openAssets();
    await selectRow("hero.png");
    const versions = await screen.findByRole("region", { name: "Versions" });
    const get = await within(versions).findAllByRole("button", { name: "Get this version" });
    expect(within(versions).getAllByRole("img")).toHaveLength(2);
    await userEvent.click(get[1]);
    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("overwrites them");
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(props.onRestore).not.toHaveBeenCalled();
    await userEvent.click(get[1]);
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Get this version" }));
    expect(props.onRestore).toHaveBeenCalledWith("Assets/hero.png", expect.objectContaining({ id: "c4" }));
  });

  it("runs the toolbar actions", async () => {
    await openAssets();
    await selectRow("hero.png");
    const bar = await screen.findByRole("toolbar", { name: "File actions" });
    await userEvent.click(within(bar).getByRole("button", { name: "Open" }));
    expect(api.openFile).toHaveBeenCalledWith("/work/game", "Assets/hero.png");
    await userEvent.click(within(bar).getByRole("button", { name: "Reveal in file manager" }));
    expect(api.revealFile).toHaveBeenCalledWith("/work/game", "Assets/hero.png");
    await userEvent.click(within(bar).getByRole("button", { name: "Lock" }));
    expect(api.lock).toHaveBeenCalledWith("/work/game", "main", ["Assets/hero.png"]);
    await userEvent.click(within(bar).getByRole("button", { name: "History" }));
    expect(props.onHistory).toHaveBeenCalledWith("Assets/hero.png");
    await userEvent.click(within(bar).getByRole("button", { name: "Copy path" }));
    expect(clipboard.writeText).toHaveBeenCalledWith("Assets/hero.png");
    await userEvent.click(within(bar).getByRole("button", { name: "Copy nipa:// link" }));
    await waitFor(() => expect(clipboard.writeText).toHaveBeenCalledWith("nipa://nipa.test/studio/game/file/Assets/hero.png"));
    expect(api.fileLink).toHaveBeenCalledWith("https://nipa.test/studio/game", "Assets/hero.png");
    expect(props.notify).toHaveBeenCalledWith("Link copied", "success");

    await selectRow("notes.txt");
    const notes = await screen.findByRole("toolbar", { name: "File actions" });
    await userEvent.click(within(notes).getByRole("button", { name: "Unlock" }));
    expect(api.unlock).toHaveBeenCalledWith("/work/game", "", "Assets/notes.txt");

    await selectRow("rock.png");
    expect(within(await screen.findByRole("toolbar", { name: "File actions" })).getByRole("button", { name: "Locked by Bob" })).toBeDisabled();
  });

  it("reports failed actions", async () => {
    api.openFile.mockRejectedValue(new Error("no app"));
    api.revealFile.mockRejectedValue(new Error("no file manager"));
    api.fileLink.mockRejectedValue(new Error("bad url"));
    clipboard.writeText.mockRejectedValue(new Error("denied"));
    await openAssets();
    fireEvent.doubleClick(within(list()).getAllByRole("row").find((r) => r.textContent?.includes("notes.txt"))!);
    await waitFor(() => expect(props.notify).toHaveBeenCalledWith("Could not open Assets/notes.txt: no app"));
    await selectRow("notes.txt");
    const bar = await screen.findByRole("toolbar", { name: "File actions" });
    await userEvent.click(within(bar).getByRole("button", { name: "Reveal in file manager" }));
    await userEvent.click(within(bar).getByRole("button", { name: "Copy nipa:// link" }));
    await userEvent.click(within(bar).getByRole("button", { name: "Copy path" }));
    await waitFor(() => expect(props.notify).toHaveBeenCalledWith("Could not copy: denied"));
    expect(props.notify).toHaveBeenCalledWith("Could not show Assets/notes.txt: no file manager");
    expect(props.notify).toHaveBeenCalledWith("Could not make a link: bad url");
  });

  it("opens a context menu on right click", async () => {
    await openAssets();
    const row = within(list()).getAllByRole("row").find((r) => r.textContent?.includes("hero.png"))!;
    fireEvent.contextMenu(row, { clientX: 20, clientY: 30 });
    const menu = screen.getByRole("menu");
    expect(within(menu).getAllByRole("menuitem").map((m) => m.textContent)).toEqual(["Open", "Reveal in file manager", "Lock", "History", "Copy path", "Copy nipa:// link"]);
    await userEvent.click(within(menu).getByRole("menuitem", { name: "History" }));
    expect(props.onHistory).toHaveBeenCalledWith("Assets/hero.png");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();

    fireEvent.contextMenu(within(list()).getAllByRole("row").find((r) => r.textContent?.includes("Audio"))!, { clientX: 5, clientY: 5 });
    expect(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Open folder" })).toBeInTheDocument();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();

    fireEvent.contextMenu(within(screen.getByRole("navigation", { name: "Folders" })).getByText("Source"), { clientX: 5, clientY: 5 });
    await userEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Open folder" }));
    await waitFor(() => expect(screen.getByText("This folder is empty.")).toBeInTheDocument());
    fireEvent.contextMenu(within(screen.getByRole("navigation", { name: "Folders" })).getByText("Assets"), { clientX: 5, clientY: 5 });
    fireEvent.mouseDown(screen.getByRole("menu"));
    expect(screen.getByRole("menu")).toBeInTheDocument();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("explains missing files and a server it cannot reach", async () => {
    api.fileInfo.mockResolvedValue({ entry: entry("Assets/gone.wav"), lastCommit: null, lastCommitError: "offline" });
    await openAssets();
    await selectRow("gone.wav");
    const detail = screen.getByRole("region", { name: "File details" });
    expect(within(detail).getByText(/missing from disk\. Get a version/)).toBeInTheDocument();
    expect(await within(detail).findByText("Not available offline")).toBeInTheDocument();
    expect(api.fileView).not.toHaveBeenCalled();
    expect(within(within(detail).getByRole("toolbar")).getByRole("button", { name: "Open" })).toBeDisabled();
  });

  it("greys out folders outside a sparse checkout and downloads them", async () => {
    api.remoteTree.mockImplementation((_s: string, _o: string, _p: string, _b: string, dir: string) =>
      Promise.resolve({ files: 0, fileBytes: 0, dirs: dir === "" ? [{ name: "Assets", path: "Assets" }, { name: "Docs", path: "Docs" }] : [] }),
    );
    api.files.mockImplementation((_root: string, dir: string) => Promise.resolve({ dir, entries: dir === "" ? [folder("Assets")] : [] }));
    view({ repo: { ...repo, sparse: ["Assets"] } });
    await waitFor(() => expect(rowNames()).toEqual(["Assets", "Docs"]));
    expect(api.remoteTree).toHaveBeenCalledWith("https://nipa.test", "studio", "game", "main", "");
    const docs = within(list()).getAllByRole("row").find((r) => r.textContent?.includes("Docs"))!;
    expect(docs).toHaveTextContent("Not downloaded");
    fireEvent.doubleClick(docs);
    expect(props.onAddSparse).toHaveBeenCalledWith("Docs");
    await userEvent.click(docs);
    expect(screen.getByText("This folder is not in your checkout.")).toBeInTheDocument();
    const tree = screen.getByRole("navigation", { name: "Folders" });
    await userEvent.click(within(tree).getByRole("button", { name: "Docs" }));
    await userEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Download this folder" }));
    expect(props.onAddSparse).toHaveBeenCalledTimes(2);
    await userEvent.click(within(tree).getByText("Assets"));
    await waitFor(() => expect(screen.getByText("This folder is empty.")).toBeInTheDocument());
    expect(api.remoteTree).toHaveBeenCalledTimes(1);
  });

  it("opens the file a link points at", async () => {
    const { rerender } = view({ open: { path: "Assets/notes.txt", seq: 1 } });
    await waitFor(() => expect(rowNames()).toContain("notes.txt"));
    expect(within(list()).getAllByRole("row").find((r) => r.textContent?.includes("notes.txt"))).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByRole("toolbar", { name: "File actions" })).toBeInTheDocument();
    rerender(<FilesView repo={repo} {...props} open={{ path: "Assets/Audio", seq: 2 }} />);
    await waitFor(() => expect(rowNames()).toEqual(["theme.wav"]));
  });

  it("refreshes the folders a change notice touches", async () => {
    await openAssets();
    api.files.mockClear();
    act(() => changed?.({ root: "/elsewhere", paths: [] }));
    act(() => changed?.({ root: "/work/game", paths: ["Assets/hero.png"] }));
    await waitFor(() => expect(api.files).toHaveBeenCalledWith("/work/game", "Assets"));
    api.files.mockClear();
    act(() => changed?.({ root: "/work/game", paths: [] }));
    await waitFor(() => expect(api.files).toHaveBeenCalled());
  });

  it("shows errors with a retry and an empty folder", async () => {
    api.files.mockRejectedValueOnce(new Error("daemon down"));
    view();
    expect(await screen.findByText("daemon down")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(rowNames()).toEqual(["Assets", "Source", "README.md"]));
  });

  it("virtualizes a folder with 10k files", async () => {
    const big = Array.from({ length: 10_000 }, (_, i) => entry(`Big/f${String(i).padStart(5, "0")}.png`, { kind: "image", binary: true }));
    api.files.mockImplementation((_root: string, dir: string) => Promise.resolve({ dir, entries: dir === "" ? [folder("Big")] : big }));
    view({ open: { path: "Big/f00000.png", seq: 1 } });
    await waitFor(() => expect(list()).toHaveAttribute("aria-rowcount", "10000"));
    const rendered = within(list()).getAllByRole("row").length;
    expect(rendered).toBeGreaterThan(5);
    expect(rendered).toBeLessThan(100);
    await userEvent.click(screen.getByRole("button", { name: "Thumbnails" }));
    const grid = screen.getByRole("grid", { name: "Files" });
    expect(grid).toHaveAttribute("aria-rowcount", String(Math.ceil(10_000 / 6)));
    expect(within(grid).getAllByRole("gridcell").length).toBeLessThan(100);
    expect(api.imageDiff.mock.calls.length).toBeLessThan(100);
  });
});
