import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ShortcutsSheet } from "./ShortcutsSheet";

describe("ShortcutsSheet", () => {
  it("lists every group with its keys", () => {
    render(<ShortcutsSheet onClose={() => {}} />);
    const general = screen.getByRole("region", { name: "General" });
    expect(within(general).getByText("Search and run commands").nextSibling).toHaveTextContent("CtrlK");
    const changes = screen.getByRole("region", { name: "Changes" });
    expect(within(changes).getByText("Next file").nextSibling).toHaveTextContent("Jor↓");
    expect(screen.getByRole("region", { name: "Repository" })).toHaveTextContent("Ctrl1–9");
    expect(screen.getByRole("button", { name: "Close" })).toHaveFocus();
  });

  it("closes with ?, Escape, the button and a click outside", async () => {
    const onClose = vi.fn();
    render(<ShortcutsSheet onClose={onClose} />);
    await userEvent.keyboard("?");
    await userEvent.keyboard("{Escape}");
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    await userEvent.click(screen.getByRole("dialog").parentElement!);
    await userEvent.click(screen.getByRole("dialog"));
    await userEvent.keyboard("a");
    expect(onClose).toHaveBeenCalledTimes(4);
  });
});
