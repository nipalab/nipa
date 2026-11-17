
import { translate } from "@/i18n";// Every keyboard shortcut of the app lives here: the palette, the "?" sheet
// and the handlers all read this map, so a key is bound in exactly one place.

export type ShortcutId =
  | "palette"
  | "help"
  | "sidebar"
  | "settings"
  | "tab"
  | "update"
  | "focusMessage"
  | "push"
  | "refresh"
  | "stage"
  | "next"
  | "previous"
  | "zoomIn"
  | "zoomOut"
  | "zoomReset";

// mod is Ctrl, or Cmd on macOS.
export type Chord = { key: string; mod?: boolean; shift?: boolean; alt?: boolean };

export type ShortcutGroup = "General" | "Repository" | "Changes";

export type Shortcut = {
  id: ShortcutId;
  label: string;
  group: ShortcutGroup;
  chords: Chord[];
  // whileTyping keeps the shortcut active in text fields; only chords with a
  // modifier should set it.
  whileTyping?: boolean;
  // inModals keeps the shortcut active while a modal dialog is open.
  inModals?: boolean;
  // display overrides the rendered keys, e.g. for the 1-9 range.
  display?: string[];
};

const digits = Array.from({ length: 9 }, (_, i) => String(i + 1));

export const shortcuts: Shortcut[] = [
  { id: "palette", label: translate("shortcuts.searchAndRunCommands"), group: "General", chords: [{ key: "k", mod: true }], whileTyping: true },
  { id: "help", label: translate("shortcuts.showKeyboardShortcuts"), group: "General", chords: [{ key: "?" }] },
  { id: "sidebar", label: translate("shortcuts.showOrHideTheSidebar"), group: "General", chords: [{ key: "b", mod: true }], whileTyping: true },
  { id: "settings", label: translate("shortcuts.openSettings"), group: "General", chords: [{ key: ",", mod: true }], whileTyping: true },
  { id: "zoomIn", label: translate("shortcuts.zoomIn"), group: "General", chords: [{ key: "=", mod: true }, { key: "+", mod: true }], whileTyping: true, inModals: true, display: ["Mod", "+"] },
  { id: "zoomOut", label: translate("shortcuts.zoomOut"), group: "General", chords: [{ key: "-", mod: true }], whileTyping: true, inModals: true },
  { id: "zoomReset", label: translate("shortcuts.resetZoom"), group: "General", chords: [{ key: "0", mod: true }], whileTyping: true, inModals: true },
  {
    id: "tab",
    label: translate("shortcuts.goToTab19"),
    group: "Repository",
    chords: digits.map((key) => ({ key, mod: true })),
    whileTyping: true,
    display: ["Mod", "1–9"],
  },
  { id: "update", label: translate("shortcuts.updateGetTheLatestVersion"), group: "Repository", chords: [{ key: "u", mod: true, shift: true }], whileTyping: true },
  { id: "refresh", label: translate("shortcuts.refresh"), group: "Repository", chords: [{ key: "r", mod: true }, { key: "F5" }], whileTyping: true },
  { id: "focusMessage", label: translate("shortcuts.writeThePushMessage"), group: "Changes", chords: [{ key: "m", mod: true, shift: true }], whileTyping: true },
  { id: "push", label: translate("shortcuts.pushInThePushMessage"), group: "Changes", chords: [{ key: "Enter", mod: true }], whileTyping: true },
  { id: "stage", label: translate("shortcuts.stageOrUnstageTheSelected"), group: "Changes", chords: [{ key: " " }] },
  { id: "next", label: translate("shortcuts.nextFile"), group: "Changes", chords: [{ key: "j" }, { key: "ArrowDown" }] },
  { id: "previous", label: translate("shortcuts.previousFile"), group: "Changes", chords: [{ key: "k" }, { key: "ArrowUp" }] },
];

export const shortcutGroups: ShortcutGroup[] = ["General", "Repository", "Changes"];

export const isMac = (): boolean => typeof navigator !== "undefined" && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent);

type KeyLike = Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "shiftKey" | "altKey">;

// Symbols such as "?" need Shift on most layouts, so Shift is not compared
// for them.
const isSymbol = (key: string) => key.length === 1 && !/[a-z0-9 ]/i.test(key);

export function chordMatches(chord: Chord, e: KeyLike): boolean {
  const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  if (key !== chord.key) return false;
  if ((e.ctrlKey || e.metaKey) !== !!chord.mod) return false;
  if (e.altKey !== !!chord.alt) return false;
  return isSymbol(chord.key) || e.shiftKey === !!chord.shift;
}

const editableInputs = new Set(["", "text", "search", "email", "password", "url", "tel", "number", "date", "time", "datetime-local", "month", "week"]);

// isEditable is true for targets that take typed characters.
export function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true;
  return target instanceof HTMLInputElement && editableInputs.has(target.type);
}

const controlRoles = "button,a[href],input,select,textarea,summary,[role=button],[role=checkbox],[role=switch],[role=radio],[role=tab],[role=slider],[role=menuitem],[role=option],[role=listbox],[role=tree],[role=grid]";

// isControl is true for targets that already use Space or the arrow keys.
export function isControl(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest(controlRoles);
}

const bareNavigation = new Set([" ", "ArrowDown", "ArrowUp"]);

export type ShortcutMatch = { id: ShortcutId; index: number };

// matchShortcut finds the shortcut an event triggers, if any. Plain keys never
// fire in text fields, Space and arrows never steal from a focused control,
// and nothing fires while an IME composes, nor under a modal dialog unless
// the shortcut is inModals.
export function matchShortcut(e: KeyboardEvent, list: readonly Shortcut[] = shortcuts): ShortcutMatch | null {
  if (e.isComposing || e.defaultPrevented) return null;
  const modal = typeof document !== "undefined" && !!document.querySelector("[aria-modal='true']");
  for (const s of list) {
    const index = s.chords.findIndex((c) => chordMatches(c, e));
    if (index < 0) continue;
    if (modal && !s.inModals) return null;
    const chord = s.chords[index];
    if (!s.whileTyping && isEditable(e.target)) return null;
    if (!chord.mod && bareNavigation.has(chord.key) && isControl(e.target)) return null;
    return { id: s.id, index };
  }
  return null;
}

export function shortcut(id: ShortcutId): Shortcut {
  const s = shortcuts.find((x) => x.id === id);
  if (!s) throw new Error(`unknown shortcut ${id}`);
  return s;
}

const keyNames: Record<string, string> = { " ": "Space", ArrowDown: "↓", ArrowUp: "↑", Enter: "Enter" };

export function chordKeys(chord: Chord, mac = isMac()): string[] {
  const keys: string[] = [];
  if (chord.mod) keys.push(mac ? "⌘" : "Ctrl");
  if (chord.alt) keys.push(mac ? "⌥" : "Alt");
  if (chord.shift) keys.push(mac ? "⇧" : "Shift");
  keys.push(keyNames[chord.key] ?? (chord.key.length === 1 ? chord.key.toUpperCase() : chord.key));
  return keys;
}

// shortcutKeys renders each alternative chord of a shortcut as key caps.
export function shortcutKeys(s: Shortcut, mac = isMac()): string[][] {
  if (s.display) return [s.display.map((k) => (k === "Mod" ? (mac ? "⌘" : "Ctrl") : k))];
  return s.chords.map((c) => chordKeys(c, mac));
}

// shortcutHint is the first chord as one string, for titles and the palette.
export function shortcutHint(id: ShortcutId, mac = isMac()): string {
  return shortcutKeys(shortcut(id), mac)[0].join(mac ? "" : "+");
}
