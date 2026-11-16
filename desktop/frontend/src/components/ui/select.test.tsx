import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Select } from "./select";

const options = [
  { id: "merge", label: "Merge commit", hint: "Keeps every commit" },
  { id: "squash", label: "Squash", hint: "One commit" },
  { id: "ff", label: "Fast-forward" },
] as const;

const setup = (value: (typeof options)[number]["id"] = "merge", disabled = false) => {
  const onChange = vi.fn();
  render(
    <div>
      <Select value={value} options={options} onChange={onChange} label="Strategy" disabled={disabled} />
      <p>outside</p>
    </div>,
  );
  return { onChange, trigger: screen.getByRole("combobox", { name: "Strategy" }) };
};

describe("Select", () => {
  it("shows the current option and picks another with the mouse", async () => {
    const { onChange, trigger } = setup();
    expect(trigger).toHaveTextContent("Merge commit");
    expect(trigger).toHaveAttribute("title", "Keeps every commit");
    await userEvent.click(trigger);
    expect(screen.getByRole("option", { name: /Merge commit/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("One commit")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("option", { name: /Squash/ }));
    expect(onChange).toHaveBeenCalledWith("squash");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("navigates with the keyboard and wraps around", async () => {
    const { onChange, trigger } = setup("ff");
    trigger.focus();
    await userEvent.keyboard("{ArrowDown}");
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(trigger.getAttribute("aria-activedescendant")).toMatch(/-2$/);
    await userEvent.keyboard("{ArrowDown}");
    expect(trigger.getAttribute("aria-activedescendant")).toMatch(/-0$/);
    await userEvent.keyboard("{ArrowUp}{ArrowUp}{Enter}");
    expect(onChange).toHaveBeenCalledWith("squash");
  });

  it("closes on escape, a second click and an outside click", async () => {
    const { onChange, trigger } = setup();
    await userEvent.click(trigger);
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    await userEvent.click(trigger);
    await userEvent.click(trigger);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    await userEvent.click(trigger);
    await userEvent.hover(screen.getByRole("option", { name: /Fast-forward/ }));
    expect(trigger.getAttribute("aria-activedescendant")).toMatch(/-2$/);
    await userEvent.click(screen.getByText("outside"));
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("closes when focus leaves with Tab", async () => {
    const { trigger } = setup();
    await userEvent.click(trigger);
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    await userEvent.tab();
    expect(trigger).not.toHaveFocus();
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("does nothing while disabled", async () => {
    const { trigger } = setup("merge", true);
    expect(trigger).toBeDisabled();
    await userEvent.click(trigger);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });
});
