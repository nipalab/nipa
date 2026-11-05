import { useId, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Loader2 } from "lucide-react";
import { desktop, type ConflictFile, type ConflictSet, type ConflictTexts, type ResolveChoice } from "@/api/desktop";
import { Centered, Mono, ResolverHeader } from "@/components/conflicts/parts";
import { Button } from "@/components/ui/button";
import { compose, conflictCount, hasMarkers, segmentsFor, type BlockPick, type Segment } from "@/lib/conflicts";
import { cn, errorMessage } from "@/lib/utils";

type TextResolverProps = { root: string; file: ConflictFile; set: ConflictSet; busy: boolean; onResolve: (choice: ResolveChoice, content?: string) => void };

export function TextResolver({ root, file, set, busy, onResolve }: TextResolverProps) {
  const texts = useQuery({ queryKey: ["conflict-texts", root, file.path], queryFn: () => desktop.conflictTexts(root, file.path), staleTime: 0 });
  if (texts.isPending) {
    return (
      <Centered>
        <Loader2 className="size-4 animate-spin" /> Loading the versions…
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
  return <Editor file={file} set={set} texts={texts.data} busy={busy} onResolve={onResolve} />;
}

type EditorProps = Omit<TextResolverProps, "root"> & { texts: ConflictTexts };

const pickLabels: [BlockPick, string][] = [
  ["ours", "Mine"],
  ["theirs", "Theirs"],
  ["ours-theirs", "Both, mine first"],
  ["theirs-ours", "Both, theirs first"],
];

function Editor({ file, set, texts, busy, onResolve }: EditorProps) {
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
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <span>
            {conflictCount(segments) === 0
              ? "No conflict markers left in this file."
              : `${picked} of ${blocks.length} conflict${blocks.length === 1 ? "" : "s"} picked. Mine is `}
            {conflictCount(segments) > 0 && (
              <>
                <Mono>{set.ours.label}</Mono>, theirs is <Mono>{set.theirs.label}</Mono>.
              </>
            )}
          </span>
          {!texts.base.missing && (
            <Button size="sm" variant="ghost" className="ml-auto" aria-pressed={showBase} onClick={() => setShowBase((v) => !v)}>
              {showBase ? "Hide" : "Show"} common ancestor
            </Button>
          )}
        </div>
        {showBase && (
          <figure className="rounded-md border border-border">
            <figcaption className="border-b border-border px-2 py-1 text-[11px] text-muted-foreground">Common ancestor ({set.base.label || "base"})</figcaption>
            <pre className="max-h-60 overflow-auto p-2 font-mono text-[11px] whitespace-pre-wrap" aria-label="Common ancestor">
              {texts.base.text}
            </pre>
          </figure>
        )}
        {blocks.map((b, i) => (
          <section key={i} aria-label={`Conflict ${i + 1}`} className="rounded-md border border-border">
            <header className="flex flex-wrap items-center gap-1 border-b border-border px-2 py-1">
              <span className="mr-auto text-[11px] font-medium">Conflict {i + 1}</span>
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
              <Side title={`Mine (${set.ours.label})`} text={b.ours} chosen={picks[i] === "ours"} />
              <Side title={`Theirs (${set.theirs.label})`} text={b.theirs} chosen={picks[i] === "theirs"} />
            </div>
          </section>
        ))}
        <div className="space-y-1">
          <span className="flex items-center gap-2 text-[11px] text-muted-foreground">
            <label htmlFor={resultId}>Result</label>
            {manual !== null && (
              <>
                <span>· edited by hand</span>
                <button type="button" className="text-accent hover:underline" onClick={() => setManual(null)}>
                  Reset to picks
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
          {markers && <span className="mr-auto text-[11px] text-warning">Pick a side for every conflict, or remove the markers by hand.</span>}
          <Button size="sm" variant="primary" disabled={busy || markers} onClick={() => onResolve("content", result)}>
            {busy && <Loader2 className="animate-spin" />}
            Mark resolved
          </Button>
        </div>
      </div>
    </>
  );
}

function Side({ title, text, chosen }: { title: string; text: string; chosen: boolean }) {
  return (
    <figure className={cn("min-w-0", chosen && "bg-success/10")}>
      <figcaption className="truncate px-2 pt-1 text-[10px] text-muted-foreground">{title}</figcaption>
      <pre className="max-h-48 overflow-auto px-2 pb-2 font-mono text-[11px] whitespace-pre-wrap">{text || <span className="italic text-muted-foreground">(nothing)</span>}</pre>
    </figure>
  );
}
