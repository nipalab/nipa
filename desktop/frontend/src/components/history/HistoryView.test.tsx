import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Commit, FileDiff } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  history: vi.fn(),
  commitFiles: vi.fn(),
  tags: vi.fn(),
  createTag: vi.fn(),
  createBranch: vi.fn(),
  imageDiff: vi.fn(),
  modelDiff: vi.fn(),
  audioDiff: vi.fn(),
  tableDiff: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { HistoryView } from "./HistoryView";

const commit = (id: string, message: string, parents: string[] = [], author = "Ana"): Commit => ({
  id,
  hash: `h${id}`,
  parents,
  author,
  email: `${author.toLowerCase()}@studio.dev`,
  message,
  date: "2026-11-04T09:00:00Z",
});

const c3 = commit("c3", "Merge feature\n\nBrings the new level", ["c2", "f1"], "Bo");
const c2 = commit("c2", "Paint hero", ["c1"]);
const c1 = commit("c1", "Initial import");

const textFile: FileDiff = {
  path: "notes.txt",
  status: "modified",
  kind: "text",
  old: { path: "notes.txt", mode: 420, size: 3, binary: false, hash: "a" },
  new: { path: "notes.txt", mode: 420, size: 4, binary: false, hash: "b" },
  additions: 1,
  deletions: 0,
  hunks: [{ oldStart: 1, oldLines: 0, newStart: 1, newLines: 1, lines: [{ kind: "added", old: 0, new: 1, text: "hello" }] }],
  oldRevision: "c1",
  newRevision: "c2",
};
const imageFile: FileDiff = {
  ...textFile,
  path: "art/hero.png",
  kind: "image",
  old: { path: "art/hero.png", mode: 420, size: 10, binary: true, hash: "a" },
  new: { path: "art/hero.png", mode: 420, size: 12, binary: true, hash: "b" },
  hunks: null,
};

const pngInfo = {
  width: 4,
  height: 4,
  format: "png",
  bitDepth: 8,
  hasAlpha: false,
  size: 12,
  previewWidth: 4,
  previewHeight: 4,
  src: "data:image/png;base64,AA==",
  encoding: "",
  levels: 1,
  faces: 1,
  cube: false,
  level: 0,
  face: 0,
  hdr: false,
  layers: [],
};

const notify = vi.fn();
const onRevert = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.tags.mockResolvedValue([{ name: "v1.0", commitId: "c2", message: "First playable", createdAt: "", createdBy: "" }]);
  api.history.mockImplementation(async (_root: string, q: { path?: string; start?: string; limit?: number }) => {
    if (q.limit === 1) return { commits: [commit(q.start ?? "", "Feature work")], next: "" };
    if (q.start === "c1") return { commits: [c1], next: "" };
    return { commits: [c3, c2], next: "c1" };
  });
  api.commitFiles.mockResolvedValue([textFile, imageFile]);
  api.imageDiff.mockResolvedValue({ old: null, new: pngInfo, stats: null });
});

const view = (props: Partial<Parameters<typeof HistoryView>[0]> = {}) =>
  renderWithProviders(<HistoryView root="/work/game" branch="main" busy={false} notify={notify} onRevert={onRevert} {...props} />);

const commitList = () => screen.findByRole("list", { name: "Commits" });

