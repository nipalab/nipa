import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, CircleDot, GitMerge, Loader2 } from "lucide-react";
import { desktop, type ConflictFile, type ConflictSet, type ResolveChoice } from "@/api/desktop";
import { DiffViewer } from "@/components/changes/DiffViewer";
import { FileIcon } from "@/components/changes/FileIcon";
import type { DiffLayout } from "@/components/changes/TextDiff";
import { Centered, Mono, ResolverHeader } from "@/components/conflicts/parts";
import { TextResolver } from "@/components/conflicts/TextResolver";
import { Button } from "@/components/ui/button";
import type { Notify } from "@/hooks/useLocks";
import { editsText, kindLabel, unresolved } from "@/lib/conflicts";
import { cn, errorMessage, readSetting, writeSetting } from "@/lib/utils";
import { PaneResizer } from "@/components/ui/resize-handle";
import { useResizableWidth } from "@/hooks/useResizableWidth";
import { Trans, useTranslation } from "react-i18next";
import { translate } from "@/i18n";

const LAYOUT_KEY = "nipa.diffLayout";

type ConflictsViewProps = { root: string; busy: boolean; notify: Notify };

export function ConflictsView({ root, busy, notify }: ConflictsViewProps) {
  const { t } = useTranslation();
  const listSize = useResizableWidth("conflicts", { initial: 288, min: 200, max: 560 });
  const queryClient = useQueryClient();
  const conflicts = useQuery({ queryKey: ["conflicts", root], queryFn: () => desktop.conflicts(root) });
  const [picked, setPicked] = useState("");

  const resolve = useMutation({
    mutationFn: ({ path, choice, content }: { path: string; choice: ResolveChoice; content?: string }) => desktop.resolveConflict(root, path, choice, content),
    onSuccess: (changes, { path }) => {
      queryClient.setQueryData(["changes", root], changes);
      for (const key of ["conflicts", "conflict-texts", "conflict-diff", "diff"]) void queryClient.invalidateQueries({ queryKey: [key, root] });
      notify(translate("conflictsView.resolved2", { path }), "success");
      const next = (conflicts.data?.files ?? []).find((f) => !f.resolved && f.path !== path);
      if (next) setPicked(next.path);
    },
    onError: (err, { path }) => notify(translate("conflictsView.couldNotResolve", { path, value: errorMessage(err) })),
  });

  if (conflicts.isPending) {
    return (
      <Centered>
        <Loader2 className="size-4 animate-spin" /> {t("conflictsView.readingConflicts")}
      </Centered>
    );
  }
  if (conflicts.isError) {
    return (
      <Centered>
        <AlertTriangle className="size-4 text-danger" /> {errorMessage(conflicts.error)}
      </Centered>
    );
  }
  const set = conflicts.data;
  if (!set.op || set.files.length === 0) {
    return (
      <Centered>
        <CheckCircle2 className="size-5 text-success" /> {t("conflictsView.noConflictsToResolve")}
      </Centered>
    );
  }
  const file = set.files.find((f) => f.path === picked) ?? set.files.find((f) => !f.resolved) ?? set.files[0];
  const left = unresolved(set);
  const settle = (choice: ResolveChoice, content?: string) => resolve.mutate({ path: file.path, choice, content });
  const working = busy || resolve.isPending;

  return (
    <div className="flex h-full min-h-0">
      <aside className="relative flex shrink-0 flex-col border-r border-border" ref={listSize.ref} style={{ width: listSize.width }}>
        <div className="shrink-0 space-y-1 border-b border-border px-3 py-2 text-[12px]">
          <p className="flex items-center gap-1.5 font-medium">
            <GitMerge className="size-3.5 text-muted-foreground" />
            {set.op === "merge" ? (
              <span className="truncate">
                <Trans i18nKey="conflictsView.merging" values={{ theirs: set.theirs.label, ours: set.ours.label }} components={[<Mono key="a">{null}</Mono>, <Mono key="b">{null}</Mono>]} />
              </span>
            ) : set.op === "shelf" ? (
              <span className="truncate" title={set.theirs.label}>
                {t("conflictsView.applying", { name: set.theirs.label })}
              </span>
            ) : (
              <span className="truncate" title={set.base.label}>
                {t("conflictsView.reverting", { name: set.base.label })}
              </span>
            )}
          </p>
          <p className={cn("text-[11px]", left ? "text-muted-foreground" : "text-success")}>
            {left ? translate("conflictsView.ofResolved", { left: set.files.length - left, count: set.files.length }) : translate("conflictsView.allResolvedAbove", { count: set.files.length, op: set.op === "merge" ? translate("conflictsView.finishTheMerge") : set.op === "shelf" ? translate("conflictsView.theShelfIsApplied") : translate("conflictsView.continueTheRevert") })}
          </p>
        </div>
        <ul className="min-h-0 flex-1 overflow-auto py-1" aria-label={t("conflictsView.conflictedFiles")}>
          {set.files.map((f) => (
            <li key={f.path}>
              <button
                type="button"
                aria-current={f === file}
                onClick={() => setPicked(f.path)}
                title={f.path}
                className={cn("flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-[12px] hover:bg-muted", f === file && "bg-muted font-medium")}
              >
                {f.resolved ? <CheckCircle2 className="size-3.5 shrink-0 text-success" aria-label={t("conflictsView.resolved")} /> : <CircleDot className="size-3.5 shrink-0 text-warning" aria-label={t("conflictsView.notResolved")} />}
                <FileIcon kind={f.viewer} />
                <span className="min-w-0 flex-1 truncate">{f.path}</span>
                <span className="shrink-0 text-[10px] text-muted-foreground">{kindLabel(f.kind)}</span>
              </button>
            </li>
          ))}
        </ul>
        <PaneResizer size={listSize} label={t("conflictsView.resizeFileList")} />
      </aside>
      <section className="flex min-w-0 flex-1 flex-col">
        {editsText(file) ? (
          <TextResolver key={file.path} root={root} file={file} set={set} busy={working} onResolve={settle} />
        ) : (
          <VersionResolver key={file.path} root={root} file={file} set={set} busy={working} onResolve={settle} />
        )}
      </section>
    </div>
  );
}

