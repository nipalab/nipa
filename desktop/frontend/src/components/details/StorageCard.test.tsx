import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SPARSE_HINT_BYTES, StorageCard, describeCleanUp } from "./StorageCard";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ diskUsage: vi.fn(), cleanUp: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

const notify = vi.fn();
const onEditSparse = vi.fn();
const usage = { workingFiles: 1234, workingBytes: 5 * 1024 ** 3, cacheObjects: 4321, cacheBytes: 300 * 1024 ** 2, pins: [] as string[], sparse: [] as string[] };

const card = (busy = false) => renderWithProviders(<StorageCard root="/work/game" busy={busy} notify={notify} onEditSparse={onEditSparse} />);

beforeEach(() => {
  vi.resetAllMocks();
  api.diskUsage.mockResolvedValue(usage);
});

describe("StorageCard", () => {
  it("shows the three numbers without pins or hint", async () => {
    card();
    expect(screen.getByText("Measuring disk usage…")).toBeInTheDocument();
    expect(await screen.findByText("5.0 GB")).toBeInTheDocument();
    expect(screen.getByText("1,234 files")).toBeInTheDocument();
    expect(screen.getByText("300 MB")).toBeInTheDocument();
    expect(screen.getByText("4,321")).toBeInTheDocument();
    expect(screen.queryByText(/locked files/)).not.toBeInTheDocument();
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    expect(api.diskUsage).toHaveBeenCalledWith("/work/game");
  });

  it("lists pinned paths", async () => {
    api.diskUsage.mockResolvedValue({ ...usage, workingFiles: 1, pins: ["art/hero.psd", "levels"] });
    card();
    expect(await screen.findByText("art/hero.psd")).toBeInTheDocument();
    expect(screen.getByText("levels")).toBeInTheDocument();
    expect(screen.getByText("1 file")).toBeInTheDocument();
    expect(screen.getByText("Kept in the cache for your locked files")).toBeInTheDocument();
  });

  it("hints at a sparse checkout only for a large full checkout", async () => {
    api.diskUsage.mockResolvedValue({ ...usage, workingBytes: SPARSE_HINT_BYTES + 1 });
    const { unmount } = card();
    const link = await screen.findByRole("button", { name: /Check out only the folders you need/ });
    expect(screen.getByRole("note")).toHaveTextContent("This full checkout is over 20 GB.");
    await userEvent.click(link);
    expect(onEditSparse).toHaveBeenCalledTimes(1);
    unmount();

    api.diskUsage.mockResolvedValue({ ...usage, workingBytes: SPARSE_HINT_BYTES + 1, sparse: ["art"] });
    const sparse = card();
    await screen.findByText("Cached objects");
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    sparse.unmount();

    api.diskUsage.mockResolvedValue({ ...usage, workingBytes: SPARSE_HINT_BYTES });
    card();
    await screen.findByText("Cached objects");
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  it("cleans up, reports the freed amount and refreshes", async () => {
    let finish!: (v: unknown) => void;
    api.cleanUp.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    card();
    const button = await screen.findByRole("button", { name: /Clean up/ });
    api.diskUsage.mockResolvedValue({ ...usage, cacheBytes: 0, cacheObjects: 0 });
    await userEvent.click(button);
    expect(screen.getByRole("button", { name: /Cleaning up/ })).toBeDisabled();
    finish({ objects: 12, bytes: 3 * 1024 ** 2, skipped: false, reason: "" });
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Freed 3.0 MB (12 objects)", "success"));
    expect(await screen.findByText("0 B")).toBeInTheDocument();
    expect(api.cleanUp).toHaveBeenCalledWith("/work/game");
    expect(api.diskUsage).toHaveBeenCalledTimes(2);
  });

  it("reports a busy repository", async () => {
    api.cleanUp.mockResolvedValue({ objects: 0, bytes: 0, skipped: true, reason: "busy" });
    card();
    await userEvent.click(await screen.findByRole("button", { name: /Clean up/ }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Repository is busy, try again", "error"));
  });

  it("reports a failed clean up", async () => {
    api.cleanUp.mockRejectedValue(new Error("disk on fire"));
    card();
    await userEvent.click(await screen.findByRole("button", { name: /Clean up/ }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Clean up failed: disk on fire"));
  });

  it("disables clean up while an operation runs", async () => {
    api.diskUsage.mockResolvedValue({ ...usage, workingBytes: SPARSE_HINT_BYTES * 2 });
    card(true);
    expect(await screen.findByRole("button", { name: /Clean up/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /Check out only the folders you need/ })).toBeDisabled();
  });

  it("reports a failed read", async () => {
    api.diskUsage.mockRejectedValue(new Error("not watched"));
    card();
    expect(await screen.findByText("Could not read the disk usage: not watched")).toBeInTheDocument();
  });

  it("describes every clean-up outcome", () => {
    expect(describeCleanUp({ objects: 0, bytes: 0, skipped: true, reason: "pending" })[0]).toMatch(/merge or revert is in progress/);
    expect(describeCleanUp({ objects: 0, bytes: 0, skipped: true, reason: "other" })).toEqual(["Repository is busy, try again", "error"]);
    expect(describeCleanUp({ objects: 0, bytes: 0, skipped: false, reason: "" })).toEqual(["Nothing to clean up", "success"]);
    expect(describeCleanUp({ objects: 1, bytes: 2048, skipped: false, reason: "" })).toEqual(["Freed 2.0 KB (1 object)", "success"]);
  });
});
