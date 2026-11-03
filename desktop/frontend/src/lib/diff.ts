import type { DiffHunk } from "@/api/desktop";

export type LineKind = "context" | "added" | "removed";

export type UnifiedRow =
  | { type: "hunk"; header: string }
  | { type: "line"; kind: LineKind; old: number; new: number; text: string; noNewline: boolean };

export type SplitCell = { kind: LineKind; line: number; text: string; noNewline: boolean } | null;

export type SplitRow = { type: "hunk"; header: string } | { type: "pair"; left: SplitCell; right: SplitCell };

export function hunkHeader(h: DiffHunk): string {
  return `@@ -${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@`;
}

const kindOf = (kind: string): LineKind => (kind === "added" || kind === "removed" ? kind : "context");

export function unifiedRows(hunks: DiffHunk[]): UnifiedRow[] {
  const rows: UnifiedRow[] = [];
  for (const h of hunks) {
    rows.push({ type: "hunk", header: hunkHeader(h) });
    for (const l of h.lines ?? []) {
      rows.push({ type: "line", kind: kindOf(l.kind), old: l.old, new: l.new, text: l.text, noNewline: !!l.noNewline });
    }
  }
  return rows;
}

// splitRows pairs each run of removed lines with the added run that follows,
// the way side-by-side diff viewers line up an edited block.
export function splitRows(hunks: DiffHunk[]): SplitRow[] {
  const rows: SplitRow[] = [];
  for (const h of hunks) {
    rows.push({ type: "hunk", header: hunkHeader(h) });
    let removed: SplitCell[] = [];
    let added: SplitCell[] = [];
    const flush = () => {
      for (let i = 0; i < Math.max(removed.length, added.length); i++) {
        rows.push({ type: "pair", left: removed[i] ?? null, right: added[i] ?? null });
      }
      removed = [];
      added = [];
    };
    for (const l of h.lines ?? []) {
      const kind = kindOf(l.kind);
      const noNewline = !!l.noNewline;
      if (kind === "removed") {
        if (added.length > 0) flush();
        removed.push({ kind, line: l.old, text: l.text, noNewline });
      } else if (kind === "added") {
        added.push({ kind, line: l.new, text: l.text, noNewline });
      } else {
        flush();
        rows.push({
          type: "pair",
          left: { kind, line: l.old, text: l.text, noNewline },
          right: { kind, line: l.new, text: l.text, noNewline },
        });
      }
    }
    flush();
  }
  return rows;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = n / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[unit]}`;
}

export function splitPath(path: string): { dir: string; name: string } {
  const i = path.lastIndexOf("/");
  return i < 0 ? { dir: "", name: path } : { dir: path.slice(0, i + 1), name: path.slice(i + 1) };
}
