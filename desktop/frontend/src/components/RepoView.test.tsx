import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { ChangeSet, FileDiff, OpProgress, Repo, RepoChanged } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  changes: vi.fn(),
  stage: vi.fn(),
  unstage: vi.fn(),
  discard: vi.fn(),
  fileDiff: vi.fn(),
  metaWarnings: vi.fn(),
  pushChecks: vi.fn(),
  diskUsage: vi.fn(),
  cleanUp: vi.fn(),
  llmSettings: vi.fn(),
  generateCommitMessage: vi.fn(),
  lockStatus: vi.fn(),
  lock: vi.fn(),
  unlock: vi.fn(),
  pickFile: vi.fn(),
  openFile: vi.fn(),
  remoteTree: vi.fn(),
  setSparse: vi.fn(),
  push: vi.fn(),
  update: vi.fn(),
  history: vi.fn(),
  commitFiles: vi.fn(),
  tags: vi.fn(),
  branches: vi.fn(),
  switchBranch: vi.fn(),
  checkoutTag: vi.fn(),
  revert: vi.fn(),
  merge: vi.fn(),
  abortMerge: vi.fn(),
  conflicts: vi.fn(),
  conflictTexts: vi.fn(),
  conflictDiff: vi.fn(),
  resolveConflict: vi.fn(),
  mergeRequests: vi.fn(),
  onRepoChanged: vi.fn(),
  onOpProgress: vi.fn(),
  files: vi.fn(),
  fileInfo: vi.fn(),
  fileView: vi.fn(),
  restoreRevision: vi.fn(),
  imageDiff: vi.fn(),
  revealFile: vi.fn(),
  fileLink: vi.fn(),
  assetRefs: vi.fn(),
  provenance: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { describeMerge, describeRevert, RepoView } from "./RepoView";

const repo: Repo = { root: "/work/game", name: "game", url: "https://nipa.test/studio/game", branch: "main", sparse: null };

const changeSet = (over: Partial<ChangeSet> = {}): ChangeSet => ({
  branch: "main",
  staged: [{ path: "src/player.go", status: "modified", kind: "text" }],
  unstaged: [
    { path: "art/hero.png", status: "modified", kind: "image" },
    { path: "notes.txt", status: "untracked", kind: "text" },
    { path: "old.wav", status: "deleted", kind: "audio" },
  ],
  conflicts: [],
  resolved: [],
  ...over,
});

const conflictSet = (op: "merge" | "revert") => ({
  op,
  base: { id: "b1", label: op === "merge" ? "merge base" : "Add hero" },
  ours: { id: "o1", label: "main" },
  theirs: { id: "t1", label: op === "merge" ? "feature" : "before Add hero" },
  files: [
    { path: "a.txt", kind: "text", binary: false, resolved: true, viewer: "text" },
    { path: "hero.png", kind: "binary", binary: true, resolved: false, viewer: "image" },
  ],
});

const textDiff: FileDiff = {
  path: "src/player.go",
  status: "modified",
  kind: "text",
  old: { path: "src/player.go", mode: 420, size: 10, binary: false, hash: "aa" },
  new: { path: "src/player.go", mode: 420, size: 12, binary: false, hash: "bb" },
  additions: 1,
  deletions: 1,
  hunks: [
    {
      oldStart: 1,
      oldLines: 2,
      newStart: 1,
      newLines: 2,
      lines: [
        { kind: "context", old: 1, new: 1, text: "package main" },
        { kind: "removed", old: 2, new: 0, text: "var speed = 1" },
        { kind: "added", old: 0, new: 2, text: "var speed = 2", noNewline: true },
      ],
    },
  ],
};

const imageDiff: FileDiff = {
  path: "art/hero.png",
  status: "modified",
  kind: "image",
  old: { path: "art/hero.png", mode: 420, size: 2048, binary: true, hash: "0123456789abcdef" },
  new: { path: "art/hero.png", mode: 420, size: 4096, binary: true, hash: "fedcba9876543210" },
  additions: 0,
  deletions: 0,
  hunks: null,
};

const repoListeners = new Set<(c: RepoChanged) => void>();
const changed = (c: RepoChanged) => repoListeners.forEach((fn) => fn(c));
let progressed: (p: OpProgress) => void = () => {};
const notify = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.changes.mockResolvedValue(changeSet());
  api.metaWarnings.mockResolvedValue([]);
  api.pushChecks.mockResolvedValue({ violations: [], broken: [], newlyUnused: [], warnings: [], truncated: false, scanned: 0 });
  api.llmSettings.mockResolvedValue({ endpoint: "", model: "", keySaved: false, configured: false });
  api.diskUsage.mockResolvedValue({ workingFiles: 2, workingBytes: 2048, cacheObjects: 1, cacheBytes: 512, pins: [], sparse: [] });
  api.lockStatus.mockResolvedValue({ userId: "me", branch: "main", defaultBranch: true, canReleaseAny: false, locks: [], files: [] });
  api.assetRefs.mockImplementation(async (_root: string, path: string) => ({ path, asset: false, uses: [], usedBy: [], unknown: [], scanned: 0, truncated: false, partial: false }));
  api.provenance.mockResolvedValue({ enabled: false, origins: [] });
  api.fileDiff.mockImplementation(async (_root: string, path: string) => (path === "art/hero.png" ? imageDiff : path === "src/player.go" ? textDiff : null));
  repoListeners.clear();
  api.onRepoChanged.mockImplementation((fn: (c: RepoChanged) => void) => {
    repoListeners.add(fn);
    return () => repoListeners.delete(fn);
  });
  api.onOpProgress.mockImplementation((fn: (p: OpProgress) => void) => {
    progressed = fn;
    return () => {};
  });
});

