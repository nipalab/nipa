import { describe, expect, it } from "vitest";
import en from "./locales/en.json";
import id from "./locales/id.json";
import ja from "./locales/ja.json";
import ko from "./locales/ko.json";
import zhCN from "./locales/zh-CN.json";

const catalogs: Record<string, Record<string, string>> = { id, ja, "zh-CN": zhCN, ko };
const PLURAL = /_(zero|one|two|few|many|other)$/;
const base = (key: string) => key.replace(PLURAL, "");
const tokens = (text: string) => (text.match(/\{\{[^}]+\}\}|<\/?\d+>|\{file\}|\{line\}/g) ?? []).sort().join("|");

describe("catalogs", () => {
  const english = new Set(Object.keys(en).map(base));

  for (const [language, catalog] of Object.entries(catalogs)) {
    describe(language, () => {
      it("has every key of the English catalog and nothing else", () => {
        const keys = new Set(Object.keys(catalog).map(base));
        expect([...english].filter((k) => !keys.has(k))).toEqual([]);
        expect([...keys].filter((k) => !english.has(k))).toEqual([]);
      });

      it("keeps the placeholders and tags of the English text", () => {
        const wrong = Object.keys(catalog).filter((key) => {
          const source = (en as Record<string, string>)[key] ?? (en as Record<string, string>)[`${base(key)}_other`];
          return tokens(catalog[key]) !== tokens(source);
        });
        expect(wrong).toEqual([]);
      });

      it("has no empty text", () => {
        expect(Object.entries(catalog).filter(([, text]) => text.trim() === "")).toEqual([]);
      });
    });
  }

  it("gives plural English keys both forms", () => {
    const forms = Object.keys(en).filter((k) => PLURAL.test(k));
    for (const key of forms.filter((k) => k.endsWith("_one"))) expect(en).toHaveProperty(`${base(key)}_other`);
  });
});
