import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { FileDiff, TableCompare, TableRow } from "@/api/desktop";
import { renderWithProviders } from "@/test/render";

const api = vi.hoisted(() => ({ tableDiff: vi.fn() }));
vi.mock("@/api/desktop", () => ({ desktop: api }));

import { DiffViewer, viewerFor } from "./DiffViewer";
import { columnIds, TableCompareView, TableDiff } from "./TableDiff";

const row = (over: Partial<TableRow>): TableRow => ({ status: "same", oldLine: 2, newLine: 2, cells: ["a", "b", "c"], old: null, changed: null, ...over });

const compare = (over: Partial<TableCompare> = {}): TableCompare => ({
  columns: [
    { name: "key", oldName: "key", old: 0, new: 0 },
    { name: "en", oldName: "english", old: 1, new: 1 },
    { name: "de", oldName: "", old: -1, new: 2 },
    { name: "fr", oldName: "fr", old: 2, new: -1 },
  ],
  rows: [
    row({ cells: ["hello", "Hi", "Hallo", "Salut"], old: ["hello", "Hello", "", "Salut"], changed: [1], status: "changed" }),
    row({ cells: ["bye", "Bye", "", "Au revoir"], status: "removed", newLine: 0, oldLine: 3 }),
    row({ cells: ["yes", "Yes", "Ja", "Oui"], oldLine: 4, newLine: 3 }),
    row({ cells: ["no", "No", "Nein", "Non"], oldLine: 5, newLine: 5, moved: true }),
    row({ cells: ["maybe", "Maybe", "Vielleicht", ""], status: "added", oldLine: 0, newLine: 4 }),
  ],
  keyColumn: 0,
  delimiter: ",",
  rowsAdded: 1,
  rowsRemoved: 1,
  rowsChanged: 1,
  rowsMoved: 1,
  cellsChanged: 1,
  columnsAdded: 1,
  columnsRemoved: 1,
  ...over,
});

const diff = (over: Partial<FileDiff> = {}): FileDiff => ({
  path: "loc/strings.csv",
  status: "modified",
  kind: "table",
  old: { path: "loc/strings.csv", mode: 420, size: 40, binary: false, hash: "aa" },
  new: { path: "loc/strings.csv", mode: 420, size: 50, binary: false, hash: "bb" },
  additions: 1,
  deletions: 1,
  hunks: [{ oldStart: 2, oldLines: 1, newStart: 2, newLines: 1, lines: [{ kind: "removed", old: 2, new: 0, text: "hello,Hello" }, { kind: "added", old: 0, new: 2, text: "hello,Hi" }] }],
  ...over,
});

beforeEach(() => vi.resetAllMocks());

describe("TableCompareView", () => {
  it("shows rows with cell changes, column changes and a summary", async () => {
    renderWithProviders(<TableCompareView compare={compare()} />);
    const summary = screen.getByLabelText("Table summary");
    expect(summary).toHaveTextContent("+1 row−1 row1 changed (1 cell)1 moved+1 column−1 column");
    expect(screen.getByText("Rows matched by key")).toBeInTheDocument();
    expect(screen.getByText("(was english)")).toBeInTheDocument();

    const table = screen.getByRole("table", { name: "Table changes" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.getAttribute("data-status"))).toEqual(["changed", "removed", "same", "same", "added"]);
    expect(within(rows[0]).getByText("Hello")).toHaveClass("line-through");
    expect(within(rows[0]).getByTitle("Hello → Hi")).toBeInTheDocument();
    expect(within(rows[3]).getByLabelText("Moved")).toBeInTheDocument();
    expect(within(rows[1]).getByRole("rowheader")).toHaveTextContent("3→·");

    await userEvent.click(screen.getByRole("checkbox", { name: "Only changed rows" }));
    expect(within(table).getAllByRole("row")).toHaveLength(5);
  });

  it("starts with only changes on long tables and explains empty results", async () => {
    const many = Array.from({ length: 60 }, (_, i) => row({ cells: [`k${i}`, "x", "y", "z"], oldLine: i + 2, newLine: i + 2 }));
    renderWithProviders(<TableCompareView compare={compare({ rows: many, keyColumn: -1, rowsAdded: 1, rowsRemoved: 0, rowsChanged: 0, rowsMoved: 0, columnsAdded: 0, columnsRemoved: 0, columns: [{ name: "", oldName: "", old: 0, new: 0 }] })} />);
    expect(screen.getByRole("checkbox", { name: "Only changed rows" })).toBeChecked();
    expect(screen.getByText("No rows changed.")).toBeInTheDocument();
    expect(screen.getByText("Rows matched by content")).toBeInTheDocument();
    expect(screen.getByLabelText("Table summary")).toHaveTextContent("+1 row");
    expect(screen.getByRole("columnheader", { name: "A" })).toBeInTheDocument();
  });

  it("reports unchanged, empty and unreadable tables", () => {
    const { unmount } = renderWithProviders(<TableCompareView compare={compare({ rows: [], rowsAdded: 0, rowsRemoved: 0, rowsChanged: 0, rowsMoved: 0, columnsAdded: 0, columnsRemoved: 0, keyColumn: 3 })} />);
    expect(screen.getByText("No cell changes")).toBeInTheDocument();
    expect(screen.getByText("The table is empty.")).toBeInTheDocument();
    unmount();
    renderWithProviders(<TableCompareView compare={compare({ rows: [], columns: [], error: "invalid table: not UTF-8 text", rowsAdded: 0, rowsRemoved: 0, rowsChanged: 0, rowsMoved: 0, columnsAdded: 0, columnsRemoved: 0 })} />);
    expect(screen.getByText("This file cannot be compared as a table: invalid table: not UTF-8 text.")).toBeInTheDocument();
    expect(screen.queryByText("No cell changes")).not.toBeInTheDocument();
  });

  it("names columns past Z like a spreadsheet", () => {
    const columns = Array.from({ length: 28 }, (_, i) => ({ name: "", oldName: "", old: i, new: i }));
    renderWithProviders(<TableCompareView compare={compare({ columns, rows: [] })} />);
    expect(screen.getByRole("columnheader", { name: "AB" })).toBeInTheDocument();
  });
});

