import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { renderWithProviders } from "./test/render";
import type { Repo } from "./api/desktop";

const api = vi.hoisted(() => ({
  connect: vi.fn(),
  repos: vi.fn(),
  pickRepo: vi.fn(),
  addRepo: vi.fn(),
  removeRepo: vi.fn(),
  changes: vi.fn(),
  fileDiff: vi.fn(),
  onRepoChanged: vi.fn(),
  onOpProgress: vi.fn(),
  accountStatus: vi.fn(),
  listProjects: vi.fn(),
  remoteBranches: vi.fn(),
  remoteTree: vi.fn(),
  clone: vi.fn(),
  activity: vi.fn(),
  onRepoActivity: vi.fn(),
  takeNavigation: vi.fn(),
  onNavigate: vi.fn(),
  ignoreRules: vi.fn(),
  settings: vi.fn(),
  uploadWorkers: vi.fn(),
}));
vi.mock("./api/desktop", () => ({ desktop: api }));

const daemon = { version: "v0.9.0", pid: 42, port: 7000, started: true };
const game: Repo = { root: "/work/game", name: "game", url: "https://nipa.test/studio/game", branch: "main", sparse: ["art", "audio"] };
const tools: Repo = { root: "/work/tools", name: "tools", url: "https://nipa.test/studio/tools", branch: "dev", sparse: null };

beforeEach(() => {
  vi.resetAllMocks();
  api.connect.mockResolvedValue(daemon);
  api.repos.mockResolvedValue([game, tools]);
  api.changes.mockResolvedValue({ branch: "main", staged: [], unstaged: [], conflicts: [] });
  api.onRepoChanged.mockReturnValue(() => {});
  api.onOpProgress.mockReturnValue(() => {});
  api.activity.mockResolvedValue([]);
  api.onRepoActivity.mockReturnValue(() => {});
  api.takeNavigation.mockResolvedValue(null);
  api.onNavigate.mockReturnValue(() => {});
  api.ignoreRules.mockResolvedValue({ shared: "", local: "" });
});

const openDetails = () => userEvent.click(screen.getByRole("tab", { name: /Details/ }));

