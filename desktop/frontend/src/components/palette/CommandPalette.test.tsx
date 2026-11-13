import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Commit, FileHit, MergeRequest, Repo } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ searchFiles: vi.fn(), branches: vi.fn(), tags: vi.fn(), mergeRequests: vi.fn(), history: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { CommandPalette, type PaletteMode } from "./CommandPalette";

const repo = (root: string, over: Partial<Repo> = {}): Repo => ({ root, name: root.slice(1), url: "", branch: "main", sparse: [], ...over });
const game = repo("/game");
const tools = repo("/tools");
const hit = (path: string, kind = "text"): FileHit => ({ path, name: path.slice(path.lastIndexOf("/") + 1), size: 1, binary: kind !== "text", kind });
const commit = (id: string, message: string): Commit => ({ id, hash: "ab".repeat(32), parents: [], author: "Ann", email: "", message, date: "" });
const mr = (number: number, title: string): MergeRequest =>
  ({ number, title, description: "", status: "open", draft: number === 9, source: "feature", target: "main", assignees: [] }) as unknown as MergeRequest;

const onClose = vi.fn();
const onGo = vi.fn();
const onSettings = vi.fn();
const onShortcuts = vi.fn();

const open = (props: { repos?: Repo[]; current?: Repo | null; mode?: PaletteMode } = {}) =>
  renderWithProviders(
    <CommandPalette
      repos={props.repos ?? [game, tools]}
      current={props.current === null ? undefined : (props.current ?? game)}
      initialMode={props.mode}
      onClose={onClose}
      onGo={onGo}
      onSettings={onSettings}
      onShortcuts={onShortcuts}
    />,
  );

const input = () => screen.getByRole("combobox", { name: "Command" });
const options = () => screen.queryAllByRole("option");
const optionWith = async (text: string) => {
  let found: HTMLElement | undefined;
  await waitFor(() => {
    found = screen.getAllByRole("option").find((o) => o.textContent?.includes(text));
    expect(found).toBeDefined();
  });
  return found!;
};
const selectedText = () => screen.getAllByRole("option").find((o) => o.getAttribute("aria-selected") === "true")?.textContent;

beforeEach(() => {
  vi.resetAllMocks();
  api.searchFiles.mockImplementation(async (root: string) => ({
    hits: root === "/game" ? [hit("Assets/Hero/armor.png", "image"), hit("docs/hero.md")] : [hit("Art/hero.png", "image")],
    truncated: false,
  }));
  api.branches.mockResolvedValue([
    { name: "main", commitId: "c1", default: true, protected: true },
    { name: "hero-rework", commitId: "c2", default: false, protected: false },
  ]);
  api.tags.mockResolvedValue([{ name: "hero-v1", commitId: "c3", message: "", createdAt: "", createdBy: "" }]);
  api.mergeRequests.mockResolvedValue({ items: [mr(4, "Hero armor textures"), mr(9, "Hero draft")], next: 0 });
  api.history.mockImplementation(async (_root: string, q: { start?: string }) =>
    q.start ? { commits: q.start === "zz99abcd" ? [commit("zz99abcd", "Old hero work")] : [], next: "" } : { commits: [commit("c1", "Rework hero armor"), commit("c0", "Unrelated")], next: "" },
  );
});

