import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SettingsDialog, pollChoices } from "./SettingsDialog";
import { readTour } from "@/lib/mode";
import { renderWithProviders } from "@/test/render";
import type { Settings, UpdateState } from "@/api/desktop";

const api = vi.hoisted(() => ({
  settings: vi.fn(),
  saveSettings: vi.fn(),
  uploadWorkers: vi.fn(),
  setUploadWorkers: vi.fn(),
  restartDaemon: vi.fn(),
  chooseProgram: vi.fn(),
  updateStatus: vi.fn(),
  checkForUpdate: vi.fn(),
  installUpdate: vi.fn(),
  restartToUpdate: vi.fn(),
  openUpdatePage: vi.fn(),
  onUpdate: vi.fn(),
  llmSettings: vi.fn(),
  setLLM: vi.fn(),
  setLLMKey: vi.fn(),
  clearLLMKey: vi.fn(),
  testLLM: vi.fn(),
  setZoom: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

const base: Settings = { keepInTray: true, startAtLogin: false, pollSeconds: 60, notify: { pushes: true, locks: true, reviews: true, lockRequests: true, maintenance: true }, tools: [], manualUpdates: false };

const idle: UpdateState = { current: "1.0.0", enabled: true, inPlace: true, phase: "idle", release: null, written: 0, total: 0, error: "", checkedAt: "" };

const notify = vi.fn();
const onClose = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.settings.mockResolvedValue(base);
  api.uploadWorkers.mockResolvedValue(0);
  api.saveSettings.mockImplementation(async (s: Settings) => s);
  api.setUploadWorkers.mockResolvedValue(undefined);
  api.updateStatus.mockResolvedValue(idle);
  api.onUpdate.mockReturnValue(() => {});
  api.llmSettings.mockResolvedValue(noLLM);
  api.setZoom.mockResolvedValue(undefined);
});

const noLLM = { endpoint: "", model: "", keySaved: false, configured: false };

const render = () => renderWithProviders(<SettingsDialog onClose={onClose} notify={notify} />);

const openTab = async (name: string) => userEvent.click(await screen.findByRole("tab", { name }));

describe("SettingsDialog interface", () => {
  it("changes and resets the zoom", async () => {
    render();
    await openTab("Appearance");
    const reset = await screen.findByRole("button", { name: "Reset" });
    expect(reset).toBeDisabled();
    await userEvent.selectOptions(screen.getByLabelText("Zoom"), "1.25");
    expect(api.setZoom).toHaveBeenLastCalledWith(1.25);
    expect(screen.getByLabelText("Zoom")).toHaveValue("1.25");
    await userEvent.click(reset);
    expect(api.setZoom).toHaveBeenLastCalledWith(1);
    expect(screen.getByLabelText("Zoom")).toHaveValue("1");
  });

  it("replays the tour only when a project is open", async () => {
    const { unmount } = renderWithProviders(<SettingsDialog onClose={onClose} notify={notify} canReplayTour={false} />);
    expect(await screen.findByRole("button", { name: "Replay" })).toBeDisabled();
    expect(screen.getByText("Open or clone a project first")).toBeInTheDocument();
    unmount();
    renderWithProviders(<SettingsDialog onClose={onClose} notify={notify} />);
    await userEvent.click(await screen.findByRole("button", { name: "Replay" }));
    expect(readTour()).toBe(0);
    expect(onClose).toHaveBeenCalled();
  });
});

describe("SettingsDialog sections", () => {
  it("shows one section at a time and keeps edits across sections", async () => {
    render();
    const general = await screen.findByRole("tab", { name: "General" });
    expect(general).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel")).toHaveAccessibleName("General");
    expect(screen.getByText("Language, mode and how Nipa starts.")).toBeVisible();
    await userEvent.click(screen.getByLabelText("Start Nipa when I log in"));
    await openTab("Updates");
    expect(screen.getByRole("tabpanel")).toHaveAccessibleName("Updates");
    await openTab("General");
    expect(screen.getByLabelText("Start Nipa when I log in")).toBeChecked();
  });

  it("moves between sections with the arrow keys", async () => {
    render();
    const general = await screen.findByRole("tab", { name: "General" });
    general.focus();
    await userEvent.keyboard("{ArrowDown}");
    expect(screen.getByRole("tab", { name: "Appearance" })).toHaveFocus();
    expect(screen.getByRole("tab", { name: "Appearance" })).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{ArrowUp}{ArrowUp}");
    expect(screen.getByRole("tab", { name: "Advanced" })).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{Home}");
    expect(general).toHaveAttribute("aria-selected", "true");
    await userEvent.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "Advanced" })).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}x");
    expect(general).toHaveFocus();
  });

  it("marks a section with an invalid value", async () => {
    render();
    await openTab("Advanced");
    const tab = screen.getByRole("tab", { name: "Advanced" });
    expect(tab.querySelector(".bg-danger")).toBeNull();
    await userEvent.type(screen.getByLabelText("Uploads at once"), "99");
    expect(tab.querySelector(".bg-danger")).not.toBeNull();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("picks the theme and toggles simple mode", async () => {
    render();
    await userEvent.click(await screen.findByRole("switch", { name: "Simple mode" }));
    expect(screen.getByRole("switch", { name: "Simple mode" })).toBeChecked();
    await openTab("Appearance");
    await userEvent.click(screen.getByRole("radio", { name: "Light" }));
    expect(screen.getByRole("radio", { name: "Light" })).toBeChecked();
  });

  it("lists no external tools until one is added", async () => {
    render();
    await openTab("External tools");
    expect(screen.getByText("No programs yet. Files open with the system default.")).toBeInTheDocument();
  });

  it("restarts the background service on demand", async () => {
    api.restartDaemon.mockResolvedValue({ version: "v1" });
    render();
    await openTab("Advanced");
    await userEvent.click(screen.getByRole("button", { name: "Restart" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Background service restarted", "success"));
  });
});

describe("SettingsDialog", () => {
  it("saves the preferences", async () => {
    render();
    expect(screen.getByRole("status")).toHaveTextContent("Loading settings");
    await userEvent.click(await screen.findByLabelText("Start Nipa when I log in"));
    await userEvent.click(screen.getByLabelText("Keep running in the tray"));
    expect(screen.getByLabelText("Start Nipa when I log in")).toBeChecked();
    expect(screen.getByLabelText("Keep running in the tray")).not.toBeChecked();
    await openTab("Notifications");
    await userEvent.click(screen.getByLabelText("Teammates push to my branch"));
    await userEvent.click(screen.getByLabelText("A lock is released"));
    await userEvent.click(screen.getByLabelText("Someone asks me to release a lock"));
    await userEvent.click(screen.getByLabelText("Someone asks for my review"));
    await userEvent.click(screen.getByLabelText("Maintenance is scheduled, starts or ends"));
    await userEvent.selectOptions(screen.getByLabelText("Check the server every"), "300");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(api.saveSettings).toHaveBeenCalledWith({ keepInTray: false, startAtLogin: true, pollSeconds: 300, notify: { pushes: false, locks: false, reviews: false, lockRequests: false, maintenance: false }, tools: [], manualUpdates: false });
    expect(api.setUploadWorkers).not.toHaveBeenCalled();
    expect(notify).toHaveBeenCalledWith("Settings saved", "success");
  });

  it("edits external tools", async () => {
    api.settings.mockResolvedValue({ ...base, tools: [{ extension: ".psd", command: "krita" }] });
    api.chooseProgram.mockResolvedValueOnce("/usr/bin/blender").mockResolvedValueOnce("").mockRejectedValueOnce(new Error("no dialogs"));
    render();
    await openTab("External tools");
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
    await openTab("Advanced");
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
    expect(await screen.findByText("Restart the background service to use the new upload setting.")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Advanced" })).toHaveAttribute("aria-selected", "true");
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
    await openTab("Advanced");
    const input = await screen.findByLabelText("Uploads at once");
    await userEvent.clear(input);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.setUploadWorkers).toHaveBeenCalledWith(0));
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("saves the AI endpoint, model and key without ever showing the key", async () => {
    api.setLLM.mockResolvedValue({ endpoint: "http://localhost:11434/v1", model: "qwen", keySaved: false, configured: true });
    api.setLLMKey.mockResolvedValue(undefined);
    render();
    await openTab("AI assistance");
    expect(await screen.findByText("Set an endpoint and a model to show the Generate buttons.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Test" })).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Endpoint"), "http://localhost:11434/v1");
    expect(screen.getByText("Set both the endpoint and the model, or clear both.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Model"), "qwen");
    const key = screen.getByLabelText("API key");
    expect(key).toHaveAttribute("type", "password");
    expect(key).toHaveAttribute("placeholder", "Optional");
    await userEvent.type(key, " sk-1 ");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(api.setLLM).toHaveBeenCalledWith("http://localhost:11434/v1", "qwen");
    expect(api.setLLMKey).toHaveBeenCalledWith("sk-1");
  });

  it("leaves unchanged AI settings alone and clears a saved key", async () => {
    api.llmSettings.mockResolvedValue({ endpoint: "http://x/v1", model: "m", keySaved: true, configured: true });
    api.clearLLMKey.mockRejectedValueOnce(new Error("keychain locked")).mockResolvedValueOnce(undefined);
    render();
    await openTab("AI assistance");
    const key = await screen.findByLabelText("API key");
    expect(key).toHaveValue("");
    expect(key).toHaveAttribute("placeholder", "Saved");
    expect(screen.queryByText("Set an endpoint and a model to show the Generate buttons.")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not remove the API key: keychain locked"));
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("API key removed", "success"));
    expect(screen.queryByRole("button", { name: "Clear" })).not.toBeInTheDocument();
    expect(key).toHaveAttribute("placeholder", "Optional");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(api.setLLM).not.toHaveBeenCalled();
    expect(api.setLLMKey).not.toHaveBeenCalled();
  });

  it("tests the endpoint and shows the result", async () => {
    api.llmSettings.mockResolvedValue({ endpoint: "http://x/v1", model: "m", keySaved: false, configured: true });
    api.testLLM.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("LLM endpoint answered 401: bad key"));
    render();
    await openTab("AI assistance");
    await userEvent.type(await screen.findByLabelText("API key"), "sk-typed");
    await userEvent.click(screen.getByRole("button", { name: "Test" }));
    expect(await screen.findByText("m answered")).toBeInTheDocument();
    expect(api.testLLM).toHaveBeenCalledWith("http://x/v1", "m", "sk-typed");
    await userEvent.click(screen.getByRole("button", { name: "Test" }));
    expect(await screen.findByText("LLM endpoint answered 401: bad key")).toBeInTheDocument();
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

  it("turns automatic updates off and checks for updates", async () => {
    api.checkForUpdate.mockResolvedValue({ ...idle, phase: "upToDate" });
    render();
    await openTab("Updates");
    expect(await screen.findByText("Nipa 1.0.0")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Check now" }));
    expect(await screen.findByText("Nipa 1.0.0 is up to date.")).toBeInTheDocument();
    await userEvent.click(screen.getByLabelText("Download updates automatically"));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledWith({ ...base, manualUpdates: true }));
  });

  it("downloads, shows progress and restarts", async () => {
    const release = { version: "1.1.0", notes: "Faster clones", publishedAt: "", page: "https://example/r" };
    api.updateStatus.mockResolvedValue({ ...idle, phase: "available", release });
    api.installUpdate.mockResolvedValue({ ...idle, phase: "ready", release });
    api.restartToUpdate.mockRejectedValue(new Error("helper did not start"));
    let push: (s: UpdateState) => void = () => {};
    api.onUpdate.mockImplementation((fn: (s: UpdateState) => void) => {
      push = fn;
      return () => {};
    });
    render();
    await openTab("Updates");
    expect(await screen.findByText("Nipa 1.1.0 is available.")).toBeInTheDocument();
    expect(screen.getByText("Faster clones")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Download" }));
    expect(await screen.findByText("Nipa 1.1.0 is ready.")).toBeInTheDocument();

    act(() => push({ ...idle, phase: "downloading", release, written: 5, total: 10 }));
    expect(await screen.findByText("Downloading Nipa 1.1.0… 50%")).toBeInTheDocument();
    act(() => push({ ...idle, phase: "ready", release }));
    await userEvent.click(await screen.findByRole("button", { name: "Restart to update" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("helper did not start"));
  });

  it("opens the release page for package-managed installs", async () => {
    const release = { version: "1.1.0", notes: "", publishedAt: "", page: "https://example/r" };
    api.updateStatus.mockResolvedValue({ ...idle, inPlace: false, phase: "available", release });
    api.openUpdatePage.mockRejectedValue(new Error("no browser"));
    render();
    await openTab("Updates");
    expect(await screen.findByLabelText("Check for updates automatically")).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Get it from the website" }));
    await waitFor(() => expect(notify).toHaveBeenCalledWith("no browser"));
  });

  it("explains that development builds do not update", async () => {
    api.updateStatus.mockResolvedValue({ ...idle, enabled: false, current: "" });
    render();
    expect(await screen.findByText("This is a development build; it does not update itself.")).toBeInTheDocument();
    expect(screen.queryByLabelText("Download updates automatically")).toBeNull();
  });
});
