import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  branches: vi.fn(),
  tags: vi.fn(),
  createBranch: vi.fn(),
  deleteBranch: vi.fn(),
  createTag: vi.fn(),
  deleteTag: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { BranchesView } from "./BranchesView";

const notify = vi.fn();
const onSwitch = vi.fn();
const onCheckoutTag = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.branches.mockResolvedValue([
    { name: "main", commitId: "c3", default: true, protected: true },
    { name: "art-pass", commitId: "c4", default: false, protected: false },
    { name: "empty", commitId: "", default: false, protected: false },
  ]);
  api.tags.mockResolvedValue([
    { name: "v1.0", commitId: "c2", message: "First playable", createdAt: "2026-11-01T12:00:00Z", createdBy: "u1" },
    { name: "v0.9", commitId: "c1", message: "", createdAt: "", createdBy: "u1" },
  ]);
});

const view = (props: Partial<Parameters<typeof BranchesView>[0]> = {}) =>
  renderWithProviders(<BranchesView root="/work/game" branch="main" busy={false} notify={notify} onSwitch={onSwitch} onCheckoutTag={onCheckoutTag} {...props} />);

const section = (name: string) => screen.getByRole("heading", { name }).closest("section") as HTMLElement;

describe("BranchesView", () => {
  it("lists branches and tags with their state", async () => {
    view();
    expect(await screen.findByText("art-pass")).toBeInTheDocument();
    const branches = section("Branches");
    expect(within(branches).getByText("Current")).toBeInTheDocument();
    expect(within(branches).getByText("Default")).toBeInTheDocument();
    expect(within(branches).getByText("Protected")).toBeInTheDocument();
    expect(within(branches).getByText("empty", { selector: "span.font-mono" })).toBeInTheDocument();
    expect(within(branches).queryByRole("button", { name: "Delete branch main" })).not.toBeInTheDocument();
    expect(within(branches).getAllByRole("button", { name: "Switch" })).toHaveLength(2);

    expect(await screen.findByText("First playable")).toBeInTheDocument();
    await userEvent.click(within(section("Tags")).getAllByRole("button", { name: "Check out" })[0]);
    expect(onCheckoutTag).toHaveBeenCalledWith("v1.0");
    await userEvent.click(within(branches).getAllByRole("button", { name: "Switch" })[0]);
    expect(onSwitch).toHaveBeenCalledWith("art-pass");
  });

  it("creates branches and tags from the current branch", async () => {
    api.createBranch.mockResolvedValueOnce({ name: "fx" }).mockRejectedValueOnce(new Error("branch exists"));
    api.createTag.mockResolvedValueOnce({ name: "v1.1" }).mockRejectedValueOnce(new Error("tag exists"));
    view();
    await screen.findByText("art-pass");

    await userEvent.click(screen.getByRole("button", { name: "New branch" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Branch name" }), "fx{Enter}");
    expect(api.createBranch).toHaveBeenCalledWith("/work/game", "fx", { branch: "main" });
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Created branch fx from main", "success"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "New branch" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Branch name" }), "fx{Enter}");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not create the branch: branch exists"));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await userEvent.click(screen.getByRole("button", { name: "New tag" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Tag name" }), "v1.1");
    await userEvent.type(screen.getByRole("textbox", { name: "Message (optional)" }), "Beta");
    await userEvent.click(screen.getByRole("button", { name: "Create tag" }));
    expect(api.createTag).toHaveBeenCalledWith("/work/game", "v1.1", { branch: "main" }, "Beta");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Tagged the head of main as v1.1", "success"));
    await userEvent.click(screen.getByRole("button", { name: "New tag" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Tag name" }), "v1.1{Enter}");
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not create the tag: tag exists"));
  });

  it("asks before deleting branches and tags", async () => {
    api.deleteBranch.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("protected"));
    api.deleteTag.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("denied"));
    view();
    await userEvent.click(await screen.findByRole("button", { name: "Delete branch art-pass" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Delete branch art-pass?");
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Deleted branch art-pass", "success"));
    await userEvent.click(screen.getByRole("button", { name: "Delete branch empty" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not delete the branch: protected"));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await userEvent.click(screen.getByRole("button", { name: "Delete tag v0.9" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Deleted tag v0.9", "success"));
    await userEvent.click(screen.getByRole("button", { name: "Delete tag v1.0" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not delete the tag: denied"));
    expect(api.deleteBranch).toHaveBeenCalledWith("/work/game", "art-pass");
    expect(api.deleteTag).toHaveBeenCalledWith("/work/game", "v0.9");
  });

  it("marks the checked-out tag and blocks creating while detached", async () => {
    view({ detached: "v1.0" });
    expect(await screen.findByText("Checked out")).toBeInTheDocument();
    expect(within(section("Tags")).getAllByRole("button", { name: "Check out" })).toHaveLength(1);
    expect(screen.getByRole("button", { name: "New branch" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "New tag" })).toBeDisabled();
    expect(within(section("Branches")).queryByText("Current")).not.toBeInTheDocument();
  });

  it("keeps the configured branch undeletable while detached", async () => {
    view({ detached: "v1.0", branch: "art-pass" });
    await screen.findByText("Checked out");
    const branches = section("Branches");
    expect(within(branches).queryByRole("button", { name: "Delete branch art-pass" })).not.toBeInTheDocument();
    expect(within(branches).getByRole("button", { name: "Delete branch empty" })).toBeInTheDocument();
  });

  it("shows empty lists, errors and refreshes", async () => {
    api.branches.mockRejectedValueOnce(new Error("offline")).mockResolvedValue([]);
    api.tags.mockResolvedValue([]);
    view();
    expect(await screen.findByText("offline")).toBeInTheDocument();
    expect(await screen.findByText(/No tags yet/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Refresh branches" }));
    expect(await screen.findByText("No branches")).toBeInTheDocument();
  });
});
