import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AnimationClip, Group } from "three";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, ModelSide, ModelStats } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ modelDiff: vi.fn(), hasMedia: vi.fn(), releaseMedia: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));
const loader = vi.hoisted(() => ({ loadModel: vi.fn(), sceneStats: vi.fn() }));
vi.mock("@/lib/modelLoad", async (orig) => ({ ...(await orig<typeof import("@/lib/modelLoad")>()), ...loader }));
const stage = vi.hoisted(() => ({
  setModels: vi.fn(),
  setMode: vi.fn(),
  setShading: vi.fn(),
  playAnimation: vi.fn(),
  setPaused: vi.fn(),
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

const side = (over: Partial<ModelSide> = {}): ModelSide => ({ path: "hero.glb", format: "glb", size: 2048, stats: stats(), url: "/nipa-media/m1/hero.glb", media: "m1", tokens: ["m1"], files: {}, missing: [], ...over });

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
  api.hasMedia.mockResolvedValue(true);
  api.releaseMedia.mockResolvedValue(undefined);
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

    expect(stage.setPaused).toHaveBeenLastCalledWith(true);
    await user.selectOptions(screen.getByLabelText("Animation"), "Run");
    expect(stage.playAnimation).toHaveBeenLastCalledWith("Run");
    await user.click(screen.getByRole("button", { name: "Play" }));
    expect(stage.setPaused).toHaveBeenLastCalledWith(false);
    await user.click(screen.getByRole("button", { name: "Pause" }));
    expect(stage.setPaused).toHaveBeenLastCalledWith(true);
    expect(stage.playAnimation).toHaveBeenLastCalledWith("Run");
    await user.selectOptions(screen.getByLabelText("Animation"), "Bind pose");
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

  it("reloads the comparison once when its files were evicted", async () => {
    api.modelDiff.mockResolvedValueOnce({ old: null, new: side({ media: "gone", tokens: ["gone"] }) }).mockResolvedValue({ old: null, new: side({ media: "fresh", tokens: ["fresh"] }) });
    loader.loadModel.mockImplementation(async (s: ModelSide) => {
      if (s.media === "gone") throw new Error("HTTP 404");
      return loaded();
    });
    api.hasMedia.mockImplementation(async (token: string) => token !== "gone");
    renderWithProviders(<DiffViewer root="/r" diff={diff} layout="unified" onLayout={vi.fn()} />);
    await waitFor(() => expect(api.modelDiff).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(stage.setModels).toHaveBeenLastCalledWith(null, expect.objectContaining({ object: expect.any(Group) })));
    expect(api.releaseMedia).toHaveBeenCalledWith(["gone"]);
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
      <ModelCompareView compare={{ old: side({ missing: ["tex/a.png"] }), new: side({ path: "hero.blend", format: "blend" }) }} />,
    );
    expect(await screen.findByText(/Before: bad glTF/)).toBeInTheDocument();
    expect(screen.getByText(/BLEND models cannot be shown yet/)).toBeInTheDocument();
    expect(screen.getByText(/Before: missing tex\/a.png/)).toBeInTheDocument();

    renderWithProviders(<ModelCompareView compare={{ old: side({ tooLarge: true, url: "", size: 1 << 30 }), new: null }} />);
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

  it("fetches each side from its media URL and releases every token on close", async () => {
    const files = { "tex/skin.png": "/nipa-media/t2/skin.png" };
    const { unmount } = renderWithProviders(
      <ModelCompareView compare={{ old: side({ media: "m0", url: "/nipa-media/m0/hero.glb", tokens: ["m0"] }), new: side({ media: "m1", tokens: ["m1", "t2"], files }) }} />,
    );
    await waitFor(() => expect(loader.loadModel).toHaveBeenCalledTimes(2));
    expect(loader.loadModel).toHaveBeenCalledWith(expect.objectContaining({ url: "/nipa-media/m1/hero.glb", files }), expect.any(AbortSignal));
    expect(api.releaseMedia).not.toHaveBeenCalled();
    unmount();
    expect(api.releaseMedia).toHaveBeenCalledWith(["m0", "m1", "t2"]);
  });

  it("aborts the fetch of a side that is no longer shown", async () => {
    let signal: AbortSignal | undefined;
    loader.loadModel.mockImplementationOnce((_side: ModelSide, s: AbortSignal) => {
      signal = s;
      return new Promise(() => {});
    });
    const { unmount } = renderWithProviders(<ModelCompareView compare={{ old: null, new: side() }} />);
    await waitFor(() => expect(signal).toBeDefined());
    unmount();
    expect(signal!.aborted).toBe(true);
  });

  it("reloads once when the prepared files were evicted, then says the preview expired", async () => {
    loader.loadModel.mockRejectedValue(new Error("the file could not be loaded (HTTP 404)"));
    api.hasMedia.mockResolvedValue(false);
    const onExpired = vi.fn().mockReturnValueOnce(true).mockReturnValue(false);
    const first = renderWithProviders(<ModelCompareView compare={{ old: null, new: side() }} onExpired={onExpired} />);
    await waitFor(() => expect(onExpired).toHaveBeenCalledTimes(1));
    expect(api.hasMedia).toHaveBeenCalledWith("m1");
    expect(screen.queryByText(/HTTP 404/)).not.toBeInTheDocument();
    first.unmount();
    renderWithProviders(<ModelCompareView compare={{ old: null, new: side() }} onExpired={onExpired} />);
    expect(await screen.findByText("After: the preview files are no longer available, reopen the file.")).toBeInTheDocument();
  });

  it("shows the load error when the availability check fails or the side has no token", async () => {
    loader.loadModel.mockRejectedValue(new Error("network down"));
    api.hasMedia.mockRejectedValue(new Error("no bridge"));
    renderWithProviders(<ModelCompareView compare={{ old: side(), new: side({ media: "" }) }} />);
    expect(await screen.findByText("Before: network down")).toBeInTheDocument();
    expect(await screen.findByText("After: network down")).toBeInTheDocument();
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
