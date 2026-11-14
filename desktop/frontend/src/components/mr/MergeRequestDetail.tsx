import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  CircleDot,
  ExternalLink,
  FileDiff,
  GitCommit,
  GitMerge,
  GitPullRequest,
  GitPullRequestClosed,
  GitPullRequestDraft,
  Loader2,
  MessageSquare,
  Pencil,
  XCircle,
} from "lucide-react";
import { desktop, type Activity, type Check, type MergeRequest, type MergeRequestDetail as Detail } from "@/api/desktop";
import { MergeRequestFiles } from "@/components/mr/MergeRequestFiles";
import { PeopleBar } from "@/components/mr/PeopleBar";
import { CommentBox, ThreadCard } from "@/components/mr/ThreadCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm";
import type { Notify } from "@/hooks/useLocks";
import { useLockStatus } from "@/hooks/useLocks";
import { useMergeRequest, type ReviewState } from "@/hooks/useMergeRequest";
import { commitTitle, shortId } from "@/lib/history";
import { blockedReason, conversation, eventText, mergeabilityText, reviewText, strategies, threadSnippet, type Strategy } from "@/lib/mergeRequests";
import { timeAgo } from "@/lib/time";
import { cn, errorMessage, readSetting, writeSetting } from "@/lib/utils";

type Tab = "conversation" | "commits" | "files";
const STRATEGY_KEY = "nipa.mergeStrategy";

export function MergeRequestStatus({ mr }: { mr: MergeRequest }) {
  if (mr.status === "merged") {
    return (
      <Badge className="text-accent">
        <GitMerge />
        Merged
      </Badge>
    );
  }
  if (mr.status === "closed") {
    return (
      <Badge className="text-danger">
        <GitPullRequestClosed />
        Closed
      </Badge>
    );
  }
  if (mr.draft) {
    return (
      <Badge>
        <GitPullRequestDraft />
        Draft
      </Badge>
    );
  }
  return (
    <Badge className="text-success">
      <GitPullRequest />
      Open
    </Badge>
  );
}

type DetailProps = { root: string; number: number; notify: Notify };

