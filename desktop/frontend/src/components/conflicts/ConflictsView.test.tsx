import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ConflictSet, ConflictTexts, FileDiff } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  conflicts: vi.fn(),
  conflictTexts: vi.fn(),
  conflictDiff: vi.fn(),
  resolveConflict: vi.fn(),
  mergeScene: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { ConflictsView } from "./ConflictsView";

const notify = vi.fn();

const set = (over: Partial<ConflictSet> = {}): ConflictSet => ({
  op: "merge",
  base: { id: "b1", label: "merge base" },
  ours: { id: "o1", label: "main" },
  theirs: { id: "t1", label: "feature" },
  files: [
    { path: "src/a.txt", kind: "text", binary: false, resolved: false, viewer: "text" },
    { path: "art/hero.png", kind: "binary", binary: true, resolved: false, viewer: "image" },
    { path: "docs/old.md", kind: "modify_delete", binary: false, resolved: true, viewer: "text" },
  ],
  ...over,
});

const texts = (working: string): ConflictTexts => ({
  base: { text: "base line\n", missing: false },
  ours: { text: "mine\n", missing: false },
  theirs: { text: "theirs\n", missing: false },
  working: { text: working, missing: false },
});

const twoConflicts = "top\n<<<<<<< ours\nmine\n=======\ntheirs\n>>>>>>> theirs\nmid\n<<<<<<< ours\nm2\n=======\nt2\n>>>>>>> theirs\n";

const compare: FileDiff = {
  path: "art/hero.png",
  status: "modified",
  kind: "text",
  old: { path: "art/hero.png", mode: 420, size: 4, binary: false, hash: "aa" },
  new: { path: "art/hero.png", mode: 420, size: 4, binary: false, hash: "bb" },
  additions: 1,
  deletions: 1,
  hunks: [{ oldStart: 1, oldLines: 1, newStart: 1, newLines: 1, lines: [{ kind: "removed", old: 1, new: 0, text: "old pixels" }, { kind: "added", old: 0, new: 1, text: "new pixels" }] }],
  oldRevision: "o1",
  newRevision: "t1",
};

beforeEach(() => {
  vi.resetAllMocks();
  api.conflicts.mockResolvedValue(set());
  api.conflictTexts.mockResolvedValue(texts(twoConflicts));
  api.conflictDiff.mockResolvedValue(compare);
  api.resolveConflict.mockResolvedValue({ branch: "main", staged: [], unstaged: [], conflicts: [], resolved: [] });
});

const view = (busy = false) => renderWithProviders(<ConflictsView root="/work/game" busy={busy} notify={notify} />);

const sceneSet = () => set({ files: [{ path: "levels/arena.tscn", kind: "text", binary: false, resolved: false, viewer: "scene" }] });

describe("Godot scene merge", () => {
  it("resolves a clean scene merge in one click", async () => {
    api.conflicts.mockResolvedValue(sceneSet());
    api.mergeScene.mockResolvedValue({ merged: "[gd_scene]\n", clean: true, conflicts: [] });
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Merge scene" }));
    expect(api.mergeScene).toHaveBeenCalledWith("/work/game", "levels/arena.tscn");
    await waitFor(() => expect(api.resolveConflict).toHaveBeenCalledWith("/work/game", "levels/arena.tscn", "content", "[gd_scene]\n"));
  });

  it("lists what the scene merge could not decide and keeps the line editor", async () => {
    api.conflicts.mockResolvedValue(sceneSet());
    api.mergeScene.mockResolvedValue({
      merged: "",
      clean: false,
      conflicts: [
        { section: 'node name="Player"', key: "position", message: "changed on both sides" },
        { section: "", key: "", message: "header differs" },
      ],
    });
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Merge scene" }));
    const list = await screen.findByRole("list", { name: "Scene conflicts" });
    expect(within(list).getAllByRole("listitem")[0]).toHaveTextContent('node name="Player" › position: changed on both sides');
    expect(within(list).getAllByRole("listitem")[1]).toHaveTextContent("file header: header differs");
    expect(screen.getByText(/Both sides changed 2 things/)).toBeInTheDocument();
    expect(api.resolveConflict).not.toHaveBeenCalled();
    expect(screen.getByRole("region", { name: "Conflict 1" })).toBeInTheDocument();
  });

  it("shows why a scene cannot be merged", async () => {
    api.conflicts.mockResolvedValue(sceneSet());
    api.mergeScene.mockRejectedValue(new Error("levels/arena.tscn cannot be merged: bad header"));
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Merge scene" }));
    expect(await screen.findByText("levels/arena.tscn cannot be merged: bad header")).toBeInTheDocument();
  });

  it("is not offered for other text files", async () => {
    view();
    await screen.findByRole("region", { name: "Conflict 1" });
    expect(screen.queryByRole("button", { name: "Merge scene" })).not.toBeInTheDocument();
  });
});