describe("App", () => {
  it("shows the splash, then the first repository", async () => {
    renderWithProviders(<App />);
    expect(screen.getByRole("status")).toHaveTextContent("Starting Nipa");

    expect(await screen.findByRole("heading", { name: "game" })).toBeInTheDocument();
    expect(screen.getByText("Daemon v0.9.0")).toBeInTheDocument();
    expect(await screen.findByText("No local changes")).toBeInTheDocument();
    await openDetails();
    expect(screen.getByText("https://nipa.test/studio/game")).toBeInTheDocument();
    expect(screen.getByText("art")).toBeInTheDocument();
    const nav = screen.getByRole("navigation", { name: "Repositories" });
    expect(within(nav).getByRole("button", { name: /game/ })).toHaveAttribute("aria-current", "page");
  });

  it("switches repositories and remembers the choice", async () => {
    renderWithProviders(<App />);
    const nav = await screen.findByRole("navigation", { name: "Repositories" });
    await userEvent.click(within(nav).getByRole("button", { name: /tools/ }));

    expect(screen.getByRole("heading", { name: "tools" })).toBeInTheDocument();
    await openDetails();
    expect(screen.getByText("Full checkout")).toBeInTheDocument();
    expect(localStorage.getItem("nipa.selectedRepo")).toBe("/work/tools");
  });

  it("restores the remembered repository", async () => {
    localStorage.setItem("nipa.selectedRepo", "/work/tools");
    renderWithProviders(<App />);
    expect(await screen.findByRole("heading", { name: "tools" })).toBeInTheDocument();
  });

  it("shows the welcome screen without repositories and opens a clone", async () => {
    api.repos.mockResolvedValue([]);
    api.pickRepo.mockResolvedValueOnce(null).mockResolvedValueOnce(tools);
    renderWithProviders(<App />);

    const open = await screen.findByRole("button", { name: /Open a clone…/ });
    await userEvent.click(open);
    expect(screen.getByRole("heading", { name: "Welcome to Nipa" })).toBeInTheDocument();

    await userEvent.click(open);
    expect(await screen.findByRole("heading", { name: "tools" })).toBeInTheDocument();
    expect(localStorage.getItem("nipa.selectedRepo")).toBe("/work/tools");
  });

  it("clones a repository and opens it", async () => {
    api.repos.mockResolvedValue([]);
    api.accountStatus.mockResolvedValue({ host: "nipa.test", address: "https://nipa.test", signedIn: true, email: "", userId: "u" });
    api.listProjects.mockResolvedValue([{ org: "studio", orgName: "Studio", orgRole: "owner", project: "game", name: "Game", description: "" }]);
    api.remoteBranches.mockResolvedValue([{ name: "main", isDefault: true, protected: false }]);
    api.clone.mockResolvedValue(game);
    renderWithProviders(<App />);
    await userEvent.click(await screen.findByRole("button", { name: /Clone a repository…/ }));
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "https://nipa.test{Enter}");
    await userEvent.click(await screen.findByRole("button", { name: /Game/ }));
    await screen.findByRole("option", { name: "main (default)" });
    await userEvent.type(screen.getByRole("textbox", { name: "Location" }), "/work");
    await userEvent.click(screen.getByRole("button", { name: "Clone" }));
    expect(await screen.findByRole("heading", { name: "game" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByText("Cloned game")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Clone a repository" }));
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("does not duplicate a clone that is opened twice", async () => {
    api.pickRepo.mockResolvedValue(game);
    renderWithProviders(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Open a clone" }));

    const nav = screen.getByRole("navigation", { name: "Repositories" });
    await waitFor(() => expect(api.pickRepo).toHaveBeenCalled());
    expect(within(nav).getAllByRole("button")).toHaveLength(2);
  });

  it("replaces a stale entry when a broken clone is opened again", async () => {
    api.repos.mockResolvedValue([{ ...game, error: "This clone is unavailable" }, tools]);
    api.pickRepo.mockResolvedValue(game);
    renderWithProviders(<App />);
    expect((await screen.findAllByText("This clone is unavailable")).length).toBeGreaterThan(0);

    await userEvent.click(screen.getByRole("button", { name: "Open a clone" }));
    await openDetails();
    expect(await screen.findByText("https://nipa.test/studio/game")).toBeInTheDocument();
    expect(screen.queryAllByText("This clone is unavailable")).toHaveLength(0);
  });

  it("reports a failed open in a dismissible toast", async () => {
    api.pickRepo.mockRejectedValue(new Error("/tmp is not inside a nipa clone"));
    renderWithProviders(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Open a clone" }));

    const alert = await screen.findByText("/tmp is not inside a nipa clone");
    expect(alert).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("/tmp is not inside a nipa clone")).not.toBeInTheDocument();
  });

  it("removes a repository and falls back to the next one", async () => {
    api.removeRepo.mockResolvedValue(undefined);
    localStorage.setItem("nipa.selectedRepo", "/work/game");
    renderWithProviders(<App />);
    await userEvent.click(await screen.findByRole("button", { name: /Remove/ }));

    expect(await screen.findByRole("heading", { name: "tools" })).toBeInTheDocument();
    expect(api.removeRepo.mock.calls[0][0]).toBe("/work/game");
    expect(localStorage.getItem("nipa.selectedRepo")).toBeNull();
  });

  it("keeps a non-selected repository selected when another is removed", async () => {
    api.removeRepo.mockResolvedValue(undefined);
    api.repos.mockResolvedValue([{ ...game, error: "gone" }, tools]);
    localStorage.setItem("nipa.selectedRepo", "/work/tools");
    renderWithProviders(<App />);
    await screen.findByRole("heading", { name: "tools" });
    await userEvent.click(screen.getByRole("button", { name: /Remove/ }));
    await waitFor(() => expect(api.removeRepo).toHaveBeenCalledWith("/work/tools", expect.anything()));
  });

  it("shows unavailable clones with their error", async () => {
    api.repos.mockResolvedValue([{ ...game, error: "/work/game is not a nipa clone" }]);
    renderWithProviders(<App />);
    expect((await screen.findAllByText("This clone is unavailable")).length).toBeGreaterThan(0);
    expect(screen.getByText("/work/game is not a nipa clone")).toBeInTheDocument();
    expect(screen.getByLabelText("Unavailable")).toBeInTheDocument();
  });

  it("reports a failed removal", async () => {
    api.removeRepo.mockRejectedValue("store is read-only");
    renderWithProviders(<App />);
    await userEvent.click(await screen.findByRole("button", { name: /Remove/ }));
    expect(await screen.findByText("store is read-only")).toBeInTheDocument();
  });

  it("explains a daemon failure and retries", async () => {
    api.connect.mockRejectedValueOnce(new Error("nipa CLI not found")).mockResolvedValueOnce(daemon);
    renderWithProviders(<App />);

    expect(await screen.findByText("nipa CLI not found")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Try again/ }));
    expect(await screen.findByRole("heading", { name: "game" })).toBeInTheDocument();
  });

  it("explains a failed repository load and retries", async () => {
    api.repos.mockRejectedValueOnce(new Error("connection refused")).mockResolvedValueOnce([game]);
    renderWithProviders(<App />);

    expect(await screen.findByText("connection refused")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Try again/ }));
    expect(await screen.findByRole("heading", { name: "game" })).toBeInTheDocument();
  });

  it("shows teammate activity in the sidebar and on the Update button", async () => {
    let push: ((a: unknown) => void) | undefined;
    api.onRepoActivity.mockImplementation((fn: (a: unknown) => void) => {
      push = fn;
      return () => {};
    });
    api.activity.mockResolvedValue([{ root: "/work/tools", name: "tools", incoming: 0, reviewRequests: 2, error: "", events: [] }]);
    renderWithProviders(<App />);
    expect(await screen.findByTitle("2 merge requests waiting for your review")).toBeInTheDocument();

    act(() => push?.({ root: "/work/game", name: "game", incoming: 3, reviewRequests: 0, error: "offline", events: [{ kind: "pushed" }, { kind: "other" }] }));
    expect(await screen.findByTitle("3 new commits to get")).toBeInTheDocument();
    expect(screen.getByLabelText("Server unreachable: offline")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Update/ })).toHaveAttribute("title", "3 new commits on main");
    act(() => push?.({ root: "/work/game", name: "game", incoming: 120, reviewRequests: 1, error: "", events: [] }));
    expect(await screen.findByTitle("100+ new commits to get")).toBeInTheDocument();
    expect(screen.getByTitle("1 merge request waiting for your review")).toBeInTheDocument();
  });

  it("follows a navigation request to a clone and tab", async () => {
    api.takeNavigation.mockResolvedValueOnce({ root: "/work/tools", tab: "details", number: 0, commit: "", server: "", org: "", project: "" });
    renderWithProviders(<App />);
    expect(await screen.findByRole("heading", { name: "tools" })).toBeInTheDocument();
    expect(await screen.findByRole("tab", { name: /Details/ })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByLabelText("Shared rules (.nipaignore)")).toBeInTheDocument();
  });

  it("opens settings and the clone dialog from navigation events", async () => {
    let fire: (() => void) | undefined;
    api.onNavigate.mockImplementation((fn: () => void) => {
      fire = fn;
      return () => {};
    });
    api.settings.mockReturnValue(new Promise(() => {}));
    api.uploadWorkers.mockResolvedValue(0);
    api.accountStatus.mockReturnValue(new Promise(() => {}));
    renderWithProviders(<App />);
    await screen.findByRole("heading", { name: "game" });

    api.takeNavigation.mockResolvedValueOnce({ root: "", tab: "settings", number: 0, commit: "", server: "", org: "", project: "" });
    act(() => fire?.());
    expect(await screen.findByRole("dialog", { name: "Settings" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
    expect(screen.queryByRole("dialog", { name: "Settings" })).not.toBeInTheDocument();

    api.takeNavigation.mockResolvedValueOnce({ root: "", tab: "changes", number: 0, commit: "", server: "https://nipa.test", org: "studio", project: "art" });
    act(() => fire?.());
    expect(await screen.findByText("studio/art is not cloned on this computer yet")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Server" })).toHaveValue("https://nipa.test");
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("dialog", { name: /Clone/ })).not.toBeInTheDocument();

    api.takeNavigation.mockResolvedValueOnce({ root: "", tab: "changes", number: 0, commit: "", server: "", org: "", project: "" });
    act(() => fire?.());
    api.takeNavigation.mockRejectedValueOnce(new Error("gone"));
    act(() => fire?.());
    await userEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(await screen.findByRole("dialog", { name: "Settings" })).toBeInTheDocument();
  });
});
