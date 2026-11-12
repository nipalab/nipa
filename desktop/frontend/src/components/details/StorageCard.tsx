import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eraser, FolderTree } from "lucide-react";
import { desktop, type CleanUpResult } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/diff";
import { errorMessage } from "@/lib/utils";

export const SPARSE_HINT_BYTES = 20 * 1024 ** 3;

export const diskUsageKey = (root: string) => ["disk-usage", root] as const;

type StorageCardProps = {
  root: string;
  busy: boolean;
  notify: (message: string, tone?: "error" | "success") => void;
  onEditSparse: () => void;
};

export function describeCleanUp(res: CleanUpResult): [string, "error" | "success" | undefined] {
  if (res.skipped) {
    if (res.reason === "pending") return ["A merge or revert is in progress. Finish or abort it, then clean up.", "error"];
    return ["Repository is busy, try again", "error"];
  }
  if (res.bytes === 0) return ["Nothing to clean up", "success"];
  return [`Freed ${formatBytes(res.bytes)} (${res.objects.toLocaleString()} object${res.objects === 1 ? "" : "s"})`, "success"];
}

export function StorageCard({ root, busy, notify, onEditSparse }: StorageCardProps) {
  const queryClient = useQueryClient();
  const usage = useQuery({ queryKey: diskUsageKey(root), queryFn: () => desktop.diskUsage(root) });
  const clean = useMutation({
    mutationFn: () => desktop.cleanUp(root),
    onSuccess: (res) => notify(...describeCleanUp(res)),
    onError: (err) => notify(`Clean up failed: ${errorMessage(err)}`),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: diskUsageKey(root) }),
  });

  if (usage.isError) return <p className="text-danger">Could not read the disk usage: {errorMessage(usage.error)}</p>;
  if (!usage.data) return <p className="text-muted-foreground">Measuring disk usage…</p>;
  const u = usage.data;
  const large = u.sparse.length === 0 && u.workingBytes > SPARSE_HINT_BYTES;
  return (
    <div className="space-y-3">
      <h2 className="font-medium">Storage</h2>
      <dl className="grid grid-cols-3 gap-3">
        <Figure label="Project files" value={formatBytes(u.workingBytes)} hint={`${u.workingFiles.toLocaleString()} file${u.workingFiles === 1 ? "" : "s"}`} />
        <Figure label="Local cache" value={formatBytes(u.cacheBytes)} />
        <Figure label="Cached objects" value={u.cacheObjects.toLocaleString()} />
      </dl>
      {u.pins.length > 0 && (
        <div>
          <p className="mb-1 text-muted-foreground">Kept in the cache for your locked files</p>
          <div className="flex flex-wrap gap-1">
            {u.pins.map((p) => (
              <Badge key={p}>{p}</Badge>
            ))}
          </div>
        </div>
      )}
      {large && (
        <p role="note" className="flex flex-wrap items-center gap-1 text-muted-foreground">
          This full checkout is over {formatBytes(SPARSE_HINT_BYTES)}.
          <button type="button" className="inline-flex items-center gap-1 text-accent hover:underline disabled:opacity-50" onClick={onEditSparse} disabled={busy}>
            <FolderTree className="size-3.5" />
            Check out only the folders you need
          </button>
        </p>
      )}
      <Button size="sm" onClick={() => clean.mutate()} disabled={busy || clean.isPending} title="Remove cached content this clone no longer needs">
        <Eraser />
        {clean.isPending ? "Cleaning up…" : "Clean up"}
      </Button>
    </div>
  );
}

function Figure({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium">{value}</dd>
      {hint && <dd className="text-[11px] text-muted-foreground">{hint}</dd>}
    </div>
  );
}
