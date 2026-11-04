import { useState, type FormEvent } from "react";
import { CheckCircle2, MessageSquare, RotateCcw } from "lucide-react";
import type { Thread } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { timeAgo } from "@/lib/time";
import { cn } from "@/lib/utils";

type ThreadCardProps = {
  thread: Thread;
  busy: boolean;
  onReply: (body: string) => Promise<unknown>;
  onResolve: (resolved: boolean) => void;
  compact?: boolean;
};

export function ThreadCard({ thread, busy, onReply, onResolve, compact }: ThreadCardProps) {
  const [open, setOpen] = useState(!thread.resolved);
  const where = thread.path ? `${thread.path}:${thread.newLine || thread.oldLine}` : "";
  if (thread.resolved && !open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className={cn("flex w-full items-center gap-1.5 rounded-md border border-border bg-card px-3 py-1.5 text-left text-[11px] text-muted-foreground hover:text-foreground", compact && "mx-2 my-1 w-auto font-sans")}
      >
        <CheckCircle2 className="size-3.5 text-success" />
        Resolved{thread.resolvedBy ? ` by ${thread.resolvedBy}` : ""} · {thread.comments.length} comment{thread.comments.length === 1 ? "" : "s"}
        {where && <span className="truncate font-mono">{where}</span>}
      </button>
    );
  }
  return (
    <div className={cn("space-y-2 rounded-md border border-border bg-card p-3 font-sans text-[12px]", compact && "mx-2 my-1")} aria-label={`Thread ${thread.id}`} role="group">
      {where && !compact && (
        <p className="flex items-center gap-1 font-mono text-[11px] text-muted-foreground">
          <MessageSquare className="size-3" />
          {where}
          {thread.outdated && <span className="ml-1 rounded bg-muted px-1 font-sans">outdated</span>}
        </p>
      )}
      {thread.comments.map((c) => (
        <div key={c.id} className="space-y-0.5">
          <p className="text-[11px] text-muted-foreground">
            <span className="font-medium text-foreground">{c.author.name || c.author.id}</span> · <span title={c.createdAt}>{timeAgo(c.createdAt)}</span>
            {c.edited && " · edited"}
          </p>
          <p className="select-text whitespace-pre-wrap break-words">{c.body}</p>
        </div>
      ))}
      <ReplyBox busy={busy} onReply={onReply} />
      <div className="flex justify-end">
        {thread.resolved ? (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onResolve(false)}>
            <RotateCcw />
            Reopen thread
          </Button>
        ) : (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onResolve(true)}>
            <CheckCircle2 />
            Resolve
          </Button>
        )}
      </div>
    </div>
  );
}

function ReplyBox({ busy, onReply }: { busy: boolean; onReply: (body: string) => Promise<unknown> }) {
  const [body, setBody] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await onReply(body);
      setBody("");
    } catch {
      return;
    }
  };
  return (
    <form onSubmit={(e) => void submit(e)} className="flex gap-2">
      <input
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder="Reply…"
        aria-label="Reply"
        className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 text-[12px] outline-none focus:border-accent"
      />
      <Button type="submit" size="sm" disabled={busy || !body.trim()}>
        Reply
      </Button>
    </form>
  );
}

type CommentBoxProps = { label: string; placeholder?: string; busy: boolean; autoFocus?: boolean; onSubmit: (body: string) => Promise<unknown>; onCancel?: () => void };

// CommentBox writes a new comment; the text stays when sending fails.
export function CommentBox({ label, placeholder, busy, autoFocus, onSubmit, onCancel }: CommentBoxProps) {
  const [body, setBody] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await onSubmit(body);
      setBody("");
    } catch {
      return;
    }
  };
  return (
    <form onSubmit={(e) => void submit(e)} className="space-y-2 font-sans">
      <textarea
        autoFocus={autoFocus}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder={placeholder}
        aria-label={label}
        rows={3}
        onKeyDown={(e) => e.key === "Escape" && onCancel?.()}
        className="w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 text-[12px] outline-none focus:border-accent"
      />
      <div className="flex justify-end gap-2">
        {onCancel && (
          <Button type="button" size="sm" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" size="sm" variant="primary" disabled={busy || !body.trim()}>
          Comment
        </Button>
      </div>
    </form>
  );
}
