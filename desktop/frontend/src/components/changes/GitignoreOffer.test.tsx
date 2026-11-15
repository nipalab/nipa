import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";
import { resetSettingsForTest } from "@/lib/settings";

const api = vi.hoisted(() => ({ canImportGitignore: vi.fn(), importGitignore: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { GitignoreOffer } from "./GitignoreOffer";

const ROOT = "/work/web";

beforeEach(() => {
  vi.resetAllMocks();
  resetSettingsForTest();
  api.canImportGitignore.mockResolvedValue(true);
  api.importGitignore.mockResolvedValue({ added: 3, unsupported: [] });
});

describe("GitignoreOffer", () => {
  it("creates .nipaignore and goes away", async () => {
    const notify = vi.fn();
    renderWithProviders(<GitignoreOffer root={ROOT} notify={notify} />);
    await userEvent.click(await screen.findByRole("button", { name: "Create .nipaignore from .gitignore" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Added 3 rules to .nipaignore", "success"));
    expect(api.importGitignore).toHaveBeenCalledWith(ROOT);
    expect(screen.queryByRole("button", { name: "Create .nipaignore from .gitignore" })).not.toBeInTheDocument();
  });

  it("names the rules that will not match", async () => {
    api.importGitignore.mockResolvedValue({ added: 2, unsupported: ["[Bb]in/"] });
    const notify = vi.fn();
    renderWithProviders(<GitignoreOffer root={ROOT} notify={notify} />);
    await userEvent.click(await screen.findByRole("button", { name: "Create .nipaignore from .gitignore" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("1 rule uses syntax Nipa does not support and will not match: [Bb]in/", "error"));
  });

  it("reports a failed import", async () => {
    api.importGitignore.mockRejectedValue(new Error("disk full"));
    const notify = vi.fn();
    renderWithProviders(<GitignoreOffer root={ROOT} notify={notify} />);
    await userEvent.click(await screen.findByRole("button", { name: "Create .nipaignore from .gitignore" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("disk full", "error"));
  });

  it("can be dismissed for good", async () => {
    const { unmount } = renderWithProviders(<GitignoreOffer root={ROOT} notify={vi.fn()} />);
    await userEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText(/This project has a .gitignore/)).not.toBeInTheDocument();
    unmount();
    api.canImportGitignore.mockClear();
    renderWithProviders(<GitignoreOffer root={ROOT} notify={vi.fn()} />);
    expect(screen.queryByText(/This project has a .gitignore/)).not.toBeInTheDocument();
    expect(api.canImportGitignore).not.toHaveBeenCalled();
  });

  it("shows nothing without a .gitignore", async () => {
    api.canImportGitignore.mockResolvedValue(false);
    const { container } = renderWithProviders(<GitignoreOffer root={ROOT} notify={vi.fn()} />);
    await waitFor(() => expect(api.canImportGitignore).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
