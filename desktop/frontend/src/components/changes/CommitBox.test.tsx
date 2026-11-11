import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PushChecks } from "@/api/desktop";
import { CommitBox } from "./CommitBox";

const none: PushChecks = { violations: [], broken: [], newlyUnused: [], warnings: [], truncated: false, scanned: 0 };

const box = (checks?: PushChecks, over: { stagedCount?: number; onSelectPath?: (paths: string[]) => void; onPush?: (m: string) => void } = {}) =>
  render(
    <CommitBox
      stagedCount={over.stagedCount ?? 2}
      checks={checks}
      onSelectPath={over.onSelectPath}
      op={null}
      message="Update hero"
      onMessage={() => {}}
      onPush={over.onPush ?? (() => {})}
      onCancel={() => {}}
    />,
  );

describe("CommitBox checks", () => {
  it("hides the block when there is nothing to report", () => {
    box(none);
    expect(screen.queryByRole("region", { name: "Checks" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Push 2 files/ })).toBeEnabled();
    box(undefined);
    expect(screen.queryByRole("region", { name: "Checks" })).not.toBeInTheDocument();
  });

  it("lists budget violations and keeps push enabled as Push anyway", async () => {
    const onSelectPath = vi.fn();
    const onPush = vi.fn();
    box(
      { ...none, violations: [{ path: "Assets/hero.png", rule: "characters", limit: "max_texture_size", actual: "4096", max: "2048", message: "4096x4096 exceeds 2048x2048" }] },
      { onSelectPath, onPush },
    );
    const region = screen.getByRole("region", { name: "Checks" });
    expect(within(region).getByText("Checks: 1 warning")).toBeInTheDocument();
    const group = within(region).getByRole("list", { name: "Over budget" });
    expect(within(group).getByText("Assets/hero.png")).toBeInTheDocument();
    expect(within(group).getByText("max_texture_size")).toBeInTheDocument();
    expect(group).toHaveTextContent("(rule characters): 4096x4096 exceeds 2048x2048");
    expect(within(region).queryByRole("list", { name: "Broken references / No longer used" })).not.toBeInTheDocument();
    await userEvent.click(within(group).getByTitle("Assets/hero.png"));
    expect(onSelectPath).toHaveBeenCalledWith(["Assets/hero.png"]);

    const push = screen.getByRole("button", { name: /Push anyway/ });
    expect(push).toBeEnabled();
    await userEvent.click(push);
    expect(onPush).toHaveBeenCalledWith("Update hero");
  });

  it("lists broken references and unused assets", async () => {
    const onSelectPath = vi.fn();
    box(
      {
        ...none,
        broken: [
          { from: "Assets/Forest.unity", raw: "guid:abc", target: "Assets/old.mat", reason: "removed" },
          { from: "scenes/main.tscn", raw: "res://gone.png", target: "", reason: "unresolved" },
        ],
        newlyUnused: ["Assets/old.png"],
      },
      { onSelectPath },
    );
    const region = screen.getByRole("region", { name: "Checks" });
    expect(within(region).getByText("Checks: 3 warnings")).toBeInTheDocument();
    expect(within(region).queryByRole("list", { name: "Over budget" })).not.toBeInTheDocument();
    const group = within(region).getByRole("list", { name: "Broken references / No longer used" });
    expect(group).toHaveTextContent("removed Assets/old.mat");
    expect(group).toHaveTextContent("unresolved res://gone.png");
    expect(within(group).getByText("No longer used by any scene or asset")).toBeInTheDocument();
    await userEvent.click(within(group).getByTitle("Assets/Forest.unity"));
    expect(onSelectPath).toHaveBeenLastCalledWith(["Assets/Forest.unity", "Assets/old.mat"]);
    await userEvent.click(within(group).getByTitle("scenes/main.tscn"));
    expect(onSelectPath).toHaveBeenLastCalledWith(["scenes/main.tscn"]);
    await userEvent.click(within(group).getByTitle("Assets/old.png"));
    expect(onSelectPath).toHaveBeenLastCalledWith(["Assets/old.png"]);
    expect(screen.getByRole("button", { name: /Push anyway/ })).toBeEnabled();
  });

  it("notes a truncated scan and check warnings, and collapses", async () => {
    box({ ...none, newlyUnused: ["a.png"], truncated: true, scanned: 5000, warnings: ["budget check skipped: bad yaml"] });
    const region = screen.getByRole("region", { name: "Checks" });
    expect(within(region).getByText("Reference scan stopped after 5000 files; the list may be incomplete.")).toBeInTheDocument();
    expect(within(region).getByText("budget check skipped: bad yaml")).toBeInTheDocument();
    const toggle = within(region).getByRole("button", { name: /Checks: 1 warning/ });
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(within(region).queryByText("a.png")).not.toBeInTheDocument();
    await userEvent.click(within(region).getAllByRole("button")[0]);
    expect(within(region).getByText("a.png")).toBeInTheDocument();
  });

  it("shows warnings alone without changing the push label", () => {
    box({ ...none, warnings: ["impact check skipped: boom"] });
    const region = screen.getByRole("region", { name: "Checks" });
    expect(within(region).getByRole("button", { name: /^Checks$/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Push 2 files/ })).toBeInTheDocument();
  });

  it("hides checks with nothing staged", () => {
    box({ ...none, newlyUnused: ["a.png"] }, { stagedCount: 0 });
    expect(screen.queryByRole("region", { name: "Checks" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Push" })).toBeDisabled();
  });

  it("ignores row clicks without a handler", async () => {
    box({ ...none, newlyUnused: ["a.png"] });
    await userEvent.click(screen.getByTitle("a.png"));
  });
});
