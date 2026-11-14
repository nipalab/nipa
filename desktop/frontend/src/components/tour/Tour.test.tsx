import { beforeEach, describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ModePrompt } from "@/components/tour/ModePrompt";
import { Tour } from "@/components/tour/Tour";
import { readTour, setMode, startTour, storedMode } from "@/lib/mode";
import { resetSettingsForTest } from "@/lib/settings";

beforeEach(() => {
  resetSettingsForTest();
  localStorage.clear();
});

describe("Tour", () => {
  it("renders nothing until started", () => {
    render(<Tour />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("walks the four steps with simple wording and finishes", async () => {
    setMode("simple");
    startTour();
    render(<Tour />);
    expect(screen.getByText("Step 1 of 4")).toBeInTheDocument();
    expect(screen.getAllByText("Get latest").length).toBeGreaterThan(0);
    for (let i = 0; i < 3; i++) await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Step 4 of 4")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Submit" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(readTour()).toBe("done");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("can be skipped", async () => {
    startTour();
    render(<Tour />);
    await userEvent.click(screen.getByRole("button", { name: "Skip tour" }));
    expect(readTour()).toBe("done");
  });
});

describe("ModePrompt", () => {
  it("stores the chosen mode", async () => {
    render(<ModePrompt />);
    await userEvent.click(screen.getByRole("button", { name: /art, audio or design/ }));
    expect(storedMode()).toBe("simple");
  });

  it("escape keeps full mode", async () => {
    render(<ModePrompt />);
    await userEvent.tab();
    await userEvent.keyboard("{Escape}");
    expect(storedMode()).toBe("full");
  });
});
