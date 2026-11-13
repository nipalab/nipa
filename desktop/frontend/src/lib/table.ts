import type { TableColumn, TableRow } from "@/api/desktop";

export const isChange = (r: TableRow) => r.status !== "same" || !!r.moved;

export function columnChanges(columns: TableColumn[], rows: TableRow[]): number[] {
  const counts = columns.map(() => 0);
  for (const r of rows) for (const i of r.changed ?? []) if (i < counts.length) counts[i]++;
  return counts;
}

const columnChanged = (c: TableColumn, count: number) => count > 0 || c.old < 0 || c.new < 0 || (c.oldName !== "" && c.oldName !== c.name);

// visibleColumns orders the shown column indexes with the key column first.
export function visibleColumns(columns: TableColumn[], counts: number[], keyColumn: number, changedOnly: boolean): number[] {
  const shown = columns.map((_, i) => i).filter((i) => !changedOnly || i === keyColumn || columnChanged(columns[i], counts[i]));
  return keyColumn >= 0 && shown.includes(keyColumn) ? [keyColumn, ...shown.filter((i) => i !== keyColumn)] : shown;
}

export type RowFilter = { onlyChanges: boolean; text: string; column: number };

export function filterRows(rows: TableRow[], { onlyChanges, text, column }: RowFilter): TableRow[] {
  const needle = text.trim().toLocaleLowerCase();
  const hit = (v: string | undefined) => !!v && v.toLocaleLowerCase().includes(needle);
  return rows.filter((r) => {
    if (onlyChanges && !isChange(r)) return false;
    if (!needle) return true;
    const cells = r.cells ?? [];
    if (column >= 0) return hit(cells[column]) || hit(r.old?.[column]);
    return cells.some(hit) || (r.old ?? []).some(hit);
  });
}

// nextChange returns the index of the next (dir 1) or previous (dir -1)
// changed row after from, wrapping around; -1 when nothing changed.
export function nextChange(rows: TableRow[], from: number, dir: 1 | -1): number {
  const n = rows.length;
  for (let step = 1; step <= n; step++) {
    const i = (((from + dir * step) % n) + n) % n;
    if (isChange(rows[i])) return i;
  }
  return -1;
}
