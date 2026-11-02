import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThemeToggle } from "@/components/ThemeToggle";
import { ThemeProvider, resolveTheme, useTheme } from "./theme";

function mockMatchMedia(dark: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: dark,
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  };
  vi.stubGlobal("matchMedia", () => media);
  return {
    set(next: boolean) {
      media.matches = next;
      listeners.forEach((fn) => fn());
    },
    listeners,
  };
}

afterEach(() => vi.unstubAllGlobals());

describe("theme", () => {
  it("follows the system preference by default", () => {
    const media = mockMatchMedia(true);
    const { unmount } = render(<ThemeProvider><ThemeToggle /></ThemeProvider>);
    expect(document.documentElement).toHaveClass("dark");
    expect(screen.getByRole("radio", { name: "System" })).toHaveAttribute("aria-checked", "true");

    media.set(false);
    expect(document.documentElement).not.toHaveClass("dark");
    unmount();
    expect(media.listeners.size).toBe(0);
  });

  it("stores an explicit choice", async () => {
    mockMatchMedia(false);
    render(<ThemeProvider><ThemeToggle /></ThemeProvider>);
    await userEvent.click(screen.getByRole("radio", { name: "Dark" }));
    expect(document.documentElement).toHaveClass("dark");
    expect(localStorage.getItem("nipa.theme")).toBe("dark");

    await userEvent.click(screen.getByRole("radio", { name: "Light" }));
    expect(document.documentElement).not.toHaveClass("dark");
  });

  it("restores a stored choice and works without matchMedia", () => {
    localStorage.setItem("nipa.theme", "dark");
    vi.stubGlobal("matchMedia", undefined);
    render(<ThemeProvider><ThemeToggle /></ThemeProvider>);
    expect(document.documentElement).toHaveClass("dark");
    expect(resolveTheme("system")).toBe("light");
    expect(resolveTheme("light")).toBe("light");
  });

  it("requires the provider", () => {
    const Broken = () => {
      useTheme();
      return null;
    };
    vi.spyOn(console, "error").mockImplementation(() => {});
    const swallow = (e: ErrorEvent) => e.preventDefault();
    window.addEventListener("error", swallow);
    expect(() => render(<Broken />)).toThrow("useTheme must be used inside ThemeProvider");
    window.removeEventListener("error", swallow);
  });
});