type ResolverProps = { root: string; file: ConflictFile; set: ConflictSet; busy: boolean; onResolve: (choice: ResolveChoice, content?: string) => void };

function VersionResolver({ root, file, set, busy, onResolve }: ResolverProps) {
  const { t } = useTranslation();
  const compare = useQuery({ queryKey: ["conflict-diff", root, file.path, set.ours.id, set.theirs.id], queryFn: () => desktop.conflictDiff(root, file.path) });
  const [layout, setLayoutState] = useState<DiffLayout>(() => (readSetting(LAYOUT_KEY) === "split" ? "split" : "unified"));
  const setLayout = (next: DiffLayout) => {
    setLayoutState(next);
    writeSetting(LAYOUT_KEY, next);
  };
  return (
    <>
      <ResolverHeader file={file} set={set} busy={busy} onResolve={onResolve}>
        <Button size="sm" variant="ghost" disabled={busy} title={t("conflictsView.keepTheFileExactlyAs")} onClick={() => onResolve("working")}>
          {t("conflictsView.useTheFileOnDisk")}
        </Button>
      </ResolverHeader>
      <p className="shrink-0 border-b border-border px-4 py-1.5 text-[11px] text-muted-foreground">
        <Trans i18nKey="conflictsView.comparing" values={{ explain: explain(file, set), ours: set.ours.label, theirs: set.theirs.label }} components={[<Mono key="a">{null}</Mono>, <Mono key="b">{null}</Mono>]} />
      </p>
      <div className="min-h-0 flex-1">
        {compare.isPending ? (
          <Centered>
            <Loader2 className="size-4 animate-spin" /> {t("conflictsView.loadingBothVersions")}
          </Centered>
        ) : compare.isError ? (
          <Centered>
            <AlertTriangle className="size-4 text-danger" /> {errorMessage(compare.error)}
          </Centered>
        ) : compare.data ? (
          <DiffViewer root={root} diff={compare.data} layout={layout} onLayout={setLayout} />
        ) : (
          <Centered>{t("conflictsView.bothSidesHoldTheSame")}</Centered>
        )}
      </div>
    </>
  );
}

function explain(file: ConflictFile, set: ConflictSet): string {
  const ours = set.ours.label;
  const theirs = set.theirs.label;
  switch (file.kind) {
    case "modify_delete":
      return translate("conflictsView.explain.modify_delete", { ours, theirs });
    case "delete_modify":
      return translate("conflictsView.explain.delete_modify", { ours, theirs });
    case "add_add":
      return translate("conflictsView.explain.add_add", { ours, theirs });
    default:
      return translate("conflictsView.explain.binary", { ours, theirs });
  }
}
