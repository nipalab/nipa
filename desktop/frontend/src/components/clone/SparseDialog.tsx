import { useState } from "react";
import { X } from "lucide-react";
import { desktop, type Repo } from "@/api/desktop";
import { SparsePicker } from "@/components/clone/SparsePicker";
import { Button } from "@/components/ui/button";
import { normalizeSelection, parseRepoUrl } from "@/lib/sparse";

type SparseDialogProps = { repo: Repo; onClose: () => void; onApply: (paths: string[]) => void };

const sameSet = (a: string[], b: string[]) => a.length === b.length && a.every((p, i) => p === b[i]);

// SparseDialog edits which folders an existing clone downloads.
export function SparseDialog({ repo, onClose, onApply }: SparseDialogProps) {
  const initial = repo.sparse && repo.sparse.length > 0 ? normalizeSelection(repo.sparse) : null;
  const [value, setValue] = useState<string[] | null>(initial);
  const address = parseRepoUrl(repo.url);
  const next = value === null ? [] : normalizeSelection(value);
  const unchanged = sameSet(next, initial ?? []);
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onClose()}>
      <div role="dialog" aria-modal="true" aria-labelledby="sparse-title" className="w-full max-w-lg space-y-4 rounded-lg border border-border bg-card p-5 shadow-xl">
        <header className="flex items-center">
          <h2 id="sparse-title" className="flex-1 text-sm font-semibold">
            Folders in this clone
          </h2>
          <button type="button" aria-label="Close" onClick={onClose} className="text-muted-foreground hover:text-foreground">
            <X className="size-4" />
          </button>
        </header>
        {address ? (
          <SparsePicker
            load={(path) => desktop.remoteTree(address.server, address.org, address.project, repo.branch, path)}
            cacheKey={["remote", address.server, address.org, address.project, repo.branch]}
            value={value}
            onChange={setValue}
          />
        ) : (
          <p className="text-[12px] text-danger">This clone's server address cannot be read: {repo.url}</p>
        )}
        <p className="text-[11px] text-muted-foreground">Files of folders you remove are deleted from this clone, not from the server. Local changes in them are kept only if they are pushed first.</p>
        <div className="flex justify-end gap-2">
          <Button size="sm" onClick={onClose} autoFocus>
            Cancel
          </Button>
          <Button variant="primary" size="sm" disabled={!address || unchanged || (value !== null && next.length === 0)} onClick={() => onApply(next)}>
            Apply
          </Button>
        </div>
      </div>
    </div>
  );
}
