import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { UpdateBanner } from "./UpdateBanner";
import { renderWithProviders } from "@/test/render";
import type { UpdateState } from "@/api/desktop";

const api = vi.hoisted(() => ({
  updateStatus: vi.fn(),
  onUpdate: vi.fn(),
  checkForUpdate: vi.fn(),
  installUpdate: vi.fn(),
  restartToUpdate: vi.fn(),
  openUpdatePage: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

const release = { version: "1.1.0", notes: "", publishedAt: "", page: "https://example/r" };
const base: UpdateState = { current: "1.0.0", enabled: true, inPlace: true, phase: "available", release, written: 0, total: 0, error: "", checkedAt: "" };
const notify = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.onUpdate.mockReturnValue(() => {});
});

describe("UpdateBanner", () => {
  it("stays hidden without a release", async () => {
    api.updateStatus.mockResolvedValue({ ...base, phase: "upToDate", release: null });
    const { container } = renderWithProviders(<UpdateBanner notify={notify} />);
    await waitFor(() => expect(api.updateStatus).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("stays hidden while downloading", async () => {
    api.updateStatus.mockResolvedValue({ ...base, phase: "downloading" });
    const { container } = renderWithProviders(<UpdateBanner notify={notify} />);
    await waitFor(() => expect(api.updateStatus).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("downloads an available update", async () => {
    api.updateStatus.mockResolvedValue(base);
    api.installUpdate.mockResolvedValue({ ...base, phase: "ready" });
    renderWithProviders(<UpdateBanner notify={notify} />);
    await userEvent.click(await screen.findByRole("button", { name: "Download" }));
    expect(await screen.findByText("Nipa 1.1.0 is ready.")).toBeInTheDocument();
    api.restartToUpdate.mockRejectedValue(new Error("busy"));
    await userEvent.click(screen.getByRole("button", { name: "Restart to update" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("busy"));
  });

  it("opens the release page and can be dismissed", async () => {
    api.updateStatus.mockResolvedValue({ ...base, inPlace: false });
    api.openUpdatePage.mockResolvedValue(undefined);
    renderWithProviders(<UpdateBanner notify={notify} />);
    await userEvent.click(await screen.findByRole("button", { name: "Get it from the website" }));
    expect(api.openUpdatePage).toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Dismiss update" }));
    expect(screen.queryByText("Nipa 1.1.0 is available.")).toBeNull();
  });

  it("reports a failed download", async () => {
    api.updateStatus.mockResolvedValue(base);
    api.installUpdate.mockRejectedValue(new Error("disk full"));
    renderWithProviders(<UpdateBanner notify={notify} />);
    await userEvent.click(await screen.findByRole("button", { name: "Download" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("disk full"));
  });
});
