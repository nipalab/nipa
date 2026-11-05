import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SettingsDialog, pollChoices } from "./SettingsDialog";
import { renderWithProviders } from "@/test/render";
import type { Settings } from "@/api/desktop";

const api = vi.hoisted(() => ({
  settings: vi.fn(),
  saveSettings: vi.fn(),
  uploadWorkers: vi.fn(),
  setUploadWorkers: vi.fn(),
  restartDaemon: vi.fn(),
  chooseProgram: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

const base: Settings = { keepInTray: true, startAtLogin: false, pollSeconds: 60, notify: { pushes: true, locks: true, reviews: true }, tools: [] };

const notify = vi.fn();
const onClose = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.settings.mockResolvedValue(base);
  api.uploadWorkers.mockResolvedValue(0);
  api.saveSettings.mockImplementation(async (s: Settings) => s);
  api.setUploadWorkers.mockResolvedValue(undefined);
});

const render = () => renderWithProviders(<SettingsDialog onClose={onClose} notify={notify} />);

describe("SettingsDialog", () => {
  it("saves the preferences", async () => {
    render();
    expect(screen.getByRole("status")).toHaveTextContent("Loading settings");
    await userEvent.click(await screen.findByLabelText("Start Nipa when I log in"));
    await userEvent.click(screen.getByLabelText("Keep Nipa running in the tray when the window is closed"));
    await userEvent.click(screen.getByLabelText("Teammates push to my branch"));
    await userEvent.click(screen.getByLabelText("A lock is released"));
    await userEvent.click(screen.getByLabelText("Someone asks for my review"));
    await userEvent.selectOptions(screen.getByLabelText("Check the server every"), "300");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(api.saveSettings).toHaveBeenCalledWith({ keepInTray: false, startAtLogin: true, pollSeconds: 300, notify: { pushes: false, locks: false, reviews: false }, tools: [] });
    expect(api.setUploadWorkers).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledWith("Settings saved", "success");
  });

  it("edits external tools", async () => {
    api.settings.mockResolvedValue({ ...base, tools: [{ extension: ".psd", command: "krita" }] });
    api.chooseProgram.mockResolvedValueOnce("/usr/bin/blender").mockResolvedValueOnce("").mockRejectedValueOnce(new Error("no dialogs"));
    render();
    await userEvent.click(await screen.findByRole("button", { name: /Add a program/ }));
    await userEvent.type(screen.getByLabelText("Extension 2"), ".blend");
    await userEvent.click(screen.getByRole("button", { name: "Choose program 2" }));
    await waitFor(() => expect(screen.getByLabelText("Program 2")).toHaveValue("/usr/bin/blender"));
    await userEvent.click(screen.getByRole("button", { name: "Choose program 2" }));
    expect(screen.getByLabelText("Program 2")).toHaveValue("/usr/bin/blender");
    await userEvent.click(screen.getByRole("button", { name: "Choose program 1" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("no dialogs"));
    await userEvent.clear(screen.getByLabelText("Program 1"));
    await userEvent.type(screen.getByLabelText("Program 1"), "gimp");
    await userEvent.click(screen.getByRole("button", { name: "Remove tool 1" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledWith({ ...base, tools: [{ extension: ".blend", command: "/usr/bin/blender" }] }));
  });

  it("shows save errors", async () => {
    api.saveSettings.mockRejectedValue(new Error("choose a program for .psd files"));
    render();
    await userEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("choose a program for .psd files");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("pins upload workers and offers a restart", async () => {
    api.uploadWorkers.mockResolvedValue(8);
    api.restartDaemon.mockRejectedValueOnce(new Error("busy")).mockResolvedValueOnce({ version: "v1" });
    render();
    const input = await screen.findByLabelText("Uploads at once");
    expect(input).toHaveValue("8");
    await userEvent.clear(input);
    await userEvent.type(input, "99");
    expect(screen.getByText(/Enter a number from 1 to 64/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await userEvent.clear(input);
    await userEvent.type(input, "4");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.setUploadWorkers).toHaveBeenCalledWith(4));
    expect(await screen.findByText(/Restart the background service/)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: /Restart/ }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not restart the background service: busy"));
    await userEvent.click(screen.getByRole("button", { name: /Restart/ }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Background service restarted", "success"));
    expect(onClose).toHaveBeenCalled();
  });

  it("clears pinned workers and closes with Escape or Cancel", async () => {
    api.uploadWorkers.mockResolvedValue(8);
    render();
    const input = await screen.findByLabelText("Uploads at once");
    await userEvent.clear(input);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.setUploadWorkers).toHaveBeenCalledWith(0));
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("reports a failed load", async () => {
    api.settings.mockRejectedValue(new Error("parse desktop.json"));
    render();
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load settings: parse desktop.json");
  });

  it("keeps an unusual poll interval selectable", () => {
    expect(pollChoices(60).map((o) => o.seconds)).toEqual([30, 60, 300, 900, 3600]);
    expect(pollChoices(120).find((o) => o.seconds === 120)?.label).toBe("120 seconds");
    expect(pollChoices(120).map((o) => o.seconds)).toEqual([30, 60, 120, 300, 900, 3600]);
  });
});
