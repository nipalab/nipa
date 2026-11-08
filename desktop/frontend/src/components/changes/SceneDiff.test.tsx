import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, SceneCompare } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ sceneDiff: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { DiffViewer, viewerFor } from "./DiffViewer";
import { SceneCompareView, SceneDiff } from "./SceneDiff";

const compare = (over: Partial<SceneCompare> = {}): SceneCompare => ({
  engine: "unity",
  objects: [
    { id: "100", path: "Hero", type: "GameObject", parent: "" },
    { id: "101", path: "Hero > Transform", type: "Transform", parent: "100" },
    { id: "400", path: "Hero/Sword", type: "GameObject", parent: "100" },
    { id: "300", path: "OldLight", type: "GameObject", parent: "" },
  ],
  changes: [
    { status: "changed", id: "101", path: "Hero > Transform", type: "Transform", props: [{ key: "m_LocalPosition", old: "{x: 0}", new: "{x: 2}" }] },
    { status: "added", id: "400", path: "Hero/Sword", type: "GameObject", props: [{ key: "m_Name", old: "", new: "Sword" }] },
    { status: "removed", id: "300", path: "OldLight", type: "GameObject", props: [{ key: "m_Name", old: "OldLight", new: "" }] },
    { status: "moved", id: "500", path: "Camera", type: "GameObject", props: [{ key: "parent", old: "Hero", new: "(root)" }] },
  ],
  added: 1,
  removed: 1,
  changed: 2,
  refs: [
    { path: "Assets/Materials/Steel.mat", status: "added" },
    { path: "Assets/Data/Old.asset", status: "removed" },
  ],
  ...over,
});

const diff = (over: Partial<FileDiff> = {}): FileDiff => ({
  path: "Assets/Level.unity",
  status: "modified",
  kind: "scene",
  old: { path: "Assets/Level.unity", mode: 420, size: 400, binary: false, hash: "aa" },
  new: { path: "Assets/Level.unity", mode: 420, size: 420, binary: false, hash: "bb" },
  additions: 1,
  deletions: 1,
  hunks: [
    {
      oldStart: 2,
      oldLines: 1,
      newStart: 2,
      newLines: 1,
      lines: [
        { kind: "removed", old: 2, new: 0, text: "  m_LocalPosition: {x: 0}" },
        { kind: "added", old: 0, new: 2, text: "  m_LocalPosition: {x: 2}" },
      ],
    },
  ],
  ...over,
});

beforeEach(() => vi.resetAllMocks());

