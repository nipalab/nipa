import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FileLock, LockStatus } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ lockStatus: vi.fn(), lock: vi.fn(), unlock: vi.fn(), pickFile: vi.fn(), openFile: vi.fn(), requestRelease: vi.fn(), lockWatches: vi.fn(), watchLock: vi.fn(), unwatchLock: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { LocksView } from "./LocksView";

const lock = (over: Partial<FileLock>): FileLock => ({
  id: over.path ?? "x",
  path: "art/hero.psd",
  branch: "",
  global: true,
  heldBy: "me",
  heldByName: "Me",
  mine: true,
  mergeRequest: 0,
  acquiredAt: new Date(Date.now() - 5 * 60_000).toISOString(),
  inScope: true,
  requests: [],
  ...over,
});

const status = (over: Partial<LockStatus> = {}): LockStatus => ({
  userId: "me",
  branch: "main",
  defaultBranch: true,
  canReleaseAny: false,
  locks: [
    lock({ path: "art/hero.psd" }),
    lock({ path: "audio", heldBy: "bob", heldByName: "Bob", mine: false }),
    lock({ path: "maps/level1.umap", heldBy: "ann", heldByName: "", mine: false, mergeRequest: 7, acquiredAt: "" }),
    lock({ path: "ui/old.png", global: false, branch: "feature", inScope: false }),
  ],
  files: [],
  ...over,
});

const notify = vi.fn();
const view = () => renderWithProviders(<LocksView root="/work/game" notify={notify} />);
const rows = () => within(screen.getByRole("table", { name: "File locks" })).getAllByRole("row").slice(1);

beforeEach(() => {
  vi.resetAllMocks();
  api.lockStatus.mockResolvedValue(status());
  api.unlock.mockResolvedValue(undefined);
  api.openFile.mockResolvedValue(undefined);
  api.requestRelease.mockResolvedValue(undefined);
  api.lockWatches.mockResolvedValue([]);
  api.watchLock.mockResolvedValue(undefined);
  api.unwatchLock.mockResolvedValue(undefined);
});