const view = (r: Repo = repo) => renderWithProviders(<RepoView repo={r} removing={false} onRemove={() => {}} notify={notify} />);

const fileList = () => screen.findByRole("listbox", { name: "Changed files" });

describe("external branch switch", () => {
  it("refreshes the repository list when the live branch differs from the cached one", async () => {
    const spy = vi.spyOn(QueryClient.prototype, "invalidateQueries");
    api.changes.mockResolvedValue(changeSet({ branch: "feature" }));
    view();
    await fileList();
    await waitFor(() => expect(spy.mock.calls.some(([f]) => JSON.stringify(f?.queryKey) === '["repos"]')).toBe(true));
    spy.mockRestore();
  });

  it("leaves the repository list alone while the branch matches", async () => {
    const spy = vi.spyOn(QueryClient.prototype, "invalidateQueries");
    view();
    await fileList();
    expect(spy.mock.calls.some(([f]) => JSON.stringify(f?.queryKey) === '["repos"]')).toBe(false);
    spy.mockRestore();
  });
});

describe("Changes view", () => {
  it("lists staged and unstaged files and diffs the first one", async () => {
    view();
    const list = await fileList();
    expect(within(list).getByText("Staged")).toBeInTheDocument();
    expect(within(list).getByText("Changes")).toBeInTheDocument();
    expect(within(list).getAllByRole("option")).toHaveLength(4);
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "src/player.go");

    const table = await screen.findByRole("table", { name: "Diff of src/player.go" });
    expect(within(table).getByText("@@ -1,2 +1,2 @@")).toBeInTheDocument();
    expect(table.innerHTML).toContain("hljs-keyword");
    expect(within(table).getByTitle("No newline at end of file")).toBeInTheDocument();
    expect(screen.getByText("+1")).toBeInTheDocument();
  });

  it("switches between unified and split layouts and remembers it", async () => {
    view();
    await screen.findByRole("table");
    expect(screen.getAllByRole("row")).toHaveLength(4);
    await userEvent.click(screen.getByRole("button", { name: "Split" }));
    expect(screen.getAllByRole("row")).toHaveLength(3);
    expect(localStorage.getItem("nipa.diffLayout")).toBe("split");
    expect(screen.getByRole("button", { name: "Split" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(screen.getByRole("button", { name: "Unified" }));
    expect(localStorage.getItem("nipa.diffLayout")).toBe("unified");
  });

  it("shows metadata for binary assets and handles files with no diff", async () => {
    view();
    const list = await fileList();
    await userEvent.click(within(list).getByTitle("art/hero.png"));
    expect(await screen.findByText("Image file")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB · 0123456789")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Diff layout" })).not.toBeInTheDocument();

    await userEvent.click(within(list).getByTitle("notes.txt"));
    expect(await screen.findByText("This file matches the last synced version.")).toBeInTheDocument();
  });

  it("lists Unity .meta warnings and jumps to the file", async () => {
    api.changes.mockResolvedValue(
      changeSet({
        staged: [{ path: "Assets/hero.png", status: "added", kind: "image" }],
        unstaged: [
          { path: "Assets/hero.png.meta", status: "untracked", kind: "text" },
          { path: "Assets/old.png", status: "deleted", kind: "image" },
          { path: "Assets/x.mat.meta", status: "modified", kind: "text" },
        ],
      }),
    );
    api.metaWarnings.mockResolvedValue([
      { path: "Assets/hero.png.meta", kind: "meta-not-staged", related: "Assets/hero.png", message: "is not staged; push it together with its asset" },
      { path: "Assets/old.png.meta", kind: "orphan-meta", related: "Assets/old.png", message: "left behind after its asset was deleted; delete it too" },
      { path: "Assets/x.mat.meta", kind: "guid-changed", related: "Assets/x.mat", message: "GUID changed from a to b" },
      { path: "Assets/Props", kind: "missing-meta", related: "Assets/Props.meta", message: "folder has no .meta file" },
      { path: "Assets/y", kind: "custom", related: "Assets/z", message: "other" },
    ]);
    view();
    const toggle = await screen.findByRole("button", { name: /5 Unity .meta warnings/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    const list = await fileList();
    expect(within(list).getAllByLabelText(/is not staged/)).toHaveLength(2);
    await userEvent.click(toggle);
    const warnings = screen.getByRole("list", { name: "Unity .meta warnings" });
    expect(within(warnings).getByText(".meta not staged")).toBeInTheDocument();
    expect(within(warnings).getByText("Orphan .meta")).toBeInTheDocument();
    expect(within(warnings).getByText("GUID changed")).toBeInTheDocument();
    expect(within(warnings).getByText("Missing .meta")).toBeInTheDocument();
    expect(within(warnings).getByText("custom")).toBeInTheDocument();

    await userEvent.click(within(warnings).getByTitle("Assets/hero.png.meta"));
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "Assets/hero.png.meta");
    await userEvent.click(within(warnings).getByTitle("Assets/old.png.meta"));
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "Assets/old.png");
    await userEvent.click(within(warnings).getByTitle("Assets/Props"));
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "Assets/old.png");
    await userEvent.click(toggle);
    expect(screen.queryByRole("list", { name: "Unity .meta warnings" })).not.toBeInTheDocument();
  });

  it("shows push checks and jumps to the file", async () => {
    api.pushChecks.mockResolvedValue({
      violations: [{ path: "src/player.go", rule: "code", limit: "max_bytes", actual: "12", max: "4", message: "12 B exceeds 4 B" }],
      broken: [{ from: "notes.txt", raw: "res://gone.png", target: "", reason: "unresolved" }],
      newlyUnused: [],
      warnings: [],
      truncated: false,
      scanned: 3,
    });
    view();
    const list = await fileList();
    const checks = await screen.findByRole("region", { name: "Checks" });
    expect(screen.getByRole("button", { name: /Push anyway/ })).toBeInTheDocument();
    await userEvent.click(within(checks).getByTitle("notes.txt"));
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "notes.txt");
    expect(api.pushChecks).toHaveBeenCalledWith("/work/game");
  });

  it("moves the selection with the arrow keys", async () => {
    view();
    const list = await fileList();
    list.focus();
    await userEvent.keyboard("{ArrowDown}");
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "art/hero.png");
    await userEvent.keyboard("{ArrowUp}{ArrowUp}");
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "src/player.go");
    await userEvent.keyboard("a");
  });

  it("stages, unstages and stages everything", async () => {
    const after = changeSet({ staged: [], unstaged: [{ path: "src/player.go", status: "modified", kind: "text" }] });
    api.unstage.mockResolvedValue(after);
    api.stage.mockResolvedValue(changeSet());
    view();
    const list = await fileList();

    await userEvent.click(within(list).getByRole("checkbox", { name: "Unstage src/player.go" }));
    expect(api.unstage).toHaveBeenCalledWith("/work/game", ["src/player.go"]);
    expect(await within(list).findByRole("checkbox", { name: "Stage src/player.go" })).not.toBeChecked();
    expect(within(list).getByRole("option", { selected: true })).toHaveAttribute("title", "src/player.go");

    await userEvent.click(within(list).getByRole("checkbox", { name: "Stage src/player.go" }));
    expect(api.stage).toHaveBeenLastCalledWith("/work/game", ["src/player.go"]);
    await userEvent.click(await within(list).findByRole("button", { name: "Stage all" }));
    expect(api.stage).toHaveBeenLastCalledWith("/work/game", ["art/hero.png", "notes.txt", "old.wav"]);
    await userEvent.click(within(list).getByRole("button", { name: "Unstage all" }));
    expect(api.unstage).toHaveBeenLastCalledWith("/work/game", ["src/player.go"]);
  });

  it("reports a failed stage", async () => {
    api.stage.mockRejectedValue(new Error("art/hero.png is ignored; use -f"));
    view();
    const list = await fileList();
    await userEvent.click(within(list).getByRole("checkbox", { name: "Stage art/hero.png" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("art/hero.png is ignored; use -f"));
  });

  it("asks before discarding a change", async () => {
    api.discard.mockResolvedValue(changeSet({ unstaged: [] }));
    view();
    const list = await fileList();
    expect(within(list).queryByRole("button", { name: "Discard changes to notes.txt" })).not.toBeInTheDocument();

    await userEvent.click(within(list).getByRole("button", { name: "Discard changes to art/hero.png" }));
    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("art/hero.png goes back to the last synced version.");
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(api.discard).not.toHaveBeenCalled();

    await userEvent.click(within(list).getByRole("button", { name: "Discard changes to art/hero.png" }));
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();

    await userEvent.click(within(list).getByRole("button", { name: "Discard changes to art/hero.png" }));
    await userEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(api.discard).toHaveBeenCalledWith("/work/game", ["art/hero.png"]);
    await waitFor(() => expect(within(list).queryByTitle("art/hero.png")).not.toBeInTheDocument());
  });

  it("warns about referrers when discarding or deleting a used asset and jumps to them", async () => {
    const used = (path: string) => ({ path, asset: true, uses: [], usedBy: ["scenes/Main.tscn"], unknown: [], scanned: 3, truncated: false, partial: false });
    api.assetRefs.mockImplementation(async (_root: string, path: string) => used(path));
    api.stage.mockResolvedValue(changeSet());
    api.files.mockImplementation(async (_root: string, dir: string) => ({ dir, entries: [] }));
    api.fileInfo.mockResolvedValue({ entry: { path: "scenes/Main.tscn" }, lastCommit: null });
    api.fileView.mockResolvedValue(null);
    api.history.mockResolvedValue({ commits: [], next: "" });
    view();
    const list = await fileList();

    await userEvent.click(within(list).getByRole("button", { name: "Discard changes to art/hero.png" }));
    expect(await within(screen.getByRole("alertdialog")).findByText("Used by 1 file: scenes/Main.tscn.")).toBeInTheDocument();
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }));

    await userEvent.click(within(list).getByRole("checkbox", { name: "Stage old.wav" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Staging deletes old.wav");
    expect(dialog).toHaveTextContent("Used by 1 file: scenes/Main.tscn.");
    await userEvent.click(within(dialog).getByRole("button", { name: "Stage deletion" }));
    expect(api.stage).toHaveBeenCalledWith("/work/game", ["old.wav"]);

    const panel = await screen.findByRole("button", { name: /References & origin/ });
    await userEvent.click(panel);
    await userEvent.click(await screen.findByRole("button", { name: "scenes/Main.tscn" }));
    expect(screen.getByRole("tab", { name: /Files/ })).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(api.files).toHaveBeenCalledWith("/work/game", "scenes"));
  });

  it("refreshes when the daemon reports changes", async () => {
    view();
    await screen.findByRole("table");
    expect(api.changes).toHaveBeenCalledTimes(1);
    act(() => changed({ root: "/elsewhere", paths: null }));
    act(() => changed({ root: "/work/game", paths: ["src/player.go"] }));
    await waitFor(() => expect(api.changes).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(api.fileDiff).toHaveBeenCalledTimes(2));
    act(() => changed({ root: "/work/game", paths: null }));
    await waitFor(() => expect(api.changes).toHaveBeenCalledTimes(3));
  });

  it("shows empty, loading errors and banners", async () => {
    api.changes.mockResolvedValueOnce(changeSet({ staged: [], unstaged: [], detached: "v1.0", conflicts: ["a.txt", "b.txt"] }));
    view();
    expect(await screen.findByText("No local changes")).toBeInTheDocument();
    expect(screen.getByText("Viewing tag v1.0")).toBeInTheDocument();
    expect(screen.getByText("2 conflicted files: resolve them in the Conflicts tab.")).toBeInTheDocument();
    expect(screen.getByText("HEAD is at tag v1.0. Switch to a branch to push.")).toBeInTheDocument();
    expect(screen.getByText("Select a changed file to see what changed.")).toBeInTheDocument();
  });

  it("explains a failed status and a failed diff", async () => {
    api.changes.mockRejectedValueOnce(new Error("not watched"));
    const first = view();
    expect(await screen.findByText("not watched")).toBeInTheDocument();
    first.unmount();

    api.fileDiff.mockRejectedValue(new Error("diff is not configured"));
    view();
    expect(await screen.findByText("diff is not configured")).toBeInTheDocument();
  });
});

