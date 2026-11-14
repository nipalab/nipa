import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Feed, FeedEvent } from "@/api/desktop";
import { readSetting, repoSetting } from "@/lib/settings";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ projectActivity: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { ActivityView } from "./ActivityView";

const at = (daysAgo: number, hour: number) => new Date(new Date().getFullYear(), new Date().getMonth(), new Date().getDate() - daysAgo, hour).toISOString();
const event = (over: Partial<FeedEvent>): FeedEvent => ({ id: "commit:c1", kind: "commit", time: at(0, 9), actor: "Ana Torres", title: "Fix the door", branch: "main", commitId: "c1abcdef0123", number: 0, path: "", ...over });

const feed = (over: Partial<Feed> = {}): Feed => ({
  events: [
    event({}),
    event({ id: "mr:5:mr_opened", kind: "mr_opened", actor: "Bo Lind", title: "Walls", branch: "feature", commitId: "", number: 5, time: at(0, 8) }),
    event({ id: "lock:l1", kind: "lock", actor: "Ana Torres", title: "art/door.psd", branch: "", commitId: "", path: "art/door.psd", time: at(1, 17) }),
    event({ id: "mr:4:mr_merged", kind: "mr_merged", actor: "Bo Lind", title: "Doors", branch: "doors", commitId: "", number: 4, time: at(3, 12) }),
  ],
  truncated: false,
  partial: false,
  ...over,
});

const handlers = { onOpenCommit: vi.fn(), onOpenMergeRequest: vi.fn(), onOpenLocks: vi.fn() };
const view = () => renderWithProviders(<ActivityView root="/work/game" {...handlers} />);

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  api.projectActivity.mockResolvedValue(feed());
});

describe("ActivityView", () => {
  it("groups the timeline by day and asks for the chosen range", async () => {
    view();

    expect(screen.getByText("Loading activity…")).toBeInTheDocument();
    const today = await screen.findByRole("region", { name: "Today" });
    expect(within(today).getByText("Fix the door")).toBeInTheDocument();
    expect(within(today).getByText("Walls")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Yesterday" })).getByText("art/door.psd")).toBeInTheDocument();
    expect(screen.getAllByRole("region")).toHaveLength(3);
    expect(api.projectActivity).toHaveBeenCalledWith("/work/game", expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/));

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Time range" }), "today");
    await waitFor(() => expect(api.projectActivity).toHaveBeenCalledTimes(2));
    expect(JSON.parse(readSetting(repoSetting("/work/game", "activity.range")) ?? "null")).toBe("today");
  });

  it("filters by person and by kind and explains an empty result", async () => {
    view();
    await screen.findByRole("region", { name: "Today" });

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Person" }), "Bo Lind");
    expect(screen.queryByText("Fix the door")).not.toBeInTheDocument();
    expect(screen.getByText("Walls")).toBeInTheDocument();
    expect(screen.getByText("Doors")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Merge requests" }));
    expect(screen.getByText("Nothing matches these filters")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Merge requests" }));
    expect(screen.getByText("Walls")).toBeInTheDocument();
  });

  it("opens the commit, merge request or locks behind a row", async () => {
    view();
    await screen.findByRole("region", { name: "Today" });

    await userEvent.click(screen.getByRole("button", { name: /Fix the door/ }));
    expect(handlers.onOpenCommit).toHaveBeenCalledWith("c1abcdef0123");
    await userEvent.click(screen.getByRole("button", { name: /Walls/ }));
    expect(handlers.onOpenMergeRequest).toHaveBeenCalledWith(5);
    await userEvent.click(screen.getByRole("button", { name: /Doors/ }));
    expect(handlers.onOpenMergeRequest).toHaveBeenCalledWith(4);
    await userEvent.click(screen.getByRole("button", { name: /art\/door.psd/ }));
    expect(handlers.onOpenLocks).toHaveBeenCalledTimes(1);
  });

  it("says when nothing happened and when the feed is partial or truncated", async () => {
    api.projectActivity.mockResolvedValueOnce(feed({ events: [] }));
    const first = view();
    expect(await screen.findByText("Nothing has happened in this period")).toBeInTheDocument();
    first.unmount();

    api.projectActivity.mockResolvedValueOnce(feed({ partial: true, truncated: true }));
    view();
    const note = await screen.findByRole("status");
    expect(note).toHaveTextContent("Some activity could not be loaded.");
    expect(note).toHaveTextContent("choose a shorter range");
  });

  it("reports a failure and retries", async () => {
    api.projectActivity.mockRejectedValueOnce(new Error("server offline"));
    view();

    expect(await screen.findByText("server offline")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("region", { name: "Today" })).toBeInTheDocument();
  });

  it("refreshes on demand", async () => {
    view();
    await screen.findByRole("region", { name: "Today" });

    await userEvent.click(screen.getByRole("button", { name: "Refresh activity" }));
    await waitFor(() => expect(api.projectActivity).toHaveBeenCalledTimes(2));
  });
});
