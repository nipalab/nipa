import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Activity, FileDiff, MergeRequest, MergeRequestDetail, Thread } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  mergeRequests: vi.fn(),
  mergeRequest: vi.fn(),
  mergeRequestActivity: vi.fn(),
  mergeRequestCommits: vi.fn(),
  revisionFiles: vi.fn(),
  lockStatus: vi.fn(),
  branches: vi.fn(),
  createMergeRequest: vi.fn(),
  editMergeRequest: vi.fn(),
  setDraft: vi.fn(),
  mergeMergeRequest: vi.fn(),
  closeMergeRequest: vi.fn(),
  reopenMergeRequest: vi.fn(),
  submitReview: vi.fn(),
  comment: vi.fn(),
  reply: vi.fn(),
  resolveThread: vi.fn(),
  imageDiff: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { MergeRequestsView } from "./MergeRequestsView";

const mr = (over: Partial<MergeRequest> = {}): MergeRequest => ({
  number: 12,
  title: "Hero pass",
  description: "New hero texture and a speed tweak",
  status: "open",
  draft: false,
  source: "art",
  target: "main",
  authorId: "u1",
  createdAt: "2026-11-05T09:00:00Z",
  updatedAt: "2026-11-05T10:00:00Z",
  mergeCommitId: "",
  mergeBaseId: "",
  mergedSourceId: "",
  approvals: 1,
  changesRequested: 0,
  assignees: [],
  ...over,
});

const detail = (over: Partial<MergeRequest> = {}, m: Partial<MergeRequestDetail["mergeability"]> = {}): MergeRequestDetail => ({
  request: mr(over),
  mergeability: { status: "mergeable", blockedBy: "", sourceCommitId: "s2", targetCommitId: "t1", mergeBaseId: "b1", ...m },
  outstanding: [],
});

const thread = (over: Partial<Thread>): Thread => ({
  id: "t1",
  path: "",
  oldLine: 0,
  newLine: 0,
  outdated: false,
  resolved: false,
  resolvedBy: "",
  createdAt: "2026-11-05T09:30:00Z",
  comments: [{ id: "c1", author: { id: "u2", name: "Bo" }, body: "Looks heavy", createdAt: "2026-11-05T09:30:00Z", edited: false }],
  ...over,
});

const activity = (over: Partial<Activity> = {}): Activity => ({
  timeline: [{ id: "e1", kind: "opened", actor: { id: "u1", name: "Ana" }, subject: { id: "", name: "" }, body: "", commitId: "", createdAt: "2026-11-05T09:00:00Z" }],
  reviews: [{ id: "r1", reviewer: { id: "u2", name: "Bo" }, state: "approved", body: "Ship it", stale: false, dismissed: false, createdAt: "2026-11-05T11:00:00Z" }],
  threads: [thread({})],
  checks: [{ name: "build", state: "success", url: "https://ci.test/1", reporter: "CI", updatedAt: "" }],
  ...over,
});

const textFile: FileDiff = {
  path: "src/player.go",
  status: "modified",
  kind: "text",
  old: { path: "src/player.go", mode: 420, size: 10, binary: false, hash: "a" },
  new: { path: "src/player.go", mode: 420, size: 12, binary: false, hash: "b" },
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
        { kind: "added", old: 0, new: 2, text: "var speed = 2" },
      ],
    },
  ],
  oldRevision: "b1",
  newRevision: "s2",
};
const imageFile: FileDiff = { ...textFile, path: "art/hero.png", kind: "image", old: { ...textFile.old!, binary: true }, new: { ...textFile.new!, binary: true }, hunks: null };

const notify = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  api.mergeRequests.mockImplementation(async (_r: string, f: { status: string; after?: number }) =>
    f.after ? { items: [mr({ number: 3, title: "Old fix" })], next: 0 } : { items: [mr(), mr({ number: 11, title: "Audio mix", draft: true, approvals: 0, changesRequested: 1 })], next: 10 },
  );
  api.mergeRequest.mockImplementation(async (_r: string, n: number) => detail(n === 11 ? { number: 11, title: "Audio mix", draft: true } : {}));
  api.mergeRequestActivity.mockResolvedValue(activity());
  api.mergeRequestCommits.mockResolvedValue([{ id: "s2", hash: "h", parents: ["b1"], author: "Ana", email: "", message: "Speed tweak\n\nbody", date: "2026-11-05T09:00:00Z" }]);
  api.revisionFiles.mockResolvedValue([textFile, imageFile]);
  api.lockStatus.mockResolvedValue({ userId: "me", branch: "main", defaultBranch: true, canReleaseAny: false, locks: [], files: [] });
  api.branches.mockResolvedValue([
    { name: "main", commitId: "t1", default: true, protected: true },
    { name: "art", commitId: "s2", default: false, protected: false },
  ]);
  api.imageDiff.mockResolvedValue({ old: null, new: null, stats: null, unsupported: "no preview" });
});

