import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Account, OpProgress, RemoteListing } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({
  accountStatus: vi.fn(),
  signIn: vi.fn(),
  signInMFA: vi.fn(),
  signInSSH: vi.fn(),
  listProjects: vi.fn(),
  remoteBranches: vi.fn(),
  remoteTree: vi.fn(),
  chooseFolder: vi.fn(),
  clone: vi.fn(),
  onOpProgress: vi.fn(),
}));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { CloneDialog } from "./CloneDialog";

const account = (over: Partial<Account> = {}): Account => ({ host: "nipa.studio.test", address: "https://nipa.studio.test", signedIn: true, email: "ann@studio.test", userId: "u1", ...over });

const trees: Record<string, RemoteListing> = {
  "": { dirs: [{ name: "art", path: "art" }, { name: "audio", path: "audio" }], files: 2, fileBytes: 2048 },
  art: { dirs: [{ name: "props", path: "art/props" }], files: 1, fileBytes: 10 },
  "art/props": { dirs: [], files: 0, fileBytes: 0 },
};

let progress: (p: OpProgress) => void = () => {};
const onClose = vi.fn();
const onCloned = vi.fn();
const show = () => renderWithProviders(<CloneDialog onClose={onClose} onCloned={onCloned} />);

beforeEach(() => {
  vi.resetAllMocks();
  api.accountStatus.mockResolvedValue(account());
  api.listProjects.mockResolvedValue([
    { org: "studio", orgName: "Studio", orgRole: "owner", project: "game", name: "Game", description: "The main title" },
    { org: "studio", orgName: "Studio", orgRole: "owner", project: "tools", name: "", description: "" },
    { org: "infra", orgName: "", orgRole: "member", project: "ci", name: "CI", description: "" },
  ]);
  api.remoteBranches.mockResolvedValue([
    { name: "main", isDefault: true, protected: true },
    { name: "feature", isDefault: false, protected: false },
  ]);
  api.remoteTree.mockImplementation(async (_s: string, _o: string, _p: string, _b: string, path: string) => trees[path]);
  api.onOpProgress.mockImplementation((fn: (p: OpProgress) => void) => {
    progress = fn;
    return () => {};
  });
});

async function toOptions() {
  show();
  const server = screen.getByRole("textbox", { name: "Server" });
  await userEvent.type(server, "https://nipa.studio.test");
  await userEvent.click(screen.getByRole("button", { name: "Continue" }));
  await userEvent.click(await screen.findByRole("button", { name: /Game/ }));
  await screen.findByRole("option", { name: "main (default)" });
}

