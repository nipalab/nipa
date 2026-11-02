import { beforeEach, describe, expect, it, vi } from "vitest";

const service = vi.hoisted(() => ({
  Connect: vi.fn(),
  Repos: vi.fn(),
  PickRepo: vi.fn(),
  AddRepo: vi.fn(),
  RemoveRepo: vi.fn(),
}));
vi.mock("../../bindings/github.com/nipalab/nipa/desktop/internal/service", () => ({ Service: service }));

import { desktop } from "./desktop";

beforeEach(() => vi.resetAllMocks());

describe("desktop api", () => {
  it("forwards calls to the bound service", async () => {
    service.Connect.mockResolvedValue({ version: "v1" });
    service.PickRepo.mockResolvedValue(null);
    service.AddRepo.mockResolvedValue({ root: "/a" });
    service.RemoveRepo.mockResolvedValue(undefined);

    expect(await desktop.connect()).toEqual({ version: "v1" });
    expect(await desktop.pickRepo()).toBeNull();
    expect(await desktop.addRepo("/a/sub")).toEqual({ root: "/a" });
    await desktop.removeRepo("/a");
    expect(service.AddRepo).toHaveBeenCalledWith("/a/sub");
    expect(service.RemoveRepo).toHaveBeenCalledWith("/a");
  });

  it("normalises a null repository list", async () => {
    service.Repos.mockResolvedValueOnce(null).mockResolvedValueOnce([{ root: "/a" }]);
    expect(await desktop.repos()).toEqual([]);
    expect(await desktop.repos()).toEqual([{ root: "/a" }]);
  });
});
