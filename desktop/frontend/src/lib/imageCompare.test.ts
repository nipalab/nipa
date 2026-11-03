import { describe, expect, it } from "vitest";
import { allChannels, clampZoom, fitZoom, formatPercent, heatmap, isolateChannels, maxZoom, minZoom, stepZoom, type Pixels } from "./imageCompare";

const px = (width: number, height: number, ...rgba: number[][]): Pixels => ({ width, height, data: new Uint8ClampedArray(rgba.flat()) });

describe("isolateChannels", () => {
  const src = px(2, 1, [10, 20, 30, 40], [50, 60, 70, 80]);

  it("keeps the image when every channel is on", () => {
    expect(isolateChannels(src, allChannels)).toBe(src);
  });

  it("shows a single channel as opaque grayscale", () => {
    expect([...isolateChannels(src, { r: false, g: true, b: false, a: false }).data]).toEqual([20, 20, 20, 255, 60, 60, 60, 255]);
    expect([...isolateChannels(src, { r: false, g: false, b: false, a: true }).data]).toEqual([40, 40, 40, 255, 80, 80, 80, 255]);
  });

  it("zeroes hidden colours and drops a hidden alpha", () => {
    expect([...isolateChannels(src, { r: true, g: false, b: true, a: false }).data]).toEqual([10, 0, 30, 255, 50, 0, 70, 255]);
    expect([...isolateChannels(src, { r: true, g: true, b: false, a: true }).data]).toEqual([10, 20, 0, 40, 50, 60, 0, 80]);
  });
});

describe("heatmap", () => {
  it("marks changed pixels and dims the rest", () => {
    const a = px(2, 1, [0, 0, 0, 255], [100, 100, 100, 255]);
    const b = px(2, 1, [0, 0, 0, 255], [100, 100, 255, 255]);
    const h = heatmap(a, b, 0);
    expect(h.changed).toBe(1);
    expect(h.total).toBe(2);
    expect([...h.pixels.data.slice(0, 4)]).toEqual([24, 24, 24, 255]);
    const [r, g, bl] = h.pixels.data.slice(4, 8);
    expect([r, bl]).toEqual([255, 0]);
    expect(g).toBeGreaterThan(0);
    expect(heatmap(a, b, 155).changed).toBe(0);
  });

  it("treats transparent pixels as equal and missing pixels as changed", () => {
    const a = px(1, 1, [255, 0, 0, 0]);
    const b = px(2, 2, [0, 255, 0, 0], [9, 9, 9, 255], [9, 9, 9, 255], [9, 9, 9, 255]);
    const h = heatmap(a, b, 0);
    expect(h.pixels.width).toBe(2);
    expect(h.changed).toBe(3);
    const wide = heatmap(px(2, 1, [0, 0, 0, 255], [255, 255, 255, 255]), px(1, 1, [0, 0, 0, 255]), 0);
    expect(wide.changed).toBe(1);
    expect([...wide.pixels.data.slice(4, 8)]).toEqual([255, 0, 0, 255]);
  });

  it("dims an unchanged pixel from the old side when only it has one", () => {
    const h = heatmap(px(2, 1, [200, 200, 200, 255], [0, 0, 0, 0]), px(1, 1, [200, 200, 200, 255]), 255);
    expect(h.changed).toBe(0);
    expect(h.pixels.data[4]).toBe(24);
  });
});

describe("zoom", () => {
  it("fits inside the box and enlarges small images up to 800%", () => {
    expect(fitZoom({ width: 432, height: 1000 }, { width: 800, height: 100 })).toBe(0.5);
    expect(fitZoom({ width: 2000, height: 2000 }, { width: 10, height: 10 })).toBe(8);
    expect(fitZoom({ width: 132, height: 1000 }, { width: 50, height: 10 })).toBe(2);
    expect(fitZoom({ width: 0, height: 0 }, { width: 10, height: 10 })).toBe(1);
    expect(fitZoom({ width: 100, height: 100 }, { width: 0, height: 10 })).toBe(1);
  });

  it("clamps and steps", () => {
    expect(clampZoom(100)).toBe(maxZoom);
    expect(clampZoom(0)).toBe(minZoom);
    expect(stepZoom(1, 1)).toBe(1.5);
    expect(stepZoom(1, -1)).toBe(0.75);
    expect(stepZoom(0.6, 1)).toBe(0.75);
    expect(stepZoom(maxZoom, 1)).toBe(maxZoom);
    expect(stepZoom(minZoom, -1)).toBe(minZoom);
  });
});

describe("formatPercent", () => {
  it.each([
    [0, "0%"],
    [0.00001, "<0.01%"],
    [0.0123, "1.23%"],
    [0.5, "50.0%"],
  ])("formats %d", (ratio, text) => expect(formatPercent(ratio)).toBe(text));
});
