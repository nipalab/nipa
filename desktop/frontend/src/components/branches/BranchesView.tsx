import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowRightLeft, GitBranch, GitMerge, GitBranchPlus, Loader2, Plus, RefreshCw, Shield, Tag as TagIcon, Trash2 } from "lucide-react";
import { desktop, type Branch, type Tag } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { PromptDialog } from "@/components/ui/prompt";
import type { Notify } from "@/hooks/useLocks";
import { shortId } from "@/lib/history";
import { timeAgo } from "@/lib/time";
import { errorMessage } from "@/lib/utils";

type BranchesViewProps = {
  root: string;
  branch: string;
  detached?: string;
  busy: boolean;
  notify: Notify;
  onSwitch: (branch: string) => void;
  onMerge: (branch: string) => void;
  onCheckoutTag: (tag: string) => void;
};

type Dialog = { kind: "new-branch" } | { kind: "merge"; name: string } | { kind: "new-tag" } | { kind: "delete-branch"; name: string } | { kind: "delete-tag"; name: string } | null;

export function BranchesView({ root, branch, detached, busy, notify, onSwitch, onMerge, onCheckoutTag }: BranchesViewProps) {
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<Dialog>(null);
  const branches = useQuery({ queryKey: ["branches", root], queryFn: () => desktop.branches(root) });
  const tags = useQuery({ queryKey: ["tags", root], queryFn: () => desktop.tags(root) });

  const baseHead = branches.data?.find((b) => b.name === branch)?.commitId;
  const baseLabel = baseHead ? `${branch} (${shortId(baseHead)})` : branch;

  const done = (key: string, message: string) => {
    setDialog(null);
    void queryClient.invalidateQueries({ queryKey: [key, root] });
    notify(message, "success");
  };
  const createBranch = useMutation({
    mutationFn: ({ name }: { name: string; switchTo: boolean }) => desktop.createBranch(root, name, { branch }),
    onSuccess: (b, vars) => {
      done("branches", `Created branch ${b.name} from ${branch}`);
      if (vars.switchTo) onSwitch(b.name);
    },
    onError: (err) => notify(`Could not create the branch: ${errorMessage(err)}`),
  });
  const deleteBranch = useMutation({
    mutationFn: (name: string) => desktop.deleteBranch(root, name),
    onSuccess: (_, name) => done("branches", `Deleted branch ${name}`),
    onError: (err) => notify(`Could not delete the branch: ${errorMessage(err)}`),
  });
  const createTag = useMutation({
    mutationFn: ({ name, message }: { name: string; message: string }) => desktop.createTag(root, name, { branch }, message),
    onSuccess: (t) => done("tags", `Tagged the head of ${branch} as ${t.name}`),
    onError: (err) => notify(`Could not create the tag: ${errorMessage(err)}`),
  });
  const deleteTag = useMutation({
    mutationFn: (name: string) => desktop.deleteTag(root, name),
    onSuccess: (_, name) => done("tags", `Deleted tag ${name}`),
    onError: (err) => notify(`Could not delete the tag: ${errorMessage(err)}`),
  });

  return (
    <div className="h-full overflow-auto">
      <div className="mx-auto max-w-3xl space-y-6 p-5">
        <Section
          title="Branches"
          Icon={GitBranch}
          action={
            <Button size="sm" onClick={() => setDialog({ kind: "new-branch" })} disabled={busy || !!detached} title={detached ? "Switch back to a branch first" : `Start a branch from ${branch}`}>
              <GitBranchPlus />
              New branch
            </Button>
          }
          query={branches}
          empty="No branches"
          render={(list: Branch[]) => (
            <ul className="divide-y divide-border">
              {list.map((b) => {
                const current = b.name === branch && !detached;
                return (
                  <li key={b.name} className="flex items-center gap-2 px-3 py-2 text-[12px]">
                    <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 truncate font-medium" title={b.name}>
                      {b.name}
                    </span>
                    {current && <Badge className="text-accent">Current</Badge>}
                    {b.default && <Badge>Default</Badge>}
                    {b.protected && (
                      <Badge title="Protected: changes land through merge requests">
                        <Shield />
                        Protected
                      </Badge>
                    )}
                    <span className="ml-auto font-mono text-[11px] text-muted-foreground">{b.commitId ? shortId(b.commitId) : "empty"}</span>
                    {!current && !detached && b.commitId && (
                      <Button size="sm" disabled={busy} onClick={() => setDialog({ kind: "merge", name: b.name })} title={`Bring the changes of ${b.name} into ${branch}`}>
                        <GitMerge />
                        Merge into {branch}
                      </Button>
                    )}
                    {!current && (
                      <Button size="sm" disabled={busy} onClick={() => onSwitch(b.name)} title={`Check out ${b.name} in this clone`}>
                        <ArrowRightLeft />
                        Switch
                      </Button>
                    )}
                    {!b.default && b.name !== branch && (
                      <Button size="sm" variant="ghost" disabled={busy || deleteBranch.isPending} aria-label={`Delete branch ${b.name}`} onClick={() => setDialog({ kind: "delete-branch", name: b.name })}>
                        <Trash2 />
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        />
        <Section
          title="Tags"
          Icon={TagIcon}
          action={
            <Button size="sm" onClick={() => setDialog({ kind: "new-tag" })} disabled={busy || !!detached} title={detached ? "Switch back to a branch first" : `Tag the head of ${branch}`}>
              <Plus />
              New tag
            </Button>
          }
          query={tags}
          empty="No tags yet. Tag a commit to mark a release or milestone."
          render={(list: Tag[]) => (
            <ul className="divide-y divide-border">
              {list.map((t) => (
                <li key={t.name} className="flex items-center gap-2 px-3 py-2 text-[12px]">
                  <TagIcon className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 shrink-0 font-medium">{t.name}</span>
                  {detached === t.name && <Badge className="text-accent">Checked out</Badge>}
                  <span className="min-w-0 flex-1 truncate text-muted-foreground" title={t.message}>
                    {t.message}
                  </span>
                  <span className="shrink-0 text-[11px] text-muted-foreground" title={t.createdAt}>
                    {timeAgo(t.createdAt)}
                  </span>
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{shortId(t.commitId)}</span>
                  {detached !== t.name && (
                    <Button size="sm" disabled={busy} onClick={() => onCheckoutTag(t.name)} title="Check out the tagged files read-only">
                      Check out
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" disabled={busy || deleteTag.isPending} aria-label={`Delete tag ${t.name}`} onClick={() => setDialog({ kind: "delete-tag", name: t.name })}>
                    <Trash2 />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        />
      </div>
      {dialog?.kind === "new-branch" && (
        <PromptDialog
          title="New branch"
          label="Branch name"
          placeholder="feature/new-level"
          base={{ label: "Based on", value: baseLabel }}
          note="The branch starts at the current head of that branch on the server, not at unpushed local changes."
          option={{ label: "Switch to the new branch", defaultChecked: true }}
          confirmLabel="Create branch"
          busy={createBranch.isPending}
          onCancel={() => setDialog(null)}
          onConfirm={(name, _message, switchTo) => createBranch.mutate({ name, switchTo })}
        />
      )}
      {dialog?.kind === "new-tag" && (
        <PromptDialog
          title="New tag"
          label="Tag name"
          placeholder="v1.2.0"
          note={`Tags the head of ${branch} on the server. To tag an older commit, use Tag… in History.`}
          confirmLabel="Create tag"
          withMessage
          busy={createTag.isPending}
          onCancel={() => setDialog(null)}
          onConfirm={(name, message) => createTag.mutate({ name, message })}
        />
      )}
      {dialog?.kind === "merge" && (
        <ConfirmDialog
          title={`Merge ${dialog.name} into ${branch}?`}
          message={`The merge runs in this clone and needs no staged changes. Without conflicts the merge commit is pushed to ${branch} right away; with conflicts it waits until you resolve them.`}
          confirmLabel="Merge"
          onCancel={() => setDialog(null)}
          onConfirm={() => {
            setDialog(null);
            onMerge(dialog.name);
          }}
        />
      )}
      {dialog?.kind === "delete-branch" && (
        <ConfirmDialog
          title={`Delete branch ${dialog.name}?`}
          message="The branch is removed from the server for everyone. Commits that were only on it can no longer be reached by name."
          confirmLabel="Delete"
          onCancel={() => setDialog(null)}
          onConfirm={() => deleteBranch.mutate(dialog.name)}
        />
      )}
      {dialog?.kind === "delete-tag" && (
        <ConfirmDialog
          title={`Delete tag ${dialog.name}?`}
          message="The tag is removed from the server for everyone. The commit it points at stays in the history."
          confirmLabel="Delete"
          onCancel={() => setDialog(null)}
          onConfirm={() => deleteTag.mutate(dialog.name)}
        />
      )}
    </div>
  );
}

type SectionQuery<T> = { data?: T[]; isPending: boolean; isError: boolean; error: unknown; isFetching: boolean; refetch: () => unknown };

function Section<T>({ title, Icon, action, query, empty, render }: { title: string; Icon: typeof GitBranch; action: ReactNode; query: SectionQuery<T>; empty: string; render: (list: T[]) => ReactNode }) {
  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex items-center gap-2 border-b border-border px-3 py-2">
        <Icon className="size-3.5 text-muted-foreground" />
        <h2 className="text-[13px] font-semibold">{title}</h2>
        {query.data && <span className="text-[11px] text-muted-foreground">{query.data.length}</span>}
        <span className="ml-auto flex gap-2">
          <Button size="sm" variant="ghost" aria-label={`Refresh ${title.toLowerCase()}`} disabled={query.isFetching} onClick={() => void query.refetch()}>
            <RefreshCw className={query.isFetching ? "animate-spin" : undefined} />
          </Button>
          {action}
        </span>
      </header>
      {query.isPending ? (
        <p className="flex items-center gap-2 px-3 py-4 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" /> Loading…
        </p>
      ) : query.isError ? (
        <p className="flex items-center gap-2 px-3 py-4 text-xs text-danger">
          <AlertTriangle className="size-3.5" /> {errorMessage(query.error)}
        </p>
      ) : (query.data ?? []).length === 0 ? (
        <p className="px-3 py-4 text-xs text-muted-foreground">{empty}</p>
      ) : (
        render(query.data ?? [])
      )}
    </section>
  );
}
