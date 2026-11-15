import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eraser, FolderTree } from "lucide-react";
import { desktop, type CleanUpResult } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/diff";
import { errorMessage } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { plural, translate , locale } from "@/i18n";

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
    if (res.reason === "pending") return [translate("storageCard.aMergeOrRevertIs"), "error"];
    return [translate("storageCard.repositoryIsBusyTryAgain"), "error"];
  }
  if (res.bytes === 0) return [translate("storageCard.nothingToCleanUp"), "success"];
  return [translate("storageCard.freed", { size: formatBytes(res.bytes), count: res.objects }), "success"];
}

export function StorageCard({ root, busy, notify, onEditSparse }: StorageCardProps) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const usage = useQuery({ queryKey: diskUsageKey(root), queryFn: () => desktop.diskUsage(root) });
  const clean = useMutation({
    mutationFn: () => desktop.cleanUp(root),
    onSuccess: (res) => notify(...describeCleanUp(res)),
    onError: (err) => notify(translate("storageCard.cleanUpFailed", { value: errorMessage(err) })),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: diskUsageKey(root) }),
  });

  if (usage.isError) return <p className="text-danger">{t("storageCard.readFailed", { error: errorMessage(usage.error) })}</p>;
  if (!usage.data) return <p className="text-muted-foreground">{t("storageCard.measuringDiskUsage")}</p>;
  const u = usage.data;
  const large = u.sparse.length === 0 && u.workingBytes > SPARSE_HINT_BYTES;
  return (
    <div className="space-y-3">
      <h2 className="font-medium">{t("storageCard.storage")}</h2>
      <dl className="grid grid-cols-3 gap-3">
        <Figure label={t("storageCard.projectFiles")} value={formatBytes(u.workingBytes)} hint={plural(u.workingFiles, "file")} />
        <Figure label={t("storageCard.localCache")} value={formatBytes(u.cacheBytes)} />
        <Figure label={t("storageCard.cachedObjects")} value={u.cacheObjects.toLocaleString(locale())} />
      </dl>
      {u.pins.length > 0 && (
        <div>
          <p className="mb-1 text-muted-foreground">{t("storageCard.keptInTheCacheFor")}</p>
          <div className="flex flex-wrap gap-1">
            {u.pins.map((p) => (
              <Badge key={p}>{p}</Badge>
            ))}
          </div>
        </div>
      )}
      {large && (
        <p role="note" className="flex flex-wrap items-center gap-1 text-muted-foreground">
          {t("storageCard.largeCheckout", { size: formatBytes(SPARSE_HINT_BYTES) })}
          <button type="button" className="inline-flex items-center gap-1 text-accent hover:underline disabled:opacity-50" onClick={onEditSparse} disabled={busy}>
            <FolderTree className="size-3.5" />
            {t("storageCard.checkOutOnlyTheFolders")}
          </button>
        </p>
      )}
      <Button size="sm" onClick={() => clean.mutate()} disabled={busy || clean.isPending} title={t("storageCard.removeCachedContentThisClone")}>
        <Eraser />
        {clean.isPending ? translate("storageCard.cleaningUp") : translate("storageCard.cleanUp")}
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