describe("ConflictsView", () => {
  it("lists the conflicts with their state and opens the first open one", async () => {
    view();
    const list = await screen.findByRole("list", { name: "Conflicted files" });
    expect(within(list).getAllByRole("button")).toHaveLength(3);
    expect(within(list).getAllByLabelText("Not resolved")).toHaveLength(2);
    expect(within(list).getByLabelText("Resolved")).toBeInTheDocument();
    expect(screen.getByText("1 of 3 resolved")).toBeInTheDocument();
    expect(screen.getByText(/Merging/)).toHaveTextContent("Merging feature into main");
    expect(within(list).getByRole("button", { name: /src\/a.txt/ })).toHaveAttribute("aria-current", "true");
    expect(await screen.findByRole("region", { name: "Conflict 1" })).toBeInTheDocument();
  });

  it("picks sides block by block and saves the result", async () => {
    view();
    const first = await screen.findByRole("region", { name: "Conflict 1" });
    const second = screen.getByRole("region", { name: "Conflict 2" });
    const save = screen.getByRole("button", { name: "Mark resolved" });
    expect(save).toBeDisabled();
    expect(screen.getByText(/Pick a side for every conflict/)).toBeInTheDocument();
    expect(screen.getByText(/0 of 2 conflicts picked/)).toBeInTheDocument();

    await userEvent.click(within(first).getByRole("button", { name: "Theirs" }));
    expect(within(first).getByRole("button", { name: "Theirs" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(within(second).getByRole("button", { name: "Both, mine first" }));
    expect(screen.getByRole("textbox", { name: "Result" })).toHaveValue("top\ntheirs\nmid\nm2\nt2\n");
    expect(save).toBeEnabled();

    await userEvent.click(within(second).getByRole("button", { name: "Both, theirs first" }));
    await userEvent.click(save);
    expect(api.resolveConflict).toHaveBeenCalledWith("/work/game", "src/a.txt", "content", "top\ntheirs\nmid\nt2\nm2\n");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Resolved src/a.txt", "success"));
    await waitFor(() => expect(screen.getByRole("button", { name: /art\/hero.png/ })).toHaveAttribute("aria-current", "true"));
  });

  it("accepts a hand-edited result and can reset it", async () => {
    view();
    await screen.findByRole("region", { name: "Conflict 1" });
    const result = screen.getByRole("textbox", { name: "Result" });
    await userEvent.clear(result);
    await userEvent.type(result, "hand made");
    expect(screen.getByText(/edited by hand/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Mark resolved" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "Reset to picks" }));
    expect(result).toHaveValue(twoConflicts);
    await userEvent.click(within(screen.getByRole("region", { name: "Conflict 1" })).getByRole("button", { name: "Mine" }));
    expect(result).toHaveValue("top\nmine\nmid\n<<<<<<< ours\nm2\n=======\nt2\n>>>>>>> theirs\n");
  });

  it("shows the common ancestor and takes a whole side", async () => {
    view();
    await screen.findByRole("region", { name: "Conflict 1" });
    await userEvent.click(screen.getByRole("button", { name: "Show common ancestor" }));
    expect(screen.getByLabelText("Common ancestor")).toHaveTextContent("base line");
    await userEvent.click(screen.getByRole("button", { name: "Hide common ancestor" }));
    expect(screen.queryByLabelText("Common ancestor")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Take theirs" }));
    expect(api.resolveConflict).toHaveBeenCalledWith("/work/game", "src/a.txt", "theirs", undefined);
  });

  it("says when a text file has no markers left", async () => {
    api.conflictTexts.mockResolvedValue({ ...texts("clean\n"), base: { text: "", missing: true } });
    view();
    expect(await screen.findByText("No conflict markers left in this file.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /common ancestor/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Mark resolved" }));
    expect(api.resolveConflict).toHaveBeenCalledWith("/work/game", "src/a.txt", "content", "clean\n");
  });

  it("falls back to picking a version when the texts cannot be read", async () => {
    api.conflictTexts.mockRejectedValue(new Error("src/a.txt is too large to edit here"));
    view();
    expect(await screen.findByText("src/a.txt is too large to edit here")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Take mine" }));
    expect(api.resolveConflict).toHaveBeenCalledWith("/work/game", "src/a.txt", "ours", undefined);
  });

  it("compares binary versions and takes one", async () => {
    view();
    await userEvent.click(await screen.findByRole("button", { name: /art\/hero.png/ }));
    expect(await screen.findByText("new pixels")).toBeInTheDocument();
    expect(screen.getByText(/binary files cannot be merged line by line/)).toBeInTheDocument();
    expect(api.conflictDiff).toHaveBeenCalledWith("/work/game", "art/hero.png");
    await userEvent.click(screen.getByRole("button", { name: "Use the file on disk" }));
    expect(api.resolveConflict).toHaveBeenLastCalledWith("/work/game", "art/hero.png", "working", undefined);
    await userEvent.click(await screen.findByRole("button", { name: /art\/hero.png/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Take theirs" }));
    expect(api.resolveConflict).toHaveBeenLastCalledWith("/work/game", "art/hero.png", "theirs", undefined);
  });

  it("explains deletions and missing comparisons", async () => {
    api.conflictDiff.mockResolvedValue(null);
    view();
    await userEvent.click(await screen.findByRole("button", { name: /docs\/old.md/ }));
    expect(await screen.findByText(/main changed this file while feature deleted it/)).toBeInTheDocument();
    expect(await screen.findByText(/Both sides hold the same content/)).toBeInTheDocument();
    expect(screen.getByText("Resolved", { selector: "span" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Delete it" }));
    expect(api.resolveConflict).toHaveBeenLastCalledWith("/work/game", "docs/old.md", "theirs", undefined);
  });

  it("describes the other conflict kinds", async () => {
    api.conflicts.mockResolvedValue(
      set({
        files: [
          { path: "gone.png", kind: "delete_modify", binary: true, resolved: false, viewer: "image" },
          { path: "new.bin", kind: "add_add", binary: true, resolved: false, viewer: "opaque" },
        ],
      }),
    );
    api.conflictDiff.mockRejectedValue(new Error("offline"));
    view();
    expect(await screen.findByText(/main deleted this file while feature changed it/)).toBeInTheDocument();
    expect(await screen.findByText("offline")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Keep it deleted" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /new.bin/ }));
    expect(await screen.findByText(/both added this file with different content/)).toBeInTheDocument();
  });

  it("reports a failed resolve", async () => {
    api.resolveConflict.mockRejectedValue(new Error("still has conflict markers"));
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Take mine" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not resolve src/a.txt: still has conflict markers"));
  });

  it("disables the actions while another operation runs", async () => {
    view(true);
    await screen.findByRole("region", { name: "Conflict 1" });
    expect(screen.getByRole("button", { name: "Take mine" })).toBeDisabled();
  });

  it("describes a revert when every file is resolved", async () => {
    api.conflicts.mockResolvedValue(set({ op: "revert", base: { id: "c1", label: "Add hero" }, files: [{ path: "docs/old.md", kind: "modify_delete", binary: false, resolved: true, viewer: "text" }] }));
    view();
    expect(await screen.findByText("Reverting Add hero")).toBeInTheDocument();
    expect(screen.getByText("All 1 resolved: continue the revert above.")).toBeInTheDocument();
  });

  it("shows loading, errors and the empty state", async () => {
    api.conflicts.mockReturnValue(new Promise(() => {}));
    const first = view();
    expect(screen.getByText("Reading conflicts…")).toBeInTheDocument();
    first.unmount();

    api.conflicts.mockRejectedValue(new Error("daemon gone"));
    const second = view();
    expect(await screen.findByText("daemon gone")).toBeInTheDocument();
    second.unmount();

    api.conflicts.mockResolvedValue(set({ op: "", files: [] }));
    view();
    expect(await screen.findByText("No conflicts to resolve.")).toBeInTheDocument();
  });

  it("shows a loading state for the versions", async () => {
    api.conflictTexts.mockReturnValue(new Promise(() => {}));
    api.conflictDiff.mockReturnValue(new Promise(() => {}));
    view();
    expect(await screen.findByText("Loading the versions…")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /art\/hero.png/ }));
    expect(await screen.findByText("Loading both versions…")).toBeInTheDocument();
  });
});
