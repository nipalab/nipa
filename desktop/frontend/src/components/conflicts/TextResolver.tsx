import { useId, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { AlertTriangle, Combine, Loader2 } from "lucide-react";
import { desktop, type ConflictFile, type ConflictSet, type ConflictTexts, type ResolveChoice } from "@/api/desktop";
import { Centered, Mono, ResolverHeader } from "@/components/conflicts/parts";
import { Button } from "@/components/ui/button";
import { compose, conflictCount, hasMarkers, mergesScene, segmentsFor, type BlockPick, type Segment } from "@/lib/conflicts";
import { cn, errorMessage } from "@/lib/utils";
import { Trans, useTranslation } from "react-i18next";
import { translate } from "@/i18n";

type TextResolverProps = { root: string; file: ConflictFile; set: ConflictSet; busy: boolean; onResolve: (choice: ResolveChoice, content?: string) => void };

export function TextResolver({ root, file, set, busy, onResolve }: TextResolverProps) {
  const { t } = useTranslation();
  const texts = useQuery({ queryKey: ["conflict-texts", root, file.path], queryFn: () => desktop.conflictTexts(root, file.path), staleTime: 0 });
  if (texts.isPending) {
    return (
      <Centered>
        <Loader2 className="size-4 animate-spin" /> {t("textResolver.loadingTheVersions")}
      </Centered>
    );
  }
  if (texts.isError) {
    return (
      <>
        <ResolverHeader file={file} set={set} busy={busy} onResolve={onResolve} />
        <Centered>
          <AlertTriangle className="size-4 text-danger" /> {errorMessage(texts.error)}
        </Centered>
      </>
    );
  }
  return <Editor root={root} file={file} set={set} texts={texts.data} busy={busy} onResolve={onResolve} />;
}

type EditorProps = TextResolverProps & { texts: ConflictTexts };

const pickLabels: [BlockPick, string][] = [
  ["ours", translate("textResolver.mine2")],
  ["theirs", translate("textResolver.theirs2")],
  ["ours-theirs", translate("textResolver.bothMineFirst")],
  ["theirs-ours", translate("textResolver.bothTheirsFirst")],
];

function Editor({ root, file, set, texts, busy, onResolve }: EditorProps) {
  const { t } = useTranslation();
  const segments = useMemo(() => segmentsFor(texts, file.kind), [texts, file.kind]);
  const blocks = segments.filter((s): s is Extract<Segment, { kind: "conflict" }> => s.kind === "conflict");
  const [picks, setPicks] = useState<(BlockPick | undefined)[]>(() => blocks.map(() => undefined));
  const [manual, setManual] = useState<string | null>(null);
  const [showBase, setShowBase] = useState(false);
  const resultId = useId();
  const result = manual ?? compose(segments, picks);
  const markers = hasMarkers(result);
  const picked = picks.filter(Boolean).length;

  const pick = (i: number, p: BlockPick) => {
    setManual(null);
    setPicks((prev) => prev.map((v, j) => (j === i ? p : v)));
  };

  return (
    <>
      <ResolverHeader file={file} set={set} busy={busy} onResolve={onResolve} />
      <div className="min-h-0 flex-1 space-y-3 overflow-auto p-4 text-[12px]">
        {mergesScene(file) && <SceneMerge root={root} path={file.path} busy={busy} onResolve={onResolve} />}
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <span>
            {conflictCount(segments) === 0
              ? t("textResolver.noMarkers")
              : t("textResolver.picked", { picked, count: blocks.length })}
            {conflictCount(segments) > 0 && (
              <>
                <Trans i18nKey="textResolver.theirsIs" values={{ ours: set.ours.label, theirs: set.theirs.label }} components={[<Mono key="a">{null}</Mono>, <Mono key="b">{null}</Mono>]} />
              </>
            )}
          </span>
          {!texts.base.missing && (
            <Button size="sm" variant="ghost" className="ml-auto" aria-pressed={showBase} onClick={() => setShowBase((v) => !v)}>
              {showBase ? t("textResolver.hideBase") : t("textResolver.showBase")}
            </Button>
          )}
        </div>
        {showBase && (
          <figure className="rounded-md border border-border">
            <figcaption className="border-b border-border px-2 py-1 text-[11px] text-muted-foreground">{t("textResolver.ancestorCaption", { label: set.base.label || t("textResolver.base") })}</figcaption>
            <pre className="max-h-60 overflow-auto p-2 font-mono text-[11px] whitespace-pre-wrap" aria-label={t("textResolver.commonAncestor")}>
              {texts.base.text}
            </pre>
          </figure>
        )}
        {blocks.map((b, i) => (
          <section key={i} aria-label={t("textResolver.conflictN", { n: i + 1 })} className="rounded-md border border-border">
            <header className="flex flex-wrap items-center gap-1 border-b border-border px-2 py-1">
              <span className="mr-auto text-[11px] font-medium">{t("textResolver.conflictN", { n: i + 1 })}</span>
              {pickLabels.map(([id, label]) => (
                <button
                  key={id}
                  type="button"
                  aria-pressed={picks[i] === id && manual === null}
                  onClick={() => pick(i, id)}
                  className={cn("rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground", picks[i] === id && manual === null && "bg-accent/15 text-foreground")}
                >
                  {label}
                </button>
              ))}
            </header>
            <div className="grid grid-cols-2 divide-x divide-border">
              <Side title={translate("textResolver.mine", { label: set.ours.label })} text={b.ours} chosen={picks[i] === "ours"} />
              <Side title={translate("textResolver.theirs", { label: set.theirs.label })} text={b.theirs} chosen={picks[i] === "theirs"} />
            </div>
          </section>
        ))}
        <div className="space-y-1">
          <span className="flex items-center gap-2 text-[11px] text-muted-foreground">
            <label htmlFor={resultId}>{t("textResolver.result")}</label>
            {manual !== null && (
              <>
                <span>{t("textResolver.editedByHand")}</span>
                <button type="button" className="text-accent hover:underline" onClick={() => setManual(null)}>
                  {t("textResolver.resetToPicks")}
                </button>
              </>
            )}
          </span>
          <textarea
            id={resultId}
            value={result}
            onChange={(e) => setManual(e.target.value)}
            spellCheck={false}
            rows={14}
            className="w-full resize-y rounded-md border border-border bg-background p-2 font-mono text-[11px] outline-none focus:border-accent"
          />
        </div>
        <div className="flex items-center justify-end gap-2">
          {markers && <span className="mr-auto text-[11px] text-warning">{t("textResolver.pickASideForEvery")}</span>}
          <Button size="sm" variant="primary" disabled={busy || markers} onClick={() => onResolve("content", result)}>
            {busy && <Loader2 className="animate-spin" />}
            {t("textResolver.markResolved")}
          </Button>
        </div>
      </div>
    </>
  );
}

// SceneMerge merges a Godot scene or resource by nodes and properties; a
// clean result resolves the file, conflicts leave the line editor below.
function SceneMerge({ root, path, busy, onResolve }: { root: string; path: string; busy: boolean; onResolve: TextResolverProps["onResolve"] }) {
  const { t } = useTranslation();
  const merge = useMutation({
    mutationFn: () => desktop.mergeScene(root, path),
    onSuccess: (m) => {
      if (m.clean) onResolve("content", m.merged);
    },
  });
  const conflicts = merge.data && !merge.data.clean ? merge.data.conflicts : [];
  return (
    <section aria-label={t("textResolver.sceneMerge")} className="space-y-1.5 rounded-md border border-border p-2">
      <div className="flex items-center gap-2">
        <p className="mr-auto text-[11px] text-muted-foreground">{t("textResolver.thisIsAGodotScene")}</p>
        <Button size="sm" disabled={busy || merge.isPending} onClick={() => merge.mutate()} title={t("textResolver.mergeBothSidesByNodes")}>
          {merge.isPending ? <Loader2 className="animate-spin" /> : <Combine />}
          {t("textResolver.mergeScene")}
        </Button>
      </div>
      {merge.isError && <p className="text-[11px] text-danger">{errorMessage(merge.error)}</p>}
      {conflicts.length > 0 && (
        <div className="space-y-1 text-[11px]">
          <p className="text-warning">
            {t("textResolver.sceneConflictsSummary", { count: conflicts.length })}
          </p>
          <ul className="space-y-0.5" aria-label={t("textResolver.sceneConflicts")}>
            {conflicts.map((c, i) => (
              <li key={i}>
                <Mono>
                  {c.section || translate("textResolver.fileHeader")}
                  {c.key ? translate("textResolver.text", { key: c.key }) : ""}
                </Mono>
                : {c.message}
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

function Side({ title, text, chosen }: { title: string; text: string; chosen: boolean }) {
  const { t } = useTranslation();
  return (
    <figure className={cn("min-w-0", chosen && "bg-success/10")}>
      <figcaption className="truncate px-2 pt-1 text-[10px] text-muted-foreground">{title}</figcaption>
      <pre className="max-h-48 overflow-auto px-2 pb-2 font-mono text-[11px] whitespace-pre-wrap">{text || <span className="italic text-muted-foreground">{t("textResolver.nothing")}</span>}</pre>
    </figure>
  );
}
