import { useEffect, useRef, useState, type ReactNode } from "react";
import { AlertTriangle, ArrowUpFromLine, ChevronDown, ChevronRight, Loader2, Lock, Sparkles, X } from "lucide-react";
import type { Draft, PushChecks } from "@/api/desktop";
import { Button } from "@/components/ui/button";
import { useDraft } from "@/hooks/useGenerate";
import { labels, useSimple } from "@/lib/mode";
import { hasPushIssues, hasPushReport } from "@/hooks/usePushChecks";
import { describeProgress, type OpState } from "@/hooks/useRepoOperation";
import { chordMatches, shortcut, shortcutHint } from "@/lib/shortcuts";
import { Trans, useTranslation } from "react-i18next";
import { locale, translate } from "@/i18n";

export const LARGE_PUSH_FILES = 10_000;

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
  const { t } = useTranslation();
  const draft = useDraft(message, (d) => onMessage(d.text));
  const box = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (focus) box.current?.focus();
  }, [focus]);
  const trimmed = message.trim();
  const simple = useSimple();
  const verb = labels(simple).push;
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
        placeholder={translate("commitBox.describeYourChangeTo", { value: shortcutHint("push"), toLowerCase: verb.toLowerCase() })}
        aria-label={translate("commitBox.message", { verb })}
        rows={3}
        disabled={!!op}
        className="no-drag w-full resize-none rounded-md border border-border bg-background px-2.5 py-2 text-[12px] outline-none placeholder:text-muted-foreground focus:ring-2 focus:ring-ring"
      />
      {!op && onGenerate && (
        <div className="flex items-center gap-2 text-[11px]">
          <Button size="sm" variant="ghost" className="h-6 px-1.5" onClick={() => void draft.run(onGenerate)} disabled={draft.busy || busy || stagedCount === 0}>
            {draft.busy ? <Loader2 className="animate-spin" /> : <Sparkles />}
            {draft.busy ? translate("commitBox.generating") : translate("commitBox.generate")}
          </Button>
          {draft.model && !draft.busy && <span className="min-w-0 truncate text-muted-foreground">{t("commitBox.draftedBy", { model: draft.model })}</span>}
        </div>
      )}
      {!op && draft.error && (
        <p role="alert" className="text-[11px] text-danger">
          {t("commitBox.generateFailed", { error: draft.error })}
        </p>
      )}
      {op ? <Progress op={op} onCancel={onCancel} /> : null}
      {!op && report && <ChecksBlock checks={report} onSelect={onSelectPath} />}
      {!op && stagedCount > LARGE_PUSH_FILES && (
        <p role="note" className="flex items-start gap-1.5 rounded-md border border-warning/40 bg-warning/10 p-2 text-[11px]">
          <AlertTriangle className="mt-px size-3 shrink-0 text-warning" />
          <span>{t("commitBox.largePush", { count: stagedCount, files: stagedCount.toLocaleString(locale()), verb: verb.toLowerCase() })}</span>
        </p>
      )}
      {!op && (
        <Button variant="primary" className="w-full" disabled={!canPush} onClick={submit} title={blockedReason ?? lockIssue?.message}>
          <ArrowUpFromLine />
          {anyway ? translate("commitBox.anyway", { verb }) : stagedCount > 0 ? translate("commitBox.withCount", { verb, count: stagedCount }) : verb}
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
      {!op && !blockedReason && stagedCount === 0 && <p className="text-[11px] text-muted-foreground">{t("commitBox.tickFiles", { verb: verb.toLowerCase() })}</p>}
    </div>
  );
}

function ChecksBlock({ checks, onSelect }: { checks: PushChecks; onSelect?: (paths: string[]) => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(true);
  const Chevron = open ? ChevronDown : ChevronRight;
  const issues = checks.violations.length + checks.broken.length + checks.newlyUnused.length;
  const select = (...paths: string[]) => onSelect?.(paths.filter(Boolean));
  return (
    <div className="rounded-md border border-warning/40 bg-warning/10 text-[11px]" role="region" aria-label={t("commitBox.checks")}>
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex w-full items-center gap-1.5 px-2 py-1.5 text-left">
        <AlertTriangle className="size-3 shrink-0 text-warning" />
        <span className="flex-1 font-medium">{issues > 0 ? translate("commitBox.checksWarnings", { count: issues }) : translate("commitBox.checks")}</span>
        <Chevron className="size-3 text-muted-foreground" />
      </button>
      {open && (
        <div className="max-h-48 space-y-2 overflow-y-auto px-2 pb-2">
          {checks.violations.length > 0 && (
            <CheckGroup title={t("commitBox.overBudget")}>
              {checks.violations.map((v) => (
                <CheckRow key={`${v.path}:${v.limit}`} title={v.path} onClick={() => select(v.path)}>
                  <span className="block truncate font-mono">{v.path}</span>
                  <span className="block text-muted-foreground">
                    <Trans i18nKey="commitBox.ruleLimit" values={{ limit: v.limit, rule: v.rule, message: v.message }} components={[<span className="font-medium text-warning" />]} />
                  </span>
                </CheckRow>
              ))}
            </CheckGroup>
          )}
          {checks.broken.length + checks.newlyUnused.length > 0 && (
            <CheckGroup title={t("commitBox.brokenReferencesNoLonger")}>
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
                  <span className="block text-muted-foreground">{t("commitBox.noLongerUsedByAny")}</span>
                </CheckRow>
              ))}
            </CheckGroup>
          )}
          {checks.truncated && <p className="text-muted-foreground">{t("commitBox.scanStopped", { count: checks.scanned })}</p>}
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
  const { t } = useTranslation();
  const { label, percent } = describeProgress(op);
  return (
    <div className="space-y-1.5" role="status" aria-live="polite">
      <div className="flex items-center gap-2 text-[11px]">
        <span className="min-w-0 flex-1 truncate">{label}</span>
        <Button variant="ghost" size="sm" className="h-6 px-1.5" onClick={onCancel} disabled={op.cancelling} aria-label={translate("commitBox.cancel2", { op: op.op })}>
          <X />
          {t("commitBox.cancel")}
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