describe("locks on the Changes screen", () => {
  const lockSet = changeSet({
    staged: [
      { path: "art/hero.png", status: "modified", kind: "image" },
      { path: "art/sky.psd", status: "modified", kind: "image" },
    ],
    unstaged: [
      { path: "art/rock.fbx", status: "modified", kind: "model" },
      { path: "art/new.png", status: "untracked", kind: "image" },
      { path: "maps/level.umap", status: "modified", kind: "opaque" },
    ],
  });
  const files = [
    { path: "art/hero.png", state: "needed", lockPath: "", heldByName: "", tracked: true },
    { path: "art/sky.psd", state: "needed", lockPath: "", heldByName: "", tracked: true },
    { path: "art/rock.fbx", state: "mine", lockPath: "art/rock.fbx", heldByName: "Me", tracked: true },
    { path: "art/new.png", state: "free", lockPath: "", heldByName: "", tracked: false },
    { path: "maps/level.umap", state: "theirs", lockPath: "maps", heldByName: "Bob", tracked: true },
  ];

  it("marks rows and asks for locks before pushing", async () => {
    api.changes.mockResolvedValue(lockSet);
    api.lockStatus.mockResolvedValue({ userId: "me", branch: "main", defaultBranch: true, canReleaseAny: false, locks: [], files });
    api.lock.mockResolvedValue({ locked: ["art/hero.png", "art/sky.psd"], failed: [] });
    view();
    const list = await fileList();
    expect(await within(list).findByLabelText("Locked by you")).toBeInTheDocument();
    expect(within(list).getByLabelText("Locked by Bob (folder maps)")).toBeInTheDocument();
    expect(within(list).getByRole("button", { name: "Lock this file" })).toBeInTheDocument();
    expect(within(list).getAllByRole("button", { name: "Needs a lock to push: lock it" })).toHaveLength(2);

    expect(screen.getByRole("alert")).toHaveTextContent("2 staged files need a lock before you can push.");
    await userEvent.type(screen.getByRole("textbox", { name: "Push message" }), "art");
    expect(screen.getByRole("button", { name: "Push 2 files" })).toBeDisabled();

    await userEvent.click(screen.getByRole("button", { name: "Lock 2 files" }));
    await waitFor(() => expect(api.lock).toHaveBeenCalledWith("/work/game", "main", ["art/hero.png", "art/sky.psd"]));
    expect(notify).toHaveBeenCalledWith("Locked 2 files", "success");

    await userEvent.click(within(list).getByRole("button", { name: "Lock this file" }));
    await waitFor(() => expect(api.lock).toHaveBeenLastCalledWith("/work/game", "main", ["art/new.png"]));
  });

  it("blocks pushing files someone else holds", async () => {
    api.changes.mockResolvedValue(changeSet({ staged: [{ path: "maps/level.umap", status: "modified", kind: "opaque" }, { path: "art/a.psd", status: "modified", kind: "image" }], unstaged: [] }));
    api.lockStatus.mockResolvedValue({
      userId: "me",
      branch: "main",
      defaultBranch: true,
      canReleaseAny: false,
      locks: [],
      files: [files[4], { path: "art/a.psd", state: "theirs", lockPath: "art/a.psd", heldByName: "", tracked: true }],
    });
    view();
    expect(await screen.findByRole("alert")).toHaveTextContent("maps/level.umap is locked by Bob (+1 more). Unstage it or ask them to release the lock.");
    expect(screen.queryByRole("button", { name: /Lock \d file/ })).not.toBeInTheDocument();
    expect(screen.getByLabelText("Locked by someone else")).toBeInTheDocument();
  });

  it("reports files that could not be locked and keeps working offline", async () => {
    api.changes.mockResolvedValue(changeSet({ staged: [{ path: "art/hero.png", status: "modified", kind: "image" }], unstaged: [] }));
    api.lockStatus.mockResolvedValue({ userId: "me", branch: "feature", defaultBranch: false, canReleaseAny: false, locks: [], files: [files[0]] });
    api.lock.mockResolvedValueOnce({ locked: [], failed: [{ path: "art/hero.png", message: "locked by Bob" }, { path: "x", message: "y" }] });
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Lock 1 file" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not lock art/hero.png: locked by Bob (+1 more)"));
    expect(api.lock).toHaveBeenCalledWith("/work/game", "feature", ["art/hero.png"]);

    api.lock.mockRejectedValueOnce(new Error("offline"));
    await userEvent.click(screen.getByRole("button", { name: "Lock 1 file" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Locking failed: offline"));
  });

  it("does not block pushes when locks cannot be read", async () => {
    api.lockStatus.mockRejectedValue(new Error("offline"));
    view();
    await fileList();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/lock/i, { selector: "svg" })).not.toBeInTheDocument();
  });

  it("opens the Locks tab", async () => {
    api.lockStatus.mockResolvedValue({ userId: "me", branch: "main", defaultBranch: true, canReleaseAny: false, locks: [], files: [] });
    view();
    await userEvent.click(screen.getByRole("tab", { name: /Locks/ }));
    expect(await screen.findByText("Nothing is locked")).toBeInTheDocument();
  });
});

