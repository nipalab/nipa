import { ArrowUpFromLine, Lock, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { describeProgress, type OpState } from "@/hooks/useRepoOperation";

export type LockIssue = { message: string; action?: { label: string; onClick: () => void; busy?: boolean } };

type CommitBoxProps = {
  stagedCount: number;
  blockedReason?: string;
  lockIssue?: LockIssue;
  op: OpState | null;
  busy?: boolean;
  message: string;
  onMessage: (message: string) => void;
  onPush: (message: string) => void;
  onCancel: () => void;
};

export function CommitBox({ stagedCount, blockedReason, lockIssue, op, busy, message, onMessage, onPush, onCancel }: CommitBoxProps) {
  const trimmed = message.trim();
  const canPush = stagedCount > 0 && trimmed !== "" && !op && !busy && !blockedReason && !lockIssue;

  const submit = () => {
    if (!canPush) return;
    onPush(trimmed);
  };

  return (
    <div className="shrink-0 space-y-2 border-t border-border p-3">
      <textarea
        value={message}
        onChange={(e) => onMessage(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            submit();
          }
        }}
        placeholder="Describe your change"
        aria-label="Push message"
        rows={3}
        disabled={!!op}
        className="no-drag w-full resize-none rounded-md border border-border bg-background px-2.5 py-2 text-[12px] outline-none placeholder:text-muted-foreground focus:ring-2 focus:ring-ring"
      />
      {op ? <Progress op={op} onCancel={onCancel} /> : null}
      {!op && (
        <Button variant="primary" className="w-full" disabled={!canPush} onClick={submit} title={blockedReason ?? lockIssue?.message}>
          <ArrowUpFromLine />
          {stagedCount > 0 ? `Push ${stagedCount} file${stagedCount === 1 ? "" : "s"}` : "Push"}
        </Button>
      )}
      {!op && blockedReason && <p className="text-[11px] text-muted-foreground">{blockedReason}</p>}
      {!op && lockIssue && (
        <div className="space-y-1.5 rounded-md border border-warning/40 bg-warning/10 p-2 text-[11px]" role="alert">
          <p className="flex items-start gap-1.5">
            <Lock className="mt-px size-3 shrink-0 text-warning" />
            <span>{lockIssue.message}</span>
          </p>
          {lockIssue.action && (
            <Button size="sm" className="w-full" onClick={lockIssue.action.onClick} disabled={lockIssue.action.busy || busy}>
              <Lock />
              {lockIssue.action.label}
            </Button>
          )}
        </div>
      )}
      {!op && !blockedReason && stagedCount === 0 && <p className="text-[11px] text-muted-foreground">Tick the files you want to push.</p>}
    </div>
  );
}

export function Progress({ op, onCancel }: { op: OpState; onCancel: () => void }) {
  const { label, percent } = describeProgress(op);
  return (
    <div className="space-y-1.5" role="status" aria-live="polite">
      <div className="flex items-center gap-2 text-[11px]">
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <Button variant="ghost" size="sm" className="h-6 px-1.5" onClick={onCancel} disabled={op.cancelling} aria-label={`Cancel ${op.op}`}>
          <X />
          Cancel
        </Button>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-muted">
        <div
          className={percent === undefined ? "h-full w-1/3 animate-pulse rounded-full bg-accent" : "h-full rounded-full bg-accent transition-[width]"}
          style={percent === undefined ? undefined : { width: `${percent}%` }}
          role="progressbar"
          aria-valuenow={percent}
          aria-valuemin={0}
          aria-valuemax={100}
        />
      </div>
    </div>
  );
}