export function MergeRequestDetail({ root, number, notify }: DetailProps) {
  const mr = useMergeRequest(root, number, notify);
  const [tab, setTab] = useState<Tab>("conversation");
  const [focusPath, setFocusPath] = useState("");
  const needsSourceHead = !!mr.detail.data && !mr.detail.data.mergeability.sourceCommitId && !mr.detail.data.request.mergedSourceId;
  const branches = useQuery({ queryKey: ["branches", root], queryFn: () => desktop.branches(root), enabled: needsSourceHead });

  if (mr.detail.isPending) {
    return (
      <Centered>
        <Loader2 className="size-4 animate-spin" /> Loading merge request…
      </Centered>
    );
  }
  if (!mr.detail.data) {
    return (
      <Centered>
        <AlertTriangle className="size-4 text-danger" /> {errorMessage(mr.detail.error)}
      </Centered>
    );
  }
  const { request, mergeability } = mr.detail.data;
  const activity = mr.activity.data;
  const base = mergeability.mergeBaseId || request.mergeBaseId;
  const sourceHead = mergeability.sourceCommitId || (branches.data ?? []).find((b) => b.name === request.source)?.commitId || "";
  const head = request.mergedSourceId || sourceHead;
  const threads = activity?.threads ?? [];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="shrink-0 space-y-1.5 border-b border-border px-4 py-3">
        <TitleRow request={request} busy={mr.busy} onSave={(title, description) => mr.edit.mutateAsync({ title, description })} />
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <MergeRequestStatus mr={request} />
          <span className="font-mono">{request.source}</span>
          <ArrowRight className="size-3" />
          <span className="font-mono">{request.target}</span>
          <span title={request.createdAt}>opened {timeAgo(request.createdAt)}</span>
          {request.approvals > 0 && (
            <span className="text-success">
              {request.approvals} approval{request.approvals === 1 ? "" : "s"}
            </span>
          )}
          {request.changesRequested > 0 && <span className="text-danger">changes requested</span>}
        </div>
        <PeopleBar root={root} request={request} outstanding={mr.detail.data.outstanding} activity={activity} busy={mr.busy} onAssign={(ids) => mr.assign.mutate(ids)} onRequestReview={(person) => mr.askReview.mutate(person)} />
      </header>
      <nav role="tablist" aria-label="Merge request" className="flex shrink-0 gap-1 border-b border-border px-4">
        {(
          [
            ["conversation", "Conversation", MessageSquare],
            ["commits", "Commits", GitCommit],
            ["files", "File changes", FileDiff],
          ] as const
        ).map(([id, label, Icon]) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={tab === id}
            onClick={() => {
              setFocusPath("");
              setTab(id);
            }}
            className={cn("flex items-center gap-1.5 border-b-2 px-2 py-1.5 text-[12px]", tab === id ? "border-accent font-medium" : "border-transparent text-muted-foreground hover:text-foreground")}
          >
            <Icon className="size-3.5" />
            {label}
          </button>
        ))}
      </nav>
      <div className="min-h-0 flex-1">
        {tab === "conversation" ? (
          <Conversation
            root={root}
            detail={mr.detail.data}
            activity={activity}
            activityError={mr.activity.error}
            mr={mr}
            base={base}
            head={head}
            onOpenFile={(path) => {
              setFocusPath(path);
              setTab("files");
            }}
          />
        ) : tab === "commits" ? (
          <Commits root={root} number={number} />
        ) : (
          <MergeRequestFiles
            root={root}
            base={base}
            head={head}
            focus={focusPath}
            threads={threads}
            busy={mr.busy}
            canComment={request.status === "open"}
            onComment={(input) => mr.comment.mutateAsync(input)}
            onReply={(threadId, body) => mr.reply.mutateAsync({ threadId, body })}
            onResolve={(threadId, resolved) => mr.resolve.mutate({ threadId, resolved })}
          />
        )}
      </div>
    </div>
  );
}

function TitleRow({ request, busy, onSave }: { request: MergeRequest; busy: boolean; onSave: (title: string, description: string) => Promise<unknown> }) {
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(request.title);
  const [description, setDescription] = useState(request.description);
  if (!editing) {
    return (
      <div className="flex items-start gap-2">
        <h2 className="min-w-0 flex-1 select-text break-words text-sm font-semibold">
          {request.title} <span className="font-normal text-muted-foreground">!{request.number}</span>
        </h2>
        {request.status === "open" && (
          <Button
            size="sm"
            variant="ghost"
            aria-label="Edit title and description"
            onClick={() => {
              setTitle(request.title);
              setDescription(request.description);
              setEditing(true);
            }}
          >
            <Pencil />
          </Button>
        )}
      </div>
    );
  }
  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        void onSave(title, description).then(() => setEditing(false), () => undefined);
      }}
    >
      <input value={title} onChange={(e) => setTitle(e.target.value)} aria-label="Title" className="w-full rounded-md border border-border bg-background px-2 py-1 text-[13px] outline-none focus:border-accent" />
      <textarea
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        aria-label="Description"
        rows={4}
        className="w-full resize-y rounded-md border border-border bg-background px-2 py-1 text-[12px] outline-none focus:border-accent"
      />
      <div className="flex justify-end gap-2">
        <Button type="button" size="sm" onClick={() => setEditing(false)}>
          Cancel
        </Button>
        <Button type="submit" size="sm" variant="primary" disabled={busy || !title.trim()}>
          Save
        </Button>
      </div>
    </form>
  );
}

type Actions = ReturnType<typeof useMergeRequest>;

type ConversationProps = { root: string; detail: Detail; activity?: Activity; activityError: unknown; mr: Actions; base: string; head: string; onOpenFile: (path: string) => void };

