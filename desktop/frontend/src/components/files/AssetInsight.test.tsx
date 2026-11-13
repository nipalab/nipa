import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AssetReferences, Origin, RepoChanged } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ assetRefs: vi.fn(), provenance: vi.fn(), setProvenance: vi.fn(), onRepoChanged: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { AssetInsight, InsightPanel } from "./AssetInsight";
import { INSIGHT_REFRESH_MS } from "@/hooks/useAssetInsight";

const ROOT = "/work/game";
const refs = (over: Partial<AssetReferences> = {}): AssetReferences => ({ path: "a.png", asset: true, uses: [], usedBy: [], unknown: [], scanned: 4, truncated: false, partial: false, ...over });
const origin = (over: Partial<Origin> = {}): Origin => ({
  path: "a.png",
  status: "recorded",
  required: false,
  hasRecord: true,
  exists: true,
  tool: "ComfyUI",
  model: "sdxl",
  seed: "42",
  source: "png-text",
  recordedAt: "2026-03-04T05:06:07Z",
  license: "",
  humanEdited: false,
  promptHash: "",
  digitalSourceType: "",
  note: "",
  ...over,
});

let changed: (c: RepoChanged) => void = () => {};

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  api.assetRefs.mockResolvedValue(refs());
  api.provenance.mockResolvedValue({ enabled: false, origins: [] });
  api.onRepoChanged.mockImplementation((fn: (c: RepoChanged) => void) => {
    changed = fn;
    return () => {};
  });
});

const show = (path = "a.png", onOpenFile = vi.fn()) => {
  renderWithProviders(<AssetInsight root={ROOT} path={path} onOpenFile={onOpenFile} />);
  return onOpenFile;
};

