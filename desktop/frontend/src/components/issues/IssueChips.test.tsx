import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ resolveIssues: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { IssueChips } from "./IssueChips";

const issues = [
  { key: "GAME-1", url: "https://jira.test/browse/GAME-1" },
  { key: "ART-2", url: "https://jira.test/browse/ART-2" },
];
const resolved = (key: string, state = "ok") => ({ key, url: "", state, title: `Title of ${key}`, type: "", status: "In Progress", statusCategory: "", assignee: "" });

beforeEach(() => vi.resetAllMocks());

describe("IssueChips", () => {
  it("renders nothing without issues", () => {
    const { container } = renderWithProviders(<IssueChips issues={null} root="/r" />);
    expect(container).toBeEmptyDOMElement();
    renderWithProviders(<IssueChips issues={[]} />);
    expect(screen.queryByLabelText("Linked issues")).toBeNull();
    expect(api.resolveIssues).not.toHaveBeenCalled();
  });

  it("shows plain key chips in rows without asking the tracker", () => {
    renderWithProviders(<IssueChips issues={issues} />);
    const chips = screen.getByLabelText("Linked issues");
    expect(chips).toHaveTextContent("GAME-1");
    expect(chips).toHaveTextContent("ART-2");
    expect(screen.queryAllByRole("link")).toHaveLength(0);
    expect(api.resolveIssues).not.toHaveBeenCalled();
  });

  it("links each issue and shows the title and status the tracker knows", async () => {
    api.resolveIssues.mockResolvedValue([resolved("GAME-1"), resolved("ART-2", "unavailable")]);
    renderWithProviders(<IssueChips issues={issues} root="/r" />);
    expect(await screen.findByText("Title of GAME-1")).toBeInTheDocument();
    expect(screen.getByText("In Progress")).toBeInTheDocument();
    expect(screen.queryByText("Title of ART-2")).toBeNull();
    expect(api.resolveIssues).toHaveBeenCalledTimes(1);
    expect(api.resolveIssues).toHaveBeenCalledWith("/r", ["GAME-1", "ART-2"]);
    const link = screen.getByRole("link", { name: "GAME-1" });
    expect(link).toHaveAttribute("href", "https://jira.test/browse/GAME-1");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("title", "Open GAME-1 in the issue tracker");
  });

  it("falls back to key-only chips when the tracker cannot answer", async () => {
    api.resolveIssues.mockRejectedValue(new Error("down"));
    renderWithProviders(<IssueChips issues={issues} root="/r" />);
    await vi.waitFor(() => expect(api.resolveIssues).toHaveBeenCalled());
    expect(screen.getByRole("link", { name: "GAME-1" })).toBeInTheDocument();
    expect(screen.queryByText(/Title of/)).toBeNull();
  });
});
