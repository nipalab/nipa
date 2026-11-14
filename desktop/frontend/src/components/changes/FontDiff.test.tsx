import { screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, FontInfo, FontSide } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ fontDiff: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { DiffViewer, viewerFor } from "./DiffViewer";
import { FontCompareView, FontDiff } from "./FontDiff";

const info = (over: Partial<FontInfo> = {}): FontInfo => ({
  family: "Starfall",
  style: "Regular",
  fullName: "Starfall Regular",
  version: "1.0",
  glyphs: 300,
  unitsPerEm: 1000,
  ascent: 900,
  descent: 200,
  lineGap: 0,
  capHeight: 700,
  xHeight: 500,
  codepoints: 280,
  scripts: [
    { script: "Latin", count: 200 },
    { script: "Common", count: 80 },
  ],
  ranges: [{ first: 0x20, last: 0x7e }],
  ...over,
});

const side = (over: Partial<FontSide> = {}): FontSide => ({ path: "ui/Starfall.ttf", format: "ttf", size: 4096, info: info(), sheet: "data:image/png;base64,AA==", ...over });

const diff: FileDiff = {
  path: "ui/Starfall.ttf",
  status: "modified",
  kind: "font",
  old: { path: "ui/Starfall.ttf", mode: 420, size: 4096, binary: true, hash: "aa" },
  new: { path: "ui/Starfall.ttf", mode: 420, size: 4096, binary: true, hash: "bb" },
  additions: 0,
  deletions: 0,
  hunks: null,
};

afterEach(() => vi.clearAllMocks());

describe("FontCompareView", () => {
  it("shows added and removed characters, metrics and both sheets", () => {
    const ranges = Array.from({ length: 14 }, (_, i) => ({ first: 0x400 + i * 4, last: 0x401 + i * 4 }));
    renderWithProviders(
      <FontCompareView
        compare={{
          old: side(),
          new: side({ info: info({ version: "1.1", xHeight: 512, scripts: [{ script: "Greek", count: 25 }] }) }),
          changes: { added: [{ first: 0x391, last: 0x3a9 }, ...ranges], removed: [{ first: 0x41, last: 0x41 }], addedCount: 53, removedCount: 1, metrics: [{ name: "x-height", old: "500", new: "512" }], truncated: true },
        }}
      />,
    );
    const changes = screen.getByRole("region", { name: "Font changes" });
    expect(within(changes).getByText("53 characters added")).toBeInTheDocument();
    expect(within(changes).getByText("U+0391–U+03A9")).toBeInTheDocument();
    expect(within(changes).getByText("U+0041")).toBeInTheDocument();
    expect(within(changes).getByText("and 3 more ranges")).toBeInTheDocument();
    expect(within(changes).getByText("x-height")).toBeInTheDocument();
    expect(screen.getByAltText("Before glyphs")).toBeInTheDocument();
    expect(screen.getByAltText("After glyphs")).toBeInTheDocument();
    const table = screen.getByRole("table", { name: "Font details" });
    expect(within(table).getByText("512")).toHaveClass("text-warning");
    expect(screen.getByText("Scripts (after)")).toBeInTheDocument();
    expect(screen.getByText("Greek")).toBeInTheDocument();
    expect(screen.getByText("Only the first changed ranges are listed.")).toBeInTheDocument();
  });

  it("says when only outlines changed and lists side problems", () => {
    renderWithProviders(
      <FontCompareView
        compare={{
          old: side({ info: info({ rangesTruncated: true }) }),
          new: side({ info: null, sheet: "", error: "unsupported format: WOFF2 fonts" }),
          changes: { added: [], removed: [], addedCount: 0, removedCount: 0, metrics: [] },
        }}
      />,
    );
    expect(screen.getByText("Same characters and metrics; only the outlines or tables changed.")).toBeInTheDocument();
    expect(screen.getAllByText("unsupported format: WOFF2 fonts")[0]).toBeInTheDocument();
    expect(screen.getByText("After: unsupported format: WOFF2 fonts")).toBeInTheDocument();
    expect(screen.getByText("Before: only the first character ranges are listed.")).toBeInTheDocument();
  });

  it("renders an added font alone", () => {
    renderWithProviders(<FontCompareView compare={{ old: null, new: side({ info: info({ scripts: [] }) }), changes: null }} />);
    expect(screen.getByRole("region", { name: "Added" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Font changes" })).not.toBeInTheDocument();
    expect(screen.queryByText(/^Scripts/)).not.toBeInTheDocument();
  });
});

describe("FontDiff", () => {
  const show = () => renderWithProviders(<DiffViewer root="/work/game" diff={diff} layout="unified" onLayout={() => {}} />);

  it("picks the font viewer and loads both sides", async () => {
    expect(viewerFor(diff)).toBe(FontDiff);
    api.fontDiff.mockResolvedValue({ old: side(), new: side(), changes: null });
    show();
    expect(screen.getByText("Reading font…")).toBeInTheDocument();
    expect(await screen.findByRole("table", { name: "Font details" })).toBeInTheDocument();
    expect(api.fontDiff).toHaveBeenCalledWith("/work/game", { source: "snapshot", path: "ui/Starfall.ttf" }, { source: "working", path: "ui/Starfall.ttf" });
  });

  it("falls back to metadata on errors and empty results", async () => {
    api.fontDiff.mockRejectedValueOnce(new Error("daemon gone"));
    const { unmount } = show();
    expect(await screen.findByText("The font could not be read: daemon gone")).toBeInTheDocument();
    unmount();
    api.fontDiff.mockResolvedValueOnce(null);
    show();
    expect(await screen.findByText("There is no preview for this font.")).toBeInTheDocument();
  });
});
