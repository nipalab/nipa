import type { ConflictFile, ConflictSet, ConflictTexts } from "@/api/desktop";
import { translate } from "@/i18n";

export const START = "<<<<<<< ours\n";
export const MID = "=======\n";
export const END = ">>>>>>> theirs\n";

export type BlockPick = "ours" | "theirs" | "ours-theirs" | "theirs-ours";

export type Segment = { kind: "same"; text: string } | { kind: "conflict"; ours: string; theirs: string };

export function parseConflicts(text: string): Segment[] {
  const out: Segment[] = [];
  const lines = text.split(/(?<=\n)/);
  let same = "";
  let i = 0;
  while (i < lines.length) {
    if (lines[i] !== START) {
      same += lines[i];
      i++;
      continue;
    }
    const mid = lines.indexOf(MID, i + 1);
    const end = mid < 0 ? -1 : lines.indexOf(END, mid + 1);
    if (end < 0) {
      same += lines.slice(i).join("");
      break;
    }
    if (same) out.push({ kind: "same", text: same });
    same = "";
    out.push({ kind: "conflict", ours: lines.slice(i + 1, mid).join(""), theirs: lines.slice(mid + 1, end).join("") });
    i = end + 1;
  }
  if (same) out.push({ kind: "same", text: same });
  return out;
}

export function hasMarkers(text: string): boolean {
  return [START, END].some((m) => text.startsWith(m) || text.includes("\n" + m));
}

const withNewline = (s: string) => (s === "" || s.endsWith("\n") ? s : s + "\n");

export function pickText(ours: string, theirs: string, pick: BlockPick): string {
  switch (pick) {
    case "ours":
      return ours;
    case "theirs":
      return theirs;
    case "ours-theirs":
      return withNewline(ours) + theirs;
    case "theirs-ours":
      return withNewline(theirs) + ours;
  }
}

export function compose(segments: Segment[], picks: (BlockPick | undefined)[]): string {
  let n = 0;
  return segments
    .map((s) => {
      if (s.kind === "same") return s.text;
      const pick = picks[n++];
      return pick ? pickText(s.ours, s.theirs, pick) : START + s.ours + MID + s.theirs + END;
    })
    .join("");
}

export function conflictCount(segments: Segment[]): number {
  return segments.filter((s) => s.kind === "conflict").length;
}

// segmentsFor builds the editable blocks: the working file's marker regions,
// or one whole-file block when both sides added the file independently.
export function segmentsFor(texts: ConflictTexts, kind: string): Segment[] {
  const parsed = parseConflicts(texts.working.text);
  if (kind !== "add_add" || conflictCount(parsed) > 0) return parsed;
  if (texts.ours.text !== texts.theirs.text && !texts.ours.missing && !texts.theirs.missing && texts.working.text === texts.ours.text) {
    return [{ kind: "conflict", ours: texts.ours.text, theirs: texts.theirs.text }];
  }
  return parsed;
}

export function editsText(file: ConflictFile): boolean {
  return !file.binary && (file.kind === "text" || file.kind === "add_add");
}

// mergesScene tells whether a conflicted file is a Godot text scene or
// resource the daemon can merge node by node.
export function mergesScene(file: ConflictFile): boolean {
  return editsText(file) && /\.(tscn|tres)$/i.test(file.path);
}

export function kindLabel(kind: string): string {
  switch (kind) {
    case "text":
      return translate("conflicts.bothChanged");
    case "binary":
      return translate("conflicts.bothChangedBinary");
    case "add_add":
      return translate("conflicts.bothAdded");
    case "modify_delete":
      return translate("conflicts.deletedOnTheirSide");
    case "delete_modify":
      return translate("conflicts.deletedOnYourSide");
    default:
      return kind;
  }
}

export type Choice = { choice: "ours" | "theirs"; label: string; hint: string };

// choices names the two whole-file outcomes in the words of the conflict.
export function choices(file: ConflictFile, set: Pick<ConflictSet, "ours" | "theirs" | "op">): [Choice, Choice] {
  const ours = set.ours.label || "yours";
  const theirs = set.theirs.label || "theirs";
  if (file.kind === "modify_delete") {
    return [
      { choice: "ours", label: translate("conflicts.keepTheFile"), hint: translate("conflicts.keepTheVersionOn", { ours }) },
      { choice: "theirs", label: translate("conflicts.deleteIt"), hint: translate("conflicts.deletedIt", { theirs }) },
    ];
  }
  if (file.kind === "delete_modify") {
    return [
      { choice: "ours", label: translate("conflicts.keepItDeleted"), hint: translate("conflicts.deletedIt2", { ours }) },
      { choice: "theirs", label: translate("conflicts.restoreTheirs"), hint: translate("conflicts.takeTheVersionFrom", { theirs }) },
    ];
  }
  return [
    { choice: "ours", label: translate("conflicts.takeMine"), hint: translate("conflicts.keepTheVersionOn", { ours }) },
    { choice: "theirs", label: translate("conflicts.takeTheirs"), hint: translate("conflicts.useTheVersionFrom", { theirs }) },
  ];
}

export function unresolved(set: Pick<ConflictSet, "files">): number {
  return set.files.filter((f) => !f.resolved).length;
}
