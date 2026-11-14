import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowRight, CheckCircle2, GitPullRequest, Loader2, Plus, RefreshCw, Search, Sparkles, XCircle } from "lucide-react";
import { desktop, type MergeRequest, type Person } from "@/api/desktop";
import { MergeRequestDetail, MergeRequestStatus } from "@/components/mr/MergeRequestDetail";
import { MemberPicker } from "@/components/people/MemberPicker";
import { Button } from "@/components/ui/button";
import type { Notify } from "@/hooks/useLocks";
import { useDraft, useLLMSettings } from "@/hooks/useGenerate";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage } from "@/lib/utils";
import { PaneResizer } from "@/components/ui/resize-handle";
import { useResizableWidth } from "@/hooks/useResizableWidth";
import { usePersistedState } from "@/hooks/usePersistedState";
import { repoSetting } from "@/lib/settings";

const filters = [
  ["open", "Open"],
  ["draft", "Drafts"],
  ["merged", "Merged"],
  ["closed", "Closed"],
  ["", "All"],
] as const;

type Filter = (typeof filters)[number][0];

const isFilter = (v: unknown): v is Filter => filters.some(([id]) => id === v);

type MergeRequestsViewProps = { root: string; branch: string; notify: Notify; open?: { number: number; seq: number }; create?: number };