describe("push and update", () => {
  function deferred<T>() {
    let resolve!: (v: T) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
      resolve = res;
      reject = rej;
    }) as Promise<T> & { cancel?: () => void };
    return { promise, resolve, reject };
  }

  it("pushes the staged files with a message and shows progress", async () => {
    const call = deferred<{ commitId: string }>();
    api.push.mockReturnValue(call.promise);
    view();
    const push = await screen.findByRole("button", { name: "Push 1 file" });
    expect(push).toBeDisabled();

    await userEvent.type(screen.getByRole("textbox", { name: "Push message" }), "Faster player{Control>}{Enter}{/Control}");
    expect(api.push).toHaveBeenCalledWith("/work/game", "Faster player");
    act(() => progressed({ root: "/work/game", op: "push", phase: "upload", ahead: 0, objectsDone: 1, objectsTotal: 2, bytesDone: 5, bytesTotal: 10 }));
    expect(screen.getByText("Uploading 1/2 · 5 B of 10 B")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update" })).toBeDisabled();

    await act(async () => call.resolve({ commitId: "c42" }));
    expect(notify).toHaveBeenCalledWith("Pushed to main (c42)", "success");
    expect(screen.getByRole("textbox", { name: "Push message" })).toHaveValue("");
  });

  it("generates a push message and pushes it with the trailer", async () => {
    api.llmSettings.mockResolvedValue({ endpoint: "http://x/v1", model: "qwen", keySaved: false, configured: true });
    api.generateCommitMessage.mockResolvedValue({ title: "", text: "feat: faster player", model: "qwen", source: "", target: "" });
    api.push.mockResolvedValue({ commitId: "c43" });
    view();
    await userEvent.click(await screen.findByRole("button", { name: /Generate/ }));
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Push message" })).toHaveValue("feat: faster player"));
    expect(api.generateCommitMessage).toHaveBeenCalledWith("/work/game");
    expect(api.push).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Push 1 file" }));
    expect(api.push).toHaveBeenCalledWith("/work/game", "feat: faster player\n\nNipa-Generated: qwen");
  });

  it("hides Generate while AI is not configured", async () => {
    view();
    await screen.findByRole("button", { name: "Push 1 file" });
    expect(screen.queryByRole("button", { name: /Generate/ })).not.toBeInTheDocument();
  });

  it("keeps the message when a push fails", async () => {
    api.push.mockRejectedValue(new Error("binary file \"art/hero.png\" requires a lock"));
    view();
    await userEvent.type(await screen.findByRole("textbox", { name: "Push message" }), "Art pass");
    await userEvent.click(screen.getByRole("button", { name: "Push 1 file" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith('Push failed: binary file "art/hero.png" requires a lock'));
    expect(screen.getByRole("textbox", { name: "Push message" })).toHaveValue("Art pass");
  });

  it("updates, can be cancelled, and reports the result", async () => {
    const call = deferred<object>();
    call.promise.cancel = vi.fn(() => call.reject(new Error("cancelled")));
    api.update.mockReturnValueOnce(call.promise).mockResolvedValueOnce({ branch: "main", commitId: "c7" });
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Update" }));
    expect(screen.getByText("Updating…")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Cancel update" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Update cancelled"));

    await userEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Up to date with main", "success"));

    api.update.mockRejectedValueOnce(new Error("offline"));
    await userEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Update failed: offline"));
  });

  it("shows the repository details tab", async () => {
    view({ ...repo, sparse: ["art"] });
    await userEvent.click(await screen.findByRole("tab", { name: /Details/ }));
    expect(screen.getByText("https://nipa.test/studio/game")).toBeInTheDocument();
    expect(screen.getByText("art")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /History/ })).toBeEnabled();
    await userEvent.click(screen.getByRole("tab", { name: /Changes/ }));
    expect(await fileList()).toBeInTheDocument();
  });

  it("changes which folders the clone downloads", async () => {
    let finish!: (v: unknown) => void;
    api.setSparse.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    api.remoteTree.mockResolvedValue({ dirs: [{ name: "art", path: "art" }, { name: "audio", path: "audio" }], files: 0, fileBytes: 0 });
    view({ ...repo, sparse: ["art"] });
    await userEvent.click(await screen.findByRole("tab", { name: /Details/ }));
    await userEvent.click(screen.getByRole("button", { name: /Change folders/ }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByRole("radio", { name: "Only some folders" })).toBeChecked();
    expect(await within(dialog).findByRole("checkbox", { name: "art" })).toBeChecked();
    expect(api.remoteTree).toHaveBeenCalledWith("https://nipa.test", "studio", "game", "main", "");
    const apply = within(dialog).getByRole("button", { name: "Apply" });
    expect(apply).toBeDisabled();
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "art" }));
    expect(apply).toBeDisabled();
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "audio" }));
    await userEvent.click(apply);
    expect(api.setSparse).toHaveBeenCalledWith("/work/game", ["audio"]);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Change folders/ })).toBeDisabled();
    act(() => progressed({ root: "/work/game", op: "sparse", phase: "download", ahead: 0, objectsDone: 1, objectsTotal: 3, bytesDone: 0, bytesTotal: 0 }));
    expect(screen.getByText("Downloading 1/3")).toBeInTheDocument();
    await act(async () => finish({ branch: "main" }));
    expect(notify).toHaveBeenCalledWith("Checkout updated", "success");

    api.setSparse.mockRejectedValueOnce(new Error("offline"));
    await userEvent.click(screen.getByRole("button", { name: /Change folders/ }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("radio", { name: "Everything" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Apply" }));
    expect(api.setSparse).toHaveBeenLastCalledWith("/work/game", []);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Checkout change failed: offline"));

    await userEvent.click(screen.getByRole("button", { name: /Change folders/ }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await userEvent.click(screen.getByRole("button", { name: /Change folders/ }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("links a large full checkout to the folder picker", async () => {
    api.diskUsage.mockResolvedValue({ workingFiles: 9000, workingBytes: 30 * 1024 ** 3, cacheObjects: 0, cacheBytes: 0, pins: [], sparse: [] });
    api.remoteTree.mockResolvedValue({ dirs: [], files: 0, fileBytes: 0 });
    view({ ...repo, sparse: null });
    await userEvent.click(await screen.findByRole("tab", { name: /Details/ }));
    expect(await screen.findByText("30 GB")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Check out only the folders you need/ }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("cannot edit folders of a clone with an unreadable URL", async () => {
    view({ ...repo, url: "weird", sparse: null });
    await userEvent.click(await screen.findByRole("tab", { name: /Details/ }));
    await userEvent.click(screen.getByRole("button", { name: /Change folders/ }));
    expect(screen.getByText(/server address cannot be read/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

describe("history, branches and pending operations", () => {
  const commit = { id: "c2", hash: "h2", parents: ["c1"], author: "Ana", email: "ana@studio.dev", message: "Paint hero", date: "2026-11-04T09:00:00Z" };

  beforeEach(() => {
    api.history.mockResolvedValue({ commits: [commit], next: "" });
    api.commitFiles.mockResolvedValue([textDiff]);
    api.tags.mockResolvedValue([{ name: "v1", commitId: "c1", message: "", createdAt: "", createdBy: "" }]);
    api.branches.mockResolvedValue([
      { name: "main", commitId: "c2", default: true, protected: false },
      { name: "feature", commitId: "c3", default: false, protected: false },
    ]);
  });

  it("reverts a commit from History and reports conflicts", async () => {
    api.revert.mockResolvedValue({ revert: { committed: false, noChange: false, skipped: false, aborted: false, conflicts: ["a.txt"] } });
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /History/ }));
    await userEvent.click(await screen.findByRole("button", { name: /Revert/ }));
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Revert" }));
    expect(api.revert).toHaveBeenCalledWith("/work/game", { target: "c2" });
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Revert stopped: 1 file conflict. Resolve them, then continue.", "error"));
  });

  it("switches branches and checks out tags", async () => {
    api.switchBranch.mockResolvedValue({ branch: "feature", commitId: "c3" });
    api.checkoutTag.mockResolvedValue({ branch: "main", commitId: "c1" });
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /Branches/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Switch" }));
    expect(api.switchBranch).toHaveBeenCalledWith("/work/game", "feature");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Switched to feature", "success"));
    await userEvent.click(screen.getByRole("button", { name: "Check out" }));
    expect(api.checkoutTag).toHaveBeenCalledWith("/work/game", "v1");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Checked out tag v1", "success"));
  });

  it("shows the detached banner and switches back", async () => {
    api.changes.mockResolvedValue(changeSet({ detached: "v1" }));
    api.switchBranch.mockResolvedValue({ branch: "main" });
    view();
    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent("Checked out at tag v1");
    expect(screen.getByRole("button", { name: "Update" })).toBeDisabled();
    expect(screen.getByTitle(/Checked out at tag v1/)).toBeInTheDocument();
    await userEvent.click(within(banner).getByRole("button", { name: /Back to main/ }));
    expect(api.switchBranch).toHaveBeenCalledWith("/work/game", "main");
  });

  it("continues, skips or aborts a waiting revert", async () => {
    api.changes.mockResolvedValue(changeSet({ pending: "revert", pendingRemaining: 3, conflicts: ["a.txt", "b.txt", "c.txt"], resolved: ["c.txt"] }));
    api.conflicts.mockResolvedValue(conflictSet("revert"));
    api.revert.mockResolvedValue({ revert: { committed: false, noChange: false, skipped: false, aborted: true, conflicts: [] } });
    const first = view();
    let banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent("2 conflicting files. Fix them, then continue. 2 more commits to revert after this one.");
    expect(within(banner).getByRole("button", { name: "Continue" })).toBeDisabled();
    first.unmount();

    api.changes.mockResolvedValue(changeSet({ pending: "revert", pendingRemaining: 3, conflicts: ["a.txt"], resolved: ["a.txt"] }));
    view();
    banner = await screen.findByRole("status");
    expect(within(banner).queryByRole("button", { name: /Resolve conflicts/ })).not.toBeInTheDocument();
    for (const mode of ["Continue", "Skip", "Abort"]) {
      await userEvent.click(within(banner).getByRole("button", { name: mode }));
      expect(api.revert).toHaveBeenLastCalledWith("/work/game", { mode: mode.toLowerCase() });
      await waitFor(() => expect(within(banner).getByRole("button", { name: mode })).toBeEnabled());
    }
    expect(notify).toHaveBeenCalledWith("Revert aborted; the clone is back where it was", "success");
  });

  it("explains a waiting merge and a waiting revert without conflicts", async () => {
    api.changes.mockResolvedValueOnce(changeSet({ pending: "merge" }));
    const first = view();
    expect(await screen.findByRole("status")).toHaveTextContent("A merge is waiting: everything is resolved.");
    first.unmount();
    api.changes.mockResolvedValue(changeSet({ pending: "revert", pendingRemaining: 2 }));
    view();
    expect(await screen.findByRole("status")).toHaveTextContent("A revert is waiting to be continued. 1 more commit to revert after this one.");
  });

  it("opens the merge requests tab", async () => {
    api.mergeRequests.mockResolvedValue({ items: [], next: 0 });
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /Merge requests/ }));
    expect(await screen.findByText("No merge requests here yet")).toBeInTheDocument();
    expect(api.mergeRequests).toHaveBeenCalledWith("/work/game", { status: "open", search: "", after: 0 });
  });

  it("resolves a waiting merge, then finishes it", async () => {
    api.changes.mockResolvedValue(changeSet({ pending: "merge", conflicts: ["a.txt", "hero.png"], resolved: ["a.txt"] }));
    api.conflicts.mockResolvedValue(conflictSet("merge"));
    api.conflictDiff.mockResolvedValue(null);
    view();
    const banner = await screen.findByRole("status");
    await waitFor(() => expect(banner).toHaveTextContent("Merging feature into main: 1 conflicting file left to resolve."));
    expect(within(banner).getByRole("button", { name: "Finish merge" })).toBeDisabled();
    expect(screen.getByRole("tab", { name: /Conflicts/ })).toHaveTextContent("1");
    await userEvent.click(within(banner).getByRole("button", { name: /Resolve conflicts/ }));
    expect(screen.getByRole("tab", { name: /Conflicts/ })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByRole("list", { name: "Conflicted files" })).toBeInTheDocument();

    api.abortMerge.mockResolvedValue({ merge: { upToDate: false, fastForwarded: false, mergeCommitted: false, aborted: true, conflicts: [] } });
    await userEvent.click(within(banner).getByRole("button", { name: "Abort" }));
    expect(api.abortMerge).toHaveBeenCalledWith("/work/game");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Merge aborted; the clone is back at the branch head", "success"));
  });

  it("finishes a resolved merge with a merge commit message", async () => {
    api.changes.mockResolvedValue(changeSet({ pending: "merge", conflicts: ["a.txt"], resolved: ["a.txt"] }));
    api.conflicts.mockResolvedValue(conflictSet("merge"));
    api.push.mockResolvedValue({ commitId: "c9" });
    view();
    const banner = await screen.findByRole("status");
    await waitFor(() => expect(within(banner).getByRole("button", { name: "Finish merge" })).toBeEnabled());
    expect(screen.queryByRole("tab", { name: /Conflicts/ })).toHaveTextContent(/^Conflicts$/);
    await userEvent.click(within(banner).getByRole("button", { name: "Finish merge" }));
    expect(api.push).toHaveBeenCalledWith("/work/game", "Merge branch 'feature' into 'main'");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Merged and pushed to main (c9)", "success"));
    api.push.mockRejectedValueOnce(new Error("offline"));
    await userEvent.click(within(banner).getByRole("button", { name: "Finish merge" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Push failed: offline"));
  });

  it("merges a branch from the Branches tab and opens the conflicts", async () => {
    api.branches.mockResolvedValue([
      { name: "main", commitId: "c2", default: true, protected: false },
      { name: "feature", commitId: "c3", default: false, protected: false },
    ]);
    api.tags.mockResolvedValue([]);
    api.merge.mockResolvedValue({ merge: { upToDate: false, fastForwarded: false, mergeCommitted: false, aborted: false, conflicts: ["a.txt"] } });
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /Branches/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Merge into main" }));
    api.changes.mockResolvedValue(changeSet({ pending: "merge", conflicts: ["a.txt"] }));
    api.conflicts.mockResolvedValue({ ...conflictSet("merge"), files: [{ path: "a.txt", kind: "text", binary: false, resolved: false, viewer: "text" }] });
    api.conflictTexts.mockResolvedValue({ base: { text: "a\n", missing: false }, ours: { text: "b\n", missing: false }, theirs: { text: "c\n", missing: false }, working: { text: "<<<<<<< ours\nb\n=======\nc\n>>>>>>> theirs\n", missing: false } });
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Merge" }));
    expect(api.merge).toHaveBeenCalledWith("/work/game", "feature");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("The merge stopped: 1 file conflict. Resolve them, then finish the merge.", "error"));
    await waitFor(() => expect(screen.getByRole("tab", { name: /Conflicts/ })).toHaveAttribute("aria-selected", "true"));
    expect(await screen.findByRole("region", { name: "Conflict 1" })).toBeInTheDocument();
  });

  it("describes every merge outcome", () => {
    const base = { upToDate: false, fastForwarded: false, mergeCommitted: false, aborted: false, conflicts: [] as string[] };
    expect(describeMerge({ merge: { ...base, conflicts: ["a", "b"] } }, "main")[0]).toContain("2 files conflict");
    expect(describeMerge({ merge: { ...base, upToDate: true } }, "main")[0]).toBe("main already has those changes");
    expect(describeMerge({ merge: { ...base, fastForwarded: true } }, "main")[0]).toBe("Fast-forwarded main");
    expect(describeMerge({ merge: { ...base, mergeCommitted: true } }, "main")[0]).toBe("Merged and pushed to main");
    expect(describeMerge({}, "main")[0]).toBe("Merged and pushed to main");
  });

  it("describes every revert outcome", () => {
    const base = { committed: false, noChange: false, skipped: false, aborted: false, conflicts: [] as string[] };
    expect(describeRevert({ revert: { ...base, conflicts: ["a", "b"] } })[0]).toContain("2 files conflict");
    expect(describeRevert({ revert: { ...base, skipped: true } })[0]).toBe("Skipped that commit");
    expect(describeRevert({ revert: { ...base, noChange: true } })[0]).toContain("Nothing to revert");
    expect(describeRevert({ revert: { ...base, committed: true } })[0]).toBe("Reverted and pushed");
    expect(describeRevert({})[0]).toBe("Reverted and pushed");
  });
});

describe("Activity and navigation", () => {
  const activity = { root: "/work/game", name: "game", incoming: 2, reviewRequests: 0, error: "", events: [] };

  it("counts incoming commits on Update until the clone is updated", async () => {
    api.update.mockResolvedValue({ branch: "main", commitId: "c7" });
    const { rerender } = renderWithProviders(<RepoView repo={repo} removing={false} onRemove={() => {}} notify={notify} activity={activity} />);
    const update = await screen.findByRole("button", { name: /Update/ });
    expect(update).toHaveTextContent("2");
    expect(update).toHaveAttribute("title", "2 new commits on main");
    await userEvent.click(update);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Up to date with main", "success"));
    expect(screen.getByRole("button", { name: /Update/ })).toHaveAttribute("title", "Get the latest version of the branch");

    rerender(<RepoView repo={repo} removing={false} onRemove={() => {}} notify={notify} activity={{ ...activity, incoming: 1 }} />);
    expect(screen.getByRole("button", { name: /Update/ })).toHaveAttribute("title", "1 new commit on main");
    rerender(<RepoView repo={repo} removing={false} onRemove={() => {}} notify={notify} activity={{ ...activity, incoming: 100 }} />);
    expect(screen.getByRole("button", { name: /Update/ })).toHaveTextContent("100+");
  });

  it("opens the tab and commit an intent asks for", async () => {
    const commit = { id: "c1", hash: "h1", message: "Paint hero", author: "Alice", email: "", createdAt: "", parents: [] };
    api.history.mockResolvedValue({ commits: [commit], next: "" });
    api.commitFiles.mockResolvedValue([]);
    const { rerender } = renderWithProviders(<RepoView repo={repo} removing={false} onRemove={() => {}} notify={notify} intent={{ tab: "history", commit: "c9", seq: 1 }} />);
    expect(await screen.findByRole("tab", { name: /History/ })).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(api.history).toHaveBeenCalledWith("/work/game", expect.objectContaining({ start: "c9", limit: 1 })));

    rerender(<RepoView repo={repo} removing={false} onRemove={() => {}} notify={notify} intent={{ tab: "nowhere", seq: 2 }} />);
    expect(screen.getByRole("tab", { name: /History/ })).toHaveAttribute("aria-selected", "true");
    rerender(<RepoView repo={repo} removing={false} onRemove={() => {}} notify={notify} intent={{ tab: "history", commit: "c1", seq: 3 }} />);
    expect(await screen.findAllByText("Paint hero")).not.toHaveLength(0);
  });

  it("lists Files first and opens the file a link points at", async () => {
    api.files.mockImplementation(async (_root: string, dir: string) => ({
      dir,
      entries:
        dir === ""
          ? [{ name: "art", path: "art", isDir: true, size: 0, binary: false, kind: "", status: "", staged: false, childrenChanged: true, tracked: true, hash: "", mtime: 0 }]
          : [{ name: "hero.png", path: "art/hero.png", isDir: false, size: 9, binary: true, kind: "image", status: "modified", staged: false, childrenChanged: false, tracked: true, hash: "h", mtime: 3 }],
    }));
    api.fileInfo.mockResolvedValue({ entry: { path: "art/hero.png" }, lastCommit: null });
    api.fileView.mockResolvedValue(null);
    api.history.mockResolvedValue({ commits: [{ id: "c7", hash: "h7", message: "Paint hero", author: "Alice", email: "", date: "", parents: [] }], next: "" });
    api.commitFiles.mockResolvedValue([]);
    api.tags.mockResolvedValue([]);
    api.restoreRevision.mockResolvedValue({ commitId: "c7" });
    renderWithProviders(<RepoView repo={repo} removing={false} onRemove={() => {}} notify={notify} intent={{ tab: "files", path: "art/hero.png", seq: 1 }} />);
    expect(screen.getAllByRole("tab")[0]).toHaveTextContent("Files");
    expect(screen.getByRole("tab", { name: /Files/ })).toHaveAttribute("aria-selected", "true");
    const versions = await screen.findByRole("region", { name: "Versions" });
    await userEvent.click(await within(versions).findByRole("button", { name: "Get this version" }));
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Get this version" }));
    expect(api.restoreRevision).toHaveBeenCalledWith("/work/game", "art/hero.png", "c7");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("art/hero.png is now the version from c7. Push it to share it, or discard the change.", "success"));

    await userEvent.click(within(screen.getByRole("toolbar", { name: "File actions" })).getByRole("button", { name: "History" }));
    expect(screen.getByRole("tab", { name: /History/ })).toHaveAttribute("aria-selected", "true");
    await waitFor(() => expect(api.history).toHaveBeenCalledWith("/work/game", expect.objectContaining({ path: "art/hero.png" })));
    expect(screen.getByRole("textbox", { name: "Path filter" })).toHaveValue("art/hero.png");
  });

  it("downloads a folder outside the sparse checkout from the Files tab", async () => {
    api.files.mockResolvedValue({ dir: "", entries: [] });
    api.remoteTree.mockResolvedValue({ files: 0, fileBytes: 0, dirs: [{ name: "Docs", path: "Docs" }] });
    api.setSparse.mockResolvedValue({ branch: "main" });
    renderWithProviders(<RepoView repo={{ ...repo, sparse: ["Art"] }} removing={false} onRemove={() => {}} notify={notify} intent={{ tab: "files", seq: 1 }} />);
    const docs = await screen.findByText("Docs", { selector: "[role=row] span" });
    fireEvent.doubleClick(docs);
    expect(api.setSparse).toHaveBeenCalledWith("/work/game", ["Art", "Docs"]);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Checkout updated", "success"));
  });
});