describe("AssetInsight references", () => {
  it("lists users and dependencies and opens them", async () => {
    api.assetRefs.mockResolvedValue(
      refs({
        usedBy: ["Scenes/Main.tscn", "Scenes/Menu.tscn"],
        uses: [
          { path: "tex/b.png", raw: "res://tex/b.png", resolved: true },
          { path: "tex/b.png", raw: "uid://x", resolved: true },
          { path: "tex/gone.png", raw: "res://tex/gone.png", resolved: false },
        ],
        unknown: [{ path: "", raw: "guid:abc", resolved: false }],
      }),
    );
    const open = show();
    const section = await screen.findByRole("region", { name: "References" });
    expect(await within(section).findByText("Used by (2)")).toBeInTheDocument();
    expect(within(section).getByText("Uses (1)")).toBeInTheDocument();
    expect(within(section).getByText("Unresolved (2)")).toBeInTheDocument();
    expect(within(section).getByText("not found")).toBeInTheDocument();
    expect(within(section).getByText("outside this clone")).toBeInTheDocument();
    expect(within(section).getByText("guid:abc")).toBeInTheDocument();
    await userEvent.click(within(section).getByRole("button", { name: "Scenes/Menu.tscn" }));
    expect(open).toHaveBeenCalledWith("Scenes/Menu.tscn");
    await userEvent.click(within(section).getByRole("button", { name: "tex/b.png" }));
    expect(open).toHaveBeenCalledWith("tex/b.png");
    expect(api.assetRefs).toHaveBeenCalledWith(ROOT, "a.png");
  });

  it("says when nothing references the asset", async () => {
    show();
    expect(await screen.findByText("Not referenced anywhere.")).toBeInTheDocument();
    expect(screen.queryByText(/Uses \(/)).not.toBeInTheDocument();
  });

  it("explains truncated and sparse scans", async () => {
    api.assetRefs.mockResolvedValue(refs({ truncated: true, partial: true, scanned: 5000 }));
    show();
    expect(await screen.findByText("Not referenced anywhere that was scanned.")).toBeInTheDocument();
    expect(screen.getByText(/stopped after 5000 files/)).toBeInTheDocument();
    expect(screen.getByText(/only downloads some folders/)).toBeInTheDocument();
  });

  it("renders paths without links when navigation is not available", async () => {
    api.assetRefs.mockResolvedValue(refs({ usedBy: ["s.tscn"] }));
    renderWithProviders(<AssetInsight root={ROOT} path="a.png" />);
    expect(await screen.findByText("s.tscn")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "s.tscn" })).not.toBeInTheDocument();
  });

  it("shows a scan in progress and failures", async () => {
    let resolve: (r: AssetReferences) => void = () => {};
    api.assetRefs.mockReturnValueOnce(new Promise((r) => (resolve = r)));
    show();
    expect(await screen.findByText("Scanning references…")).toBeInTheDocument();
    await act(async () => resolve(refs({ usedBy: ["x.tscn"] })));
    expect(await screen.findByText("Used by (1)")).toBeInTheDocument();
  });

  it("reports a failed lookup", async () => {
    api.assetRefs.mockRejectedValue(new Error("asset references are not configured"));
    show();
    expect(await screen.findByText(/not available: asset references are not configured/)).toBeInTheDocument();
  });

  it("renders nothing for files that are not assets and have no origin", async () => {
    api.assetRefs.mockResolvedValue(refs({ asset: false }));
    const { container } = renderWithProviders(<AssetInsight root={ROOT} path="README.md" />);
    await waitFor(() => expect(api.provenance).toHaveBeenCalledWith(ROOT, ["README.md"]));
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it("refreshes after a change notice of the same clone", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      show();
      await screen.findByText("Not referenced anywhere.");
      api.assetRefs.mockResolvedValue(refs({ usedBy: ["n.tscn"] }));
      act(() => changed({ root: "/other", paths: [] }));
      await act(() => vi.advanceTimersByTimeAsync(INSIGHT_REFRESH_MS * 2));
      expect(api.assetRefs).toHaveBeenCalledTimes(1);
      act(() => changed({ root: ROOT, paths: ["n.tscn"] }));
      act(() => changed({ root: ROOT, paths: ["n.tscn"] }));
      await act(() => vi.advanceTimersByTimeAsync(INSIGHT_REFRESH_MS * 2));
      expect(await screen.findByText("n.tscn")).toBeInTheDocument();
      expect(api.assetRefs).toHaveBeenCalledTimes(2);
      expect(api.provenance).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("AssetInsight origin", () => {
  it("shows a recorded origin for a file that is not an engine asset", async () => {
    api.assetRefs.mockResolvedValue(refs({ asset: false }));
    api.provenance.mockResolvedValue({ enabled: true, origins: [origin({ license: "cc0", note: "first pass" })] });
    show();
    const section = await screen.findByRole("region", { name: "Origin" });
    expect(screen.queryByRole("region", { name: "References" })).not.toBeInTheDocument();
    expect(within(section).getByText("The file matches the recorded version.")).toBeInTheDocument();
    for (const text of ["ComfyUI", "sdxl", "42", "PNG metadata", "cc0", "first pass"]) expect(within(section).getByText(text)).toBeInTheDocument();
    expect(within(section).getByText("Recorded")).toBeInTheDocument();
    expect(within(section).queryByRole("button", { name: "Add record…" })).not.toBeInTheDocument();
  });

  it.each([
    ["modified", "The file changed after it was recorded and is not marked as edited by a person."],
    ["edited", "A person edited the file after it was generated."],
    ["odd", "odd"],
  ])("explains the %s status", async (status, text) => {
    api.provenance.mockResolvedValue({ enabled: true, origins: [origin({ status, source: "custom" })] });
    show();
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.getByText("custom")).toBeInTheDocument();
  });

  it("adds a missing record that a rule requires", async () => {
    api.provenance.mockResolvedValue({ enabled: false, origins: [origin({ status: "missing", required: true, hasRecord: false, tool: "", model: "", seed: "", source: "", recordedAt: "" })] });
    api.setProvenance.mockResolvedValue({ created: true, origin: origin({ source: "manual" }) });
    show();
    const section = await screen.findByRole("region", { name: "Origin" });
    expect(within(section).getByText(/requires an origin record/)).toBeInTheDocument();
    await userEvent.click(within(section).getByRole("button", { name: "Add record…" }));
    const form = within(section).getByRole("form", { name: "Add origin record" });
    expect(within(form).getByText(/creates .nipaprovenance.yml/)).toBeInTheDocument();

    await userEvent.click(within(form).getByRole("button", { name: "Save record" }));
    expect(within(form).getByRole("alert")).toHaveTextContent("Enter the tool that made the file.");
    await userEvent.type(within(form).getByLabelText("Tool"), "Krita");
    await userEvent.type(within(form).getByLabelText("Seed"), "12x");
    await userEvent.click(within(form).getByRole("button", { name: "Save record" }));
    expect(within(form).getByRole("alert")).toHaveTextContent("The seed must be a whole number.");
    expect(api.setProvenance).not.toHaveBeenCalled();

    await userEvent.clear(within(form).getByLabelText("Seed"));
    await userEvent.type(within(form).getByLabelText("Seed"), "12");
    await userEvent.type(within(form).getByLabelText("Model"), "brush");
    await userEvent.type(within(form).getByLabelText("License"), "cc0");
    await userEvent.type(within(form).getByLabelText(/^Prompt/), "a sword");
    await userEvent.type(within(form).getByLabelText("Note"), "n");
    await userEvent.click(within(form).getByRole("checkbox"));
    api.provenance.mockResolvedValue({ enabled: true, origins: [origin({ source: "manual", tool: "Krita" })] });
    await userEvent.click(within(form).getByRole("button", { name: "Save record" }));
    await waitFor(() =>
      expect(api.setProvenance).toHaveBeenCalledWith(ROOT, { path: "a.png", tool: "Krita", model: "brush", seed: "12", license: "cc0", prompt: "a sword", note: "n", humanEdited: true }),
    );
    expect(await screen.findByText("Krita")).toBeInTheDocument();
    expect(screen.queryByRole("form")).not.toBeInTheDocument();
  });

  it("shows a server error and can cancel", async () => {
    api.provenance.mockResolvedValue({ enabled: true, origins: [origin({ status: "missing", hasRecord: false })] });
    api.setProvenance.mockRejectedValue(new Error("cannot read a.png"));
    show();
    await userEvent.click(await screen.findByRole("button", { name: "Add record…" }));
    const form = screen.getByRole("form", { name: "Add origin record" });
    expect(within(form).queryByText(/creates .nipaprovenance.yml/)).not.toBeInTheDocument();
    await userEvent.type(within(form).getByLabelText("Tool"), "Krita");
    await userEvent.click(within(form).getByRole("button", { name: "Save record" }));
    expect(await within(form).findByRole("alert")).toHaveTextContent("cannot read a.png");
    await userEvent.click(within(form).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("form")).not.toBeInTheDocument();
    expect(screen.getByText("No origin record.")).toBeInTheDocument();
  });

  it("cannot add a record for a file that is not on disk", async () => {
    api.provenance.mockResolvedValue({ enabled: true, origins: [origin({ status: "missing", hasRecord: false, exists: false })] });
    show();
    expect(await screen.findByRole("button", { name: "Add record…" })).toBeDisabled();
  });
});

describe("InsightPanel", () => {
  it("stays hidden for files it has nothing to say about", async () => {
    api.assetRefs.mockResolvedValue(refs({ asset: false }));
    const { container } = renderWithProviders(<InsightPanel root={ROOT} path="README.md" />);
    await waitFor(() => expect(api.provenance).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("summarizes, expands and remembers the choice", async () => {
    api.assetRefs.mockResolvedValue(refs({ usedBy: ["a.tscn", "b.tscn"], uses: [{ path: "c.png", raw: "x", resolved: true }] }));
    api.provenance.mockResolvedValue({ enabled: true, origins: [origin({ status: "missing", hasRecord: false })] });
    const open = vi.fn();
    const first = renderWithProviders(<InsightPanel root={ROOT} path="a.png" onOpenFile={open} />);
    const toggle = await screen.findByRole("button", { name: /References & origin/ });
    expect(toggle).toHaveTextContent("Used by 2 · Uses 1 · Origin: missing");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByLabelText("Origin record missing")).toBeInTheDocument();
    await userEvent.click(toggle);
    await userEvent.click(await screen.findByRole("button", { name: "a.tscn" }));
    expect(open).toHaveBeenCalledWith("a.tscn");
    first.unmount();

    renderWithProviders(<InsightPanel root={ROOT} path="a.png" />);
    const again = await screen.findByRole("button", { name: /References & origin/ });
    expect(again).toHaveAttribute("aria-expanded", "true");
    await userEvent.click(again);
    expect(again).toHaveAttribute("aria-expanded", "false");
    expect(localStorage.getItem("nipa.insightPanel")).toBeNull();
  });
});