const view = () => renderWithProviders(<MergeRequestsView root="/work/game" branch="art" notify={notify} />);
const list = () => screen.findByRole("list", { name: "Merge requests" });

describe("MergeRequestsView list", () => {
  it("lists requests, filters by status and search, and pages", async () => {
    view();
    const rows = await list();
    expect(within(rows).getByText("Hero pass")).toBeInTheDocument();
    expect(within(rows).getByText("Draft")).toBeInTheDocument();
    expect(within(rows).getByLabelText("Changes requested")).toBeInTheDocument();
    expect(api.mergeRequests).toHaveBeenCalledWith("/work/game", { status: "open", search: "", after: 0 });

    await userEvent.click(within(rows).getByRole("button", { name: "Load more" }));
    expect(await within(rows).findByText("Old fix")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Merged" }));
    expect(api.mergeRequests).toHaveBeenCalledWith("/work/game", { status: "merged", search: "", after: 0 });
    await userEvent.type(screen.getByRole("textbox", { name: "Search merge requests" }), "hero{Enter}");
    expect(api.mergeRequests).toHaveBeenCalledWith("/work/game", { status: "merged", search: "hero", after: 0 });
    await userEvent.click(screen.getByRole("button", { name: "Refresh merge requests" }));
  });

  it("explains empty lists and failures", async () => {
    api.mergeRequests.mockResolvedValueOnce({ items: [], next: 0 }).mockRejectedValueOnce(new Error("offline"));
    view();
    expect(await screen.findByText("No merge requests here yet")).toBeInTheDocument();
    expect(screen.getByText("Select a merge request")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Closed" }));
    expect(await screen.findByText("offline")).toBeInTheDocument();
  });

  it("opens a new merge request from the clone's branch", async () => {
    api.createMergeRequest.mockRejectedValueOnce(new Error("already open")).mockResolvedValueOnce(mr({ number: 13, title: "Sky", draft: true }));
    view();
    await list();
    await userEvent.click(screen.getByRole("button", { name: "New" }));
    const dialog = screen.getByRole("dialog");
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "Target branch" })).toHaveValue("main"));
    expect(within(dialog).getByRole("combobox", { name: "Source branch" })).toHaveValue("art");
    const submit = within(dialog).getByRole("button", { name: "Open merge request" });
    expect(submit).toBeDisabled();
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Title" }), "Sky");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "New sky");
    await userEvent.click(within(dialog).getByRole("checkbox"));
    await userEvent.click(submit);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not open the merge request: already open"));
    await userEvent.click(submit);
    expect(api.createMergeRequest).toHaveBeenLastCalledWith("/work/game", { source: "art", target: "main", title: "Sky", description: "New sky", draft: true });
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Opened !13", "success"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Drafts" })).toHaveAttribute("aria-pressed", "true");
  });

  it("closes the new request dialog and picks other branches", async () => {
    api.branches.mockResolvedValue([{ name: "main", commitId: "t1", default: true, protected: false }]);
    renderWithProviders(<MergeRequestsView root="/work/game" branch="main" notify={notify} />);
    await list();
    await userEvent.click(screen.getByRole("button", { name: "New" }));
    const dialog = screen.getByRole("dialog");
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "Source branch" })).toHaveValue("main"));
    expect(within(dialog).getByRole("combobox", { name: "Target branch" })).toHaveValue("");
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "New" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  });
});

