import { useEffect, useRef, useState } from "react";
import { FolderX } from "lucide-react";
import { useTranslation } from "react-i18next";
import { desktop, type ChangeSet, type GeneratedFolder } from "@/api/desktop";
import { Button } from "@/components/ui/button";

const MAX_LISTED = 5;

export function untrackedPaths(changes: ChangeSet | undefined, paths: string[]): string[] {
  if (!changes) return [];
  const untracked = new Set(changes.unstaged.filter((f) => f.status === "untracked").map((f) => f.path));
  return paths.filter((p) => untracked.has(p));
}

export function outsideFolders(paths: string[], folders: GeneratedFolder[]): string[] {
  return paths.filter((p) => !folders.some((f) => p === f.dir || p.startsWith(f.dir + "/")));
}

// findGeneratedFolders names the folders among the new paths that look like
// build output or installed dependencies. A failed lookup never blocks staging.
export async function findGeneratedFolders(root: string, changes: ChangeSet | undefined, paths: string[]): Promise<GeneratedFolder[]> {
  const untracked = untrackedPaths(changes, paths);
  if (untracked.length === 0) return [];
  try {
    return await desktop.generatedFolders(root, untracked);
  } catch {
    return [];
  }
}

type Props = {
  root: string;
  folders: GeneratedFolder[];
  onCancel: () => void;
  onStageAnyway: () => void;
  onIgnored: () => void;
  onError: (message: string) => void;
};

export function GeneratedFoldersDialog({ root, folders, onCancel, onStageAnyway, onIgnored, onError }: Props) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const cancelRef = useRef<HTMLButtonElement>(null);
  useEffect(() => cancelRef.current?.focus(), []);
  const files = folders.reduce((n, f) => n + f.files, 0);

  const ignore = async () => {
    setBusy(true);
    try {
      await desktop.ignoreFolders(root, folders.map((f) => f.pattern));
      onIgnored();
    } catch (err) {
      onError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onCancel()}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="generated-title" className="w-full max-w-md space-y-3 rounded-lg border border-border bg-card p-5 shadow-xl">
        <h2 id="generated-title" className="flex items-center gap-1.5 text-sm font-semibold">
          <FolderX className="size-4 text-warning" />
          {t("generatedFolders.title", { count: folders.length })}
        </h2>
        <p className="text-xs text-muted-foreground">{t("generatedFolders.message", { count: files })}</p>
        <ul className="space-y-1 rounded-md border border-border bg-background px-3 py-2 text-xs">
          {folders.slice(0, MAX_LISTED).map((f) => (
            <li key={f.dir} className="flex justify-between gap-3">
              <span className="truncate font-mono">{f.dir}/</span>
              <span className="shrink-0 text-muted-foreground">{t("generatedFolders.files", { count: f.files })}</span>
            </li>
          ))}
          {folders.length > MAX_LISTED && <li className="text-muted-foreground">{t("generatedFolders.more", { count: folders.length - MAX_LISTED })}</li>}
        </ul>
        <p className="text-[11px] text-muted-foreground">{t("generatedFolders.ignoreHint")}</p>
        <div className="flex justify-end gap-2 pt-1">
          <Button ref={cancelRef} size="sm" disabled={busy} onClick={onCancel}>
            {t("confirm.cancel")}
          </Button>
          <Button size="sm" disabled={busy} onClick={onStageAnyway}>
            {t("generatedFolders.stageAnyway")}
          </Button>
          <Button size="sm" variant="primary" disabled={busy} onClick={() => void ignore()}>
            {t("generatedFolders.ignore")}
          </Button>
        </div>
      </div>
    </div>
  );
}
