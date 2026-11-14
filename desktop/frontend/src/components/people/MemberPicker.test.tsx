import { useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Person } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ members: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { MemberPicker } from "./MemberPicker";

const ana = { id: "u1", name: "Ana Torres" };
const bo = { id: "u2", name: "Bo Lind" };
const cy = { id: "u3", name: "Cy" };

const onChange = vi.fn();

function Harness({ initial = [], exclude = [] }: { initial?: Person[]; exclude?: string[] }) {
  const [selected, setSelected] = useState<Person[]>(initial);
  return (
    <MemberPicker
      root="/r"
      label="Reviewers"
      selected={selected}
      exclude={exclude}
      onChange={(next) => {
        onChange(next);
        setSelected(next);
      }}
    />
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.members.mockImplementation(async (_root: string, q: string) => [ana, bo, cy].filter((p) => p.name.toLowerCase().includes(q.toLowerCase())));
});

describe("MemberPicker", () => {
  it("lists people on focus and picks several with the mouse", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);

    await user.click(screen.getByRole("combobox", { name: "Reviewers" }));
    expect(await screen.findByRole("option", { name: /Ana Torres/ })).toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: /Ana Torres/ }));
    await user.click(screen.getByRole("option", { name: /Bo Lind/ }));

    expect(onChange).toHaveBeenLastCalledWith([ana, bo]);
    expect(screen.getByRole("option", { name: /Ana Torres/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("button", { name: "Remove Ana Torres" })).toBeInTheDocument();
  });

  it("searches by name after a pause and tells when nobody matches", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);

    await user.type(screen.getByRole("combobox", { name: "Reviewers" }), "lind");
    await waitFor(() => expect(api.members).toHaveBeenCalledWith("/r", "lind"));
    expect(await screen.findByRole("option", { name: /Bo Lind/ })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Ana/ })).not.toBeInTheDocument();

    await user.clear(screen.getByRole("combobox", { name: "Reviewers" }));
    await user.type(screen.getByRole("combobox", { name: "Reviewers" }), "zzz");
    expect(await screen.findByText("Nobody with that name can read this project")).toBeInTheDocument();
  });

  it("works from the keyboard: arrows move, Enter picks, Backspace removes, Escape closes", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    const input = screen.getByRole("combobox", { name: "Reviewers" });

    await user.click(input);
    await screen.findByRole("option", { name: /Ana Torres/ });
    await user.keyboard("{ArrowDown}{Enter}");
    expect(onChange).toHaveBeenLastCalledWith([bo]);
    await user.keyboard("{ArrowUp}{Enter}");
    expect(onChange).toHaveBeenLastCalledWith([bo, ana]);
    await user.keyboard("{Backspace}");
    expect(onChange).toHaveBeenLastCalledWith([bo]);

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    await user.keyboard("{ArrowDown}");
    expect(await screen.findByRole("listbox")).toBeInTheDocument();
  });

  it("unselects through the list and hides excluded people", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness initial={[ana]} exclude={["u2"]} />);

    await user.click(screen.getByRole("combobox", { name: "Reviewers" }));
    await screen.findByRole("option", { name: /Cy/ });
    expect(screen.queryByRole("option", { name: /Bo Lind/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: /Ana Torres/ }));

    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it("shows a search failure and an empty project", async () => {
    const user = userEvent.setup();
    api.members.mockRejectedValueOnce(new Error("server is down"));
    renderWithProviders(<Harness />);

    await user.click(screen.getByRole("combobox", { name: "Reviewers" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("server is down");
  });

  it("explains an empty project", async () => {
    const user = userEvent.setup();
    api.members.mockResolvedValue([]);
    renderWithProviders(<Harness />);

    await user.click(screen.getByRole("combobox", { name: "Reviewers" }));
    expect(await screen.findByText("No other people can read this project yet")).toBeInTheDocument();
  });

  it("closes when focus leaves and does nothing while disabled", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <>
        <Harness initial={[ana]} />
        <button>elsewhere</button>
      </>,
    );
    await user.click(screen.getByRole("combobox", { name: "Reviewers" }));
    await screen.findByRole("listbox");
    await user.click(screen.getByRole("button", { name: "elsewhere" }));
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });
});
