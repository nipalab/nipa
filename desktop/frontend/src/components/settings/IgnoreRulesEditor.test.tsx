import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { IgnoreRulesEditor } from "./IgnoreRulesEditor";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ ignoreRules: vi.fn(), saveIgnoreRules: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

const notify = vi.fn();

beforeEach(() => {
  vi.resetAllMocks();
  api.ignoreRules.mockResolvedValue({ shared: "*.tmp\n", local: "" });
});

describe("IgnoreRulesEditor", () => {
  it("edits and saves both rule files", async () => {
    api.saveIgnoreRules.mockRejectedValueOnce(new Error("read-only")).mockResolvedValueOnce(undefined);
    renderWithProviders(<IgnoreRulesEditor root="/work/game" notify={notify} />);
    expect(screen.getByText("Loading ignore rules…")).toBeInTheDocument();
    const shared = await screen.findByLabelText("Shared rules (.nipaignore)");
    expect(shared).toHaveValue("*.tmp\n");
    const save = screen.getByRole("button", { name: /Save ignore rules/ });
    expect(save).toBeDisabled();

    await userEvent.type(shared, "Build/");
    await userEvent.type(screen.getByLabelText("Only on this computer (.nipa/ignore)"), "scratch/");
    await userEvent.click(save);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Could not save the ignore rules: read-only"));
    await userEvent.click(save);
    await waitFor(() => expect(notify).toHaveBeenCalledWith("Ignore rules saved", "success"));
    expect(api.saveIgnoreRules).toHaveBeenLastCalledWith("/work/game", { shared: "*.tmp\nBuild/", local: "scratch/" });
    await waitFor(() => expect(save).toBeDisabled());
  });

  it("reports a failed read", async () => {
    api.ignoreRules.mockRejectedValue(new Error("permission denied"));
    renderWithProviders(<IgnoreRulesEditor root="/work/game" notify={notify} />);
    expect(await screen.findByText("Could not read the ignore rules: permission denied")).toBeInTheDocument();
  });
});
