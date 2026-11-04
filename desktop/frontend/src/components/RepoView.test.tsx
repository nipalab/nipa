import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChangeSet, FileDiff, OpProgress, Repo, RepoChanged } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  changes: vi.fn(),
  stage: vi.fn(),
  unstage: vi.fn(),
  discard: vi.fn(),
  fileDiff: vi.fn(),
  metaWarnings: vi.fn(),
  push: vi.fn(),
  update: vi.fn(),
  onRepoChanged: vi.fn(),
  onOpProgress: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { RepoView } from "./RepoView";

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
  ...over,
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

let changed: (c: RepoChanged) => void = () => {};
let progressed: (p: OpProgress) => void = () => {};
const notify = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.changes.mockResolvedValue(changeSet());
  api.metaWarnings.mockResolvedValue([]);
  api.fileDiff.mockImplementation(async (_root: string, path: string) => (path === "art/hero.png" ? imageDiff : path === "src/player.go" ? textDiff : null));
  api.onRepoChanged.mockImplementation((fn: (c: RepoChanged) => void) => {
    changed = fn;
    return () => {};
  });
  api.onOpProgress.mockImplementation((fn: (p: OpProgress) => void) => {
    progressed = fn;
    return () => {};
  });
});

const view = (r: Repo = repo) => renderWithProviders(<RepoView repo={r} removing={false} onRemove={() => {}} notify={notify} />);

const fileList = () => screen.findByRole("listbox", { name: "Changed files" });

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
    expect(screen.getByText("2 conflicted files: resolve, stage, then push.")).toBeInTheDocument();
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
    expect(screen.getByRole("tab", { name: /History/ })).toBeDisabled();
    await userEvent.click(screen.getByRole("tab", { name: /Changes/ }));
    expect(await fileList()).toBeInTheDocument();
  });
});
