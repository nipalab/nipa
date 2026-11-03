import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AnimationClip, Group } from "three";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, ModelSide, ModelStats } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ modelDiff: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));
const loader = vi.hoisted(() => ({ loadModel: vi.fn(), sceneStats: vi.fn() }));
vi.mock("@/lib/modelLoad", async (orig) => ({ ...(await orig<typeof import("@/lib/modelLoad")>()), ...loader }));
const stage = vi.hoisted(() => ({
  setModels: vi.fn(),
  setMode: vi.fn(),
  setShading: vi.fn(),
  playAnimation: vi.fn(),
  resetView: vi.fn(),
  resize: vi.fn(),
  dispose: vi.fn(),
}));
const stageFactory = vi.hoisted(() => ({ createStage: vi.fn() }));
vi.mock("@/lib/modelStage", () => stageFactory);

import { DiffViewer } from "./DiffViewer";
import { ModelCompareView, boundsSize } from "./ModelDiff";

const stats = (over: Partial<ModelStats> = {}): ModelStats => ({
  generator: "Blender",
  meshes: 2,
  primitives: 2,
  vertices: 1000,
  triangles: 500,
  materials: ["Skin", "Cloth"],
  textures: 2,
  nodes: 3,
  skins: 1,
  joints: 20,
  animations: ["Run"],
  morphTargets: 0,
  min: [0, 0, 0],
  max: [1, 2, 0.5],
  ...over,
});

const side = (over: Partial<ModelSide> = {}): ModelSide => ({ path: "hero.glb", format: "glb", size: 2048, stats: stats(), data: "AAAA", files: {}, missing: [], ...over });

const diff: FileDiff = {
  path: "art/hero.glb",
  status: "modified",
  kind: "model",
  old: { path: "art/hero.glb", mode: 420, size: 2048, binary: true, hash: "a" },
  new: { path: "art/hero.glb", mode: 420, size: 4096, binary: true, hash: "" },
  additions: 0,
  deletions: 0,
  hunks: [],
};

const loaded = (clips: string[] = []) => ({ object: new Group(), animations: clips.map((n) => new AnimationClip(n, 1, [])) });

beforeEach(() => {
  vi.resetAllMocks();
  stageFactory.createStage.mockReturnValue(stage);
  loader.loadModel.mockImplementation(async () => loaded(["Run"]));
  loader.sceneStats.mockReturnValue(stats({ generator: "", vertices: 8 }));
});