function Conversation({ root, detail, activity, activityError, mr, base, head, onOpenFile }: ConversationProps) {
  const { request } = detail;
  const open = request.status === "open";
  const me = useLockStatus(root).data?.userId;
  const hasLineThreads = (activity?.threads ?? []).some((t) => !!t.path);
  const files = useQuery({ queryKey: ["commit", root, head, "vs", base], queryFn: () => desktop.revisionFiles(root, base, head), enabled: hasLineThreads && !!base && !!head, staleTime: Infinity });
  return (
    <div className="h-full overflow-auto">
      <div className="mx-auto max-w-3xl space-y-3 p-4">
        <section className="rounded-md border border-border bg-card p-3 text-[12px]">
          {request.description ? <p className="select-text whitespace-pre-wrap break-words">{request.description}</p> : <p className="text-muted-foreground">No description.</p>}
        </section>
        {!activity ? (
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            {activityError ? (
              <>
                <AlertTriangle className="size-3.5 text-danger" /> {errorMessage(activityError)}
              </>
            ) : (
              <>
                <Loader2 className="size-3.5 animate-spin" /> Loading activity…
              </>
            )}
          </p>
        ) : (
          <ol className="space-y-2" aria-label="Activity">
            {conversation(activity).map((item) => (
              <li key={`${item.kind}-${item.kind === "event" ? item.event.id : item.kind === "review" ? item.review.id : item.thread.id}`}>
                {item.kind === "event" ? (
                  <p className="flex items-center gap-1.5 px-1 text-[11px] text-muted-foreground">
                    <CircleDot className="size-3" />
                    <span className="font-medium text-foreground">{item.event.actor.name || "Someone"}</span> {eventText(item.event.kind)}
                    {item.event.subject.name && <span className="font-medium text-foreground">{item.event.subject.name}</span>}
                    {item.event.commitId && <span className="font-mono">{shortId(item.event.commitId)}</span>}
                    <span title={item.event.createdAt}>· {timeAgo(item.event.createdAt)}</span>
                  </p>
                ) : item.kind === "review" ? (
                  <div className={cn("rounded-md border border-border bg-card p-3 text-[12px]", (item.review.stale || item.review.dismissed) && "opacity-60")}>
                    <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      {item.review.state === "approved" ? <CheckCircle2 className="size-3.5 text-success" /> : item.review.state === "changes_requested" ? <XCircle className="size-3.5 text-danger" /> : <MessageSquare className="size-3.5" />}
                      <span className="font-medium text-foreground">{item.review.reviewer.name}</span> {reviewText[item.review.state] ?? item.review.state}
                      <span title={item.review.createdAt}>· {timeAgo(item.review.createdAt)}</span>
                      {item.review.dismissed ? <Badge>dismissed</Badge> : item.review.stale ? <Badge title="New commits were pushed after this review">outdated</Badge> : null}
                    </p>
                    {item.review.body && <p className="mt-1 select-text whitespace-pre-wrap break-words">{item.review.body}</p>}
                  </div>
                ) : (
                  <ThreadCard
                    thread={item.thread}
                    busy={mr.busy}
                    snippet={item.thread.outdated ? undefined : threadSnippet(files.data ?? [], item.thread)}
                    onOpenFile={item.thread.path ? () => onOpenFile(item.thread.path) : undefined}
                    onReply={(body) => mr.reply.mutateAsync({ threadId: item.thread.id, body })}
                    onResolve={(resolved) => mr.resolve.mutate({ threadId: item.thread.id, resolved })}
                  />
                )}
              </li>
            ))}
          </ol>
        )}
        {activity && activity.checks.length > 0 && <Checks checks={activity.checks} />}
        {open && <ReviewBox busy={mr.busy} isAuthor={!!me && me === request.authorId} onSubmit={(state, body) => mr.review.mutateAsync({ state, body })} />}
        {open && <CommentBox label="Comment" placeholder="Add to the conversation" busy={mr.busy} onSubmit={(body) => mr.comment.mutateAsync({ body })} />}
        <MergeBox detail={detail} mr={mr} />
      </div>
    </div>
  );
}

