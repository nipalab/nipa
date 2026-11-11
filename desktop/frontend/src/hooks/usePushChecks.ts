import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type PushChecks } from "@/api/desktop";

export const PUSH_CHECKS_DEBOUNCE_MS = 400;

export const pushChecksKey = (root: string) => ["pushChecks", root] as const;

export function hasPushIssues(checks: PushChecks | undefined): boolean {
  return !!checks && checks.violations.length + checks.broken.length + checks.newlyUnused.length > 0;
}

export function hasPushReport(checks: PushChecks | undefined): boolean {
  return hasPushIssues(checks) || (checks?.warnings.length ?? 0) > 0;
}

function useDebounced<T>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return debounced;
}

// usePushChecks runs the warn-only asset checks of the staged set, debounced
// and refreshed when the working copy changes.
export function usePushChecks(root: string, staged: string[]) {
  const queryClient = useQueryClient();
  const stagedKey = useDebounced([...staged].sort().join("\n"), PUSH_CHECKS_DEBOUNCE_MS);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const off = desktop.onRepoChanged((change) => {
      if (change.root !== root) return;
      clearTimeout(timer);
      timer = setTimeout(() => void queryClient.invalidateQueries({ queryKey: pushChecksKey(root) }), PUSH_CHECKS_DEBOUNCE_MS);
    });
    return () => {
      clearTimeout(timer);
      off();
    };
  }, [root, queryClient]);

  return useQuery<PushChecks>({
    queryKey: [...pushChecksKey(root), stagedKey],
    queryFn: () => desktop.pushChecks(root),
    enabled: stagedKey !== "",
    placeholderData: (prev) => prev,
    retry: false,
  });
}