export function MergeRequestsView({ root, branch, notify, open, create }: MergeRequestsViewProps) {
  const listSize = useResizableWidth("mr", { initial: 340, min: 240, max: 720 });
  const [status, setStatus] = usePersistedState<Filter>(repoSetting(root, "mrs.status"), "open", { valid: isFilter });
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = usePersistedState<number | null>(repoSetting(root, "mrs.selected"), null, {
    valid: (v): v is number | null => v === null || typeof v === "number",
    prefer: open?.number,
  });
  useEffect(() => {
    if (open) setSelected(open.number);
  }, [open?.number, open?.seq]);
  const [creating, setCreating] = useState(false);
  useEffect(() => {
    if (create) setCreating(true);
  }, [create]);

  const list = useInfiniteQuery({
    queryKey: ["mrs", root, status, search],
    queryFn: ({ pageParam }) => desktop.mergeRequests(root, { status, search, after: pageParam }),
    initialPageParam: 0,
    getNextPageParam: (last) => last.next || undefined,
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const current = selected ?? items[0]?.number ?? null;

  const applySearch = (e: FormEvent) => {
    e.preventDefault();
    setSearch(searchInput.trim());
    setSelected(null);
  };

  return (
    <div className="flex h-full min-h-0">
      <aside className="relative flex shrink-0 flex-col border-r border-border" ref={listSize.ref} style={{ width: listSize.width }}>
        <div className="flex shrink-0 items-center gap-1 border-b border-border px-3 py-2">
          <div className="flex rounded-md border border-border p-0.5 text-[11px]" role="group" aria-label="Status">
            {filters.map(([id, label]) => (
              <button
                key={label}
                type="button"
                aria-pressed={status === id}
                onClick={() => {
                  setStatus(id);
                  setSelected(null);
                }}
                className={cn("rounded px-1.5 py-0.5 text-muted-foreground hover:text-foreground", status === id && "bg-muted text-foreground")}
              >
                {label}
              </button>
            ))}
          </div>
          <Button size="sm" variant="ghost" className="ml-auto" aria-label="Refresh merge requests" disabled={list.isFetching} onClick={() => void list.refetch()}>
            <RefreshCw className={cn(list.isFetching && "animate-spin")} />
          </Button>
          <Button size="sm" variant="primary" onClick={() => setCreating(true)}>
            <Plus />
            New
          </Button>
        </div>
        <form onSubmit={applySearch} className="flex shrink-0 items-center gap-1.5 border-b border-border px-3 py-2 text-[12px]">
          <Search className="size-3.5 text-muted-foreground" />
          <input value={searchInput} onChange={(e) => setSearchInput(e.target.value)} placeholder="Search titles and descriptions" aria-label="Search merge requests" className="min-w-0 flex-1 bg-transparent outline-none" />
        </form>
        {list.isPending ? (
          <Centered>
            <Loader2 className="size-4 animate-spin" /> Loading merge requests…
          </Centered>
        ) : list.isError ? (
          <Centered>
            <AlertTriangle className="size-4 text-danger" />
            {errorMessage(list.error)}
          </Centered>
        ) : items.length === 0 ? (
          <Centered>
            <GitPullRequest className="size-5" />
            {search ? "No merge requests match" : "No merge requests here yet"}
          </Centered>
        ) : (
          <ol className="min-h-0 flex-1 overflow-auto" aria-label="Merge requests">
            {items.map((mr) => (
              <Row key={mr.number} mr={mr} selected={mr.number === current} onSelect={() => setSelected(mr.number)} />
            ))}
            {list.hasNextPage && (
              <li className="px-3 py-2">
                <Button size="sm" className="w-full justify-center" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
                  {list.isFetchingNextPage && <Loader2 className="animate-spin" />}
                  Load more
                </Button>
              </li>
            )}
          </ol>
        )}
        <PaneResizer size={listSize} label="Resize merge request list" />
      </aside>
      <section className="min-w-0 flex-1">
        {current !== null ? <MergeRequestDetail key={current} root={root} number={current} notify={notify} /> : <Centered>Select a merge request</Centered>}
      </section>
      {creating && (
        <NewMergeRequestDialog
          root={root}
          branch={branch}
          notify={notify}
          onClose={() => setCreating(false)}
          onCreated={(mr) => {
            setCreating(false);
            setStatus(mr.draft ? "draft" : "open");
            setSelected(mr.number);
          }}
        />
      )}
    </div>
  );
}

function Row({ mr, selected, onSelect }: { mr: MergeRequest; selected: boolean; onSelect: () => void }) {
  return (
    <li>
      <button type="button" onClick={onSelect} aria-current={selected} className={cn("w-full space-y-1 border-b border-border/60 px-3 py-2 text-left hover:bg-muted", selected && "bg-muted")}>
        <span className="flex items-center gap-1.5">
          <span className="min-w-0 flex-1 truncate text-[12px] font-medium">{mr.title}</span>
          <span className="shrink-0 text-[11px] text-muted-foreground">!{mr.number}</span>
        </span>
        <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          <MergeRequestStatus mr={mr} />
          <span className="truncate font-mono">{mr.source}</span>
          <ArrowRight className="size-3 shrink-0" />
          <span className="truncate font-mono">{mr.target}</span>
          <span className="ml-auto flex shrink-0 items-center gap-1.5">
            {mr.approvals > 0 && (
              <span className="flex items-center gap-0.5 text-success" title={`${mr.approvals} approvals`}>
                <CheckCircle2 className="size-3" />
                {mr.approvals}
              </span>
            )}
            {mr.changesRequested > 0 && <XCircle className="size-3 text-danger" aria-label="Changes requested" />}
            <span title={mr.updatedAt}>{timeAgo(mr.updatedAt || mr.createdAt)}</span>
          </span>
        </span>
      </button>
    </li>
  );
}

type NewProps = { root: string; branch: string; notify: Notify; onClose: () => void; onCreated: (mr: MergeRequest) => void };

function NewMergeRequestDialog({ root, branch, notify, onClose, onCreated }: NewProps) {
  const queryClient = useQueryClient();
  const branches = useQuery({ queryKey: ["branches", root], queryFn: () => desktop.branches(root) });
  const names = (branches.data ?? []).map((b) => b.name);
  const defaultTarget = branches.data?.find((b) => b.default)?.name ?? "";
  const [source, setSource] = useState(branch);
  const [target, setTarget] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [draft, setDraft] = useState(false);
  const [reviewers, setReviewers] = useState<Person[]>([]);
  const [assignees, setAssignees] = useState<Person[]>([]);
  const into = target || (defaultTarget !== source ? defaultTarget : "");
  const llm = useLLMSettings();
  const generated = useDraft(description, (d) => {
    if (d.title) setTitle(d.title);
    setDescription(d.text);
  });

  const create = useMutation({
    mutationFn: () =>
      desktop.createMergeRequest(root, { source, target: into, title, description: generated.finalize(description), draft, reviewers: reviewers.map((p) => p.id), assignees: assignees.map((p) => p.id) }),
    onSuccess: (mr) => {
      void queryClient.invalidateQueries({ queryKey: ["mrs", root] });
      notify(`Opened !${mr.number}`, "success");
      onCreated(mr);
    },
    onError: (err) => notify(`Could not open the merge request: ${errorMessage(err)}`),
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  const select = "w-full rounded-md border border-border bg-background px-2 py-1.5 font-mono text-[12px]";
  return (
    <div role="presentation" className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" onKeyDown={(e) => e.key === "Escape" && onClose()}>
      <form role="dialog" aria-modal="true" aria-labelledby="new-mr-title" onSubmit={submit} className="w-full max-w-lg space-y-3 rounded-lg border border-border bg-card p-5 text-[12px] shadow-xl">
        <h2 id="new-mr-title" className="text-sm font-semibold">
          New merge request
        </h2>
        {branches.isError && <p className="text-danger">{errorMessage(branches.error)}</p>}
        <div className="flex items-end gap-2">
          <label className="flex-1 space-y-1">
            <span className="text-muted-foreground">From</span>
            <select value={source} onChange={(e) => setSource(e.target.value)} aria-label="Source branch" className={select}>
              {!names.includes(source) && <option value={source}>{source}</option>}
              {names.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
          <ArrowRight className="mb-2 size-4 text-muted-foreground" />
          <label className="flex-1 space-y-1">
            <span className="text-muted-foreground">Into</span>
            <select value={into} onChange={(e) => setTarget(e.target.value)} aria-label="Target branch" className={select}>
              <option value="">Choose a branch</option>
              {names
                .filter((n) => n !== source)
                .map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
            </select>
          </label>
        </div>
        {llm.data?.configured && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-[11px]">
              <Button size="sm" variant="ghost" className="h-6 px-1.5" onClick={() => void generated.run(() => desktop.generateMergeRequest(root, source, into))} disabled={generated.busy || !into || !source}>
                {generated.busy ? <Loader2 className="animate-spin" /> : <Sparkles />}
                {generated.busy ? "Generating…" : "Generate title and description"}
              </Button>
              {generated.model && !generated.busy && <span className="min-w-0 truncate text-muted-foreground">Drafted by {generated.model}; review it before opening</span>}
            </div>
            {generated.error && (
              <p role="alert" className="text-[11px] text-danger">
                Could not generate a description: {generated.error}
              </p>
            )}
          </div>
        )}
        <label className="block space-y-1">
          <span className="text-muted-foreground">Title</span>
          <input autoFocus value={title} onChange={(e) => setTitle(e.target.value)} aria-label="Title" className="w-full rounded-md border border-border bg-background px-2 py-1.5 outline-none focus:border-accent" />
        </label>
        <label className="block space-y-1">
          <span className="text-muted-foreground">Description</span>
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} aria-label="Description" rows={4} className="w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 outline-none focus:border-accent" />
        </label>
        <div className="flex gap-2">
          <div className="flex-1 space-y-1">
            <span className="text-muted-foreground">Reviewers</span>
            <MemberPicker root={root} label="Reviewers" selected={reviewers} onChange={setReviewers} />
          </div>
          <div className="flex-1 space-y-1">
            <span className="text-muted-foreground">Assignees</span>
            <MemberPicker root={root} label="Assignees" selected={assignees} onChange={setAssignees} />
          </div>
        </div>
        <label className="flex items-center gap-1.5">
          <input type="checkbox" checked={draft} onChange={(e) => setDraft(e.target.checked)} />
          Open as a draft (cannot be merged until marked ready)
        </label>
        <p className="text-[11px] text-muted-foreground">Push your changes to {source || "the source branch"} first; the request compares what is on the server. Changed binary files are locked for the request.</p>
        <div className="flex justify-end gap-2">
          <Button type="button" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" size="sm" variant="primary" disabled={create.isPending || !title.trim() || !into || !source}>
            {create.isPending && <Loader2 className="animate-spin" />}
            Open merge request
          </Button>
        </div>
      </form>
    </div>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full flex-1 flex-col items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
