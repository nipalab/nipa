import { Service } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service";
import type { DaemonInfo, Repo } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";

export type { DaemonInfo, Repo };

export const desktop = {
  connect: (): Promise<DaemonInfo> => Service.Connect(),
  repos: async (): Promise<Repo[]> => (await Service.Repos()) ?? [],
  pickRepo: (): Promise<Repo | null> => Service.PickRepo(),
  addRepo: (path: string): Promise<Repo> => Service.AddRepo(path),
  removeRepo: (root: string): Promise<void> => Service.RemoveRepo(root),
};
