import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { desktop, type FileLock, type FileLockState, type LockResult, type LockStatus } from "@/api/desktop";
import { errorMessage } from "@/lib/utils";
import { translate } from "@/i18n";

const POLL_MS = 30_000;

export const locksKey = (root: string) => ["locks", root] as const;

export function useLockStatus(root: string) {
  return useQuery<LockStatus>({
    queryKey: locksKey(root),
    queryFn: () => desktop.lockStatus(root),
    refetchInterval: POLL_MS,
    placeholderData: (prev) => prev,
    retry: false,
  });
}

export function lockStateMap(status: LockStatus | undefined): Map<string, FileLockState> {
  return new Map((status?.files ?? []).map((f) => [f.path, f]));
}

export type Notify = (message: string, tone?: "error" | "success") => void;

export function describeLockResult(r: LockResult): string | null {
  if (r.failed.length === 0) return null;
  const [first] = r.failed;
  const more = r.failed.length > 1 ? translate("useLocks.more", { value: r.failed.length - 1 }) : "";
  return translate("useLocks.couldNotLock", { path: first.path, message: first.message, more });
}

// useLockActions locks and releases files and refreshes the lock picture.
export function useLockActions(root: string, notify: Notify) {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: locksKey(root) });
  const lock = useMutation({
    mutationFn: ({ branch, paths }: { branch: string; paths: string[] }) => desktop.lock(root, branch, paths),
    onSuccess: (r) => {
      void refresh();
      const failure = describeLockResult(r);
      if (failure) notify(failure);
      else if (r.locked.length > 0) notify(r.locked.length === 1 ? translate("useLocks.locked", { value: r.locked[0] }) : translate("useLocks.lockedFiles", { count: r.locked.length }), "success");
    },
    onError: (err) => notify(translate("useLocks.lockingFailed", { value: errorMessage(err) })),
  });
  const unlock = useMutation({
    mutationFn: (l: FileLock) => desktop.unlock(root, l.global ? "" : l.branch, l.path),
    onSuccess: (_, l) => {
      void refresh();
      notify(translate("useLocks.released", { path: l.path }), "success");
    },
    onError: (err) => notify(translate("useLocks.releasingFailed", { value: errorMessage(err) })),
  });
  return { lock, unlock, refresh };
}
