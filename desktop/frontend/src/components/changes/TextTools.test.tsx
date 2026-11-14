import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Blame, FileDiff, FileSide } from "@/api/desktop";
import { RepoNavContext, type RepoNav } from "@/hooks/useTextTools";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ blame: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { DiffViewer } from "./DiffViewer";
import type { DiffLayout } from "./TextDiff";

const side = (over: Partial<FileSide> = {}): FileSide => ({ path: "src/a.cs", mode: 420, size: 30, binary: false, hash: "", ...over });

const diff = (over: Partial<FileDiff> = {}): FileDiff => ({
  path: "src/a.cs",
  status: "modified",
  kind: "text",
  old: side(),
  new: side(),
  additions: 1,
  deletions: 1,
  hunks: [
    {
      oldStart: 1,
      oldLines: 2,
      newStart: 1,
      newLines: 2,
      lines: [
        { kind: "context", old: 1, new: 1, text: "class A {" },
        { kind: "removed", old: 2, new: 0, text: "  int x;" },
        { kind: "added", old: 0, new: 2, text: "  int y;" },
      ],
    },
  ],
  ...over,
});

const blame = (over: Partial<Blame> = {}): Blame => ({
  path: "src/a.cs",
  revision: "",
  truncated: false,
  commits: [{ id: "c1", hash: "h1", parents: [], author: "Ana", email: "ana@x", message: "Add A", date: new Date().toISOString() }],
  lines: [
    { commitId: "c1", uncommitted: false },
    { commitId: "", uncommitted: true },
  ],
  ...over,
});

const nav = { showCommit: vi.fn(), openAt: vi.fn() };

const show = (d: FileDiff, layout: DiffLayout = "unified", value: RepoNav = nav) =>
  renderWithProviders(
    <RepoNavContext.Provider value={value}>
      <DiffViewer root="/r" diff={d} layout={layout} onLayout={vi.fn()} />
    </RepoNavContext.Provider>,
  );

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  api.blame.mockResolvedValue(blame());
});

describe("blame gutter", () => {
  it("toggles blame and opens the commit of a line", async () => {
    show(diff());
    const toggle = screen.getByRole("button", { name: "Blame" });
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(api.blame).not.toHaveBeenCalled();

    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-pressed", "true");
    expect(localStorage.getItem("nipa.blame")).toBe("on");
    const owner = await screen.findByRole("button", { name: /Show the commit that last changed this line: c1 Add A/ });
    expect(owner).toHaveTextContent("Ana · just now");
    expect(api.blame).toHaveBeenCalledWith("/r", "src/a.cs", "");
    expect(screen.getByText("Not pushed")).toBeInTheDocument();

    await userEvent.click(owner);
    expect(nav.showCommit).toHaveBeenCalledWith("c1");

    await userEvent.click(toggle);
    expect(localStorage.getItem("nipa.blame")).toBeNull();
    expect(screen.queryByText("Not pushed")).not.toBeInTheDocument();
  });

  it("blames the revision of a commit diff in split layout", async () => {
    localStorage.setItem("nipa.blame", "on");
    show(diff({ newRevision: "c9", oldRevision: "c8" }), "split");
    expect(await screen.findByText("Not pushed")).toBeInTheDocument();
    expect(api.blame).toHaveBeenCalledWith("/r", "src/a.cs", "c9");
    expect(screen.queryByRole("button", { name: /Open in editor/ })).not.toBeInTheDocument();
  });

  it("shows lines without a readable commit as plain text", async () => {
    localStorage.setItem("nipa.blame", "on");
    api.blame.mockResolvedValue(blame({ lines: [{ commitId: "", uncommitted: false }, { commitId: "c1", uncommitted: false }] }));
    show(diff(), "unified", {});
    expect(await screen.findByText("Older")).toBeInTheDocument();
    expect(screen.getByText(/Ana ·/).tagName).toBe("SPAN");
  });

  it("reports blame failures on the toggle", async () => {
    localStorage.setItem("nipa.blame", "on");
    api.blame.mockRejectedValue(new Error("a.cs is not a text file"));
    show(diff());
    await waitFor(() => expect(screen.getByRole("button", { name: "Blame" })).toHaveAttribute("title", "Blame is not available: a.cs is not a text file"));
  });

  it("is not offered for binary or deleted files", () => {
    show(diff({ kind: "image", new: side({ binary: true }), old: side({ binary: true }) }));
    expect(screen.queryByRole("button", { name: "Blame" })).not.toBeInTheDocument();
  });
});

describe("open in editor", () => {
  it("opens the working file at a new-side line", async () => {
    show(diff());
    const header = screen.getByRole("table", { name: "Diff of src/a.cs" });
    expect(within(header).getAllByRole("button", { name: /Open in editor/ })).toHaveLength(2);
    await userEvent.click(within(header).getByRole("button", { name: "Open in editor at line 2" }));
    expect(nav.openAt).toHaveBeenCalledWith("src/a.cs", 2);
  });

  it("opens from the new half of a split diff", async () => {
    show(diff(), "split");
    await userEvent.click(screen.getByRole("button", { name: "Open in editor at line 1" }));
    expect(nav.openAt).toHaveBeenCalledWith("src/a.cs", 1);
  });

  it("needs a way to open files", () => {
    show(diff(), "unified", {});
    expect(screen.queryByRole("button", { name: /Open in editor/ })).not.toBeInTheDocument();
  });
});
