import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChangeSet, GeneratedFolder } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ assetRefs: vi.fn(), generatedFolders: vi.fn(), ignoreFolders: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { useStageGuard } from "./DeleteSafety";
import { outsideFolders, untrackedPaths } from "./GeneratedFolders";

const ROOT = "/work/web";
const changes: ChangeSet = {
  branch: "main",
  staged: [],
  unstaged: [
    { path: "src/app.ts", status: "untracked", kind: "text" },
    { path: "node_modules/a/index.js", status: "untracked", kind: "text" },
    { path: "node_modules/b/index.js", status: "untracked", kind: "text" },
    { path: "README.md", status: "modified", kind: "text" },
  ],
  conflicts: [],
  resolved: [],
};
const all = changes.unstaged.map((f) => f.path);
const nodeModules: GeneratedFolder = { dir: "node_modules", files: 2, pattern: "node_modules/" };

function Harness({ stage, onError = vi.fn() }: { stage: (paths: string[]) => void; onError?: (m: string) => void }) {
  const guard = useStageGuard(ROOT, changes, stage, onError);
  return (
    <>
      <button type="button" onClick={() => guard.request(all)}>
        stage
      </button>
      {guard.dialog}
    </>
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.generatedFolders.mockResolvedValue([nodeModules]);
  api.ignoreFolders.mockResolvedValue(1);
});

describe("helpers", () => {
  it("keeps only untracked paths", () => {
    expect(untrackedPaths(changes, all)).toEqual(["src/app.ts", "node_modules/a/index.js", "node_modules/b/index.js"]);
    expect(untrackedPaths(undefined, all)).toEqual([]);
  });

  it("drops paths inside the folders", () => {
    expect(outsideFolders(["node_modules", "node_modules/x", "node_modules2/y", "a"], [nodeModules])).toEqual(["node_modules2/y", "a"]);
  });
});

describe("generated folders guard", () => {
  it("asks before staging new files in a generated folder", async () => {
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(api.generatedFolders).toHaveBeenCalledWith(ROOT, ["src/app.ts", "node_modules/a/index.js", "node_modules/b/index.js"]);
    expect(dialog).toHaveTextContent("This folder looks generated");
    expect(dialog).toHaveTextContent("node_modules/");
    expect(dialog).toHaveTextContent("2 files");
    expect(stage).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(stage).not.toHaveBeenCalled();
  });

  it("stages everything when asked to", async () => {
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await userEvent.click(await screen.findByRole("button", { name: "Stage anyway" }));
    await waitFor(() => expect(stage).toHaveBeenCalledWith(all));
    expect(api.ignoreFolders).not.toHaveBeenCalled();
  });

  it("ignores the folders and stages the rest", async () => {
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await userEvent.click(await screen.findByRole("button", { name: "Ignore folders" }));
    await waitFor(() => expect(stage).toHaveBeenCalledWith(["src/app.ts", "README.md"]));
    expect(api.ignoreFolders).toHaveBeenCalledWith(ROOT, ["node_modules/"]);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("stages nothing when only generated files were picked and ignored", async () => {
    const stage = vi.fn();
    function Only() {
      const guard = useStageGuard(ROOT, changes, stage);
      return (
        <>
          <button type="button" onClick={() => guard.request(["node_modules/a/index.js"])}>
            stage
          </button>
          {guard.dialog}
        </>
      );
    }
    renderWithProviders(<Only />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await userEvent.click(await screen.findByRole("button", { name: "Ignore folders" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(stage).not.toHaveBeenCalled();
  });

  it("reports a failed ignore and keeps the dialog", async () => {
    api.ignoreFolders.mockRejectedValue(new Error("read-only"));
    const onError = vi.fn();
    renderWithProviders(<Harness stage={vi.fn()} onError={onError} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await userEvent.click(await screen.findByRole("button", { name: "Ignore folders" }));
    await waitFor(() => expect(onError).toHaveBeenCalledWith("read-only"));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Ignore folders" })).toBeEnabled();
  });

  it("stages when the lookup fails or finds nothing", async () => {
    api.generatedFolders.mockRejectedValue(new Error("gone"));
    const stage = vi.fn();
    renderWithProviders(<Harness stage={stage} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await waitFor(() => expect(stage).toHaveBeenCalledWith(all));

    api.generatedFolders.mockResolvedValue([]);
    stage.mockClear();
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    await waitFor(() => expect(stage).toHaveBeenCalledWith(all));
  });

  it("lists at most five folders", async () => {
    api.generatedFolders.mockResolvedValue(Array.from({ length: 7 }, (_, i) => ({ dir: `p${i}/node_modules`, files: 1, pattern: "node_modules/" })));
    renderWithProviders(<Harness stage={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: "stage" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("These folders look generated");
    expect(dialog).toHaveTextContent("and 2 more folders");
    expect(dialog).not.toHaveTextContent("p5/node_modules");
  });
});
