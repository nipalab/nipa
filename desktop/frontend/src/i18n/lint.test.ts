import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import { describe, expect, it } from "vitest";

const ATTRS = new Set(["title", "placeholder", "aria-label", "alt", "label", "description", "aria-description"]);
const ALLOWED = new Set(["Hz", "kHz", "EV", "Nipa", "LUFS", "dBFS", "px", "x", "RGB", "RGBA", "HDR", "GLB", "glTF", "PNG"]);

function components(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return components(p);
    return p.endsWith(".tsx") && !p.includes(".test.") ? [p] : [];
  });
}

function hardCoded(file: string): string[] {
  const src = fs.readFileSync(file, "utf8");
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const found: string[] = [];
  const visit = (n: ts.Node) => {
    if (ts.isJsxText(n)) {
      const text = n.text.replace(/\s+/g, " ").trim();
      if (/[A-Za-z]{2}/.test(text) && !ALLOWED.has(text)) found.push(`${sf.getLineAndCharacterOfPosition(n.getStart()).line + 1} ${text}`);
    }
    if (ts.isJsxAttribute(n) && ATTRS.has(n.name.getText()) && n.initializer && ts.isStringLiteral(n.initializer)) {
      if (/[A-Za-z]{2}/.test(n.initializer.text)) found.push(n.initializer.text);
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return found;
}

describe("localization lint", () => {
  it("has no hard-coded text in components", () => {
    const root = path.resolve(__dirname, "../components");
    const bad = components(root).flatMap((f) => hardCoded(f).map((t) => `${path.relative(root, f)}:${t}`));
    expect(bad).toEqual([]);
  });
});

describe("translation keys", () => {
  it("only uses keys the English catalog defines", () => {
    const en = JSON.parse(fs.readFileSync(path.resolve(__dirname, "locales/en.json"), "utf8")) as Record<string, string>;
    const base = (k: string) => k.replace(/_(one|other|zero|few|many|two)$/, "");
    const known = new Set(Object.keys(en).map(base));
    const used: string[] = [];
    const walk = (dir: string) => {
      for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name);
        if (e.isDirectory()) {
          if (e.name !== "locales") walk(p);
        } else if (/\.tsx?$/.test(p) && !p.includes(".test.")) {
          const src = fs.readFileSync(p, "utf8");
          for (const m of src.matchAll(/(?:\bt|translate)\(\s*"([^"]+)"|i18nKey="([^"]+)"/g)) used.push(m[1] ?? m[2]);
        }
      }
    };
    walk(path.resolve(__dirname, ".."));
    const dynamic = (k: string) => k.startsWith("commandPalette.section.") || k.startsWith("noun.");
    expect(used.filter((k) => !known.has(k) && !dynamic(k))).toEqual([]);
  });
});
