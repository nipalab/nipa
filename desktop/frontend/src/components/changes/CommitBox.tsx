import { useEffect, useRef, useState, type ReactNode } from "react";
import { AlertTriangle, ArrowUpFromLine, ChevronDown, ChevronRight, Loader2, Lock, Sparkles, X } from "lucide-react";
import type { Draft, PushChecks } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { useDraft } from "@/hooks/useGenerate";
import { hasPushIssues, hasPushReport } from "@/hooks/usePushChecks";
import { describeProgress, type OpState } from "@/hooks/useRepoOperation";
import { chordMatches, shortcut, shortcutHint } from "@/lib/shortcuts";

export type LockIssue = { message: string; action?: { label: string; onClick: () => void; busy?: boolean } };

type CommitBoxProps = {
  stagedCount: number;
  blockedReason?: string;
  lockIssue?: LockIssue;
  checks?: PushChecks;
  onSelectPath?: (paths: string[]) => void;
  op: OpState | null;
  busy?: boolean;
  message: string;
  onMessage: (message: string) => void;
  onPush: (message: string) => void;
  onCancel: () => void;
  onGenerate?: () => Promise<Draft>;
  focus?: number;
};

export function CommitBox({ stagedCount, blockedReason, lockIssue, checks, onSelectPath, op, busy, message, onMessage, onPush, onCancel, onGenerate, focus }: CommitBoxProps) {
  const draft = useDraft(message, (d) => onMessage(d.text));
  const box = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (focus) box.current?.focus();
  }, [focus]);
  const trimmed = message.trim();
  const report = stagedCount > 0 && hasPushReport(checks) ? checks : undefined;
  const anyway = hasPushIssues(report);
  const canPush = stagedCount > 0 && trimmed !== "" && !op && !busy && !blockedReason && !lockIssue;

  const submit = () => {
    if (!canPush) return;
    onPush(draft.finalize(trimmed));
  };

  return (
    <div className="shrink-0 space-y-2 border-t border-border p-3">
      <textarea
        ref={box}
        value={message}
        onChange={(e) => onMessage(e.target.value)}
        onKeyDown={(e) => {
          if (shortcut("push").chords.some((c) => chordMatches(c, e.nativeEvent))) {
            e.preventDefault();
            submit();
          }
        }}
        placeholder={`Describe your change (${shortcutHint("push")} to push)`}
        aria-label="Push message"
        rows={3}
        disabled={!!op}
        className="no-drag w-full resize-none rounded-md border border-border bg-background px-2.5 py-2 text-[12px] outline-none placeholder:text-muted-foreground focus:ring-2 focus:ring-ring"
      />
      {!op && onGenerate && (
        <div className="flex items-center gap-2 text-[11px]">
          <Button size="sm" variant="ghost" className="h-6 px-1.5" onClick={() => void draft.run(onGenerate)} disabled={draft.busy || busy || stagedCount === 0}>
            {draft.busy ? <Loader2 className="animate-spin" /> : <Sparkles />}
            {draft.busy ? "Generating…" : "Generate"}
          </Button>
          {draft.model && !draft.busy && <span className="min-w-0 truncate text-muted-foreground">Drafted by {draft.model}; review it before pushing</span>}
        </div>
      )}
      {!op && draft.error && (
        <p role="alert" className="text-[11px] text-danger">
          Could not generate a message: {draft.error}
        </p>
      )}
      {op ? <Progress op={op} onCancel={onCancel} /> : null}
      {!op && report && <ChecksBlock checks={report} onSelect={onSelectPath} />}
      {!op && (
        <Button variant="primary" className="w-full" disabled={!canPush} onClick={submit} title={blockedReason ?? lockIssue?.message}>
          <ArrowUpFromLine />
          {anyway ? "Push anyway" : stagedCount > 0 ? `Push ${stagedCount} file${stagedCount === 1 ? "" : "s"}` : "Push"}
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

function ChecksBlock({ checks, onSelect }: { checks: PushChecks; onSelect?: (paths: string[]) => void }) {
  const [open, setOpen] = useState(true);
  const Chevron = open ? ChevronDown : ChevronRight;
  const issues = checks.violations.length + checks.broken.length + checks.newlyUnused.length;
  const select = (...paths: string[]) => onSelect?.(paths.filter(Boolean));
  return (
    <div className="rounded-md border border-warning/40 bg-warning/10 text-[11px]" role="region" aria-label="Checks">
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex w-full items-center gap-1.5 px-2 py-1.5 text-left">
        <AlertTriangle className="size-3 shrink-0 text-warning" />
        <span className="flex-1 font-medium">{issues > 0 ? `Checks: ${issues} warning${issues === 1 ? "" : "s"}` : "Checks"}</span>
        <Chevron className="size-3 text-muted-foreground" />
      </button>
      {open && (
        <div className="max-h-48 space-y-2 overflow-y-auto px-2 pb-2">
          {checks.violations.length > 0 && (
            <CheckGroup title="Over budget">
              {checks.violations.map((v) => (
                <CheckRow key={`${v.path}:${v.limit}`} title={v.path} onClick={() => select(v.path)}>
                  <span className="block truncate font-mono">{v.path}</span>
                  <span className="block text-muted-foreground">
                    <span className="font-medium text-warning">{v.limit}</span> (rule {v.rule}): {v.message}
                  </span>
                </CheckRow>
              ))}
            </CheckGroup>
          )}
          {checks.broken.length + checks.newlyUnused.length > 0 && (
            <CheckGroup title="Broken references / No longer used">
              {checks.broken.map((b) => (
                <CheckRow key={`${b.from}:${b.raw}`} title={b.from} onClick={() => select(b.from, b.target)}>
                  <span className="block truncate font-mono">{b.from}</span>
                  <span className="block text-muted-foreground">
                    <span className="font-medium text-warning">{b.reason}</span> {b.target || b.raw}
                  </span>
                </CheckRow>
              ))}
              {checks.newlyUnused.map((p) => (
                <CheckRow key={`unused:${p}`} title={p} onClick={() => select(p)}>
                  <span className="block truncate font-mono">{p}</span>
                  <span className="block text-muted-foreground">No longer used by any scene or asset</span>
                </CheckRow>
              ))}
            </CheckGroup>
          )}
          {checks.truncated && <p className="text-muted-foreground">Reference scan stopped after {checks.scanned} files; the list may be incomplete.</p>}
          {checks.warnings.map((w) => (
            <p key={w} className="text-muted-foreground">
              {w}
            </p>
          ))}
        </div>
      )}
    </div>
  );
}

function CheckGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <p className="mb-0.5 font-medium">{title}</p>
      <ul aria-label={title}>{children}</ul>
    </div>
  );
}

function CheckRow({ title, onClick, children }: { title: string; onClick: () => void; children: ReactNode }) {
  return (
    <li>
      <button type="button" onClick={onClick} title={title} className="w-full rounded px-1 py-0.5 text-left hover:bg-background">
        {children}
      </button>
    </li>
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
