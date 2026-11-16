import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ChangeSet, FileChange } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";
import { buildRows, FileList, stickyHeader } from "./FileList";

const file = (path: string): FileChange => ({ path, status: "modified", kind: "text" }) as FileChange;

const changes = (staged: number, unstaged: number): ChangeSet =>
  ({
    staged: Array.from({ length: staged }, (_, i) => file(`staged/${i}.txt`)),
    unstaged: Array.from({ length: unstaged }, (_, i) => file(`work/${i}.txt`)),
  }) as ChangeSet;

const list = (set: ChangeSet, over: Partial<Parameters<typeof FileList>[0]> = {}) => {
  const props = { onSelect: vi.fn(), onStage: vi.fn(), onUnstage: vi.fn(), onDiscard: vi.fn(), ...over };
  renderWithProviders(<FileList changes={set} selected={null} busy={false} {...props} />);
  return props;
};

describe("stickyHeader", () => {
  it("picks the last header at or before the first visible row", () => {
    expect(stickyHeader([0, 5], 0)).toBe(0);
    expect(stickyHeader([0, 5], 4)).toBe(0);
    expect(stickyHeader([0, 5], 5)).toBe(5);
    expect(stickyHeader([0, 5], 40)).toBe(5);
    expect(stickyHeader([], 3)).toBe(-1);
  });
});

describe("FileList", () => {
  it("builds a header per non-empty section", () => {
    const rows = buildRows(changes(2, 0));
    expect(rows.map((r) => r.type)).toEqual(["header", "file", "file"]);
  });

  it("pins the staged header while its files scroll", () => {
    list(changes(200, 3));
    const header = screen.getByRole("button", { name: "Unstage all" }).closest("[data-pinned]");
    expect(header).not.toBeNull();
    expect((header as HTMLElement).style.position).toBe("sticky");
  });

  it("keeps the pinned header after scrolling far down", () => {
    list(changes(200, 3));
    const box = screen.getByRole("listbox");
    box.scrollTop = 28 * 150;
    fireEvent.scroll(box);
    const header = screen.getByRole("button", { name: "Unstage all" }).closest("[data-pinned]");
    expect(header).not.toBeNull();
  });

  it("stages and unstages whole sections", async () => {
    const props = list(changes(1, 2));
    await userEvent.click(screen.getByRole("button", { name: "Stage all" }));
    expect(props.onStage).toHaveBeenCalledWith(["work/0.txt", "work/1.txt"]);
    await userEvent.click(screen.getByRole("button", { name: "Unstage all" }));
    expect(props.onUnstage).toHaveBeenCalledWith(["staged/0.txt"]);
  });
});
