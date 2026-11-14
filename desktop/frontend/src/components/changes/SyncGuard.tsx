import { useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { desktop } from "@/api/desktop";
import { Button } from "@/components/ui/button";

export type SyncAction = {
  target: { branch?: string; tag?: string };
  verb: string;
  shelfName: string;
  start: () => void;
};

type Pending = SyncAction & { files: string[] };

const SHOWN = 8;

// useSyncGuard checks, before an update, switch or tag checkout, which local
// changes it would replace. When there are some it asks first and offers to
// shelve exactly those files and then go on. A failed check never blocks;
// hasChanges undefined (not read yet) always checks.
export function useSyncGuard(root: string, hasChanges: boolean | undefined, shelveThen: (paths: string[], name: string, next: () => void) => void) {
  const [pending, setPending] = useState<Pending | null>(null);
  const [checking, setChecking] = useState(false);
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  const guard = useCallback(
    async (action: SyncAction) => {
      if (hasChanges === false) return action.start();
      setChecking(true);
      let files: string[] = [];
      try {
        files = await desktop.syncPlan(root, action.target);
      } catch {
        files = [];
      }
      if (!live.current) return;
      setChecking(false);
      if (files.length === 0) return action.start();
      setPending({ ...action, files });
    },
    [root, hasChanges],
  );

  const dialog = pending && (
    <SyncGuardDialog
      pending={pending}
      onCancel={() => setPending(null)}
      onContinue={() => {
        setPending(null);
        pending.start();
      }}
      onShelve={() => {
        setPending(null);
        shelveThen(pending.files, pending.shelfName, pending.start);
      }}
    />
  );
  return { guard, checking, dialog };
}

function SyncGuardDialog({ pending, onCancel, onContinue, onShelve }: { pending: Pending; onCancel: () => void; onContinue: () => void; onShelve: () => void }) {
  const { files, verb } = pending;
  const more = files.length - SHOWN;
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onCancel()}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="sync-guard-title" className="w-full max-w-md space-y-3 rounded-lg border border-border bg-card p-5 shadow-xl">
        <h2 id="sync-guard-title" className="flex items-center gap-2 text-sm font-semibold">
          <AlertTriangle className="size-4 text-warning" />
          Your changes would be replaced
        </h2>
        <p className="text-xs text-muted-foreground">
          {verb} replaces your local changes to {files.length} file{files.length === 1 ? "" : "s"}. Shelve them to keep a copy you can apply later.
        </p>
        <ul aria-label="Changes that would be replaced" className="max-h-40 overflow-y-auto rounded-md border border-border px-2 py-1 font-mono text-[11px]">
          {files.slice(0, SHOWN).map((f) => (
            <li key={f} className="truncate" title={f}>
              {f}
            </li>
          ))}
          {more > 0 && <li className="text-muted-foreground">and {more} more</li>}
        </ul>
        <div className="flex flex-wrap justify-end gap-2 pt-1">
          <Button size="sm" onClick={onCancel} autoFocus>
            Cancel
          </Button>
          <Button size="sm" variant="ghost" className="text-danger" onClick={onContinue}>
            Continue anyway
          </Button>
          <Button size="sm" variant="primary" onClick={onShelve}>
            Shelve and continue
          </Button>
        </div>
      </div>
    </div>
  );
}