describe("ModelDiff", () => {
  it("loads both sides and compares their statistics", async () => {
    const user = userEvent.setup();
    api.modelDiff.mockResolvedValue({ old: side(), new: side({ size: 4096, stats: stats({ vertices: 1200, triangles: 450, materials: ["Skin", "Metal"], max: [1, 2, 1] }) }) });
    const { unmount } = renderWithProviders(<DiffViewer root="/r" diff={diff} layout="unified" onLayout={vi.fn()} />);
    await waitFor(() => expect(stage.setModels).toHaveBeenLastCalledWith(expect.objectContaining({ object: expect.any(Group) }), expect.anything()));
    expect(api.modelDiff).toHaveBeenCalledWith("/r", { source: "snapshot", path: "art/hero.glb" }, { source: "working", path: "art/hero.glb" });
    const table = screen.getByRole("table", { name: "Model details" });
    const row = (label: string) => within(table).getByText(label).closest("tr")!;
    expect(row("Vertices")).toHaveTextContent("1,0001,200+200");
    expect(row("Triangles")).toHaveTextContent("−50");
    expect(row("Bounds")).toHaveTextContent("1.00 × 2.00 × 0.501.00 × 2.00 × 1.00");
    expect(row("Exported by")).toHaveTextContent("Blender");
    expect(within(screen.getByRole("list", { name: "Materials changes" })).getAllByRole("listitem").map((l) => l.textContent)).toEqual(["+ Metal", "− Cloth"]);
    expect(screen.queryByRole("list", { name: "Animations changes" })).not.toBeInTheDocument();
    expect(screen.getAllByText("Before")).toHaveLength(2);

    await user.click(screen.getByRole("button", { name: "Overlay" }));
    expect(stage.setMode).toHaveBeenLastCalledWith("overlay");
    expect(screen.getByText("After · Before as orange wireframe")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Wireframe" }));
    expect(stage.setShading).toHaveBeenLastCalledWith("wireframe");
    await user.click(screen.getByRole("button", { name: "Shaded with wireframe" }));
    await user.click(screen.getByRole("button", { name: "Shaded" }));
    await user.click(screen.getByRole("button", { name: "Side by side" }));
    expect(stage.setMode).toHaveBeenLastCalledWith("side");

    await user.selectOptions(screen.getByLabelText("Animation"), "Run");
    await user.click(screen.getByRole("button", { name: "Play" }));
    expect(stage.playAnimation).toHaveBeenLastCalledWith("Run");
    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(stage.playAnimation).toHaveBeenLastCalledWith(null);
    await user.click(screen.getByRole("button", { name: "Reset view" }));
    expect(stage.resetView).toHaveBeenCalled();
    expect(stage.resize).toHaveBeenCalledWith(800, 800);
    unmount();
    expect(stage.dispose).toHaveBeenCalled();
  });

  it("explains failures and empty answers", async () => {
    api.modelDiff.mockReturnValue(new Promise(() => {}));
    const pending = renderWithProviders(<DiffViewer root="/r" diff={diff} layout="unified" onLayout={vi.fn()} />);
    expect(await screen.findByText("Loading model…")).toBeInTheDocument();
    pending.unmount();
    api.modelDiff.mockRejectedValue(new Error("daemon gone"));
    const { unmount } = renderWithProviders(<DiffViewer root="/r" diff={diff} layout="unified" onLayout={vi.fn()} />);
    expect(await screen.findByText("The model could not be read: daemon gone")).toBeInTheDocument();
    unmount();
    api.modelDiff.mockResolvedValue({ old: null, new: null });
    renderWithProviders(<DiffViewer root="/r" diff={diff} layout="unified" onLayout={vi.fn()} />);
    expect(await screen.findByText("There is no preview for this model.")).toBeInTheDocument();
  });
});

describe("ModelCompareView", () => {
  it("counts from the scene when there are no daemon stats", async () => {
    renderWithProviders(<ModelCompareView compare={{ old: null, new: side({ format: "fbx", stats: null, statsError: "unsupported" }) }} />);
    await waitFor(() => expect(within(screen.getByRole("table", { name: "Model details" })).getByText("Vertices").closest("tr")).toHaveTextContent("—8"));
    expect(screen.getByText("Added")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Compare mode" })).not.toBeInTheDocument();
    expect(screen.queryByText("Exported by")).not.toBeInTheDocument();
  });

  it("warns about large, unknown, broken and incomplete models", async () => {
    loader.loadModel.mockRejectedValueOnce(new Error("bad glTF")).mockRejectedValueOnce("odd");
    renderWithProviders(
      <ModelCompareView compare={{ old: side({ missing: ["tex/a.png"] }), new: side({ path: "hero.blend", format: "blend", data: "AAAA" }) }} />,
    );
    expect(await screen.findByText(/Before: bad glTF/)).toBeInTheDocument();
    expect(screen.getByText(/BLEND models cannot be shown yet/)).toBeInTheDocument();
    expect(screen.getByText(/Before: missing tex\/a.png/)).toBeInTheDocument();

    renderWithProviders(<ModelCompareView compare={{ old: side({ tooLarge: true, data: "", size: 1 << 30 }), new: null }} />);
    expect(screen.getByText(/the model and its files are too large to show \(1.0 GB\)/)).toBeInTheDocument();
    expect(screen.getByText("No 3D preview for this model.")).toBeInTheDocument();
  });

  it("shows the deleted side and reports a missing WebGL", async () => {
    stageFactory.createStage.mockImplementation(() => {
      throw new Error("WebGL is off");
    });
    renderWithProviders(<ModelCompareView compare={{ old: side(), new: null }} />);
    expect(screen.getByText(/3D preview is not available here: WebGL is off/)).toBeInTheDocument();
    expect(await screen.findByText("Deleted")).toBeInTheDocument();
    stageFactory.createStage.mockImplementation(() => {
      throw "plain";
    });
    renderWithProviders(<ModelCompareView compare={{ old: side(), new: null }} />);
    expect(screen.getByText(/not available here: plain/)).toBeInTheDocument();
  });

  it("drops a stale load when the side changes", async () => {
    let resolve!: (m: unknown) => void;
    loader.loadModel.mockImplementationOnce(() => new Promise((r) => (resolve = r)));
    const { rerender } = renderWithProviders(<ModelCompareView compare={{ old: null, new: side() }} />);
    expect(screen.getByText("Building scene…")).toBeInTheDocument();
    rerender(<ModelCompareView compare={{ old: null, new: side({ format: "blend" }) }} />);
    await act(async () => resolve(loaded()));
    expect(screen.queryByText("Building scene…")).not.toBeInTheDocument();
  });
});

describe("boundsSize", () => {
  it("formats extents", () => {
    expect(boundsSize(null)).toBe("—");
    expect(boundsSize(stats({ min: null }))).toBe("—");
    expect(boundsSize(stats())).toBe("1.00 × 2.00 × 0.50");
  });
});
