import { AnimationClip, BoxGeometry, Group, Mesh, MeshStandardMaterial, NumberKeyframeTrack, type Material, type Scene } from "three";
import { describe, expect, it, vi } from "vitest";
import type { LoadedModel } from "./modelLoad";
import { createStage, type RendererLike } from "./modelStage";

function fakeRenderer() {
  const calls: { viewport: number[]; override: Material | null; objects: number }[] = [];
  let viewport: number[] = [];
  let loop: (() => void) | null = null;
  const r = {
    domElement: document.createElement("canvas"),
    autoClear: true,
    setPixelRatio: vi.fn(),
    setSize: vi.fn(),
    setScissorTest: vi.fn(),
    setScissor: vi.fn(),
    setViewport: vi.fn((x: number, y: number, w: number, h: number) => {
      viewport = [x, y, w, h];
    }),
    render: vi.fn((scene: Scene) => calls.push({ viewport, override: scene.overrideMaterial, objects: scene.children.length })),
    clear: vi.fn(),
    clearDepth: vi.fn(),
    setAnimationLoop: vi.fn((fn: (() => void) | null) => {
      loop = fn;
    }),
    dispose: vi.fn(),
    setClearColor: vi.fn(),
  };
  return { renderer: r as unknown as RendererLike, raw: r, calls, frame: () => loop?.(), hasLoop: () => !!loop };
}

const model = (size = 1, clips: AnimationClip[] = []): LoadedModel => {
  const g = new Group();
  g.add(new Mesh(new BoxGeometry(size, size, size), new MeshStandardMaterial()));
  return { object: g, animations: clips };
};

describe("createStage", () => {
  it("renders both sides side by side, then one pane with a ghost overlay", () => {
    const fake = fakeRenderer();
    const container = document.createElement("div");
    const stage = createStage(container, () => fake.renderer);
    expect(container.contains(fake.raw.domElement)).toBe(true);
    expect(fake.raw.autoClear).toBe(false);
    stage.resize(800, 400);
    expect(fake.raw.setSize).toHaveBeenCalledWith(800, 400);

    stage.setModels(model(1), model(2));
    fake.calls.length = 0;
    fake.frame();
    expect(fake.calls.map((c) => c.viewport)).toEqual([
      [0, 0, 400, 400],
      [400, 0, 400, 400],
    ]);

    stage.setMode("overlay");
    fake.calls.length = 0;
    fake.frame();
    expect(fake.calls).toHaveLength(2);
    expect(fake.calls[1].override).toMatchObject({ wireframe: true, depthTest: false });
    expect(fake.raw.clearDepth).toHaveBeenCalled();

    stage.setShading("wireframe");
    fake.calls.length = 0;
    fake.frame();
    expect(fake.calls[0].override).toMatchObject({ wireframe: true });
    stage.setShading("both");
    fake.calls.length = 0;
    fake.frame();
    expect(fake.calls.map((c) => c.override === null)).toEqual([true, false, false]);

    stage.dispose();
    expect(fake.hasLoop()).toBe(false);
    expect(fake.raw.dispose).toHaveBeenCalled();
    expect(container.contains(fake.raw.domElement)).toBe(false);
  });

  it("shows a single side and fits the camera to it", () => {
    const fake = fakeRenderer();
    const stage = createStage(document.createElement("div"), () => fake.renderer);
    stage.resize(0, 0);
    stage.setModels(null, null);
    stage.setModels(model(4), null);
    fake.calls.length = 0;
    fake.frame();
    expect(fake.calls[0].viewport).toEqual([0, 0, 1, 1]);
    stage.setModels(null, model(1));
    stage.resetView();
    fake.frame();
  });

  it("plays animations on both sides by name", () => {
    const fake = fakeRenderer();
    const stage = createStage(document.createElement("div"), () => fake.renderer);
    const clip = () => new AnimationClip("", 1, [new NumberKeyframeTrack(".position[x]", [0, 1], [0, 5])]);
    const a = model(1, [clip()]);
    const b = model(1, [clip()]);
    stage.setModels(a, b);
    stage.playAnimation("Animation 1");
    const now = performance.now();
    vi.spyOn(performance, "now").mockReturnValue(now + 500);
    fake.frame();
    expect(a.object.position.x).toBeGreaterThan(0);
    expect(b.object.position.x).toBeCloseTo(a.object.position.x);
    stage.playAnimation(null);
    stage.setModels(model(), null);
    vi.restoreAllMocks();
  });

  it("defaults to a WebGL renderer", () => {
    expect(() => createStage(document.createElement("div"))).toThrow();
  });
});
