import { useState } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Draft, PushChecks } from "@/api/desktop";
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

function Harness({ onGenerate, onPush, stagedCount = 1 }: { onGenerate?: () => Promise<Draft>; onPush: (m: string) => void; stagedCount?: number }) {
  const [message, setMessage] = useState("");
  return <CommitBox stagedCount={stagedCount} op={null} message={message} onMessage={setMessage} onPush={onPush} onCancel={() => {}} onGenerate={onGenerate} />;
}

const draft = (text: string): Draft => ({ title: "", text, model: "qwen", source: "", target: "" });

describe("CommitBox generate", () => {
  it("is hidden while generation is not configured", () => {
    render(<Harness onPush={() => {}} />);
    expect(screen.queryByRole("button", { name: /Generate/ })).not.toBeInTheDocument();
  });

  it("is disabled with nothing staged", () => {
    render(<Harness onPush={() => {}} onGenerate={vi.fn()} stagedCount={0} />);
    expect(screen.getByRole("button", { name: /Generate/ })).toBeDisabled();
  });

  it("fills the message and adds the trailer to kept or edited drafts", async () => {
    const onPush = vi.fn();
    let resolve: (d: Draft) => void = () => {};
    const onGenerate = vi.fn(() => new Promise<Draft>((r) => (resolve = r)));
    render(<Harness onPush={onPush} onGenerate={onGenerate} />);
    await userEvent.click(screen.getByRole("button", { name: /Generate/ }));
    expect(screen.getByRole("button", { name: /Generating/ })).toBeDisabled();
    resolve(draft("feat: add hero"));
    const text = screen.getByLabelText("Push message");
    await waitFor(() => expect(text).toHaveValue("feat: add hero"));
    expect(screen.getByText(/Drafted by qwen/)).toBeInTheDocument();
    expect(onPush).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: /Push 1 file/ }));
    expect(onPush).toHaveBeenLastCalledWith("feat: add hero\n\nNipa-Generated: qwen");

    await userEvent.type(text, " art");
    await userEvent.click(screen.getByRole("button", { name: /Push 1 file/ }));
    expect(onPush).toHaveBeenLastCalledWith("feat: add hero art\n\nNipa-Generated: qwen");
  });

  it("drops the trailer once the user clears the draft and writes their own", async () => {
    const onPush = vi.fn();
    render(<Harness onPush={onPush} onGenerate={() => Promise.resolve(draft("feat: x"))} />);
    await userEvent.click(screen.getByRole("button", { name: /Generate/ }));
    const text = screen.getByLabelText("Push message");
    await waitFor(() => expect(text).toHaveValue("feat: x"));
    await userEvent.clear(text);
    await userEvent.type(text, "my own words");
    expect(screen.queryByText(/Drafted by/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Push 1 file/ }));
    expect(onPush).toHaveBeenLastCalledWith("my own words");
  });

  it("shows a readable error", async () => {
    render(<Harness onPush={() => {}} onGenerate={() => Promise.reject(new Error("AI generation is not configured"))} />);
    await userEvent.click(screen.getByRole("button", { name: /Generate/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not generate a message: AI generation is not configured");
    expect(screen.getByLabelText("Push message")).toHaveValue("");
  });
});