describe("TableDiff", () => {
  it("loads the table and switches to the text diff", async () => {
    api.tableDiff.mockResolvedValue(compare());
    renderWithProviders(<DiffViewer root="/work/game" diff={diff()} layout="unified" onLayout={() => {}} />);
    expect(screen.getByText("Comparing table…")).toBeInTheDocument();
    expect(await screen.findByRole("table", { name: "Table changes" })).toBeInTheDocument();
    expect(api.tableDiff).toHaveBeenCalledWith("/work/game", { source: "snapshot", path: "loc/strings.csv" }, { source: "working", path: "loc/strings.csv" });
    expect(screen.getByRole("group", { name: "Diff layout" })).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Text" }));
    expect(await screen.findByRole("table", { name: "Diff of loc/strings.csv" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Table" }));
    expect(screen.getByRole("table", { name: "Table changes" })).toBeInTheDocument();
  });

  it("falls back to metadata on errors", async () => {
    api.tableDiff.mockRejectedValueOnce(new Error("boom"));
    const { unmount } = renderWithProviders(<TableDiff root="/r" diff={diff({ hunks: [] })} layout="unified" />);
    expect(await screen.findByText("The table could not be read: boom")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Table view" })).not.toBeInTheDocument();
    unmount();
    api.tableDiff.mockResolvedValueOnce(null);
    renderWithProviders(<TableDiff root="/r" diff={diff()} layout="unified" />);
    expect(await screen.findByText("There is no table preview for this file.")).toBeInTheDocument();
  });

  it("is picked for text tables only", () => {
    expect(viewerFor(diff())).toBe(TableDiff);
    expect(viewerFor(diff({ hunksOmitted: true }))).toBe(TableDiff);
    expect(viewerFor(diff({ old: null, new: { path: "a.csv", mode: 420, size: 0, binary: false, hash: "" } }))).not.toBe(TableDiff);
    expect(viewerFor(diff({ new: { path: "a.csv", mode: 420, size: 9, binary: true, hash: "" } }))).toBe(TableDiff);
  });

  it("resizes columns per file and keeps rows aligned", async () => {
    localStorage.setItem("nipa.layout.cols.table:data/a.csv", JSON.stringify({ en: 240 }));
    renderWithProviders(<TableCompareView compare={compare()} storageKey="table:data/a.csv" />);
    const header = screen.getByRole("columnheader", { name: /^en/ }).parentElement!;
    expect(header).toHaveStyle({ width: "240px" });
    const firstRow = screen.getAllByRole("row")[1];
    expect(within(firstRow).getAllByRole("cell")[1]).toHaveStyle({ width: "240px" });

    screen.getByRole("separator", { name: "Resize key column" }).focus();
    await userEvent.keyboard("{ArrowLeft}");
    expect(screen.getByRole("columnheader", { name: "key" }).parentElement).toHaveStyle({ width: "144px" });
    expect(JSON.parse(localStorage.getItem("nipa.layout.cols.table:data/a.csv")!)).toEqual({ en: 240, key: 144 });
  });

  it("keeps duplicate header names apart", async () => {
    const columns = [
      { name: "value", oldName: "value", old: 0, new: 0 },
      { name: "value", oldName: "value", old: 1, new: 1 },
      { name: "", oldName: "", old: 2, new: 2 },
    ];
    expect(columnIds(columns)).toEqual(["value", "value #2", "C"]);
    renderWithProviders(<TableCompareView compare={compare({ columns, rows: [row({ cells: ["1", "2", "3"] })], keyColumn: -1 })} storageKey="table:dup.csv" />);
    screen.getByRole("separator", { name: "Resize value #2 column" }).focus();
    await userEvent.keyboard("{ArrowRight}");
    const [first, second] = screen.getAllByRole("columnheader", { name: "value" }).map((h) => h.parentElement);
    expect(first).toHaveStyle({ width: "160px" });
    expect(second).toHaveStyle({ width: "176px" });
    expect(screen.getByRole("separator", { name: "Resize value column" })).toBeInTheDocument();
    expect(JSON.parse(localStorage.getItem("nipa.layout.cols.table:dup.csv")!)).toEqual({ "value #2": 176 });
  });
});
