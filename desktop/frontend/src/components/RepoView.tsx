import { AlertTriangle, FileDiff, GitBranch, History, Lock, Trash2 } from "lucide-react";
import type { Repo } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

const tabs = [
  { id: "changes", label: "Changes", Icon: FileDiff },
  { id: "history", label: "History", Icon: History },
  { id: "locks", label: "Locks", Icon: Lock },
];

type RepoViewProps = { repo: Repo; removing: boolean; onRemove: (root: string) => void };

export function RepoView({ repo, removing, onRemove }: RepoViewProps) {
  return (
    <div className="flex h-full flex-col">
      <header className="drag-region flex h-12 shrink-0 items-center gap-3 border-b border-border px-5">
        <h1 className="truncate text-sm font-semibold">{repo.name}</h1>
        {!repo.error && (
          <Badge>
            <GitBranch />
            {repo.branch}
          </Badge>
        )}
        <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={repo.root}>
          {repo.root}
        </span>
        <Button variant="danger" size="sm" onClick={() => onRemove(repo.root)} disabled={removing} title="Remove from list (files stay on disk)">
          <Trash2 />
          Remove
        </Button>
      </header>

      {repo.error ? (
        <div className="m-5 flex items-start gap-3 rounded-lg border border-border bg-danger-soft p-4" role="alert">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-danger" />
          <div className="space-y-1">
            <p className="text-sm font-medium">This clone is unavailable</p>
            <p className="text-xs text-muted-foreground">{repo.error}</p>
            <p className="text-xs text-muted-foreground">It may have been moved or deleted. Remove it from the list or restore the folder.</p>
          </div>
        </div>
      ) : (
        <>
          <nav aria-label="Views" className="flex shrink-0 gap-1 border-b border-border px-4">
            {tabs.map(({ id, label, Icon }, i) => (
              <span
                key={id}
                aria-disabled={i > 0}
                className={
                  i === 0
                    ? "flex items-center gap-1.5 border-b-2 border-accent px-2 py-2 text-[13px] font-medium"
                    : "flex items-center gap-1.5 border-b-2 border-transparent px-2 py-2 text-[13px] text-muted-foreground opacity-60"
                }
              >
                <Icon className="size-3.5" />
                {label}
              </span>
            ))}
          </nav>
          <section className="grid gap-4 p-5 lg:grid-cols-[2fr_1fr]">
            <div className="flex min-h-56 flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border bg-card p-8 text-center">
              <FileDiff className="size-6 text-muted-foreground" />
              <p className="text-sm font-medium">Changes view is on its way</p>
              <p className="max-w-xs text-xs text-muted-foreground">Edited files, visual asset diffs and push will appear here.</p>
            </div>
            <dl className="space-y-3 rounded-lg border border-border bg-card p-4 text-xs">
              <Detail label="Server" value={repo.url} />
              <Detail label="Branch" value={repo.branch} />
              <Detail label="Location" value={repo.root} />
              <div>
                <dt className="mb-1 text-muted-foreground">Checkout</dt>
                <dd className="flex flex-wrap gap-1">
                  {repo.sparse && repo.sparse.length > 0 ? repo.sparse.map((p) => <Badge key={p}>{p}</Badge>) : <span>Full checkout</span>}
                </dd>
              </div>
            </dl>
          </section>
        </>
      )}
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="mb-0.5 text-muted-foreground">{label}</dt>
      <dd className="select-text break-all font-mono text-[11px]">{value}</dd>
    </div>
  );
}
