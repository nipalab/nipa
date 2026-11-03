import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, ImageCompare, ImageInfo } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ imageDiff: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));
const pixels = vi.hoisted(() => ({ loadPixels: vi.fn(), drawPixels: vi.fn() }));
vi.mock("@/lib/pixels", () => pixels);

import { DiffViewer } from "./DiffViewer";
import { ImageCompareView, imageRefs } from "./ImageDiff";

const info = (over: Partial<ImageInfo> = {}): ImageInfo => ({
  width: 4,
  height: 2,
  format: "png",
  bitDepth: 8,
  hasAlpha: false,
  size: 100,
  previewWidth: 4,
  previewHeight: 2,
  src: "data:old",
  ...over,
});

const compare: ImageCompare = {
  old: info(),
  new: info({ src: "data:new", format: "tga", hasAlpha: true, size: 2048 }),
  stats: {
    changedPixels: 2,
    totalPixels: 8,
    changedRatio: 0.25,
    maxDelta: 200,
    boundsX: 1,
    boundsY: 0,
    boundsWidth: 2,
    boundsHeight: 1,
    sizeChanged: false,
    formatChanged: true,
    bitDepthChanged: false,
    alphaChanged: true,
  },
};

const solid = (v: number) => ({ width: 4, height: 2, data: new Uint8ClampedArray(32).fill(v) });

const imageDiff = (over: Partial<FileDiff> = {}): FileDiff => ({
  path: "art/hero.png",
  status: "modified",
  kind: "image",
  old: { path: "art/hero.png", mode: 420, size: 100, binary: true, hash: "aa" },
  new: { path: "art/hero.png", mode: 420, size: 2048, binary: true, hash: "" },
  additions: 0,
  deletions: 0,
  hunks: [],
  ...over,
});

beforeEach(() => {
  vi.resetAllMocks();
  pixels.loadPixels.mockImplementation(async (src: string) => solid(src === "data:old" ? 0 : 255));
});

afterEach(() => vi.useRealTimers());

const modeButton = (name: string) => within(screen.getByRole("group", { name: "Compare mode" })).getByRole("button", { name });

describe("imageRefs", () => {
  it("compares the snapshot with the working file", () => {
    expect(imageRefs(imageDiff())).toEqual({
      old: { source: "snapshot", path: "art/hero.png" },
      next: { source: "working", path: "art/hero.png" },
    });
    expect(imageRefs(imageDiff({ oldPath: "a.png", old: { path: "", mode: 0, size: 0, binary: true, hash: "" }, new: null }))).toEqual({
      old: { source: "snapshot", path: "a.png" },
      next: null,
    });
    expect(imageRefs(imageDiff({ old: null, new: { path: "", mode: 0, size: 0, binary: true, hash: "" } })).next?.path).toBe("art/hero.png");
    expect(imageRefs(imageDiff({ old: { path: "", mode: 0, size: 0, binary: true, hash: "" } })).old?.path).toBe("art/hero.png");
  });
});

