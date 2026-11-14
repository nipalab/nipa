import { useMemo, useState, type FormEvent } from "react";
import type { ChangeSet, FileChange } from "@/api/desktop";
import { FileIcon, StatusLetter } from "@/components/changes/FileIcon";
import { Button } from "@/components/ui/button";

type ShelveDialogProps = {
  changes: ChangeSet;
  initial?: string[];
  busy?: boolean;
  onConfirm: (input: { name: string; paths: string[] }) => void;
  onCancel: () => void;
};

export function shelvable(changes: ChangeSet): FileChange[] {
  const seen = new Set<string>();
  return [...changes.staged, ...changes.unstaged].filter((f) => !seen.has(f.path) && seen.add(f.path));
}

// ShelveDialog picks the changed files to put aside; the checked files go
// back to the last synced version once the shelf is written.
export function ShelveDialog({ changes, initial, busy, onConfirm, onCancel }: ShelveDialogProps) {
  const files = useMemo(() => shelvable(changes), [changes]);
  const [name, setName] = useState("");
  const [checked, setChecked] = useState<Set<string>>(() => new Set(initial && initial.length > 0 ? initial : files.map((f) => f.path)));
  const picked = files.filter((f) => checked.has(f.path));
  const all = picked.length === files.length;
  const toggle = (path: string) =>
    setChecked((prev) => {
      const next = new Set(prev);
      if (!next.delete(path)) next.add(path);
      return next;
    });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (picked.length > 0) onConfirm({ name: name.trim(), paths: picked.map((f) => f.path) });
  };
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onCancel()}>
      <form role="dialog" aria-modal="true" aria-labelledby="shelve-title" onSubmit={submit} className="flex max-h-[80vh] w-full max-w-md flex-col gap-3 rounded-lg border border-border bg-card p-5 shadow-xl">
        <h2 id="shelve-title" className="text-sm font-semibold">
          Shelve changes
        </h2>
        <p className="text-xs text-muted-foreground">The checked files go back to the last synced version. Apply the shelf later to bring your changes back. Shelves stay on this computer.</p>
        <label className="block space-y-1 text-[12px]">
          <span className="text-muted-foreground">Name (optional)</span>
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={`${picked.length} file${picked.length === 1 ? "" : "s"} on ${changes.branch}`}
            className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-[12px] outline-none focus:border-accent"
          />
        </label>
        <div className="flex items-center justify-between text-[11px] text-muted-foreground">
          <span>
            {picked.length} of {files.length} selected
          </span>
          <button type="button" className="text-accent hover:underline" onClick={() => setChecked(new Set(all ? [] : files.map((f) => f.path)))}>
            {all ? "Select none" : "Select all"}
          </button>
        </div>
        <ul aria-label="Files to shelve" className="min-h-0 flex-1 overflow-y-auto rounded-md border border-border">
          {files.map((f) => (
            <li key={f.path}>
              <label className="flex cursor-default items-center gap-2 px-2 py-1 text-[12px] hover:bg-muted" title={f.path}>
                <input type="checkbox" checked={checked.has(f.path)} onChange={() => toggle(f.path)} className="size-3.5 shrink-0 accent-[var(--accent)]" />
                <FileIcon kind={f.kind} />
                <span className="min-w-0 flex-1 truncate">{f.path}</span>
                <StatusLetter status={f.status} />
              </label>
            </li>
          ))}
        </ul>
        <div className="flex justify-end gap-2 pt-1">
          <Button type="button" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={picked.length === 0 || busy}>
            Shelve {picked.length} file{picked.length === 1 ? "" : "s"}
          </Button>
        </div>
      </form>
    </div>
  );
}
