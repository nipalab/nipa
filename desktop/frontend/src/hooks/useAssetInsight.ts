import { useEffect } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type AssetReferences, type Origin } from "@/api/desktop";

export const INSIGHT_REFRESH_MS = 300;

export const refsKey = (root: string, path: string) => ["refs", root, path] as const;
export const provenanceKey = (root: string, path: string) => ["provenance", root, path] as const;

// useInsightRefresh refetches the active reference and provenance queries of
// a clone after a change notice; the daemon keeps its graph cached until a
// change can affect it, so most refreshes are cheap.
export function useInsightRefresh(root: string) {
  const queryClient = useQueryClient();
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const off = desktop.onRepoChanged((change) => {
      if (change.root !== root) return;
      clearTimeout(timer);
      timer = setTimeout(() => {
        void queryClient.invalidateQueries({ queryKey: ["refs", root] });
        void queryClient.invalidateQueries({ queryKey: ["provenance", root] });
      }, INSIGHT_REFRESH_MS);
    });
    return () => {
      clearTimeout(timer);
      off();
    };
  }, [root, queryClient]);
}

export function useAssetRefs(root: string, path: string, enabled = true) {
  return useQuery<AssetReferences>({
    queryKey: refsKey(root, path),
    queryFn: () => desktop.assetRefs(root, path),
    enabled: enabled && !!path,
    retry: false,
  });
}

export function useOrigin(root: string, path: string) {
  return useQuery<{ enabled: boolean; origin: Origin | null }>({
    queryKey: provenanceKey(root, path),
    queryFn: async () => {
      const p = await desktop.provenance(root, [path]);
      return {
        enabled: p.enabled,
        origin: p.origins.find((o) => o.path === path) ?? null,
      };
    },
    enabled: !!path,
    retry: false,
  });
}

// useReferrers looks up who uses each path, sharing the per-file reference
// queries so a lookup started early is reused by the confirmation.
export function useReferrers(root: string, paths: string[]) {
  return useQueries({
    queries: paths.map((path) => ({
      queryKey: refsKey(root, path),
      queryFn: () => desktop.assetRefs(root, path),
      retry: false,
    })),
  });
}
