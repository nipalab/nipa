import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { FileDiff, FileSide } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";
import { DiffViewer } from "./DiffViewer";

const side = (over: Partial<FileSide> = {}): FileSide => ({ path: "a.txt", mode: 420, size: 10, binary: false, hash: "", ...over });

const diff = (over: Partial<FileDiff>): FileDiff => ({
  path: "a.txt",
  status: "modified",
  kind: "text",
  old: side(),
  new: side(),
  additions: 0,
  deletions: 0,
  hunks: [],
  ...over,
});

const show = (d: FileDiff) => renderWithProviders(<DiffViewer root="/r" diff={d} layout="unified" onLayout={vi.fn()} />);

describe("DiffViewer notices", () => {
  it.each([
    [{ contentUnavailable: true }, "Content not available"],
    [{ hunksOmitted: true, old: null, new: side({ size: 5 << 20 }) }, "This diff is too large to show"],
    [{ status: "deleted", new: null }, "File deleted"],
    [{ status: "renamed", oldPath: "b.txt", similarity: 100 }, "File renamed"],
    [{ new: side({ mode: 493 }) }, "File mode changed"],
    [{ hunks: null }, "Empty file"],
    [{ kind: "scene", hunks: null, old: side({ size: 0 }), new: side({ size: 0 }) }, "Empty file"],
  ] as [Partial<FileDiff>, string][])("explains %o", (over, title) => {
    show(diff(over));
    expect(screen.getByText(title)).toBeInTheDocument();
  });

  it("shows renames, sizes and mode changes", () => {
    show(diff({ status: "renamed", oldPath: "b.txt", similarity: 87 }));
    expect(screen.getByText("b.txt")).toBeInTheDocument();
    expect(screen.getByText("87% similar")).toBeInTheDocument();
    expect(screen.getAllByText("10 B")).toHaveLength(2);
  });

  it("marks approximate counts", () => {
    show(diff({ hunksOmitted: true, countsApproximate: true, additions: 12, deletions: 3 }));
    expect(screen.getByText("~+12")).toBeInTheDocument();
    expect(screen.getByText("~−3")).toBeInTheDocument();
    expect(screen.getByTitle(/Estimated: the file is too large to diff/)).toBeInTheDocument();
  });

  it("shows exact counts without a marker", () => {
    show(diff({ additions: 2, deletions: 1 }));
    expect(screen.getByText("+2")).toBeInTheDocument();
    expect(screen.getByText("−1")).toBeInTheDocument();
    expect(screen.queryByTitle(/Estimated/)).not.toBeInTheDocument();
  });

  it("describes binary kinds and missing sides", () => {
    show(diff({ kind: "opaque", status: "added", untracked: true, old: null, new: side({ binary: true, size: 2048 }) }));
    expect(screen.getByText("Binary file")).toBeInTheDocument();
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.getByLabelText("New, not tracked yet")).toBeInTheDocument();
  });

  it("falls back for unknown kinds and statuses", () => {
    show(diff({ kind: "mystery", status: "copied", old: side({ binary: true }), new: null }));
    expect(screen.getByText("Binary file")).toBeInTheDocument();
    expect(screen.getByLabelText("copied")).toBeInTheDocument();
  });

  it("omits sizes when both sides are missing", () => {
    show(diff({ kind: "audio", old: null, new: null, contentUnavailable: true }));
    expect(screen.queryByText("Before")).not.toBeInTheDocument();
  });
});
