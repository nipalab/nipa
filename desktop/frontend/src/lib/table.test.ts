import { describe, expect, it } from "vitest";
import type { TableColumn, TableRow } from "@/api/desktop";
import { changedColumns, columnChanges, filterRows, isChange, nextChange, visibleColumns } from "./table";

const row = (over: Partial<TableRow>): TableRow => ({ status: "same", oldLine: 2, newLine: 2, cells: [], old: null, changed: null, ...over });
const col = (name: string, over: Partial<TableColumn> = {}): TableColumn => ({ name, oldName: name, old: 0, new: 0, ...over });

const rows = [
  row({ cells: ["1", "Iron Sword", "12"] }),
  row({ status: "changed", cells: ["2", "Storm Bow", "22"], old: ["2", "Storm Bow", "20"], changed: [2] }),
  row({ cells: ["3", "Ember Staff", "18"], moved: true }),
  row({ status: "added", cells: ["4", "Pike", "28"] }),
  row({ cells: ["5", "Dagger", "8"] }),
  row({ status: "changed", cells: ["6", "Axe", "9"], old: ["6", "Hatchet", "9"], changed: [1, 7] }),
];

describe("table helpers", () => {
  it("counts changed cells per column", () => {
    expect(columnChanges([col("id"), col("name"), col("damage")], rows)).toEqual([1, 2, 2]);
  });

  it("puts the key column first and hides unchanged columns on request", () => {
    const columns = [col("name"), col("id"), col("damage"), col("added", { old: -1 }), col("gone", { new: -1 }), col("label", { oldName: "title" }), col("fresh", { oldName: "" })];
    const counts = changedColumns(columns, columnChanges(columns, [row({ status: "changed", cells: ["a", "1", "9", "", "", "", ""], changed: [2] })]));
    expect(counts).toEqual([false, false, true, true, true, true, false]);
    expect(visibleColumns(columns, counts, 1, false)).toEqual([1, 0, 2, 3, 4, 5, 6]);
    expect(visibleColumns(columns, counts, 1, true)).toEqual([1, 2, 3, 4, 5]);
    expect(visibleColumns(columns, counts, -1, true)).toEqual([2, 3, 4, 5]);
  });

  it("treats values of added and removed rows as changed columns", () => {
    const columns = [col("id"), col("name"), col("note"), col("hp")];
    const appended = [row({ cells: ["1", "a", "", "3"] }), row({ status: "added", cells: ["2", "b", "", "4", "extra"] }), row({ status: "removed", cells: ["3", "", "", "5"] })];
    expect(columnChanges(columns, appended)).toEqual([2, 1, 0, 2]);
    expect(visibleColumns(columns, changedColumns(columns, columnChanges(columns, appended)), -1, true)).toEqual([0, 1, 3]);
    expect(columnChanges(columns, [row({ status: "added", cells: null as unknown as string[] })])).toEqual([0, 0, 0, 0]);
  });

  it("filters by change, text and column", () => {
    expect(rows.filter(isChange)).toHaveLength(4);
    expect(filterRows(rows, { onlyChanges: true, text: "", column: -1 }).map((r) => r.cells![0])).toEqual(["2", "3", "4", "6"]);
    expect(filterRows(rows, { onlyChanges: false, text: " STORM ", column: -1 }).map((r) => r.cells![0])).toEqual(["2"]);
    expect(filterRows(rows, { onlyChanges: false, text: "hatchet", column: -1 }).map((r) => r.cells![0])).toEqual(["6"]);
    expect(filterRows(rows, { onlyChanges: false, text: "2", column: 0 }).map((r) => r.cells![0])).toEqual(["2"]);
    expect(filterRows(rows, { onlyChanges: false, text: "20", column: 2 }).map((r) => r.cells![0])).toEqual(["2"]);
    expect(filterRows([row({ cells: null as unknown as string[] })], { onlyChanges: false, text: "x", column: -1 })).toEqual([]);
  });

  it("steps through changes with wrap-around", () => {
    expect(nextChange(rows, -1, 1)).toBe(1);
    expect(nextChange(rows, 1, 1)).toBe(2);
    expect(nextChange(rows, 5, 1)).toBe(1);
    expect(nextChange(rows, 1, -1)).toBe(5);
    expect(nextChange(rows, -1, -1)).toBe(5);
    expect(nextChange(rows, 3, -1)).toBe(2);
    expect(nextChange([row({})], 0, 1)).toBe(-1);
    expect(nextChange([], -1, 1)).toBe(-1);
  });
});
