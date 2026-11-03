import { describe, expect, it } from "vitest";
import type { ImageLayer } from "@/api/desktop";
import { blendName, diffLayers } from "./layers";

const layer = (name: string, over: Partial<ImageLayer> = {}): ImageLayer => ({
  name,
  kind: "layer",
  depth: 0,
  visible: true,
  opacity: 255,
  blend: "norm",
  x: 0,
  y: 0,
  width: 10,
  height: 10,
  hash: name,
  ...over,
});

describe("diffLayers", () => {
  it("marks added, removed, changed and unchanged layers", () => {
    const rows = diffLayers(
      [layer("Sky"), layer("Hero"), layer("Old"), layer("Shadow", { opacity: 255 })],
      [layer("Sky"), layer("Hero", { hash: "x", x: 4 }), layer("Glow"), layer("Shadow", { opacity: 128, visible: false, blend: "mul", depth: 1 })],
    );
    expect(rows.map((r) => [r.layer.name, r.status])).toEqual([
      ["Sky", "unchanged"],
      ["Hero", "changed"],
      ["Old", "removed"],
      ["Glow", "added"],
      ["Shadow", "changed"],
    ]);
    expect(rows[1].changes).toEqual(["pixels", "bounds"]);
    expect(rows[4].changes).toEqual(["hidden", "opacity 100% → 50%", "Normal → Multiply", "moved"]);
  });

  it("pairs duplicate names by order and puts a removed first layer on top", () => {
    const rows = diffLayers([layer("A"), layer("A", { hash: "2" })], [layer("A", { visible: false }), layer("B")]);
    expect(rows.map((r) => [r.layer.name, r.status])).toEqual([
      ["A", "changed"],
      ["A", "removed"],
      ["B", "added"],
    ]);
    expect(rows[0].changes).toEqual(["hidden"]);
    expect(diffLayers([layer("X")], [])[0].status).toBe("removed");
    expect(diffLayers([layer("X", { visible: false })], [layer("X")])[0].changes).toEqual(["shown"]);
  });
});

describe("blendName", () => {
  it("names Photoshop blend keys", () => {
    expect(blendName("mul ")).toBe("Multiply");
    expect(blendName("div ")).toBe("Color dodge");
    expect(blendName("zzzz")).toBe("zzzz");
  });
});