describe("MergeRequestDetail", () => {
  it("shows the conversation with reviews, threads and checks", async () => {
    view();
    expect(await screen.findByRole("heading", { name: /Hero pass/ })).toBeInTheDocument();
    const feed = await screen.findByRole("list", { name: "Activity" });
    expect(within(feed).getByText("opened this request")).toBeInTheDocument();
    expect(within(feed).getByText("approved these changes")).toBeInTheDocument();
    expect(within(feed).getByText("Looks heavy")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Status checks" })).toHaveTextContent("build");
    expect(screen.getByRole("link", { name: /Details/ })).toHaveAttribute("href", "https://ci.test/1");
    expect(screen.getByRole("region", { name: "Merge" })).toHaveTextContent("Ready to merge");
  });

  it("blocks approving your own request", async () => {
    api.lockStatus.mockResolvedValue({ userId: "u1", branch: "main", defaultBranch: true, canReleaseAny: false, locks: [], files: [] });
    view();
    const review = await screen.findByRole("region", { name: "Review" });
    await userEvent.type(within(review).getByRole("textbox", { name: "Review comment" }), "ok");
    await waitFor(() => expect(within(review).getByRole("button", { name: "Approve" })).toBeDisabled());
    expect(within(review).getByRole("button", { name: "Request changes" })).toBeDisabled();
    expect(within(review).getByRole("button", { name: "Comment" })).toBeEnabled();
  });

  it("submits reviews, comments, replies and resolves threads", async () => {
    api.submitReview.mockResolvedValue(undefined);
    api.comment.mockResolvedValue(thread({ id: "t2" }));
    api.reply.mockRejectedValueOnce(new Error("locked")).mockResolvedValue({ id: "c2" });
    api.resolveThread.mockResolvedValue(undefined);
    view();
    const review = await screen.findByRole("region", { name: "Review" });
    expect(within(review).getByRole("button", { name: "Approve" })).toBeDisabled();
    await userEvent.type(within(review).getByRole("textbox", { name: "Review comment" }), "Looks good");
    await userEvent.click(within(review).getByRole("button", { name: "Approve" }));
    expect(api.submitReview).toHaveBeenCalledWith("/work/game", 12, "approved", "Looks good");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Approved", "success"));
    await userEvent.type(within(review).getByRole("textbox", { name: "Review comment" }), "Too dark");
    await userEvent.click(within(review).getByRole("button", { name: "Request changes" }));
    expect(api.submitReview).toHaveBeenLastCalledWith("/work/game", 12, "changes_requested", "Too dark");
    await waitFor(() => expect(within(review).getByRole("textbox", { name: "Review comment" })).toHaveValue(""));
    await userEvent.type(within(review).getByRole("textbox", { name: "Review comment" }), "Noted");
    await userEvent.click(within(review).getByRole("button", { name: "Comment" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Review sent", "success"));

    await userEvent.type(screen.getByRole("textbox", { name: "Comment" }), "Can we keep the old one?");
    await userEvent.click(screen.getAllByRole("button", { name: "Comment" }).at(-1)!);
    expect(api.comment).toHaveBeenCalledWith("/work/game", 12, { body: "Can we keep the old one?" });

    const card = screen.getByRole("group", { name: "Thread t1" });
    await userEvent.type(within(card).getByRole("textbox", { name: "Reply" }), "Agreed");
    await userEvent.click(within(card).getByRole("button", { name: "Reply" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not reply: locked"));
    expect(within(card).getByRole("textbox", { name: "Reply" })).toHaveValue("Agreed");
    await userEvent.click(within(card).getByRole("button", { name: "Reply" }));
    await waitFor(() => expect(within(card).getByRole("textbox", { name: "Reply" })).toHaveValue(""));
    await userEvent.click(within(card).getByRole("button", { name: "Resolve" }));
    expect(api.resolveThread).toHaveBeenCalledWith("/work/game", 12, "t1", true);
  });

  it("collapses resolved threads and reopens them", async () => {
    api.mergeRequestActivity.mockResolvedValue(activity({ threads: [thread({ resolved: true, resolvedBy: "Ana" })], reviews: [], checks: [] }));
    api.resolveThread.mockResolvedValue(undefined);
    view();
    await userEvent.click(await screen.findByRole("button", { name: /Resolved by Ana · 1 comment/ }));
    await userEvent.click(screen.getByRole("button", { name: "Reopen thread" }));
    expect(api.resolveThread).toHaveBeenCalledWith("/work/game", 12, "t1", false);
  });

  it("merges with the chosen strategy and remembers it", async () => {
    api.mergeMergeRequest.mockResolvedValue(detail({ status: "merged" }));
    view();
    const box = await screen.findByRole("region", { name: "Merge" });
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Merge strategy" }), "squash");
    await userEvent.click(within(box).getByRole("checkbox"));
    await userEvent.click(within(box).getByRole("button", { name: "Merge" }));
    expect(api.mergeMergeRequest).toHaveBeenCalledWith("/work/game", 12, "squash", true);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Merged !12", "success"));
    expect(localStorage.getItem("nipa.mergeStrategy")).toBe("squash");
  });

  it("explains blocked requests and lets drafts become ready", async () => {
    api.mergeRequest.mockResolvedValue(detail({ draft: true }, { blockedBy: "draft" }));
    api.setDraft.mockResolvedValue(mr());
    view();
    const box = await screen.findByRole("region", { name: "Merge" });
    expect(box).toHaveTextContent("It is a draft");
    expect(within(box).getByRole("button", { name: "Merge" })).toBeDisabled();
    await userEvent.click(within(box).getByRole("button", { name: "Mark ready for review" }));
    expect(api.setDraft).toHaveBeenCalledWith("/work/game", 12, false);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Marked ready for review", "success"));
  });

  it("handles a target that moved on", async () => {
    api.mergeRequest.mockResolvedValue(detail({}, { status: "behind_target" }));
    api.setDraft.mockRejectedValue(new Error("not yours"));
    localStorage.setItem("nipa.mergeStrategy", "ff");
    view();
    const box = await screen.findByRole("region", { name: "Merge" });
    expect(box).toHaveTextContent("A fast-forward is not possible");
    expect(within(box).getByRole("button", { name: "Merge" })).toBeDisabled();
    await userEvent.selectOptions(within(box).getByRole("combobox", { name: "Merge strategy" }), "rebase");
    expect(box).toHaveTextContent("this strategy can still merge");
    expect(within(box).getByRole("button", { name: "Merge" })).toBeEnabled();
    await userEvent.click(within(box).getByRole("button", { name: "Mark as draft" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not change the draft state: not yours"));
  });

  it("closes and reopens a request", async () => {
    api.closeMergeRequest.mockResolvedValue(undefined);
    api.reopenMergeRequest.mockResolvedValue(undefined);
    const first = view();
    const box = await screen.findByRole("region", { name: "Merge" });
    await userEvent.click(within(box).getByRole("button", { name: "Close" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await userEvent.click(within(box).getByRole("button", { name: "Close" }));
    await userEvent.click(screen.getByRole("button", { name: "Close request" }));
    expect(api.closeMergeRequest).toHaveBeenCalledWith("/work/game", 12);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Closed !12", "success"));
    first.unmount();

    api.mergeRequest.mockResolvedValue(detail({ status: "closed" }, { status: "closed" }));
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Reopen" }));
    expect(api.reopenMergeRequest).toHaveBeenCalledWith("/work/game", 12);
    expect(screen.queryByRole("region", { name: "Review" })).not.toBeInTheDocument();
  });

  it("shows a merged request", async () => {
    api.mergeRequest.mockResolvedValue(detail({ status: "merged", mergeCommitId: "m9", mergeBaseId: "b1", mergedSourceId: "s7" }, { status: "merged", sourceCommitId: "", mergeBaseId: "" }));
    view();
    expect(await screen.findByText(/Merged into/)).toBeInTheDocument();
    expect(screen.getAllByText("m9").length).toBeGreaterThan(0);
    await userEvent.click(screen.getByRole("tab", { name: /File changes/ }));
    await waitFor(() => expect(api.revisionFiles).toHaveBeenCalledWith("/work/game", "b1", "s7"));
    expect(api.revisionFiles).not.toHaveBeenCalledWith("/work/game", "b1", "m9");
    expect(api.branches).not.toHaveBeenCalled();
  });

  it("uses the live source branch head for a closed request", async () => {
    api.branches.mockResolvedValue([{ name: "art", commitId: "s3", default: false, protected: false }]);
    api.mergeRequest.mockResolvedValue(detail({ status: "closed", mergeBaseId: "b1" }, { status: "closed", sourceCommitId: "", mergeBaseId: "" }));
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /File changes/ }));
    await waitFor(() => expect(api.revisionFiles).toHaveBeenCalledWith("/work/game", "b1", "s3"));
  });

  it("edits the title and description", async () => {
    api.editMergeRequest.mockResolvedValue(mr({ title: "Hero pass v2" }));
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Edit title and description" }));
    const title = screen.getByRole("textbox", { name: "Title" });
    await userEvent.clear(title);
    await userEvent.type(title, "Hero pass v2");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(api.editMergeRequest).toHaveBeenCalledWith("/work/game", 12, "Hero pass v2", "New hero texture and a speed tweak");
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "Title" })).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Edit title and description" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  });

  it("lists the commits", async () => {
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /Commits/ }));
    const commits = await screen.findByRole("list", { name: "Commits" });
    expect(within(commits).getByText("Speed tweak")).toBeInTheDocument();
  });

  it("comments on a diff line and shows threads inline", async () => {
    api.mergeRequestActivity.mockResolvedValue(
      activity({ threads: [thread({ id: "t5", path: "src/player.go", newLine: 2 }), thread({ id: "t6", path: "src/player.go", oldLine: 9, outdated: true })] }),
    );
    api.comment.mockRejectedValueOnce(new Error("line not in diff")).mockResolvedValue(thread({ id: "t7" }));
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /File changes/ }));
    const files = await screen.findByRole("list", { name: "Changed files" });
    expect(api.revisionFiles).toHaveBeenCalledWith("/work/game", "b1", "s2");
    expect(within(files).getByLabelText("2 threads")).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Diff of src/player.go" });
    expect(within(table).getByRole("group", { name: "Thread t5" })).toBeInTheDocument();
    expect(screen.getByText("1 outdated thread on lines that changed since")).toBeInTheDocument();

    expect(within(table).getByRole("button", { name: "Comment on removed line 2" })).toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Comment on line 2" }));
    const box = await within(table).findByRole("textbox", { name: "Comment on line 2" });
    await userEvent.type(box, "Why 2?");
    await userEvent.click(within(table).getByRole("button", { name: "Comment" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not comment: line not in diff"));
    expect(within(table).getByRole("textbox", { name: "Comment on line 2" })).toHaveValue("Why 2?");
    await userEvent.click(within(table).getByRole("button", { name: "Comment" }));
    await waitFor(() => expect(within(table).queryByRole("textbox", { name: "Comment on line 2" })).not.toBeInTheDocument());
    const anchors = api.comment.mock.calls.map((c) => c[2]);
    expect(anchors.at(-1)).toEqual({ path: "src/player.go", oldLine: 0, newLine: 2, body: "Why 2?" });

    await userEvent.click(within(files).getByTitle("art/hero.png"));
    expect(await screen.findByText("Binary files take comments in the conversation, not on lines.")).toBeInTheDocument();
  });

  it("comments in the split layout and cancels a draft comment", async () => {
    localStorage.setItem("nipa.diffLayout", "split");
    api.mergeRequestActivity.mockResolvedValue(activity({ threads: [thread({ id: "t8", path: "src/player.go", oldLine: 2 })] }));
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /File changes/ }));
    const table = await screen.findByRole("table", { name: "Diff of src/player.go" });
    expect(within(table).getByRole("group", { name: "Thread t8" })).toBeInTheDocument();
    await userEvent.click(within(table).getAllByRole("button", { name: "Comment on line 1" })[0]);
    const box = within(table).getByRole("textbox", { name: "Comment on line 1" });
    await userEvent.type(box, "x{Escape}");
    expect(within(table).queryByRole("textbox", { name: "Comment on line 1" })).not.toBeInTheDocument();
  });

  it("explains missing branches, empty and failed file lists", async () => {
    api.mergeRequest.mockResolvedValueOnce(detail({}, { status: "invalid", sourceCommitId: "", mergeBaseId: "" }));
    const first = view();
    await userEvent.click(await screen.findByRole("tab", { name: /File changes/ }));
    expect(screen.getByText(/branches of this request are gone/)).toBeInTheDocument();
    first.unmount();

    api.revisionFiles.mockResolvedValueOnce([]).mockRejectedValueOnce(new Error("revision missing"));
    const second = view();
    await userEvent.click(await screen.findByRole("tab", { name: /File changes/ }));
    expect(await screen.findByText("No file changes.")).toBeInTheDocument();
    second.unmount();
    view();
    await userEvent.click(await screen.findByRole("tab", { name: /File changes/ }));
    expect(await screen.findByText("revision missing")).toBeInTheDocument();
  });

  it("reports a request or its activity that cannot be loaded", async () => {
    api.mergeRequest.mockRejectedValue(new Error("not found"));
    const first = view();
    expect(await screen.findByText("not found")).toBeInTheDocument();
    first.unmount();
    api.mergeRequest.mockResolvedValue(detail());
    api.mergeRequestActivity.mockRejectedValue(new Error("activity offline"));
    api.mergeRequestCommits.mockRejectedValue(new Error("commits offline"));
    view();
    expect(await screen.findByText("activity offline")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("tab", { name: /Commits/ }));
    expect(await screen.findByText("commits offline")).toBeInTheDocument();
  });
});
