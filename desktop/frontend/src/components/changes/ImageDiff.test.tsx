import { useState } from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, ImageCompare, ImageInfo, ImageLayer } from "@/api/desktop";
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
  encoding: "",
  levels: 1,
  faces: 1,
  cube: false,
  level: 0,
  face: 0,
  hdr: false,
  layers: null,
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
    expect(api.imageDiff).toHaveBeenCalledWith("/r", { source: "snapshot", path: "art/hero.png" }, { source: "working", path: "art/hero.png" }, { level: 0, face: 0 });
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

  it("reports a resize instead of zeroed pixel statistics", async () => {
    const user = userEvent.setup();
    const resized: ImageCompare = {
      old: info(),
      new: info({ src: "data:new", width: 8 }),
      stats: { ...compare.stats!, changedPixels: 0, changedRatio: 0, boundsWidth: 0, sizeChanged: true },
    };
    renderWithProviders(<ImageCompareView compare={resized} />);
    expect(screen.getByText("Resized")).toBeInTheDocument();
    expect(screen.queryByText("0% changed")).not.toBeInTheDocument();
    expect(screen.queryByText("Changed pixels")).not.toBeInTheDocument();
    expect(screen.getByText("Dimensions changed, so pixels are not compared.")).toBeInTheDocument();
    await user.click(modeButton("Difference"));
    expect(screen.queryByLabelText("Changed region")).not.toBeInTheDocument();
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

describe("texture surfaces, HDR and layers", () => {
  const texture = (over: Partial<ImageInfo> = {}) => info({ format: "dds", encoding: "BC7_UNORM", levels: 3, faces: 6, cube: true, width: 4, height: 2, ...over });

  it("picks mip levels and cube faces and keeps the last preview while loading", async () => {
    const user = userEvent.setup();
    api.imageDiff.mockImplementation(async (_root, _old, _next, opts: { level: number; face: number }) => ({
      old: texture({ level: opts.level, width: 4 >> opts.level || 1, height: 2 >> opts.level || 1 }),
      new: texture({ src: "data:new", level: opts.level, width: 4 >> opts.level || 1, height: 2 >> opts.level || 1, face: opts.face }),
      stats: null,
    }));
    function Switcher() {
      const [path, setPath] = useState("sky.dds");
      return (
        <>
          <button type="button" onClick={() => setPath("other.dds")}>
            switch
          </button>
          <DiffViewer root="/r" diff={imageDiff({ path })} layout="unified" onLayout={vi.fn()} />
        </>
      );
    }
    renderWithProviders(<Switcher />);
    const mip = await screen.findByLabelText("Mip level");
    expect(within(mip).getAllByRole("option").map((o) => o.textContent)).toEqual(["Mip 0 · 4×2", "Mip 1 · 2×1", "Mip 2 · 1×1"]);
    expect(screen.getByRole("table", { name: "Image details" })).toHaveTextContent("BC7_UNORM");
    expect(screen.getByRole("table", { name: "Image details" })).toHaveTextContent("Mip levels");

    await user.selectOptions(mip, "1");
    await waitFor(() => expect(api.imageDiff).toHaveBeenLastCalledWith("/r", expect.anything(), expect.anything(), { level: 1, face: 0 }));
    expect(screen.queryByText("Decoding image…")).not.toBeInTheDocument();
    const face = screen.getByLabelText("Face");
    expect(within(face).getAllByRole("option")[1]).toHaveTextContent("−X");
    await user.selectOptions(face, "4");
    await waitFor(() => expect(api.imageDiff).toHaveBeenLastCalledWith("/r", expect.anything(), expect.anything(), { level: 1, face: 4 }));
    expect(await screen.findByDisplayValue("+Z")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "switch" }));
    await waitFor(() => expect(api.imageDiff).toHaveBeenLastCalledWith("/r", expect.anything(), expect.anything(), { level: 0, face: 0 }));
  });

  it("labels texture array layers", () => {
    const onSurface = vi.fn();
    renderWithProviders(<ImageCompareView compare={{ old: null, new: texture({ cube: false, faces: 3, levels: 1 }), stats: null }} onSurface={onSurface} />);
    const layer = screen.getByLabelText("Layer");
    expect(within(layer).getAllByRole("option").map((o) => o.textContent)).toEqual(["Layer 0", "Layer 1", "Layer 2"]);
    fireEvent.change(layer, { target: { value: "2" } });
    expect(onSurface).toHaveBeenCalledWith({ level: 0, face: 2 });
  });

  it("names faces of cube arrays", () => {
    renderWithProviders(<ImageCompareView compare={{ old: null, new: texture({ faces: 12, levels: 1 }), stats: null }} />);
    expect(within(screen.getByLabelText("Face")).getAllByRole("option")[7]).toHaveTextContent("Layer 1 −X");
  });

  it("tone maps HDR previews with exposure", async () => {
    const user = userEvent.setup();
    pixels.loadPixels.mockResolvedValue({ width: 2, height: 1, data: new Uint8ClampedArray([128, 128, 128, 255, 129, 255, 0, 255]) });
    renderWithProviders(<ImageCompareView compare={{ old: null, new: info({ hdr: true, width: 1, height: 1, previewWidth: 1, previewHeight: 1 }), stats: null }} />);
    await waitFor(() => expect(pixels.drawPixels).toHaveBeenCalled());
    const first = pixels.drawPixels.mock.calls.at(-1)![1];
    expect(first.width).toBe(1);
    expect(first.data[0]).toBe(255);

    fireEvent.change(screen.getByLabelText("Exposure"), { target: { value: "-2" } });
    await waitFor(() => expect(pixels.drawPixels.mock.calls.at(-1)![1].data[0]).toBeLessThan(255));
    const reset = screen.getByRole("button", { name: "-2.00 EV" });
    await user.click(reset);
    expect(screen.getByRole("button", { name: "0.00 EV" })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Exposure"), { target: { value: "1.5" } });
    expect(screen.getByRole("button", { name: "+1.50 EV" })).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Tone map"), "aces");
    expect(screen.getByLabelText("Tone map")).toHaveValue("aces");
  });

  it("lists layer changes for Photoshop files", () => {
    const layer = (name: string, over: Partial<ImageLayer> = {}): ImageLayer => ({
      name, kind: "layer", depth: 0, visible: true, opacity: 255, blend: "norm", x: 0, y: 0, width: 4, height: 2, hash: name, ...over,
    });
    renderWithProviders(
      <ImageCompareView
        compare={{
          old: info({ layers: [layer("Hero"), layer("Old")] }),
          new: info({ layers: [layer("Group", { kind: "group" }), layer("Hero", { hash: "x", depth: 1, visible: false }), layer("New")] }),
          stats: null,
        }}
      />,
    );
    const list = screen.getByRole("list", { name: "Layers" });
    expect(screen.getByText("Layers · 4 changed")).toBeInTheDocument();
    const items = within(list).getAllByRole("listitem");
    expect(items.map((i) => i.textContent)).toEqual(["AGroup", "MHeropixels, hidden, moved", "DOld", "ANew"]);
    expect(items[1]).toHaveStyle({ paddingLeft: "12px" });
    expect(within(items[1]).getByLabelText("Hidden")).toBeInTheDocument();
    expect(items[2]).toHaveClass("line-through");
  });

  it("says when no layer changed", () => {
    const l: ImageLayer = { name: "A", kind: "layer", depth: 0, visible: true, opacity: 255, blend: "norm", x: 0, y: 0, width: 1, height: 1, hash: "h" };
    renderWithProviders(<ImageCompareView compare={{ old: info({ layers: [l] }), new: null, stats: null }} />);
    expect(screen.getByText("Layers · 1 changed")).toBeInTheDocument();
    renderWithProviders(<ImageCompareView compare={{ old: info({ layers: [l] }), new: info({ layers: [l] }), stats: null }} />);
    expect(screen.getByText("Layers · no layer changes")).toBeInTheDocument();
  });
});
