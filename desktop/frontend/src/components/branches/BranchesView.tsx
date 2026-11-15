import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowRightLeft, GitBranch, GitMerge, GitBranchPlus, Loader2, Plus, RefreshCw, Shield, Tag as TagIcon, Trash2 } from "lucide-react";
import { desktop, type Branch, type Tag } from "@/api/desktop";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { PromptDialog } from "@/components/ui/prompt";
import type { Notify } from "@/hooks/useLocks";
import { useMaintenance } from "@/hooks/useMaintenance";
import { shortId } from "@/lib/history";
import { timeAgo } from "@/lib/time";
import { errorMessage } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { translate } from "@/i18n";

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
  const { t } = useTranslation();
  const { writes, sync } = useMaintenance();
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<Dialog>(null);
  const branches = useQuery({ queryKey: ["branches", root], queryFn: () => desktop.branches(root) });
  const tags = useQuery({ queryKey: ["tags", root], queryFn: () => desktop.tags(root) });

  const baseHead = branches.data?.find((b) => b.name === branch)?.commitId;
  const baseLabel = baseHead ? translate("branchesView.text", { branch, value: shortId(baseHead) }) : branch;

  const done = (key: string, message: string) => {
    setDialog(null);
    void queryClient.invalidateQueries({ queryKey: [key, root] });
    notify(message, "success");
  };
  const createBranch = useMutation({
    mutationFn: ({ name }: { name: string; switchTo: boolean }) => desktop.createBranch(root, name, { branch }),
    onSuccess: (b, vars) => {
      done("branches", translate("branchesView.createdBranchFrom", { name: b.name, branch }));
      if (vars.switchTo) onSwitch(b.name);
    },
    onError: (err) => notify(translate("branchesView.couldNotCreateTheBranch", { value: errorMessage(err) })),
  });
  const deleteBranch = useMutation({
    mutationFn: (name: string) => desktop.deleteBranch(root, name),
    onSuccess: (_, name) => done("branches", translate("branchesView.deletedBranch", { name })),
    onError: (err) => notify(translate("branchesView.couldNotDeleteTheBranch", { value: errorMessage(err) })),
  });
  const createTag = useMutation({
    mutationFn: ({ name, message }: { name: string; message: string }) => desktop.createTag(root, name, { branch }, message),
    onSuccess: (t) => done("tags", translate("branchesView.taggedTheHeadOfAs", { branch, name: t.name })),
    onError: (err) => notify(translate("branchesView.couldNotCreateTheTag", { value: errorMessage(err) })),
  });
  const deleteTag = useMutation({
    mutationFn: (name: string) => desktop.deleteTag(root, name),
    onSuccess: (_, name) => done("tags", translate("branchesView.deletedTag", { name })),
    onError: (err) => notify(translate("branchesView.couldNotDeleteTheTag", { value: errorMessage(err) })),
  });

  return (
    <div className="h-full overflow-auto">
      <div className="mx-auto max-w-3xl space-y-6 p-5">
        <Section
          title={t("branchesView.branches")}
          Icon={GitBranch}
          action={
            <Button size="sm" onClick={() => setDialog({ kind: "new-branch" })} disabled={busy || !!detached || !!writes} title={writes ?? (detached ? translate("branchesView.switchBackToABranch") : translate("branchesView.startABranchFrom", { branch }))}>
              <GitBranchPlus />
              {t("branchesView.newBranch")}
            </Button>
          }
          query={branches}
          empty={translate("branchesView.noBranches")}
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
                    {current && <Badge className="text-accent">{t("branchesView.current")}</Badge>}
                    {b.default && <Badge>{t("branchesView.default")}</Badge>}
                    {b.protected && (
                      <Badge title={t("branchesView.protectedChangesLandThroughMerge")}>
                        <Shield />
                        {t("branchesView.protected")}
                      </Badge>
                    )}
                    <span className="ml-auto font-mono text-[11px] text-muted-foreground">{b.commitId ? shortId(b.commitId) : "empty"}</span>
                    {!current && !detached && b.commitId && (
                      <Button size="sm" disabled={busy || !!writes} onClick={() => setDialog({ kind: "merge", name: b.name })} title={writes ?? t("branchesView.mergeIntoTitle", { name: b.name, branch })}>
                        <GitMerge />
                        {t("branchesView.mergeInto", { branch })}
                      </Button>
                    )}
                    {!current && (
                      <Button size="sm" disabled={busy || !!sync} onClick={() => onSwitch(b.name)} title={sync ?? translate("branchesView.checkOutInThisClone", { name: b.name })}>
                        <ArrowRightLeft />
                        {t("branchesView.switch")}
                      </Button>
                    )}
                    {!b.default && b.name !== branch && (
                      <Button size="sm" variant="ghost" disabled={busy || deleteBranch.isPending || !!writes} title={writes} aria-label={translate("branchesView.deleteBranch", { name: b.name })} onClick={() => setDialog({ kind: "delete-branch", name: b.name })}>
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
          title={t("branchesView.tags")}
          Icon={TagIcon}
          action={
            <Button size="sm" onClick={() => setDialog({ kind: "new-tag" })} disabled={busy || !!detached || !!writes} title={writes ?? (detached ? translate("branchesView.switchBackToABranch") : translate("branchesView.tagTheHeadOf", { branch }))}>
              <Plus />
              {t("branchesView.newTag")}
            </Button>
          }
          query={tags}
          empty={translate("branchesView.noTagsYetTagA")}
          render={(list: Tag[]) => (
            <ul className="divide-y divide-border">
              {list.map((tag) => (
                <li key={tag.name} className="flex items-center gap-2 px-3 py-2 text-[12px]">
                  <TagIcon className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 shrink-0 font-medium">{tag.name}</span>
                  {detached === tag.name && <Badge className="text-accent">{t("branchesView.checkedOut")}</Badge>}
                  <span className="min-w-0 flex-1 truncate text-muted-foreground" title={tag.message}>
                    {tag.message}
                  </span>
                  <span className="shrink-0 text-[11px] text-muted-foreground" title={tag.createdAt}>
                    {timeAgo(tag.createdAt)}
                  </span>
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{shortId(tag.commitId)}</span>
                  {detached !== tag.name && (
                    <Button size="sm" disabled={busy || !!sync} onClick={() => onCheckoutTag(tag.name)} title={sync ?? t("branchesView.checkOutTheTaggedFiles")}>
                      {t("branchesView.checkOut")}
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" disabled={busy || deleteTag.isPending || !!writes} title={writes} aria-label={translate("branchesView.deleteTag", { name: tag.name })} onClick={() => setDialog({ kind: "delete-tag", name: tag.name })}>
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
          title={t("branchesView.newBranch")}
          label={t("branchesView.branchName")}
          placeholder={t("branchesView.featureNewLevel")}
          base={{ label: translate("branchesView.basedOn"), value: baseLabel }}
          note={translate("branchesView.theBranchStartsAtThe")}
          option={{ label: translate("branchesView.switchToTheNewBranch"), defaultChecked: true }}
          confirmLabel={translate("branchesView.createBranch")}
          busy={createBranch.isPending}
          onCancel={() => setDialog(null)}
          onConfirm={(name, _message, switchTo) => createBranch.mutate({ name, switchTo })}
        />
      )}
      {dialog?.kind === "new-tag" && (
        <PromptDialog
          title={t("branchesView.newTag")}
          label={t("branchesView.tagName")}
          placeholder="v1.2.0"
          note={translate("branchesView.tagsTheHeadOfOn", { branch })}
          confirmLabel={translate("branchesView.createTag")}
          withMessage
          busy={createTag.isPending}
          onCancel={() => setDialog(null)}
          onConfirm={(name, message) => createTag.mutate({ name, message })}
        />
      )}
      {dialog?.kind === "merge" && (
        <ConfirmDialog
          title={translate("branchesView.mergeInto2", { name: dialog.name, branch })}
          message={translate("branchesView.theMergeRunsInThis", { branch })}
          confirmLabel={translate("branchesView.merge")}
          onCancel={() => setDialog(null)}
          onConfirm={() => {
            setDialog(null);
            onMerge(dialog.name);
          }}
        />
      )}
      {dialog?.kind === "delete-branch" && (
        <ConfirmDialog
          title={translate("branchesView.deleteBranch2", { name: dialog.name })}
          message={translate("branchesView.theBranchIsRemovedFrom")}
          confirmLabel={translate("branchesView.delete")}
          onCancel={() => setDialog(null)}
          onConfirm={() => deleteBranch.mutate(dialog.name)}
        />
      )}
      {dialog?.kind === "delete-tag" && (
        <ConfirmDialog
          title={translate("branchesView.deleteTag2", { name: dialog.name })}
          message={translate("branchesView.theTagIsRemovedFrom")}
          confirmLabel={translate("branchesView.delete")}
          onCancel={() => setDialog(null)}
          onConfirm={() => deleteTag.mutate(dialog.name)}
        />
      )}
    </div>
  );
}

type SectionQuery<T> = { data?: T[]; isPending: boolean; isError: boolean; error: unknown; isFetching: boolean; refetch: () => unknown };

function Section<T>({ title, Icon, action, query, empty, render }: { title: string; Icon: typeof GitBranch; action: ReactNode; query: SectionQuery<T>; empty: string; render: (list: T[]) => ReactNode }) {
  const { t } = useTranslation();
  return (
    <section className="rounded-lg border border-border bg-card">
      <header className="flex items-center gap-2 border-b border-border px-3 py-2">
        <Icon className="size-3.5 text-muted-foreground" />
        <h2 className="text-[13px] font-semibold">{title}</h2>
        {query.data && <span className="text-[11px] text-muted-foreground">{query.data.length}</span>}
        <span className="ml-auto flex gap-2">
          <Button size="sm" variant="ghost" aria-label={translate("branchesView.refresh", { toLowerCase: title.toLowerCase() })} disabled={query.isFetching} onClick={() => void query.refetch()}>
            <RefreshCw className={query.isFetching ? "animate-spin" : undefined} />
          </Button>
          {action}
        </span>
      </header>
      {query.isPending ? (
        <p className="flex items-center gap-2 px-3 py-4 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" /> {t("branchesView.loading")}
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
