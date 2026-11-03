import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowDownToLine, FileDiff, GitBranch, History, Info, Lock, Trash2 } from "lucide-react";
import { desktop, type Repo } from "@/api/desktop";
import { ChangesView } from "@/components/changes/ChangesView";
import { Progress } from "@/components/changes/CommitBox";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useRepoOperation } from "@/hooks/useRepoOperation";
import { cn, errorMessage } from "@/lib/utils";

export type Notify = (message: string, tone?: "error" | "success") => void;

type TabId = "changes" | "details";

const tabs: { id: TabId | "history" | "locks"; label: string; Icon: typeof FileDiff; soon?: boolean }[] = [
  { id: "changes", label: "Changes", Icon: FileDiff },
  { id: "history", label: "History", Icon: History, soon: true },
  { id: "locks", label: "Locks", Icon: Lock, soon: true },
  { id: "details", label: "Details", Icon: Info },
];

type RepoViewProps = { repo: Repo; removing: boolean; onRemove: (root: string) => void; notify: Notify };

export function RepoView({ repo, removing, onRemove, notify }: RepoViewProps) {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<TabId>("changes");
  const [message, setMessage] = useState("");

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["changes", repo.root] });
    void queryClient.invalidateQueries({ queryKey: ["diff", repo.root] });
  };
  const operation = useRepoOperation(repo.root, {
    onDone: (op, outcome) => {
      refresh();
      if (op === "push") {
        setMessage("");
        notify(`Pushed to ${repo.branch}${outcome.commitId ? ` (${outcome.commitId})` : ""}`, "success");
      } else {
        void queryClient.invalidateQueries({ queryKey: ["repos"] });
        notify(`Up to date with ${outcome.branch || repo.branch}`, "success");
      }
    },
    onError: (op, err) => {
      refresh();
      notify(`${op === "push" ? "Push" : "Update"} failed: ${errorMessage(err)}`);
    },
    onCancelled: (op) => {
      refresh();
      notify(`${op === "push" ? "Push" : "Update"} cancelled`);
    },
  });
  const op = operation.state;

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
        {!repo.error &&
          (op?.op === "update" ? (
            <div className="no-drag w-64">
              <Progress op={op} onCancel={operation.cancel} />
            </div>
          ) : (
            <Button
              size="sm"
              onClick={() => operation.run("update", () => desktop.update(repo.root))}
              disabled={!!op}
              title="Get the latest version of the branch"
            >
              <ArrowDownToLine />
              Update
            </Button>
          ))}
        <Button variant="danger" size="sm" onClick={() => onRemove(repo.root)} disabled={removing || !!op} title="Remove from list (files stay on disk)">
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
          <nav aria-label="Views" role="tablist" className="flex shrink-0 gap-1 border-b border-border px-4">
            {tabs.map(({ id, label, Icon, soon }) => (
              <button
                key={id}
                type="button"
                role="tab"
                aria-selected={tab === id}
                disabled={soon}
                title={soon ? "Coming soon" : undefined}
                onClick={() => setTab(id as TabId)}
                className={cn(
                  "flex items-center gap-1.5 border-b-2 px-2 py-2 text-[13px]",
                  tab === id ? "border-accent font-medium" : "border-transparent text-muted-foreground hover:text-foreground",
                  soon && "opacity-60 hover:text-muted-foreground",
                )}
              >
                <Icon className="size-3.5" />
                {label}
              </button>
            ))}
          </nav>
          <div className="min-h-0 flex-1">
            {tab === "changes" ? (
              <ChangesView
                root={repo.root}
                op={op}
                message={message}
                onMessage={setMessage}
                onPush={(msg) => operation.run("push", () => desktop.push(repo.root, msg))}
                onCancelOp={operation.cancel}
                onError={(msg) => notify(msg)}
              />
            ) : (
              <Details repo={repo} />
            )}
          </div>
        </>
      )}
    </div>
  );
}

function Details({ repo }: { repo: Repo }) {
  return (
    <dl className="m-5 max-w-xl space-y-3 rounded-lg border border-border bg-card p-4 text-xs">
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
