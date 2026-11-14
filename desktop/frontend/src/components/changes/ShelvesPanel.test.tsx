import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChangeSet } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";
import { ShelveDialog, shelvable } from "./ShelveDialog";
import { ShelvesPanel, type ShelfPick } from "./ShelvesPanel";

const api = vi.hoisted(() => ({ shelves: vi.fn(), dropShelf: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

const shelf = (id: string, over: object = {}) => ({
  id,
  name: `Shelf ${id}`,
  createdAt: new Date().toISOString(),
  branch: "main",
  size: 0,
  entries: [
    { path: "art/hero.png", status: "modified", staged: true, untracked: false, locked: true, size: 1, binary: true, kind: "image" },
    { path: "new.txt", status: "added", staged: false, untracked: true, locked: false, size: 1, binary: false, kind: "text" },
  ],
  ...over,
});

const onPick = vi.fn();
const onApply = vi.fn();
const onError = vi.fn();
const panel = (picked: ShelfPick | null = null, busy = false) =>
  renderWithProviders(<ShelvesPanel root="/work/game" busy={busy} picked={picked} onPick={onPick} onApply={onApply} onError={onError} />);

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
});

describe("ShelvesPanel", () => {
  it("renders nothing without shelves", async () => {
    api.shelves.mockResolvedValue({ shelves: [], applying: "" });
    const { container } = panel();
    await waitFor(() => expect(api.shelves).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("lists, expands, picks, collapses and deletes", async () => {
    api.shelves.mockResolvedValue({ shelves: [shelf("a", { tag: "v1", size: 2048 }), shelf("b")], applying: "" });
    api.dropShelf.mockResolvedValue(undefined);
    panel({ id: "a", path: "new.txt" });
    const region = await screen.findByRole("region", { name: "Shelves" });
    expect(within(region).getByText(/tag v1/)).toHaveTextContent("2 files · tag v1");
    expect(within(region).getByText(/2.0 KB/)).toBeInTheDocument();
    await userEvent.click(within(region).getByRole("button", { name: /Shelf a/ }));
    const files = within(region).getByRole("list", { name: "Files in Shelf a" });
    expect(within(files).getByRole("button", { name: /new.txt/ })).toHaveAttribute("aria-current", "true");
    expect(within(files).getByLabelText("You held a lock on it when shelving")).toBeInTheDocument();
    expect(within(files).getByText("staged")).toBeInTheDocument();
    await userEvent.click(within(files).getByRole("button", { name: /hero.png/ }));
    expect(onPick).toHaveBeenCalledWith({ id: "a", path: "art/hero.png" });

    await userEvent.click(within(region).getByRole("button", { name: "Delete Shelf a" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(api.dropShelf).toHaveBeenCalledWith("/work/game", "a"));

    await userEvent.click(within(region).getByRole("button", { name: /Shelves/ }));
    expect(within(region).queryByText("Shelf b")).not.toBeInTheDocument();
    expect(localStorage.getItem("nipa.shelves.open")).toBe("closed");
  });

  it("cancels a delete and reports a failed one", async () => {
    api.shelves.mockResolvedValue({ shelves: [shelf("a")], applying: "" });
    api.dropShelf.mockRejectedValue(new Error("busy"));
    panel();
    const region = await screen.findByRole("region", { name: "Shelves" });
    await userEvent.click(within(region).getByRole("button", { name: /Shelf a/ }));
    await userEvent.click(within(region).getByRole("button", { name: "Delete Shelf a" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(api.dropShelf).not.toHaveBeenCalled();
    await userEvent.click(within(region).getByRole("button", { name: "Delete Shelf a" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(onError).toHaveBeenCalledWith("Could not delete the shelf: busy"));
  });

  it("marks the shelf being applied and blocks its actions", async () => {
    api.shelves.mockResolvedValue({ shelves: [shelf("a")], applying: "a" });
    panel();
    const region = await screen.findByRole("region", { name: "Shelves" });
    expect(within(region).getByText("Applying")).toBeInTheDocument();
    await userEvent.click(within(region).getByRole("button", { name: /Shelf a/ }));
    expect(within(region).getByRole("button", { name: "Apply" })).toBeDisabled();
    expect(within(region).getByRole("button", { name: "Apply and keep" })).toBeDisabled();
  });

  it("clears a pick whose shelf is gone", async () => {
    api.shelves.mockResolvedValue({ shelves: [shelf("a")], applying: "" });
    panel({ id: "gone", path: "x" });
    await waitFor(() => expect(onPick).toHaveBeenCalledWith(null));
  });

  it("applies with or without keeping the shelf", async () => {
    api.shelves.mockResolvedValue({ shelves: [shelf("a")], applying: "" });
    panel();
    const region = await screen.findByRole("region", { name: "Shelves" });
    await userEvent.click(within(region).getByRole("button", { name: /Shelf a/ }));
    await userEvent.click(within(region).getByRole("button", { name: "Apply and keep" }));
    expect(onApply).toHaveBeenCalledWith(expect.objectContaining({ id: "a" }), true);
  });
});

const changes: ChangeSet = {
  branch: "main",
  staged: [{ path: "a.txt", status: "modified", kind: "text" }],
  unstaged: [
    { path: "a.txt", status: "modified", kind: "text" },
    { path: "new.png", status: "untracked", kind: "image" },
  ],
  conflicts: [],
  resolved: [],
};

describe("ShelveDialog", () => {
  it("dedups files", () => {
    expect(shelvable(changes).map((f) => f.path)).toEqual(["a.txt", "new.png"]);
  });

  it("selects all by default, toggles and confirms", async () => {
    const onConfirm = vi.fn();
    renderWithProviders(<ShelveDialog changes={changes} onConfirm={onConfirm} onCancel={() => {}} />);
    expect(screen.getByText("2 of 2 selected")).toBeInTheDocument();
    expect(screen.getByRole("textbox")).toHaveAttribute("placeholder", "2 files on main");
    await userEvent.click(screen.getByRole("button", { name: "Select none" }));
    expect(screen.getByRole("button", { name: "Shelve 0 files" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Select all" }));
    await userEvent.click(screen.getByRole("checkbox", { name: /a.txt/ }));
    await userEvent.click(screen.getByRole("button", { name: "Shelve 1 file" }));
    expect(onConfirm).toHaveBeenCalledWith({ name: "", paths: ["new.png"] });
  });

  it("starts from the given files and closes on Escape", async () => {
    const onCancel = vi.fn();
    renderWithProviders(<ShelveDialog changes={changes} initial={["a.txt"]} onConfirm={() => {}} onCancel={onCancel} />);
    expect(screen.getByText("1 of 2 selected")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(onCancel).toHaveBeenCalled();
  });
});
