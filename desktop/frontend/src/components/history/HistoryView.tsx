import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, FileClock, GitMerge, Image as ImageIcon, Loader2, RefreshCw, Search, Tag as TagIcon, X } from "lucide-react";
import { desktop, type Commit, type Tag } from "@/api/desktop";
import { CommitDetail } from "@/components/history/CommitDetail";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import { PromptDialog } from "@/components/ui/prompt";
import type { Notify } from "@/hooks/useLocks";
import { commitTitle, filterCommits, isImagePath, shortId, tagsByCommit } from "@/lib/history";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage } from "@/lib/utils";

type HistoryViewProps = {
  root: string;
  branch: string;
  detached?: string;
  busy: boolean;
  notify: Notify;
  onRevert: (commit: Commit) => void;
  path?: string;
  open?: { id: string; seq: number };
};

type Dialog = { kind: "revert" | "tag" | "branch"; commit: Commit } | null;

export function HistoryView({ root, branch, detached, busy, notify, onRevert, path: initialPath = "", open }: HistoryViewProps) {
  const queryClient = useQueryClient();
  const [path, setPath] = useState(initialPath);
  const [pathInput, setPathInput] = useState(initialPath);
  const [search, setSearch] = useState("");
  const [selectedId, setSelectedId] = useState<string | null>(open?.id ?? null);
  useEffect(() => {
    if (open) setSelectedId(open.id);
  }, [open?.id, open?.seq]);
  const [dialog, setDialog] = useState<Dialog>(null);

  const log = useInfiniteQuery({
    queryKey: ["history", root, branch, path],
    queryFn: ({ pageParam }) => desktop.history(root, { branch, path, start: pageParam }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next || undefined,
  });
  const tags = useQuery({ queryKey: ["tags", root], queryFn: () => desktop.tags(root) });
  const byCommit = useMemo(() => tagsByCommit(tags.data ?? []), [tags.data]);

  const commits = useMemo(() => log.data?.pages.flatMap((p) => p.commits) ?? [], [log.data]);
  const shown = useMemo(() => filterCommits(commits, search), [commits, search]);
  const listed = selectedId ? commits.find((c) => c.id === selectedId) : undefined;
  const lookup = useQuery({
    queryKey: ["history", root, "commit", selectedId],
    queryFn: () => desktop.history(root, { branch, start: selectedId ?? "", limit: 1 }),
    enabled: !!selectedId && !listed,
    staleTime: Infinity,
  });
  const selected = listed ?? lookup.data?.commits[0] ?? (selectedId ? undefined : shown[0]);

  const createTag = useMutation({
    mutationFn: ({ name, message, commit }: { name: string; message: string; commit: Commit }) => desktop.createTag(root, name, { commit: commit.id }, message),
    onSuccess: (tag) => {
      setDialog(null);
      void queryClient.invalidateQueries({ queryKey: ["tags", root] });
      notify(`Tagged ${shortId(tag.commitId)} as ${tag.name}`, "success");
    },
    onError: (err) => notify(`Could not create the tag: ${errorMessage(err)}`),
  });
  const createBranch = useMutation({
    mutationFn: ({ name, commit }: { name: string; commit: Commit }) => desktop.createBranch(root, name, { commit: commit.id }),
    onSuccess: (b) => {
      setDialog(null);
      void queryClient.invalidateQueries({ queryKey: ["branches", root] });
      notify(`Created branch ${b.name}. Switch to it from Branches & tags.`, "success");
    },
    onError: (err) => notify(`Could not create the branch: ${errorMessage(err)}`),
  });

  const applyPath = (e: FormEvent) => {
    e.preventDefault();
    setPath(pathInput.trim().replace(/^\/+|\/+$/g, ""));
    setSelectedId(null);
  };
  const clearPath = () => {
    setPath("");
    setPathInput("");
    setSelectedId(null);
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      {path && isImagePath(path) && commits.length > 0 && (
        <AssetStrip root={root} path={path} commits={commits} selected={selected?.id} onSelect={setSelectedId} />
      )}
      <div className="flex min-h-0 flex-1">
        <aside className="flex w-[340px] shrink-0 flex-col border-r border-border">
          <form onSubmit={applyPath} className="flex shrink-0 items-center gap-1.5 border-b border-border px-3 py-2">
            <FileClock className="size-3.5 shrink-0 text-muted-foreground" />
            <input
              value={pathInput}
              onChange={(e) => setPathInput(e.target.value)}
              placeholder="History of a file or folder"
              aria-label="Path filter"
              className="min-w-0 flex-1 bg-transparent font-mono text-[12px] outline-none"
            />
            {path && (
              <button type="button" aria-label="Show all commits" onClick={clearPath} className="text-muted-foreground hover:text-foreground">
                <X className="size-3.5" />
              </button>
            )}
          </form>
          <label className="flex shrink-0 items-center gap-1.5 border-b border-border px-3 py-2 text-[12px]">
            <Search className="size-3.5 text-muted-foreground" />
            <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Filter by author or message" aria-label="Filter commits" className="min-w-0 flex-1 bg-transparent outline-none" />
          </label>
          {log.isPending ? (
            <Centered>
              <Loader2 className="size-4 animate-spin" /> Loading history…
            </Centered>
          ) : log.isError ? (
            <Centered>
              <AlertTriangle className="size-4 text-danger" />
              <span>{errorMessage(log.error)}</span>
              <Button size="sm" onClick={() => void log.refetch()}>
                <RefreshCw />
                Try again
              </Button>
            </Centered>
          ) : shown.length === 0 ? (
            <Centered>
              <span>{commits.length === 0 ? (path ? `No commits changed ${path}` : "No commits yet") : "No commits match the loaded history"}</span>
              {log.hasNextPage && (
                <Button size="sm" disabled={log.isFetchingNextPage} onClick={() => void log.fetchNextPage()}>
                  {log.isFetchingNextPage && <Loader2 className="animate-spin" />}
                  Load older commits
                </Button>
              )}
            </Centered>
          ) : (
            <ol className="min-h-0 flex-1 overflow-auto py-1" aria-label="Commits">
              {shown.map((c, i) => (
                <CommitRow key={c.id} commit={c} tags={byCommit.get(c.id) ?? []} last={i === shown.length - 1 && !log.hasNextPage} selected={c.id === selected?.id} onSelect={() => setSelectedId(c.id)} />
              ))}
              {log.hasNextPage && (
                <li className="px-3 py-2">
                  <Button size="sm" className="w-full justify-center" disabled={log.isFetchingNextPage} onClick={() => void log.fetchNextPage()}>
                    {log.isFetchingNextPage && <Loader2 className="animate-spin" />}
                    Load older commits
                  </Button>
                </li>
              )}
            </ol>
          )}
        </aside>
        <section className="min-w-0 flex-1">
          {selected ? (
            <CommitDetail
              root={root}
              commit={selected}
              tags={byCommit.get(selected.id) ?? []}
              focusPath={path}
              busy={busy}
              revertBlocked={detached ? `Switch back to a branch to revert (checked out at tag ${detached})` : undefined}
              onRevert={(commit) => setDialog({ kind: "revert", commit })}
              onTag={(commit) => setDialog({ kind: "tag", commit })}
              onBranch={(commit) => setDialog({ kind: "branch", commit })}
              onSelectCommit={setSelectedId}
            />
          ) : selectedId && lookup.isPending ? (
            <Centered>
              <Loader2 className="size-4 animate-spin" /> Loading commit…
            </Centered>
          ) : (
            <Centered>{selectedId ? "This commit could not be loaded" : "Select a commit"}</Centered>
          )}
        </section>
      </div>
      {dialog?.kind === "revert" && (
        <ConfirmDialog
          title="Revert this commit?"
          message={`A new commit on ${branch} undoes "${commitTitle(dialog.commit.message)}". Your clone must be up to date and have no unpushed changes to the same files.`}
          confirmLabel="Revert"
          onCancel={() => setDialog(null)}
          onConfirm={() => {
            const commit = dialog.commit;
            setDialog(null);
            onRevert(commit);
          }}
        />
      )}
      {dialog?.kind === "tag" && (
        <PromptDialog
          title="Tag this commit"
          label="Tag name"
          placeholder="v1.2.0"
          note={`Tags mark ${shortId(dialog.commit.id)} "${commitTitle(dialog.commit.message)}" for everyone. They cannot be moved, only deleted.`}
          confirmLabel="Create tag"
          withMessage
          busy={createTag.isPending}
          onCancel={() => setDialog(null)}
          onConfirm={(name, message) => createTag.mutate({ name, message, commit: dialog.commit })}
        />
      )}
      {dialog?.kind === "branch" && (
        <PromptDialog
          title="Create a branch here"
          label="Branch name"
          placeholder="feature/new-level"
          note={`The branch starts at ${shortId(dialog.commit.id)} "${commitTitle(dialog.commit.message)}".`}
          confirmLabel="Create branch"
          busy={createBranch.isPending}
          onCancel={() => setDialog(null)}
          onConfirm={(name) => createBranch.mutate({ name, commit: dialog.commit })}
        />
      )}
    </div>
  );
}

function CommitRow({ commit, tags, last, selected, onSelect }: { commit: Commit; tags: Tag[]; last: boolean; selected: boolean; onSelect: () => void }) {
  const merge = commit.parents.length > 1;
  return (
    <li>
      <button type="button" onClick={onSelect} aria-current={selected} className={cn("flex w-full gap-2 px-3 text-left hover:bg-muted", selected && "bg-muted")}>
        <span className="relative flex w-3 shrink-0 justify-center" aria-hidden>
          <span className={cn("absolute top-0 w-px bg-border", last ? "h-3" : "h-full")} />
          {merge ? (
            <GitMerge className="relative mt-1.5 size-3 bg-background text-accent" />
          ) : (
            <span className={cn("relative mt-2 size-2 rounded-full border border-accent bg-background", selected && "bg-accent")} />
          )}
        </span>
        <span className="min-w-0 flex-1 py-1.5">
          <span className="flex items-center gap-1">
            <span className="min-w-0 flex-1 truncate text-[12px] font-medium">{commitTitle(commit.message)}</span>
            {tags.map((t) => (
              <span key={t.name} className="flex shrink-0 items-center gap-0.5 rounded bg-accent/15 px-1 text-[10px] text-accent">
                <TagIcon className="size-2.5" />
                {t.name}
              </span>
            ))}
          </span>
          <span className="flex gap-2 text-[11px] text-muted-foreground">
            <span className="truncate">{commit.author || commit.email}</span>
            <span className="shrink-0" title={commit.date}>
              {timeAgo(commit.date)}
            </span>
            <span className="ml-auto shrink-0 font-mono">{shortId(commit.id)}</span>
          </span>
        </span>
      </button>
    </li>
  );
}

// AssetStrip shows one thumbnail per version of an image so artists can
// scan how it evolved and jump to a version.
function AssetStrip({ root, path, commits, selected, onSelect }: { root: string; path: string; commits: Commit[]; selected?: string; onSelect: (id: string) => void }) {
  return (
    <ol className="flex shrink-0 gap-2 overflow-x-auto border-b border-border bg-sidebar px-3 py-2" aria-label={`Versions of ${path}`}>
      {commits.map((c) => (
        <li key={c.id} className="shrink-0">
          <button
            type="button"
            onClick={() => onSelect(c.id)}
            title={`${commitTitle(c.message)} · ${c.author}`}
            aria-label={`Version ${shortId(c.id)}`}
            className={cn("flex w-20 flex-col items-center gap-1 rounded-md border border-transparent p-1 hover:bg-muted", c.id === selected && "border-accent bg-muted")}
          >
            <Thumbnail root={root} path={path} commit={c.id} />
            <span className="w-full truncate text-center text-[10px] text-muted-foreground">{timeAgo(c.date)}</span>
          </button>
        </li>
      ))}
    </ol>
  );
}

function Thumbnail({ root, path, commit }: { root: string; path: string; commit: string }) {
  const thumb = useQuery({
    queryKey: ["commit", root, commit, path, "thumb"],
    queryFn: () => desktop.imageDiff(root, null, { source: "revision", revision: commit, path }, { maxSize: 128 }),
    staleTime: Infinity,
    retry: false,
  });
  const info = thumb.data?.new;
  return (
    <span className="checkerboard flex size-16 items-center justify-center overflow-hidden rounded">
      {info && !info.hdr && info.src ? (
        <img src={info.src} alt="" className="max-h-full max-w-full object-contain" />
      ) : thumb.isPending ? (
        <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
      ) : (
        <ImageIcon className="size-4 text-muted-foreground" aria-label={thumb.isError ? "Not available in this version" : "No preview"} />
      )}
    </span>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-1 flex-col items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
