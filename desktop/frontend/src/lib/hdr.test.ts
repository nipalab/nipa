import { describe, expect, it } from "vitest";
import { tonemap, unpackHdr } from "./hdr";

describe("unpackHdr", () => {
  it("rebuilds floats and alpha from the packed halves", () => {
    const packed = { width: 4, height: 1, data: new Uint8ClampedArray([128, 64, 0, 255, 0, 0, 0, 255, 129, 128, 0, 255, 0, 255, 0, 255]) };
    const f = unpackHdr(packed);
    expect(f.width).toBe(2);
    expect(f.data[0]).toBeCloseTo(1.0039, 3);
    expect(f.data[1]).toBeCloseTo(0.5039, 3);
    expect(f.data[3]).toBeCloseTo(128 / 255);
    expect([...f.data.slice(4, 8)]).toEqual([0, 0, 0, 1]);
  });
});

describe("tonemap", () => {
  const src = { width: 2, height: 1, data: new Float32Array([0.5, 2, -1, 1, 0.0018, 0, 0, 0.5]) };

  it("clamps with exposure and encodes sRGB", () => {
    expect([...tonemap(src, 0, "clamp").data]).toEqual([188, 255, 0, 255, 6, 0, 0, 128]);
    expect([...tonemap(src, -1, "clamp").data.slice(0, 3)]).toEqual([137, 255, 0]);
  });

  it("compresses highlights with Reinhard and ACES", () => {
    expect([...tonemap(src, 0, "reinhard").data.slice(0, 2)]).toEqual([156, 213]);
    const aces = tonemap(src, 0, "aces").data;
    expect(aces[1]).toBeGreaterThan(aces[0]);
    expect(aces[1]).toBeLessThan(255);
  });
});