describe("ImageDiff in the viewer registry", () => {
  it("loads previews and shows details", async () => {
    api.imageDiff.mockResolvedValue(compare);
    renderWithProviders(<DiffViewer root="/r" diff={imageDiff()} layout="unified" onLayout={vi.fn()} />);
    expect(screen.getByText("Decoding image…")).toBeInTheDocument();
    expect(await screen.findByText("25.0% changed")).toBeInTheDocument();
    expect(api.imageDiff).toHaveBeenCalledWith("/r", { source: "snapshot", path: "art/hero.png" }, { source: "working", path: "art/hero.png" });
    const table = screen.getByRole("table", { name: "Image details" });
    expect(within(table).getByText("TGA")).toHaveClass("text-warning");
    expect(within(table).getByText("2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("2 × 1 at 1, 0")).toBeInTheDocument();
    await waitFor(() => expect(pixels.drawPixels).toHaveBeenCalledTimes(2));
  });

  it("falls back to metadata when the format has no preview", async () => {
    api.imageDiff.mockResolvedValue({ old: null, new: null, stats: null, unsupported: "hero.dds: unsupported image format" });
    renderWithProviders(<DiffViewer root="/r" diff={imageDiff({ path: "hero.dds" })} layout="unified" onLayout={vi.fn()} />);
    expect(await screen.findByText("hero.dds: unsupported image format")).toBeInTheDocument();
    expect(screen.getByText("Image file")).toBeInTheDocument();
  });

  it("explains failures and empty answers", async () => {
    api.imageDiff.mockRejectedValue(new Error("daemon gone"));
    const { unmount } = renderWithProviders(<DiffViewer root="/r" diff={imageDiff()} layout="unified" onLayout={vi.fn()} />);
    expect(await screen.findByText("The preview failed: daemon gone")).toBeInTheDocument();
    unmount();
    api.imageDiff.mockResolvedValue(null);
    renderWithProviders(<DiffViewer root="/r" diff={imageDiff()} layout="unified" onLayout={vi.fn()} />);
    expect(await screen.findByText("There is no preview for this image.")).toBeInTheDocument();
  });

  it("keeps the notice for an image missing from the cache", () => {
    renderWithProviders(<DiffViewer root="/r" diff={imageDiff({ contentUnavailable: true })} layout="unified" onLayout={vi.fn()} />);
    expect(screen.getByText("Content not available")).toBeInTheDocument();
    expect(api.imageDiff).not.toHaveBeenCalled();
  });
});

describe("ImageCompareView", () => {
  it("switches modes and remembers the choice", async () => {
    const user = userEvent.setup();
    const { unmount } = renderWithProviders(<ImageCompareView compare={compare} />);
    expect(screen.getAllByTestId("image-pane")).toHaveLength(2);
    expect(modeButton("Side by side")).toHaveAttribute("aria-pressed", "true");

    await user.click(modeButton("Swipe"));
    expect(screen.getAllByTestId("image-pane")).toHaveLength(1);
    const swipe = screen.getByLabelText("Swipe position");
    fireEvent.change(swipe, { target: { value: "30" } });
    expect(screen.getByRole("img", { name: "After" }).parentElement).toHaveStyle({ clipPath: "inset(0 0 0 30%)" });

    await user.click(modeButton("Onion skin"));
    fireEvent.change(screen.getByLabelText("After opacity"), { target: { value: "20" } });
    expect(screen.getByRole("img", { name: "After" }).parentElement).toHaveStyle({ opacity: "0.2" });

    await user.click(modeButton("Difference"));
    expect(await screen.findByText("100.0% above tolerance")).toBeInTheDocument();
    expect(screen.getByLabelText("Changed region")).toHaveStyle({ left: "25%", width: "50%" });
    fireEvent.change(screen.getByLabelText("Tolerance"), { target: { value: "255" } });
    expect(await screen.findByText("0% above tolerance")).toBeInTheDocument();

    unmount();
    renderWithProviders(<ImageCompareView compare={compare} />);
    expect(modeButton("Difference")).toHaveAttribute("aria-pressed", "true");
  });

  it("blinks between both versions", async () => {
    localStorage.setItem("nipa.imageMode", "blink");
    vi.useFakeTimers();
    renderWithProviders(<ImageCompareView compare={compare} />);
    expect(within(screen.getByTestId("image-pane")).getByText("After")).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(700));
    expect(within(screen.getByTestId("image-pane")).getByText("Before")).toBeInTheDocument();
  });

  it("toggles channels", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ImageCompareView compare={compare} />);
    await waitFor(() => expect(pixels.drawPixels).toHaveBeenCalledTimes(2));
    const red = screen.getByRole("button", { name: "R channel" });
    await user.click(red);
    expect(red).toHaveAttribute("aria-pressed", "false");
    await waitFor(() => expect(pixels.drawPixels).toHaveBeenCalledTimes(4));
  });

  it("zooms and pans both panes together", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ImageCompareView compare={compare} />);
    const level = screen.getByLabelText("Zoom level");
    expect(level).toHaveTextContent("800%");
    await user.click(screen.getByRole("button", { name: "Actual size" }));
    expect(level).toHaveTextContent("100%");
    await user.click(screen.getByRole("button", { name: "Zoom in" }));
    expect(level).toHaveTextContent("150%");
    await user.click(screen.getByRole("button", { name: "Zoom out" }));
    expect(level).toHaveTextContent("100%");

    const [pane] = screen.getAllByTestId("image-pane");
    fireEvent.wheel(pane, { deltaY: -1 });
    expect(level).toHaveTextContent("115%");
    fireEvent.wheel(pane, { deltaY: 1 });
    expect(level).toHaveTextContent("100%");

    fireEvent.pointerDown(pane, { clientX: 10, clientY: 10, pointerId: 1 });
    fireEvent.pointerMove(pane, { clientX: 30, clientY: 15, pointerId: 1 });
    fireEvent.pointerUp(pane);
    fireEvent.pointerMove(pane, { clientX: 90, clientY: 90 });
    for (const frame of screen.getAllByTestId("image-frame")) {
      expect(frame.style.transform).toBe("translate(calc(-50% + 20px), calc(-50% + 5px))");
    }
    await user.click(screen.getByRole("button", { name: "Zoom in" }));
    expect(screen.getAllByTestId("image-frame")[1].style.transform).toBe("translate(calc(-50% + 30px), calc(-50% + 7.5px))");

    await user.click(screen.getByRole("button", { name: "Actual size" }));
    expect(level).toHaveTextContent("100%");
    await user.click(screen.getByRole("button", { name: "Fit" }));
    expect(screen.getByRole("button", { name: "Fit" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getAllByTestId("image-frame")[0].style.transform).toBe("translate(calc(-50% + 0px), calc(-50% + 0px))");
  });

  it("pixelates when zoomed past the preview resolution", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ImageCompareView compare={{ ...compare, old: info({ previewWidth: 2 }) }} />);
    expect(screen.getByRole("img", { name: "After" })).toHaveStyle({ imageRendering: "pixelated" });
    await user.click(screen.getByRole("button", { name: "Actual size" }));
    expect(screen.getByRole("img", { name: "Before" })).toHaveStyle({ imageRendering: "pixelated" });
    expect(screen.getByRole("img", { name: "After" })).toHaveStyle({ imageRendering: "auto" });
    await user.click(screen.getByRole("button", { name: "Zoom in" }));
    await user.click(screen.getByRole("button", { name: "Zoom in" }));
    expect(screen.getByRole("img", { name: "After" })).toHaveStyle({ imageRendering: "pixelated" });
  });

  it("shows a single version for added and deleted images", () => {
    pixels.loadPixels.mockRejectedValue(new Error("bad"));
    const { unmount } = renderWithProviders(<ImageCompareView compare={{ old: null, new: info({ bitDepth: 0 }), stats: null }} />);
    expect(screen.queryByRole("group", { name: "Compare mode" })).not.toBeInTheDocument();
    expect(screen.getByText("Added")).toBeInTheDocument();
    expect(screen.queryByText("Changed pixels")).not.toBeInTheDocument();
    expect(within(screen.getByRole("table", { name: "Image details" })).getAllByText("—").length).toBeGreaterThan(5);
    unmount();
    renderWithProviders(<ImageCompareView compare={{ old: info(), new: null, stats: { ...compare.stats!, boundsWidth: 0 } }} />);
    expect(screen.getByText("Deleted")).toBeInTheDocument();
    expect(screen.getByText("none")).toBeInTheDocument();
  });

  it("survives storage failures", async () => {
    const user = userEvent.setup();
    const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    renderWithProviders(<ImageCompareView compare={compare} />);
    await user.click(modeButton("Onion skin"));
    expect(modeButton("Onion skin")).toHaveAttribute("aria-pressed", "true");
    get.mockRestore();
    set.mockRestore();
  });
});
