import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { desktop, type Repo } from "@/api/desktop";
import { needsRemote, remoteOnly, type Row } from "@/lib/files";
import { parseRepoUrl } from "@/lib/sparse";

export const filesKey = (root: string, dir?: string) => (dir === undefined ? ["files", root] : ["files", root, dir]);

// useDirectory lists one folder: the clone's entries plus, for a sparse
// clone outside its checkout, the server's folders it leaves out.
export function useDirectory(repo: Repo, dir: string, enabled = true) {
  const sparse = useMemo(() => repo.sparse ?? [], [repo.sparse]);
  const local = useQuery({ queryKey: filesKey(repo.root, dir), queryFn: () => desktop.files(repo.root, dir), enabled, placeholderData: (prev) => prev });
  const address = parseRepoUrl(repo.url);
  const remote = useQuery({
    queryKey: ["remote-tree", repo.root, repo.branch, dir],
    queryFn: () => desktop.remoteTree(address!.server, address!.org, address!.project, repo.branch, dir),
    enabled: enabled && !!address && needsRemote(dir, sparse),
    staleTime: 5 * 60_000,
    retry: false,
  });
  const localEntries = local.data?.dir === dir ? local.data.entries : undefined;
  const rows = useMemo<Row[]>(() => {
    if (!localEntries) return [];
    return [...localEntries, ...remoteOnly(remote.data?.dirs ?? [], localEntries, sparse)];
  }, [localEntries, remote.data, sparse]);
  return { rows, loading: !localEntries && (local.isPending || local.isFetching), error: localEntries ? null : local.error, refetch: local.refetch };
}
