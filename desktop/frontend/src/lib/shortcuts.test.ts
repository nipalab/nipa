import { afterEach, describe, expect, it } from "vitest";
import { chordKeys, chordMatches, isControl, isEditable, matchShortcut, shortcut, shortcutHint, shortcutKeys, shortcuts, type Chord } from "./shortcuts";

const key = (k: string, init: KeyboardEventInit = {}, target?: HTMLElement) => {
  const e = new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...init });
  if (target) Object.defineProperty(e, "target", { value: target });
  return e;
};

const mount = <T extends HTMLElement>(el: T): T => {
  document.body.appendChild(el);
  return el;
};

afterEach(() => {
  document.body.innerHTML = "";
});

const signature = (c: Chord) => `${c.mod ? "mod+" : ""}${c.alt ? "alt+" : ""}${c.shift ? "shift+" : ""}${c.key}`;

describe("shortcut map", () => {
  it("binds every chord to exactly one shortcut", () => {
    const seen = new Map<string, string>();
    for (const s of shortcuts) {
      for (const c of s.chords) {
        const sig = signature(c);
        expect(seen.get(sig), `${sig} is bound to ${seen.get(sig)} and ${s.id}`).toBeUndefined();
        seen.set(sig, s.id);
      }
    }
  });

  it("only keeps shortcuts with a modifier (or function keys) active while typing", () => {
    for (const s of shortcuts.filter((x) => x.whileTyping)) {
      for (const c of s.chords) expect(c.mod || /^F\d+$/.test(c.key), `${s.id} ${signature(c)}`).toBeTruthy();
    }
  });

  it("never binds a plain character that typing would need, unless it is ignored in text fields", () => {
    for (const s of shortcuts) {
      for (const c of s.chords) if (!c.mod && c.key.length === 1) expect(s.whileTyping).toBeFalsy();
    }
  });
});

describe("chordMatches", () => {
  it("compares the key and modifiers", () => {
    expect(chordMatches({ key: "k", mod: true }, key("K", { ctrlKey: true }))).toBe(true);
    expect(chordMatches({ key: "k", mod: true }, key("k", { metaKey: true }))).toBe(true);
    expect(chordMatches({ key: "k", mod: true }, key("k"))).toBe(false);
    expect(chordMatches({ key: "k" }, key("k", { ctrlKey: true }))).toBe(false);
    expect(chordMatches({ key: "k" }, key("k", { altKey: true }))).toBe(false);
    expect(chordMatches({ key: "u", mod: true, shift: true }, key("U", { ctrlKey: true, shiftKey: true }))).toBe(true);
    expect(chordMatches({ key: "u", mod: true, shift: true }, key("u", { ctrlKey: true }))).toBe(false);
  });

  it("ignores Shift for symbols", () => {
    expect(chordMatches({ key: "?" }, key("?", { shiftKey: true }))).toBe(true);
    expect(chordMatches({ key: "?" }, key("?"))).toBe(true);
  });
});

describe("targets", () => {
  it("detects text fields", () => {
    expect(isEditable(mount(document.createElement("textarea")))).toBe(true);
    expect(isEditable(mount(document.createElement("select")))).toBe(true);
    expect(isEditable(mount(document.createElement("input")))).toBe(true);
    const box = document.createElement("input");
    box.type = "checkbox";
    expect(isEditable(mount(box))).toBe(false);
    const div = mount(document.createElement("div"));
    div.contentEditable = "true";
    Object.defineProperty(div, "isContentEditable", { value: true });
    expect(isEditable(div)).toBe(true);
    expect(isEditable(document.body)).toBe(false);
    expect(isEditable(null)).toBe(false);
  });

  it("detects controls including their children", () => {
    const button = mount(document.createElement("button"));
    const icon = button.appendChild(document.createElement("span"));
    expect(isControl(icon)).toBe(true);
    const list = mount(document.createElement("div"));
    list.setAttribute("role", "listbox");
    expect(isControl(list)).toBe(true);
    expect(isControl(document.body)).toBe(false);
    expect(isControl(null)).toBe(false);
  });
});

describe("matchShortcut", () => {
  it("finds shortcuts and the chord index", () => {
    expect(matchShortcut(key("k", { ctrlKey: true }))).toEqual({ id: "palette", index: 0 });
    expect(matchShortcut(key("3", { metaKey: true }))).toEqual({ id: "tab", index: 2 });
    expect(matchShortcut(key("F5"))).toEqual({ id: "refresh", index: 1 });
    expect(matchShortcut(key("ArrowDown", {}, document.body))).toEqual({ id: "next", index: 1 });
    expect(matchShortcut(key("x"))).toBeNull();
  });

  it("keeps plain keys out of text fields but lets modifier shortcuts through", () => {
    const input = mount(document.createElement("input"));
    expect(matchShortcut(key("j", {}, input))).toBeNull();
    expect(matchShortcut(key("?", { shiftKey: true }, input))).toBeNull();
    expect(matchShortcut(key(" ", {}, input))).toBeNull();
    expect(matchShortcut(key("k", { ctrlKey: true }, input))).toEqual({ id: "palette", index: 0 });
    const area = mount(document.createElement("textarea"));
    expect(matchShortcut(key("m", { ctrlKey: true, shiftKey: true }, area))?.id).toBe("focusMessage");
  });

  it("leaves Space and arrows to focused controls but not letters", () => {
    const button = mount(document.createElement("button"));
    expect(matchShortcut(key(" ", {}, button))).toBeNull();
    expect(matchShortcut(key("ArrowUp", {}, button))).toBeNull();
    expect(matchShortcut(key("j", {}, button))).toEqual({ id: "next", index: 0 });
    expect(matchShortcut(key(" ", {}, document.body))?.id).toBe("stage");
  });

  it("does nothing while composing, after another handler, or under a modal", () => {
    expect(matchShortcut(key("k", { ctrlKey: true, isComposing: true }))).toBeNull();
    const handled = key("k", { ctrlKey: true });
    handled.preventDefault();
    expect(matchShortcut(handled)).toBeNull();
    const dialog = mount(document.createElement("div"));
    dialog.setAttribute("aria-modal", "true");
    expect(matchShortcut(key("k", { ctrlKey: true }))).toBeNull();
  });
});

describe("rendering", () => {
  it("renders keys for macOS and others", () => {
    expect(chordKeys({ key: "u", mod: true, shift: true, alt: true }, false)).toEqual(["Ctrl", "Alt", "Shift", "U"]);
    expect(chordKeys({ key: "u", mod: true, shift: true, alt: true }, true)).toEqual(["⌘", "⌥", "⇧", "U"]);
    expect(chordKeys({ key: " " }, false)).toEqual(["Space"]);
    expect(chordKeys({ key: "F5" }, false)).toEqual(["F5"]);
    expect(shortcutKeys(shortcut("tab"), false)).toEqual([["Ctrl", "1–9"]]);
    expect(shortcutKeys(shortcut("tab"), true)).toEqual([["⌘", "1–9"]]);
    expect(shortcutKeys(shortcut("next"), false)).toEqual([["J"], ["↓"]]);
    expect(shortcutHint("palette", false)).toBe("Ctrl+K");
    expect(shortcutHint("palette", true)).toBe("⌘K");
  });

  it("rejects unknown ids", () => {
    expect(() => shortcut("nope" as never)).toThrow();
  });
});
