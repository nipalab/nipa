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
  sheets: [],
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
    expect(api.tableDiff).toHaveBeenCalledWith("/work/game", { source: "snapshot", path: "loc/strings.csv" }, { source: "working", path: "loc/strings.csv" }, "");
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

describe("table filters", () => {
  it("filters rows by text and column and hides unchanged columns", async () => {
    renderWithProviders(<TableCompareView compare={compare()} />);
    const table = screen.getByRole("table", { name: "Table changes" });
    const header = within(table).getAllByRole("row")[0];
    expect(within(header).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Row", "key", "en(was english)1", "de", "fr"]);
    expect(screen.getByTitle("1 changed cell in this column")).toBeInTheDocument();

    await userEvent.type(screen.getByRole("searchbox", { name: "Filter rows" }), "hello");
    expect(within(table).getAllByRole("row").slice(1).map((r) => r.getAttribute("data-status"))).toEqual(["changed"]);
    expect(screen.getByText("1 of 5 rows")).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Filter column" }), "de");
    expect(screen.getByText("No rows match the filter.")).toBeInTheDocument();
    await userEvent.clear(screen.getByRole("searchbox", { name: "Filter rows" }));
    await userEvent.type(screen.getByRole("searchbox", { name: "Filter rows" }), "nein");
    expect(within(table).getAllByRole("row")).toHaveLength(2);

    await userEvent.click(screen.getByRole("checkbox", { name: "Changed columns only" }));
    expect(within(within(table).getAllByRole("row")[0]).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Row", "key", "en(was english)1", "de", "fr"]);
  });

  it("keeps the key column first and sticky, and drops unchanged columns", async () => {
    const columns = [
      { name: "note", oldName: "note", old: 0, new: 0 },
      { name: "id", oldName: "id", old: 1, new: 1 },
      { name: "hp", oldName: "hp", old: 2, new: 2 },
    ];
    const rows = [row({ cells: ["a", "1", "10"], old: ["a", "1", "8"], changed: [2], status: "changed" }), row({ cells: ["b", "2", "5"], status: "added", oldLine: 0 })];
    renderWithProviders(<TableCompareView compare={compare({ columns, rows, keyColumn: 1 })} />);
    const table = screen.getByRole("table", { name: "Table changes" });
    const names = () => within(within(table).getAllByRole("row")[0]).getAllByRole("columnheader").map((h) => h.textContent);
    expect(names()).toEqual(["Row", "id", "note", "hp1"]);
    expect(screen.getByRole("columnheader", { name: "id" }).parentElement).toHaveClass("sticky");
    const cells = within(within(table).getAllByRole("row")[2]).getAllByRole("cell");
    expect(cells[0]).toHaveTextContent("2");
    expect(cells[0]).toHaveClass("sticky");
    expect(cells[1]).not.toHaveClass("sticky");
    await userEvent.click(screen.getByRole("checkbox", { name: "Changed columns only" }));
    expect(names()).toEqual(["Row", "id", "hp1"]);
  });

  it("jumps between changes", async () => {
    renderWithProviders(<TableCompareView compare={compare()} />);
    expect(screen.getByText("4 changes")).toBeInTheDocument();
    const next = screen.getByRole("button", { name: "Next change" });
    const current = () => screen.getByRole("table", { name: "Table changes" }).querySelector("[aria-current]")?.getAttribute("data-status");
    await userEvent.click(next);
    expect(screen.getByText("1 of 4 changes")).toBeInTheDocument();
    expect(current()).toBe("changed");
    await userEvent.click(next);
    await userEvent.click(next);
    expect(screen.getByText("3 of 4 changes")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Previous change" }));
    await userEvent.click(screen.getByRole("button", { name: "Previous change" }));
    await userEvent.click(screen.getByRole("button", { name: "Previous change" }));
    expect(screen.getByText("4 of 4 changes")).toBeInTheDocument();
    expect(current()).toBe("added");

    await userEvent.type(screen.getByRole("searchbox", { name: "Filter rows" }), "yes");
    expect(screen.getByText("No changes")).toBeInTheDocument();
    await userEvent.click(next);
    expect(current()).toBeUndefined();
  });
});

describe("workbook sheets", () => {
  const sheets = [
    { name: "Weapons", status: "changed", rowsAdded: 1, rowsRemoved: 0, rowsChanged: 2, cellsChanged: 3 },
    { name: "Notes", status: "same", rowsAdded: 0, rowsRemoved: 0, rowsChanged: 0, cellsChanged: 0 },
    { name: "Loot", status: "added", rowsAdded: 2, rowsRemoved: 0, rowsChanged: 0, cellsChanged: 0 },
    { name: "Old", status: "removed", rowsAdded: 0, rowsRemoved: 1, rowsChanged: 0, cellsChanged: 0 },
    { name: "Cols", status: "changed", rowsAdded: 0, rowsRemoved: 0, rowsChanged: 0, cellsChanged: 0 },
  ];
  const book = (sheet: string) => compare({ sheet, sheets, delimiter: "" });

  it("shows sheet tabs with change markers and switches sheets", async () => {
    api.tableDiff.mockImplementation((_root, _old, _next, sheet: string) => Promise.resolve(book(sheet || "Weapons")));
    const xlsx = diff({ path: "data/balance.xlsx", hunks: null, old: { path: "data/balance.xlsx", mode: 420, size: 900, binary: true, hash: "a" }, new: { path: "data/balance.xlsx", mode: 420, size: 990, binary: true, hash: "b" } });
    renderWithProviders(<TableDiff root="/r" diff={xlsx} layout="unified" />);
    const tabs = await screen.findByRole("tablist", { name: "Sheets" });
    const tab = (name: RegExp) => within(tabs).getByRole("tab", { name });
    expect(tab(/^Weapons/)).toHaveAttribute("aria-selected", "true");
    expect(tab(/^Weapons/)).toHaveAttribute("title", "Weapons: +1 row, 2 rows changed (3 cells)");
    expect(within(tab(/^Weapons/)).getByLabelText("Sheet changed")).toBeInTheDocument();
    expect(within(tab(/^Notes/)).queryByLabelText(/^Sheet/)).not.toBeInTheDocument();
    expect(tab(/^Notes/)).toHaveAttribute("title", "Notes: no changes");
    expect(tab(/^Loot/)).toHaveAttribute("title", "Loot: sheet added");
    expect(tab(/^Old/)).toHaveClass("line-through");
    expect(tab(/^Cols/)).toHaveAttribute("title", "Cols: columns changed");
    expect(screen.queryByRole("group", { name: "Table view" })).not.toBeInTheDocument();

    await userEvent.click(tab(/^Loot/));
    await vi.waitFor(() => expect(tab(/^Loot/)).toHaveAttribute("aria-selected", "true"));
    expect(api.tableDiff).toHaveBeenLastCalledWith("/r", expect.anything(), expect.anything(), "Loot");
  });

  it("labels an unnamed sheet", () => {
    renderWithProviders(<TableCompareView compare={compare({ sheet: "", sheets: [{ ...sheets[1], name: "" }] })} />);
    expect(screen.getByRole("tab", { name: "Sheet" })).toBeInTheDocument();
  });
});

