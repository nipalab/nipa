import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AssetReferences, ChangeSet } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ assetRefs: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { QUICK_LOOKUP_MS, ReferrerNote, useStageGuard } from "./DeleteSafety";

const ROOT = "/work/game";
const refs = (path: string, usedBy: string[], asset = true): AssetReferences => ({ path, asset, uses: [], usedBy, unknown: [], scanned: 1, truncated: false, partial: false });
const changes: ChangeSet = {
  branch: "main",
  staged: [],
  unstaged: [
    { path: "tex/rock.png", status: "deleted", kind: "image" },
    { path: "tex/bark.png", status: "deleted", kind: "image" },
    { path: "notes.txt", status: "modified", kind: "text" },
  ],
  conflicts: [],
  resolved: [],
};

function Harness({ stage, paths }: { stage: (paths: string[]) => void; paths: string[] }) {
  const guard = useStageGuard(ROOT, changes, stage);
  return (
    <>
      <button type="button" onClick={() => guard.request(paths)}>
        stage
      </button>
      {guard.dialog}
    </>
  );
}

const users: Record<string, string[]> = { "tex/rock.png": ["mat/Rock.mat", "scene/A.unity", "scene/B.unity", "scene/C.unity"], "tex/bark.png": [] };

beforeEach(() => {
  vi.resetAllMocks();
  api.assetRefs.mockImplementation((_root: string, path: string) => Promise.resolve(refs(path, users[path] ?? [])));
});

describe("useStageGuard", () => {
  it("stages right away without deletions", async () => {
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} paths={["notes.txt"]} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    expect(stage).toHaveBeenCalledWith(["notes.txt"]);
    expect(api.assetRefs).not.toHaveBeenCalled();
  });

  it("stages right away when the deleted files are not used", async () => {
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} paths={["tex/bark.png", "notes.txt"]} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await waitFor(() => expect(stage).toHaveBeenCalledWith(["tex/bark.png", "notes.txt"]));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("stages when the lookup fails", async () => {
    api.assetRefs.mockRejectedValue(new Error("not configured"));
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} paths={["tex/rock.png"]} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await waitFor(() => expect(stage).toHaveBeenCalledWith(["tex/rock.png"]));
  });

  it("asks before deleting an asset other files use", async () => {
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} paths={["tex/rock.png"]} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Staging deletes tex/rock.png from the project when you push.");
    expect(dialog).toHaveTextContent("Used by 4 files: mat/Rock.mat, scene/A.unity, scene/B.unity and 1 more.");
    expect(stage).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(stage).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await userEvent.click(await screen.findByRole("button", { name: "Stage deletion" }));
    expect(stage).toHaveBeenCalledWith(["tex/rock.png"]);
  });

  it("opens at once when the lookup is slow and fills in later", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const pending: ((r: AssetReferences) => void)[] = [];
      api.assetRefs.mockImplementation((_root: string, path: string) => new Promise<AssetReferences>((resolve) => pending.push(() => resolve(refs(path, users[path])))));
      const stage = vi.fn();
      renderWithProviders(<Harness stage={stage} paths={["tex/rock.png", "tex/bark.png"]} />);
      await userEvent.click(screen.getByRole("button", { name: "stage" }));
      await act(() => vi.advanceTimersByTimeAsync(QUICK_LOOKUP_MS + 50));
      const dialog = await screen.findByRole("alertdialog");
      expect(dialog).toHaveTextContent("Staging deletes 2 files");
      expect(dialog).toHaveTextContent("Checking which files use them…");
      await act(async () => {
        for (const resolve of pending) resolve(refs("", []));
      });
      expect(await screen.findByText(/Used by 4 files/)).toBeInTheDocument();
      expect(dialog).toHaveTextContent("tex/rock.png:");
      expect(dialog).not.toHaveTextContent("Checking");
    } finally {
      vi.useRealTimers();
    }
  });

  it("says when nothing turned out to use the slow lookups", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let resolve: () => void = () => {};
      api.assetRefs.mockImplementation((_root: string, path: string) => new Promise<AssetReferences>((r) => (resolve = () => r(refs(path, [])))));
      renderWithProviders(<Harness stage={vi.fn()} paths={["tex/bark.png"]} />);
      await userEvent.click(screen.getByRole("button", { name: "stage" }));
      await act(() => vi.advanceTimersByTimeAsync(QUICK_LOOKUP_MS + 50));
      expect(await screen.findByText("Checking which files use it…")).toBeInTheDocument();
      await act(async () => resolve());
      expect(await screen.findByText("No other file references it.")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("ReferrerNote", () => {
  it("names the files that use the asset", async () => {
    renderWithProviders(<ReferrerNote root={ROOT} path="tex/rock.png" />);
    expect(await screen.findByText(/Used by 4 files/)).toBeInTheDocument();
  });

  it("stays empty when disabled, unused or not an asset", async () => {
    const { container, rerender } = renderWithProviders(<ReferrerNote root={ROOT} path="tex/rock.png" enabled={false} />);
    expect(container).toBeEmptyDOMElement();
    expect(api.assetRefs).not.toHaveBeenCalled();
    rerender(<ReferrerNote root={ROOT} path="tex/bark.png" />);
    await waitFor(() => expect(api.assetRefs).toHaveBeenCalledWith(ROOT, "tex/bark.png"));
    expect(container).toBeEmptyDOMElement();
  });
});
