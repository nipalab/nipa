import { useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Loader2 } from "lucide-react";
import { desktop, type AssetReferences, type ChangeSet } from "@/api/desktop";
import { ConfirmDialog } from "@/components/ui/confirm";
import { refsKey, useAssetRefs, useReferrers } from "@/hooks/useAssetInsight";
import { deletionTargets, plural, referrerSummary } from "@/lib/insight";

export const QUICK_LOOKUP_MS = 400;
const LOOKUP_STALE_MS = 5_000;

// ReferrerNote warns, inside a confirmation, that other files use the asset.
export function ReferrerNote({ root, path, enabled = true }: { root: string; path: string; enabled?: boolean }) {
  const refs = useAssetRefs(root, path, enabled);
  if (!enabled || !refs.data?.asset || refs.data.usedBy.length === 0) return null;
  return <Warning>{referrerSummary(refs.data.usedBy)}</Warning>;
}

type PendingStage = { paths: string[]; targets: string[] };

// useStageGuard stages paths, first asking for confirmation when a staged
// deletion removes an asset other files still use. Lookups come from the
// daemon's cached graph; one that is slower than QUICK_LOOKUP_MS opens the
// confirmation right away and fills in when it arrives.
export function useStageGuard(root: string, changes: ChangeSet | undefined, stage: (paths: string[]) => void) {
  const queryClient = useQueryClient();
  const [pending, setPending] = useState<PendingStage | null>(null);

  const request = async (paths: string[]) => {
    const targets = changes ? deletionTargets(changes, paths) : [];
    if (targets.length === 0) return stage(paths);
    const lookups = Promise.allSettled(
      targets.map((path) =>
        queryClient.fetchQuery({
          queryKey: refsKey(root, path),
          queryFn: () => desktop.assetRefs(root, path),
          retry: false,
          staleTime: LOOKUP_STALE_MS,
        }),
      ),
    );
    const settled = await Promise.race([lookups, new Promise<null>((resolve) => setTimeout(() => resolve(null), QUICK_LOOKUP_MS))]);
    const clear = settled?.every((r) => r.status === "rejected" || r.value.usedBy.length === 0);
    if (clear) return stage(paths);
    setPending({ paths, targets });
  };

  const dialog = pending ? (
    <DeleteReferrersDialog
      root={root}
      targets={pending.targets}
      onCancel={() => setPending(null)}
      onConfirm={() => {
        stage(pending.paths);
        setPending(null);
      }}
    />
  ) : null;
  return { request: (paths: string[]) => void request(paths), dialog };
}

function DeleteReferrersDialog({ root, targets, onCancel, onConfirm }: { root: string; targets: string[]; onCancel: () => void; onConfirm: () => void }) {
  const results = useReferrers(root, targets);
  const loading = results.some((r) => r.isPending);
  const used = targets.flatMap((path, i) => {
    const data: AssetReferences | undefined = results[i]?.data;
    return data && data.usedBy.length > 0 ? [{ path, usedBy: data.usedBy }] : [];
  });
  const what = targets.length === 1 ? targets[0] : plural(targets.length, "file");
  return (
    <ConfirmDialog title="Stage deletion?" message={`Staging deletes ${what} from the project when you push.`} confirmLabel="Stage deletion" onCancel={onCancel} onConfirm={onConfirm}>
      <div aria-live="polite" className="space-y-1.5">
        {used.map((u) => (
          <Warning key={u.path}>
            {targets.length > 1 && <span className="font-mono">{u.path}: </span>}
            {referrerSummary(u.usedBy)}
          </Warning>
        ))}
        {loading ? (
          <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Loader2 className="size-3 animate-spin" /> Checking which files use {targets.length === 1 ? "it" : "them"}…
          </p>
        ) : (
          used.length === 0 && <p className="text-[11px] text-muted-foreground">No other file references {targets.length === 1 ? "it" : "them"}.</p>
        )}
      </div>
    </ConfirmDialog>
  );
}

function Warning({ children }: { children: ReactNode }) {
  return (
    <p className="flex items-start gap-1.5 break-words text-[11px] text-warning">
      <AlertTriangle className="mt-px size-3.5 shrink-0" />
      <span className="min-w-0">{children}</span>
    </p>
  );
}