function Checks({ checks }: { checks: Check[] }) {
  return (
    <section className="rounded-md border border-border bg-card text-[12px]" aria-label="Status checks">
      <h3 className="border-b border-border px-3 py-1.5 text-[11px] font-semibold">Status checks</h3>
      <ul className="divide-y divide-border">
        {checks.map((c) => (
          <li key={c.name} className="flex items-center gap-2 px-3 py-1.5">
            {c.state === "success" ? <CheckCircle2 className="size-3.5 text-success" /> : c.state === "failed" ? <XCircle className="size-3.5 text-danger" /> : <Loader2 className="size-3.5 text-warning" />}
            <span className="font-medium">{c.name}</span>
            <span className="text-muted-foreground">{c.state}</span>
            {c.reporter && <span className="text-muted-foreground">· {c.reporter}</span>}
            {c.url && (
              <a href={c.url} target="_blank" rel="noreferrer" className="ml-auto flex items-center gap-1 text-accent hover:underline">
                Details <ExternalLink className="size-3" />
              </a>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function ReviewBox({ busy, isAuthor, onSubmit }: { busy: boolean; isAuthor: boolean; onSubmit: (state: ReviewState, body: string) => Promise<unknown> }) {
  const [body, setBody] = useState("");
  const send = (state: ReviewState) =>
    void onSubmit(state, body).then(
      () => setBody(""),
      () => undefined,
    );
  return (
    <section className="space-y-2 rounded-md border border-border bg-card p-3" aria-label="Review">
      <h3 className="text-[11px] font-semibold">Your review</h3>
      {isAuthor && <p className="text-[11px] text-muted-foreground">You cannot approve or request changes on your own merge request.</p>}
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        aria-label="Review comment"
        placeholder="Summarize your review"
        rows={2}
        className="w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 text-[12px] outline-none focus:border-accent"
      />
      <div className="flex justify-end gap-2">
        <Button size="sm" disabled={busy || !body.trim()} onClick={() => send("commented")}>
          Comment
        </Button>
        <Button size="sm" disabled={busy || !body.trim() || isAuthor} onClick={() => send("changes_requested")}>
          <XCircle />
          Request changes
        </Button>
        <Button size="sm" variant="primary" disabled={busy || !body.trim() || isAuthor} onClick={() => send("approved")}>
          <CheckCircle2 />
          Approve
        </Button>
      </div>
    </section>
  );
}

function MergeBox({ detail, mr }: { detail: Detail; mr: Actions }) {
  const { request, mergeability } = detail;
  const [strategy, setStrategyState] = useState<Strategy>(() => (strategies.find((s) => s.id === readSetting(STRATEGY_KEY))?.id ?? "merge") as Strategy);
  const [deleteSource, setDeleteSource] = useState(false);
  const [closing, setClosing] = useState(false);
  const setStrategy = (s: Strategy) => {
    setStrategyState(s);
    writeSetting(STRATEGY_KEY, s);
  };

  if (request.status === "merged") {
    return (
      <p className="flex items-center gap-2 rounded-md border border-border bg-card p-3 text-[12px]">
        <GitMerge className="size-4 text-accent" />
        Merged into <span className="font-mono">{request.target}</span>
        {request.mergeCommitId && <span className="font-mono text-muted-foreground">{shortId(request.mergeCommitId)}</span>}
      </p>
    );
  }
  if (request.status === "closed") {
    return (
      <div className="flex items-center gap-2 rounded-md border border-border bg-card p-3 text-[12px]">
        <GitPullRequestClosed className="size-4 text-danger" />
        <span className="mr-auto">This request was closed without merging.</span>
        <Button size="sm" disabled={mr.busy} onClick={() => mr.reopen.mutate(undefined)}>
          Reopen
        </Button>
      </div>
    );
  }
  const blocked = blockedReason(mergeability.blockedBy);
  const behindForFF = mergeability.status === "behind_target" && strategy === "ff";
  const canMerge = !blocked && (mergeability.status === "mergeable" || (mergeability.status === "behind_target" && strategy !== "ff"));
  return (
    <section className="space-y-2 rounded-md border border-border bg-card p-3 text-[12px]" aria-label="Merge">
      <p className={cn("flex items-center gap-2 font-medium", canMerge ? "text-success" : "text-warning")}>
        {canMerge ? <CheckCircle2 className="size-4" /> : <AlertTriangle className="size-4" />}
        {blocked || (behindForFF ? mergeabilityText("behind_target") : mergeability.status === "behind_target" ? "The target moved on; this strategy can still merge." : mergeabilityText(mergeability.status))}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <label className="flex items-center gap-1.5">
          <span className="text-muted-foreground">Strategy</span>
          <select
            value={strategy}
            onChange={(e) => setStrategy(e.target.value as Strategy)}
            aria-label="Merge strategy"
            className="rounded-md border border-border bg-background px-1.5 py-1 text-[12px]"
            title={strategies.find((s) => s.id === strategy)?.hint}
          >
            {strategies.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1.5">
          <input type="checkbox" checked={deleteSource} onChange={(e) => setDeleteSource(e.target.checked)} />
          Delete <span className="font-mono">{request.source}</span> after merging
        </label>
        <span className="ml-auto flex gap-2">
          {request.draft ? (
            <Button size="sm" disabled={mr.busy} onClick={() => mr.draft.mutate(false)}>
              Mark ready for review
            </Button>
          ) : (
            <Button size="sm" variant="ghost" disabled={mr.busy} onClick={() => mr.draft.mutate(true)}>
              Mark as draft
            </Button>
          )}
          <Button size="sm" variant="ghost" disabled={mr.busy} onClick={() => setClosing(true)}>
            Close
          </Button>
          <Button size="sm" variant="primary" disabled={mr.busy || !canMerge} onClick={() => mr.merge.mutate({ strategy, deleteSource })}>
            {mr.merge.isPending ? <Loader2 className="animate-spin" /> : <GitMerge />}
            Merge
          </Button>
        </span>
      </div>
      {closing && (
        <ConfirmDialog
          title={`Close !${request.number}?`}
          message="The request is closed without merging. Its locks are released; you can reopen it later."
          confirmLabel="Close request"
          onCancel={() => setClosing(false)}
          onConfirm={() => {
            setClosing(false);
            mr.close.mutate(undefined);
          }}
        />
      )}
    </section>
  );
}

function Commits({ root, number }: { root: string; number: number }) {
  const commits = useQuery({ queryKey: ["mr", root, number, "commits"], queryFn: () => desktop.mergeRequestCommits(root, number) });
  if (commits.isPending) {
    return (
      <Centered>
        <Loader2 className="size-4 animate-spin" /> Loading commits…
      </Centered>
    );
  }
  if (commits.isError) {
    return (
      <Centered>
        <AlertTriangle className="size-4 text-danger" /> {errorMessage(commits.error)}
      </Centered>
    );
  }
  if (commits.data.length === 0) return <Centered>No commits.</Centered>;
  return (
    <ol className="h-full divide-y divide-border overflow-auto" aria-label="Commits">
      {commits.data.map((c) => (
        <li key={c.id} className="flex items-center gap-2 px-4 py-2 text-[12px]">
          <GitCommit className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate">{commitTitle(c.message)}</span>
          <span className="shrink-0 text-muted-foreground">{c.author}</span>
          <span className="shrink-0 text-muted-foreground" title={c.date}>
            {timeAgo(c.date)}
          </span>
          <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{shortId(c.id)}</span>
        </li>
      ))}
    </ol>
  );
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex h-full items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">{children}</div>;
}