describe("LocksView", () => {
  it("lists locks in scope first, mine before the team's", async () => {
    view();
    expect(screen.getByText("Loading locks…")).toBeInTheDocument();
    await screen.findByRole("table", { name: "File locks" });
    expect(screen.getByText(/check locks on the/)).toHaveTextContent("Pushes to main check locks on the mainline (main).");
    expect(rows().map((r) => (r as HTMLTableRowElement).cells[0].textContent)).toEqual(["art/hero.psd", "audio", "maps/level1.umap!7", "ui/old.png"]);
    expect(within(rows()[0]).getByText("You")).toBeInTheDocument();
    expect(within(rows()[0]).getByText("5 min ago")).toBeInTheDocument();
    expect(within(rows()[1]).getByLabelText("Folder lock")).toBeInTheDocument();
    expect(within(rows()[1]).queryByRole("button", { name: /Release/ })).not.toBeInTheDocument();
    expect(within(rows()[2]).getByText("ann")).toBeInTheDocument();
    expect(within(rows()[3]).getByText("feature")).toBeInTheDocument();
    expect(within(rows()[3]).getByRole("button", { name: "Release ui/old.png" })).toBeInTheDocument();
  });

  it("filters by owner and text", async () => {
    view();
    await screen.findByRole("table", { name: "File locks" });
    await userEvent.click(screen.getByRole("button", { name: "Mine (2)" }));
    expect(rows()).toHaveLength(2);
    await userEvent.click(screen.getByRole("button", { name: "Team (2)" }));
    expect(rows().map((r) => (r as HTMLTableRowElement).cells[0].textContent)).toEqual(["audio", "maps/level1.umap!7"]);
    await userEvent.type(screen.getByRole("textbox", { name: "Filter locks" }), "bob");
    expect(rows()).toHaveLength(1);
    await userEvent.type(screen.getByRole("textbox", { name: "Filter locks" }), "zzz");
    expect(screen.getByText("No locks match")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "All (4)" }));
  });

  it("releases my locks directly and others' after confirming", async () => {
    api.lockStatus.mockResolvedValue(status({ canReleaseAny: true }));
    view();
    await screen.findByRole("table", { name: "File locks" });
    await userEvent.click(screen.getByRole("button", { name: "Release art/hero.psd" }));
    await waitFor(() => expect(api.unlock).toHaveBeenCalledWith("/work/game", "", "art/hero.psd"));
    expect(notify).toHaveBeenCalledWith("Released art/hero.psd", "success");

    await userEvent.click(screen.getByRole("button", { name: "Release audio" }));
    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Bob holds audio.");
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(api.unlock).toHaveBeenCalledTimes(1);

    await userEvent.click(screen.getByRole("button", { name: "Release ui/old.png" }));
    await waitFor(() => expect(api.unlock).toHaveBeenLastCalledWith("/work/game", "feature", "ui/old.png"));

    await userEvent.click(screen.getByRole("button", { name: "Release audio" }));
    await userEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Release" }));
    await waitFor(() => expect(api.unlock).toHaveBeenLastCalledWith("/work/game", "", "audio"));

    api.unlock.mockRejectedValueOnce(new Error("no permission"));
    await userEvent.click(screen.getByRole("button", { name: "Release art/hero.psd" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Releasing failed: no permission"));
  });

  it("opens my locked files", async () => {
    view();
    await screen.findByRole("table", { name: "File locks" });
    await userEvent.click(screen.getByRole("button", { name: "Open art/hero.psd" }));
    expect(api.openFile).toHaveBeenCalledWith("/work/game", "art/hero.psd");
    api.openFile.mockRejectedValueOnce(new Error("no app"));
    await userEvent.click(screen.getByRole("button", { name: "Open art/hero.psd" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not open art/hero.psd: no app"));
  });

  it("locks a picked file and opens it", async () => {
    api.pickFile.mockResolvedValueOnce("art/sky.psd").mockResolvedValueOnce("").mockResolvedValueOnce("art/taken.psd").mockRejectedValueOnce(new Error("dialog broke"));
    api.lock.mockResolvedValueOnce({ locked: ["art/sky.psd"], failed: [] }).mockResolvedValueOnce({ locked: [], failed: [{ path: "art/taken.psd", message: "locked by Bob" }] });
    view();
    const button = await screen.findByRole("button", { name: /Lock & edit a file/ });
    await userEvent.click(button);
    await waitFor(() => expect(api.openFile).toHaveBeenCalledWith("/work/game", "art/sky.psd"));
    expect(api.lock).toHaveBeenCalledWith("/work/game", "main", ["art/sky.psd"]);
    expect(notify).toHaveBeenCalledWith("Locked art/sky.psd", "success");

    await userEvent.click(button);
    await waitFor(() => expect(button).toBeEnabled());
    expect(api.lock).toHaveBeenCalledTimes(1);

    await userEvent.click(button);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not lock art/taken.psd: locked by Bob"));
    expect(api.openFile).toHaveBeenCalledTimes(1);

    await userEvent.click(button);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("dialog broke"));
  });

  it("explains empty lists, branch scopes and failures", async () => {
    api.lockStatus.mockResolvedValueOnce(status({ locks: [], defaultBranch: false, branch: "feature" }));
    const { unmount } = view();
    expect(await screen.findByText("Nothing is locked")).toBeInTheDocument();
    expect(screen.getByText(/check locks on the/)).toHaveTextContent("on the branch feature.");
    unmount();

    api.lockStatus.mockRejectedValueOnce(new Error("server unreachable"));
    view();
    expect(await screen.findByText("server unreachable")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("table", { name: "File locks" })).toBeInTheDocument();
    const before = api.lockStatus.mock.calls.length;
    await userEvent.click(screen.getByRole("button", { name: "Refresh locks" }));
    await waitFor(() => expect(api.lockStatus.mock.calls.length).toBeGreaterThan(before));
  });

  it("resizes columns and restores their widths", async () => {
    localStorage.setItem("nipa.layout.cols.locks", JSON.stringify({ scope: 200 }));
    view();
    const table = await screen.findByRole("table", { name: "File locks" });
    const cols = table.querySelectorAll("col");
    expect(cols[0]).toHaveStyle({ width: "420px" });
    expect(cols[1]).toHaveStyle({ width: "200px" });
    expect(screen.getByRole("columnheader", { name: "Path" })).toBeInTheDocument();

    screen.getByRole("separator", { name: "Resize Path column" }).focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(cols[0]).toHaveStyle({ width: "436px" });
    expect(JSON.parse(localStorage.getItem("nipa.layout.cols.locks")!)).toEqual({ scope: 200, path: 436 });
  });

  it("watches a teammate's lock and stops watching it", async () => {
    api.lockWatches.mockResolvedValue([{ branch: "", path: "maps/level1.umap" }]);
    view();
    await screen.findByRole("table", { name: "File locks" });
    expect(within(rows()[0]).queryByRole("button", { name: /free|watching/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Notify me when audio is free" }));
    expect(api.watchLock).toHaveBeenCalledWith("/work/game", "", "audio");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("You will be told when audio is free", "success"));
    await userEvent.click(await screen.findByRole("button", { name: "Stop watching maps/level1.umap" }));
    expect(api.unwatchLock).toHaveBeenCalledWith("/work/game", "", "maps/level1.umap");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Stopped watching maps/level1.umap", "success"));
  });

  it("keeps watches of the same path on another branch scope apart", async () => {
    api.lockStatus.mockResolvedValue(
      status({ locks: [lock({ path: "audio", heldBy: "bob", mine: false }), lock({ path: "audio", global: false, branch: "feature", heldBy: "ann", mine: false })] }),
    );
    api.lockWatches.mockResolvedValue([{ branch: "feature", path: "audio" }]);
    view();
    await screen.findByRole("table", { name: "File locks" });
    expect(screen.getAllByRole("button", { name: "Notify me when audio is free" })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Stop watching audio" })).toHaveLength(1);
  });

  it("reports a failed watch", async () => {
    api.watchLock.mockRejectedValue(new Error("nope"));
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Notify me when audio is free" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("nope"));
  });

  it("asks a teammate to release a lock with a message", async () => {
    view();
    await screen.findByRole("table", { name: "File locks" });
    expect(screen.queryByRole("button", { name: "Ask to release art/hero.psd" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Ask to release audio" }));
    expect(screen.getByText(/Bob holds/)).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: /Message/ }), "  need it today ");
    await userEvent.click(screen.getByRole("button", { name: "Send request" }));
    expect(api.requestRelease).toHaveBeenCalledWith("/work/game", "audio", "need it today");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Asked Bob to release audio", "success"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps the dialog open and reports a failed request, and cancels with Escape", async () => {
    api.requestRelease.mockRejectedValue(new Error("you hold this lock"));
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Ask to release audio" }));
    await userEvent.click(screen.getByRole("button", { name: "Send request" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("you hold this lock"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Ask to release audio" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows my lock's requests and says when I already asked", async () => {
    api.lockStatus.mockResolvedValue(
      status({
        locks: [
          lock({ path: "art/hero.psd", requests: [{ id: "q1", requesterId: "bob", requesterName: "Bob", message: "soon?", requestedAt: "" }, { id: "q2", requesterId: "ann", requesterName: "", message: "", requestedAt: "" }] }),
          lock({ path: "audio", heldBy: "bob", heldByName: "Bob", mine: false, requests: [{ id: "q3", requesterId: "me", requesterName: "Me", message: "", requestedAt: "" }] }),
        ],
      }),
    );
    view();
    const badge = await screen.findByText("2 requests");
    expect(badge.closest("span[title]")).toHaveAttribute("title", "Bob: soon?\nann");
    expect(screen.getByRole("button", { name: "Ask to release audio" })).toHaveAttribute("title", "You already asked; ask again");
  });

  it("labels a single request", async () => {
    api.lockStatus.mockResolvedValue(status({ locks: [lock({ path: "a.psd", requests: [{ id: "q1", requesterId: "bob", requesterName: "Bob", message: "", requestedAt: "" }] })] }));
    view();
    expect(await screen.findByText("1 request")).toBeInTheDocument();
  });
});
