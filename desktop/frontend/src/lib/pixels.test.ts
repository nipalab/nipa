import { afterEach, describe, expect, it, vi } from "vitest";
import { drawPixels, loadPixels } from "./pixels";

class FakeImage {
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  naturalWidth = 2;
  naturalHeight = 1;
  set src(value: string) {
    queueMicrotask(() => (value === "bad" ? this.onerror?.() : this.onload?.()));
  }
}

const data = new Uint8ClampedArray([1, 2, 3, 4, 5, 6, 7, 8]);

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("loadPixels", () => {
  it("reads the decoded image through a canvas", async () => {
    vi.stubGlobal("Image", FakeImage);
    const ctx = { drawImage: vi.fn(), getImageData: vi.fn(() => ({ data })) };
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(ctx as never);
    await expect(loadPixels("data:x")).resolves.toEqual({ width: 2, height: 1, data });
    expect(ctx.getImageData).toHaveBeenCalledWith(0, 0, 2, 1);
  });

  it("fails without a canvas or on a broken image", async () => {
    vi.stubGlobal("Image", FakeImage);
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
    await expect(loadPixels("data:x")).rejects.toThrow("Canvas is not available");
    await expect(loadPixels("bad")).rejects.toThrow("could not be decoded");
  });
});

describe("drawPixels", () => {
  it("puts the pixels on the canvas", () => {
    const image = { data: new Uint8ClampedArray(8) };
    const ctx = { createImageData: vi.fn(() => image), putImageData: vi.fn() };
    const canvas = document.createElement("canvas");
    vi.spyOn(canvas, "getContext").mockReturnValue(ctx as never);
    drawPixels(canvas, { width: 2, height: 1, data });
    expect(canvas.width).toBe(2);
    expect([...image.data]).toEqual([...data]);
    expect(ctx.putImageData).toHaveBeenCalledWith(image, 0, 0);
  });

  it("does nothing without a context", () => {
    const canvas = document.createElement("canvas");
    vi.spyOn(canvas, "getContext").mockReturnValue(null);
    expect(() => drawPixels(canvas, { width: 2, height: 1, data })).not.toThrow();
  });
});