describe("SceneCompareView", () => {
  it("shows the object tree with badges, property rows and dependencies", async () => {
    renderWithProviders(<SceneCompareView compare={compare()} />);
    expect(screen.getByText("Unity")).toBeInTheDocument();
    expect(screen.getByLabelText("Scene summary")).toHaveTextContent("+1 object−1 object2 changed2 dependency changes");

    const tree = screen.getByRole("tree", { name: "Scene changes" });
    const hero = within(tree).getByRole("treeitem", { name: "Hero" });
    expect(hero).toHaveAttribute("data-status", "same");
    const transform = within(hero).getByRole("treeitem", { name: "Hero > Transform" });
    expect(transform).toHaveAttribute("data-status", "changed");
    expect(within(transform).getByText("Transform", { selector: "span.truncate" })).toBeInTheDocument();
    const props = within(transform).getByRole("table", { name: "Properties of Hero > Transform" });
    expect(props).toHaveTextContent("m_LocalPosition{x: 0}→{x: 2}");
    expect(within(hero).getByRole("treeitem", { name: "Hero/Sword" })).toHaveTextContent("SwordGameObjectaddedm_NameSword");
    expect(within(tree).getByRole("treeitem", { name: "OldLight" })).toHaveTextContent("removedm_NameOldLight");
    expect(within(tree).getByRole("treeitem", { name: "Camera" })).toHaveTextContent("moved");

    const deps = screen.getByRole("region", { name: "Dependency changes" });
    expect(deps).toHaveTextContent("+ Assets/Materials/Steel.mat");
    expect(deps).toHaveTextContent("− Assets/Data/Old.asset");

    await userEvent.click(screen.getByRole("button", { name: "Collapse Hero" }));
    expect(within(tree).queryByRole("treeitem", { name: "Hero > Transform" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Expand Hero" }));
    expect(within(tree).getByRole("treeitem", { name: "Hero > Transform" })).toBeInTheDocument();
  });

  it("shows empty scenes and parse errors", () => {
    const { unmount } = renderWithProviders(<SceneCompareView compare={compare({ objects: [], changes: [], refs: null, added: 0, removed: 0, changed: 0, engine: "custom" })} />);
    expect(screen.getByText("No object changes.")).toBeInTheDocument();
    expect(screen.getByText("custom")).toBeInTheDocument();
    expect(screen.getByLabelText("Scene summary")).toHaveTextContent("No object changes");
    unmount();
    renderWithProviders(<SceneCompareView compare={compare({ objects: null, changes: null, refs: null, added: 0, removed: 0, changed: 0, error: "binary Unity asset" })} />);
    expect(screen.getByText("This file cannot be compared as a scene: binary Unity asset.")).toBeInTheDocument();
    expect(screen.queryByRole("tree")).not.toBeInTheDocument();
  });
});

describe("SceneDiff", () => {
  it("loads the structure and switches to the text diff", async () => {
    api.sceneDiff.mockResolvedValue(compare());
    renderWithProviders(<DiffViewer root="/work/game" diff={diff()} layout="unified" onLayout={() => {}} />);
    expect(screen.getByText("Comparing scene…")).toBeInTheDocument();
    expect(await screen.findByRole("tree", { name: "Scene changes" })).toBeInTheDocument();
    expect(api.sceneDiff).toHaveBeenCalledWith("/work/game", { source: "snapshot", path: "Assets/Level.unity" }, { source: "working", path: "Assets/Level.unity" });
    expect(screen.getByRole("group", { name: "Diff layout" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Text" }));
    expect(await screen.findByRole("table", { name: "Diff of Assets/Level.unity" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Structure" }));
    expect(screen.getByRole("tree", { name: "Scene changes" })).toBeInTheDocument();
  });

  it("starts on the text diff when there are line comments", () => {
    renderWithProviders(<SceneDiff root="/r" diff={diff()} layout="split" comments={{ render: () => null }} />);
    expect(screen.getByRole("button", { name: "Text" })).toHaveAttribute("aria-pressed", "true");
    expect(api.sceneDiff).not.toHaveBeenCalled();
  });

  it("falls back to metadata on errors", async () => {
    api.sceneDiff.mockRejectedValueOnce(new Error("boom"));
    const { unmount } = renderWithProviders(<SceneDiff root="/r" diff={diff({ hunks: [] })} layout="unified" />);
    expect(await screen.findByText("The scene could not be read: boom")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Scene view" })).not.toBeInTheDocument();
    unmount();
    api.sceneDiff.mockResolvedValueOnce(null);
    renderWithProviders(<SceneDiff root="/r" diff={diff()} layout="unified" />);
    expect(await screen.findByText("There is no scene preview for this file.")).toBeInTheDocument();
  });

  it("renders a Godot scene diff", async () => {
    api.sceneDiff.mockResolvedValue(
      compare({
        engine: "godot",
        objects: [
          { id: ".", path: "Main", type: "Node2D", parent: "" },
          { id: "Player", path: "Main/Player", type: "CharacterBody2D", parent: "." },
        ],
        changes: [{ status: "changed", id: "Player", path: "Main/Player", type: "CharacterBody2D", props: [{ key: "speed", old: "5", new: "7" }] }],
        added: 0,
        removed: 0,
        changed: 1,
        refs: [{ path: "res://sword.tscn", status: "added" }],
      }),
    );
    const tscn = { path: "scenes/main.tscn", mode: 420, size: 120, binary: false, hash: "cc" };
    const d = diff({ path: "scenes/main.tscn", old: tscn, new: { ...tscn, hash: "dd" } });
    expect(viewerFor(d)).toBe(SceneDiff);
    renderWithProviders(<DiffViewer root="/work/game" diff={d} layout="unified" onLayout={() => {}} />);
    const tree = await screen.findByRole("tree", { name: "Scene changes" });
    expect(screen.getByText("Godot")).toBeInTheDocument();
    expect(api.sceneDiff).toHaveBeenCalledWith("/work/game", { source: "snapshot", path: "scenes/main.tscn" }, { source: "working", path: "scenes/main.tscn" });
    const player = within(tree).getByRole("treeitem", { name: "Main/Player" });
    expect(within(player).getByRole("table", { name: "Properties of Main/Player" })).toHaveTextContent("speed5→7");
    expect(screen.getByRole("region", { name: "Dependency changes" })).toHaveTextContent("+ res://sword.tscn");
  });

  it("is picked for non-empty scenes only", () => {
    expect(viewerFor(diff())).toBe(SceneDiff);
    expect(viewerFor(diff({ hunksOmitted: true }))).toBe(SceneDiff);
    expect(viewerFor(diff({ old: null, new: { path: "a.unity", mode: 420, size: 0, binary: false, hash: "" } }))).not.toBe(SceneDiff);
  });
});