describe("HistoryView", () => {
  it("lists commits with tags and pages older ones", async () => {
    view();
    const list = await commitList();
    expect(within(list).getByText("Merge feature")).toBeInTheDocument();
    expect(within(list).getByText("v1.0")).toBeInTheDocument();
    expect(api.history).toHaveBeenCalledWith("/work/game", { branch: "main", path: "", start: "" });

    await userEvent.click(within(list).getByRole("button", { name: "Load older commits" }));
    expect(await within(list).findByText("Initial import")).toBeInTheDocument();
    expect(within(list).queryByRole("button", { name: "Load older commits" })).not.toBeInTheDocument();
  });

  it("shows the selected commit with its changed files", async () => {
    view();
    expect(await screen.findByRole("heading", { name: "Merge feature" })).toBeInTheDocument();
    expect(screen.getByText("Brings the new level")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Revert/ })).toBeDisabled();

    await userEvent.click(within(await commitList()).getByText("Paint hero"));
    const files = await screen.findByRole("list", { name: "Changed files" });
    expect(api.commitFiles).toHaveBeenCalledWith("/work/game", "c2", "c1");
    expect(await screen.findByRole("table", { name: "Diff of notes.txt" })).toBeInTheDocument();
    await userEvent.click(within(files).getByTitle("art/hero.png"));
    await waitFor(() => expect(api.imageDiff).toHaveBeenCalledWith("/work/game", { source: "revision", revision: "c1", path: "art/hero.png" }, { source: "revision", revision: "c2", path: "art/hero.png" }, { level: 0, face: 0 }));
  });

  it("filters by author and message", async () => {
    view();
    const list = await commitList();
    await userEvent.type(screen.getByRole("textbox", { name: "Filter commits" }), "bo merge");
    expect(within(list).queryByText("Paint hero")).not.toBeInTheDocument();
    await userEvent.clear(screen.getByRole("textbox", { name: "Filter commits" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Filter commits" }), "nobody");
    expect(screen.getByText("No commits match")).toBeInTheDocument();
  });

  it("shows the history of one image with a thumbnail strip", async () => {
    view();
    await commitList();
    await userEvent.type(screen.getByRole("textbox", { name: "Path filter" }), "/art/hero.png{Enter}");
    expect(api.history).toHaveBeenCalledWith("/work/game", { branch: "main", path: "art/hero.png", start: "" });
    const strip = await screen.findByRole("list", { name: "Versions of art/hero.png" });
    await waitFor(() => expect(strip.querySelector("img")).toHaveAttribute("src", pngInfo.src));
    expect(api.imageDiff).toHaveBeenCalledWith("/work/game", null, { source: "revision", revision: "c2", path: "art/hero.png" }, { maxSize: 128 });
    await userEvent.click(within(strip).getByRole("button", { name: "Version c2" }));
    expect(await screen.findByRole("heading", { name: "Paint hero" })).toBeInTheDocument();
    await waitFor(() => expect(within(screen.getByRole("list", { name: "Changed files" })).getByTitle("art/hero.png")).toHaveAttribute("aria-current", "true"));

    await userEvent.click(screen.getByRole("button", { name: "Show all commits" }));
    expect(screen.queryByRole("list", { name: "Versions of art/hero.png" })).not.toBeInTheDocument();
  });

  it("explains an empty path history and a failed thumbnail", async () => {
    api.imageDiff.mockRejectedValue(new Error("gone"));
    api.history.mockImplementation(async (_r: string, q: { path?: string }) => (q.path === "docs" ? { commits: [], next: "" } : { commits: [c2], next: "" }));
    view({ path: "art/hero.png" });
    expect(await screen.findByLabelText("Not available in this version")).toBeInTheDocument();
    await userEvent.clear(screen.getByRole("textbox", { name: "Path filter" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Path filter" }), "docs{Enter}");
    expect(await screen.findByText("No commits changed docs")).toBeInTheDocument();
  });

  it("jumps to a parent that is not loaded yet", async () => {
    view();
    await screen.findByRole("heading", { name: "Merge feature" });
    await userEvent.click(screen.getByRole("button", { name: /merged f1/ }));
    expect(await screen.findByRole("heading", { name: "Feature work" })).toBeInTheDocument();
    expect(api.history).toHaveBeenCalledWith("/work/game", { branch: "main", start: "f1", limit: 1 });
  });

  it("confirms a revert and blocks it while detached", async () => {
    const first = view();
    await userEvent.click(within(await commitList()).getByText("Paint hero"));
    await userEvent.click(screen.getByRole("button", { name: /Revert/ }));
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }));
    expect(onRevert).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: /Revert/ }));
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Revert" }));
    expect(onRevert).toHaveBeenCalledWith(c2);
    first.unmount();

    view({ detached: "v1.0" });
    await userEvent.click(within(await commitList()).getByText("Paint hero"));
    expect(screen.getByRole("button", { name: /Revert/ })).toHaveAttribute("title", "Switch back to a branch to revert (checked out at tag v1.0)");
  });

  it("tags a commit and creates a branch from it", async () => {
    api.createTag.mockResolvedValueOnce({ name: "v1.1", commitId: "c3" }).mockRejectedValueOnce(new Error("tag exists"));
    api.createBranch.mockResolvedValueOnce({ name: "hotfix" }).mockRejectedValueOnce(new Error("bad name"));
    view();
    await screen.findByRole("heading", { name: "Merge feature" });

    await userEvent.click(screen.getByRole("button", { name: /Tag…/ }));
    const tagDialog = screen.getByRole("dialog");
    expect(within(tagDialog).getByRole("button", { name: "Create tag" })).toBeDisabled();
    await userEvent.type(within(tagDialog).getByRole("textbox", { name: "Tag name" }), "v1.1");
    await userEvent.type(within(tagDialog).getByRole("textbox", { name: "Message (optional)" }), "Milestone");
    await userEvent.click(within(tagDialog).getByRole("button", { name: "Create tag" }));
    expect(api.createTag).toHaveBeenCalledWith("/work/game", "v1.1", { commit: "c3" }, "Milestone");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Tagged c3 as v1.1", "success"));

    await userEvent.click(screen.getByRole("button", { name: /Tag…/ }));
    await userEvent.type(screen.getByRole("textbox", { name: "Tag name" }), "v1.1{Enter}");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not create the tag: tag exists"));
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Branch…/ }));
    await userEvent.type(screen.getByRole("textbox", { name: "Branch name" }), "hotfix{Enter}");
    expect(api.createBranch).toHaveBeenCalledWith("/work/game", "hotfix", { commit: "c3" });
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Created branch hotfix. Switch to it from Branches & tags.", "success"));

    await userEvent.click(screen.getByRole("button", { name: /Branch…/ }));
    await userEvent.type(screen.getByRole("textbox", { name: "Branch name" }), "bad{Enter}");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not create the branch: bad name"));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  });

  it("reports load failures", async () => {
    api.history.mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ commits: [], next: "" });
    view();
    expect(await screen.findByText("offline")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No commits yet")).toBeInTheDocument();
    expect(screen.getByText("Select a commit")).toBeInTheDocument();
  });

  it("reports a commit whose files cannot be listed or that changed nothing", async () => {
    api.commitFiles.mockRejectedValueOnce(new Error("revision not found")).mockResolvedValue([]);
    view();
    expect(await screen.findByText("revision not found")).toBeInTheDocument();
    await userEvent.click(within(await commitList()).getByText("Paint hero"));
    expect(await screen.findByText("This commit changed no files you can see.")).toBeInTheDocument();
  });

  it("says when a parent commit cannot be loaded", async () => {
    view();
    await screen.findByRole("heading", { name: "Merge feature" });
    api.history.mockRejectedValueOnce(new Error("gone"));
    await userEvent.click(screen.getByRole("button", { name: /merged f1/ }));
    expect(await screen.findByText("This commit could not be loaded")).toBeInTheDocument();
  });
});

describe("commit file refs", () => {
  it("points viewers at the commit and its parent", () => {
    expect(imageRefs(imageFile)).toEqual({
      old: { source: "revision", revision: "c1", path: "art/hero.png" },
      next: { source: "revision", revision: "c2", path: "art/hero.png" },
    });
    expect(imageRefs({ ...imageFile, old: null })).toEqual({ old: null, next: { source: "revision", revision: "c2", path: "art/hero.png" } });
    expect(viewerKey("/r", imageFile, "image")).toEqual(["commit", "/r", "c2", "art/hero.png", "image"]);
    expect(viewerKey("/r", { ...imageFile, newRevision: undefined }, "image")).toEqual(["diff", "/r", "art/hero.png", "image"]);
  });
});