describe("CommandPalette", () => {
  it("lists actions first and runs them", async () => {
    open();
    expect(input()).toHaveFocus();
    expect(screen.getByRole("option", { name: /^Update/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("option", { name: /Go to Files/ })).toHaveTextContent("Ctrl+1");
    expect(screen.queryByRole("option", { name: /Go to Conflicts/ })).toBeNull();
    await userEvent.keyboard("{Enter}");
    expect(onClose).toHaveBeenCalled();
    expect(onGo).toHaveBeenCalledWith("/game", { tab: "", action: "update" });
    expect(api.searchFiles).not.toHaveBeenCalled();
  });

  it("moves through results with the arrows and wraps", async () => {
    open();
    const count = options().length;
    await userEvent.keyboard("{ArrowDown}");
    expect(selectedText()).toMatch(/^Push…/);
    expect(input()).toHaveAttribute("aria-activedescendant", "palette-option-1");
    await userEvent.keyboard("{ArrowUp}{ArrowUp}");
    expect(options()[count - 1]).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{ArrowDown}");
    expect(selectedText()).toMatch(/^Update/);
  });

  it("searches files in every clone, ranked together", async () => {
    open();
    await userEvent.type(input(), "hero");
    const files = await screen.findByRole("option", { name: /Art\/hero\.png/ });
    expect(files).toHaveTextContent("tools");
    await waitFor(() => expect(api.searchFiles).toHaveBeenCalledWith("/game", "hero", 10));
    expect(api.searchFiles).toHaveBeenCalledWith("/tools", "hero", 10);
    const names = options().map((o) => o.textContent ?? "");
    expect(names.findIndex((n) => n.startsWith("Art/hero.png"))).toBeLessThan(names.findIndex((n) => n.startsWith("Assets/Hero/armor.png")));
    expect(within(files).getByText("hero", { selector: "span" })).toHaveClass("text-accent");
    await userEvent.click(files);
    expect(onGo).toHaveBeenCalledWith("/tools", { tab: "files", path: "Art/hero.png" });
  });

  it("skips clones that are unavailable", async () => {
    open({ repos: [game, repo("/gone", { error: "moved" })] });
    await userEvent.type(input(), "hero");
    await screen.findByRole("option", { name: /docs\/hero\.md/ });
    expect(api.searchFiles).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("game")).toBeNull();
  });

  it("finds branches, tags, merge requests and commits", async () => {
    open();
    await userEvent.type(input(), "hero");
    const branch = await screen.findByRole("option", { name: /hero-rework/ });
    expect(branch).toHaveTextContent("Switch");
    const tag = screen.getByRole("option", { name: /hero-v1/ });
    expect(tag).toHaveTextContent("Check out");
    const request = await optionWith("#4 Hero armor textures");
    expect(request).toHaveTextContent("feature → main · open");
    expect(await optionWith("#9 Hero draft")).toHaveTextContent("· draft");
    const found = await screen.findByRole("option", { name: /Rework hero armor/ });
    expect(found).toHaveTextContent("c1 · Ann");
    expect(screen.queryByRole("option", { name: /Unrelated/ })).toBeNull();
    expect(api.mergeRequests).toHaveBeenCalledWith("/game", { status: "", search: "hero" });

    await userEvent.click(branch);
    expect(onGo).toHaveBeenLastCalledWith("/game", { tab: "branches", action: "switch", target: "hero-rework" });
    await userEvent.click(tag);
    expect(onGo).toHaveBeenLastCalledWith("/game", { tab: "branches", action: "checkout-tag", target: "hero-v1" });
    await userEvent.click(request);
    expect(onGo).toHaveBeenLastCalledWith("/game", { tab: "mrs", number: 4 });
    await userEvent.click(found);
    expect(onGo).toHaveBeenLastCalledWith("/game", { tab: "history", commit: "c1" });
  });

  it("opens the current branch's tab instead of switching to it", async () => {
    open();
    await userEvent.type(input(), "main");
    const here = await screen.findByRole("option", { name: /main.*Current branch/ });
    await userEvent.click(here);
    expect(onGo).toHaveBeenCalledWith("/game", { tab: "branches" });
  });

  it("opens a merge request by number", async () => {
    open();
    await userEvent.type(input(), "#12");
    await userEvent.click(await screen.findByRole("option", { name: "Open merge request #12" }));
    expect(onGo).toHaveBeenCalledWith("/game", { tab: "mrs", number: 12 });
    expect(api.mergeRequests).not.toHaveBeenCalled();
  });

  it("looks a commit up by its exact id", async () => {
    open();
    await userEvent.type(input(), "zz99abcd");
    await userEvent.click(await screen.findByRole("option", { name: /Old hero work/ }));
    expect(api.history).toHaveBeenCalledWith("/game", { branch: "main", start: "zz99abcd", limit: 1 });
    expect(onGo).toHaveBeenCalledWith("/game", { tab: "history", commit: "zz99abcd" });
  });

  it("switches branch through its own mode and goes back", async () => {
    open();
    await userEvent.type(input(), "switch");
    await userEvent.keyboard("{Enter}");
    expect(input()).toHaveAttribute("placeholder", "Switch to branch…");
    expect(input()).toHaveValue("");
    expect(await screen.findByRole("option", { name: /hero-rework/ })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: /main/ })).toHaveTextContent("Current branch");
    await userEvent.type(input(), "rew");
    expect(options()).toHaveLength(1);
    await userEvent.keyboard("{Backspace}{Backspace}{Backspace}{Backspace}");
    expect(input()).toHaveAttribute("placeholder", expect.stringMatching(/^Search files/));
    await userEvent.type(input(), "switch");
    await userEvent.keyboard("{Enter}");
    await userEvent.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
    expect(input()).toHaveAttribute("placeholder", expect.stringMatching(/^Search files/));
  });

  it("starts in switch mode and picks a branch", async () => {
    open({ mode: "switch" });
    await userEvent.click(await screen.findByRole("option", { name: /hero-rework/ }));
    expect(onGo).toHaveBeenCalledWith("/game", { tab: "branches", action: "switch", target: "hero-rework" });
    await userEvent.click(screen.getByRole("button", { name: "Back to all commands" }));
    expect(screen.getByRole("option", { name: /^Update/ })).toBeInTheDocument();
  });

  it("locks a file picked in lock mode", async () => {
    open({ mode: "lock" });
    expect(screen.getByText("Type part of the file's name")).toBeInTheDocument();
    await userEvent.type(input(), "armor");
    const row = await screen.findByRole("option", { name: /Assets\/Hero\/armor\.png/ });
    expect(row).toHaveTextContent("Lock");
    expect(api.searchFiles).toHaveBeenCalledWith("/game", "armor", 30);
    expect(api.searchFiles).not.toHaveBeenCalledWith("/tools", expect.anything(), expect.anything());
    await userEvent.keyboard("{Enter}");
    expect(onGo).toHaveBeenCalledWith("/game", { tab: "locks", action: "lock", path: "Assets/Hero/armor.png" });
  });

  it("enters lock mode from the action", async () => {
    open();
    await userEvent.click(screen.getByRole("option", { name: /Lock file…/ }));
    expect(input()).toHaveAttribute("placeholder", "Find the file to lock…");
  });

  it("runs the remaining actions", async () => {
    open();
    for (const [name, intent] of [
      [/^Push…/, { tab: "changes", action: "focus-message" }],
      [/^New merge request/, { tab: "mrs", action: "new-mr" }],
      [/^Refresh/, { tab: "", action: "refresh" }],
      [/^Go to History/, { tab: "history" }],
    ] as const) {
      await userEvent.click(screen.getByRole("option", { name }));
      expect(onGo).toHaveBeenLastCalledWith("/game", intent);
    }
    await userEvent.click(screen.getByRole("option", { name: /^Settings/ }));
    expect(onSettings).toHaveBeenCalled();
    await userEvent.click(screen.getByRole("option", { name: /^Keyboard shortcuts/ }));
    expect(onShortcuts).toHaveBeenCalled();
  });

  it("filters actions by the query", async () => {
    open();
    await userEvent.type(input(), "settngs");
    expect(await screen.findByRole("option", { name: /^Settings/ })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /^Update/ })).toBeNull();
  });

  it("offers only files and app actions without a usable clone", async () => {
    open({ current: null, repos: [tools] });
    expect(input()).toHaveAttribute("placeholder", "Search files and actions");
    expect(screen.queryByRole("option", { name: /^Update/ })).toBeNull();
    expect(screen.getByRole("option", { name: /^Settings/ })).toBeInTheDocument();
    await userEvent.type(input(), "zzzz");
    await waitFor(() => expect(api.searchFiles).toHaveBeenCalled());
    api.searchFiles.mockResolvedValue({ hits: [], truncated: false });
    expect(await screen.findByText("No results")).toBeInTheDocument();
    expect(api.branches).not.toHaveBeenCalled();
  });

  it("closes on Escape, Ctrl+K and a click outside", async () => {
    const { unmount } = open();
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
    await userEvent.keyboard("{Control>}k{/Control}");
    expect(onClose).toHaveBeenCalledTimes(2);
    await userEvent.click(screen.getByRole("dialog").parentElement!);
    expect(onClose).toHaveBeenCalledTimes(3);
    await userEvent.click(screen.getByRole("listbox"));
    expect(onClose).toHaveBeenCalledTimes(3);
    unmount();
  });

  it("keeps app shortcuts from seeing its keys", async () => {
    const outer = vi.fn();
    document.addEventListener("keydown", outer);
    open();
    await userEvent.keyboard("j");
    expect(outer).not.toHaveBeenCalled();
    document.removeEventListener("keydown", outer);
  });
});