describe("CloneDialog", () => {
  it("browses projects and clones a sparse checkout with progress", async () => {
    let finish!: (v: unknown) => void;
    api.clone.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    api.chooseFolder.mockResolvedValueOnce("/home/ann/work");
    await toOptions();
    expect(api.accountStatus).toHaveBeenCalledWith("https://nipa.studio.test");
    expect(localStorage.getItem("nipa.lastServer")).toBe("https://nipa.studio.test");
    expect(screen.getByText("ann@studio.test · nipa.studio.test")).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Branch" }), "feature");
    await userEvent.click(screen.getByRole("radio", { name: "Only some folders" }));
    expect(await screen.findByText("2 files · 2.0 KB at the top level (only with Everything)")).toBeInTheDocument();
    expect(api.remoteTree).toHaveBeenCalledWith("https://nipa.studio.test", "studio", "game", "feature", "");
    expect(screen.getByRole("button", { name: "Clone" })).toBeDisabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "art" }));
    await userEvent.click(screen.getByRole("button", { name: "Expand art" }));
    const props = await screen.findByRole("checkbox", { name: "art/props" });
    expect(props).toBeChecked();
    expect(props).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Expand art/props" }));
    expect(await screen.findByText("Empty")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Browse/ }));
    expect(screen.getByRole("textbox", { name: "Location" })).toHaveValue("/home/ann/work");
    await userEvent.clear(screen.getByRole("textbox", { name: "Folder name" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Folder name" }), "game-art");
    expect(screen.getByText("/home/ann/work/game-art")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Clone" }));
    expect(api.clone).toHaveBeenCalledWith({ server: "https://nipa.studio.test", org: "studio", project: "game", branch: "feature", sparse: ["art"], target: "/home/ann/work/game-art" });
    expect(localStorage.getItem("nipa.cloneParent")).toBe("/home/ann/work");
    expect(screen.getByRole("status")).toHaveTextContent("Cloning…");
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    act(() => progress({ root: "/home/ann/work/game-art", op: "clone", phase: "download", ahead: 0, objectsDone: 1, objectsTotal: 4, bytesDone: 0, bytesTotal: 0 }));
    act(() => progress({ root: "/elsewhere", op: "clone", phase: "download", ahead: 0, objectsDone: 3, objectsTotal: 4, bytesDone: 0, bytesTotal: 0 }));
    expect(screen.getByRole("status")).toHaveTextContent("Downloading 1/4");

    const repo = { root: "/home/ann/work/game-art", name: "game-art", url: "https://nipa.studio.test/studio/game", branch: "feature", sparse: ["art"] };
    await act(async () => finish(repo));
    expect(onCloned).toHaveBeenCalledWith(repo);
  });

  it("offers retry and sign-in when branches fail or are missing", async () => {
    api.remoteBranches.mockRejectedValueOnce(new Error("session expired")).mockResolvedValueOnce([]);
    show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "https://nipa.studio.test");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await userEvent.click(await screen.findByRole("button", { name: /Game/ }));
    expect(await screen.findByText(/session expired/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("This project has no branches to clone.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Sign in again" }));
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  });

  it("signs in with an SSH key first and falls back to a password", async () => {
    api.accountStatus.mockResolvedValueOnce(account({ signedIn: false, email: "" })).mockResolvedValue(account({ email: "" }));
    api.signInSSH.mockRejectedValueOnce(new Error("invalid email or SSH key"));
    api.signIn.mockRejectedValueOnce(new Error("invalid credentials")).mockResolvedValueOnce({ mfaRequired: false, challenge: "", expiresIn: 0 });
    show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "nipa.studio.test{Enter}");
    expect(await screen.findByText("SSH key sign-in failed.")).toBeInTheDocument();
    expect(screen.getByText("invalid email or SSH key")).toBeInTheDocument();
    expect(api.signInSSH).toHaveBeenCalledWith("https://nipa.studio.test", "");

    await userEvent.type(screen.getByRole("textbox", { name: "Email" }), "ann@studio.test");
    await userEvent.type(screen.getByLabelText("Password"), "secret");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("invalid credentials");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(api.signIn).toHaveBeenLastCalledWith("https://nipa.studio.test", "ann@studio.test", "secret");
    expect(await screen.findByRole("button", { name: /Game/ })).toBeInTheDocument();
    expect(screen.getByText("nipa.studio.test")).toBeInTheDocument();
  });

  it("asks for a two-factor code after the password", async () => {
    api.accountStatus.mockResolvedValueOnce(account({ signedIn: false, email: "" })).mockResolvedValue(account({ email: "" }));
    api.signInSSH.mockRejectedValueOnce(new Error("invalid email or SSH key"));
    api.signIn.mockResolvedValue({ mfaRequired: true, challenge: "ch-1", expiresIn: 300 });
    api.signInMFA.mockRejectedValueOnce(new Error("invalid code")).mockResolvedValueOnce(undefined);
    show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "nipa.studio.test{Enter}");
    expect(await screen.findByText("SSH key sign-in failed.")).toBeInTheDocument();

    await userEvent.type(screen.getByRole("textbox", { name: "Email" }), "ann@studio.test");
    await userEvent.type(screen.getByLabelText("Password"), "secret");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    const codeInput = await screen.findByRole("textbox", { name: "Authentication code" });
    expect(screen.getByRole("button", { name: "Verify" })).toBeDisabled();

    await userEvent.type(codeInput, "000000");
    await userEvent.click(screen.getByRole("button", { name: "Verify" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("invalid code");
    expect(api.signInMFA).toHaveBeenLastCalledWith("https://nipa.studio.test", "ann@studio.test", "ch-1", "000000");

    await userEvent.clear(codeInput);
    await userEvent.type(codeInput, "123456");
    await userEvent.click(screen.getByRole("button", { name: "Verify" }));
    expect(api.signInMFA).toHaveBeenLastCalledWith("https://nipa.studio.test", "ann@studio.test", "ch-1", "123456");
    expect(await screen.findByRole("button", { name: /Game/ })).toBeInTheDocument();
  });

  it("goes back to the password from the code step", async () => {
    api.accountStatus.mockResolvedValueOnce(account({ signedIn: false, email: "" })).mockResolvedValue(account({ email: "" }));
    api.signInSSH.mockRejectedValueOnce(new Error("invalid email or SSH key"));
    api.signIn.mockResolvedValue({ mfaRequired: true, challenge: "ch-1", expiresIn: 300 });
    show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "nipa.studio.test{Enter}");
    expect(await screen.findByText("SSH key sign-in failed.")).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Email" }), "ann@studio.test");
    await userEvent.type(screen.getByLabelText("Password"), "secret");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await userEvent.click(await screen.findByRole("button", { name: "Start over" }));
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(api.signInMFA).not.toHaveBeenCalled();
  });

  it("signs in with an SSH key without asking", async () => {
    api.accountStatus.mockResolvedValueOnce(account({ signedIn: false })).mockResolvedValue(account());
    api.signInSSH.mockResolvedValue(undefined);
    show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "nipa.studio.test{Enter}");
    expect(await screen.findByRole("button", { name: /Game/ })).toBeInTheDocument();
  });

  it("filters projects, goes back and reports problems", async () => {
    api.accountStatus.mockRejectedValueOnce(new Error("invalid URL scheme, expected http or https"));
    show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "ftp://x{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("invalid URL scheme");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("region", { name: "Studio" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "infra" })).toBeInTheDocument();
    expect(screen.getByText("studio/tools")).toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Filter projects" }), "main title");
    expect(screen.queryByText("studio/tools")).not.toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: "Filter projects" }), "zzz");
    expect(screen.getByText("No project matches.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByRole("textbox", { name: "Server" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("explains empty and failed project lists", async () => {
    api.listProjects.mockResolvedValueOnce([]);
    const { unmount } = show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "a{Enter}");
    expect(await screen.findByText("This account cannot read any project on this server yet.")).toBeInTheDocument();
    unmount();

    api.listProjects.mockRejectedValueOnce(new Error("unable to get access token"));
    api.accountStatus.mockResolvedValueOnce(account()).mockResolvedValueOnce(account());
    api.signInSSH.mockReturnValue(new Promise(() => {}));
    show();
    await userEvent.type(screen.getByRole("textbox", { name: "Server" }), "b{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("unable to get access token");
    await userEvent.click(screen.getByRole("button", { name: "Sign in again" }));
    expect(await screen.findByText("Trying your SSH keys…")).toBeInTheDocument();
  });

  it("shows clone failures and cancels a running clone", async () => {
    api.clone.mockRejectedValueOnce(new Error("target directory is not empty"));
    await toOptions();
    await userEvent.type(screen.getByRole("textbox", { name: "Location" }), "C:\\Projects");
    expect(screen.getByText("C:\\Projects\\game")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Clone" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("target directory is not empty");

    const cancel = vi.fn();
    let fail!: (e: unknown) => void;
    api.clone.mockReturnValueOnce(Object.assign(new Promise((_, reject) => (fail = reject)), { cancel }));
    await userEvent.click(screen.getByRole("button", { name: "Clone" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(cancel).toHaveBeenCalled();
    await act(async () => fail(new Error("context canceled")));
    expect(await screen.findByRole("alert")).toHaveTextContent("context canceled");

    api.chooseFolder.mockRejectedValueOnce(new Error("dialog broke"));
    await userEvent.click(screen.getByRole("button", { name: /Browse/ }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("dialog broke"));
    api.chooseFolder.mockResolvedValueOnce("");
    await userEvent.click(screen.getByRole("button", { name: /Browse/ }));
    await userEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(await screen.findByRole("button", { name: /Game/ })).toBeInTheDocument();
    expect(within(screen.getByRole("dialog")).getByText("Choose a project")).toBeInTheDocument();
  });

  it("closes with Escape unless a clone is running", async () => {
    show();
    await userEvent.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
