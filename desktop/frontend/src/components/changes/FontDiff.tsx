import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { desktop, type FileDiff, type FontChanges, type FontCompare, type FontSide } from "@/api/desktop";
import { imageRefs, viewerKey } from "@/components/changes/ImageDiff";
import { MetaDiff } from "@/components/changes/Notices";
import { Notice } from "@/components/changes/Toolbar";
import { formatBytes } from "@/lib/diff";
import { fontRows, formatRange, sampleChars, type CodeRange } from "@/lib/font";
import { cn } from "@/lib/utils";

export function FontDiff({ root, diff }: { root: string; diff: FileDiff }) {
  const { old, next } = imageRefs(diff);
  const query = useQuery({
    queryKey: viewerKey(root, diff, "font"),
    queryFn: () => desktop.fontDiff(root, old, next),
    staleTime: 30_000,
  });
  if (query.isPending) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Reading font…
      </div>
    );
  }
  if (query.isError) return <MetaDiff diff={diff} detail={`The font could not be read: ${String((query.error as Error)?.message ?? query.error)}`} />;
  if (!query.data || (!query.data.old && !query.data.new)) return <MetaDiff diff={diff} detail="There is no preview for this font." />;
  return <FontCompareView compare={query.data} />;
}

export function FontCompareView({ compare }: { compare: FontCompare }) {
  const { old, new: next, changes } = compare;
  const both = !!old && !!next;
  const notices: string[] = [];
  for (const [label, s] of [["Before", old], ["After", next]] as const) {
    if (s?.error) notices.push(`${label}: ${s.error}`);
    if (s?.info?.rangesTruncated) notices.push(`${label}: only the first character ranges are listed.`);
  }
  if (changes?.truncated) notices.push("Only the first changed ranges are listed.");
  return (
    <div className="flex h-full min-h-0 flex-col overflow-auto bg-card">
      {changes && <ChangeSummary changes={changes} />}
      <div className={cn("grid gap-3 p-3", both ? "grid-cols-2" : "grid-cols-1")}>
        {old && <Sheet label={next ? "Before" : "Deleted"} side={old} />}
        {next && <Sheet label={old ? "After" : "Added"} side={next} />}
      </div>
      <div className="flex flex-wrap items-start gap-x-8 gap-y-3 border-t border-border px-4 py-3 text-[11px]">
        <DetailsTable old={old} next={next} />
        <Scripts old={old} next={next} />
        {notices.length > 0 && (
          <div className="flex flex-col gap-1">
            {notices.map((n) => (
              <Notice key={n}>{n}</Notice>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ChangeSummary({ changes }: { changes: FontChanges }) {
  const same = changes.addedCount === 0 && changes.removedCount === 0 && (changes.metrics ?? []).length === 0;
  return (
    <section aria-label="Font changes" className="border-b border-border px-4 py-2 text-[11px]">
      {same ? (
        <p className="text-muted-foreground">Same characters and metrics; only the outlines or tables changed.</p>
      ) : (
        <div className="flex flex-wrap gap-x-8 gap-y-2">
          <RangeList title={`${changes.addedCount.toLocaleString()} characters added`} ranges={changes.added ?? []} className="text-success" />
          <RangeList title={`${changes.removedCount.toLocaleString()} characters removed`} ranges={changes.removed ?? []} className="text-danger" />
          {(changes.metrics ?? []).length > 0 && (
            <div>
              <p className="mb-1 font-medium">Changed</p>
              <ul className="space-y-0.5 font-mono">
                {(changes.metrics ?? []).map((m) => (
                  <li key={m.name}>
                    <span className="font-sans text-muted-foreground">{m.name}</span> {m.old} → <span className="text-warning">{m.new}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  );
}

const shownRanges = 12;

function RangeList({ title, ranges, className }: { title: string; ranges: CodeRange[]; className: string }) {
  if (ranges.length === 0) return null;
  return (
    <div>
      <p className={cn("mb-1 font-medium", className)}>{title}</p>
      <ul className="space-y-0.5">
        {ranges.slice(0, shownRanges).map((r) => (
          <li key={r.first} className="flex gap-2">
            <span className="font-mono text-muted-foreground">{formatRange(r)}</span>
            <span>{sampleChars(r)}</span>
          </li>
        ))}
        {ranges.length > shownRanges && <li className="text-muted-foreground">and {ranges.length - shownRanges} more ranges</li>}
      </ul>
    </div>
  );
}

function Sheet({ label, side }: { label: string; side: FontSide }) {
  const info = side.info;
  return (
    <section aria-label={label} className="rounded-md border border-border bg-background">
      <header className="flex items-center gap-2 border-b border-border px-2 py-1 text-[11px]">
        <span className="font-medium">{label}</span>
        <span className="truncate text-muted-foreground">{info ? [info.family, info.style].filter(Boolean).join(" ") : side.path}</span>
        <span className="ml-auto uppercase text-muted-foreground">{side.format}</span>
      </header>
      {side.sheet ? (
        <img src={side.sheet} alt={`${label} glyphs`} className="block max-w-full p-2 dark:invert" />
      ) : (
        <p className="p-3 text-[11px] text-muted-foreground">{side.error || "No glyphs to show for this version."}</p>
      )}
    </section>
  );
}

function DetailsTable({ old, next }: { old: FontSide | null; next: FontSide | null }) {
  const [a, b] = [old?.info ?? null, next?.info ?? null];
  return (
    <table aria-label="Font details">
      <thead>
        <tr className="text-muted-foreground">
          <th />
          <th className="px-3 text-left font-medium">Before</th>
          <th className="px-3 text-left font-medium">After</th>
        </tr>
      </thead>
      <tbody className="font-mono">
        <tr>
          <th className="pr-3 text-left font-sans font-normal text-muted-foreground">File size</th>
          <td className="px-3">{old ? formatBytes(old.size) : "—"}</td>
          <td className="px-3">{next ? formatBytes(next.size) : "—"}</td>
        </tr>
        {fontRows.map((r) => {
          const changed = !!a && !!b && r.value(a) !== r.value(b);
          return (
            <tr key={r.label}>
              <th className="pr-3 text-left font-sans font-normal text-muted-foreground">{r.label}</th>
              <td className="px-3">{a ? String(r.value(a)) : "—"}</td>
              <td className={cn("px-3", changed && "font-semibold text-warning")}>{b ? String(r.value(b)) : "—"}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function Scripts({ old, next }: { old: FontSide | null; next: FontSide | null }) {
  const info = next?.info ?? old?.info;
  if (!info || (info.scripts ?? []).length === 0) return null;
  return (
    <div>
      <p className="mb-1 font-medium">Scripts{next?.info && old?.info ? " (after)" : ""}</p>
      <ul className="space-y-0.5">
        {(info.scripts ?? []).slice(0, 10).map((s) => (
          <li key={s.script} className="flex justify-between gap-4">
            <span>{s.script}</span>
            <span className="font-mono text-muted-foreground">{s.count.toLocaleString()}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
